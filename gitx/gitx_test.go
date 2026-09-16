package gitx

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/develdeco/jig/axi"
)

func TestIsLocalRemote(t *testing.T) {
	dir := t.TempDir()
	cases := []struct {
		url  string
		want bool
	}{
		{`C:\x\y`, true},
		{"/tmp/x", true},
		{"file://x", true},
		{"https://github.com/x", false},
		{"git@github.com:x", false},
		{"ssh://x", false},
		{dir, true}, // existing directory path
	}
	for _, c := range cases {
		if got := IsLocalRemote(c.url); got != c.want {
			t.Errorf("IsLocalRemote(%q) = %v, want %v", c.url, got, c.want)
		}
	}
}

// initRepo creates a bare-bones git repo at dir with the given remote URL
// registered as "origin", without ever contacting the network.
func initRepo(t *testing.T, remote string) string {
	t.Helper()
	dir := t.TempDir()
	if _, err := Run(dir, "init", "-b", "main"); err != nil {
		t.Fatalf("git init: %v", err)
	}
	if _, err := Run(dir, "config", "user.email", "fixture@example.invalid"); err != nil {
		t.Fatalf("git config email: %v", err)
	}
	if _, err := Run(dir, "config", "user.name", "jig-fixture"); err != nil {
		t.Fatalf("git config name: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	if _, err := Run(dir, "add", "-A"); err != nil {
		t.Fatalf("git add: %v", err)
	}
	if _, err := Run(dir, "commit", "-m", "init"); err != nil {
		t.Fatalf("git commit: %v", err)
	}
	if _, err := Run(dir, "remote", "add", "origin", remote); err != nil {
		t.Fatalf("git remote add: %v", err)
	}
	return dir
}

func TestGuardedPush(t *testing.T) {
	t.Run("non-local unconfirmed refuses", func(t *testing.T) {
		dir := initRepo(t, "https://example.invalid/fake/repo.git")
		err := GuardedPush(dir, "origin", "main", false)
		if err == nil {
			t.Fatal("expected refusal, got nil error")
		}
		var ae *axi.Error
		if !errors.As(err, &ae) {
			t.Fatalf("expected *axi.Error, got %T: %v", err, err)
		}
		if ae.Code != "PUSH_REFUSED" {
			t.Fatalf("code = %q, want PUSH_REFUSED", ae.Code)
		}
	})

	t.Run("non-local confirmed classifies as non-local without pushing", func(t *testing.T) {
		// Do not call GuardedPush with confirmed=true here: that would
		// attempt an actual network push to a fake remote. Assert only
		// the URL classifier that GuardedPush relies on for its decision.
		if IsLocalRemote("https://example.invalid/fake/repo.git") {
			t.Fatal("expected https URL to classify as non-local")
		}
	})

	t.Run("local remote pushes without confirmation", func(t *testing.T) {
		remoteDir := t.TempDir()
		if _, err := Run(remoteDir, "init", "--bare", "-b", "main"); err != nil {
			t.Fatalf("git init --bare: %v", err)
		}
		dir := initRepo(t, remoteDir)
		if err := GuardedPush(dir, "origin", "main", false); err != nil {
			t.Fatalf("GuardedPush to local remote: %v", err)
		}
	})
}
