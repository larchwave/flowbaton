package cli

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/nohavewho/flowbaton/internal/engine"
)

// These tests cover the host services supplied to the engine as injected
// boundaries.
//
// Two of these deal with the filesystem on behalf of an authored flow, which
// makes them a trust boundary: the path in a flow file is external input.

func TestArtifactSinkWritesUnderItsDirectoryAndNamesTheFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	sink := NewArtifactSink(dir, dir)
	result, err := sink.Write(context.Background(), engine.ArtifactWriteRequest{
		SuggestedName: "shot.png",
		Data:          []byte("PNG-BYTES"),
		Metadata:      map[string]string{"kind": "screenshot"},
	})
	if err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if result.BytesWritten != int64(len("PNG-BYTES")) {
		t.Fatalf("BytesWritten = %d, want %d", result.BytesWritten, len("PNG-BYTES"))
	}
	contents, err := os.ReadFile(result.Artifact.Path)
	if err != nil {
		t.Fatalf("artifact not on disk: %v", err)
	}
	if string(contents) != "PNG-BYTES" {
		t.Fatalf("contents = %q", contents)
	}
	if filepath.Dir(result.Artifact.Path) != dir {
		t.Fatalf("wrote to %s, want it under %s", result.Artifact.Path, dir)
	}
}

func TestArtifactSinkKeepsAWrittenArtifactFromOverwritingAnEarlierOne(t *testing.T) {
	t.Parallel()

	// Two screenshots in one flow suggest the same name. Silently overwriting
	// would leave a run reporting two artifacts and holding one.
	dir := t.TempDir()
	sink := NewArtifactSink(dir, dir)
	first, err := sink.Write(context.Background(), engine.ArtifactWriteRequest{
		SuggestedName: "shot.png", Data: []byte("one")})
	if err != nil {
		t.Fatal(err)
	}
	second, err := sink.Write(context.Background(), engine.ArtifactWriteRequest{
		SuggestedName: "shot.png", Data: []byte("two")})
	if err != nil {
		t.Fatal(err)
	}
	if first.Artifact.Path == second.Artifact.Path {
		t.Fatalf("both writes landed on %s", first.Artifact.Path)
	}
	for path, want := range map[string]string{
		first.Artifact.Path: "one", second.Artifact.Path: "two",
	} {
		got, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Fatal(readErr)
		}
		if string(got) != want {
			t.Fatalf("%s = %q, want %q", path, got, want)
		}
	}
}

func TestArtifactSinkRefusesANameThatEscapesItsDirectory(t *testing.T) {
	t.Parallel()

	// The suggested name originates in an authored flow, which is external
	// input. A name that walks out of the output directory would let a flow
	// write anywhere the process can reach.
	dir := t.TempDir()
	sink := NewArtifactSink(dir, dir)
	for _, name := range []string{
		"../escape.png",
		"a/../../escape.png",
		"/etc/escape.png",
		"",
	} {
		_, err := sink.Write(context.Background(), engine.ArtifactWriteRequest{
			SuggestedName: name, Data: []byte("x")})
		if err == nil {
			t.Fatalf("Write(%q) was accepted; want a refusal", name)
		}
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("refused writes still created %v", entries)
	}
}

func TestResourceReaderResolvesRelativeToItsBaseDirectory(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "script.js"), "output.value = 1")

	result, err := NewResourceReader(dir).Read(
		context.Background(), engine.ResourceReadRequest{Path: "script.js"})
	if err != nil {
		t.Fatalf("Read() error = %v", err)
	}
	if string(result.Data) != "output.value = 1" {
		t.Fatalf("Data = %q", result.Data)
	}
}

func TestResourceReaderRefusesAPathThatEscapesItsBaseDirectory(t *testing.T) {
	t.Parallel()

	// Same trust boundary as the sink, in the other direction: a flow must not
	// be able to read arbitrary files off the host by authoring a path.
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "inside.txt"), "ok")

	for _, path := range []string{"../outside.txt", "a/../../outside.txt", ""} {
		if _, err := NewResourceReader(dir).Read(
			context.Background(), engine.ResourceReadRequest{Path: path}); err == nil {
			t.Fatalf("Read(%q) was accepted; want a refusal", path)
		}
	}
}

func TestResourceReaderRefusesAnEscapeToAFileThatActuallyExists(t *testing.T) {
	t.Parallel()

	// A negative control found the hole this closes. The test above uses paths
	// that do not exist, so every one of them was refused for being MISSING —
	// deleting the containment check entirely left it green. Only a real file
	// outside the tree reaches the check the reader exists for.
	outer := t.TempDir()
	secret := filepath.Join(outer, "secret.txt")
	writeFile(t, secret, "not for a flow to read")
	base := filepath.Join(outer, "flows")
	if err := os.Mkdir(base, 0o755); err != nil {
		t.Fatal(err)
	}

	reader := NewResourceReader(base)
	for _, path := range []string{"../secret.txt", secret} {
		if _, err := reader.Read(
			context.Background(), engine.ResourceReadRequest{Path: path}); err == nil {
			t.Fatalf("Read(%q) read a file outside the flow directory", path)
		}
	}
}

