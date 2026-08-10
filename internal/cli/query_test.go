package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/larchwave/flowbaton/internal/device"
	"github.com/larchwave/flowbaton/internal/enginetest"
)

// query is spec 03's element finder: "which elements match this expression".
// The matching happens on the HOST, with the same matcher a flow uses — see
// query_match.go for why asking the device was the wrong question.

func queryRunnerReturning(nodes []device.TreeNode, err error) QueryRunner {
	return QueryRunner{
		Fetch: func(_ context.Context, _, _, _, _ string) ([]device.TreeNode, error) {
			return nodes, err
		},
	}
}

func runQuery(t *testing.T, runner QueryRunner, args ...string) (string, string, int) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := runner.Run(context.Background(), args, &stdout, &stderr)
	return stdout.String(), stderr.String(), code
}

func TestQueryPrintsMatchesAsJSONArray(t *testing.T) {
	t.Parallel()

	matches := []device.TreeNode{
		{Attributes: map[string]string{"text": "Login"}},
		{Attributes: map[string]string{"text": "Logout"}},
	}
	stdout, _, code := runQuery(t, queryRunnerReturning(matches, nil),
		"-p", "android", "--device", "emulator-5554", "Log")
	if code != ExitOK {
		t.Fatalf("exit = %d, want %d", code, ExitOK)
	}
	var decoded []device.TreeNode
	if err := json.Unmarshal([]byte(stdout), &decoded); err != nil {
		t.Fatalf("stdout is not a JSON array: %v\n%s", err, stdout)
	}
	if len(decoded) != 2 {
		t.Fatalf("got %d matches, want 2\n%s", len(decoded), stdout)
	}
	if decoded[0].Attributes["text"] != "Login" || decoded[1].Attributes["text"] != "Logout" {
		t.Fatalf("matches not preserved\n%s", stdout)
	}
}

func TestQueryRequiresAnExpression(t *testing.T) {
	t.Parallel()

	// Without an expression there is nothing to match — a usage error, not an
	// empty result.
	_, stderr, code := runQuery(t, queryRunnerReturning(nil, nil), "-p", "android")
	if code != ExitInvalid {
		t.Fatalf("exit = %d, want %d", code, ExitInvalid)
	}
	if !strings.Contains(stderr, "expression") {
		t.Fatalf("the refusal did not mention the missing expression: %q", stderr)
	}
}

func TestQueryRequiresAPlatform(t *testing.T) {
	t.Parallel()

	_, stderr, code := runQuery(t, queryRunnerReturning(nil, nil), "Login")
	if code != ExitInvalid {
		t.Fatalf("exit = %d, want %d", code, ExitInvalid)
	}
	if !strings.Contains(stderr, "platform") {
		t.Fatalf("the refusal did not mention the missing platform: %q", stderr)
	}
}

func TestQueryReportsAFetchFailure(t *testing.T) {
	t.Parallel()

	_, stderr, code := runQuery(t,
		queryRunnerReturning(nil, errors.New("agent query timed out")),
		"-p", "android", "Login")
	if code != ExitFailure {
		t.Fatalf("exit = %d, want %d", code, ExitFailure)
	}
	if !strings.Contains(stderr, "agent query timed out") {
		t.Fatalf("the failure did not carry the driver error: %q", stderr)
	}
}

func TestQueryFetchJoinsFailureWithBoundedCleanupAfterCancellation(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	fetchErr := errors.New("device info failed")
	closeErr := errors.New("driver close failed")
	driver := &queryCleanupDriver{
		FakeDriver: enginetest.NewFakeDriver(),
		cancel:     cancel,
		fetchErr:   fetchErr,
		closeErr:   closeErr,
	}
	_, err := queryDriverFetch(ctx, driver, "", "text=Login")
	if !errors.Is(err, fetchErr) {
		t.Fatalf("queryDriverFetch() error = %v, want fetch failure", err)
	}
	if !errors.Is(err, closeErr) {
		t.Fatalf("queryDriverFetch() error = %v, want close failure", err)
	}
	if ctx.Err() != context.Canceled {
		t.Fatalf("execution context error = %v, want cancellation", ctx.Err())
	}
	if driver.closeContextErr != nil {
		t.Fatalf("Close() context error = %v, want fresh cleanup context", driver.closeContextErr)
	}
	if !driver.closeHadDeadline {
		t.Fatal("Close() context had no cleanup deadline")
	}
}

