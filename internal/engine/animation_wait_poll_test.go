package engine

import (
	"testing"

	"github.com/larchwave/flowbaton/internal/device"
	"github.com/larchwave/flowbaton/internal/enginetest"
	"github.com/larchwave/flowbaton/internal/model"
)

// staticAnswers counts how many times the driver was asked whether the screen
// is static.
func staticAnswers(driver *enginetest.FakeDriver) int {
	count := 0
	for _, action := range driver.Actions() {
		if action.Method == enginetest.MethodWaitUntilScreenIsStatic {
			count++
		}
	}
	return count
}

func animationWaitCommand(timeoutMillis int64) model.Command {
	return model.Command{
		Kind: model.CommandWaitForAnimationToEnd, Form: model.CommandFormObject,
		Arguments: map[string]any{"timeout": timeoutMillis},
	}
}

// waitForAnimationToEnd must spend its budget waiting. internal/ios and
// internal/android both answer the question with ONE sample -- iOS reads
// two frames a tenth of a second apart, android asks the agent once -- and
// both carry the comment that the caller enforces the timeout. Neither
// caller did, so a 15-second animation wait returned in a fraction of a
// second on the two mobile platforms. internal/web is unaffected: it blocks
// internally for the timeout, so its first answer already ends the loop.
func TestWaitForAnimationToEndPollsUntilTheScreenIsStatic(t *testing.T) {
	t.Parallel()

	driver := enginetest.NewFakeDriver()
	driver.Enqueue(enginetest.DriverScript{
		DeviceInfo: []enginetest.Result[device.DeviceInfo]{{
			Value: device.DeviceInfo{Platform: device.Platform("android"), WidthGrid: 400, HeightGrid: 884},
		}},
		WaitUntilScreenIsStatic: []enginetest.Result[bool]{{Value: false}, {Value: false}, {Value: true}},
	})
	result, err := runNavigationCommand(t, driver, animationWaitCommand(2000))
	if err != nil {
		t.Fatalf("execute(waitForAnimationToEnd) error = %v", err)
	}
	if result.Outcome() != Completed {
		t.Fatalf("outcome = %s, want %s", result.Outcome(), Completed)
	}
	if got := staticAnswers(driver); got != 3 {
		t.Fatalf("driver asked %d time(s), want 3: the wait must poll until the screen settles", got)
	}
}

// A screen that never settles still completes the command -- this is a wait,
// not an assertion -- but only after the budget is spent, not after one
// sample.
func TestWaitForAnimationToEndSpendsItsBudgetOnAMovingScreen(t *testing.T) {
	t.Parallel()

	driver := enginetest.NewFakeDriver()
	driver.Enqueue(enginetest.DriverScript{
		DeviceInfo: []enginetest.Result[device.DeviceInfo]{{
			Value: device.DeviceInfo{Platform: device.Platform("android"), WidthGrid: 400, HeightGrid: 884},
		}},
		WaitUntilScreenIsStatic: []enginetest.Result[bool]{{Value: false}},
	})
	result, err := runNavigationCommand(t, driver, animationWaitCommand(500))
	if err != nil {
		t.Fatalf("execute error = %T %v, want completion on a screen that never settles", err, err)
	}
	if result.Outcome() != Completed {
		t.Fatalf("outcome = %s, want %s", result.Outcome(), Completed)
	}
	// The harness clock advances fake time on every wait and never sleeps, so
	// the budget is counted in polls, not in wall time: a 500ms budget at the
	// 100ms cadence is five waits and the sixth answer finds the deadline.
	if got := staticAnswers(driver); got != 6 {
		t.Fatalf("driver asked %d time(s), want 6 across a 500ms budget at %s", got, animationPollInterval)
	}
}
