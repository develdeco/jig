package store

import (
	"testing"

	"github.com/develdeco/jig/internal/gitx"
)

// TestStoresOfOneRepoUnderOneMemoResolveTheIdentityOnce: a command that makes
// several Stores of one repository asks git for the identity once, and every
// commit is still the person's.
func TestStoresOfOneRepoUnderOneMemoResolveTheIdentityOnce(t *testing.T) {
	st, work, _ := newPersonStore(t, false)
	m := gitx.NewMemo()
	st.Memo = m
	second, err := Open(work)
	if err != nil {
		t.Fatal(err)
	}
	second.Memo = m

	writeStoreFile(t, work, "first.txt")
	if err := st.Push("first"); err != nil {
		t.Fatalf("Push: %v", err)
	}
	asked := m.Asked()
	if asked == 0 {
		t.Fatal("the first commit asked the memo nothing: the identity is not read through it")
	}
	writeStoreFile(t, work, "second.txt")
	if err := second.Push("second"); err != nil {
		t.Fatalf("Push through the second Store: %v", err)
	}
	if got := m.Asked(); got != asked {
		t.Errorf("the second Store's commit asked git %d more questions, want none: the identity is already known", got-asked)
	}
	if got := lastCommitIdent(t, work); got != personIdent+" / "+personIdent {
		t.Errorf("commit through the second Store = %q", got)
	}
}

// TestAStoreUnderAMemoStillNeedsAnIdentity: the memo changes nobody's answer:
// a clone with no identity is refused with IDENTITY_REQUIRED, every time, and
// commits as the person once one is set.
func TestAStoreUnderAMemoStillNeedsAnIdentity(t *testing.T) {
	st, work, _ := newPersonStore(t, false)
	st.Memo = gitx.NewMemo()
	runGit(t, work, "config", "--unset", "user.name")
	runGit(t, work, "config", "--unset", "user.email")
	runGit(t, work, "config", "user.useConfigOnly", "true")

	writeStoreFile(t, work, "stray.txt")
	requireIdentityRequired(t, "Push of a dirty store", st.Push("a file"))
	requireIdentityRequired(t, "Push of a dirty store, again", st.Push("a file"))

	runGit(t, work, "config", "user.name", personName)
	runGit(t, work, "config", "user.email", personEmail)
	if err := st.Push("a file"); err != nil {
		t.Fatalf("Push after the identity was set: %v", err)
	}
	if got := lastCommitIdent(t, work); got != personIdent+" / "+personIdent {
		t.Errorf("commit after the identity was set = %q", got)
	}
}
