package version

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestPOSIXSmokeCanonicalizesTemporaryHomeRoot(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash unavailable")
	}
	root := t.TempDir()
	realDir := filepath.Join(root, "real")
	if err := os.Mkdir(realDir, 0700); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(root, "alias")
	if err := os.Symlink(realDir, alias); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	bin := filepath.Join(root, "bin")
	if err := os.Mkdir(bin, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(bin, "mktemp"), []byte("#!/bin/sh\nprintf '%s\\n' \"$SMOKE_TEST_TEMP\"\n"), 0700); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile("../../scripts/release/smoke-posix.sh")
	if err != nil {
		t.Fatal(err)
	}
	setup, _, ok := strings.Cut(string(data), "server_pid=")
	if !ok {
		t.Fatal("smoke setup boundary missing")
	}
	cmd := exec.Command(bash, "-c", setup+"\nprintf '%s' \"$tmp\"", "smoke", "candidate", "0.2.0-beta.4")
	cmd.Env = append(os.Environ(), "PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"), "SMOKE_TEST_TEMP="+alias)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("smoke setup: %v: %s", err, out)
	}
	want, err := filepath.EvalSymlinks(realDir)
	if err != nil {
		t.Fatal(err)
	}
	if string(out) != want {
		t.Fatalf("smoke root %q retains a symlink; want physical directory %q", out, want)
	}
}
