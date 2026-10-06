package store

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/axi"
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
// of the same race, r1-f6 called out as fixed per the human's decision
// (slice fix-3-r1-f6): another process on this same clone commits its own
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

			id, err := st.Mint("JIG-{n}", Ticket{Title: "x"})
			if err != nil {
				return "", nil, err
			}
			return id, []string{id}, nil
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
			id, merr := st.Mint("JIG-{n}", Ticket{Title: "x"})
			if merr != nil {
				return "", nil, merr
			}
			return id, []string{id}, nil
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
