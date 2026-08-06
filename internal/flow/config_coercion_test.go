package flow

import "testing"

// Flow header string fields coerce scalar YAML values. Collections remain
// invalid, and a flow must still declare an app target key.

func configParses(t *testing.T, header string) error {
	t.Helper()
	_, err := ParseBytes("/config.yaml", []byte(header+"---\n- back\n"))
	return err
}

func TestConfigScalarFieldsAreCoerced(t *testing.T) {
	t.Parallel()

	for _, header := range []string{
		"appId: 42\n",
		"appId: com.example.app\nname: 42\n",
		"appId: com.example.app\nname: true\n",
		"appId: com.example.app\nname: 1.5\n",
	} {
		if err := configParses(t, header); err != nil {
			t.Fatalf("config refused supported scalar %q: %v", header, err)
		}
	}
}

func TestConfigTagsCoerceTheirEntries(t *testing.T) {
	t.Parallel()

	for _, header := range []string{
		"appId: com.example.app\ntags:\n  - 42\n",
		"appId: com.example.app\ntags:\n  - smoke\n  - 42\n  - true\n",
	} {
		if err := configParses(t, header); err != nil {
			t.Fatalf("config refused supported tag scalar %q: %v", header, err)
		}
	}
}

func TestConfigEnvCoercesItsValues(t *testing.T) {
	t.Parallel()

	for _, header := range []string{
		"appId: com.example.app\nenv:\n  RETRIES: 3\n",
		"appId: com.example.app\nenv:\n  DEBUG: true\n",
		"appId: com.example.app\nenv:\n  RATIO: 1.5\n",
	} {
		if err := configParses(t, header); err != nil {
			t.Fatalf("config refused supported environment scalar %q: %v", header, err)
		}
	}
}

// Scalar coercion does not admit collections or remove the app-target requirement.
func TestConfigStillRefusesCollectionsAndMissingAppTarget(t *testing.T) {
	t.Parallel()

	for _, header := range []string{
		"appId: [a, b]\n",
		"appId: {a: b}\n",
		"appId: com.example.app\nname:\n  a: b\n",
		"appId: com.example.app\nenv:\n  OUTER:\n    INNER: 1\n",
		"appId: com.example.app\ntags:\n  - a: b\n",
	} {
		if err := configParses(t, header); err == nil {
			t.Fatalf("config accepted %q, which is not a scalar", header)
		}
	}
	if err := configParses(t, ""); err == nil {
		t.Fatal("config accepted a flow with no app target")
	}
}

// A blank app target is present and is validated by commands that require an
// app. A missing or null target is rejected during parsing.
func TestABlankAppTargetIsDeferredButAMissingOneIsNot(t *testing.T) {
	t.Parallel()

	for _, header := range []string{
		"appId: \"\"\n",
		"_appId: \"\"\n",
		"url: \"\"\n",
	} {
		if err := configParses(t, header); err != nil {
			t.Fatalf("config refused present blank target %q: %v", header, err)
		}
	}
	// No app-target key and a null target remain invalid. A null is not blank text.
	for _, header := range []string{"name: no target\n", "appId:\n", "url:\n"} {
		if err := configParses(t, header); err == nil {
			t.Fatalf("config accepted missing or null target %q", header)
		}
	}
}
