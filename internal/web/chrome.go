package web

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"time"
)

// Launching the browser the driver attaches to.
//
// Spec 02-device-drivers.md §4 fixes the flag set. Two of them are load-bearing
// rather than cosmetic: --remote-allow-origins=* is what lets a websocket
// handshake attach at all (Chrome rejects an unexpected Origin otherwise), and
// a dedicated --user-data-dir keeps a run out of the operator's real profile,
// where it would inherit their cookies and sessions.

type ChromeOptions struct {
	// Binary is the browser executable; empty picks the platform default.
	Binary string
	// Port is the DevTools port to listen on.
	Port int
	// UserDataDir isolates the run's profile.
	UserDataDir string
	// Headless selects --headless=new.
	Headless bool
	// WindowSize is the "width,height" the spec passes through.
	WindowSize string
}

func chromeArguments(options ChromeOptions) []string {
	arguments := []string{
		"--remote-debugging-port=" + strconv.Itoa(options.Port),
		// Without this the websocket handshake is refused by Origin check.
		"--remote-allow-origins=*",
		"--disable-search-engine-choice-screen",
		"--lang=en",
		"--password-store=basic",
		"--no-first-run",
		"--no-default-browser-check",
		"--user-data-dir=" + options.UserDataDir,
	}
	if options.Headless {
		arguments = append(arguments, "--headless=new")
	}
	if options.WindowSize != "" {
		arguments = append(arguments, "--window-size="+options.WindowSize)
	}
	// Opening a blank page explicitly means exactly one page target exists; a
	// fresh profile can otherwise come up on a welcome page, and the driver
	// would attach to that instead.
	return append(arguments, "about:blank")
}

// DefaultChromeBinary is where the browser lives on this platform.
func DefaultChromeBinary() string {
	switch runtime.GOOS {
	case "darwin":
		return "/Applications/Google Chrome.app/Contents/MacOS/Google Chrome"
	case "windows":
		return `C:\Program Files\Google\Chrome\Application\chrome.exe`
	default:
		return "google-chrome"
	}
}

// Chrome is a browser this process started and is responsible for stopping.
type Chrome struct {
	command *exec.Cmd
	// profile is removed on Stop when this launcher created it.
	profile string
	BaseURL string
}

// LaunchChrome starts the browser and waits for DevTools to answer.
//
// It waits for the endpoint rather than returning immediately: a driver that
// attaches before the browser is listening fails with a connection error that
// reads like a missing browser rather than a race.
func LaunchChrome(ctx context.Context, options ChromeOptions) (*Chrome, error) {
	binary := options.Binary
	if binary == "" {
		binary = DefaultChromeBinary()
	}
	if options.Port <= 0 {
		return nil, fmt.Errorf("web chrome: a devtools port is required")
	}
	created := ""
	if options.UserDataDir == "" {
		directory, err := os.MkdirTemp("", "flowbaton-chrome-")
		if err != nil {
			return nil, fmt.Errorf("web chrome: creating a profile directory: %w", err)
		}
		options.UserDataDir, created = directory, directory
	}

	command := exec.CommandContext(ctx, binary, chromeArguments(options)...)
	if err := command.Start(); err != nil {
		if created != "" {
			_ = os.RemoveAll(created)
		}
		return nil, fmt.Errorf("web chrome: starting %s: %w", binary, err)
	}
	chrome := &Chrome{
		command: command,
		profile: created,
		BaseURL: "http://127.0.0.1:" + strconv.Itoa(options.Port),
	}
	if err := waitForDevTools(ctx, chrome.BaseURL, 20*time.Second); err != nil {
		_ = chrome.Stop()
		return nil, err
	}
	return chrome, nil
}

func waitForDevTools(ctx context.Context, baseURL string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: time.Second}
	var last error
	for time.Now().Before(deadline) {
		if _, err := discoverPageEndpoint(ctx, baseURL, client); err == nil {
			return nil
		} else {
			last = err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	return fmt.Errorf("web chrome: devtools did not come up on %s: %w", baseURL, last)
}

// Stop ends the browser and removes a profile this launcher created.
//
// The profile is removed whether or not there is still a process to kill: a
// browser that already exited on its own still leaves its profile behind, and
// that is disk the run is responsible for.
func (chrome *Chrome) Stop() error {
	if chrome == nil {
		return nil
	}
	if chrome.command != nil && chrome.command.Process != nil {
		_ = chrome.command.Process.Kill()
		_, _ = chrome.command.Process.Wait()
	}
	if chrome.profile != "" {
		_ = os.RemoveAll(chrome.profile)
		chrome.profile = ""
	}
	chrome.command = nil
	return nil
}
