package store

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/axi"
)

const (
	personName  = "Store Person"
	personEmail = "store.person@example.invalid"
	personIdent = personName + " <" + personEmail + ">"
)

// commitPaths are the two ways a store commit runs: in process, on a store
// shaped as jig creates one, and by the git program, on a store that says
// nothing about conversion (its .gitattributes is gone).
var commitPaths = []struct {
	name    string
	program bool
}{
	{"in process", false},
	{"git program", true},
}

// newPersonStore is newTestRemoteStore with the clone's identity set to the
// person, and, when program is true, its .gitattributes removed so every
// store step runs by the git program.
func newPersonStore(t *testing.T, program bool) (st *Store, work, remote string) {
	t.Helper()
	st, work, remote = newTestRemoteStore(t)
	if program {
		runGit(t, work, "rm", "-q", ".gitattributes")
		runGit(t, work, "commit", "-q", "-m", "no attributes")
	}
	runGit(t, work, "config", "user.name", personName)
	runGit(t, work, "config", "user.email", personEmail)
	return st, work, remote
}

// lastCommitIdent returns the author and committer of work's HEAD, as
// "Name <email> / Name <email>".
func lastCommitIdent(t *testing.T, work string) string {
	t.Helper()
	return strings.TrimSpace(runGit(t, work, "log", "-1", "--pretty=%an <%ae> / %cn <%ce>"))
}

