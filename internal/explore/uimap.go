package explore

import (
	"math"
	"strconv"
	"time"

	"github.com/larchwave/flowbaton/internal/device"
)

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

// PointLocator answers the point locator value for a screen coordinate in
// the "x,y" form the engine's tapOn point accepts: two base-10 integers.
// A half-pixel center floors rather than rounds, so the point stays
// inside the element's own bounds and never lands one pixel past an
// edge of the screen.
func PointLocator(point device.Point) string {
	return strconv.FormatInt(int64(math.Floor(point.X)), 10) + "," +
		strconv.FormatInt(int64(math.Floor(point.Y)), 10)
}

// Locator is one way to find an element on a screen.
type Locator struct {
	Kind  LocatorKind
	Value string
	// Index disambiguates when Value matches several elements.
	Index int
	// Label names the element for a human reader when Value cannot: a point
	// or a tree path says where, never what. Nothing matches on it and the
	// exporter never writes it -- an element reached by coordinate is reached
	// by coordinate on replay too.
	Label string
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
