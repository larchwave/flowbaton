package cli

// The two web-only test options, spec 03 §1: `--headless` and
// `--screen-size WxH`. They configure the browser used by the run.

import (
	"os"
	"strconv"
	"strings"

	"github.com/larchwave/flowbaton/internal/web"
)

// webChromeOptions turns the operator's flags into a browser configuration.
//
// The screen size uses the CLI grammar documented by `flowbaton test --help`:
// "(Web only) Set the size of the headless browser. Use
// the format {Width}x{Height}. Usage is --screen-size 1920x1080". Chrome's own
// flag is comma-separated, so the value is translated rather than forwarded.
func webChromeOptions(options TestOptions, port int) (web.ChromeOptions, error) {
	windowSize := ""
	if options.ScreenSize != "" {
		translated, err := chromeWindowSize(options.ScreenSize)
		if err != nil {
			return web.ChromeOptions{}, err
		}
		windowSize = translated
	}
	return web.ChromeOptions{
		// FLOWBATON_CHROME names the binary. It is an environment variable
		// rather than a flag because the CLI surface has no binary-path option.
		Binary:     os.Getenv("FLOWBATON_CHROME"),
		Port:       port,
		Headless:   options.Headless,
		WindowSize: windowSize,
	}, nil
}

// chromeWindowSize parses {Width}x{Height} into Chrome's "width,height".
//
// A malformed value is refused rather than dropped: a run at the wrong viewport
// is not obviously wrong from the outside, and a responsive layout genuinely
// shows different elements at different widths.
func chromeWindowSize(value string) (string, error) {
	width, height, found := strings.Cut(value, "x")
	if !found {
		return "", usageErrorf(
			"--screen-size %q must be {Width}x{Height}, for example 1920x1080", value)
	}
	parsedWidth, widthErr := strconv.Atoi(width)
	parsedHeight, heightErr := strconv.Atoi(height)
	if widthErr != nil || heightErr != nil || parsedWidth <= 0 || parsedHeight <= 0 {
		return "", usageErrorf(
			"--screen-size %q must be {Width}x{Height} in positive pixels, for example 1920x1080", value)
	}
	return strconv.Itoa(parsedWidth) + "," + strconv.Itoa(parsedHeight), nil
}
