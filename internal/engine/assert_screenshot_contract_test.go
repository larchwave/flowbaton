package engine

import (
	"strings"
	"testing"

	"github.com/larchwave/flowbaton/internal/model"
)

// assertScreenshot resolves extensionless expected-image paths to PNG, accepts
// the thresholdPercentage field, and interprets that value as the minimum
// similarity percentage. The default is 95%.

func TestAssertScreenshotResolvesTheExpectedToAPNG(t *testing.T) {
	t.Parallel()

	reader := &stubResourceReader{data: []byte("expected-png")}
	checker := &stubImageChecker{ratio: 0}
	if _, err := runAssertScreenshot(t,
		assertScreenshotServices{reader: reader, checker: checker},
		assertScreenshotCommand("expected/home", nil, nil), nil); err != nil {
		t.Fatalf("execute(assertScreenshot) error = %T %v", err, err)
	}
	if len(reader.requests) != 1 || reader.requests[0].Path != "expected/home.png" {
		t.Fatalf("read %#v, want expected/home.png", reader.requests)
	}
}

// A path the author already gave an extension is left alone: the contract
// accepts `expected/home.png` and does not go looking for home.png.png.
func TestAssertScreenshotLeavesAnAuthoredExtensionAlone(t *testing.T) {
	t.Parallel()

	reader := &stubResourceReader{data: []byte("expected-png")}
	checker := &stubImageChecker{ratio: 0}
	if _, err := runAssertScreenshot(t,
		assertScreenshotServices{reader: reader, checker: checker},
		assertScreenshotCommand("expected/home.png", nil, nil), nil); err != nil {
		t.Fatalf("execute(assertScreenshot) error = %T %v", err, err)
	}
	if len(reader.requests) != 1 || reader.requests[0].Path != "expected/home.png" {
		t.Fatalf("read %#v, want the authored path untouched", reader.requests)
	}
}

func TestAssertScreenshotAcceptsThresholdPercentage(t *testing.T) {
	t.Parallel()

	command := model.Command{
		Kind: model.CommandAssertScreenshot, Form: model.CommandFormObject,
		Arguments: map[string]any{"path": "expected/home", "thresholdPercentage": 90.0},
	}
	compiled, err := compileAssertScreenshot(command)
	if err != nil {
		t.Fatalf("compile(thresholdPercentage) error = %v", err)
	}
	payload, ok := compiled.(assertScreenshotCompiled)
	if !ok {
		t.Fatalf("compiled = %T, want assertScreenshotCompiled", compiled)
	}
	if payload.similarityPercent != 90 {
		t.Fatalf("similarity = %v, want 90", payload.similarityPercent)
	}
}

// `threshold` is not part of the authored contract.
func TestAssertScreenshotRefusesUnknownThresholdField(t *testing.T) {
	t.Parallel()

	command := model.Command{
		Kind: model.CommandAssertScreenshot, Form: model.CommandFormObject,
		Arguments: map[string]any{"path": "expected/home", "threshold": 90.0},
	}
	if _, err := compileAssertScreenshot(command); err == nil {
		t.Fatal("compile accepted `threshold`, which the contract refuses as an unknown property")
	}
}

// The number is the similarity the capture must reach.
func TestAssertScreenshotTreatsTheNumberAsRequiredSimilarity(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		name       string
		similarity float64
		difference float64
		wantErr    bool
	}{
		{name: "97.1% alike passes the contract default", similarity: 95, difference: 0.0286},
		{name: "97.1% alike fails an exact demand", similarity: 100, difference: 0.0286, wantErr: true},
		{name: "nothing alike passes a zero demand", similarity: 0, difference: 1},
		{name: "identical passes an exact demand", similarity: 100, difference: 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			threshold := test.similarity
			_, err := runAssertScreenshot(t,
				assertScreenshotServices{
					reader:  &stubResourceReader{data: []byte("expected-png")},
					checker: &stubImageChecker{ratio: test.difference},
				},
				assertScreenshotCommand("expected/home", &threshold, nil), nil)
			if test.wantErr && err == nil {
				t.Fatalf("similarity %v with %v%% different passed, want a failure",
					test.similarity, test.difference*100)
			}
			if !test.wantErr && err != nil {
				t.Fatalf("similarity %v with %v%% different failed: %v",
					test.similarity, test.difference*100, err)
			}
		})
	}
}

// A flow that omits the threshold uses the contract default of 95% similarity.
func TestAssertScreenshotUsesNinetyFivePercentByDefault(t *testing.T) {
	t.Parallel()

	// Three percent difference remains within the default.
	_, err := runAssertScreenshot(t,
		assertScreenshotServices{
			reader:  &stubResourceReader{data: []byte("expected-png")},
			checker: &stubImageChecker{ratio: 0.03},
		},
		assertScreenshotCommand("expected/home", nil, nil), nil)
	if err != nil {
		t.Fatalf("a 97%%-alike capture failed the default threshold: %v", err)
	}
}

// A failed screenshot assertion reports the achieved similarity so the
// operator can diagnose the threshold failure.
func TestAssertScreenshotFailureNamesTheSimilarityItReached(t *testing.T) {
	t.Parallel()

	threshold := 99.0
	_, err := runAssertScreenshot(t,
		assertScreenshotServices{
			reader:  &stubResourceReader{data: []byte("expected-png")},
			checker: &stubImageChecker{ratio: 0.075},
		},
		assertScreenshotCommand("expected/home", &threshold, nil), nil)
	if err == nil {
		t.Fatal("a 92.5%-alike capture passed a 99% demand")
	}
	for _, fragment := range []string{"92.5", "99"} {
		if !strings.Contains(err.Error(), fragment) {
			t.Fatalf("error = %q, want it to name %s", err.Error(), fragment)
		}
	}
}
