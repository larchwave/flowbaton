package web

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"
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

// Chrome can start successfully at the OS level and then exit before opening
// its DevTools port. That startup failure is the useful diagnosis; waiting for
// the port timeout and returning only "connection refused" hides it.
func TestLaunchChromeReportsAnEarlyProcessExit(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	started := time.Now()
	_, err := LaunchChrome(ctx, ChromeOptions{
		Binary:      os.Args[0],
		Port:        freePort(t),
		UserDataDir: t.TempDir(),
		Headless:    true,
	})
	if err == nil {
		t.Fatal("LaunchChrome accepted a browser process that exited immediately")
	}
	if elapsed := time.Since(started); elapsed >= 3*time.Second {
		t.Fatalf("LaunchChrome waited %v for a process that had already exited: %v", elapsed, err)
	}
	for _, want := range []string{"browser process exited", "flag provided but not defined"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("LaunchChrome error = %q, want %q", err, want)
		}
	}
}

// A Chrome child can inherit stdout or stderr and survive the browser process.
// The launcher must not wait indefinitely for that descendant to close a pipe.
func TestLaunchChromeBoundsInheritedOutputPipes(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the inherited-pipe fixture is a POSIX shell script")
	}

	directory := t.TempDir()
	script := filepath.Join(directory, "chrome-fixture")
	childPID := filepath.Join(directory, "child.pid")
	t.Setenv("FLOWBATON_TEST_CHILD_PID", childPID)
	if err := os.WriteFile(script, []byte(`#!/bin/sh
sleep 30 &
printf '%s\n' "$!" > "$FLOWBATON_TEST_CHILD_PID"
printf 'chrome startup failed\n' >&2
exit 9
`), 0o700); err != nil {
		t.Fatalf("writing browser fixture: %v", err)
	}
	t.Cleanup(func() {
		contents, err := os.ReadFile(childPID)
		if err != nil {
			return
		}
		pid, err := strconv.Atoi(strings.TrimSpace(string(contents)))
		if err != nil {
			return
		}
		if process, err := os.FindProcess(pid); err == nil {
			_ = process.Kill()
		}
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	started := time.Now()
	_, err := LaunchChrome(ctx, ChromeOptions{
		Binary:      script,
		Port:        freePort(t),
		UserDataDir: directory,
		Headless:    true,
	})
	if err == nil {
		t.Fatal("LaunchChrome accepted a browser process that exited immediately")
	}
	if elapsed := time.Since(started); elapsed >= 5*time.Second {
		t.Fatalf("LaunchChrome waited %v for an inherited output pipe: %v", elapsed, err)
	}
	if !strings.Contains(err.Error(), "chrome startup failed") {
		t.Fatalf("LaunchChrome error = %q, want captured browser output", err)
	}
}