func TestResourceReaderRefusesASymlinkPointingOutOfTheTree(t *testing.T) {
	t.Parallel()

	// The reason the check uses RESOLVED paths. A symlink inside the tree
	// is a lexically innocent name, so a prefix check on the authored string
	// would let it through.
	outer := t.TempDir()
	secret := filepath.Join(outer, "secret.txt")
	writeFile(t, secret, "not for a flow to read")
	base := filepath.Join(outer, "flows")
	if err := os.Mkdir(base, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(secret, filepath.Join(base, "innocent.txt")); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	if _, err := NewResourceReader(base).Read(
		context.Background(), engine.ResourceReadRequest{Path: "innocent.txt"}); err == nil {
		t.Fatal("a symlink out of the tree was followed")
	}
}

func TestImageCheckerDelegatesToTheSharedCheck(t *testing.T) {
	t.Parallel()

	// Two identical 1x1 PNGs must check as no difference; the point is that
	// this boundary does not reimplement check, it forwards to the one
	// place that already has tests for it.
	pixel := onePixelPNG(t)
	result, err := ImageChecker{}.Check(context.Background(), engine.ImageCheckRequest{
		Expected: pixel, Actual: pixel,
	})
	if err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	if result.DifferentPixels() != 0 {
		t.Fatalf("DifferentPixels = %d, want 0 for identical images", result.DifferentPixels())
	}
	if result.TotalPixels() == 0 {
		t.Fatal("TotalPixels = 0; the check did not run")
	}
}

func TestImageCheckerForwardsTheCrop(t *testing.T) {
	t.Parallel()

	// A dropped crop would check the whole screen against a cropped
	// snapshot and report a difference that is not there.
	pixel := onePixelPNG(t)
	crop := image.Rect(0, 0, 5, 5)
	_, err := ImageChecker{}.Check(context.Background(), engine.ImageCheckRequest{
		Expected: pixel, Actual: pixel, Crop: &crop,
	})
	if err == nil {
		t.Fatal("a 5x5 crop of a 1x1 image was accepted; the crop was not forwarded")
	}
}

func TestInputGeneratorProducesEveryKindAtTheRequestedLength(t *testing.T) {
	t.Parallel()

	generator := NewInputGenerator()
	for _, kind := range []engine.InputKind{
		engine.InputText, engine.InputNumber, engine.InputEmail,
		engine.InputPersonName, engine.InputCityName,
		engine.InputCountryName, engine.InputColorName,
	} {
		value, err := generator.Generate(context.Background(), engine.InputRequest{Kind: kind, Length: 8})
		if err != nil {
			t.Fatalf("Generate(%s) error = %v", kind, err)
		}
		if value == "" {
			t.Fatalf("Generate(%s) returned an empty value", kind)
		}
	}
}

func TestGeneratedTextAndNumbersHonorTheRequestedLength(t *testing.T) {
	t.Parallel()

	// specs/01-core-engine.md:41 gives length 8 as the default for the
	// random-input commands, so a requested length has to be respected or the
	// value that reaches a length-limited field is the wrong size.
	generator := NewInputGenerator()
	for _, kind := range []engine.InputKind{engine.InputText, engine.InputNumber} {
		for _, length := range []int{1, 8, 32} {
			value, err := generator.Generate(
				context.Background(), engine.InputRequest{Kind: kind, Length: length})
			if err != nil {
				t.Fatal(err)
			}
			if len(value) != length {
				t.Fatalf("Generate(%s, %d) = %q, length %d", kind, length, value, len(value))
			}
		}
	}
}

func TestGeneratedNumbersAreOnlyDigits(t *testing.T) {
	t.Parallel()

	value, err := NewInputGenerator().Generate(
		context.Background(), engine.InputRequest{Kind: engine.InputNumber, Length: 16})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Trim(value, "0123456789") != "" {
		t.Fatalf("Generate(number) = %q, want digits only", value)
	}
}

func TestGeneratedEmailLooksLikeAnAddress(t *testing.T) {
	t.Parallel()

	value, err := NewInputGenerator().Generate(
		context.Background(), engine.InputRequest{Kind: engine.InputEmail, Length: 8})
	if err != nil {
		t.Fatal(err)
	}
	local, domain, found := strings.Cut(value, "@")
	if !found || local == "" || !strings.Contains(domain, ".") {
		t.Fatalf("Generate(email) = %q, want a local part and a dotted domain", value)
	}
}

func TestInputGeneratorRefusesAnUnknownKind(t *testing.T) {
	t.Parallel()

	if _, err := NewInputGenerator().Generate(
		context.Background(), engine.InputRequest{Kind: engine.InputKind("hologram"), Length: 8}); err == nil {
		t.Fatal("an unknown input kind was accepted")
	}
}

func TestGeneratedValuesDiffer(t *testing.T) {
	t.Parallel()

	// "Random" that returns a constant would make every inputRandomText in a
	// suite type the same string, which is exactly what the command exists to
	// avoid.
	generator := NewInputGenerator()
	seen := map[string]bool{}
	for range 32 {
		value, err := generator.Generate(
			context.Background(), engine.InputRequest{Kind: engine.InputText, Length: 12})
		if err != nil {
			t.Fatal(err)
		}
		seen[value] = true
	}
	if len(seen) < 2 {
		t.Fatalf("32 generated values produced %d distinct results", len(seen))
	}
}

func TestRecordingControllerRefusesRatherThanPretending(t *testing.T) {
	t.Parallel()

	// Screen recording needs a long-lived child process this layer has no way
	// to own, and the frozen Driver surface has no stop counterpart. Returning
	// success and no file would report a recording that does not exist.
	controller := UnsupportedRecordingController{}
	if err := controller.Start(context.Background(), engine.RecordingStartRequest{Name: "run"}); err == nil {
		t.Fatal("Start() succeeded; want an explicit refusal")
	}
	artifacts, err := controller.Stop(context.Background())
	if err == nil {
		t.Fatal("Stop() succeeded; want an explicit refusal")
	}
	if len(artifacts) != 0 {
		t.Fatalf("Stop() returned %v artifacts alongside its error", artifacts)
	}
}

// onePixelPNG encodes the smallest valid image, so a check test does not
// depend on a fixture file.
func onePixelPNG(t *testing.T) []byte {
	t.Helper()
	canvas := image.NewRGBA(image.Rect(0, 0, 1, 1))
	canvas.Set(0, 0, color.RGBA{R: 1, G: 2, B: 3, A: 255})
	var buffer bytes.Buffer
	if err := png.Encode(&buffer, canvas); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

// Authored screenshot destinations resolve against the process working
// directory. Nested destinations create their parent directories before write.
//
// Everything the author did not name keeps going to the run directory: the
// automatic failure captures are the run's own bookkeeping, not the flow's
// output, so they remain under the run directory.
func TestArtifactSinkPutsAuthoredScreenshotsWhereTheAuthorAsked(t *testing.T) {
	t.Parallel()

	runDirectory := t.TempDir()
	authored := t.TempDir()
	sink := NewArtifactSink(runDirectory, authored)

	result, err := sink.Write(context.Background(), engine.ArtifactWriteRequest{
		Kind: "screenshot", SuggestedName: "settings.png", Data: []byte("PNG"),
	})
	if err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	want := filepath.Join(authored, "settings.png")
	if result.Artifact.Path != want {
		t.Fatalf("path = %q, want %q", result.Artifact.Path, want)
	}
	if _, err := os.Stat(want); err != nil {
		t.Fatalf("the file the author named is not there: %v", err)
	}
}

func TestArtifactSinkCreatesTheSubdirectoryAnAuthoredNameAsksFor(t *testing.T) {
	t.Parallel()

	authored := t.TempDir()
	sink := NewArtifactSink(t.TempDir(), authored)

	result, err := sink.Write(context.Background(), engine.ArtifactWriteRequest{
		Kind: "screenshot", SuggestedName: filepath.Join("shots", "deep.png"), Data: []byte("PNG"),
	})
	if err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	want := filepath.Join(authored, "shots", "deep.png")
	if result.Artifact.Path != want {
		t.Fatalf("path = %q, want %q", result.Artifact.Path, want)
	}
	if _, err := os.Stat(want); err != nil {
		t.Fatalf("the nested file was not written: %v", err)
	}
}

// The run's own captures stay in the run directory because they are
// bookkeeping rather than authored flow output.
func TestArtifactSinkKeepsRunCapturesInTheRunDirectory(t *testing.T) {
	t.Parallel()

	runDirectory := t.TempDir()
	authored := t.TempDir()
	sink := NewArtifactSink(runDirectory, authored)

	result, err := sink.Write(context.Background(), engine.ArtifactWriteRequest{
		Kind: "failure-screenshot", SuggestedName: "failure-000002.png", Data: []byte("PNG"),
	})
	if err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if filepath.Dir(result.Artifact.Path) != runDirectory {
		t.Fatalf("path = %q, want it under the run directory %q", result.Artifact.Path, runDirectory)
	}
}

// A flow file is external input and cannot write outside its authorized output
// directory.
func TestArtifactSinkStillRefusesAnEscapingName(t *testing.T) {
	t.Parallel()

	authored := t.TempDir()
	sink := NewArtifactSink(t.TempDir(), authored)

	for _, name := range []string{
		filepath.Join("..", "escaped.png"),
		filepath.Join("shots", "..", "..", "escaped.png"),
		filepath.Join(t.TempDir(), "absolute.png"),
		"", "   ", ".", "..",
	} {
		if _, err := sink.Write(context.Background(), engine.ArtifactWriteRequest{
			Kind: "screenshot", SuggestedName: name, Data: []byte("PNG"),
		}); err == nil {
			t.Errorf("artifact name %q was accepted", name)
		}
	}
}
