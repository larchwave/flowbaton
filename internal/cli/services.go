package cli

import (
	"context"
	"crypto/rand"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strings"

	"github.com/nohavewho/flowbaton/internal/device"
	"github.com/nohavewho/flowbaton/internal/engine"
	"github.com/nohavewho/flowbaton/internal/imagecheck"
	"github.com/nohavewho/flowbaton/internal/js"
)

// Production implementations of the host services injected into the engine.
//
// Two of them touch the filesystem on behalf of an authored flow. The path in
// a flow file is external input, so both confine themselves to one directory
// and refuse anything that walks out of it.

// ArtifactSink writes run artifacts, into one of two directories.
//
// A name the flow author wrote resolves against the process working directory: a
// flow saying `takeScreenshot: settings` leaves ./settings.png where the
// operator ran from, whichever directory the flow itself lives in. Everything
// the author did not name — the automatic failure captures — is the run's own
// bookkeeping and stays in the run directory.
type ArtifactSink struct {
	// directory holds the run's own captures.
	directory string
	// authoredDirectory holds artifacts whose name came from the flow.
	authoredDirectory string
}

// authoredArtifactKind is the capture an author asked for by name. It matches
// the engine's own label for an explicit takeScreenshot.
const authoredArtifactKind = "screenshot"

func NewArtifactSink(directory, authoredDirectory string) *ArtifactSink {
	if authoredDirectory == "" {
		authoredDirectory = directory
	}
	return &ArtifactSink{directory: directory, authoredDirectory: authoredDirectory}
}

// root picks the directory a request belongs in.
func (sink *ArtifactSink) root(kind string) string {
	if kind == authoredArtifactKind {
		return sink.authoredDirectory
	}
	return sink.directory
}

func (sink *ArtifactSink) Write(
	_ context.Context,
	request engine.ArtifactWriteRequest,
) (engine.ArtifactWriteResult, error) {
	root := sink.root(request.Kind)
	name, err := safeArtifactName(request.SuggestedName)
	if err != nil {
		return engine.ArtifactWriteResult{}, err
	}
	// MkdirAll on the name's own directory, not just the root: an authored
	// name may carry a sub-path.
	if err := os.MkdirAll(filepath.Dir(filepath.Join(root, name)), 0o755); err != nil {
		return engine.ArtifactWriteResult{}, fmt.Errorf("artifact sink: %w", err)
	}

	// O_EXCL rather than a stat-then-create: two screenshots in one flow
	// suggest the same name, and an overwrite would leave the run reporting
	// two artifacts while holding one.
	path := filepath.Join(root, name)
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	for attempt := 1; os.IsExist(err) && attempt < 1000; attempt++ {
		path = filepath.Join(root, disambiguate(name, attempt))
		file, err = os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	}
	if err != nil {
		return engine.ArtifactWriteResult{}, fmt.Errorf("artifact sink: %w", err)
	}
	defer func() { _ = file.Close() }()

	written, err := file.Write(request.Data)
	if err != nil {
		return engine.ArtifactWriteResult{}, fmt.Errorf("artifact sink: writing %s: %w", path, err)
	}
	return engine.ArtifactWriteResult{
		Artifact: device.Artifact{
			Kind:     request.Metadata["kind"],
			Path:     path,
			Metadata: request.Metadata,
		},
		BytesWritten: int64(written),
	}, nil
}

// disambiguate inserts a counter before the extension, so shot.png becomes
// shot-1.png rather than shot.png-1.
func disambiguate(name string, attempt int) string {
	extension := filepath.Ext(name)
	return fmt.Sprintf("%s-%d%s", strings.TrimSuffix(name, extension), attempt, extension)
}

// safeArtifactName accepts a relative sub-path and refuses anything that would
// leave the directory it is joined to.
//
// Authored sub-paths such as `shots/deep` are supported. Parent traversal is
// refused because a flow file is external input and may not escape the output
// directory.
func safeArtifactName(suggested string) (string, error) {
	name := strings.TrimSpace(suggested)
	if name == "" {
		return "", fmt.Errorf("artifact sink: an artifact name is required")
	}
	if filepath.IsAbs(name) {
		return "", fmt.Errorf(
			"artifact sink: artifact name %q must be relative, not an absolute path", suggested)
	}
	// Clean first, so `shots/../../escaped` is judged by where it lands rather
	// than by how it is spelled.
	cleaned := filepath.Clean(name)
	if cleaned == "." || cleaned == ".." ||
		cleaned == string(filepath.Separator) ||
		strings.HasPrefix(cleaned, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf(
			"artifact sink: artifact name %q must stay inside the output directory", suggested)
	}
	return cleaned, nil
}

// ResourceReader reads flow resources confined to one base directory.
type ResourceReader struct {
	baseDirectory string
}

func NewResourceReader(baseDirectory string) *ResourceReader {
	return &ResourceReader{baseDirectory: baseDirectory}
}

