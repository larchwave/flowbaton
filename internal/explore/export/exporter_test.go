package export

import (
	"strings"
	"testing"

	"github.com/larchwave/flowbaton/internal/explore"
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
- launchApp
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
	for _, absent := range []string{"stopApp", "Broken", "wait"} {
		if strings.Contains(text, absent) {
			t.Errorf("skipped step leaked %q into the output:\n%s", absent, text)
		}
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
