package run

import (
	"strings"
	"testing"

	"github.com/larchwave/flowbaton/internal/device"
)

func iosNode(attrs map[string]string) device.TreeNode {
	if attrs["bounds"] == "" {
		attrs["bounds"] = "[0,0][60,44]"
	}
	return device.TreeNode{Attributes: attrs}
}

// iOS answers with an element's accessibility VALUE in text and its NAME in
// accessibilityText; ElementLabel prefers text, so a row was named after its
// value. Captured live on the Calendar day view, iOS 26.2, 2026-09-01: the
// month strip is 31 buttons and the table gave them one name between them --
// "No events", "No events", "No events" -- so nothing in it could tell Monday
// from Friday. Session mmx61 spent a 24-step budget on that strip.
func TestElementTableNamesARowRatherThanValuingIt(t *testing.T) {
	t.Parallel()

	table := elementTable(makeState("com.apple.mobilecal", screen("September",
		iosNode(map[string]string{"elementType": "9", "accessibilityText": "Monday, August 31", "text": "No events"}),
		iosNode(map[string]string{"elementType": "9", "accessibilityText": "Tuesday, September 1", "text": "3 events"}),
	)))
	for _, want := range []string{`"Monday, August 31"`, `"Tuesday, September 1"`} {
		if !strings.Contains(table, want) {
			t.Errorf("the table does not name the day %s:\n%s", want, table)
		}
	}
	if strings.Contains(table, `button "No events"`) {
		t.Errorf("a day is still named after its value:\n%s", table)
	}
}

// The value a row carries is still a fact, and for a field it is the fact that
// matters: the row now names the field and says what is in it. A capture of
// the contacts search screen had the typed "ZZZNoSuchContact" standing as the
// field's own name, which lost the name "Search" instead.
func TestElementTableSaysWhatAFieldHolds(t *testing.T) {
	t.Parallel()

	table := elementTable(makeState("com.example.app", screen("Search",
		iosNode(map[string]string{"elementType": "45", "accessibilityText": "Search", "text": "ZZZNoSuchContact"}),
		iosNode(map[string]string{"elementType": "49", "accessibilityText": "Email"}),
		iosNode(map[string]string{"elementType": "50", "accessibilityText": "Password", "text": "hunter2"}),
	)))
	if !strings.Contains(table, `"Search" text-field holding "ZZZNoSuchContact"`) {
		t.Errorf("the field does not say what it holds:\n%s", table)
	}
	if !strings.Contains(table, `"Email" text-field empty`) {
		t.Errorf("an empty field lost its mark:\n%s", table)
	}
	// A secure field's content never reaches a prompt, a log, or an artifact.
	if strings.Contains(table, "hunter2") {
		t.Errorf("a secure field printed what was typed into it:\n%s", table)
	}
	if !strings.Contains(table, `"Password" text-field secure`) {
		t.Errorf("a secure field is not marked as one:\n%s", table)
	}
}

// A whitespace-only accessibility text names nothing, and taking it would give
// a row a blank name where it had none. Two rows of the captured contacts
// screens carry exactly one space.
func TestElementTableIgnoresABlankAccessibilityName(t *testing.T) {
	t.Parallel()

	table := elementTable(makeState("com.example.app", screen("Home",
		iosNode(map[string]string{"elementType": "9", "accessibilityText": " ", "text": "Continue"}),
	)))
	if !strings.Contains(table, `"Continue"`) {
		t.Errorf("the row lost the only name it had:\n%s", table)
	}
}

// Android puts a widget's name in text and has no name/value split, so no
// Android row moves.
func TestElementTableLeavesAndroidRowsAlone(t *testing.T) {
	t.Parallel()

	table := elementTable(makeState("com.example.app", screen("Home",
		button("Sign in", "signin", "[0,0][100,50]"),
	)))
	if !strings.Contains(table, `"Sign in"`) {
		t.Errorf("an android row was renamed:\n%s", table)
	}
}

// The driver's own check reaches the judge as a fact, and its evidence names
// the element it matched. Naming it by its value put "No events" in a line
// meant to say which day was checked.
func TestCheckVisibleEvidenceNamesTheRow(t *testing.T) {
	t.Parallel()

	day := iosNode(map[string]string{
		"elementType": "9", "accessibilityText": "Tuesday, September 1", "text": "3 events"})
	session, _ := inputSession(t, screen("September", day))
	reply, err := session.handleCheckVisible(t.Context(), []byte(`{"eidx":0}`))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(reply, "Tuesday, September 1") {
		t.Errorf("the check does not name the row: %s", reply)
	}
	if strings.Contains(reply, "3 events") {
		t.Errorf("the check names the row by its value: %s", reply)
	}
}

// Naming a row by its name took away the value it used to stand under, and
// for a calendar day the value is what a tester picks on: "1 event" against
// "No events". iOS carries both -- the name in accessibilityText, the value in
// text -- so the row carries both. Census over twenty-five captures: 54 of 599
// rows have a value that differs from their name, and 31 of them are one
// month of a calendar.
func TestElementTableKeepsAValueBesideTheName(t *testing.T) {
	t.Parallel()

	table := elementTable(makeState("com.apple.mobilecal", screen("September",
		iosNode(map[string]string{"elementType": "9", "accessibilityText": "Saturday, August 29", "text": "1 event"}),
		iosNode(map[string]string{"elementType": "9", "accessibilityText": "Sunday, August 30", "text": "No events"}),
		// A row whose value IS its name says it once.
		iosNode(map[string]string{"elementType": "48", "accessibilityText": "September", "text": "September"}),
	)))
	if !strings.Contains(table, `"Saturday, August 29" value "1 event"`) {
		t.Errorf("the row lost the value it stands under:\n%s", table)
	}
	if strings.Contains(table, `"September" value`) {
		t.Errorf("a row whose value is its name says it twice:\n%s", table)
	}
}

// A field says what it holds and never says it twice, and a secure one still
// says nothing at all.
func TestElementTableDoesNotValueAFieldTwice(t *testing.T) {
	t.Parallel()

	table := elementTable(makeState("com.example.app", screen("Search",
		iosNode(map[string]string{"elementType": "45", "accessibilityText": "Search", "text": "Kate"}),
		iosNode(map[string]string{"elementType": "50", "accessibilityText": "Password", "text": "hunter2"}),
	)))
	if strings.Contains(table, `value "Kate"`) {
		t.Errorf("a field says what it holds twice:\n%s", table)
	}
	if strings.Contains(table, "hunter2") {
		t.Errorf("a secure field printed what was typed into it:\n%s", table)
	}
}
