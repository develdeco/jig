package locate

import (
	"testing"

	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/project"
)

// TestLocateAsksGitOnceUnderAMemo: two Locate calls for one repository under
// one memo ask git for its top level and its origin once, not once per
// locator; the answer is what a call with no memo gives.
func TestLocateAsksGitOnceUnderAMemo(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	st := w.store("alpha-tickets", "alpha", "https://example.com/o/lib.git")
	w.mapProject("alpha", project.MachineProject{Store: st, Clones: map[string]string{}})
	fresh := w.repo("fresh", "git@example.com:o/lib")

	m := gitx.NewMemo()
	first := w.locate(fresh, Opts{Memo: m, NoMap: true})
	second := w.locate(fresh, Opts{Memo: m, NoMap: true})
	if first.How != HowMapRemote || second.How != HowMapRemote || first.Repo != "lib" || second.Repo != "lib" {
		t.Fatalf("how %q and %q, repo %q and %q; want map-remote and lib twice", first.How, second.How, first.Repo, second.Repo)
	}
	if got := m.Asked(); got != 2 {
		t.Errorf("two Locate calls asked git %d questions, want 2 (the top level and the origin, once each)", got)
	}
	if plain := w.locate(fresh, Opts{NoMap: true}); plain.How != first.How || plain.Repo != first.Repo || plain.RepoDir != first.RepoDir {
		t.Errorf("with no memo: %+v, with one: %+v", plain, first)
	}
}

// TestCloneRootAsksTheCommonDirOnce: a linked worktree's clone is found
// through git's common directory, asked once however often the clone is
// named.
func TestCloneRootAsksTheCommonDirOnce(t *testing.T) {
	t.Parallel()
	w := newWorld(t)
	clone := w.repo("clone", "")
	linked := w.path("linked")
	git(t, clone, "worktree", "add", "-q", "--detach", linked)

	m := gitx.NewMemo()
	first, second := CloneRoot(m, linked), CloneRoot(m, linked)
	if first != second || !gitx.SameDir(first, clone) {
		t.Fatalf("CloneRoot = %q then %q, want the clone %q", first, second, clone)
	}
	if got := m.Asked(); got != 1 {
		t.Errorf("naming the clone twice asked git %d questions, want 1", got)
	}
}
