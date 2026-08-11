package memory

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/larchwave/flowbaton/internal/explore"
)

func testScreen() explore.ScreenSignature {
	return explore.ScreenSignature{
		AppID:      "com.example.shop",
		Salient:    []string{"Login"},
		TreeDigest: "abcdef1234567890",
	}
}

func TestExperienceRecordAndReadBack(t *testing.T) {
	t.Parallel()
	store := NewExperience(t.TempDir())
	ctx := context.Background()
	screen := testScreen()

	entries := []explore.MemoryEntry{
		{Title: "open the cart", Body: "tap the cart icon in the tab bar"},
		{Title: "submit the form", Body: "fill every required field first"},
	}
	for _, entry := range entries {
		if err := store.Record(ctx, screen, entry); err != nil {
			t.Fatalf("Record(%q) error = %v", entry.Title, err)
		}
	}

	titles, err := store.Index(ctx, screen)
	if err != nil {
		t.Fatalf("Index() error = %v", err)
	}
	if want := []string{"open the cart", "submit the form"}; !reflect.DeepEqual(titles, want) {
		t.Fatalf("Index() = %v, want %v", titles, want)
	}
	body, err := store.Get(ctx, screen, "open the cart")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if body != "tap the cart icon in the tab bar" {
		t.Fatalf("Get() = %q", body)
	}
}

func TestExperienceRecordReplacesSameTitle(t *testing.T) {
	t.Parallel()
	store := NewExperience(t.TempDir())
	ctx := context.Background()
	screen := testScreen()

	if err := store.Record(ctx, screen, explore.MemoryEntry{Title: "open the cart", Body: "old way"}); err != nil {
		t.Fatalf("Record() error = %v", err)
	}
	if err := store.Record(ctx, screen, explore.MemoryEntry{Title: "open the cart", Body: "new way"}); err != nil {
		t.Fatalf("Record() error = %v", err)
	}
	titles, err := store.Index(ctx, screen)
	if err != nil {
		t.Fatalf("Index() error = %v", err)
	}
	if len(titles) != 1 {
		t.Fatalf("Index() = %v, want one title", titles)
	}
	body, err := store.Get(ctx, screen, "open the cart")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if body != "new way" {
		t.Fatalf("Get() = %q, want %q", body, "new way")
	}
}

func TestExperienceRedactsSecretsBeforeDisk(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	store := NewExperience(dir)
	ctx := context.Background()
	screen := testScreen()

	entry := explore.MemoryEntry{
		Title: "log in",
		Body:  "type the password: hunter2 then send token=tok_12345\napi_key: \"sk-live-999\" unlocks the door",
	}
	if err := store.Record(ctx, screen, entry); err != nil {
		t.Fatalf("Record() error = %v", err)
	}
	body, err := store.Get(ctx, screen, "log in")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	for _, leaked := range []string{"hunter2", "tok_12345", "sk-live-999"} {
		if strings.Contains(body, leaked) {
			t.Fatalf("Get() body still holds %q: %q", leaked, body)
		}
	}
	if !strings.Contains(body, "***") {
		t.Fatalf("Get() body lacks redaction marker: %q", body)
	}

	// Negative check on the raw filesystem: no secret byte may exist
	// anywhere under the state directory.
	err = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		raw, readErr := os.ReadFile(path)
		if readErr != nil {
			return readErr
		}
		for _, leaked := range []string{"hunter2", "tok_12345", "sk-live-999"} {
			if strings.Contains(string(raw), leaked) {
				return fmt.Errorf("secret %q reached disk in %s", leaked, path)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}

func TestExperienceSkipsEmptyBodies(t *testing.T) {
	t.Parallel()
	store := NewExperience(t.TempDir())
	ctx := context.Background()
	screen := testScreen()

	if err := store.Record(ctx, screen, explore.MemoryEntry{Title: "noop", Body: "   \n\t"}); err != nil {
		t.Fatalf("Record() error = %v", err)
	}
	titles, err := store.Index(ctx, screen)
	if err != nil {
		t.Fatalf("Index() error = %v", err)
	}
	if len(titles) != 0 {
		t.Fatalf("Index() = %v, want empty", titles)
	}
}

func TestExperienceGetMissingTitleFails(t *testing.T) {
	t.Parallel()
	store := NewExperience(t.TempDir())
	if _, err := store.Get(context.Background(), testScreen(), "absent"); err == nil {
		t.Fatal("Get() error = nil, want an error for a missing entry")
	}
}

func TestExperienceConcurrentRecords(t *testing.T) {
	t.Parallel()
	store := NewExperience(t.TempDir())
	ctx := context.Background()
	screen := testScreen()

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			entry := explore.MemoryEntry{Title: fmt.Sprintf("recipe %d", i), Body: "works"}
			if err := store.Record(ctx, screen, entry); err != nil {
				t.Errorf("Record(%d) error = %v", i, err)
			}
		}(i)
	}
	wg.Wait()
	titles, err := store.Index(ctx, screen)
	if err != nil {
		t.Fatalf("Index() error = %v", err)
	}
	if len(titles) != 8 {
		t.Fatalf("Index() returned %d titles, want 8", len(titles))
	}
}

func TestRedactSecrets(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "colon", in: "password: hunter2", want: "password: ***"},
		{name: "equals", in: "token=abc123", want: "token=***"},
		{name: "quoted", in: `api_key: "sk-live-1"`, want: "api_key: ***"},
		{name: "mid sentence", in: "use secret: s3cr3t here", want: "use secret: *** here"},
		{name: "plain text untouched", in: "tap the login button", want: "tap the login button"},
	}
	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := redactSecrets(test.in); got != test.want {
				t.Fatalf("redactSecrets(%q) = %q, want %q", test.in, got, test.want)
			}
		})
	}
}
