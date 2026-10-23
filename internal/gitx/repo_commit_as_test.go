package gitx

import (
	"errors"
	"testing"
)

// TestCommitAllAsAsksForTheIdentityOnlyToCommit: the lookup runs once for a
// commit and not at all when there is nothing to commit (a clean tree, or one
// with only an ignored file), and the commit it makes carries the identity it
// returned as author and committer.
func TestCommitAllAsAsksForTheIdentityOnlyToCommit(t *testing.T) {
	dir := newStoreRepo(t)
	asked := 0
	who := func() (Identity, error) {
		asked++
		return Identity{Name: "Store Person", Email: "store.person@example.invalid"}, nil
	}

	committed, err := openRepo(t, dir).CommitAllAs("nothing", who)
	if err != nil || committed || asked != 0 {
		t.Fatalf("CommitAllAs on a clean tree = %v, %v after %d lookups; want no commit and no lookup", committed, err, asked)
	}
	writeFileT(t, dir, "journal.lock", "held")
	committed, err = openRepo(t, dir).CommitAllAs("nothing", who)
	if err != nil || committed || asked != 0 {
		t.Fatalf("CommitAllAs with only an ignored file = %v, %v after %d lookups; want no commit and no lookup", committed, err, asked)
	}

	writeFileT(t, dir, "note.txt", "hello\n")
	committed, err = openRepo(t, dir).CommitAllAs("jig: record", who)
	if err != nil || !committed || asked != 1 {
		t.Fatalf("CommitAllAs with a new file = %v, %v after %d lookups; want a commit and one lookup", committed, err, asked)
	}
	const ident = "Store Person <store.person@example.invalid>"
	if got := mustRun(t, dir, "log", "-1", "--format=%an <%ae> / %cn <%ce>"); got != ident+" / "+ident {
		t.Fatalf("commit = %q, want the looked-up identity as author and committer", got)
	}
}

// TestCommitAllAsLeavesTheStoreAloneWhenTheIdentityFails: a lookup that
// fails fails the commit with its own error, and the branch, the index and
// the work tree are as they were, so the same change commits once the lookup
// works.
func TestCommitAllAsLeavesTheStoreAloneWhenTheIdentityFails(t *testing.T) {
	dir := newStoreRepo(t)
	writeFileT(t, dir, "note.txt", "hello\n")
	head := mustRun(t, dir, "rev-parse", "HEAD")
	staged := mustRun(t, dir, "diff", "--cached", "--name-only")

	refused := errors.New("no identity")
	committed, err := openRepo(t, dir).CommitAllAs("jig: record", func() (Identity, error) { return Identity{}, refused })
	if !errors.Is(err, refused) || committed {
		t.Fatalf("CommitAllAs with a failing lookup = %v, %v; want %v and no commit", committed, err, refused)
	}
	if got := mustRun(t, dir, "rev-parse", "HEAD"); got != head {
		t.Fatalf("HEAD moved to %s, want %s", got, head)
	}
	if got := mustRun(t, dir, "diff", "--cached", "--name-only"); got != staged {
		t.Fatalf("staged files = %q, want %q: the failed commit staged its change", got, staged)
	}

	committed, err = openRepo(t, dir).CommitAllAs("jig: record", func() (Identity, error) {
		return Identity{Name: "Store Person", Email: "store.person@example.invalid"}, nil
	})
	if err != nil || !committed {
		t.Fatalf("CommitAllAs after the failure = %v, %v; want a commit", committed, err)
	}
	mustRun(t, dir, "fsck", "--strict")
}
