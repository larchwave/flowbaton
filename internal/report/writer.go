package report

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync"
)

const (
	ArtifactKindCommands          = "commands"
	ArtifactKindFailureScreenshot = "failure-screenshot"
	ArtifactKindJUnit             = "junit"
	ArtifactKindManifest          = "manifest"
	ManifestSchemaVersion         = "flowbaton.artifacts/v1"
)

// Writer owns report files beneath one injected output root. It records a file
// only after the write succeeds, and all returned paths are relative to root.
type Writer struct {
	root string

	mu      sync.Mutex
	created map[string]Artifact
}

// NewWriter prepares an output-root-bound artifact writer.
func NewWriter(root string) (*Writer, error) {
	if root == "" {
		return nil, fmt.Errorf("report output root is empty")
	}

	absoluteRoot, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve report output root: %w", err)
	}
	if err := os.MkdirAll(absoluteRoot, 0o755); err != nil {
		return nil, fmt.Errorf("create report output root: %w", err)
	}
	info, err := os.Stat(absoluteRoot)
	if err != nil {
		return nil, fmt.Errorf("inspect report output root: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("report output root %q is not a directory", root)
	}

	return &Writer{
		root:    absoluteRoot,
		created: make(map[string]Artifact),
	}, nil
}

// SanitizeFlowName returns a portable ASCII path component for flow artifacts.
func SanitizeFlowName(name string) string {
	var output strings.Builder
	output.Grow(len(name))
	lastWasHyphen := false

	for _, r := range name {
		if asciiLetterOrDigit(r) || r == '-' || r == '_' {
			output.WriteRune(r)
			lastWasHyphen = r == '-'
			continue
		}
		if output.Len() > 0 && !lastWasHyphen {
			output.WriteByte('-')
			lastWasHyphen = true
		}
	}

	sanitized := strings.Trim(output.String(), "-_")
	if sanitized == "" {
		return "flow"
	}
	return sanitized
}

// WriteCommands creates <sanitized-flow>/commands.json.
func (writer *Writer) WriteCommands(flow FlowResult) (Artifact, error) {
	data, err := MarshalCommands(flow)
	if err != nil {
		return Artifact{}, err
	}
	relativePath := path.Join(SanitizeFlowName(flow.Name), "commands.json")
	return writer.writeArtifact(ArtifactKindCommands, relativePath, data)
}

// WriteFailureScreenshot writes the provided bytes unchanged.
func (writer *Writer) WriteFailureScreenshot(flowName string, sequence int64, data []byte) (Artifact, error) {
	if sequence < 0 {
		return Artifact{}, fmt.Errorf("failure screenshot sequence must be non-negative")
	}
	relativePath := path.Join(
		SanitizeFlowName(flowName),
		fmt.Sprintf("failure-%06d.png", sequence),
	)
	return writer.writeArtifact(ArtifactKindFailureScreenshot, relativePath, data)
}

// WriteJUnit creates the suite-wide junit.xml artifact.
func (writer *Writer) WriteJUnit(options JUnitOptions, flows []FlowResult) (Artifact, error) {
	data, err := MarshalJUnit(options, flows)
	if err != nil {
		return Artifact{}, err
	}
	return writer.writeArtifact(ArtifactKindJUnit, "junit.xml", data)
}

// WriteArtifact writes arbitrary writer-owned bytes beneath the output root
// and records the resulting artifact for the manifest.
func (writer *Writer) WriteArtifact(kind, relativePath string, data []byte) (Artifact, error) {
	return writer.writeArtifact(kind, relativePath, data)
}

// RegisterArtifact records an already-finalized regular file beneath the
// output root without rewriting it.
func (writer *Writer) RegisterArtifact(kind, relativePath string) (Artifact, error) {
	if writer == nil {
		return Artifact{}, fmt.Errorf("report writer is nil")
	}
	if err := validateArtifactIdentity(kind, relativePath); err != nil {
		return Artifact{}, err
	}

	writer.mu.Lock()
	defer writer.mu.Unlock()
	if err := writer.validateExistingRegularFileLocked(relativePath); err != nil {
		return Artifact{}, fmt.Errorf("register %s artifact: %w", kind, err)
	}
	artifact := Artifact{Kind: kind, Path: relativePath}
	writer.created[relativePath] = artifact
	return artifact, nil
}