type queryCleanupDriver struct {
	*enginetest.FakeDriver
	cancel           context.CancelFunc
	fetchErr         error
	closeErr         error
	closeContextErr  error
	closeHadDeadline bool
}

func (driver *queryCleanupDriver) DeviceInfo(context.Context) (device.DeviceInfo, error) {
	driver.cancel()
	return device.DeviceInfo{}, driver.fetchErr
}

func (driver *queryCleanupDriver) Close(ctx context.Context) error {
	driver.closeContextErr = ctx.Err()
	_, driver.closeHadDeadline = ctx.Deadline()
	return driver.closeErr
}

func TestQueryReportsZeroMatchesHonestly(t *testing.T) {
	t.Parallel()

	// No match is a valid answer (exit 0), but a bare "[]" reads as a broken
	// command, so it says so on stderr while stdout stays a real empty array a
	// pipe can still parse.
	stdout, stderr, code := runQuery(t, queryRunnerReturning(nil, nil), "-p", "android", "Nope")
	if code != ExitOK {
		t.Fatalf("exit = %d, want %d", code, ExitOK)
	}
	var decoded []device.TreeNode
	if err := json.Unmarshal([]byte(stdout), &decoded); err != nil || decoded == nil && stdout == "" {
		t.Fatalf("stdout is not a parseable empty array: %q", stdout)
	}
	if len(decoded) != 0 {
		t.Fatalf("expected zero matches, got %d", len(decoded))
	}
	if !strings.Contains(stderr, "no elements") {
		t.Fatalf("zero matches said nothing on stderr: %q", stderr)
	}
}

func TestQueryRefusesAnUnknownPlatform(t *testing.T) {
	t.Parallel()

	_, stderr, code := runQuery(t, queryRunnerReturning(nil, nil), "-p", "web", "Login")
	if code != ExitInvalid {
		t.Fatalf("exit = %d, want %d", code, ExitInvalid)
	}
	if !strings.Contains(stderr, "web") {
		t.Fatalf("the refusal did not name the bad platform: %q", stderr)
	}
}

// The same trap hierarchy had: on iOS an unnamed app means the springboard,
// not the app in front, so a query would answer "nothing matched" about the
// home screen while looking perfectly healthy.

func TestQueryPassesTheAppIDThrough(t *testing.T) {
	t.Parallel()

	var seen string
	runner := QueryRunner{
		Fetch: func(_ context.Context, _, _, appID, _ string) ([]device.TreeNode, error) {
			seen = appID
			return nil, nil
		},
	}
	if _, _, code := runQuery(t, runner,
		"-p", "ios", "--device", "UDID-1", "--app-id", "com.apple.Settings",
		"text=General"); code != ExitOK {
		t.Fatalf("exit = %d, want %d", code, ExitOK)
	}
	if seen != "com.apple.Settings" {
		t.Fatalf("app id = %q, want the one that was passed", seen)
	}
}

func TestQuerySaysWhenItIsSearchingTheSpringboard(t *testing.T) {
	t.Parallel()

	_, stderr, _ := runQuery(t, queryRunnerReturning(nil, nil),
		"-p", "ios", "--device", "UDID-1", "text=General")
	if !strings.Contains(stderr, "--app-id") {
		t.Fatalf("stderr = %q, want the springboard caveat", stderr)
	}

	// Android has no such ambiguity: its hierarchy is whatever is on screen.
	_, androidStderr, _ := runQuery(t, queryRunnerReturning(nil, nil),
		"-p", "android", "--device", "emulator-5554", "text=General")
	if strings.Contains(androidStderr, "--app-id") {
		t.Fatalf("stderr = %q, want no springboard caveat on android", androidStderr)
	}
}
