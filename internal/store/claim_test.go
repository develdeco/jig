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

// TestClaimRemintsAfterRejectedPushPreservesUnrelatedLocalCommit covers the
// other half of the same race, r1-f6 called out as untested: another
// process on this same clone commits its own work (as Store.Sync's or
// Store.Push's own `add -A` would) on top of this claim's own commit, in
// the window between the two - so by the time the push is rejected,
// undoClaim's `reset --soft` moves the branch back past both commits at
// once, leaving the other process's content staged against the rewound
// HEAD rather than committed under its own name (see undoClaim). Without
// stageAndCommitPaths' own pathspec-scoped commit, the retried claim's
// plain `git commit` would sweep that staged content into its own commit,
// under its own message, and push it to the origin as if it were part of
// the claim.
func TestClaimRemintsAfterRejectedPushPreservesUnrelatedLocalCommit(t *testing.T) {
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
				// Another process on this same clone, committing its own
				// work while this claim is in flight - lands on top of
				// this claim's own commit once stageAndCommitPaths makes
				// it below.
				if err := os.WriteFile(filepath.Join(work, "from-same-clone.txt"), []byte("x\n"), 0o644); err != nil {
					t.Fatal(err)
				}
				runGit(t, work, "add", "-A")
				runGit(t, work, "-c", "user.name=other-local", "-c", "user.email=other-local@example.invalid", "commit", "-m", "other local commit")
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

	data, err := os.ReadFile(filepath.Join(work, "from-same-clone.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "x\n" {
		t.Fatalf("from-same-clone.txt = %q, want the other process's commit content to survive the rejected claim's undo", data)
	}
	status := runGit(t, work, "status", "--porcelain")
	if !strings.Contains(status, "from-same-clone.txt") {
		t.Fatalf("status = %q, want from-same-clone.txt still showing as uncommitted (its own commit was orphaned by the undo)", status)
	}

	remoteLog := runGit(t, "", "--git-dir", remote, "log", "--pretty=%s")
	for _, want := range []string{"from other", "JIG-1: new ticket"} {
		if !strings.Contains(remoteLog, want) {
			t.Fatalf("remote log = %q, want it to contain %q", remoteLog, want)
		}
	}
	if strings.Contains(remoteLog, "other local commit") {
		t.Fatalf("remote log = %q, want the orphaned local commit never pushed under its own message", remoteLog)
	}
	remoteFiles := runGit(t, "", "--git-dir", remote, "show", "--name-only", "--pretty=", "HEAD")
	if strings.Contains(remoteFiles, "from-same-clone.txt") {
		t.Fatalf("pushed commit's files = %q, want the other process's content never swept into the claim's own commit", remoteFiles)
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
