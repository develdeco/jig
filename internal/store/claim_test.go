package store

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/gittest"
	"github.com/develdeco/jig/internal/gitx"
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
			id, err := st.Mint("JIG", Ticket{Title: "x"})
			if err != nil {
				return "", nil, err
			}
			return id, []string{st.TicketRelDir(id)}, nil
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
			id, err := st.Mint("JIG", Ticket{Title: "x"})
			if err != nil {
				return "", nil, err
			}
			return id, []string{st.TicketRelDir(id)}, nil
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
			id, err := st.Mint("JIG", Ticket{Title: "x"})
			if err != nil {
				return "", nil, err
			}
			return id, []string{st.TicketRelDir(id)}, nil
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

// TestClaimRemintsAfterRejectedPushPreservesUnrelatedDirtyState covers the
// same rejected-then-retried race as TestClaimRemintsAfterRejectedPush, but
// with another process's uncommitted edit to a tracked file (project.yaml)
// landing in the store in the window between Claim reading its pre-write
// HEAD and its own commit - the interleaving store.go's own comments already
// treat as possible ("Another process working on the same store can still
// slip in between"). undoClaim must discard only its own rejected commit,
// never that edit: a repo-wide `git reset --hard` would silently restore
// project.yaml to what it held before the edit, discarding the other
// process's uncommitted work along with the rejected claim.
func TestClaimRemintsAfterRejectedPushPreservesUnrelatedDirtyState(t *testing.T) {
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
			if calls == 1 {
				// Another process on this same clone, uncommitted and no
				// part of this claim's own paths.
				if err := os.WriteFile(filepath.Join(work, "project.yaml"), []byte("schema_version: 1\nextra: from-another-process\n"), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			id, err := st.Mint("JIG", Ticket{Title: "x"})
			if err != nil {
				return "", nil, err
			}
			return id, []string{st.TicketRelDir(id)}, nil
		},
		func(id string) string { return id + ": new ticket" },
	)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if id != "JIG-1" {
		t.Fatalf("id = %q, want JIG-1", id)
	}
	if calls != 2 {
		t.Fatalf("write was called %d times, want exactly 2 (one rejected, one that landed)", calls)
	}

	data, err := os.ReadFile(filepath.Join(work, "project.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "extra: from-another-process") {
		t.Fatalf("project.yaml = %q, want the other process's uncommitted edit to survive the rejected claim's undo", data)
	}
	status := runGit(t, work, "status", "--porcelain")
	if !strings.Contains(status, "project.yaml") {
		t.Fatalf("status = %q, want project.yaml still showing as modified (uncommitted)", status)
	}
	subject := runGit(t, work, "log", "-1", "--pretty=%s")
	if strings.Contains(subject, "project.yaml") {
		t.Fatalf("commit subject = %q, want the claim's own commit, not the swept-up project.yaml edit", subject)
	}
}

// TestClaimRefusesRatherThanOrphaningAConcurrentCommit covers the other half
// of the same race: another process on this same clone commits its own
// work (as Store.Sync's or Store.Push's own `add -A` would) on top of this
// claim's own commit, in the window between the two. A rejected push's
// undo, `reset --soft` to the pre-write HEAD, cannot discard this claim's
// own commit alone without also rewinding the branch past that other
// commit - orphaning it, no longer reachable except through the clone's own
// reflog (see undoClaim). Claim must refuse instead, ID_NOT_CLAIMED, naming
// the other commit, and never call that reset at all: the branch, both
// commits, and the rejected claim are left exactly as they were for the
// operator to reconcile by hand.
func TestClaimRefusesRatherThanOrphaningAConcurrentCommit(t *testing.T) {
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

	headBefore := strings.TrimSpace(runGit(t, work, "rev-parse", "HEAD"))

	calls := 0
	_, err := st.Claim(
		func() (string, []string, error) {
			calls++
			// Another process on this same clone, committing its own
			// work while this claim is in flight - lands on top of
			// this claim's own commit once stageAndCommitPaths makes
			// it below.
			if err := os.WriteFile(filepath.Join(work, "from-same-clone.txt"), []byte("x\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			runGit(t, work, "add", "-A")
			runGit(t, work, "-c", "user.name=other-local", "-c", "user.email=other-local@example.invalid", "commit", "-m", "other local commit")

			id, err := st.Mint("JIG", Ticket{Title: "x"})
			if err != nil {
				return "", nil, err
			}
			return id, []string{st.TicketRelDir(id)}, nil
		},
		func(id string) string { return id + ": new ticket" },
	)
	if err == nil {
		t.Fatal("Claim: want an error when a concurrent commit would be orphaned by the undo, got nil")
	}
	var ae *axi.Error
	if !errors.As(err, &ae) || ae.Code != idNotClaimedCode {
		t.Fatalf("err = %v, want *axi.Error %s", err, idNotClaimedCode)
	}
	if calls != 1 {
		t.Fatalf("write was called %d times, want exactly 1 (Claim must refuse, not retry, once it would have to orphan a concurrent commit)", calls)
	}

	otherCommit := strings.TrimSpace(runGit(t, work, "rev-parse", "HEAD^"))
	full := ae.Msg + " " + strings.Join(ae.Help, " ")
	if !strings.Contains(full, otherCommit) {
		t.Fatalf("err = %+v, want the other process's own commit %s named", ae, otherCommit)
	}

	// Claim never reset the branch: both the other process's commit and
	// this claim's own rejected commit are still exactly where they
	// landed, untouched.
	headAfter := strings.TrimSpace(runGit(t, work, "rev-parse", "HEAD"))
	if headAfter == headBefore {
		t.Fatalf("HEAD = %s, want it still at this claim's own rejected commit, not rewound to %s", headAfter, headBefore)
	}
	subject := runGit(t, work, "log", "-1", "--pretty=%s")
	if !strings.Contains(subject, "JIG-1: new ticket") {
		t.Fatalf("HEAD subject = %q, want this claim's own rejected commit still there", subject)
	}
	parentSubject := runGit(t, work, "log", "-1", "--pretty=%s", "HEAD^")
	if !strings.Contains(parentSubject, "other local commit") {
		t.Fatalf("HEAD^ subject = %q, want the other process's own commit still there, not orphaned", parentSubject)
	}
	if _, err := os.Stat(st.TicketDir("JIG-1")); err != nil {
		t.Fatalf("JIG-1 missing after a refused claim that left its own rejected commit in place: %v", err)
	}
	status := runGit(t, work, "status", "--porcelain")
	if strings.TrimSpace(status) != "" {
		t.Fatalf("store left dirty after a refused claim that touched nothing: %q", status)
	}

	remoteLog := runGit(t, "", "--git-dir", remote, "log", "--pretty=%s")
	if strings.Contains(remoteLog, "JIG-1: new ticket") || strings.Contains(remoteLog, "other local commit") {
		t.Fatalf("remote log = %q, want neither local commit pushed", remoteLog)
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

			id, merr := st.Mint("JIG", Ticket{Title: "x"})
			if merr != nil {
				return "", nil, merr
			}
			return id, []string{st.TicketRelDir(id)}, nil
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
			id, merr := st.Mint("JIG", Ticket{Title: "x"})
			if merr != nil {
				return "", nil, merr
			}
			return id, []string{st.TicketRelDir(id)}, nil
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

// TestClaimWrapsUndoFailureAsIDNotClaimed covers undoClaim itself failing
// partway through, after a rejected push: its own `reset --soft`, the first
// of its steps, has already moved the branch back by the time a later,
// per-path step fails, so the claim's own commit is off the branch either
// way - but it still exists in the store, and project.yaml, the path that
// step was restoring, is left with its claimed edit on disk, uncommitted.
// Claim must not return that git failure raw - it wraps it as
// ID_NOT_CLAIMED, like every other refusal here, naming the commit so the
// operator has something to inspect rather than a bare git error.
//
// The failure is forced structurally: project.yaml is opened with a share
// mode that denies writes but allows reads, so `git add` and `git commit`
// (both read-only against the working tree) still succeed, but undoClaim's
// `git restore --worktree`, which must overwrite project.yaml's content
// with what it held before this claim's commit, cannot.
func TestClaimWrapsUndoFailureAsIDNotClaimed(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("lockFileExclusive has no portable non-Windows equivalent")
	}
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

	headBefore := strings.TrimSpace(runGit(t, work, "rev-parse", "HEAD"))
	projectYAML := filepath.Join(work, "project.yaml")
	if err := os.WriteFile(projectYAML, []byte("schema_version: 1\nextra: edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	unlock := lockFileExclusive(t, projectYAML)
	defer unlock()

	_, err := st.Claim(
		func() (string, []string, error) { return "JIG-1", []string{"project.yaml"}, nil },
		func(id string) string { return id + ": new ticket" },
	)
	if err == nil {
		t.Fatal("Claim: want an error when undoClaim itself fails, got nil")
	}
	var ae *axi.Error
	if !errors.As(err, &ae) || ae.Code != "ID_NOT_CLAIMED" {
		t.Fatalf("err = %v, want *axi.Error ID_NOT_CLAIMED", err)
	}

	// reset --soft already ran: the branch is back where it started, and
	// the claim's own commit is HEAD's immediate reflog predecessor.
	headAfter := strings.TrimSpace(runGit(t, work, "rev-parse", "HEAD"))
	if headAfter != headBefore {
		t.Fatalf("HEAD = %s, want it back at %s (undoClaim's own reset --soft ran before the failing step)", headAfter, headBefore)
	}
	claimSHA := strings.TrimSpace(runGit(t, work, "rev-parse", "HEAD@{1}"))
	if _, err := gitx.Run(work, "cat-file", "-e", claimSHA); err != nil {
		t.Fatalf("the claim's own commit %s no longer exists in the store: %v", claimSHA, err)
	}
	if !strings.Contains(ae.Msg, claimSHA) && !strings.Contains(strings.Join(ae.Help, "\n"), claimSHA) {
		t.Fatalf("err = %+v, want the claim's own commit %s named in Msg or Help", ae, claimSHA)
	}

	// The path undoClaim was restoring when it failed is left with its
	// claimed edit still on disk, uncommitted.
	data, err := os.ReadFile(projectYAML)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "extra: edited") {
		t.Fatalf("project.yaml = %q, want the claimed edit still on disk (undoClaim could not restore it)", data)
	}
}

// TestUndoClaimReportsWhetherResetRan covers undoClaim's own resetRan
// report, which undoFailedError's message depends on: a sha that does not
// resolve fails at the very first step, `reset --soft`, before it ever
// moves the branch, so resetRan must come back false - unlike a failure in
// one of the later, per-path steps (TestClaimWrapsUndoFailureAsIDNotClaimed),
// where the reset has already run.
func TestUndoClaimReportsWhetherResetRan(t *testing.T) {
	st, work := newTestStandaloneStore(t)
	headBefore := strings.TrimSpace(runGit(t, work, "rev-parse", "HEAD"))

	resetRan, err := st.undoClaim("0000000000000000000000000000000000000000", []string{"project.yaml"})
	if err == nil {
		t.Fatal("undoClaim: want an error for a sha that does not resolve, got nil")
	}
	if resetRan {
		t.Fatal("resetRan = true, want false: `reset --soft` itself failed before it could move the branch")
	}
	headAfter := strings.TrimSpace(runGit(t, work, "rev-parse", "HEAD"))
	if headAfter != headBefore {
		t.Fatalf("HEAD moved from %s to %s even though the reset itself failed", headBefore, headAfter)
	}
}

// TestForeignCommitAheadFindsACommitOnEitherSideOfClaimsOwn pins
// foreignCommitAhead's four shapes directly, at the git-plumbing level,
// since TestClaimRefusesRatherThanOrphaningAConcurrentCommit can only drive
// the one shape Claim's own write hook can arrange (a concurrent commit
// landing before this round's own, not after it - Claim gives a test no
// hook between its own commit and its push to arrange the other).
func TestForeignCommitAheadFindsACommitOnEitherSideOfClaimsOwn(t *testing.T) {
	commit := func(t *testing.T, work, path, subject string) string {
		t.Helper()
		if err := os.WriteFile(filepath.Join(work, path), []byte(subject+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		runGit(t, work, "add", "-A")
		runGit(t, work, "commit", "-m", subject)
		return strings.TrimSpace(runGit(t, work, "rev-parse", "HEAD"))
	}

	t.Run("SafeWithOnlyClaimsOwnCommit", func(t *testing.T) {
		st, work := newTestStandaloneStore(t)
		pre := strings.TrimSpace(runGit(t, work, "rev-parse", "HEAD"))
		claimSHA := commit(t, work, "a.txt", "claim's own commit")

		foreign, err := st.foreignCommitAhead(pre, claimSHA)
		if err != nil {
			t.Fatal(err)
		}
		if foreign != "" {
			t.Fatalf("foreign = %q, want \"\": nothing sits between pre and claimSHA but claimSHA itself", foreign)
		}
	})

	t.Run("SafeWithNothingCommittedThisRound", func(t *testing.T) {
		st, work := newTestStandaloneStore(t)
		pre := strings.TrimSpace(runGit(t, work, "rev-parse", "HEAD"))

		foreign, err := st.foreignCommitAhead(pre, "")
		if err != nil {
			t.Fatal(err)
		}
		if foreign != "" {
			t.Fatalf("foreign = %q, want \"\": the branch never left pre", foreign)
		}
	})

	t.Run("NamesACommitLandedBeforeClaimsOwn", func(t *testing.T) {
		st, work := newTestStandaloneStore(t)
		pre := strings.TrimSpace(runGit(t, work, "rev-parse", "HEAD"))
		other := commit(t, work, "other.txt", "other process's commit")
		claimSHA := commit(t, work, "a.txt", "claim's own commit")

		foreign, err := st.foreignCommitAhead(pre, claimSHA)
		if err != nil {
			t.Fatal(err)
		}
		if foreign != other {
			t.Fatalf("foreign = %q, want %q (the commit between pre and claimSHA)", foreign, other)
		}
	})

	t.Run("NamesACommitLandedAfterClaimsOwn", func(t *testing.T) {
		st, work := newTestStandaloneStore(t)
		pre := strings.TrimSpace(runGit(t, work, "rev-parse", "HEAD"))
		claimSHA := commit(t, work, "a.txt", "claim's own commit")
		other := commit(t, work, "other.txt", "other process's commit")

		foreign, err := st.foreignCommitAhead(pre, claimSHA)
		if err != nil {
			t.Fatal(err)
		}
		if foreign != other {
			t.Fatalf("foreign = %q, want %q (the branch's tip has moved past claimSHA)", foreign, other)
		}
	})

	t.Run("NamesACommitWhenThisRoundCommittedNothingButHeadMoved", func(t *testing.T) {
		st, work := newTestStandaloneStore(t)
		pre := strings.TrimSpace(runGit(t, work, "rev-parse", "HEAD"))
		other := commit(t, work, "other.txt", "other process's commit")

		foreign, err := st.foreignCommitAhead(pre, "")
		if err != nil {
			t.Fatal(err)
		}
		if foreign != other {
			t.Fatalf("foreign = %q, want %q: this round made no commit of its own, but the branch moved anyway", foreign, other)
		}
	})
}

// TestUndoFailedErrorDescribesActualBranchState pins undoFailedError's
// message to what undoClaim actually reported (resetRan) and to whether
// stageAndCommitPaths made a commit this round at all (claimSHA != pre),
// rather than assuming undoClaim's documented happy path always holds. The
// four combinations below are mutually exclusive and must each describe the
// branch differently - conflating any two is exactly the gate finding this
// pins down.
func TestUndoFailedErrorDescribesActualBranchState(t *testing.T) {
	cause := errors.New("boom")

	assertRefusal := func(t *testing.T, err error) *axi.Error {
		t.Helper()
		var ae *axi.Error
		if !errors.As(err, &ae) || ae.Code != idNotClaimedCode {
			t.Fatalf("err = %v, want *axi.Error %s", err, idNotClaimedCode)
		}
		return ae
	}

	t.Run("ResetFailedWithCommit", func(t *testing.T) {
		// undoClaim's own `reset --soft` failed (a lock, a sha that no
		// longer resolves) before it could move the branch back: the
		// claim's own commit, claimSHA, is still the branch's HEAD, not
		// off it.
		ae := assertRefusal(t, undoFailedError(cause, "claimsha", "presha", false))
		full := ae.Msg + " " + strings.Join(ae.Help, " ")
		if strings.Contains(full, "off the branch") {
			t.Fatalf("message = %q, want it not to claim the commit is off the branch when the reset itself failed", full)
		}
		if !strings.Contains(full, "claimsha") {
			t.Fatalf("message = %q, want claimSHA named", full)
		}
	})

	t.Run("ResetFailedNoCommit", func(t *testing.T) {
		// Nothing was committed this round (claimSHA == pre): the reset,
		// even though its target was already HEAD, still failed.
		ae := assertRefusal(t, undoFailedError(cause, "samesha", "samesha", false))
		full := ae.Msg + " " + strings.Join(ae.Help, " ")
		if strings.Contains(full, "rejected commit") {
			t.Fatalf("message = %q, want it not to call an unrelated, pre-existing commit \"its rejected commit\"", full)
		}
	})

	t.Run("LaterStepFailedNoCommit", func(t *testing.T) {
		// The reset ran (a no-op: pre was already HEAD) but a later,
		// per-path step failed cleaning up after a push that was rejected
		// without this round ever having committed anything, so claimSHA
		// names a commit that predates this attempt.
		ae := assertRefusal(t, undoFailedError(cause, "samesha", "samesha", true))
		full := ae.Msg + " " + strings.Join(ae.Help, " ")
		if strings.Contains(full, "its rejected commit") {
			t.Fatalf("message = %q, want it not to call an unrelated, pre-existing commit \"its rejected commit\"", full)
		}
		if !strings.Contains(full, "samesha") {
			t.Fatalf("message = %q, want the commit named", full)
		}
	})

	t.Run("LaterStepFailedWithCommit", func(t *testing.T) {
		// undoClaim's documented happy path: the reset already moved
		// claimSHA off the branch before a later step failed.
		ae := assertRefusal(t, undoFailedError(cause, "claimsha", "presha", true))
		full := ae.Msg + " " + strings.Join(ae.Help, " ")
		if !strings.Contains(full, "claimsha") || !strings.Contains(full, "off the branch") {
			t.Fatalf("message = %q, want claimSHA named as off the branch", full)
		}
	})
}

// TestPullAfterUndoErrorPreservesInnerAxiError covers a post-undo pull
// failure that already carries its own, more specific *axi.Error - a rebase
// conflict abortFailedPull wrapped as STORE_CONFLICT, with the exact recipe
// to resolve it (or, when jig's own best-effort `rebase --abort` also
// failed, that the store is left mid-rebase). pullAfterUndoError must return
// it unchanged rather than relabeling it ID_NOT_CLAIMED and replacing its
// Help with "commit or stash it by hand", which resolves neither case.
func TestPullAfterUndoErrorPreservesInnerAxiError(t *testing.T) {
	inner := &axi.Error{
		Msg:  "the store at /store is still mid-rebase",
		Code: "STORE_CONFLICT",
		Help: []string{"resolve it there with `git status`, then `git rebase --abort` or `--continue`"},
	}
	if got := pullAfterUndoError("/store", inner); got != inner {
		t.Fatalf("pullAfterUndoError = %v, want the inner *axi.Error returned unchanged", got)
	}
}

// TestPullAfterUndoErrorNamesOnlyAGenuineLocalChange covers the two shapes a
// plain (non-*axi.Error) post-undo pull failure can take: one where git's
// own message names a local change a merge or rebase would overwrite, and
// one where it does not (the fetch itself failed - an unreachable origin).
// Only the first should blame a local change, and only the path git itself
// named - never dirtyPaths' whole-store listing, which would blame paths
// that have nothing to do with the incoming commits.
func TestPullAfterUndoErrorNamesOnlyAGenuineLocalChange(t *testing.T) {
	t.Run("LocalChangeNamed", func(t *testing.T) {
		cause := errors.New("git merge --ff-only --quiet refs/remotes/origin/main: error: Your local changes to the following files would be overwritten by merge:\n\tproject.yaml\nPlease commit your changes or stash them before you merge.\nAborting: exit status 1")
		var ae *axi.Error
		err := pullAfterUndoError("/store", cause)
		if !errors.As(err, &ae) || ae.Code != idNotClaimedCode {
			t.Fatalf("err = %v, want *axi.Error %s", err, idNotClaimedCode)
		}
		full := ae.Msg + " " + strings.Join(ae.Help, " ")
		if !strings.Contains(full, "project.yaml") {
			t.Fatalf("message = %q, want project.yaml named", full)
		}
		if strings.Contains(full, "an unrecorded local change") {
			t.Fatalf("message = %q, want the real path named, not a generic placeholder", full)
		}
	})

	t.Run("NoLocalChangeToBlame", func(t *testing.T) {
		cause := errors.New("git fetch origin: could not resolve host: origin.example.invalid: exit status 128")
		var ae *axi.Error
		err := pullAfterUndoError("/store", cause)
		if !errors.As(err, &ae) || ae.Code != idNotClaimedCode {
			t.Fatalf("err = %v, want *axi.Error %s", err, idNotClaimedCode)
		}
		full := ae.Msg + " " + strings.Join(ae.Help, " ")
		if strings.Contains(full, "commit or stash") {
			t.Fatalf("message = %q, want it not to send the operator after a local change that was never confirmed", full)
		}
		if !strings.Contains(full, "could not resolve host") {
			t.Fatalf("message = %q, want the real cause named", full)
		}
	})
}

// TestClaimWrapsPullFailureAfterUndoAsIDNotClaimed covers the pull that
// follows a rejected push failing in turn: unlike before undoClaim's own
// undo was scoped to write's own paths, that pull is no longer guaranteed a
// fast-forward - a local change undoClaim's scoped undo preserved can
// overlap a file the rejecting push itself brought in. Here, another
// process on this same clone edits project.yaml, uncommitted, while the
// claim (touching only its own new ticket folder) is in flight; the other
// clone that rejects this claim's push also committed its own edit to
// project.yaml, so pulling it in after the undo conflicts with the
// preserved edit, and the in-process fast-forward merge refuses. Claim must
// not return that raw git error - it wraps it as ID_NOT_CLAIMED, naming the
// local change that blocked it.
func TestClaimWrapsPullFailureAfterUndoAsIDNotClaimed(t *testing.T) {
	st, work, remote := newTestRemoteStore(t)

	other := t.TempDir()
	runGit(t, "", "clone", remote, other)
	runGit(t, other, "config", "user.name", "other")
	runGit(t, other, "config", "user.email", "other@example.invalid")
	if err := os.WriteFile(filepath.Join(other, "project.yaml"), []byte("schema_version: 1\nfrom: other\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, other, "add", "-A")
	runGit(t, other, "commit", "-m", "from other")
	runGit(t, other, "push", "origin", "main")

	calls := 0
	_, err := st.Claim(
		func() (string, []string, error) {
			calls++
			if err := os.WriteFile(filepath.Join(work, "project.yaml"), []byte("schema_version: 1\nextra: from-another-process\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			id, merr := st.Mint("JIG", Ticket{Title: "x"})
			if merr != nil {
				return "", nil, merr
			}
			return id, []string{st.TicketRelDir(id)}, nil
		},
		func(id string) string { return id + ": new ticket" },
	)
	if err == nil {
		t.Fatal("Claim: want an error when the post-undo pull conflicts with a preserved local change, got nil")
	}
	var ae *axi.Error
	if !errors.As(err, &ae) || ae.Code != "ID_NOT_CLAIMED" {
		t.Fatalf("err = %v, want *axi.Error ID_NOT_CLAIMED", err)
	}
	if !strings.Contains(ae.Msg, "project.yaml") && !strings.Contains(strings.Join(ae.Help, "\n"), "project.yaml") {
		t.Fatalf("err = %+v, want project.yaml, the local change that blocked the pull, named in Msg or Help", ae)
	}
	if calls != 1 {
		t.Fatalf("write was called %d times, want exactly 1 (the pull failure refuses rather than retrying blind)", calls)
	}

	data, err := os.ReadFile(filepath.Join(work, "project.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "extra: from-another-process") {
		t.Fatalf("project.yaml = %q, want the other process's uncommitted edit to survive", data)
	}
	if _, statErr := os.Stat(st.TicketDir("JIG-1")); statErr == nil {
		t.Fatal("ticket folder JIG-1 was left behind after a failed claim")
	}
}

// failedMintWrite returns a Claim write that mints, reports the ticket folder
// it created, and then fails with sentinel: the shape of a graduate whose
// chart write fails after Mint.
func failedMintWrite(st *Store, sentinel error, minted *string) func() (string, []string, error) {
	return func() (string, []string, error) {
		id, err := st.Mint("JIG", Ticket{Title: "x"})
		if err != nil {
			return "", nil, err
		}
		if minted != nil {
			*minted = id
		}
		return "", []string{st.TicketRelDir(id)}, sentinel
	}
}

// TestClaimUndoesAFailedWrite covers the contract: a write that mints and
// then fails reports what it touched, and Claim removes it - the ticket
// folder and its .lock sidecar are gone, the store is clean, HEAD is
// unchanged, nothing was committed, the error is the write's own and the next
// Mint takes the same id - with and without an origin.
func TestClaimUndoesAFailedWrite(t *testing.T) {
	for _, tc := range []struct {
		name string
		mk   func(t *testing.T) (*Store, string)
	}{
		{"NoOrigin", func(t *testing.T) (*Store, string) {
			st, work := newTestStandaloneStore(t)
			// As project.Init's stores have: the lock sidecars stay out of `git status`.
			if err := os.WriteFile(filepath.Join(work, ".gitignore"), []byte("*.lock\n.*.tmp\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			runGit(t, work, "add", "-A")
			runGit(t, work, "commit", "-m", "ignore locks")
			return st, work
		}},
		{"WithOrigin", func(t *testing.T) (*Store, string) {
			st, work, _ := newTestRemoteStore(t)
			return st, work
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			st, work := tc.mk(t)
			headBefore := strings.TrimSpace(runGit(t, work, "rev-parse", "HEAD"))
			sentinel := errors.New("the write failed")

			var minted string
			_, err := st.Claim(failedMintWrite(st, sentinel, &minted), func(id string) string { return id + ": new ticket" })
			if !errors.Is(err, sentinel) {
				t.Fatalf("err = %v, want the write's own error", err)
			}
			if IsUndoIncomplete(err) {
				t.Fatalf("err = %v, want a complete undo", err)
			}
			if minted == "" {
				t.Fatal("the write never minted")
			}
			dir := filepath.Join(work, "tickets", minted)
			if _, err := os.Stat(dir); !os.IsNotExist(err) {
				t.Fatalf("ticket folder %s survived a failed claim (stat err %v)", dir, err)
			}
			if status := strings.TrimSpace(runGit(t, work, "status", "--porcelain")); status != "" {
				t.Fatalf("git status = %q, want clean", status)
			}
			if head := strings.TrimSpace(runGit(t, work, "rev-parse", "HEAD")); head != headBefore {
				t.Fatalf("HEAD moved from %s to %s", headBefore, head)
			}
			next, err := st.Mint("JIG", Ticket{Title: "again"})
			if err != nil {
				t.Fatal(err)
			}
			if next != minted {
				t.Fatalf("next Mint = %s, want the failed attempt's id %s back", next, minted)
			}
		})
	}
}

// TestClaimUndoesAFailedWriteEditOfATrackedFile covers a write that edits a
// tracked file and then fails: the pre-claim content comes back.
func TestClaimUndoesAFailedWriteEditOfATrackedFile(t *testing.T) {
	st, work := newTestStandaloneStore(t)
	sentinel := errors.New("the write failed")
	path := filepath.Join(work, "project.yaml")
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = st.Claim(
		func() (string, []string, error) {
			if err := os.WriteFile(path, []byte("schema_version: 1\nextra: edited\n"), 0o644); err != nil {
				return "", nil, err
			}
			return "", []string{"project.yaml"}, sentinel
		},
		func(id string) string { return id },
	)
	if !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want the write's own error", err)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != string(before) {
		t.Fatalf("project.yaml = %q, want %q restored", after, before)
	}
	if status := strings.TrimSpace(runGit(t, work, "status", "--porcelain")); status != "" {
		t.Fatalf("git status = %q, want clean", status)
	}
}

// dirtyStore leaves an unrelated untracked file and a dirty tracked file in
// the store and returns a func that checks both survived.
func dirtyStore(t *testing.T, work string) func() {
	t.Helper()
	stray := filepath.Join(work, "unrelated.txt")
	if err := os.WriteFile(stray, []byte("stray\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	tracked := filepath.Join(work, "project.yaml")
	const dirty = "schema_version: 1\nmine: edited\n"
	if err := os.WriteFile(tracked, []byte(dirty), 0o644); err != nil {
		t.Fatal(err)
	}
	return func() {
		t.Helper()
		if data, err := os.ReadFile(stray); err != nil || string(data) != "stray\n" {
			t.Fatalf("unrelated.txt = %q, %v, want it untouched", data, err)
		}
		if data, err := os.ReadFile(tracked); err != nil || string(data) != dirty {
			t.Fatalf("project.yaml = %q, %v, want the person's edit untouched", data, err)
		}
	}
}

// TestClaimFailedWriteWithNoPathsTouchesNothing covers a write that fails
// before touching anything: no paths, so no git step runs and nothing else in
// the store is disturbed.
func TestClaimFailedWriteWithNoPathsTouchesNothing(t *testing.T) {
	st, work := newTestStandaloneStore(t)
	check := dirtyStore(t, work)
	sentinel := errors.New("the write failed")
	_, err := st.Claim(
		func() (string, []string, error) { return "", nil, sentinel },
		func(id string) string { return id },
	)
	if err != sentinel {
		t.Fatalf("err = %v, want the write's own error unchanged", err)
	}
	check()
}

// TestClaimFailedWriteLeavesUnreportedDirtAlone covers a write that reports
// only its own folder: whatever else is dirty in the store survives the undo.
func TestClaimFailedWriteLeavesUnreportedDirtAlone(t *testing.T) {
	st, work := newTestStandaloneStore(t)
	check := dirtyStore(t, work)
	sentinel := errors.New("the write failed")
	var minted string
	_, err := st.Claim(failedMintWrite(st, sentinel, &minted), func(id string) string { return id })
	if !errors.Is(err, sentinel) {
		t.Fatalf("err = %v, want the write's own error", err)
	}
	if _, err := os.Stat(filepath.Join(work, "tickets", minted)); !os.IsNotExist(err) {
		t.Fatalf("the write's own folder survived (stat err %v)", err)
	}
	check()
}

// TestUndoPathsContinuesPastAFailingStep covers best-effort undo: a locked
// tracked file listed before a minted folder fails its own restore, yet the
// folder is still cleaned, and the error names the locked path.
func TestUndoPathsContinuesPastAFailingStep(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("lockFileExclusive has no portable non-Windows equivalent")
	}
	st, work := newTestStandaloneStore(t)
	sha := strings.TrimSpace(runGit(t, work, "rev-parse", "HEAD"))
	locked := filepath.Join(work, "project.yaml")
	if err := os.WriteFile(locked, []byte("schema_version: 1\nextra: edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	folder := filepath.Join(work, "tickets", "JIG-1")
	if err := os.MkdirAll(folder, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(folder, "ticket.yaml"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	unlock := lockFileExclusive(t, locked)
	defer unlock()

	err := st.undoPaths(sha, []string{"project.yaml", filepath.Join("tickets", "JIG-1")})
	if err == nil {
		t.Fatal("undoPaths: want an error for the locked file")
	}
	var upe *undoPathsError
	if !errors.As(err, &upe) || len(upe.leftovers) != 1 || upe.leftovers[0] != "project.yaml" {
		t.Fatalf("err = %v, leftovers = %v, want exactly project.yaml named", err, upe)
	}
	if _, err := os.Stat(folder); !os.IsNotExist(err) {
		t.Fatalf("the minted folder survived the locked file before it (stat err %v)", err)
	}
}

// TestClaimReportsLeftoverPathsWhenUndoFails covers both shapes of an undo
// that does not finish: the write error and the push error each report
// IsUndoIncomplete and name the leftover path; the write's error still
// unwraps.
func TestClaimReportsLeftoverPathsWhenUndoFails(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("lockFileExclusive has no portable non-Windows equivalent")
	}
	t.Run("WriteError", func(t *testing.T) {
		st, work := newTestStandaloneStore(t)
		sentinel := &axi.Error{Msg: "the write failed", Code: "SOME_CODE", Help: []string{"h"}}
		projectYAML := filepath.Join(work, "project.yaml")
		_, err := st.Claim(
			func() (string, []string, error) {
				if err := os.WriteFile(projectYAML, []byte("schema_version: 1\nextra: edited\n"), 0o644); err != nil {
					return "", nil, err
				}
				unlock := lockFileExclusive(t, projectYAML)
				t.Cleanup(unlock)
				return "", []string{"project.yaml"}, sentinel
			},
			func(id string) string { return id },
		)
		if !IsUndoIncomplete(err) {
			t.Fatalf("err = %v, want IsUndoIncomplete", err)
		}
		if !errors.Is(err, sentinel) {
			t.Fatalf("err = %v, want it to unwrap to the write's own error", err)
		}
		var ae *axi.Error
		if !errors.As(err, &ae) {
			t.Fatalf("err = %v, want an *axi.Error", err)
		}
		if ae.Code != "SOME_CODE" {
			t.Fatalf("code = %q, want the write's own code kept", ae.Code)
		}
		assertLeftoverReport(t, ae, "h")
	})
	t.Run("PushError", func(t *testing.T) {
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

		projectYAML := filepath.Join(work, "project.yaml")
		if err := os.WriteFile(projectYAML, []byte("schema_version: 1\nextra: edited\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		unlock := lockFileExclusive(t, projectYAML)
		defer unlock()
		_, err := st.Claim(
			func() (string, []string, error) { return "JIG-1", []string{"project.yaml"}, nil },
			func(id string) string { return id + ": new ticket" },
		)
		if !IsUndoIncomplete(err) {
			t.Fatalf("err = %v, want IsUndoIncomplete", err)
		}
		var ae *axi.Error
		if !errors.As(err, &ae) || ae.Code != idNotClaimedCode {
			t.Fatalf("err = %v, want ID_NOT_CLAIMED kept", err)
		}
		assertLeftoverReport(t, ae)
	})
}

// assertLeftoverReport pins what a person reads when an undo did not finish:
// the leftover list in the message, a help line that names it and the command
// that finishes it, and the help lines the error already carried (keep).
func assertLeftoverReport(t *testing.T, ae *axi.Error, keep ...string) {
	t.Helper()
	if !strings.Contains(ae.Msg, "(left in the store: project.yaml)") {
		t.Fatalf("msg = %q, want the leftover list '(left in the store: project.yaml)'", ae.Msg)
	}
	finish := false
	for _, h := range ae.Help {
		if strings.Contains(h, "project.yaml") && strings.Contains(h, "git restore --source=HEAD") {
			finish = true
		}
	}
	if !finish {
		t.Fatalf("help = %q, want a line naming project.yaml and `git restore --source=HEAD`", ae.Help)
	}
	for _, k := range keep {
		if !slices.Contains(ae.Help, k) {
			t.Fatalf("help = %q, want %q kept", ae.Help, k)
		}
	}
}

// TestUndoClaimWithNoPathsLeavesTheStoreAlone covers the path a push undo
// takes for a write that reported no paths (claimIssue's adopt branch):
// `clean -fdx --` with no pathspec would take every untracked and ignored
// file of the store, so nothing may run.
func TestUndoClaimWithNoPathsLeavesTheStoreAlone(t *testing.T) {
	st, work := newTestStandaloneStore(t)
	if err := os.WriteFile(filepath.Join(work, ".gitignore"), []byte("*.lock\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, work, "add", "-A")
	runGit(t, work, "commit", "-m", "ignore locks")
	sha := strings.TrimSpace(runGit(t, work, "rev-parse", "HEAD"))
	stray := filepath.Join(work, "unrelated.txt")
	ignored := filepath.Join(work, "x.lock")
	for _, f := range []string{stray, ignored} {
		if err := os.WriteFile(f, []byte("keep\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := st.undoClaim(sha, nil); err != nil {
		t.Fatal(err)
	}
	for _, f := range []string{stray, ignored} {
		if _, err := os.Stat(f); err != nil {
			t.Fatalf("%s: %v, want it left alone by an undo of no paths", f, err)
		}
	}
}

// TestClaimUndoesAFailedCommit covers the commit that fails after write
// touched paths (here the store's own failing pre-commit hook): the folder is
// undone and HEAD stays.
func TestClaimUndoesAFailedCommit(t *testing.T) {
	st, work := newTestStandaloneStore(t)
	// As project.Init's stores have: the lock sidecars stay out of `git status`.
	if err := os.WriteFile(filepath.Join(work, ".gitignore"), []byte("*.lock\n.*.tmp\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, work, "add", "-A")
	runGit(t, work, "commit", "-m", "ignore locks")
	failingHooks(t, filepath.Join(work, ".git", "hooks"), "pre-commit")
	headBefore := strings.TrimSpace(runGit(t, work, "rev-parse", "HEAD"))
	var minted string
	_, err := st.Claim(mintingWrite(st, &minted), func(id string) string { return id })
	if err == nil {
		t.Fatal("Claim = nil, want the failing pre-commit hook to stop the commit")
	}
	if IsUndoIncomplete(err) {
		t.Fatalf("err = %v, want a complete undo", err)
	}
	if _, err := os.Stat(filepath.Join(work, "tickets", minted)); !os.IsNotExist(err) {
		t.Fatalf("the minted folder survived a failed commit (stat err %v)", err)
	}
	if status := strings.TrimSpace(runGit(t, work, "status", "--porcelain")); status != "" {
		t.Fatalf("git status = %q, want clean", status)
	}
	if head := strings.TrimSpace(runGit(t, work, "rev-parse", "HEAD")); head != headBefore {
		t.Fatalf("HEAD moved from %s to %s", headBefore, head)
	}
}

// mintingWrite is a write that mints and succeeds, for a claim whose commit
// then fails.
func mintingWrite(st *Store, minted *string) func() (string, []string, error) {
	return func() (string, []string, error) {
		id, err := st.Mint("JIG", Ticket{Title: "x"})
		if err != nil {
			return "", nil, err
		}
		*minted = id
		return id, []string{st.TicketRelDir(id)}, nil
	}
}

// TestClaimReportsAnIncompleteUndoOfAFailedCommit covers the same failure when
// the undo cannot finish (a file in the minted folder is held open): the error
// is an undo-incomplete one and names the folder.
func TestClaimReportsAnIncompleteUndoOfAFailedCommit(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("lockFileExclusive has no portable non-Windows equivalent")
	}
	st, work := newTestStandaloneStore(t)
	failingHooks(t, filepath.Join(work, ".git", "hooks"), "pre-commit")
	var minted string
	mint := mintingWrite(st, &minted)
	_, err := st.Claim(
		func() (string, []string, error) {
			id, paths, werr := mint()
			if werr != nil {
				return "", nil, werr
			}
			unlock := lockFileExclusive(t, filepath.Join(work, "tickets", id, "ticket.yaml"))
			t.Cleanup(unlock)
			return id, paths, nil
		},
		func(id string) string { return id },
	)
	if !IsUndoIncomplete(err) {
		t.Fatalf("err = %v, want IsUndoIncomplete", err)
	}
	var ae *axi.Error
	if !errors.As(err, &ae) || !strings.Contains(ae.Msg, "(left in the store: "+st.TicketRelDir(minted)+")") {
		t.Fatalf("err = %v, want the minted folder named as left over", err)
	}
}

// TestUndoPathsNamesOnlyTheFolderACleanLeft covers a clean that fails (a file
// inside the minted folder is held open) after a tracked edited file was
// restored fine: only the folder is left over.
func TestUndoPathsNamesOnlyTheFolderACleanLeft(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("lockFileExclusive has no portable non-Windows equivalent")
	}
	st, work := newTestStandaloneStore(t)
	sha := strings.TrimSpace(runGit(t, work, "rev-parse", "HEAD"))
	if err := os.WriteFile(filepath.Join(work, "project.yaml"), []byte("schema_version: 1\nextra: edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	folder := filepath.Join(work, "tickets", "JIG-1")
	if err := os.MkdirAll(folder, 0o755); err != nil {
		t.Fatal(err)
	}
	held := filepath.Join(folder, "ticket.yaml")
	if err := os.WriteFile(held, []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	unlock := lockFileExclusive(t, held)
	defer unlock()

	// A second minted folder with nothing held in it: the clean still takes it
	// after failing on the first, so it is not left over.
	free := filepath.Join(work, "tickets", "JIG-2")
	if err := os.MkdirAll(free, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(free, "ticket.yaml"), []byte("y\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	err := st.undoPaths(sha, []string{"project.yaml", filepath.Join("tickets", "JIG-1"), filepath.Join("tickets", "JIG-2")})
	var upe *undoPathsError
	if !errors.As(err, &upe) {
		t.Fatalf("err = %v, want an *undoPathsError", err)
	}
	if _, err := os.Stat(free); !os.IsNotExist(err) {
		t.Fatalf("the free folder survived the clean (stat err %v)", err)
	}
	if len(upe.leftovers) != 1 || upe.leftovers[0] != filepath.Join("tickets", "JIG-1") {
		t.Fatalf("leftovers = %v, want the minted folder alone", upe.leftovers)
	}
	if data, rerr := os.ReadFile(filepath.Join(work, "project.yaml")); rerr != nil || strings.Contains(string(data), "edited") {
		t.Fatalf("project.yaml = %q, %v, want the tracked file restored", data, rerr)
	}
}

// TestOutermostDropsPathsInsideAnother pins the clean's pathspec: a file
// listed with the folder that holds it is cleaned through the folder, since git
// leaves the emptied folder behind when the file comes first.
func TestOutermostDropsPathsInsideAnother(t *testing.T) {
	got := outermost([]string{"a/b/7/record.yaml", "a/b/7", "c", "c", "ab"})
	want := []string{"a/b/7", "c", "ab"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("outermost = %v, want %v", got, want)
	}
}

// TestUndoPathsTakesStagedPathsOutOnAnUnbornBranch covers a claim's own add
// on a store with nothing committed: clean skips what the index holds, so the
// undo must unstage each path too.
func TestUndoPathsTakesStagedPathsOutOnAnUnbornBranch(t *testing.T) {
	work := t.TempDir()
	runGit(t, "", "init", "-b", "main", work)
	st := &Store{Root: work}
	folder := filepath.Join(work, "tickets", "JIG-1")
	if err := os.MkdirAll(folder, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(folder, "ticket.yaml"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, work, "add", "--", filepath.Join("tickets", "JIG-1"))
	if err := st.undoPaths("", []string{filepath.Join("tickets", "JIG-1")}); err != nil {
		t.Fatal(err)
	}
	if status := strings.TrimSpace(runGit(t, work, "status", "--porcelain")); status != "" {
		t.Fatalf("git status = %q, want clean", status)
	}
	if _, err := os.Stat(folder); !os.IsNotExist(err) {
		t.Fatalf("the folder survived (stat err %v)", err)
	}
}

// TestClaimUndoesAFailedCommitOnAnUnbornStore covers a first claim on a store
// with nothing committed, whose commit fails (the store's own failing
// pre-commit hook): the claim's own add staged the mint, so the undo must
// unstage it, and a complete undo is reported as one.
func TestClaimUndoesAFailedCommitOnAnUnbornStore(t *testing.T) {
	work := t.TempDir()
	runGit(t, "", "init", "-b", "main", work)
	runGit(t, work, "config", "user.name", "tester")
	runGit(t, work, "config", "user.email", "tester@example.invalid")
	if err := os.WriteFile(filepath.Join(work, "project.yaml"), []byte("schema_version: 1\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, ".gitattributes"), []byte(gitx.StoreAttributes), 0o644); err != nil {
		t.Fatal(err)
	}
	st, err := Open(work)
	if err != nil {
		t.Fatal(err)
	}
	failingHooks(t, filepath.Join(work, ".git", "hooks"), "pre-commit")
	var minted string
	_, err = st.Claim(mintingWrite(st, &minted), func(id string) string { return id })
	if err == nil {
		t.Fatal("Claim = nil, want the failing pre-commit hook to stop the commit")
	}
	if IsUndoIncomplete(err) {
		t.Fatalf("err = %v, want a complete undo", err)
	}
	if status := runGit(t, work, "status", "--porcelain", "--", "tickets"); strings.TrimSpace(status) != "" {
		t.Fatalf("git status lists %q under tickets/, want nothing", status)
	}
	if _, err := os.Stat(filepath.Join(work, "tickets", minted)); !os.IsNotExist(err) {
		t.Fatalf("the minted folder survived a failed commit (stat err %v)", err)
	}
}

// TestClaimUndoesAFailedCommitWithAnEditedTrackedFile covers a write that
// mints a folder and edits a tracked file, reporting both, whose commit then
// fails: the tracked file comes back to its committed content (it is
// restored, never taken out of the index and cleaned) and the folder is gone.
func TestClaimUndoesAFailedCommitWithAnEditedTrackedFile(t *testing.T) {
	st, work := newTestStandaloneStore(t)
	failingHooks(t, filepath.Join(work, ".git", "hooks"), "pre-commit")
	var minted string
	mint := mintingWrite(st, &minted)
	_, err := st.Claim(
		func() (string, []string, error) {
			id, paths, werr := mint()
			if werr != nil {
				return "", nil, werr
			}
			if err := os.WriteFile(filepath.Join(work, "project.yaml"), []byte("schema_version: 1\nextra: edited\n"), 0o644); err != nil {
				return "", nil, err
			}
			return id, append(paths, "project.yaml"), nil
		},
		func(id string) string { return id },
	)
	if err == nil {
		t.Fatal("Claim = nil, want the failing pre-commit hook to stop the commit")
	}
	if IsUndoIncomplete(err) {
		t.Fatalf("err = %v, want a complete undo", err)
	}
	if data, rerr := os.ReadFile(filepath.Join(work, "project.yaml")); rerr != nil || string(data) != "schema_version: 1\n" {
		t.Fatalf("project.yaml = %q, %v, want its committed content", data, rerr)
	}
	if _, err := os.Stat(filepath.Join(work, "tickets", minted)); !os.IsNotExist(err) {
		t.Fatalf("the minted folder survived a failed commit (stat err %v)", err)
	}
}

// TestUndoPathsNamesAFolderOnceWhenTwoOfItsStepsFail covers a created folder
// whose unstaging fails (the index is locked) and whose clean then fails too (a
// file in it is held open): it is named once.
func TestUndoPathsNamesAFolderOnceWhenTwoOfItsStepsFail(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("lockFileExclusive has no portable non-Windows equivalent")
	}
	st, work := newTestStandaloneStore(t)
	sha := strings.TrimSpace(runGit(t, work, "rev-parse", "HEAD"))
	folder := filepath.Join(work, "tickets", "JIG-1")
	if err := os.MkdirAll(folder, 0o755); err != nil {
		t.Fatal(err)
	}
	held := filepath.Join(folder, "ticket.yaml")
	if err := os.WriteFile(held, []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, ".git", "index.lock"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	unlock := lockFileExclusive(t, held)
	defer unlock()

	err := st.undoPaths(sha, []string{filepath.Join("tickets", "JIG-1")})
	var upe *undoPathsError
	if !errors.As(err, &upe) {
		t.Fatalf("err = %v, want an *undoPathsError", err)
	}
	if len(upe.leftovers) != 1 || upe.leftovers[0] != filepath.Join("tickets", "JIG-1") {
		t.Fatalf("leftovers = %v, want the folder once", upe.leftovers)
	}
}

// chartRewritingWrite is a graduate-shaped write: it mints a folder and
// rewrites a tracked or untracked file in place, reporting both.
func chartRewritingWrite(st *Store, work, rel, rewritten string, minted *string) func() (string, []string, error) {
	mint := mintingWrite(st, minted)
	return func() (string, []string, error) {
		id, paths, err := mint()
		if err != nil {
			return "", nil, err
		}
		if err := os.WriteFile(filepath.Join(work, filepath.FromSlash(rel)), []byte(rewritten), 0o644); err != nil {
			return "", nil, err
		}
		return id, append(paths, rel), nil
	}
}

// TestClaimUndoesAFailedCommitKeepingUncommittedEdits covers a chart
// whose hand-written entries no commit holds, on a store with no origin (Sync
// returns before committing there): the write rewrites it and the commit
// fails, and the chart holds the entries byte for byte, the minted folder gone.
func TestClaimUndoesAFailedCommitKeepingUncommittedEdits(t *testing.T) {
	st, work := newTestStandaloneStore(t)
	const chart = "charts/c/tickets.yaml"
	if err := os.MkdirAll(filepath.Join(work, "charts", "c"), 0o755); err != nil {
		t.Fatal(err)
	}
	committed := "entries:\n- title: one\n"
	if err := os.WriteFile(filepath.Join(work, filepath.FromSlash(chart)), []byte(committed), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, work, "add", "--", chart)
	runGit(t, work, "commit", "-m", "chart")
	handWritten := committed + "- title: two, typed by hand\n- title: three\n"
	if err := os.WriteFile(filepath.Join(work, filepath.FromSlash(chart)), []byte(handWritten), 0o644); err != nil {
		t.Fatal(err)
	}
	failingHooks(t, filepath.Join(work, ".git", "hooks"), "pre-commit")
	var minted string
	_, err := st.Claim(chartRewritingWrite(st, work, chart, committed+"- title: one, id added\n", &minted), func(id string) string { return id })
	if err == nil {
		t.Fatal("Claim = nil, want the failing pre-commit hook to stop the commit")
	}
	if IsUndoIncomplete(err) {
		t.Fatalf("err = %v, want a complete undo", err)
	}
	if data, rerr := os.ReadFile(filepath.Join(work, filepath.FromSlash(chart))); rerr != nil || string(data) != handWritten {
		t.Fatalf("chart = %q, %v, want the hand-written entries byte for byte", data, rerr)
	}
	if _, err := os.Stat(filepath.Join(work, "tickets", minted)); !os.IsNotExist(err) {
		t.Fatalf("the minted folder survived a failed commit (stat err %v)", err)
	}
}

// TestClaimFailedCommitKeepsAChartNeverCommitted covers the same failure for a
// chart no commit holds at all: it is not deleted, it holds what it did
// before the write.
func TestClaimFailedCommitKeepsAChartNeverCommitted(t *testing.T) {
	st, work := newTestStandaloneStore(t)
	const chart = "charts/c/tickets.yaml"
	if err := os.MkdirAll(filepath.Join(work, "charts", "c"), 0o755); err != nil {
		t.Fatal(err)
	}
	handWritten := "entries:\n- title: one\n- title: two\n"
	if err := os.WriteFile(filepath.Join(work, filepath.FromSlash(chart)), []byte(handWritten), 0o644); err != nil {
		t.Fatal(err)
	}
	failingHooks(t, filepath.Join(work, ".git", "hooks"), "pre-commit")
	var minted string
	_, err := st.Claim(chartRewritingWrite(st, work, chart, "entries:\n- title: one\n  id: X-1\n", &minted), func(id string) string { return id })
	if err == nil {
		t.Fatal("Claim = nil, want the failing pre-commit hook to stop the commit")
	}
	if IsUndoIncomplete(err) {
		t.Fatalf("err = %v, want a complete undo", err)
	}
	if data, rerr := os.ReadFile(filepath.Join(work, filepath.FromSlash(chart))); rerr != nil || string(data) != handWritten {
		t.Fatalf("chart = %q, %v, want what it held before the write", data, rerr)
	}
	if _, err := os.Stat(filepath.Join(work, "tickets", minted)); !os.IsNotExist(err) {
		t.Fatalf("the minted folder survived a failed commit (stat err %v)", err)
	}
	if status := runGit(t, work, "status", "--porcelain", "--", "tickets"); strings.TrimSpace(status) != "" {
		t.Fatalf("git status lists %q under tickets/, want nothing", status)
	}
}

// TestClaimFailedCommitLeavesAFileDeletedBeforeTheWrite covers a reported file
// a person had deleted without committing: the write recreates it, and the
// undo leaves it deleted, as it was just before the write.
func TestClaimFailedCommitLeavesAFileDeletedBeforeTheWrite(t *testing.T) {
	st, work := newTestStandaloneStore(t)
	if err := os.Remove(filepath.Join(work, "project.yaml")); err != nil {
		t.Fatal(err)
	}
	failingHooks(t, filepath.Join(work, ".git", "hooks"), "pre-commit")
	var minted string
	_, err := st.Claim(chartRewritingWrite(st, work, "project.yaml", "schema_version: 1\nextra: x\n", &minted), func(id string) string { return id })
	if err == nil {
		t.Fatal("Claim = nil, want the failing pre-commit hook to stop the commit")
	}
	if _, serr := os.Stat(filepath.Join(work, "project.yaml")); !os.IsNotExist(serr) {
		t.Fatalf("project.yaml survived (stat err %v), want it still deleted", serr)
	}
}

// TestClaimSnapshotCostsOneGitProcessOnACleanStore pins what the pre-write
// record adds to a claim's git processes: one status, and no blob written
// when nothing differs from the pre-claim commit.
func TestClaimSnapshotCostsOneGitProcessOnACleanStore(t *testing.T) {
	st, _ := newTestStandaloneStore(t)
	var minted string
	argv := gittest.GitArgv(t, func() {
		if _, err := st.Claim(mintingWrite(st, &minted), func(id string) string { return id }); err != nil {
			t.Fatal(err)
		}
	})
	statuses := 0
	for _, a := range argv {
		t.Log(strings.Join(a, " "))
		if slices.Contains(a, "status") {
			statuses++
		}
		if slices.Contains(a, "hash-object") {
			t.Fatalf("a clean store wrote a blob: %v", a)
		}
	}
	if statuses != 1 {
		t.Fatalf("status ran %d times, want once", statuses)
	}
}

// TestClaimFailedCommitRestoresUncommittedFilesInAReportedFolder covers a
// reported folder: a file in it that held uncommitted work comes back as it was
// before the write, a file the write added to it is gone, and the mode of the
// restored file is kept.
func TestClaimFailedCommitRestoresUncommittedFilesInAReportedFolder(t *testing.T) {
	st, work := newTestStandaloneStore(t)
	folder := filepath.Join(work, "notes")
	if err := os.MkdirAll(folder, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(folder, "a.txt"), []byte("typed by hand\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	failingHooks(t, filepath.Join(work, ".git", "hooks"), "pre-commit")
	_, err := st.Claim(
		func() (string, []string, error) {
			if err := os.WriteFile(filepath.Join(folder, "a.txt"), []byte("rewritten\n"), 0o644); err != nil {
				return "", nil, err
			}
			if err := os.WriteFile(filepath.Join(folder, "b.txt"), []byte("new\n"), 0o644); err != nil {
				return "", nil, err
			}
			return "X-1", []string{"notes"}, nil
		},
		func(id string) string { return id },
	)
	if err == nil {
		t.Fatal("Claim = nil, want the failing pre-commit hook to stop the commit")
	}
	if IsUndoIncomplete(err) {
		t.Fatalf("err = %v, want a complete undo", err)
	}
	if data, rerr := os.ReadFile(filepath.Join(folder, "a.txt")); rerr != nil || string(data) != "typed by hand\n" {
		t.Fatalf("a.txt = %q, %v, want what it held before the write", data, rerr)
	}
	if _, serr := os.Stat(filepath.Join(folder, "b.txt")); !os.IsNotExist(serr) {
		t.Fatalf("b.txt, which the write added, survived (stat err %v)", serr)
	}
	if runtime.GOOS != "windows" {
		if info, serr := os.Stat(filepath.Join(folder, "a.txt")); serr != nil || info.Mode().Perm() != 0o755 {
			t.Fatalf("a.txt mode = %v, %v, want 0755", info, serr)
		}
	}
}

// TestClaimIncompleteUndoKeepsTheRestoredWorkOutOfTheWipeRecipe covers a stale
// index.lock: the commit and the undo's git steps fail on it, yet the snapshot
// restore (plain file writes) works. The error is incomplete and names the
// chart, but the finish-by-hand help must not tell the person to restore or
// clean a path whose file holds the work just put back.
func TestClaimIncompleteUndoKeepsTheRestoredWorkOutOfTheWipeRecipe(t *testing.T) {
	st, work := newTestStandaloneStore(t)
	const chart = "charts/c/tickets.yaml"
	if err := os.MkdirAll(filepath.Join(work, "charts", "c"), 0o755); err != nil {
		t.Fatal(err)
	}
	committed := "entries:\n- title: one\n"
	if err := os.WriteFile(filepath.Join(work, filepath.FromSlash(chart)), []byte(committed), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, work, "add", "--", chart)
	runGit(t, work, "commit", "-m", "chart")
	handWritten := committed + "- title: two, typed by hand\n"
	if err := os.WriteFile(filepath.Join(work, filepath.FromSlash(chart)), []byte(handWritten), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, ".git", "index.lock"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	var minted string
	_, err := st.Claim(chartRewritingWrite(st, work, chart, committed+"- title: one, id added\n", &minted), func(id string) string { return id })
	if !IsUndoIncomplete(err) {
		t.Fatalf("err = %v, want an incomplete undo (the index is locked)", err)
	}
	if data, rerr := os.ReadFile(filepath.Join(work, filepath.FromSlash(chart))); rerr != nil || string(data) != handWritten {
		t.Fatalf("chart = %q, %v, want the hand-written entries", data, rerr)
	}
	var ae *axi.Error
	if !errors.As(err, &ae) {
		t.Fatalf("err = %v, want an *axi.Error inside", err)
	}
	for _, h := range ae.Help {
		if strings.Contains(h, "git restore --source=HEAD") && strings.Contains(h, "tickets.yaml") {
			t.Fatalf("help tells the person to restore the chart over the work just put back:\n%s", h)
		}
		if strings.Contains(h, "git clean") && strings.Contains(h, "tickets.yaml") {
			t.Fatalf("help tells the person to clean the chart:\n%s", h)
		}
	}
}

// TestClaimSnapshotSkipsAFileItCannotRead covers one unreadable dirty file
// elsewhere in the store: the claim goes on rather than refusing before its
// write.
func TestClaimSnapshotSkipsAFileItCannotRead(t *testing.T) {
	st, work := newTestStandaloneStore(t)
	held := filepath.Join(work, "scratch.bin")
	if err := os.WriteFile(held, []byte("x\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if runtime.GOOS == "windows" {
		defer lockFileExclusive(t, held)()
	} else {
		if os.Geteuid() == 0 {
			t.Skip("root reads any file")
		}
		if err := os.Chmod(held, 0); err != nil {
			t.Fatal(err)
		}
		defer os.Chmod(held, 0o600)
	}
	var minted string
	if _, err := st.Claim(mintingWrite(st, &minted), func(id string) string { return id }); err != nil {
		t.Fatalf("Claim = %v, want an unreadable unrelated file not to refuse it", err)
	}
}

// TestClaimSnapshotIgnoresGitStderr covers a git that writes to stderr on
// every call (an fsmonitor hook here): the warning must not be parsed as a status
// record, or the first dirty path goes unrecorded and its work is lost.
func TestClaimSnapshotIgnoresGitStderr(t *testing.T) {
	st, work := newTestStandaloneStore(t)
	// An fsmonitor hook that complains on stderr makes every status warn.
	hook := filepath.Join(t.TempDir(), "fsmonitor")
	if err := os.WriteFile(hook, []byte("#!/bin/sh\necho 'warning: a complaint on stderr' >&2\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, work, "config", "core.fsmonitor", filepath.ToSlash(hook))
	handWritten := "schema_version: 1\nhand: written\n"
	if err := os.WriteFile(filepath.Join(work, "project.yaml"), []byte(handWritten), 0o644); err != nil {
		t.Fatal(err)
	}
	failingHooks(t, filepath.Join(work, ".git", "hooks"), "pre-commit")
	var minted string
	_, err := st.Claim(chartRewritingWrite(st, work, "project.yaml", "schema_version: 1\nrewritten: yes\n", &minted), func(id string) string { return id })
	if err == nil {
		t.Fatal("Claim = nil, want the failing pre-commit hook to stop the commit")
	}
	if data, rerr := os.ReadFile(filepath.Join(work, "project.yaml")); rerr != nil || string(data) != handWritten {
		t.Fatalf("project.yaml = %q, %v, want the hand-written content", data, rerr)
	}
}

// TestClaimFailedCommitRestoresAnUncommittedChmod covers a tracked file whose
// mode a person changed without committing: the undo's git restore resets the
// mode to the commit's, and the restore puts the recorded one back.
func TestClaimFailedCommitRestoresAnUncommittedChmod(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("modes are not kept on Windows")
	}
	st, work := newTestStandaloneStore(t)
	if err := os.Chmod(filepath.Join(work, "project.yaml"), 0o755); err != nil {
		t.Fatal(err)
	}
	failingHooks(t, filepath.Join(work, ".git", "hooks"), "pre-commit")
	var minted string
	_, err := st.Claim(chartRewritingWrite(st, work, "project.yaml", "schema_version: 1\nx: y\n", &minted), func(id string) string { return id })
	if err == nil {
		t.Fatal("Claim = nil, want the failing pre-commit hook to stop the commit")
	}
	if info, serr := os.Stat(filepath.Join(work, "project.yaml")); serr != nil || info.Mode().Perm() != 0o755 {
		t.Fatalf("project.yaml mode = %v, %v, want 0755", info, serr)
	}
}

// TestClaimSnapshotTakesNoOptionalLock pins that the pre-write status does not
// refresh the index on disk: a person's own git command in the store would
// otherwise fail on the lock it took.
func TestClaimSnapshotTakesNoOptionalLock(t *testing.T) {
	st, _ := newTestStandaloneStore(t)
	var minted string
	argv := gittest.GitArgv(t, func() {
		if _, err := st.Claim(mintingWrite(st, &minted), func(id string) string { return id }); err != nil {
			t.Fatal(err)
		}
	})
	for _, a := range argv {
		if slices.Contains(a, "status") && !slices.Contains(a, "--no-optional-locks") {
			t.Fatalf("status without --no-optional-locks: %v", a)
		}
	}
}
