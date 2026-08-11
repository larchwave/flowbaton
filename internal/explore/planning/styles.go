package planning

// Style pairs a planning style name with the prompt directive that shapes
// one planning iteration.
type Style struct {
	Name      string
	Directive string
}

// builtinStyles holds the rotation order; normal is the default and the
// fallback for unknown names.
var builtinStyles = []Style{
	{
		Name: "normal",
		Directive: "Walk the screen the way a committed user would: pick the " +
			"core value paths, carry each workflow through to completion, " +
			"favour steps that change data or leave a lasting state, and " +
			"confirm the visible result at the end.",
	},
	{
		Name: "curious",
		Directive: "Chase the roads a checklist misses: alternate ways to " +
			"reach the same goal, cancelling or backing out mid-flow, " +
			"switching to another task before finishing, and whatever the " +
			"screen shows when there is nothing to show. Every scenario " +
			"must still end at an observable on-screen result.",
	},
	{
		Name: "edge",
		Directive: "Push the screen hard: feed invalid, empty, oversized, " +
			"and boundary values, interrupt flows midway, repeat the same " +
			"action back to back - then commit and watch how the app " +
			"answers on screen.",
	},
}

// Styles returns the built-in planning styles in rotation order. The CLI
// lists these to the operator.
func Styles() []Style {
	return append([]Style(nil), builtinStyles...)
}

// LookupStyle resolves a style name. Unknown names fall back to the
// normal style with known reported false so callers can note the miss.
func LookupStyle(name string) (style Style, known bool) {
	for _, s := range builtinStyles {
		if s.Name == name {
			return s, true
		}
	}
	return builtinStyles[0], false
}
