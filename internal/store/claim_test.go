package store

import (
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/axi"
)

// TestClaimNoRemoteCommitsOnlyOncePerCall covers a standalone store (no
// origin): Claim commits write's own id and leaves any other dirty state in
// the store untouched, calling write exactly once.
func TestClaimNoRemoteCommitsOnlyOncePerCall(t *testing.T) {
	st, work := newTestStandaloneStore(t)

	if err := os.WriteFile(filepath.Join(work, "unrelated.txt"), []byte("stray\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	calls := 0
	id, err := st.Claim(
		func() (string, []string, error) {
			calls++
			id, err := st.Mint("JIG-{n}", Ticket{Title: "x"})
			if err != nil {
				return "", nil, err
			}
			return id, []string{id}, nil
		},
		func(id string) string { return id + ": new ticket" },
	)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if id != "JIG-1" {
		t.Fatalf("id = %q, want JIG-1", id)
	}
	if calls != 1 {
		t.Fatalf("write was called %d times, want exactly 1 (no origin to retry against)", calls)
	}

	subject := runGit(t, work, "log", "-1", "--pretty=%s")
	if strings.TrimSpace(subject) != "JIG-1: new ticket" {
		t.Fatalf("commit subject = %q, want %q", strings.TrimSpace(subject), "JIG-1: new ticket")
	}

	// The unrelated stray file is untouched: still on disk, still uncommitted.
	if _, err := os.Stat(filepath.Join(work, "unrelated.txt")); err != nil {
		t.Fatalf("Claim removed unrelated.txt: %v", err)
	}
	status := runGit(t, work, "status", "--porcelain")
	if !strings.Contains(status, "unrelated.txt") {
		t.Fatalf("status = %q, want unrelated.txt still showing as untracked (Claim must not sweep it in)", status)
	}
}

// TestClaimPushesAlone covers a store with an origin: a successful Claim
// lands exactly write's own path on the remote, leaving other dirty state in
// the local store untouched and off the remote.
func TestClaimPushesAlone(t *testing.T) {
	st, work, remote := newTestRemoteStore(t)

	if err := os.WriteFile(filepath.Join(work, "unrelated.txt"), []byte("stray\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	id, err := st.Claim(
		func() (string, []string, error) {
			id, err := st.Mint("JIG-{n}", Ticket{Title: "x"})
			if err != nil {
				return "", nil, err
			}
			return id, []string{id}, nil
		},
		func(id string) string { return id + ": new ticket" },
	)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if id != "JIG-1" {
		t.Fatalf("id = %q, want JIG-1", id)
	}

	subject := runGit(t, "", "--git-dir", remote, "log", "-1", "--pretty=%s")
	if strings.TrimSpace(subject) != "JIG-1: new ticket" {
		t.Fatalf("remote HEAD subject = %q, want %q", strings.TrimSpace(subject), "JIG-1: new ticket")
	}
	if _, err := os.Stat(filepath.Join(work, "unrelated.txt")); err != nil {
		t.Fatalf("Claim removed unrelated.txt: %v", err)
	}
	status := runGit(t, work, "status", "--porcelain")
	if !strings.Contains(status, "unrelated.txt") {
		t.Fatalf("status = %q, want unrelated.txt still showing as untracked", status)
	}
}

// TestClaimRemintsAfterRejectedPush reproduces two clones racing to claim an
// id: another clone pushes first, so this Claim's first push is rejected; it
// must undo its own commit, pull the other clone's work in, and land its
// claim on a second attempt - never a merge or rebase conflict, and never a
// leftover claim.
func TestClaimRemintsAfterRejectedPush(t *testing.T) {
	st, work, remote := newTestRemoteStore(t)

	other := t.TempDir()
	runGit(t, "", "clone", remote, other)
	runGit(t, other, "config", "user.name", "other")
	runGit(t, other, "config", "user.email", "other@example.invalid")
	if err := os.WriteFile(filepath.Join(other, "from-other.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, other, "add", "-A")
	runGit(t, other, "commit", "-m", "from other")
	runGit(t, other, "push", "origin", "main")

	calls := 0
	id, err := st.Claim(
		func() (string, []string, error) {
			calls++
			id, err := st.Mint("JIG-{n}", Ticket{Title: "x"})
			if err != nil {
				return "", nil, err
			}
			return id, []string{id}, nil
		},
		func(id string) string { return id + ": new ticket" },
	)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if id != "JIG-1" {
		t.Fatalf("id = %q, want JIG-1 (the other clone's commit touched no ticket folder)", id)
	}
	if calls != 2 {
		t.Fatalf("write was called %d times, want exactly 2 (one rejected, one that landed)", calls)
	}

	if _, err := os.Stat(filepath.Join(work, "from-other.txt")); err != nil {
		t.Fatalf("the other clone's commit was not pulled in: %v", err)
	}
	if _, err := os.Stat(st.TicketDir("JIG-1")); err != nil {
		t.Fatalf("JIG-1 missing after Claim: %v", err)
	}

	remoteLog := runGit(t, "", "--git-dir", remote, "log", "--pretty=%s")
	for _, want := range []string{"from other", "JIG-1: new ticket"} {
		if !strings.Contains(remoteLog, want) {
			t.Fatalf("remote log = %q, want it to contain %q", remoteLog, want)
		}
	}
	if n := strings.Count(remoteLog, "JIG-1: new ticket"); n != 1 {
		t.Fatalf("remote log has %d commits claiming JIG-1, want exactly 1 (the rejected attempt must leave nothing behind)", n)
	}

	status := runGit(t, work, "status", "--porcelain")
	if strings.TrimSpace(status) != "" {
		t.Fatalf("store left dirty after Claim: %q", status)
	}
}

// TestClaimGivesUpAfterMaxAttempts covers an origin that keeps moving out
// from under every push: write itself pushes a competing commit to the
// remote (standing in for another, faster writer) before every attempt, so
// every one of Claim's own pushes is rejected. Claim must give up after
// maxClaimAttempts, refuse with ID_NOT_CLAIMED, and leave no claim - no
// commit, no ticket folder - behind.
func TestClaimGivesUpAfterMaxAttempts(t *testing.T) {
	st, work, remote := newTestRemoteStore(t)

	racer := t.TempDir()
	runGit(t, "", "clone", remote, racer)
	runGit(t, racer, "config", "user.name", "racer")
	runGit(t, racer, "config", "user.email", "racer@example.invalid")

	calls := 0
	_, err := st.Claim(
		func() (string, []string, error) {
			calls++
			runGit(t, racer, "pull", "--rebase", "origin", "main")
			if err := os.WriteFile(filepath.Join(racer, "race.txt"), []byte(strings.Repeat("x", calls)), 0o644); err != nil {
				t.Fatal(err)
			}
			runGit(t, racer, "add", "-A")
			runGit(t, racer, "commit", "-m", "racer wins again")
			runGit(t, racer, "push", "origin", "main")

			id, merr := st.Mint("JIG-{n}", Ticket{Title: "x"})
			if merr != nil {
				return "", nil, merr
			}
			return id, []string{id}, nil
		},
		func(id string) string { return id + ": new ticket" },
	)
	if err == nil {
		t.Fatal("Claim: want an error once every push is rejected, got nil")
	}
	var ae *axi.Error
	if !errors.As(err, &ae) || ae.Code != "ID_NOT_CLAIMED" {
		t.Fatalf("err = %v, want *axi.Error ID_NOT_CLAIMED", err)
	}
	if calls != maxClaimAttempts {
		t.Fatalf("write was called %d times, want exactly maxClaimAttempts (%d)", calls, maxClaimAttempts)
	}

	// Every attempt's own commit was undone: neither the local branch nor
	// the remote carries any of our rejected claims.
	localLog := runGit(t, work, "log", "--pretty=%s")
	if strings.Contains(localLog, "new ticket") {
		t.Fatalf("local log = %q, want none of our own rejected claims left behind", localLog)
	}
	remoteLog := runGit(t, "", "--git-dir", remote, "log", "--pretty=%s")
	if strings.Contains(remoteLog, "new ticket") {
		t.Fatalf("remote log = %q, want none of our own rejected claims to have landed", remoteLog)
	}
	status := runGit(t, work, "status", "--porcelain")
	if strings.TrimSpace(status) != "" {
		t.Fatalf("store left dirty after Claim gave up: %q", status)
	}
	for i := 1; i <= maxClaimAttempts+1; i++ {
		id := "JIG-" + strconv.Itoa(i)
		if _, statErr := os.Stat(st.TicketDir(id)); statErr == nil {
			t.Fatalf("ticket folder %s was left behind after Claim gave up", id)
		}
	}
}

// TestClaimRefusesWhenOriginUnreachable covers a push that fails for a
// reason other than rejection (here: the push URL points nowhere): Claim
// tries exactly once, undoes its commit and the ticket folder it created,
// and refuses with ID_NOT_CLAIMED rather than retrying blind.
func TestClaimRefusesWhenOriginUnreachable(t *testing.T) {
	st, work, _ := newTestRemoteStore(t)
	runGit(t, work, "remote", "set-url", "--push", "origin", filepath.Join(t.TempDir(), "does-not-exist"))

	headBefore := strings.TrimSpace(runGit(t, work, "rev-parse", "HEAD"))

	calls := 0
	_, err := st.Claim(
		func() (string, []string, error) {
			calls++
			id, merr := st.Mint("JIG-{n}", Ticket{Title: "x"})
			if merr != nil {
				return "", nil, merr
			}
			return id, []string{id}, nil
		},
		func(id string) string { return id + ": new ticket" },
	)
	if err == nil {
		t.Fatal("Claim: want an error for an unreachable origin, got nil")
	}
	var ae *axi.Error
	if !errors.As(err, &ae) || ae.Code != "ID_NOT_CLAIMED" {
		t.Fatalf("err = %v, want *axi.Error ID_NOT_CLAIMED", err)
	}
	if len(ae.Help) == 0 || !strings.Contains(strings.ToLower(ae.Help[0]), "retry") {
		t.Fatalf("Help = %+v, want a line about retrying once the origin is reachable", ae.Help)
	}
	if calls != 1 {
		t.Fatalf("write was called %d times, want exactly 1 (an unreachable origin is not worth retrying blind)", calls)
	}

	headAfter := strings.TrimSpace(runGit(t, work, "rev-parse", "HEAD"))
	if headAfter != headBefore {
		t.Fatalf("HEAD moved from %s to %s: the failed claim's commit was not undone", headBefore, headAfter)
	}
	if _, statErr := os.Stat(st.TicketDir("JIG-1")); statErr == nil {
		t.Fatal("ticket folder JIG-1 was left behind after a failed claim")
	}
	status := runGit(t, work, "status", "--porcelain")
	if strings.TrimSpace(status) != "" {
		t.Fatalf("store left dirty after a failed claim: %q", status)
	}
}
