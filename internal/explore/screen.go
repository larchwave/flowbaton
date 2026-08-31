package explore

import (
	"time"

	"github.com/larchwave/flowbaton/internal/device"
	"github.com/larchwave/flowbaton/internal/hierarchy"
)

// ScreenSignature identifies one distinct screen state of the app under
// exploration. It plays the role a URL plays for a web page: memory files,
// research caches, and learned recipes are keyed by it. The digest must be
// normalized against animation and volatile text so that the same logical
// screen yields the same signature.
type ScreenSignature struct {
	// AppID scopes the signature to the application.
	AppID string
	// Salient holds a few stable prominent labels (screen title, tab
	// name) that make the signature human-readable.
	Salient []string
	// TreeDigest is a short hex digest of the normalized flattened
	// accessibility tree.
	TreeDigest string
}

// Key returns a filesystem-safe slug identifying this screen, built from
// the salient labels and a digest prefix.
func (s ScreenSignature) Key() string {
	slug := ""
	for _, part := range s.Salient {
		cleaned := slugify(part)
		if cleaned == "" {
			continue
		}
		if slug != "" {
			slug += "-"
		}
		slug += cleaned
	}
	digest := s.TreeDigest
	if len(digest) > 8 {
		digest = digest[:8]
	}
	if slug == "" {
		return digest
	}
	if digest == "" {
		return slug
	}
	return slug + "-" + digest
}

// Same reports whether two signatures identify the same screen state.
func (s ScreenSignature) Same(other ScreenSignature) bool {
	return s.AppID == other.AppID && s.TreeDigest == other.TreeDigest
}

func slugify(value string) string {
	out := make([]rune, 0, len(value))
	lastDash := false
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z' || r >= '0' && r <= '9':
			out = append(out, r)
			lastDash = false
		case r >= 'A' && r <= 'Z':
			out = append(out, r+('a'-'A'))
			lastDash = false
		default:
			if !lastDash && len(out) > 0 {
				out = append(out, '-')
				lastDash = true
			}
		}
	}
	for len(out) > 0 && out[len(out)-1] == '-' {
		out = out[:len(out)-1]
	}
	const maxSlug = 48
	if len(out) > maxSlug {
		out = out[:maxSlug]
	}
	return string(out)
}

// FlatElement is one interactive element of the flattened accessibility
// tree. EIDX is a machine-assigned index that joins the tree dump, the
// element table shown to models, and screenshot annotations.
type FlatElement struct {
	EIDX int
	Node device.TreeNode
	// Path is the child-index chain from the root, e.g. "0/2/1".
	Path string
	// Depth is the nesting level, root = 0.
	Depth int
}

// ScreenState is one full observation of the device screen.
type ScreenState struct {
	Signature     ScreenSignature
	Hierarchy     device.TreeNode
	Elements      []FlatElement
	ScreenshotPNG []byte
	CapturedAt    time.Time
	// DialogActive reports that a modal surface (alert, sheet, dialog)
	// dominates the screen.
	DialogActive bool
	// Viewport is the screen the device reported when this state was
	// captured. A zero one means nobody measured, not a screen of no size.
	Viewport device.Bounds
}

// FullTree normalizes the captured hierarchy and prunes nothing. Only a
// caller asking what could ever match wants this -- deciding whether a
// generalized selector stays unambiguous, where a row one scroll away is a
// second match waiting to happen. Anything deciding what to touch or what is
// on screen wants VisibleTree.
func (state *ScreenState) FullTree() (*hierarchy.Element, error) {
	return hierarchy.New(state.Hierarchy)
}

// VisibleTree normalizes the captured hierarchy and prunes it the way the
// engine does before matching (internal/engine/lookup.go). Everything that
// selects an element by name has to see the same screen the exported flow
// will be replayed against; matching the raw tree reaches elements with no
// area, whose centre is the screen corner, and elements past the screen
// edge, whose centre is off it.
//
// An unmeasured viewport prunes nothing: a caller holding a hand-built state
// has said nothing about screen size, and treating that as a screen of zero
// size would hide every element instead.
func (state *ScreenState) VisibleTree() (*hierarchy.Element, error) {
	root, err := state.FullTree()
	if err != nil {
		return nil, err
	}
	if state.Viewport.Width <= 0 || state.Viewport.Height <= 0 {
		return root, nil
	}
	return hierarchy.FilterVisible(root, state.Viewport), nil
}
