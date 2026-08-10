package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/larchwave/flowbaton/internal/device"
)

// query finds on-device elements matching an expression — spec 03's element
// finder. The expression is handed to the driver's QueryOnDeviceElements as-is;
// the agent owns that grammar, so the CLI adds no selector DSL of its own.

// QueryRunner holds the query behind a field so a test can match without a
// device. The default opens the real driver, queries, and closes it.
type QueryRunner struct {
	Fetch func(ctx context.Context, platform, udid, appID, expression string) ([]device.TreeNode, error)
}

func (runner QueryRunner) fetch() func(context.Context, string, string, string, string) ([]device.TreeNode, error) {
	if runner.Fetch != nil {
		return runner.Fetch
	}
	return realQueryFetch
}

func realQueryFetch(
	ctx context.Context, platform, udid, appID, expression string,
) ([]device.TreeNode, error) {
	port, err := diagnosticPort(platform, os.Environ())
	if err != nil {
		return nil, err
	}
	driver, err := newDriver(ctx, TestOptions{Platform: platform}, udid, port, 1)
	if err != nil {
		return nil, err
	}
	return queryDriverFetch(ctx, driver, appID, expression)
}

func queryDriverFetch(
	ctx context.Context, driver device.Driver, appID, expression string,
) (matches []device.TreeNode, resultErr error) {
	if err := driver.Open(ctx); err != nil {
		return nil, err
	}
	defer func() {
		cleanupCtx, cancel := context.WithTimeout(
			context.WithoutCancel(ctx), deviceSessionCleanupTimeout)
		defer cancel()
		closeErr := driver.Close(cleanupCtx)
		if closeErr != nil {
			closeErr = fmt.Errorf("closing query driver: %w", closeErr)
		}
		resultErr = errors.Join(resultErr, closeErr)
	}()

	// The hierarchy, then the host's own matcher — see query_match.go for why
	// this does not go to the driver's QueryOnDeviceElements.
	info, err := driver.DeviceInfo(ctx)
	if err != nil {
		return nil, err
	}
	tree, err := driver.ContentDescriptor(ctx, device.ContentDescriptorRequest{AppIDs: appIDFilter(appID)})
	if err != nil {
		return nil, err
	}
	viewport := device.Bounds{Width: info.WidthGrid, Height: info.HeightGrid}
	return matchQuery(tree, viewport, expression)
}

func (runner QueryRunner) Run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	options, code := parseQueryArgs(args, stderr)
	if code != ExitOK {
		return code
	}
	// The same caveat `hierarchy` carries, for the same reason: XCUITest cannot
	// snapshot "whatever is in front", so an unnamed app means the springboard,
	// and a query against the home screen finds nothing while looking healthy.
	if options.platform == "ios" && options.appID == "" {
		fmt.Fprintln(stderr,
			"query: no app named, so this searches the springboard, not an app; "+
				"pass --app-id <bundle-id> for the app in front")
	}

	matches, err := runner.fetch()(
		ctx, options.platform, options.udid, options.appID, options.expression)
	if err != nil {
		fmt.Fprintf(stderr, "query: %v\n", err)
		return ExitFailure
	}
	if matches == nil {
		// Marshal an empty array rather than null so a downstream pipe always
		// gets a parseable list.
		matches = []device.TreeNode{}
	}
	encoded, err := json.MarshalIndent(matches, "", "  ")
	if err != nil {
		fmt.Fprintf(stderr, "query: %v\n", err)
		return ExitFailure
	}
	fmt.Fprintln(stdout, string(encoded))
	if len(matches) == 0 {
		fmt.Fprintln(stderr, "query: no elements matched")
	}
	return ExitOK
}

type queryArgs struct {
	platform   string
	udid       string
	appID      string
	expression string
}

func parseQueryArgs(args []string, stderr io.Writer) (queryArgs, int) {
	var options queryArgs
	var expressions []string
	for i := 0; i < len(args); i++ {
		arg := args[i]
		needsValue := func() (string, bool) {
			if i+1 >= len(args) {
				fmt.Fprintf(stderr, "query: %s needs a value\n", arg)
				return "", false
			}
			i++
			return args[i], true
		}
		switch {
		case arg == "-p" || arg == "--platform":
			value, ok := needsValue()
			if !ok {
				return queryArgs{}, ExitInvalid
			}
			options.platform = value
		case strings.HasPrefix(arg, "--platform="):
			options.platform = strings.TrimPrefix(arg, "--platform=")
		case arg == "--device" || arg == "--udid":
			value, ok := needsValue()
			if !ok {
				return queryArgs{}, ExitInvalid
			}
			options.udid = value
		case strings.HasPrefix(arg, "--device="):
			options.udid = strings.TrimPrefix(arg, "--device=")
		case strings.HasPrefix(arg, "--udid="):
			options.udid = strings.TrimPrefix(arg, "--udid=")
		case arg == "--app-id":
			value, ok := needsValue()
			if !ok {
				return queryArgs{}, ExitInvalid
			}
			options.appID = value
		case strings.HasPrefix(arg, "--app-id="):
			options.appID = strings.TrimPrefix(arg, "--app-id=")
		case strings.HasPrefix(arg, "-"):
			fmt.Fprintf(stderr, "query: unexpected flag %q\n", arg)
			return queryArgs{}, ExitInvalid
		default:
			expressions = append(expressions, arg)
		}
	}

	switch options.platform {
	case "ios", "android":
	case "":
		fmt.Fprintln(stderr, "query: a platform is required: pass -p ios or -p android")
		return queryArgs{}, ExitInvalid
	default:
		fmt.Fprintf(stderr, "query: unknown platform %q (want ios or android)\n", options.platform)
		return queryArgs{}, ExitInvalid
	}
	if len(expressions) == 0 {
		fmt.Fprintln(stderr, "query: an expression is required")
		return queryArgs{}, ExitInvalid
	}
	options.expression = strings.Join(expressions, " ")
	return options, ExitOK
}
