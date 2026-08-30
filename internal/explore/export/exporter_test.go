package export

import (
	"strings"
	"testing"

	"github.com/larchwave/flowbaton/internal/explore"
	"github.com/larchwave/flowbaton/internal/flow"
)

func step(index int, kind explore.ActionKind, target *explore.Locator, text, direction string) explore.StepRecord {
	return explore.StepRecord{
		Index:  index,
		Status: explore.StepOK,
		Action: explore.Action{Kind: kind, Target: target, Text: text, Direction: direction},
	}
}

func passingResult(steps ...explore.StepRecord) *explore.TestResult {
	return &explore.TestResult{
		Scenario: explore.Scenario{Name: "login works"},
		Status:   explore.TestPassed,
		Steps:    steps,
	}
}

func TestExportGoldenMultiStepRun(t *testing.T) {
	failed := step(12, explore.ActionTap, &explore.Locator{Kind: explore.LocatorText, Value: "Broken"}, "", "")
	failed.Status = explore.StepFailed
	result := passingResult(
		step(0, explore.ActionLaunch, nil, "", ""),
		step(1, explore.ActionTap, &explore.Locator{Kind: explore.LocatorID, Value: "login_button"}, "", ""),
		step(2, explore.ActionTap, &explore.Locator{Kind: explore.LocatorText, Value: "Continue", Index: 1}, "", ""),
		step(3, explore.ActionInput, nil, "hello", ""),
		step(4, explore.ActionSwipe, nil, "", "up"),
		step(5, explore.ActionScroll, nil, "", "down"),
		step(6, explore.ActionBack, nil, "", ""),
		step(7, explore.ActionPressKey, nil, "ENTER", ""),
		step(8, explore.ActionHideKeys, nil, "", ""),
		step(9, explore.ActionOpenLink, nil, "https://example.com", ""),
		step(10, explore.ActionErase, nil, "5", ""),
		step(11, explore.ActionWait, nil, "", ""),
		failed,
		step(13, explore.ActionVerify, nil, "Welcome", ""),
	)
	data, err := Exporter{}.ExportFlow(result, "com.example.app")
	if err != nil {
		t.Fatalf("ExportFlow: %v", err)
	}
	want := `appId: com.example.app
name: "login works"
tags:
  - explored
---
- launchApp:
    stopApp: true
- tapOn:
    id: "login_button"
- tapOn:
    text: "Continue"
    index: 1
- inputText: "hello"
- swipe:
    direction: UP
- scroll
- back
- pressKey: ENTER
- hideKeyboard
- openLink: "https://example.com"
- eraseText: 5
- assertVisible: "Welcome"
`
	if string(data) != want {
		t.Fatalf("golden mismatch\n--- want ---\n%s\n--- got ---\n%s", want, string(data))
	}
}

func TestSelectorLadderPerLocatorKind(t *testing.T) {
	cases := []struct {
		name    string
		target  *explore.Locator
		want    string
		wantErr bool
	}{
		{name: "id", target: &explore.Locator{Kind: explore.LocatorID, Value: "save"}, want: "- tapOn:\n    id: \"save\"\n"},
		{name: "text", target: &explore.Locator{Kind: explore.LocatorText, Value: "Save"}, want: "- tapOn: \"Save\"\n"},
		{name: "point", target: &explore.Locator{Kind: explore.LocatorPoint, Value: "50%,20%"}, want: "- tapOn:\n    point: \"50%,20%\"\n"},
		{name: "path has no stable selector", target: &explore.Locator{Kind: explore.LocatorPath, Value: "0/2/1"}, wantErr: true},
		{name: "missing locator", target: nil, wantErr: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			data, err := Exporter{}.ExportFlow(passingResult(step(0, explore.ActionTap, c.target, "", "")), "com.example.app")
			if c.wantErr {
				if err == nil {
					t.Fatal("want an error")
				}
				return
			}
			if err != nil {
				t.Fatalf("ExportFlow: %v", err)
			}
			if !strings.Contains(string(data), c.want) {
				t.Errorf("output missing %q:\n%s", c.want, string(data))
			}
		})
	}
}

