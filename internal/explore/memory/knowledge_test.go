package memory

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/larchwave/flowbaton/internal/explore"
)

func writeKnowledgeFile(t *testing.T, stateDir, name, content string) {
	t.Helper()
	dir := filepath.Join(stateDir, "knowledge")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestKnowledgeMatch(t *testing.T) {
	t.Parallel()
	loginScreen := explore.ScreenSignature{
		AppID:      "com.example.shop",
		Salient:    []string{"Login", "Welcome back"},
		TreeDigest: "abcdef1234567890",
	}
	cartScreen := explore.ScreenSignature{
		AppID:      "com.example.shop",
		Salient:    []string{"Cart"},
		TreeDigest: "1234567890abcdef",
	}

	tests := []struct {
		name   string
		files  map[string]string
		screen explore.ScreenSignature
		want   []string
	}{
		{
			name:   "no directive applies everywhere",
			files:  map[string]string{"general.md": "the app resets nightly"},
			screen: cartScreen,
			want:   []string{"the app resets nightly"},
		},
		{
			name:   "glob on screen key",
			files:  map[string]string{"login.md": "match: login-*\nuse the demo account"},
			screen: loginScreen,
			want:   []string{"use the demo account"},
		},
		{
			name:   "glob excludes other screens",
			files:  map[string]string{"login.md": "match: login-*\nuse the demo account"},
			screen: cartScreen,
			want:   nil,
		},
		{
			name:   "substring on salient labels",
			files:  map[string]string{"welcome.md": "match: welcome back\nskip the promo banner"},
			screen: loginScreen,
			want:   []string{"skip the promo banner"},
		},
		{
			name: "several files in filename order",
			files: map[string]string{
				"a-general.md": "always dismiss the rating dialog",
				"b-login.md":   "match: login-*\nuse the demo account",
			},
			screen: loginScreen,
			want:   []string{"always dismiss the rating dialog", "use the demo account"},
		},
		{
			name:   "empty body skipped",
			files:  map[string]string{"blank.md": "match: login-*\n   \n"},
			screen: loginScreen,
			want:   nil,
		},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			stateDir := t.TempDir()
			for name, content := range test.files {
				writeKnowledgeFile(t, stateDir, name, content)
			}
			store := NewKnowledge(stateDir, func(string) string { return "" })
			got, err := store.Match(context.Background(), test.screen)
			if err != nil {
				t.Fatalf("Match() error = %v", err)
			}
			if !reflect.DeepEqual(got, test.want) {
				t.Fatalf("Match() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestKnowledgeInterpolatesEnv(t *testing.T) {
	t.Parallel()
	stateDir := t.TempDir()
	writeKnowledgeFile(t, stateDir, "login.md",
		"log in as ${env.DEMO_USER} with ${env.DEMO_PASS}; ${env.UNSET} stays empty")
	fake := map[string]string{"DEMO_USER": "demo@example.com", "DEMO_PASS": "pw-from-env"}
	store := NewKnowledge(stateDir, func(name string) string { return fake[name] })

	got, err := store.Match(context.Background(), testScreen())
	if err != nil {
		t.Fatalf("Match() error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("Match() = %v, want one body", got)
	}
	want := "log in as demo@example.com with pw-from-env;  stays empty"
	if got[0] != want {
		t.Fatalf("Match() body = %q, want %q", got[0], want)
	}

	// The credential value must exist only in the returned text, never in
	// the file on disk.
	raw, err := os.ReadFile(filepath.Join(stateDir, "knowledge", "login.md"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "pw-from-env") {
		t.Fatal("credential value found inside the knowledge file")
	}
}

func TestKnowledgeMissingDirectoryIsEmpty(t *testing.T) {
	t.Parallel()
	store := NewKnowledge(t.TempDir(), nil)
	got, err := store.Match(context.Background(), testScreen())
	if err != nil {
		t.Fatalf("Match() error = %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("Match() = %v, want empty", got)
	}
}
