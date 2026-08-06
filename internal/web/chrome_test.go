package web

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"
)

// Spec 02-device-drivers.md §4 names the flags the browser must be launched
// with. Two of them are load-bearing rather than cosmetic:
// --remote-allow-origins=* is what lets the websocket handshake attach at all,
// and --user-data-dir is what keeps a run out of the operator's real profile.
func TestChromeArgumentsCarryTheSpecFlags(t *testing.T) {
	t.Parallel()

	arguments := chromeArguments(ChromeOptions{
		Port:        9222,
		UserDataDir: "/tmp/profile",
		Headless:    true,
		WindowSize:  "1024,768",
	})

	for _, required := range []string{
		"--remote-debugging-port=9222",
		"--remote-allow-origins=*",
		"--disable-search-engine-choice-screen",
		"--lang=en",
		"--password-store=basic",
		"--headless=new",
		"--window-size=1024,768",
		"--user-data-dir=/tmp/profile",
	} {
		if !slices.Contains(arguments, required) {
			t.Errorf("missing %q in %v", required, arguments)
		}
	}
}

// A headed run must not carry --headless=new, or every operator watching a run
// would see nothing.
func TestChromeArgumentsOmitHeadlessWhenHeaded(t *testing.T) {
	t.Parallel()

	arguments := chromeArguments(ChromeOptions{Port: 9333, UserDataDir: "/tmp/p"})
	for _, argument := range arguments {
		if strings.HasPrefix(argument, "--headless") {
			t.Fatalf("headed launch carries %q", argument)
		}
	}
	if !slices.Contains(arguments, "--remote-debugging-port=9333") {
		t.Fatalf("port flag missing: %v", arguments)
	}
}

// The first positional target is about:blank so the browser opens exactly one
// page target; without it a fresh profile can come up on a welcome page whose
// target the driver would then drive instead.
func TestChromeArgumentsOpenABlankPage(t *testing.T) {
	t.Parallel()

	arguments := chromeArguments(ChromeOptions{Port: 9222, UserDataDir: "/tmp/p"})
	if arguments[len(arguments)-1] != "about:blank" {
		t.Fatalf("last argument = %q, want about:blank", arguments[len(arguments)-1])
	}
}

// A driver that launches its own browser owns that browser's lifetime: Open
// starts it and attaches to whatever port it came up on, Close ends it. The
// CLI needs this — it is handed a flow, not a running DevTools endpoint — and
// a Close that forgets to stop the browser leaks a Chrome process and its
// profile directory for every run.
func TestLaunchingDriverStartsAndStopsItsOwnBrowser(t *testing.T) {
	t.Parallel()

	server := newCDPServer(t, func(string, json.RawMessage) (any, string) { return nil, "" })
	profile := t.TempDir()
	launched := 0
	driver := NewLaunchingDriver(ChromeOptions{Headless: true})
	driver.launch = func(context.Context, ChromeOptions) (*Chrome, error) {
		launched++
		return &Chrome{profile: profile, BaseURL: server.Server.URL}, nil
	}

	ctx := context.Background()
	if err := driver.Open(ctx); err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if launched != 1 || driver.baseURL != server.Server.URL {
		t.Fatalf("launched %d times, baseURL = %q", launched, driver.baseURL)
	}
	if err := driver.Close(ctx); err != nil {
		t.Fatalf("Close() error = %v", err)
	}
	// Stop removes the profile the launcher created; its absence is the
	// observable proof that Close reached the browser and not just the socket.
	if _, err := os.Stat(profile); !os.IsNotExist(err) {
		t.Fatalf("the profile directory survived Close: %v", err)
	}
	if driver.chrome != nil {
		t.Fatal("Close left the browser attached")
	}
}

// A browser that will not start must fail Open, not leave a driver that looks
// attached and fails on the first command with a socket error.
func TestLaunchingDriverReportsALaunchFailureFromOpen(t *testing.T) {
	t.Parallel()

	driver := NewLaunchingDriver(ChromeOptions{})
	driver.launch = func(context.Context, ChromeOptions) (*Chrome, error) {
		return nil, errors.New("no browser here")
	}
	if err := driver.Open(context.Background()); err == nil {
		t.Fatal("Open() accepted a browser that never started")
	}
}