func writeStoreFile(t *testing.T, work, name string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(work, name), []byte(name+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

// requireIdentityRequired fails the test unless err is the IDENTITY_REQUIRED
// refusal with both of its help lines.
func requireIdentityRequired(t *testing.T, what string, err error) {
	t.Helper()
	var ae *axi.Error
	if !errors.As(err, &ae) || ae.Code != "IDENTITY_REQUIRED" {
		t.Fatalf("%s = %v, want the IDENTITY_REQUIRED refusal", what, err)
	}
	if len(ae.Help) != 2 || !strings.Contains(ae.Help[0], "user.name") || !strings.Contains(ae.Help[1], "user.email") {
		t.Fatalf("%s help = %q, want the user.name and user.email commands", what, ae.Help)
	}
}

// claimTicket mints a ticket through Claim, as `jig ticket new` does, and
// counts how many times Claim called its write.
func claimTicket(st *Store, calls *int) (string, error) {
	return st.Claim(
		func() (string, []string, error) {
			*calls++
			id, err := st.Mint("JIG", Ticket{Title: "x"})
			if err != nil {
				return "", nil, err
			}
			return id, []string{st.TicketRelDir(id)}, nil
		},
		func(id string) string { return id + ": new ticket" },
	)
}

// TestStoreCommitsAreAuthoredByTheClonesIdentity: every commit the store
// makes (Push's, Sync's of leftovers and Claim's) is authored and committed
// by the identity git resolves in the store clone, on both the in-process
// path and the git program's, and none is jig's own.
func TestStoreCommitsAreAuthoredByTheClonesIdentity(t *testing.T) {
	for _, path := range commitPaths {
		t.Run(path.name, func(t *testing.T) {
			st, work, _ := newPersonStore(t, path.program)
			want := personIdent + " / " + personIdent

			writeStoreFile(t, work, "pushed.txt")
			if err := st.Push("push a file"); err != nil {
				t.Fatalf("Push: %v", err)
			}
			if got := lastCommitIdent(t, work); got != want {
				t.Errorf("Push's commit = %q, want %q", got, want)
			}

			writeStoreFile(t, work, "leftover.txt")
			if err := st.Sync(); err != nil {
				t.Fatalf("Sync: %v", err)
			}
			if subject := strings.TrimSpace(runGit(t, work, "log", "-1", "--pretty=%s")); subject != "jig: record uncommitted store state" {
				t.Fatalf("Sync did not commit the leftover: HEAD is %q", subject)
			}
			if got := lastCommitIdent(t, work); got != want {
				t.Errorf("Sync's commit of leftovers = %q, want %q", got, want)
			}

			calls := 0
			id, err := claimTicket(st, &calls)
			if err != nil {
				t.Fatalf("Claim: %v", err)
			}
			if got := lastCommitIdent(t, work); got != want {
				t.Errorf("Claim's commit for %s = %q, want %q", id, got, want)
			}
		})
	}
}

// TestStoreRefusesToCommitWithoutAnIdentity: a clone with no identity, where
// git may not guess one either, is refused with IDENTITY_REQUIRED when a
// command has something to commit, and the store is left as it was; a
// command with nothing to commit needs no identity, and Claim, which always
// commits, is refused before its write puts anything on disk.
func TestStoreRefusesToCommitWithoutAnIdentity(t *testing.T) {
	for _, path := range commitPaths {
		t.Run(path.name, func(t *testing.T) {
			st, work, _ := newPersonStore(t, path.program)
			runGit(t, work, "config", "--unset", "user.name")
			runGit(t, work, "config", "--unset", "user.email")
			runGit(t, work, "config", "user.useConfigOnly", "true")
			head := strings.TrimSpace(runGit(t, work, "rev-parse", "HEAD"))

			if err := st.Sync(); err != nil {
				t.Fatalf("Sync on a clean store = %v, want nothing to commit and no identity needed", err)
			}
			if err := st.Push("nothing"); err != nil {
				t.Fatalf("Push on a clean store = %v, want nothing to commit and no identity needed", err)
			}

			calls := 0
			_, err := claimTicket(st, &calls)
			requireIdentityRequired(t, "Claim", err)
			if calls != 0 {
				t.Errorf("Claim called write %d times, want it refused before write", calls)
			}
			if _, err := os.Stat(st.TicketDir("JIG-1")); !os.IsNotExist(err) {
				t.Errorf("Claim left a ticket folder behind: %v", err)
			}

			writeStoreFile(t, work, "stray.txt")
			requireIdentityRequired(t, "Push of a dirty store", st.Push("a file"))
			requireIdentityRequired(t, "Sync of a dirty store", st.Sync())
			if got := strings.TrimSpace(runGit(t, work, "rev-parse", "HEAD")); got != head {
				t.Errorf("HEAD moved to %s, want %s: a refused command committed", got, head)
			}

			// The refusal is not kept: once the person sets an identity, the
			// same Store commits as them.
			runGit(t, work, "config", "user.name", personName)
			runGit(t, work, "config", "user.email", personEmail)
			if err := st.Push("a file"); err != nil {
				t.Fatalf("Push after the identity was set: %v", err)
			}
			if got := lastCommitIdent(t, work); got != personIdent+" / "+personIdent {
				t.Errorf("commit after the identity was set = %q", got)
			}
		})
	}
}

// TestStoreKeepsTheIdentityItResolved: a Store resolves the identity once, on
// its first commit, and keeps it; a Store opened afterwards resolves afresh.
func TestStoreKeepsTheIdentityItResolved(t *testing.T) {
	st, work, _ := newPersonStore(t, false)

	writeStoreFile(t, work, "first.txt")
	if err := st.Push("first"); err != nil {
		t.Fatalf("Push: %v", err)
	}
	runGit(t, work, "config", "user.name", "Someone Else")
	writeStoreFile(t, work, "second.txt")
	if err := st.Push("second"); err != nil {
		t.Fatalf("Push: %v", err)
	}
	if got := lastCommitIdent(t, work); got != personIdent+" / "+personIdent {
		t.Errorf("second commit of the same Store = %q, want the identity it resolved first", got)
	}

	reopened, err := Open(work)
	if err != nil {
		t.Fatal(err)
	}
	writeStoreFile(t, work, "third.txt")
	if err := reopened.Push("third"); err != nil {
		t.Fatalf("Push: %v", err)
	}
	want := "Someone Else <" + personEmail + ">"
	if got := lastCommitIdent(t, work); got != want+" / "+want {
		t.Errorf("commit of a reopened Store = %q, want %q for both", got, want)
	}
}
