package explore

import "time"

// LocatorKind orders the locator ladder: stable identifiers first, visible
// text second, tree path third, grid point last.
type LocatorKind string

// Locator kinds, from most to least stable.
const (
	LocatorID    LocatorKind = "id"
	LocatorText  LocatorKind = "text"
	LocatorPath  LocatorKind = "path"
	LocatorPoint LocatorKind = "point"
)

// Locator is one way to find an element on a screen.
type Locator struct {
	Kind  LocatorKind
	Value string
	// Index disambiguates when Value matches several elements.
	Index int
}

// MappedElement is one interactive element in a researched UI map.
type MappedElement struct {
	EIDX     int
	Role     string
	Label    string
	Locators []Locator
	// Notes carries validation rules, data hints, or vision findings.
	Notes string
}

// Section groups related elements of one screen region.
type Section struct {
	Name     string
	Notes    string
	Elements []MappedElement
	// Trigger records the action that revealed a hidden section
	// (expanded row, opened sheet), empty for always-visible sections.
	Trigger string
}

// UIMap is the researched, validated map of one screen.
type UIMap struct {
	Screen    ScreenSignature
	Sections  []Section
	CreatedAt time.Time
	// Markdown is the rendered map given to planning and testing
	// conversations.
	Markdown string
}