func TestLongPressAndVerifyShareTheLadder(t *testing.T) {
	data, err := Exporter{}.ExportFlow(passingResult(
		step(0, explore.ActionLongPress, &explore.Locator{Kind: explore.LocatorID, Value: "row_3"}, "", ""),
		step(1, explore.ActionVerify, &explore.Locator{Kind: explore.LocatorText, Value: "Deleted"}, "", ""),
	), "com.example.app")
	if err != nil {
		t.Fatalf("ExportFlow: %v", err)
	}
	for _, want := range []string{"- longPressOn:\n    id: \"row_3\"\n", "- assertVisible: \"Deleted\"\n"} {
		if !strings.Contains(string(data), want) {
			t.Errorf("output missing %q:\n%s", want, string(data))
		}
	}
}

func TestSkippedStepsStayOut(t *testing.T) {
	failed := step(3, explore.ActionTap, &explore.Locator{Kind: explore.LocatorText, Value: "Broken"}, "", "")
	failed.Status = explore.StepFailed
	data, err := Exporter{}.ExportFlow(passingResult(
		step(0, explore.ActionLaunch, nil, "", ""),
		step(1, explore.ActionStop, nil, "", ""),
		step(2, explore.ActionWait, nil, "", ""),
		failed,
		step(4, explore.ActionTap, &explore.Locator{Kind: explore.LocatorText, Value: "Keep"}, "", ""),
	), "com.example.app")
	if err != nil {
		t.Fatalf("ExportFlow: %v", err)
	}
	text := string(data)
	if got := strings.Count(text, "launchApp"); got != 1 {
		t.Errorf("want exactly one launchApp, got %d:\n%s", got, text)
	}
	for _, absent := range []string{"Broken", "wait"} {
		if strings.Contains(text, absent) {
			t.Errorf("skipped step leaked %q into the output:\n%s", absent, text)
		}
	}
	// stopApp appears once, as the leading launchApp's own argument. What
	// must not appear is a stop step of its own; testing for the bare word
	// stopped telling those two apart the moment the launch grew it.
	if strings.Count(text, "stopApp") != 1 || strings.Contains(text, "- stopApp") {
		t.Errorf("a skipped stop step became a command of its own:\n%s", text)
	}
	if !strings.Contains(text, "\"Keep\"") {
		t.Errorf("surviving step is missing:\n%s", text)
	}
}

func TestRoundTripRejectsBadOutput(t *testing.T) {
	_, err := Exporter{}.ExportFlow(passingResult(
		step(0, explore.ActionPressKey, nil, "NOT_A_REAL_KEY", ""),
	), "com.example.app")
	if err == nil {
		t.Fatal("want the round-trip check to reject an invalid key name")
	}
	if !strings.Contains(err.Error(), "failed validation") {
		t.Fatalf("want a validation error, got: %v", err)
	}
}

func TestExportRefusesBadInput(t *testing.T) {
	if _, err := (Exporter{}).ExportFlow(nil, "com.example.app"); err == nil {
		t.Error("want an error for a nil result")
	}
	if _, err := (Exporter{}).ExportFlow(passingResult(), ""); err == nil {
		t.Error("want an error for an empty app id")
	}
	failedRun := passingResult()
	failedRun.Status = explore.TestFailed
	if _, err := (Exporter{}).ExportFlow(failedRun, "com.example.app"); err == nil {
		t.Error("want an error for a run that did not pass")
	}
}

func TestEraseAndSwipeEdges(t *testing.T) {
	data, err := Exporter{}.ExportFlow(passingResult(step(0, explore.ActionErase, nil, "", "")), "com.example.app")
	if err != nil {
		t.Fatalf("bare erase: %v", err)
	}
	if !strings.Contains(string(data), "- eraseText\n") {
		t.Errorf("want a bare eraseText command:\n%s", string(data))
	}
	if _, err := (Exporter{}).ExportFlow(passingResult(step(0, explore.ActionErase, nil, "lots", "")), "com.example.app"); err == nil {
		t.Error("want an error for a non-integer erase count")
	}
	if _, err := (Exporter{}).ExportFlow(passingResult(step(0, explore.ActionSwipe, nil, "", "")), "com.example.app"); err == nil {
		t.Error("want an error for a swipe without direction")
	}
}

