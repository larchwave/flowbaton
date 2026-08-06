package cli

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/larchwave/flowbaton/internal/device"
)

// hierarchy dumps the current view hierarchy for one device — spec 03's
// operator diagnostic for "what is on screen". It reads the tree off the
// driver's ContentDescriptor and prints it; it taps and changes nothing.

// HierarchyRunner holds the tree fetch behind a field so a test can stand in a
// known tree without a device. The default opens the real driver, snapshots,
// and closes it.
type HierarchyRunner struct {
	Fetch func(ctx context.Context, platform, udid string, appIDs []string) (device.TreeNode, error)
}

func (runner HierarchyRunner) fetch() func(context.Context, string, string, []string) (device.TreeNode, error) {
	if runner.Fetch != nil {
		return runner.Fetch
	}
	return realHierarchyFetch
}

// realHierarchyFetch is the device path: assign an ephemeral driver port, build
// the platform driver, open it, snapshot the foreground tree, close. AppIDs is
// left empty — with no flow there is no declared app; Android returns the full
// tree and iOS the foreground app's.
func realHierarchyFetch(
	ctx context.Context, platform, udid string, appIDs []string,
) (device.TreeNode, error) {
	port, err := diagnosticPort(platform, os.Environ())
	if err != nil {
		return device.TreeNode{}, err
	}
	driver, err := newDriver(TestOptions{Platform: platform}, udid, port, 1)
	if err != nil {
		return device.TreeNode{}, err
	}
	if err := driver.Open(ctx); err != nil {
		return device.TreeNode{}, err
	}
	defer func() { _ = driver.Close(ctx) }()
	return driver.ContentDescriptor(ctx, device.ContentDescriptorRequest{AppIDs: appIDs})
}

func (runner HierarchyRunner) Run(ctx context.Context, args []string, stdout, stderr io.Writer) int {
	options, code := parseHierarchyArgs(args, stderr)
	if code != ExitOK {
		return code
	}

	// The caveat goes to stderr, so a piped `hierarchy | jq` is unaffected.
	if options.platform == "ios" && options.appID == "" {
		fmt.Fprintln(stderr,
			"hierarchy: no app named, so this is the springboard's tree, not an app's; "+
				"pass --app-id <bundle-id> for the app in front")
	}
	tree, err := runner.fetch()(ctx, options.platform, options.udid, options.appIDs())
	if err != nil {
		fmt.Fprintf(stderr, "hierarchy: %v\n", err)
		return ExitFailure
	}

	if options.csv {
		if err := writeHierarchyCSV(stdout, tree); err != nil {
			fmt.Fprintf(stderr, "hierarchy: %v\n", err)
			return ExitFailure
		}
		return ExitOK
	}
	encoded, err := json.MarshalIndent(tree, "", "  ")
	if err != nil {
		fmt.Fprintf(stderr, "hierarchy: %v\n", err)
		return ExitFailure
	}
	fmt.Fprintln(stdout, string(encoded))
	return ExitOK
}

type hierarchyArgs struct {
	platform string
	udid     string
	appID    string
	csv      bool
}

// appIDs is the filter the driver takes. Empty means "no filter", which on
// Android is the whole window and on iOS is the springboard.
func (args hierarchyArgs) appIDs() []string { return appIDFilter(args.appID) }

func parseHierarchyArgs(args []string, stderr io.Writer) (hierarchyArgs, int) {
	var parsed hierarchyArgs
	for i := 0; i < len(args); i++ {
		arg := args[i]
		needsValue := func() (string, bool) {
			if i+1 >= len(args) {
				fmt.Fprintf(stderr, "hierarchy: %s needs a value\n", arg)
				return "", false
			}
			i++
			return args[i], true
		}
		switch {
		case arg == "-p" || arg == "--platform":
			value, ok := needsValue()
			if !ok {
				return parsed, ExitInvalid
			}
			parsed.platform = value
		case strings.HasPrefix(arg, "--platform="):
			parsed.platform = strings.TrimPrefix(arg, "--platform=")
		case arg == "--device" || arg == "--udid":
			value, ok := needsValue()
			if !ok {
				return parsed, ExitInvalid
			}
			parsed.udid = value
		case strings.HasPrefix(arg, "--device="):
			parsed.udid = strings.TrimPrefix(arg, "--device=")
		case strings.HasPrefix(arg, "--udid="):
			parsed.udid = strings.TrimPrefix(arg, "--udid=")
		// --compact is the documented spelling. --csv remains a compatibility
		// alias for the same output format.
		case arg == "--app-id":
			value, ok := needsValue()
			if !ok {
				return parsed, ExitInvalid
			}
			parsed.appID = value
		case strings.HasPrefix(arg, "--app-id="):
			parsed.appID = strings.TrimPrefix(arg, "--app-id=")
		case arg == "--csv" || arg == "--compact":
			parsed.csv = true
		case arg == "--target" || strings.HasPrefix(arg, "--target="):
			// --target=devtools (Android WebView) is spec'd but not built.
			// Refuse rather than dump the native tree under a WebView flag.
			fmt.Fprintln(stderr, "hierarchy: --target (devtools WebView) is not supported yet")
			return parsed, ExitInvalid
		default:
			fmt.Fprintf(stderr, "hierarchy: unexpected argument %q\n", arg)
			return parsed, ExitInvalid
		}
	}

	switch parsed.platform {
	case "ios", "android":
	case "":
		fmt.Fprintln(stderr, "hierarchy: a platform is required: pass -p ios or -p android")
		return parsed, ExitInvalid
	default:
		fmt.Fprintf(stderr, "hierarchy: unknown platform %q (want ios or android)\n", parsed.platform)
		return parsed, ExitInvalid
	}
	return parsed, ExitOK
}

// writeHierarchyCSV flattens the tree to one row per node, keeping the nesting
// as a depth column. Attributes are joined k=v;k=v (sorted, stable) so the row
// stays platform-neutral — iOS and Android name their attributes differently.
func writeHierarchyCSV(w io.Writer, root device.TreeNode) error {
	writer := csv.NewWriter(w)
	if err := writer.Write([]string{
		"depth", "attributes", "clickable", "enabled", "focused", "checked", "selected",
	}); err != nil {
		return err
	}
	var walk func(node device.TreeNode, depth int) error
	walk = func(node device.TreeNode, depth int) error {
		row := []string{
			strconv.Itoa(depth),
			joinAttributes(node.Attributes),
			boolCell(node.Clickable),
			boolCell(node.Enabled),
			boolCell(node.Focused),
			boolCell(node.Checked),
			boolCell(node.Selected),
		}
		if err := writer.Write(row); err != nil {
			return err
		}
		for _, child := range node.Children {
			if err := walk(child, depth+1); err != nil {
				return err
			}
		}
		return nil
	}
	if err := walk(root, 0); err != nil {
		return err
	}
	writer.Flush()
	return writer.Error()
}

func joinAttributes(attributes map[string]string) string {
	keys := make([]string, 0, len(attributes))
	for key := range attributes {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	pairs := make([]string, 0, len(keys))
	for _, key := range keys {
		pairs = append(pairs, key+"="+attributes[key])
	}
	return strings.Join(pairs, ";")
}

func boolCell(value *bool) string {
	if value == nil {
		return ""
	}
	return strconv.FormatBool(*value)
}

// appIDFilter turns one optional bundle id into the driver's filter. Empty
// means no filter, which on Android is the whole window and on iOS is the
// springboard rather than the app in front.
func appIDFilter(appID string) []string {
	if strings.TrimSpace(appID) == "" {
		return nil
	}
	return []string{appID}
}
