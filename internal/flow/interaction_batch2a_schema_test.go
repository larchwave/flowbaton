package flow

import "testing"

func TestInteractionBatch2APressKeySchemaExactStaticAndDynamicValues(t *testing.T) {
	t.Parallel()

	// Supported key names are case-insensitive, and underscore and space
	// spellings are interchangeable.
	accepted := []string{
		"ENTER", "enter", "Enter", "BACK", "back", "HOME", "home", "LOCK", "lock",
		"VOLUME_UP", "volume up", "VOLUME UP", "volume_up", "VOLUME_DOWN", "volume down",
		"POWER", "power", "backspace", "TAB", "${KEY}",
		"Remote Dpad Up", "remote_dpad_up", "Remote Media Play Pause", "TV Input HDMI 1",
	}
	for _, value := range accepted {
		yaml := "appId: com.example\n---\n- pressKey: '" + value + "'\n"
		if _, err := ParseBytes("/workspace/batch2a.yaml", []byte(yaml)); err != nil {
			t.Fatalf("pressKey %q parse error = %v", value, err)
		}
	}

	// Keys outside the supported set are rejected during parsing.
	invalid := []string{"", " ", "unknown", "REMOTE_UP", "REMOTE", "TV Input HDMI 4"}
	for _, value := range invalid {
		yaml := "appId: com.example\n---\n- pressKey: '" + value + "'\n"
		if _, err := ParseBytes("/workspace/batch2a.yaml", []byte(yaml)); err == nil {
			t.Fatalf("pressKey %q unexpectedly parsed", value)
		}
	}

	for _, value := range []string{"1", "true", "{}", "[]"} {
		yaml := "appId: com.example\n---\n- pressKey: " + value + "\n"
		if _, err := ParseBytes("/workspace/batch2a.yaml", []byte(yaml)); err == nil {
			t.Fatalf("pressKey wrong type %s unexpectedly parsed", value)
		}
	}
}

func TestInteractionBatch2ABareCommandsRejectEveryArgumentShape(t *testing.T) {
	t.Parallel()

	for _, keyword := range []string{"back", "hideKeyboard", "scroll"} {
		if _, err := ParseBytes("/workspace/batch2a.yaml", []byte("appId: com.example\n---\n- "+keyword+"\n")); err != nil {
			t.Fatalf("bare %s parse error = %v", keyword, err)
		}
		// A no-argument command still accepts an empty map and the universal
		// optional and label are universal command fields;
		// scalars, sequences, and unknown fields stay rejected.
		for _, value := range []string{"value", "1", "true", "{extra: true}", "[]"} {
			yaml := "appId: com.example\n---\n- " + keyword + ": " + value + "\n"
			if _, err := ParseBytes("/workspace/batch2a.yaml", []byte(yaml)); err == nil {
				t.Fatalf("%s argument %s unexpectedly parsed", keyword, value)
			}
		}
		if _, err := ParseBytes("/workspace/batch2a.yaml", []byte("appId: com.example\n---\n- "+keyword+": {}\n")); err != nil {
			t.Fatalf("%s empty-map argument rejected: %v", keyword, err)
		}
	}
}
