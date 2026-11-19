package store

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/axi"
)

// The tests here decide, over a store with no origin or a plain value, rules
// that need no remote: what a landing commits, what a sync's writes leave
// pending, what a guard refuses. A remote is built only where the remote is
// the thing under test.

// newRuleLandStore is a standalone store with the ticket JIG-1 committed,
// ready for a hook that writes its tracker records; each rule is read from
// the store's own HEAD.
func newRuleLandStore(t *testing.T) (st *Store, work string, warnings *[]string) {
	t.Helper()
	st, w := newTreeStore(t)
	if err := st.Push("JIG-1: ticket"); err != nil {
		t.Fatal(err)
	}
	st.takeTrackerWrites()
	return st, st.Root, w
}

// headTree lists the paths HEAD holds under dir.
func headTree(t *testing.T, work, dir string) string {
	t.Helper()
	return runGit(t, work, "ls-tree", "-r", "--name-only", "HEAD", dir)
}

func headLevel(t *testing.T, work string) string {
	t.Helper()
	return strings.TrimSpace(runGit(t, work, "rev-parse", "HEAD"))
}

// TestRuleAtLandingCommitsOnlyTheTrackerPathsAsThePerson: the records a hook
// wrote go in a commit of their own that holds only the tracker paths, under
// the person's identity; another process's uncommitted file stays uncommitted.
func TestRuleAtLandingCommitsOnlyTheTrackerPathsAsThePerson(t *testing.T) {
	t.Parallel()
	st, work, warnings := newRuleLandStore(t)
	entry := TrackerEntry{Provider: ProviderGitHub, Scope: "acme/tracking", ID: "7"}
	st.AfterCheckpoint = func(s *Store) error {
		putRecord(t, s, TrackerOwner{Ticket: "JIG-1"}, "acme/tracking", 7)
		return nil
	}
	journal := filepath.Join(st.TicketDir("JIG-1"), "journal.ndjson")
	if err := os.WriteFile(journal, []byte("{\"event\":\"spend\"}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	st.RunCheckpointHook()

	if len(*warnings) != 0 {
		t.Fatalf("warnings = %v, want none", *warnings)
	}
	rel := filepath.ToSlash(st.TrackerRecordRelPath(entry))
	if got := runGit(t, work, "show", "HEAD:"+rel); !strings.Contains(got, "issue: 7") {
		t.Fatalf("the landing's %s = %q, want the record the hook wrote", rel, got)
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
	if got := porcelain(t, work); !strings.Contains(got, "journal.ndjson") || strings.Count(got, "\n") != 0 {
		t.Fatalf("working tree = %q, want only the other process's journal line left uncommitted", got)
	}
}

// TestRuleAtLandingKeepsWhatAFailedHookWrote: the records a sync wrote before
// it failed are true and land; the failure is still the hook's warning.
func TestRuleAtLandingKeepsWhatAFailedHookWrote(t *testing.T) {
	t.Parallel()
	st, work, warnings := newRuleLandStore(t)
	st.AfterCheckpoint = func(s *Store) error {
		putRecord(t, s, TrackerOwner{Ticket: "JIG-1"}, "acme/tracking", 7)
		return errors.New("github is down")
	}

	st.RunCheckpointHook()

	if len(*warnings) != 1 || !strings.Contains((*warnings)[0], "github is down") {
		t.Fatalf("warnings = %v, want the one naming the hook's cause", *warnings)
	}
	if got := runGit(t, work, "show", "HEAD:trackers/github/acme/tracking/7/record.yaml"); !strings.Contains(got, "issue: 7") {
		t.Fatalf("the landing lacks the record the failed hook wrote: %q", got)
	}
}

// TestRuleAtLandingOfAnIdenticalWriteCommitsNothing: a sync that finds the
// store up to date rewrites no record (one already holding the bytes is left
// alone), so the second checkpoint adds no commit.
func TestRuleAtLandingOfAnIdenticalWriteCommitsNothing(t *testing.T) {
	t.Parallel()
	st, work, warnings := newRuleLandStore(t)
	st.AfterCheckpoint = func(s *Store) error {
		putRecord(t, s, TrackerOwner{Ticket: "JIG-1"}, "acme/tracking", 7)
		return nil
	}
	st.RunCheckpointHook()
	head := headLevel(t, work)

	st.RunCheckpointHook()

	if got := headLevel(t, work); got != head {
		t.Fatalf("HEAD moved from %s to %s, want the second sync to commit nothing", head, got)
	}
	if len(*warnings) != 0 {
		t.Fatalf("warnings = %v, want none", *warnings)
	}
}

// TestRuleAtIdenticalRecordWriteChangesNothing: writing the bytes a record
// already holds changes nothing on disk and notes nothing to land.
func TestRuleAtIdenticalRecordWriteChangesNothing(t *testing.T) {
	t.Parallel()
	st, _, _ := newRuleLandStore(t)
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

// TestRuleAtLandingLeavesAForeignStagedFileStaged: a file another process
// staged is not the landing's to commit; the commit holds only the tracker
// path and the file stays staged.
func TestRuleAtLandingLeavesAForeignStagedFileStaged(t *testing.T) {
	t.Parallel()
	st, work, warnings := newRuleLandStore(t)
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
	tree := runGit(t, work, "ls-tree", "-r", "--name-only", "HEAD")
	if !strings.Contains(tree, "trackers/github/acme/tracking/7/record.yaml\n") || strings.Contains(tree, "foreign.txt") {
		t.Errorf("HEAD's tree = %q, want the record and not foreign.txt", tree)
	}
}

// TestRuleAtLandingTakesEveryKindOfWrite: a removal, an addition, a flat
// record's move into its folder, an old-place record's move and drop, and a
// link made or removed are the sync's writes as much as a record is. None
// reaches the working tree before the landing, and the landing's commit ends
// with the tree the writes describe and a clean working tree.
func TestRuleAtLandingTakesEveryKindOfWrite(t *testing.T) {
	t.Parallel()
	owner := TrackerOwner{Ticket: "JIG-1"}
	e7 := TrackerEntry{Provider: ProviderGitHub, Scope: "acme/tracking", ID: "7"}
	e8 := TrackerEntry{Provider: ProviderGitHub, Scope: "acme/tracking", ID: "8"}
	const rec7 = "trackers/github/acme/tracking/7/record.yaml\n"
	const rec8 = "trackers/github/acme/tracking/8/record.yaml\n"

	cases := []struct {
		name    string
		symlink bool
		// setup leaves the committed starting tree.
		setup func(t *testing.T, st *Store)
		// hook is the sync's writes, run inside the checkpoint hook.
		hook func(t *testing.T, s *Store, work string) error
		// check reads HEAD's trackers tree and the whole tree.
		check func(t *testing.T, trackers, all string)
	}{
		{
			name: "remove a record and add another",
			setup: func(t *testing.T, st *Store) {
				putRecord(t, st, owner, "acme/tracking", 7)
			},
			hook: func(t *testing.T, s *Store, work string) error {
				if err := s.RemoveTrackerRecord(e7); err != nil {
					return err
				}
				cleanDuring(t, work, "after the removal")
				putRecord(t, s, owner, "acme/tracking", 8)
				cleanDuring(t, work, "after the addition")
				return nil
			},
			check: func(t *testing.T, trackers, _ string) {
				if !strings.Contains(trackers, rec8) || strings.Contains(trackers, rec7) {
					t.Errorf("trackers/ after the landing:\n%s", trackers)
				}
			},
		},
		{
			name:    "make a link and remove another",
			symlink: true,
			setup: func(t *testing.T, st *Store) {
				e := putRecord(t, st, owner, "acme/tracking", 7)
				if _, err := st.LinkTrackerEntry(e, owner); err != nil {
					t.Fatal(err)
				}
			},
			hook: func(t *testing.T, s *Store, work string) error {
				if err := s.RemoveTrackerRecord(e7); err != nil {
					return err
				}
				cleanDuring(t, work, "after the removal")
				e2 := putRecord(t, s, owner, "acme/tracking", 8)
				_, err := s.LinkTrackerEntry(e2, owner)
				cleanDuring(t, work, "after the link")
				return err
			},
			check: func(t *testing.T, trackers, _ string) {
				if !strings.Contains(trackers, "trackers/github/acme/tracking/8/JIG-1\n") {
					t.Errorf("trackers/ lacks the made link:\n%s", trackers)
				}
				if strings.Contains(trackers, "trackers/github/acme/tracking/7/") {
					t.Errorf("trackers/ still holds the removed entry:\n%s", trackers)
				}
			},
		},
		{
			name: "move a flat record into its folder",
			setup: func(t *testing.T, st *Store) {
				writeFlat(t, st, e7, owner, flatText("JIG-1", "7"))
			},
			hook: func(t *testing.T, s *Store, work string) error {
				moved, err := s.MoveFlatTrackerRecords()
				cleanDuring(t, work, "after the move")
				if err == nil && len(moved) != 1 {
					t.Errorf("moved %v, want the one flat record", moved)
				}
				return err
			},
			check: func(t *testing.T, trackers, _ string) {
				if !strings.Contains(trackers, rec7) {
					t.Errorf("trackers/ lacks the record in its folder:\n%s", trackers)
				}
				for _, flat := range []string{"trackers/github/acme/tracking/7.yaml\n", "trackers/github/acme/tracking/7\n"} {
					if strings.Contains(trackers, flat) {
						t.Errorf("trackers/ still holds the flat layout's %q:\n%s", strings.TrimSpace(flat), trackers)
					}
				}
			},
		},
		{
			name: "write a record over a flat one",
			setup: func(t *testing.T, st *Store) {
				writeFlat(t, st, e7, owner, flatText("JIG-1", "7"))
			},
			hook: func(t *testing.T, s *Store, work string) error {
				err := s.WriteTrackerRecord(e7, owner, githubFields(t, "acme/tracking", 7), treeHeader)
				cleanDuring(t, work, "after the write")
				return err
			},
			check: func(t *testing.T, trackers, _ string) {
				if !strings.Contains(trackers, rec7) {
					t.Errorf("trackers/ lacks the written record:\n%s", trackers)
				}
			},
		},
		{
			name: "move an old-place record",
			setup: func(t *testing.T, st *Store) {
				commitLegacy(t, st, owner, "8")
			},
			hook: func(t *testing.T, s *Store, work string) error {
				l, _, err := s.LegacyTrackerRecord(owner)
				if err != nil {
					return err
				}
				err = s.MoveLegacyTrackerRecord(l, e8, treeHeader)
				cleanDuring(t, work, "after the move")
				return err
			},
			check: func(t *testing.T, _, all string) {
				if !strings.Contains(all, rec8) || strings.Contains(all, "github.yaml") {
					t.Errorf("tree after the move:\n%s", all)
				}
			},
		},
		{
			name: "drop an old-place record",
			setup: func(t *testing.T, st *Store) {
				commitLegacy(t, st, owner, "8")
			},
			hook: func(t *testing.T, s *Store, work string) error {
				l, _, err := s.LegacyTrackerRecord(owner)
				if err != nil {
					return err
				}
				err = s.DropLegacyTrackerRecord(l)
				cleanDuring(t, work, "after the drop")
				return err
			},
			check: func(t *testing.T, _, all string) {
				if strings.Contains(all, "github.yaml") {
					t.Errorf("tree still holds the old-place record:\n%s", all)
				}
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			st, work, warnings := newRuleLandStore(t)
			if c.symlink {
				skipWithoutSymlinks(t, st)
				runGit(t, work, "config", "core.symlinks", "true")
			}
			c.setup(t, st)
			if err := st.Push("JIG-1: starting tree"); err != nil {
				t.Fatal(err)
			}
			st.takeTrackerWrites()
			st.AfterCheckpoint = func(s *Store) error { return c.hook(t, s, work) }

			st.RunCheckpointHook()

			for _, w := range *warnings {
				// A clone that makes no links says so; that is not a failure.
				if strings.Contains(w, "failed") || strings.Contains(w, "not landed") {
					t.Fatalf("warning %q, want the writes landed", w)
				}
			}
			if got := porcelain(t, work); got != "" {
				t.Fatalf("working tree still dirty after the landing:\n%s", got)
			}
			c.check(t, headTree(t, work, "trackers"), runGit(t, work, "ls-tree", "-r", "--name-only", "HEAD"))
		})
	}
}

// TestRuleAtAClaimInsideTheHookDoesNotLandTheSyncsLaterWrites: a Claim made
// inside the hook commits its own record, and the writes after it are the
// sync's again and stay pending until the landing, so the racing claim of the
// next builder still finds a clean tree.
func TestRuleAtAClaimInsideTheHookDoesNotLandTheSyncsLaterWrites(t *testing.T) {
	t.Parallel()
	st, work, warnings := newRuleLandStore(t)
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
	tree := headTree(t, work, "trackers")
	for _, want := range []string{"trackers/github/acme/tracking/7/record.yaml\n", "trackers/github/acme/tracking/8/record.yaml\n"} {
		if !strings.Contains(tree, want) {
			t.Errorf("HEAD's trackers/ lacks %s:\n%s", strings.TrimSpace(want), tree)
		}
	}
	if got := porcelain(t, work); got != "" {
		t.Errorf("working tree after the landing:\n%s", got)
	}
}

// TestRuleAtARebuiltIndexSeesTheSyncsPendingWrites: the mirror reloads the
// tree at every claim attempt, so the index is rebuilt in the middle of a
// sync, and the rebuild must find the records the sync has written or moved,
// which are not on disk yet.
func TestRuleAtARebuiltIndexSeesTheSyncsPendingWrites(t *testing.T) {
	t.Parallel()
	st, work, warnings := newRuleLandStore(t)
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

// TestRuleAtChartHistoryReadsOneChartsFolder: the commits of one chart's
// folder, its own and no neighbor's (charts/<name>-two/ shares the prefix,
// tickets/ is another folder), newest first, bounded by the limit. A chart no
// commit touched has none, and a store with no commit has no history to read.
func TestRuleAtChartHistoryReadsOneChartsFolder(t *testing.T) {
	t.Parallel()
	st, work := newTestStandaloneStore(t)
	if got, want := st.ChartFile("demo"), filepath.Join(work, "charts", "demo", "tickets.yaml"); got != want {
		t.Errorf("ChartFile = %q, want %q", got, want)
	}
	if got, err := st.ChartHistory("demo", 10); err != nil || len(got) != 0 {
		t.Errorf("ChartHistory before any commit under it = %+v, %v, want none", got, err)
	}

	commit := func(path, subject string) string {
		t.Helper()
		full := filepath.Join(work, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(subject), 0o644); err != nil {
			t.Fatal(err)
		}
		runGit(t, work, "add", path)
		runGit(t, work, "commit", "-m", subject)
		return headLevel(t, work)
	}
	graduate := commit("charts/demo/tickets.yaml", "chart demo: graduate SHOP-1")
	commit("charts/demo-two/tickets.yaml", "chart demo-two: graduate SHOP-9")
	commit("tickets/SHOP-9/ticket.yaml", "SHOP-9: a ticket only")
	edit := commit("charts/demo/map.md", "demo: edit the map")

	got, err := st.ChartHistory("demo", 10)
	if err != nil {
		t.Fatalf("ChartHistory: %v", err)
	}
	if len(got) != 2 || got[0].SHA != edit || got[1].SHA != graduate {
		t.Fatalf("ChartHistory = %+v, want %s then %s: only charts/demo/, newest first", got, edit, graduate)
	}
	if got[0].Subject != "demo: edit the map" || got[1].Subject != "chart demo: graduate SHOP-1" || got[0].Time.IsZero() {
		t.Errorf("ChartHistory = %+v, want the subjects and a time", got)
	}
	if got, err := st.ChartHistory("demo", 1); err != nil || len(got) != 1 || got[0].SHA != edit {
		t.Errorf("ChartHistory with limit 1 = %+v, %v, want only %s", got, err, edit)
	}
	if got, err := st.ChartHistory("no-such-chart", 10); err != nil || len(got) != 0 {
		t.Errorf("ChartHistory of a chart no commit touched = %+v, %v, want none", got, err)
	}

	unborn := t.TempDir()
	runGit(t, unborn, "init", "-b", "main")
	if got, err := (&Store{Root: unborn}).ChartHistory("demo", 10); err == nil {
		t.Errorf("ChartHistory of a store with no commit = %+v, want an error", got)
	}
}

// TestRuleAtDirtyIsAnyUncommittedChange: a clean checkout is not dirty; an
// untracked file and a staged-but-uncommitted change are, and Dirty itself
// stages and commits nothing.
func TestRuleAtDirtyIsAnyUncommittedChange(t *testing.T) {
	t.Parallel()
	st, work := newTestStandaloneStore(t)

	if dirty, err := st.Dirty(); err != nil || dirty {
		t.Fatalf("Dirty = %v, %v, want false right after a clean checkout", dirty, err)
	}
	if err := os.MkdirAll(filepath.Join(work, "ticket-a"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "ticket-a", "ticket.md"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if dirty, err := st.Dirty(); err != nil || !dirty {
		t.Fatalf("Dirty = %v, %v, want true with an untracked ticket folder present", dirty, err)
	}
	if staged, err := st.hasStagedChanges(); err != nil || staged {
		t.Fatalf("hasStagedChanges = %v, %v after Dirty, want false: Dirty stages nothing", staged, err)
	}
	runGit(t, work, "add", "-A")
	if staged, err := st.hasStagedChanges(); err != nil || !staged {
		t.Fatal("test setup did not stage the untracked file")
	}
	if dirty, err := st.Dirty(); err != nil || !dirty {
		t.Fatalf("Dirty = %v, %v, want true with a staged-but-uncommitted change", dirty, err)
	}
}

// TestRuleAtSyncAndPushRefuseAnUnfinishedStateAndNameIt: whatever unfinished
// git state the store was left in (a conflicted or resolved cherry-pick or
// revert, a conflicted stash pop, an apply-backend rebase), Sync and Push
// refuse with STORE_CONFLICT before anything else, commit nothing, leave the
// state for the operator, and name what they found in the message.
func TestRuleAtSyncAndPushRefuseAnUnfinishedStateAndNameIt(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		leave func(t *testing.T, work string)
		want  string
	}{
		{"ConflictedStashPop", leaveConflictedStashPop, "unresolved (unmerged) index entries"},
		{"ConflictedCherryPick", leaveConflictedCherryPick, "an unfinished cherry-pick"},
		{"ResolvedCherryPick", leaveResolvedCherryPick, "an unfinished cherry-pick"},
		{"ResolvedRevert", leaveResolvedRevert, "an unfinished revert"},
		{"RebaseApply", leaveRebaseApplyInProgress, "an unfinished rebase"},
	}
	for _, c := range cases {
		for _, which := range []string{"Sync", "Push"} {
			t.Run(c.name+"/"+which, func(t *testing.T) {
				t.Parallel()
				st, work := newTestStandaloneStore(t)
				c.leave(t, work)
				before := runGit(t, work, "rev-list", "--count", "HEAD")

				var err error
				if which == "Sync" {
					err = st.Sync()
				} else {
					err = st.Push("should be refused")
				}

				var ae *axi.Error
				if !errors.As(err, &ae) || ae.Code != "STORE_CONFLICT" {
					t.Fatalf("%s: err = %v, want *axi.Error STORE_CONFLICT", which, err)
				}
				if !strings.Contains(ae.Msg, c.want) {
					t.Fatalf("%s refusal Msg = %q, want it to name %q", which, ae.Msg, c.want)
				}
				if strings.Contains(ae.Msg, "unfinished rebase or merge, or unresolved conflicts") {
					t.Fatalf("%s refusal Msg = %q, still uses the old generic wording", which, ae.Msg)
				}
				if after := runGit(t, work, "rev-list", "--count", "HEAD"); after != before {
					t.Fatalf("%s committed over the unfinished state: HEAD count %q -> %q", which, before, after)
				}
				if still, ierr := inProgressRebaseOrMerge(work); ierr != nil || still == "" {
					t.Fatalf("%s: the fixture's broken state is gone after the call (%q, %v)", which, still, ierr)
				}
			})
		}
	}
}

// TestRuleAtSyncAndPushRefuseADetachedHEAD: mid-bisect (or any detached
// checkout) HEAD is no branch, so Sync and Push refuse with STORE_CONFLICT
// before touching anything.
func TestRuleAtSyncAndPushRefuseADetachedHEAD(t *testing.T) {
	t.Parallel()
	for _, which := range []string{"Sync", "Push"} {
		t.Run(which, func(t *testing.T) {
			t.Parallel()
			st, work := newTestStandaloneStore(t)
			runGit(t, work, "checkout", headLevel(t, work))
			if err := os.WriteFile(filepath.Join(work, "uncommitted.txt"), []byte("x\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			before := runGit(t, work, "rev-list", "--count", "HEAD")

			var err error
			if which == "Sync" {
				err = st.Sync()
			} else {
				err = st.Push("should be refused")
			}

			var ae *axi.Error
			if !errors.As(err, &ae) || ae.Code != "STORE_CONFLICT" {
				t.Fatalf("%s with a detached HEAD: err = %v, want *axi.Error STORE_CONFLICT", which, err)
			}
			if after := runGit(t, work, "rev-list", "--count", "HEAD"); after != before {
				t.Fatalf("%s committed on a detached HEAD: HEAD count %q -> %q", which, before, after)
			}
		})
	}
}