// WriteManifest creates artifacts.json from writer-created files that still
// exist as regular files. The manifest intentionally does not list itself.
func (writer *Writer) WriteManifest() (Artifact, error) {
	if writer == nil {
		return Artifact{}, fmt.Errorf("report writer is nil")
	}

	writer.mu.Lock()
	defer writer.mu.Unlock()

	artifacts := make([]Artifact, 0, len(writer.created))
	for _, artifact := range writer.created {
		info, err := os.Lstat(writer.fullPath(artifact.Path))
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		artifacts = append(artifacts, artifact)
	}
	artifacts = cloneAndSortArtifacts(artifacts)

	var output bytes.Buffer
	encoder := json.NewEncoder(&output)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(struct {
		SchemaVersion string     `json:"schemaVersion"`
		Artifacts     []Artifact `json:"artifacts"`
	}{
		SchemaVersion: ManifestSchemaVersion,
		Artifacts:     artifacts,
	}); err != nil {
		return Artifact{}, fmt.Errorf("marshal artifact manifest: %w", err)
	}

	manifest := Artifact{Kind: ArtifactKindManifest, Path: "artifacts.json"}
	if err := writer.writeFileLocked(manifest.Path, output.Bytes()); err != nil {
		return Artifact{}, fmt.Errorf("write artifact manifest: %w", err)
	}
	return manifest, nil
}

func (writer *Writer) writeArtifact(kind, relativePath string, data []byte) (Artifact, error) {
	if writer == nil {
		return Artifact{}, fmt.Errorf("report writer is nil")
	}
	if err := validateArtifactIdentity(kind, relativePath); err != nil {
		return Artifact{}, err
	}

	writer.mu.Lock()
	defer writer.mu.Unlock()

	if err := writer.writeFileLocked(relativePath, data); err != nil {
		return Artifact{}, fmt.Errorf("write %s artifact: %w", kind, err)
	}
	artifact := Artifact{Kind: kind, Path: relativePath}
	writer.created[relativePath] = artifact
	return artifact, nil
}

func (writer *Writer) writeFileLocked(relativePath string, data []byte) error {
	filename := writer.fullPath(relativePath)
	if err := writer.prepareWriteTargetLocked(relativePath); err != nil {
		return err
	}
	return os.WriteFile(filename, data, 0o644)
}

func (writer *Writer) prepareWriteTargetLocked(relativePath string) error {
	segments := strings.Split(relativePath, "/")
	current := writer.root
	for _, segment := range segments[:len(segments)-1] {
		current = filepath.Join(current, segment)
		info, err := os.Lstat(current)
		if os.IsNotExist(err) {
			if err := os.Mkdir(current, 0o755); err != nil {
				return fmt.Errorf("create artifact directory %q: %w", segment, err)
			}
			continue
		}
		if err != nil {
			return fmt.Errorf("inspect artifact directory %q: %w", segment, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("artifact path traverses symlink %q", segment)
		}
		if !info.IsDir() {
			return fmt.Errorf("artifact path component %q is not a directory", segment)
		}
	}

	filename := writer.fullPath(relativePath)
	info, err := os.Lstat(filename)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("inspect artifact target: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("artifact target is a symlink")
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("artifact target is not a regular file")
	}
	return nil
}

func (writer *Writer) validateExistingRegularFileLocked(relativePath string) error {
	segments := strings.Split(relativePath, "/")
	current := writer.root
	for index, segment := range segments {
		current = filepath.Join(current, segment)
		info, err := os.Lstat(current)
		if err != nil {
			return fmt.Errorf("inspect artifact path %q: %w", segment, err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("artifact path contains symlink %q", segment)
		}
		if index < len(segments)-1 {
			if !info.IsDir() {
				return fmt.Errorf("artifact path component %q is not a directory", segment)
			}
			continue
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("artifact target is not a regular file")
		}
	}
	return nil
}

func validateArtifactIdentity(kind, relativePath string) error {
	if strings.TrimSpace(kind) == "" {
		return fmt.Errorf("artifact kind is blank")
	}
	if strings.TrimSpace(relativePath) == "" {
		return fmt.Errorf("artifact path is blank")
	}
	if strings.Contains(relativePath, `\`) {
		return fmt.Errorf("artifact path %q must use slash separators", relativePath)
	}
	if path.IsAbs(relativePath) || hasWindowsVolumePrefix(relativePath) {
		return fmt.Errorf("artifact path %q must be relative", relativePath)
	}
	if relativePath == "." || path.Clean(relativePath) != relativePath {
		return fmt.Errorf("artifact path %q must be clean", relativePath)
	}
	for _, segment := range strings.Split(relativePath, "/") {
		if segment == "." || segment == ".." || segment == "" {
			return fmt.Errorf("artifact path %q contains an invalid segment", relativePath)
		}
	}
	return nil
}

func hasWindowsVolumePrefix(value string) bool {
	return len(value) >= 2 && asciiLetterOrDigit(rune(value[0])) && value[1] == ':'
}

func (writer *Writer) fullPath(relativePath string) string {
	return filepath.Join(writer.root, filepath.FromSlash(relativePath))
}

func asciiLetterOrDigit(r rune) bool {
	return r >= 'a' && r <= 'z' ||
		r >= 'A' && r <= 'Z' ||
		r >= '0' && r <= '9'
}
