package gitx

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/develdeco/jig/internal/axi"
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

// TestMaintenanceAutoPacksWhenThresholdMet forces gc.autoPackLimit's exact
// (non-sampled) pack-count check low, accumulates several small packs, and
// asserts MaintenanceAuto consolidates them - proving the "-c
// maintenance.auto=false" flag every gitx call carries does not suppress
// this explicit, synchronous invocation.
//
// Which maintenance task does the consolidating depends on the git version
// under test: git 2.54 made the geometric-repack task the default
// strategy, and that task is the one that packs here on a git that new; on
// an older git, the gc task does it instead, by shelling out to "git gc
// --auto" (kept synchronous by the gc.autoDetach=false MaintenanceAuto
// itself sets). gc.auto is left at its default rather than forced to 0:
// 0 disables the gc task's pack-count check entirely, which would silently
// stop this test from exercising the older-git path.
func TestMaintenanceAutoPacksWhenThresholdMet(t *testing.T) {
	dir := t.TempDir()
	if _, err := Run(dir, "init", "-b", "main"); err != nil {
		t.Fatalf("git init: %v", err)
	}
	if _, err := Run(dir, "config", "gc.autoPackLimit", "1"); err != nil {
		t.Fatalf("git config gc.autoPackLimit: %v", err)
	}
	for i := 0; i < 5; i++ {
		name := filepath.Join(dir, "f"+string(rune('a'+i))+".txt")
		if err := os.WriteFile(name, []byte("x"), 0o644); err != nil {
			t.Fatalf("write file: %v", err)
		}
		if _, err := Run(dir, "add", "-A"); err != nil {
			t.Fatalf("git add: %v", err)
		}
		if _, err := RunEnv(dir, []string{"GIT_AUTHOR_NAME=jig-fixture", "GIT_AUTHOR_EMAIL=fixture@example.invalid", "GIT_COMMITTER_NAME=jig-fixture", "GIT_COMMITTER_EMAIL=fixture@example.invalid"}, "commit", "-m", "c"); err != nil {
			t.Fatalf("git commit: %v", err)
		}
		if _, err := Run(dir, "repack", "-d", "-q"); err != nil {
			t.Fatalf("git repack: %v", err)
		}
	}

	before := countPackFiles(t, dir)
	if before < 2 {
		t.Fatalf("setup: %d pack files before MaintenanceAuto, want >= 2 to prove consolidation", before)
	}

	if err := MaintenanceAuto(dir); err != nil {
		t.Fatalf("MaintenanceAuto: %v", err)
	}

	after := countPackFiles(t, dir)
	if after >= before {
		t.Fatalf("pack files after MaintenanceAuto = %d, want fewer than %d (threshold was met, so it should have done real work)", after, before)
	}
}

func countPackFiles(t *testing.T, dir string) int {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(dir, ".git", "objects", "pack", "*.pack"))
	if err != nil {
		t.Fatalf("glob pack files: %v", err)
	}
	return len(matches)
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
