package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/gitx"
)

// TestTicketNewLeavesTheStoreLevelWithItsOriginAfterTheSync reproduces the
// gap end to end: a store with an origin and a GitHub mirror, and the last
// checkpoint of `jig ticket new`. The mirror's claim lands the new issue's
// bare record on origin; the sync then fills the record in (the issue's
// synced: fields, the board item) and, before this fix, left those writes in
// the working tree, so a teammate's clone and the store on GitHub never saw
// them until some later command's checkpoint swept them up.
func TestTicketNewLeavesTheStoreLevelWithItsOriginAfterTheSync(t *testing.T) {
	t.Parallel()
	e := testEnv(t.TempDir())
	clone := filepath.Join(t.TempDir(), "clone")
	remote := newTestOriginCloneWithGitHubTracker(t, clone)
	e.mirrorClient = &stubGitHub{}

	var buf bytes.Buffer
	if code := run(e, []string{"ticket", "new", "--title", "Fix the thing", "--store", clone}, &buf, strings.NewReader("")); code != 0 {
		t.Fatalf("jig ticket new: exit %d\n%s", code, buf.String())
	}

	if status, err := gitx.Run(clone, "status", "--porcelain"); err != nil {
		t.Fatal(err)
	} else if status != "" {
		t.Errorf("the sync's record writes stayed uncommitted in the store:\n%s", status)
	}
	local, err := gitx.Run(clone, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	origin, err := gitx.Run(remote, "rev-parse", "main")
	if err != nil {
		t.Fatal(err)
	}
	if local != origin {
		t.Errorf("origin is at %s, want it level with the store at %s", origin, local)
	}
	// What origin holds is what the sync left: the record carries synced:.
	shown, err := gitx.Run(remote, "show", "main:trackers/github/example/tracking/1/record.yaml")
	if err != nil {
		t.Fatalf("origin has no record for issue 1: %v", err)
	}
	if !strings.Contains(shown, "synced:") {
		t.Errorf("origin's record lacks the sync's synced: fields:\n%s", shown)
	}
}

// leaveASpendLine appends, to ticket's journal, a spend line as the observe
// hook leaves one: written, not committed.
func leaveASpendLine(t *testing.T, clone, ticket string) {
	t.Helper()
	path := filepath.Join(clone, "tickets", ticket, "journal.ndjson")
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if _, err := f.WriteString(`{"ts":"2026-10-28T10:00:00Z","ticket":"` + ticket + `","event":"spend","session":"s-1"}` + "\n"); err != nil {
		t.Fatal(err)
	}
}

// TestTrackersSyncLeavesNothingUncommittedOrUnpushed: the store is left behind
// its origin by a session that wrote outside any checkpoint (the spend line
// of its last Stop) and by a sync that never ran (the checkpoint's own failed).
// `jig trackers sync` commits the leftover, mirrors the ticket, and pushes
// the lot: the working tree is clean, origin is level with the store, and
// origin holds both the spend line and the record as the sync left it.
func TestTrackersSyncLeavesNothingUncommittedOrUnpushed(t *testing.T) {
	t.Parallel()
	e := testEnv(t.TempDir())
	clone := filepath.Join(t.TempDir(), "clone")
	remote := newTestOriginCloneWithGitHubTracker(t, clone)
	// The ticket's own checkpoint sync fails, so the ticket has no record.
	e.mirrorClient = stubFailingMirrorClient(t)
	var buf bytes.Buffer
	if code := run(e, []string{"ticket", "new", "--title", "Fix the thing", "--store", clone}, &buf, strings.NewReader("")); code != 0 {
		t.Fatalf("jig ticket new: exit %d\n%s", code, buf.String())
	}
	leaveASpendLine(t, clone, "DEMO-1")

	e.mirrorClient = &stubGitHub{}
	buf.Reset()
	if code := run(e, []string{"trackers", "sync", "--store", clone}, &buf, strings.NewReader("")); code != 0 {
		t.Fatalf("jig trackers sync: exit %d\n%s", code, buf.String())
	}

	if status, err := gitx.Run(clone, "status", "--porcelain"); err != nil {
		t.Fatal(err)
	} else if status != "" {
		t.Errorf("jig trackers sync left the store dirty:\n%s", status)
	}
	local, err := gitx.Run(clone, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if origin, err := gitx.Run(remote, "rev-parse", "main"); err != nil {
		t.Fatal(err)
	} else if local != origin {
		t.Errorf("origin is at %s, want it level with the store at %s", origin, local)
	}
	if journal, err := gitx.Run(remote, "show", "main:tickets/DEMO-1/journal.ndjson"); err != nil || !strings.Contains(journal, `"event":"spend"`) {
		t.Errorf("origin's journal = %q (%v), want the spend line the sync committed", journal, err)
	}
	if record, err := gitx.Run(remote, "show", "main:trackers/github/example/tracking/1/record.yaml"); err != nil || !strings.Contains(record, "synced:") {
		t.Errorf("origin's record = %q (%v), want it as the sync left it, with synced:", record, err)
	}
}

// TestTrackersSyncDryRunCommitsAndPushesNothing: a dry run writes to neither
// GitHub nor the store, and the landing is part of what it does not do.
func TestTrackersSyncDryRunCommitsAndPushesNothing(t *testing.T) {
	t.Parallel()
	e := testEnv(t.TempDir())
	clone := filepath.Join(t.TempDir(), "clone")
	remote := newTestOriginCloneWithGitHubTracker(t, clone)
	e.mirrorClient = stubFailingMirrorClient(t)
	var buf bytes.Buffer
	if code := run(e, []string{"ticket", "new", "--title", "Fix the thing", "--store", clone}, &buf, strings.NewReader("")); code != 0 {
		t.Fatalf("jig ticket new: exit %d\n%s", code, buf.String())
	}
	leaveASpendLine(t, clone, "DEMO-1")
	headBefore, err := gitx.Run(clone, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	originBefore, err := gitx.Run(remote, "rev-parse", "main")
	if err != nil {
		t.Fatal(err)
	}

	buf.Reset()
	if code := run(e, []string{"trackers", "sync", "--dry-run", "--store", clone}, &buf, strings.NewReader("")); code != 0 {
		t.Fatalf("jig trackers sync --dry-run: exit %d\n%s", code, buf.String())
	}

	if head, _ := gitx.Run(clone, "rev-parse", "HEAD"); head != headBefore {
		t.Errorf("--dry-run moved the store's HEAD from %s to %s", headBefore, head)
	}
	if origin, _ := gitx.Run(remote, "rev-parse", "main"); origin != originBefore {
		t.Errorf("--dry-run moved origin from %s to %s", originBefore, origin)
	}
	if status, _ := gitx.Run(clone, "status", "--porcelain"); status == "" {
		t.Error("--dry-run committed the leftover spend line")
	}
}
