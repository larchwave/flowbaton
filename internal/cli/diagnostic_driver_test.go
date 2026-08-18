package cli

import (
	"context"
	"fmt"
	"testing"

	"github.com/larchwave/flowbaton/internal/ios"
)

// The one-shot diagnostics run against a plain device, where no runner is
// listening yet. Skipping managed delivery leaves them dialing a port nobody
// serves, which reads as a dead device rather than a missing driver.
func TestDiagnosticsResolveTheManagedRunner(t *testing.T) {
	const udid = "UDID-1"
	for _, test := range []struct {
		name string
		call func(context.Context) error
	}{
		{name: "hierarchy", call: func(ctx context.Context) error {
			_, err := realHierarchyFetch(ctx, "ios", udid, nil, "")
			return err
		}},
		{name: "query", call: func(ctx context.Context) error {
			_, err := realQueryFetch(ctx, "ios", udid, "", "id == 'x'")
			return err
		}},
		{name: "screenshot", call: func(ctx context.Context) error {
			_, err := realScreenshotFetch(ctx, "ios", udid)
			return err
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			previous := resolveIOSRunnerBundle
			resolved := false
			// Refusing here keeps the test off the network: the fetch stops at
			// asset resolution instead of dialing a device.
			resolveIOSRunnerBundle = func(context.Context, iosRunnerFlavor) (*ios.RunnerBundle, error) {
				resolved = true
				return nil, fmt.Errorf("stub")
			}
			t.Cleanup(func() { resolveIOSRunnerBundle = previous })

			if err := test.call(context.Background()); err == nil {
				t.Fatal("stubbed asset resolution did not stop the fetch")
			}
			if !resolved {
				t.Fatal("diagnostic built its driver without managed runner delivery")
			}
		})
	}
}