func (reader *ResourceReader) Read(
	_ context.Context,
	request engine.ResourceReadRequest,
) (engine.ResourceReadResult, error) {
	path, err := reader.resolve(request.Path)
	if err != nil {
		return engine.ResourceReadResult{}, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return engine.ResourceReadResult{}, fmt.Errorf("resource reader: %w", err)
	}
	return engine.ResourceReadResult{
		Data:     data,
		Metadata: map[string]string{"path": path},
	}, nil
}

// resolve confines a flow-authored path to the base directory. The check
// is made on the evaluated paths, so a symlink pointing out of the tree is
// caught too — a relative-prefix check alone would not see it.
func (reader *ResourceReader) resolve(requested string) (string, error) {
	if strings.TrimSpace(requested) == "" {
		return "", fmt.Errorf("resource reader: a resource path is required")
	}
	base, err := filepath.EvalSymlinks(reader.baseDirectory)
	if err != nil {
		return "", fmt.Errorf("resource reader: %w", err)
	}
	candidate := requested
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(base, candidate)
	}
	resolved, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		// A missing file cannot escape anything, so it is reported as missing
		// rather than as an escape — but only after the lexical check, so a
		// missing path outside the tree still reads as an escape.
		if !withinDirectory(base, filepath.Clean(candidate)) {
			return "", escapeError(requested)
		}
		return "", fmt.Errorf("resource reader: %w", err)
	}
	if !withinDirectory(base, resolved) {
		return "", escapeError(requested)
	}
	return resolved, nil
}

func escapeError(requested string) error {
	return fmt.Errorf(
		"resource reader: path %q resolves outside the flow directory", requested)
}

func withinDirectory(base, candidate string) bool {
	relative, err := filepath.Rel(base, candidate)
	if err != nil {
		return false
	}
	return relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

// ImageChecker forwards to the shared check rather than repeating it.
type ImageChecker struct{}

func (ImageChecker) Check(
	_ context.Context,
	request engine.ImageCheckRequest,
) (imagecheck.Result, error) {
	return imagecheck.Check(request.Expected, request.Actual, request.Crop)
}

// CryptoRandom is the process random source, shared by the JS faker binding
// and the random-input commands so there is one notion of randomness.
type CryptoRandom struct{}

func (CryptoRandom) Intn(n int) int {
	if n <= 0 {
		return 0
	}
	value, err := rand.Int(rand.Reader, big.NewInt(int64(n)))
	if err != nil {
		// crypto/rand failing is not a condition a test run can proceed
		// through meaningfully, but returning 0 keeps it deterministic rather
		// than panicking inside a flow.
		return 0
	}
	return int(value.Int64())
}

func (CryptoRandom) Read(p []byte) (int, error) {
	return rand.Read(p)
}

// InputGenerator supplies values for the inputRandom* commands.
type InputGenerator struct {
	random js.RandomSource
}

// NewInputGenerator builds a generator over the process random source.
func NewInputGenerator() *InputGenerator {
	return &InputGenerator{random: CryptoRandom{}}
}

const (
	textAlphabet   = "abcdefghijklmnopqrstuvwxyz"
	digitAlphabet  = "0123456789"
	defaultInputLn = 8
)

// The word lists behind the named kinds.
//
// These local word lists provide values of the documented shape. A flow may
// inspect the generated text, so changing them is a visible behavior change.
var (
	personNames  = []string{"Avery", "Casey", "Devon", "Emerson", "Harper", "Jordan", "Morgan", "Quinn", "Reese", "Sawyer"}
	cityNames    = []string{"Bergen", "Cordoba", "Dresden", "Galway", "Kyoto", "Lisbon", "Nairobi", "Porto", "Tallinn", "Valencia"}
	countryNames = []string{"Argentina", "Croatia", "Denmark", "Estonia", "Iceland", "Kenya", "Morocco", "Norway", "Portugal", "Uruguay"}
	colorNames   = []string{"amber", "cerulean", "crimson", "indigo", "ivory", "magenta", "olive", "scarlet", "teal", "violet"}
)

func (generator *InputGenerator) Generate(
	_ context.Context,
	request engine.InputRequest,
) (string, error) {
	length := request.Length
	if length <= 0 {
		length = defaultInputLn
	}
	switch request.Kind {
	case engine.InputText:
		return generator.randomString(textAlphabet, length), nil
	case engine.InputNumber:
		return generator.randomString(digitAlphabet, length), nil
	case engine.InputEmail:
		// The length applies to the local part; a fixed domain keeps the value
		// a valid address at every requested length.
		return generator.randomString(textAlphabet, length) + "@example.com", nil
	case engine.InputPersonName:
		return generator.pick(personNames), nil
	case engine.InputCityName:
		return generator.pick(cityNames), nil
	case engine.InputCountryName:
		return generator.pick(countryNames), nil
	case engine.InputColorName:
		return generator.pick(colorNames), nil
	default:
		return "", fmt.Errorf("input generator: unknown input kind %q", request.Kind)
	}
}

func (generator *InputGenerator) randomString(alphabet string, length int) string {
	characters := make([]byte, length)
	for index := range characters {
		characters[index] = alphabet[generator.random.Intn(len(alphabet))]
	}
	return string(characters)
}

func (generator *InputGenerator) pick(values []string) string {
	return values[generator.random.Intn(len(values))]
}
