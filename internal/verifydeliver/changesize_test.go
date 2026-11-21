package verifydeliver

import (
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/journal"
)

// sizedBranch makes a clone of a repo whose main holds f.txt (3 bytes) and
// h.txt (11 bytes), and a branch off it that modifies f.txt, adds an 8 byte
// file and deletes h.txt. It returns the clone, with origin fetched.
func sizedBranch(t *testing.T, added string) string {
	t.Helper()
	repo, remote := tinyRepo(t)
	writeAndCommit(t, repo, "h.txt", "hello world", "add h")
	run(t, repo, "push", "origin", "main")
	clone := cloneFrom(t, remote)
	run(t, clone, "checkout", "-b", "work")
	writeAndCommit(t, clone, "f.txt", "one, changed a lot", "modify f")
	writeAndCommit(t, clone, "new.txt", added, "add new")
	run(t, clone, "rm", "h.txt")
	run(t, clone, "commit", "-m", "delete h")
	// The target moves on after the branch left it: its change is not the branch's.
	other := cloneFrom(t, remote)
	writeAndCommit(t, other, "elsewhere.txt", "upstream work that is no part of the change", "upstream")
	run(t, other, "push", "origin", "main")
	run(t, clone, "fetch", "origin")
	return clone
}

// TestPublishChangeSizeCountsFilesAndBytesAtTheBase: the size of a change is
// the files it touched and their bytes at the merge base with the base
// branch: a modified file counts with its old size, a deleted one with the
// size it had, an added one with 0, and what the base gained since the branch
// left it is no part of it.
func TestPublishChangeSizeCountsFilesAndBytesAtTheBase(t *testing.T) {
	t.Parallel()
	clone := sizedBranch(t, "12345678")
	got, err := changeSize([]repoSpan{{dir: clone, base: "main", head: "work"}})
	if err != nil {
		t.Fatalf("changeSize: %v", err)
	}
	if got.Files != 3 || got.Bytes != 3+11 {
		t.Errorf("size = %+v, want 3 files and 14 bytes (f.txt 3, h.txt 11, new.txt 0)", got)
	}
}

// TestPublishChangeSizeSumsTheRepos: a ticket in several repos has one size,
// the sum of its repos' files and of their bytes.
func TestPublishChangeSizeSumsTheRepos(t *testing.T) {
	t.Parallel()
	a, b := sizedBranch(t, "1"), sizedBranch(t, "22")
	// A second change in b: one more file that exists at its base.
	one, err := changeSize([]repoSpan{{dir: a, base: "main", head: "work"}})
	if err != nil {
		t.Fatalf("changeSize a: %v", err)
	}
	run(t, b, "checkout", "work")
	writeAndCommit(t, b, "extra.txt", "x", "extra")
	both, err := changeSize([]repoSpan{{dir: a, base: "main", head: "work"}, {dir: b, base: "main", head: "work"}})
	if err != nil {
		t.Fatalf("changeSize both: %v", err)
	}
	if want := (journal.ChangeSize{Files: one.Files + 4, Bytes: one.Bytes + 14}); both != want {
		t.Errorf("two repos = %+v, want %+v", both, want)
	}
}

// TestPublishChangeSizeIsJournaledWithTheDoneLine: a publish writes the size
// of what it shipped on its publish-done line.
func TestPublishChangeSizeIsJournaledWithTheDoneLine(t *testing.T) {
	t.Parallel()
	fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
	d := newDeps(t, fx)
	useGithubHost(t, &d, "", "")
	gateToClean(t, fx, d)
	if _, err := Publish(d, PublishOpts{Ticket: fx.Ticket, Yes: true}); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	lines := journalFrom(t, d, fx.Ticket, 0)
	var done []journal.Line
	for _, l := range lines {
		if l.Event == journal.EventPublishDone {
			done = append(done, l)
		}
	}
	if len(done) != 1 || done[0].Change == nil {
		t.Fatalf("publish-done lines = %+v, want one with a change size", done)
	}
	// The same count, read the way a person would: the files the pushed
	// branch changes against main.
	run(t, fx.RepoDir, "fetch", "origin")
	names := run(t, fx.RepoDir, "diff", "--name-only", "--no-renames", "origin/main...origin/"+fx.Branch)
	if want := len(strings.Split(names, "\n")); done[0].Change.Files != want || want < 1 {
		t.Errorf("change = %+v, want %d files (%s)", *done[0].Change, want, names)
	}
}
