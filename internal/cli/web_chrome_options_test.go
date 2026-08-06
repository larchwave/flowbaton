package cli

import (
	"errors"
	"strings"
	"testing"
)

// specs/03-cli-tooling.md:20 lists `--headless` and `--screen-size WxH (web)`
// as test options. These checks keep both options connected to ChromeOptions.
//
// The documented CLI grammar is:
// "(Web only) Set the size of the headless browser. Use the format
// {Width}x{Height}. Usage is --screen-size 1920x1080".
func TestWebChromeOptionsCarryTheDocumentedFlags(t *testing.T) {
	t.Parallel()

	got, err := webChromeOptions(
		TestOptions{Platform: "web", Headless: true, ScreenSize: "1920x1080"}, 9222)
	if err != nil {
		t.Fatalf("webChromeOptions() error = %v", err)
	}
	if !got.Headless {
		t.Error("--headless did not reach the browser")
	}
	// Chrome's own flag is comma-separated, so the WxH the operator types has
	// to be translated rather than passed through.
	if got.WindowSize != "1920,1080" {
		t.Errorf("WindowSize = %q, want 1920,1080", got.WindowSize)
	}
	if got.Port != 9222 {
		t.Errorf("Port = %d, want the shard's", got.Port)
	}
}

// The control: without the flags the browser is headed and sized by Chrome.
func TestWebChromeOptionsDefaultToAHeadedBrowser(t *testing.T) {
	t.Parallel()

	got, err := webChromeOptions(TestOptions{Platform: "web"}, 9222)
	if err != nil {
		t.Fatalf("webChromeOptions() error = %v", err)
	}
	if got.Headless || got.WindowSize != "" {
		t.Fatalf("options = %#v, want no headless and no window size", got)
	}
}

// A size that is not {Width}x{Height} is a typo, and a typo that is silently
// dropped runs the whole suite at the wrong viewport — where a responsive
// layout can genuinely show different elements.
func TestWebChromeOptionsRejectAMalformedScreenSize(t *testing.T) {
	t.Parallel()

	for _, size := range []string{"1920", "1920x", "x1080", "1920x1080x1", "1920,1080", "widexhigh", "0x1080", "-1x10"} {
		_, err := webChromeOptions(TestOptions{Platform: "web", ScreenSize: size}, 9222)
		if err == nil {
			t.Errorf("--screen-size %q was accepted", size)
			continue
		}
		var usage *UsageError
		if !errors.As(err, &usage) {
			t.Errorf("--screen-size %q error = %T %v, want *UsageError", size, err, err)
		}
		if !strings.Contains(err.Error(), size) {
			t.Errorf("error %q does not quote the value the operator typed", err)
		}
	}
}
