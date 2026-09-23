package gitx

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
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

// unsetEnvForTest unsets each of keys for the duration of t, restoring
// whatever value (or absence) each one had beforehand once t finishes.
// Mutating process env like this is safe here because these tests never run
// in parallel with each other.
func unsetEnvForTest(t *testing.T, keys ...string) {
	t.Helper()
	for _, k := range keys {
		old, had := os.LookupEnv(k)
		if err := os.Unsetenv(k); err != nil {
			t.Fatalf("unset %s: %v", k, err)
		}
		t.Cleanup(func() {
			if had {
				os.Setenv(k, old)
			} else {
				os.Unsetenv(k)
			}
		})
	}
}

// TestCheckIdentityFailsWithoutIdentity forces git into a state where it
// cannot resolve any author/committer identity - no env vars, and a global
// config that declares user.useConfigOnly=true with no [user] name/email -
// and asserts CheckIdentity turns that into an actionable IDENTITY_REQUIRED
// error rather than a bare git failure.
//
// useConfigOnly is required for this to be deterministic: without it, git
// may auto-detect an identity from the machine's username/hostname instead
// of failing, on some machines.
func TestCheckIdentityFailsWithoutIdentity(t *testing.T) {
	dir := t.TempDir()
	if _, err := Run(dir, "init", "-b", "main"); err != nil {
		t.Fatalf("git init: %v", err)
	}

	cfgPath := filepath.Join(t.TempDir(), "gitconfig-no-identity")
	if err := os.WriteFile(cfgPath, []byte("[user]\n\tuseConfigOnly = true\n"), 0o644); err != nil {
		t.Fatalf("write global config: %v", err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", cfgPath)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	unsetEnvForTest(t,
		"GIT_AUTHOR_NAME", "GIT_AUTHOR_EMAIL", "GIT_AUTHOR_DATE",
		"GIT_COMMITTER_NAME", "GIT_COMMITTER_EMAIL", "GIT_COMMITTER_DATE",
	)

	err := CheckIdentity(dir)
	var ae *axi.Error
	if !errors.As(err, &ae) {
		t.Fatalf("CheckIdentity = %v (%T), want *axi.Error", err, err)
	}
	if ae.Code != "IDENTITY_REQUIRED" {
		t.Fatalf("code = %q, want IDENTITY_REQUIRED", ae.Code)
	}
	if len(ae.Help) == 0 {
		t.Fatal("expected help lines telling the operator how to configure an identity, got none")
	}
}

// TestCheckIdentitySucceedsWithConfiguredIdentity is the control: a repo
// with an ordinary repo-level identity configured must pass.
func TestCheckIdentitySucceedsWithConfiguredIdentity(t *testing.T) {
	dir := initRepo(t, "https://example.invalid/fake/repo.git")
	if err := CheckIdentity(dir); err != nil {
		t.Fatalf("CheckIdentity = %v, want nil", err)
	}
}

// TestIdentityEnvResolvesDirsOwnIdentity checks that IdentityEnv reads the
// identity configured in dir itself - a repo-local user.name/user.email -
// rather than any ambient process environment, and renders it as plain
// GIT_AUTHOR_*/GIT_COMMITTER_* env entries with no dates.
func TestIdentityEnvResolvesDirsOwnIdentity(t *testing.T) {
	unsetEnvForTest(t,
		"GIT_AUTHOR_NAME", "GIT_AUTHOR_EMAIL", "GIT_AUTHOR_DATE",
		"GIT_COMMITTER_NAME", "GIT_COMMITTER_EMAIL", "GIT_COMMITTER_DATE",
	)
	dir := initRepo(t, "https://example.invalid/fake/repo.git")

	env, err := IdentityEnv(dir)
	if err != nil {
		t.Fatalf("IdentityEnv: %v", err)
	}
	want := []string{
		"GIT_AUTHOR_NAME=jig-fixture",
		"GIT_AUTHOR_EMAIL=fixture@example.invalid",
		"GIT_COMMITTER_NAME=jig-fixture",
		"GIT_COMMITTER_EMAIL=fixture@example.invalid",
	}
	if len(env) != len(want) {
		t.Fatalf("IdentityEnv = %v, want %v", env, want)
	}
	for i := range want {
		if env[i] != want[i] {
			t.Errorf("IdentityEnv[%d] = %q, want %q", i, env[i], want[i])
		}
	}

	// The env it returns must actually steer a commit made elsewhere: a
	// second, identity-less repo commits with dir's identity when given
	// this env, proving the whole point of resolving it in one directory
	// to use in another.
	other := t.TempDir()
	if _, err := Run(other, "init", "-b", "main"); err != nil {
		t.Fatalf("git init: %v", err)
	}
	if _, err := RunEnv(other, env, "commit", "--allow-empty", "-m", "x"); err != nil {
		t.Fatalf("commit with resolved env: %v", err)
	}
	got, err := Run(other, "log", "-1", "--format=%an <%ae> / %cn <%ce>")
	if err != nil {
		t.Fatalf("log: %v", err)
	}
	if wantLine := "jig-fixture <fixture@example.invalid> / jig-fixture <fixture@example.invalid>"; got != wantLine {
		t.Fatalf("commit identity = %q, want %q", got, wantLine)
	}
}

// TestIdentityRequiredErrorIsOneLine checks that a missing identity's error
// message stays to one line (no embedded multi-line git advice dump) and
// that its help lines use jig's own "Run `...`" hint style.
func TestIdentityRequiredErrorIsOneLine(t *testing.T) {
	dir := t.TempDir()
	if _, err := Run(dir, "init", "-b", "main"); err != nil {
		t.Fatalf("git init: %v", err)
	}
	cfgPath := filepath.Join(t.TempDir(), "gitconfig-no-identity")
	if err := os.WriteFile(cfgPath, []byte("[user]\n\tuseConfigOnly = true\n"), 0o644); err != nil {
		t.Fatalf("write global config: %v", err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", cfgPath)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	unsetEnvForTest(t,
		"GIT_AUTHOR_NAME", "GIT_AUTHOR_EMAIL", "GIT_AUTHOR_DATE",
		"GIT_COMMITTER_NAME", "GIT_COMMITTER_EMAIL", "GIT_COMMITTER_DATE",
	)

	_, err := IdentityEnv(dir)
	var ae *axi.Error
	if !errors.As(err, &ae) {
		t.Fatalf("IdentityEnv = %v (%T), want *axi.Error", err, err)
	}
	if strings.Count(ae.Msg, "\n") != 0 {
		t.Fatalf("Msg = %q, want a single line", ae.Msg)
	}
	if !strings.Contains(ae.Msg, dir) {
		t.Errorf("Msg = %q, want it to name %q", ae.Msg, dir)
	}
	for _, h := range ae.Help {
		if !strings.HasPrefix(h, "Run `") {
			t.Errorf("help line %q, want jig's \"Run `...`\" hint style", h)
		}
	}
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

// TestIsAncestor exercises all three outcomes: true, false (a valid but
// unrelated commit), and error (an unknown object).
func TestIsAncestor(t *testing.T) {
	dir := t.TempDir()
	run := func(args ...string) string {
		t.Helper()
		out, err := Run(dir, args...)
		if err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
		return out
	}
	run("init", "-b", "main")
	run("config", "user.name", "jig-fixture")
	run("config", "user.email", "fixture@example.invalid")
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("1"), 0o644); err != nil {
		t.Fatalf("write f.txt: %v", err)
	}
	run("add", "-A")
	run("commit", "-m", "c1")
	c1, err := RevParse(dir, "HEAD")
	if err != nil {
		t.Fatalf("rev-parse c1: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("2"), 0o644); err != nil {
		t.Fatalf("write f.txt: %v", err)
	}
	run("add", "-A")
	run("commit", "-m", "c2")
	c2, err := RevParse(dir, "HEAD")
	if err != nil {
		t.Fatalf("rev-parse c2: %v", err)
	}

	t.Run("true", func(t *testing.T) {
		ok, err := IsAncestor(dir, c1, c2)
		if err != nil {
			t.Fatalf("IsAncestor: %v", err)
		}
		if !ok {
			t.Fatal("IsAncestor(c1, c2) = false, want true")
		}
	})

	t.Run("false", func(t *testing.T) {
		ok, err := IsAncestor(dir, c2, c1)
		if err != nil {
			t.Fatalf("IsAncestor: %v", err)
		}
		if ok {
			t.Fatal("IsAncestor(c2, c1) = true, want false")
		}
	})

	t.Run("error on unknown object", func(t *testing.T) {
		_, err := IsAncestor(dir, "0000000000000000000000000000000000000000", c2)
		if err == nil {
			t.Fatal("IsAncestor with an unknown object: expected an error, got nil")
		}
	})
}

// TestDiffNameOnly checks the changed/deleted split a rename produces under
// --no-renames (the old path deleted, the new path added), and that an
// unrestricted, unmodified range returns nothing.
func TestDiffNameOnly(t *testing.T) {
	dir := t.TempDir()
	run := func(args ...string) string {
		t.Helper()
		out, err := Run(dir, args...)
		if err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
		return out
	}
	run("init", "-b", "main")
	run("config", "user.name", "jig-fixture")
	run("config", "user.email", "fixture@example.invalid")
	writeFile := func(rel, content string) {
		t.Helper()
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatalf("mkdir for %s: %v", rel, err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	writeFile("keep.txt", "1")
	writeFile("old.txt", "will be renamed")
	run("add", "-A")
	run("commit", "-m", "c1")
	base, err := RevParse(dir, "HEAD")
	if err != nil {
		t.Fatalf("rev-parse base: %v", err)
	}

	if err := os.Rename(filepath.Join(dir, "old.txt"), filepath.Join(dir, "new.txt")); err != nil {
		t.Fatalf("rename: %v", err)
	}
	writeFile("keep.txt", "2")
	writeFile("added.txt", "brand new")
	run("add", "-A")
	run("commit", "-m", "c2")
	head, err := RevParse(dir, "HEAD")
	if err != nil {
		t.Fatalf("rev-parse head: %v", err)
	}

	changed, err := DiffNameOnly(dir, base, head, "AMT")
	if err != nil {
		t.Fatalf("DiffNameOnly AMT: %v", err)
	}
	wantChanged := map[string]bool{"keep.txt": true, "added.txt": true, "new.txt": true}
	if len(changed) != len(wantChanged) {
		t.Fatalf("changed = %v, want exactly %v", changed, wantChanged)
	}
	for _, f := range changed {
		if !wantChanged[f] {
			t.Errorf("unexpected changed file %q", f)
		}
	}

	deleted, err := DiffNameOnly(dir, base, head, "D")
	if err != nil {
		t.Fatalf("DiffNameOnly D: %v", err)
	}
	if len(deleted) != 1 || deleted[0] != "old.txt" {
		t.Fatalf("deleted = %v, want [old.txt]", deleted)
	}

	same, err := DiffNameOnly(dir, head, head, "AMT")
	if err != nil {
		t.Fatalf("DiffNameOnly on an empty range: %v", err)
	}
	if len(same) != 0 {
		t.Fatalf("DiffNameOnly on an empty range = %v, want none", same)
	}
}

// TestFileExistsAtRev checks a present file, an absent one, a deleted one
// (present at base, gone at head), a directory (a tree, not a blob), and a
// rev that does not resolve to a commit.
func TestFileExistsAtRev(t *testing.T) {
	dir := t.TempDir()
	run := func(args ...string) string {
		t.Helper()
		out, err := Run(dir, args...)
		if err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
		return out
	}
	run("init", "-b", "main")
	run("config", "user.name", "jig-fixture")
	run("config", "user.email", "fixture@example.invalid")
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatalf("mkdir sub: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "gone.txt"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write gone.txt: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sub", "f.txt"), []byte("z"), 0o644); err != nil {
		t.Fatalf("write sub/f.txt: %v", err)
	}
	run("add", "-A")
	run("commit", "-m", "c1")
	base, err := RevParse(dir, "HEAD")
	if err != nil {
		t.Fatalf("rev-parse base: %v", err)
	}
	run("rm", "gone.txt")
	if err := os.WriteFile(filepath.Join(dir, "here.txt"), []byte("y"), 0o644); err != nil {
		t.Fatalf("write here.txt: %v", err)
	}
	run("add", "-A")
	run("commit", "-m", "c2")
	head, err := RevParse(dir, "HEAD")
	if err != nil {
		t.Fatalf("rev-parse head: %v", err)
	}

	cases := []struct {
		rev, path string
		want      bool
	}{
		{head, "here.txt", true},
		{head, "gone.txt", false},
		{head, "never.txt", false},
		{base, "gone.txt", true},
		{head, "sub", false},           // a directory is a tree, not a blob
		{head, "sub/", false},          // a directory lists its children; it is still not a file
		{head, "sub/f.txt", true},      // a nested path
		{head, "sub/never.txt", false}, // a missing path under an existing directory
		{head, ".", false},             // the repo root is a tree
		{head, ":/here.txt", false},    // pathspec magic stays a literal path, which does not exist
		{head, "he*.txt", false},       // a glob stays a literal path, which does not exist
	}
	for _, c := range cases {
		got, err := FileExistsAtRev(dir, c.rev, c.path)
		if err != nil {
			t.Fatalf("FileExistsAtRev(%s, %s): %v", c.rev, c.path, err)
		}
		if got != c.want {
			t.Errorf("FileExistsAtRev(%s, %s) = %v, want %v", c.rev, c.path, got, c.want)
		}
	}

	if _, err := FileExistsAtRev(dir, "deadbeefdeadbeefdeadbeefdeadbeefdeadbeef", "here.txt"); err == nil {
		t.Error("FileExistsAtRev with a bad rev: want an error, got nil")
	}
}

// TestFileExistsAtRevIgnoresTheWorkingTree checks the wedge scenario a
// message-text match on git's cat-file output used to fall into: a path
// that git ignores, so it is never tracked at any rev, but that happens to
// sit on disk right now (for example an oracle regenerated it in a lease).
// The working tree must never make an absent path look present, or the
// reverse.
func TestFileExistsAtRevIgnoresTheWorkingTree(t *testing.T) {
	dir := t.TempDir()
	run := func(args ...string) string {
		t.Helper()
		out, err := Run(dir, args...)
		if err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
		return out
	}
	run("init", "-b", "main")
	run("config", "user.name", "jig-fixture")
	run("config", "user.email", "fixture@example.invalid")
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("gen.txt\n"), 0o644); err != nil {
		t.Fatalf("write .gitignore: %v", err)
	}
	run("add", "-A")
	run("commit", "-m", "c1")
	head, err := RevParse(dir, "HEAD")
	if err != nil {
		t.Fatalf("rev-parse head: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "gen.txt"), []byte("regenerated"), 0o644); err != nil {
		t.Fatalf("write gen.txt: %v", err)
	}

	got, err := FileExistsAtRev(dir, head, "gen.txt")
	if err != nil {
		t.Fatalf("FileExistsAtRev(gen.txt), ignored on disk: %v", err)
	}
	if got {
		t.Error("FileExistsAtRev(gen.txt) = true, want false: it is untracked at head regardless of the working tree")
	}
}
