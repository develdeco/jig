package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// cleanDuring fails the test when the store's working tree holds anything
// uncommitted: called from inside a hook, it is what a claim on the same clone
// would meet.
func cleanDuring(t *testing.T, work, when string) {
	t.Helper()
	if got := porcelain(t, work); got != "" {
		t.Errorf("%s: the working tree holds the sync's writes before the landing:\n%s", when, got)
	}
}

// commitLegacy leaves a record in its ticket's own folder, the old place, and
// commits it.
func commitLegacy(t *testing.T, st *Store, o TrackerOwner, issue string) {
	t.Helper()
	abs := filepath.Join(st.Root, st.legacyRecordRel(o))
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(abs, []byte("repo: acme/tracking\nissue: "+issue+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := st.Push("JIG-1: old place"); err != nil {
		t.Fatal(err)
	}
	st.takeTrackerWrites()
}

// TestWritesAfterAClaimInsideTheHookStayPending: a Claim made inside the hook
// writes its record straight to the working tree, as it commits it itself; the
// writes after it are the sync's again and stay pending, so the racing claim
// of the next builder still finds a clean tree.
func TestWritesAfterAClaimInsideTheHookStayPending(t *testing.T) {
	st, work, remote, warnings := newLandStore(t)
	owner := TrackerOwner{Ticket: "JIG-1"}
	first := TrackerEntry{Provider: ProviderGitHub, Scope: "acme/tracking", ID: "7"}
	second := TrackerEntry{Provider: ProviderGitHub, Scope: "acme/tracking", ID: "8"}
	st.AfterCheckpoint = func(s *Store) error {
		if _, err := s.Claim(
			func() (string, []string, error) {
				if err := s.WriteTrackerRecord(first, owner, githubFields(t, "acme/tracking", 7), treeHeader); err != nil {
					return "", nil, err
				}
				return "JIG-1", []string{s.TrackerRecordRelPath(first)}, nil
			},
			func(id string) string { return id + ": issue" },
		); err != nil {
			return err
		}
		cleanDuring(t, work, "after the claim")
		if err := s.WriteTrackerRecord(second, owner, githubFields(t, "acme/tracking", 8), treeHeader); err != nil {
			return err
		}
		cleanDuring(t, work, "after a write following the claim")
		return nil
	}
	st.RunCheckpointHook()

	if len(*warnings) != 0 {
		t.Fatalf("warnings = %v, want none", *warnings)
	}
	tree := runGit(t, remote, "ls-tree", "-r", "--name-only", "main", "trackers")
	for _, want := range []string{"trackers/github/acme/tracking/7/record.yaml\n", "trackers/github/acme/tracking/8/record.yaml\n"} {
		if !strings.Contains(tree, want) {
			t.Errorf("origin's trackers/ lacks %s:\n%s", strings.TrimSpace(want), tree)
		}
	}
	if got := porcelain(t, work); got != "" {
		t.Errorf("working tree after the landing:\n%s", got)
	}
}

// TestRebuiltIndexSeesTheSyncsPendingWrites: the mirror reloads the tree at
// every claim attempt, so the index is rebuilt in the middle of a sync, and
// the rebuild must find the records the sync has written or moved, which are
// not on disk yet.
func TestRebuiltIndexSeesTheSyncsPendingWrites(t *testing.T) {
	st, work, _, warnings := newLandStore(t)
	owner := TrackerOwner{Ticket: "JIG-1"}
	commitLegacy(t, st, owner, "8")
	moved := TrackerEntry{Provider: ProviderGitHub, Scope: "acme/tracking", ID: "8"}
	written := TrackerEntry{Provider: ProviderGitHub, Scope: "acme/tracking", ID: "9"}
	st.AfterCheckpoint = func(s *Store) error {
		l, ok, err := s.LegacyTrackerRecord(owner)
		if err != nil || !ok {
			t.Fatalf("legacy record: %v, %v", ok, err)
		}
		if err := s.MoveLegacyTrackerRecord(l, moved, treeHeader); err != nil {
			return err
		}
		putRecord(t, s, owner, "acme/tracking", 9)
		if _, err := os.Stat(s.TrackerRecordPath(moved)); err == nil {
			t.Error("the moved record is on disk before the landing")
		}
		s.ReloadTrackerTree()
		recs, err := s.TrackerRecordsFor(owner)
		if err != nil {
			return err
		}
		got := map[TrackerEntry]string{}
		for _, r := range recs {
			if r.Err != nil {
				t.Errorf("record %s after the reload: %v", r.Rel, r.Err)
				continue
			}
			for i := 0; i+1 < len(r.Fields.Content); i += 2 {
				if r.Fields.Content[i].Value == "issue" {
					got[r.Entry] = r.Fields.Content[i+1].Value
				}
			}
		}
		if got[moved] != "8" || got[written] != "9" || len(got) != 2 {
			t.Errorf("records after the reload = %v, want %v and %v with their fields", got, moved, written)
		}
		cleanDuring(t, work, "after the reload")
		return nil
	}
	st.RunCheckpointHook()

	if len(*warnings) != 0 {
		t.Fatalf("warnings = %v, want none", *warnings)
	}
}

// TestEveryTrackerWriterKeepsItsChangeOutOfTheWorkingTreeWhileASyncHoldsIt: a
// removal, an old-place record's move and removal, a flat record's move and a
// link are writes of the sync as much as a record is, and none reaches the
// working tree before the landing.
func TestEveryTrackerWriterKeepsItsChangeOutOfTheWorkingTreeWhileASyncHoldsIt(t *testing.T) {
	owner := TrackerOwner{Ticket: "JIG-1"}
	e7 := TrackerEntry{Provider: ProviderGitHub, Scope: "acme/tracking", ID: "7"}
	e8 := TrackerEntry{Provider: ProviderGitHub, Scope: "acme/tracking", ID: "8"}

	t.Run("remove a record and its link", func(t *testing.T) {
		st, work, remote, warnings := newLandStore(t)
		skipWithoutSymlinks(t, st)
		runGit(t, work, "config", "core.symlinks", "true")
		putRecord(t, st, owner, "acme/tracking", 7)
		if _, err := st.LinkTrackerEntry(e7, owner); err != nil {
			t.Fatal(err)
		}
		if err := st.Push("JIG-1: record"); err != nil {
			t.Fatal(err)
		}
		st.takeTrackerWrites()
		st.AfterCheckpoint = func(s *Store) error {
			err := s.RemoveTrackerRecord(e7)
			cleanDuring(t, work, "after the removal")
			return err
		}
		st.RunCheckpointHook()
		if len(*warnings) != 0 {
			t.Fatalf("warnings = %v, want none", *warnings)
		}
		if tree := runGit(t, remote, "ls-tree", "-r", "--name-only", "main", "trackers"); strings.TrimSpace(tree) != "" {
			t.Errorf("origin's trackers/ after the removal:\n%s", tree)
		}
	})

	t.Run("move a flat record into its folder", func(t *testing.T) {
		st, work, remote, warnings := newLandStore(t)
		writeFlat(t, st, e7, owner, flatText("JIG-1", "7"))
		if err := st.Push("JIG-1: flat layout"); err != nil {
			t.Fatal(err)
		}
		st.takeTrackerWrites()
		st.AfterCheckpoint = func(s *Store) error {
			_, err := s.MoveFlatTrackerRecords()
			cleanDuring(t, work, "after the move")
			return err
		}
		st.RunCheckpointHook()
		for _, w := range *warnings {
			// A clone that makes no links says so; that is not a failure.
			if strings.Contains(w, "failed") || strings.Contains(w, "not landed") {
				t.Fatalf("warning %q, want the move landed", w)
			}
		}
		if tree := runGit(t, remote, "ls-tree", "-r", "--name-only", "main", "trackers"); !strings.Contains(tree, "7/record.yaml\n") {
			t.Errorf("origin's trackers/ lacks the moved record:\n%s", tree)
		}
	})

	t.Run("write a record over a flat one", func(t *testing.T) {
		st, work, _, warnings := newLandStore(t)
		writeFlat(t, st, e7, owner, flatText("JIG-1", "7"))
		if err := st.Push("JIG-1: flat layout"); err != nil {
			t.Fatal(err)
		}
		st.takeTrackerWrites()
		st.AfterCheckpoint = func(s *Store) error {
			err := s.WriteTrackerRecord(e7, owner, githubFields(t, "acme/tracking", 7), treeHeader)
			cleanDuring(t, work, "after the write")
			return err
		}
		st.RunCheckpointHook()
		if len(*warnings) != 0 {
			t.Fatalf("warnings = %v, want none", *warnings)
		}
	})

	t.Run("move an old-place record", func(t *testing.T) {
		st, work, remote, warnings := newLandStore(t)
		commitLegacy(t, st, owner, "8")
		st.AfterCheckpoint = func(s *Store) error {
			l, _, err := s.LegacyTrackerRecord(owner)
			if err != nil {
				return err
			}
			err = s.MoveLegacyTrackerRecord(l, e8, treeHeader)
			cleanDuring(t, work, "after the move")
			return err
		}
		st.RunCheckpointHook()
		if len(*warnings) != 0 {
			t.Fatalf("warnings = %v, want none", *warnings)
		}
		tree := runGit(t, remote, "ls-tree", "-r", "--name-only", "main")
		if !strings.Contains(tree, "trackers/github/acme/tracking/8/record.yaml\n") || strings.Contains(tree, "github.yaml") {
			t.Errorf("origin's tree after the move:\n%s", tree)
		}
	})

	t.Run("drop an old-place record", func(t *testing.T) {
		st, work, remote, warnings := newLandStore(t)
		commitLegacy(t, st, owner, "8")
		st.AfterCheckpoint = func(s *Store) error {
			l, _, err := s.LegacyTrackerRecord(owner)
			if err != nil {
				return err
			}
			err = s.DropLegacyTrackerRecord(l)
			cleanDuring(t, work, "after the drop")
			return err
		}
		st.RunCheckpointHook()
		if len(*warnings) != 0 {
			t.Fatalf("warnings = %v, want none", *warnings)
		}
		if tree := runGit(t, remote, "ls-tree", "-r", "--name-only", "main"); strings.Contains(tree, "github.yaml") {
			t.Errorf("origin still holds the old-place record:\n%s", tree)
		}
	})

	t.Run("make a link", func(t *testing.T) {
		st, work, remote, warnings := newLandStore(t)
		skipWithoutSymlinks(t, st)
		runGit(t, work, "config", "core.symlinks", "true")
		st.AfterCheckpoint = func(s *Store) error {
			putRecord(t, s, owner, "acme/tracking", 7)
			_, err := s.LinkTrackerEntry(e7, owner)
			cleanDuring(t, work, "after the link")
			return err
		}
		st.RunCheckpointHook()
		if len(*warnings) != 0 {
			t.Fatalf("warnings = %v, want none", *warnings)
		}
		want, err := st.linkTarget(e7, owner)
		if err != nil {
			t.Fatal(err)
		}
		link := filepath.ToSlash(st.TrackerLinkRelPath(e7, owner))
		if got := strings.TrimSpace(runGit(t, remote, "show", "main:"+link)); got != filepath.ToSlash(want) {
			t.Errorf("origin's link %s points at %q, want %q", link, got, filepath.ToSlash(want))
		}
		if target, err := os.Readlink(st.TrackerLinkPath(e7, owner)); err != nil || !sameTarget(st.TrackerLinkPath(e7, owner), target, want) {
			t.Errorf("the link on disk = %q (%v), want %q", target, err, want)
		}
	})
}
