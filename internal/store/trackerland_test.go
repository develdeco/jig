package store

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// newLandStore is a store with an origin and the ticket JIG-1 committed and
// pushed, ready for a hook that writes its tracker records.
func newLandStore(t *testing.T) (st *Store, work, remote string, warnings *[]string) {
	t.Helper()
	st, work, remote = newTestRemoteStore(t)
	if err := st.CreateTicketRecord("JIG-1", Ticket{Title: "Ticket JIG-1"}); err != nil {
		t.Fatal(err)
	}
	if err := st.Push("JIG-1: ticket"); err != nil {
		t.Fatal(err)
	}
	warnings = new([]string)
	st.Warn = func(format string, args ...any) { *warnings = append(*warnings, fmt.Sprintf(format, args...)) }
	return st, work, remote, warnings
}

// porcelain is the store's `git status --porcelain`, "" for a clean tree.
func porcelain(t *testing.T, work string) string {
	t.Helper()
	return strings.TrimSpace(runGit(t, work, "status", "--porcelain"))
}

// TestCheckpointHookLandsTheTrackerWritesItMade is the seam of the gap: the
// hook's sync writes records into the working tree (an issue's prs: entry,
// its synced: fields) and, before they were landed, nothing committed them
// when the hook belonged to a ticket's last checkpoint. They are committed
// as the person, in a commit of their own that holds only the tracker
// paths, and pushed.
func TestCheckpointHookLandsTheTrackerWritesItMade(t *testing.T) {
	st, work, remote, warnings := newLandStore(t)
	entry := TrackerEntry{Provider: ProviderGitHub, Scope: "acme/tracking", ID: "7"}
	st.AfterCheckpoint = func(s *Store) error {
		putRecord(t, s, TrackerOwner{Ticket: "JIG-1"}, "acme/tracking", 7)
		return nil
	}
	// Another process's journal line, written outside any checkpoint: not
	// the hook's, and not for this landing to take.
	journal := filepath.Join(st.TicketDir("JIG-1"), "journal.ndjson")
	if err := os.WriteFile(journal, []byte("{\"event\":\"spend\"}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	st.RunCheckpointHook()

	if len(*warnings) != 0 {
		t.Fatalf("warnings = %v, want none", *warnings)
	}
	rel := filepath.ToSlash(st.TrackerRecordRelPath(entry))
	if got := runGit(t, remote, "show", "main:"+rel); !strings.Contains(got, "issue: 7") {
		t.Fatalf("origin's %s = %q, want the record the hook wrote", rel, got)
	}
	if subject := strings.TrimSpace(runGit(t, work, "log", "-1", "--pretty=%s")); subject != trackerLandMessage {
		t.Fatalf("last commit = %q, want the landing's %q", subject, trackerLandMessage)
	}
	if author := strings.TrimSpace(runGit(t, work, "log", "-1", "--pretty=%an <%ae>")); author != "tester <tester@example.invalid>" {
		t.Fatalf("landing authored by %q, want the person's identity", author)
	}
	if files := strings.Fields(runGit(t, work, "show", "--name-only", "--pretty=format:", "HEAD")); len(files) != 1 || files[0] != rel {
		t.Fatalf("the landing holds %v, want only %s", files, rel)
	}
	if local, origin := strings.TrimSpace(runGit(t, work, "rev-parse", "HEAD")), strings.TrimSpace(runGit(t, remote, "rev-parse", "main")); local != origin {
		t.Fatalf("origin is at %s, want it level with the store at %s", origin, local)
	}
	if got := porcelain(t, work); !strings.Contains(got, "journal.ndjson") || strings.Count(got, "\n") != 0 {
		t.Fatalf("working tree = %q, want only the other process's journal line left uncommitted", got)
	}
}

// TestCheckpointHookLandsWhatAFailedHookWrote: the records a sync wrote before
// it failed are true and go out; the failure is still the hook's warning.
func TestCheckpointHookLandsWhatAFailedHookWrote(t *testing.T) {
	st, _, remote, warnings := newLandStore(t)
	st.AfterCheckpoint = func(s *Store) error {
		putRecord(t, s, TrackerOwner{Ticket: "JIG-1"}, "acme/tracking", 7)
		return errors.New("github is down")
	}

	st.RunCheckpointHook()

	if len(*warnings) != 1 || !strings.Contains((*warnings)[0], "github is down") {
		t.Fatalf("warnings = %v, want the one naming the hook's cause", *warnings)
	}
	if got := runGit(t, remote, "show", "main:trackers/github/acme/tracking/7/record.yaml"); !strings.Contains(got, "issue: 7") {
		t.Fatalf("origin lacks the record the failed hook wrote: %q", got)
	}
}

// TestCheckpointHookThatWroteNothingCommitsAndPushesNothing: a sync that finds
// the store up to date rewrites no record (a record already holding the bytes
// is left alone), so the checkpoint costs no commit and no push.
func TestCheckpointHookThatWroteNothingCommitsAndPushesNothing(t *testing.T) {
	st, work, remote, warnings := newLandStore(t)
	write := func(s *Store) error {
		putRecord(t, s, TrackerOwner{Ticket: "JIG-1"}, "acme/tracking", 7)
		return nil
	}
	st.AfterCheckpoint = write
	st.RunCheckpointHook()
	head := strings.TrimSpace(runGit(t, work, "rev-parse", "HEAD"))

	// A second sync writes the same record again; origin then refuses every
	// push, so one attempted would surface as a warning.
	runGit(t, work, "remote", "set-url", "origin", filepath.Join(filepath.Dir(remote), "gone.git"))
	st.RunCheckpointHook()

	if got := strings.TrimSpace(runGit(t, work, "rev-parse", "HEAD")); got != head {
		t.Fatalf("HEAD moved from %s to %s, want the second sync to commit nothing", head, got)
	}
	if len(*warnings) != 0 {
		t.Fatalf("warnings = %v, want none: nothing was written, so nothing was pushed", *warnings)
	}
}

// TestCheckpointHookLandsARecordItRemovesAndOneItAdds: a record the hook
// removes goes from origin, and one it adds comes in, in the same landing.
func TestCheckpointHookLandsARecordItRemovesAndOneItAdds(t *testing.T) {
	st, work, remote, warnings := newLandStore(t)
	e := putRecord(t, st, TrackerOwner{Ticket: "JIG-1"}, "acme/tracking", 7)
	if err := st.Push("JIG-1: record"); err != nil {
		t.Fatal(err)
	}
	st.takeTrackerWrites()

	st.AfterCheckpoint = func(s *Store) error {
		if err := s.RemoveTrackerRecord(e); err != nil {
			return err
		}
		putRecord(t, s, TrackerOwner{Ticket: "JIG-1"}, "acme/tracking", 8)
		return nil
	}
	st.RunCheckpointHook()

	if len(*warnings) != 0 {
		t.Fatalf("warnings = %v, want none", *warnings)
	}
	if got := porcelain(t, work); got != "" {
		t.Fatalf("working tree still dirty after the landing:\n%s", got)
	}
	tree := runGit(t, remote, "ls-tree", "-r", "--name-only", "main", "trackers")
	if !strings.Contains(tree, "trackers/github/acme/tracking/8/record.yaml\n") {
		t.Errorf("origin's trackers/ lacks the added record:\n%s", tree)
	}
	if strings.Contains(tree, "trackers/github/acme/tracking/7/record.yaml") {
		t.Errorf("origin's trackers/ still holds the removed record:\n%s", tree)
	}
}

// TestCheckpointHookLandingLeavesAForeignStagedFileStaged: a file another
// process staged is not the landing's to commit, so the landing's commit holds
// only the tracker path and the file stays staged.
func TestCheckpointHookLandingLeavesAForeignStagedFileStaged(t *testing.T) {
	st, work, remote, warnings := newLandStore(t)
	if err := os.WriteFile(filepath.Join(work, "foreign.txt"), []byte("staged elsewhere\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, work, "add", "foreign.txt")
	st.AfterCheckpoint = func(s *Store) error {
		putRecord(t, s, TrackerOwner{Ticket: "JIG-1"}, "acme/tracking", 7)
		return nil
	}

	st.RunCheckpointHook()

	if len(*warnings) != 0 {
		t.Fatalf("warnings = %v, want none", *warnings)
	}
	if got := porcelain(t, work); got != "A  foreign.txt" {
		t.Errorf("status = %q, want only foreign.txt still staged", got)
	}
	tree := runGit(t, remote, "ls-tree", "-r", "--name-only", "main")
	if !strings.Contains(tree, "trackers/github/acme/tracking/7/record.yaml\n") || strings.Contains(tree, "foreign.txt") {
		t.Errorf("origin's tree = %q, want the record and not foreign.txt", tree)
	}
}

// TestCheckpointHookLandsAFlatRecordMovedIntoItsFolder: the sync that moves a
// record out of the flat layout takes away the link that sat where the entry's
// folder goes (a plain file where the clone has no symlinks), and a file and a
// folder cannot share a path, so the landing commits the removal and the
// folder's record together and origin ends with the folder alone.
func TestCheckpointHookLandsAFlatRecordMovedIntoItsFolder(t *testing.T) {
	st, work, remote, warnings := newLandStore(t)
	e := TrackerEntry{Provider: ProviderGitHub, Scope: "acme/tracking", ID: "7"}
	writeFlat(t, st, e, TrackerOwner{Ticket: "JIG-1"}, flatText("JIG-1", "7"))
	if err := st.Push("JIG-1: flat layout"); err != nil {
		t.Fatal(err)
	}
	st.takeTrackerWrites()

	st.AfterCheckpoint = func(s *Store) error {
		moved, err := s.MoveFlatTrackerRecords()
		if err == nil && len(moved) != 1 {
			err = fmt.Errorf("moved %v, want the one flat record", moved)
		}
		return err
	}
	st.RunCheckpointHook()

	for _, w := range *warnings {
		if strings.Contains(w, "failed") || strings.Contains(w, "not landed") {
			t.Fatalf("warning %q, want the move landed", w)
		}
	}
	if got := porcelain(t, work); got != "" {
		t.Fatalf("working tree still dirty after the landing:\n%s", got)
	}
	tree := runGit(t, remote, "ls-tree", "-r", "--name-only", "main", "trackers")
	if !strings.Contains(tree, "trackers/github/acme/tracking/7/record.yaml\n") {
		t.Errorf("origin's trackers/ lacks the record in its folder:\n%s", tree)
	}
	for _, flat := range []string{"trackers/github/acme/tracking/7.yaml\n", "trackers/github/acme/tracking/7\n"} {
		if strings.Contains(tree, flat) {
			t.Errorf("origin's trackers/ still holds the flat layout's %q:\n%s", strings.TrimSpace(flat), tree)
		}
	}
}

// TestCheckpointHookLandsLinks: a link the hook makes lands with its record,
// and one it removes goes.
func TestCheckpointHookLandsLinks(t *testing.T) {
	st, work, remote, warnings := newLandStore(t)
	skipWithoutSymlinks(t, st)
	runGit(t, work, "config", "core.symlinks", "true")
	owner := TrackerOwner{Ticket: "JIG-1"}
	e := putRecord(t, st, owner, "acme/tracking", 7)
	if _, err := st.LinkTrackerEntry(e, owner); err != nil {
		t.Fatal(err)
	}
	if err := st.Push("JIG-1: record"); err != nil {
		t.Fatal(err)
	}
	st.takeTrackerWrites()

	st.AfterCheckpoint = func(s *Store) error {
		if err := s.RemoveTrackerRecord(e); err != nil {
			return err
		}
		e2 := putRecord(t, s, owner, "acme/tracking", 8)
		_, err := s.LinkTrackerEntry(e2, owner)
		return err
	}
	st.RunCheckpointHook()

	if len(*warnings) != 0 {
		t.Fatalf("warnings = %v, want none", *warnings)
	}
	if got := porcelain(t, work); got != "" {
		t.Fatalf("working tree still dirty after the landing:\n%s", got)
	}
	tree := runGit(t, remote, "ls-tree", "-r", "--name-only", "main", "trackers")
	if !strings.Contains(tree, "trackers/github/acme/tracking/8/JIG-1\n") {
		t.Errorf("origin's trackers/ lacks the made link:\n%s", tree)
	}
	if strings.Contains(tree, "trackers/github/acme/tracking/7/JIG-1\n") {
		t.Errorf("origin's trackers/ still holds the removed link:\n%s", tree)
	}
}

// TestCheckpointHookLandsOnAStoreWithNoOrigin: a standalone store commits the
// records and has nothing to push.
func TestCheckpointHookLandsOnAStoreWithNoOrigin(t *testing.T) {
	st, work := newTestStandaloneStore(t)
	if err := st.CreateTicketRecord("JIG-1", Ticket{Title: "Ticket JIG-1"}); err != nil {
		t.Fatal(err)
	}
	if err := st.Push("JIG-1: ticket"); err != nil {
		t.Fatal(err)
	}
	var warnings []string
	st.Warn = func(format string, args ...any) { warnings = append(warnings, fmt.Sprintf(format, args...)) }
	st.AfterCheckpoint = func(s *Store) error {
		putRecord(t, s, TrackerOwner{Ticket: "JIG-1"}, "acme/tracking", 7)
		return nil
	}

	st.RunCheckpointHook()

	if len(warnings) != 0 {
		t.Fatalf("warnings = %v, want none", warnings)
	}
	if got := porcelain(t, work); got != "" {
		t.Fatalf("working tree = %q, want the record committed", got)
	}
	if subject := strings.TrimSpace(runGit(t, work, "log", "-1", "--pretty=%s")); subject != trackerLandMessage {
		t.Fatalf("last commit = %q, want the landing's", subject)
	}
}

// TestCheckpointHookLandingRebasesOntoAMovedOrigin: another clone pushed while
// the sync ran, so the landing's push is rejected, and it takes what moved
// and pushes again, as every checkpoint's push does.
func TestCheckpointHookLandingRebasesOntoAMovedOrigin(t *testing.T) {
	st, work, remote, warnings := newLandStore(t)
	other := filepath.Join(t.TempDir(), "other")
	runGit(t, "", "clone", remote, other)
	runGit(t, other, "config", "user.name", "other")
	runGit(t, other, "config", "user.email", "other@example.invalid")

	st.AfterCheckpoint = func(s *Store) error {
		putRecord(t, s, TrackerOwner{Ticket: "JIG-1"}, "acme/tracking", 7)
		// The other clone pushes while this sync is still running.
		if err := os.WriteFile(filepath.Join(other, "note.txt"), []byte("theirs\n"), 0o644); err != nil {
			return err
		}
		runGit(t, other, "add", "-A")
		runGit(t, other, "commit", "-m", "theirs")
		runGit(t, other, "push", "origin", "main")
		return nil
	}
	st.RunCheckpointHook()

	if len(*warnings) != 0 {
		t.Fatalf("warnings = %v, want none", *warnings)
	}
	files := runGit(t, remote, "ls-tree", "-r", "--name-only", "main")
	for _, want := range []string{"note.txt", "trackers/github/acme/tracking/7/record.yaml"} {
		if !strings.Contains(files, want+"\n") {
			t.Errorf("origin lacks %s:\n%s", want, files)
		}
	}
	if local, origin := strings.TrimSpace(runGit(t, work, "rev-parse", "HEAD")), strings.TrimSpace(runGit(t, remote, "rev-parse", "main")); local != origin {
		t.Fatalf("origin is at %s, want it level with the store at %s", origin, local)
	}
}

// TestLandTrackerWritesPushesWhatSyncCommittedAndNothingWroteSince: `jig
// trackers sync` calls Sync, which commits the leftovers without pushing,
// then the mirror, then LandTrackerWrites: the leftovers' commit reaches
// origin even when the mirror wrote nothing.
func TestLandTrackerWritesPushesWhatSyncCommittedAndNothingWroteSince(t *testing.T) {
	st, work, remote, _ := newLandStore(t)
	journal := filepath.Join(st.TicketDir("JIG-1"), "journal.ndjson")
	if err := os.WriteFile(journal, []byte("{\"event\":\"spend\"}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := st.Sync(); err != nil {
		t.Fatal(err)
	}
	if local, origin := strings.TrimSpace(runGit(t, work, "rev-parse", "HEAD")), strings.TrimSpace(runGit(t, remote, "rev-parse", "main")); local == origin {
		t.Fatal("Sync pushed; the premise of this test is that it commits without pushing")
	}

	if err := st.LandTrackerWrites(); err != nil {
		t.Fatalf("LandTrackerWrites: %v", err)
	}

	if local, origin := strings.TrimSpace(runGit(t, work, "rev-parse", "HEAD")), strings.TrimSpace(runGit(t, remote, "rev-parse", "main")); local != origin {
		t.Fatalf("origin is at %s, want it level with the store at %s", origin, local)
	}
	if got := runGit(t, remote, "show", "main:tickets/JIG-1/journal.ndjson"); !strings.Contains(got, "spend") {
		t.Fatalf("origin's journal = %q, want the leftover Sync committed", got)
	}
	if got := porcelain(t, work); got != "" {
		t.Fatalf("working tree = %q, want clean", got)
	}
}

// TestLandTrackerWritesKeepsItsCommitWhenThePushFails: a landing whose push
// the origin refuses outright returns the cause and keeps its commit, which
// the next checkpoint's push carries.
func TestLandTrackerWritesKeepsItsCommitWhenThePushFails(t *testing.T) {
	st, work, remote, _ := newLandStore(t)
	putRecord(t, st, TrackerOwner{Ticket: "JIG-1"}, "acme/tracking", 7)
	runGit(t, work, "remote", "set-url", "origin", filepath.Join(filepath.Dir(remote), "gone.git"))

	if err := st.LandTrackerWrites(); err == nil {
		t.Fatal("LandTrackerWrites with an unreachable origin = nil, want the push's cause")
	}
	if subject := strings.TrimSpace(runGit(t, work, "log", "-1", "--pretty=%s")); subject != trackerLandMessage {
		t.Fatalf("last commit = %q, want the landing's commit kept for the next push", subject)
	}

	runGit(t, work, "remote", "set-url", "origin", remote)
	if err := st.Push("JIG-1: next checkpoint"); err != nil {
		t.Fatalf("Push once origin is back: %v", err)
	}
	if local, origin := strings.TrimSpace(runGit(t, work, "rev-parse", "HEAD")), strings.TrimSpace(runGit(t, remote, "rev-parse", "main")); local != origin {
		t.Fatalf("origin is at %s, want it level with the store at %s", origin, local)
	}
}

// TestCheckpointHookPushesWritesAnotherProcessesSyncCommitted: another
// process's Sync commits the working tree, the hook's record writes with it,
// and never pushes. The landing then finds nothing of its own to commit, and
// pushes because the branch is ahead of origin.
func TestCheckpointHookPushesWritesAnotherProcessesSyncCommitted(t *testing.T) {
	st, work, remote, warnings := newLandStore(t)
	st.AfterCheckpoint = func(s *Store) error {
		putRecord(t, s, TrackerOwner{Ticket: "JIG-1"}, "acme/tracking", 7)
		runGit(t, work, "add", "-A")
		runGit(t, work, "commit", "-m", leftoversMessage)
		return nil
	}

	st.RunCheckpointHook()

	if len(*warnings) != 0 {
		t.Fatalf("warnings = %v, want none", *warnings)
	}
	if got := runGit(t, remote, "show", "main:trackers/github/acme/tracking/7/record.yaml"); !strings.Contains(got, "issue: 7") {
		t.Errorf("origin's record = %q, want the one the other process's commit holds", got)
	}
	if local, origin := strings.TrimSpace(runGit(t, work, "rev-parse", "HEAD")), strings.TrimSpace(runGit(t, remote, "rev-parse", "main")); local != origin {
		t.Errorf("origin is at %s, want it level with the store at %s", origin, local)
	}
}

// TestCheckpointHookLandingTakesWhatMovedWithAChangeUncommitted: origin moved
// while the sync ran and another process has a tracked file modified in the
// working tree, which the landing leaves where it is. A rebase refuses a dirty
// tree, so a rejected push records that change first, as Sync does, and then
// takes what moved: the store still ends level with origin.
func TestCheckpointHookLandingTakesWhatMovedWithAChangeUncommitted(t *testing.T) {
	st, work, remote, warnings := newLandStore(t)
	base := filepath.Join(work, "base.txt")
	if err := os.WriteFile(base, []byte("one\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := st.Push("JIG-1: base"); err != nil {
		t.Fatal(err)
	}
	other := filepath.Join(t.TempDir(), "other")
	runGit(t, "", "clone", remote, other)
	runGit(t, other, "config", "user.name", "other")
	runGit(t, other, "config", "user.email", "other@example.invalid")

	st.AfterCheckpoint = func(s *Store) error {
		putRecord(t, s, TrackerOwner{Ticket: "JIG-1"}, "acme/tracking", 7)
		if err := os.WriteFile(base, []byte("one\ntwo\n"), 0o644); err != nil {
			return err
		}
		if err := os.WriteFile(filepath.Join(other, "note.txt"), []byte("theirs\n"), 0o644); err != nil {
			return err
		}
		runGit(t, other, "add", "-A")
		runGit(t, other, "commit", "-m", "theirs")
		runGit(t, other, "push", "origin", "main")
		return nil
	}
	st.RunCheckpointHook()

	if len(*warnings) != 0 {
		t.Fatalf("warnings = %v, want none", *warnings)
	}
	if got := porcelain(t, work); got != "" {
		t.Fatalf("working tree still dirty after the landing:\n%s", got)
	}
	files := runGit(t, remote, "ls-tree", "-r", "--name-only", "main")
	for _, want := range []string{"note.txt", "trackers/github/acme/tracking/7/record.yaml"} {
		if !strings.Contains(files, want+"\n") {
			t.Errorf("origin lacks %s:\n%s", want, files)
		}
	}
	if got := runGit(t, remote, "show", "main:base.txt"); got != "one\ntwo\n" {
		t.Errorf("origin's base.txt = %q, want the change recorded before the rebase", got)
	}
	if local, origin := strings.TrimSpace(runGit(t, work, "rev-parse", "HEAD")), strings.TrimSpace(runGit(t, remote, "rev-parse", "main")); local != origin {
		t.Fatalf("origin is at %s, want it level with the store at %s", origin, local)
	}
}

// TestWriteTrackerRecordLeavesAnIdenticalRecordAlone: writing the bytes a
// record already holds changes nothing on disk and notes nothing to land.
func TestWriteTrackerRecordLeavesAnIdenticalRecordAlone(t *testing.T) {
	st, _, _, _ := newLandStore(t)
	e := putRecord(t, st, TrackerOwner{Ticket: "JIG-1"}, "acme/tracking", 7)
	if got := st.takeTrackerWrites(); len(got) != 1 {
		t.Fatalf("first write noted %v, want the record", got)
	}
	before, err := os.Stat(st.TrackerRecordPath(e))
	if err != nil {
		t.Fatal(err)
	}

	putRecord(t, st, TrackerOwner{Ticket: "JIG-1"}, "acme/tracking", 7)

	if got := st.takeTrackerWrites(); len(got) != 0 {
		t.Fatalf("an identical write noted %v, want nothing", got)
	}
	after, err := os.Stat(st.TrackerRecordPath(e))
	if err != nil {
		t.Fatal(err)
	}
	if !after.ModTime().Equal(before.ModTime()) {
		t.Fatal("an identical write replaced the file")
	}
}