func maskedStep(index int) explore.StepRecord {
	record := step(index, explore.ActionInput, nil, explore.MaskedText, "")
	record.Action.Masked = true
	return record
}

func TestExportParameterizesMaskedInput(t *testing.T) {
	result := passingResult(
		maskedStep(0),
		step(1, explore.ActionInput, nil, "plain", ""),
		maskedStep(2),
	)
	data, err := Exporter{}.ExportFlow(result, "com.example.app")
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if strings.Contains(text, explore.MaskedText) {
		t.Fatalf("literal mask exported:\n%s", text)
	}
	for _, want := range []string{"${FLOWBATON_EXPLORE_SECRET_1}", "${FLOWBATON_EXPLORE_SECRET_2}", "plain"} {
		if !strings.Contains(text, want) {
			t.Fatalf("export misses %q:\n%s", want, text)
		}
	}
}

func TestExportKeepsLiteralAsterisks(t *testing.T) {
	result := passingResult(step(0, explore.ActionInput, nil, "***", ""))
	data, err := Exporter{}.ExportFlow(result, "com.example.app")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"***"`) || strings.Contains(string(data), "FLOWBATON_EXPLORE_SECRET") {
		t.Fatalf("literal asterisks were treated as a mask:\n%s", data)
	}
}

func TestExportedCountSelectorSurvivesTheYAMLRoundTrip(t *testing.T) {
	// A generalized count selector is the first exported value carrying a
	// backslash. YAML double quotes give the backslash its own meaning, so
	// the pattern has to come back out of the parser byte for byte or the
	// flow taps nothing.
	pattern := `All, \d+ reminders`
	result := passingResult(
		step(1, explore.ActionTap, &explore.Locator{Kind: explore.LocatorText, Value: pattern}, "", ""),
	)
	data, err := (Exporter{}).ExportFlow(result, "com.apple.reminders")
	if err != nil {
		t.Fatalf("ExportFlow: %v", err)
	}
	parsed, err := flow.ParseBytes("flow.yaml", data)
	if err != nil {
		t.Fatalf("the exported flow does not parse:\n%s\n%v", data, err)
	}
	found := false
	for _, command := range parsed.Commands {
		if command.Selector == nil || command.Selector.TextRegex == nil {
			continue
		}
		found = true
		if *command.Selector.TextRegex != pattern {
			t.Fatalf("parsed text selector = %q, want %q\nYAML:\n%s",
				*command.Selector.TextRegex, pattern, data)
		}
	}
	if !found {
		t.Fatalf("no text selector survived the round trip:\n%s", data)
	}
}

// The session now returns the app to its start screen before every
// scenario, so the run an exported flow records begins on a freshly
// launched app. A bare launchApp resumes whatever is already running, so
// replaying the flow would begin somewhere else -- which is how mmx36's
// flows failed at step 2 on selectors that were never the problem.
func TestExportedFlowLaunchesTheAppTheWayTheSessionDid(t *testing.T) {
	data, err := Exporter{}.ExportFlow(&explore.TestResult{
		Scenario: explore.Scenario{Name: "Open a list"},
		Status:   explore.TestPassed,
		Steps: []explore.StepRecord{{
			Index:  0,
			Status: explore.StepOK,
			Action: explore.Action{
				Kind:   explore.ActionTap,
				Target: &explore.Locator{Kind: explore.LocatorText, Value: "Reminders"},
			},
		}},
	}, "com.example.app")
	if err != nil {
		t.Fatalf("ExportFlow: %v", err)
	}
	body := string(data)
	if !strings.Contains(body, "stopApp: true") {
		t.Fatalf("exported flow does not stop the app before launching it:\n%s", body)
	}
	if strings.Contains(body, "clearState") {
		t.Fatalf("exported flow clears state, which would destroy the data it needs:\n%s", body)
	}
}
