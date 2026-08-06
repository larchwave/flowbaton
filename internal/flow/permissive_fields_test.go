package flow

import "testing"

// scrollUntilVisible.speed and setLocation coordinates are string-typed so
// interpolation reaches runtime evaluation. Non-numeric resolved values fail
// during command evaluation. retry.maxRetries follows the same parser and
// compile-then-interpolate structure.

func acceptsValueShapes(t *testing.T, name, template string, values ...string) {
	t.Helper()
	for _, value := range values {
		yaml := "appId: com.example.app\n---\n" + template + value + "\n"
		if _, err := ParseBytes("/permissive.yaml", []byte(yaml)); err != nil {
			t.Fatalf("%s refused supported value %s: %v", name, value, err)
		}
	}
}

func TestScrollUntilVisibleSpeedIsStringTyped(t *testing.T) {
	t.Parallel()

	acceptsValueShapes(t, "scrollUntilVisible.speed",
		"- scrollUntilVisible:\n    element: {text: Ready}\n    speed: ",
		"40", `"40"`, `"abc"`, "${x}")
}

func TestSetLocationCoordinatesAreStringTyped(t *testing.T) {
	t.Parallel()

	acceptsValueShapes(t, "setLocation.latitude",
		"- setLocation:\n    longitude: 2.0\n    latitude: ",
		"48.0", `"48.0"`, `"abc"`, "${x}")
	acceptsValueShapes(t, "setLocation.longitude",
		"- setLocation:\n    latitude: 48.0\n    longitude: ",
		"2.0", `"2.0"`, `"abc"`, "${x}")
}

// travel.speed remains numeric and does not accept unresolved text.
func TestTravelSpeedStaysStrictlyNumeric(t *testing.T) {
	t.Parallel()

	for _, value := range []string{`"abc"`, "${x}"} {
		yaml := "appId: com.example.app\n---\n- travel:\n    points: ['43.65, -79.38']\n    speed: " + value + "\n"
		if _, err := ParseBytes("/permissive.yaml", []byte(yaml)); err == nil {
			t.Fatalf("travel.speed accepted non-numeric value %s", value)
		}
	}
}

// setLocation requires both coordinates even though their values are scalar text.
func TestSetLocationStillRequiresBothCoordinates(t *testing.T) {
	t.Parallel()

	for _, yaml := range []string{
		"- setLocation:\n    latitude: 48.0",
		"- setLocation:\n    longitude: 2.0",
	} {
		if _, err := ParseBytes("/permissive.yaml",
			[]byte("appId: com.example.app\n---\n"+yaml+"\n")); err == nil {
			t.Fatalf("setLocation accepted %q with one coordinate missing", yaml)
		}
	}
}

// Selector text, id, css, and label fields coerce scalar YAML values to text.
func TestSelectorStringFieldsTakeAnyScalar(t *testing.T) {
	t.Parallel()

	for _, field := range []string{"css", "label", "text", "id"} {
		for _, value := range []string{"Ready", "42", "true", "1.5"} {
			yaml := "appId: com.example.app\n---\n- tapOn:\n    point: 50%,50%\n    " +
				field + ": " + value + "\n"
			if _, err := ParseBytes("/selector.yaml", []byte(yaml)); err != nil {
				t.Fatalf("selector %s refused supported scalar %s: %v", field, value, err)
			}
		}
	}
}

// Selector text fields permit scalars and reject collections.
func TestSelectorStringFieldsStillRefuseCollections(t *testing.T) {
	t.Parallel()

	for _, field := range []string{"css", "label", "text", "id"} {
		for _, value := range []string{"[a, b]", "{a: b}"} {
			yaml := "appId: com.example.app\n---\n- tapOn:\n    point: 50%,50%\n    " +
				field + ": " + value + "\n"
			if _, err := ParseBytes("/selector.yaml", []byte(yaml)); err == nil {
				t.Fatalf("selector %s accepted %s, which is not a scalar", field, value)
			}
		}
	}
}
