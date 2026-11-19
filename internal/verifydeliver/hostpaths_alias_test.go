package verifydeliver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestAListedWorktreeIsAlsoSpelledThroughTheClonesAliases: git lists a
// worktree resolved, but a reviewer names it the way its machine spells the
// clone's path (through a symlink, or a Windows short name, of a directory
// above both), so a reviewer's words that name a worktree outside the clone in
// that spelling are left out of what reaches the store, as they are in git's.
func TestAListedWorktreeIsAlsoSpelledThroughTheClonesAliases(t *testing.T) {
	t.Parallel()
	base := t.TempDir()
	clone := filepath.Join(base, "clone")
	if err := os.Mkdir(clone, 0o755); err != nil {
		t.Fatal(err)
	}
	run(t, clone, "init", "-b", "main")
	run(t, clone, "config", "user.name", "jig-fixture")
	run(t, clone, "config", "user.email", "fixture@example.invalid")
	writeAndCommit(t, clone, "f.txt", "one", "init")
	run(t, clone, "worktree", "add", "--detach", filepath.Join(base, "harness-worktree"))
	alias := otherSpelling(t, base)

	dirs := reviewerHostDirs(RoundInput{Ticket: "T-1", CloneDir: filepath.Join(alias, "clone")})
	for _, spelled := range []string{
		filepath.Join(alias, "harness-worktree"),
		filepath.ToSlash(filepath.Join(alias, "harness-worktree")),
	} {
		words := "checked in " + spelled + "/f.txt"
		if got := leaveOutSessionHostPaths(words, dirs...); strings.Contains(got, spelled) || !strings.Contains(got, "<worktree>") {
			t.Errorf("the reviewer's words %q became %q, want the worktree left out as <worktree>", words, got)
		}
	}
}
