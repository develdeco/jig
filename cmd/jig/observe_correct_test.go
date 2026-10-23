package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/home"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/locate"
	"github.com/develdeco/jig/internal/observe"
	"github.com/develdeco/jig/internal/store"
)

// correctionCtx is what the observe core hands the correction source for a
// tool call on the fixture ticket: two failures of slice a with one
// signature, made since the session began.
func correctionCtx(t *testing.T, kind string) (observe.NoteCtx, *fixture.Fixture) {
	t.Helper()
	fx := newFixture(t, fixture.Opts{})
	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatal(err)
	}
	for attempt := 1; attempt <= 2; attempt++ {
		l := journal.Line{Slice: "a", Event: journal.EventOracle, Outcome: "fail", Attempt: attempt, Signature: "a|code-bug|boom", Summary: "boom", Commit: "beef00beef00beef00beef00beef00beef00beef"}
		if err := journal.Append(st, fx.Ticket, l); err != nil {
			t.Fatal(err)
		}
	}
	return observe.NoteCtx{
		Event:   observe.Event{Kind: kind, Session: "s1", Harness: "claude"},
		Where:   locate.Where{Ticket: fx.Ticket, Cwd: fx.RepoDir, RepoDir: fx.RepoDir},
		Store:   st,
		JigHome: fx.Home,
		Now:     time.Now(),
		Since:   time.Now().Add(-time.Minute),
		Journal: func() ([]journal.Line, error) { return journal.Read(st, fx.Ticket) },
	}, fx
}

func TestCorrectionSourceIsRegisteredAndEveryNoteIsEnforced(t *testing.T) {
	t.Parallel()
	registered := false
	for _, s := range observe.NoteSources() {
		registered = registered || s.Name == "correction"
	}
	if !registered {
		t.Fatalf("note sources = %+v, want correction among them", observe.NoteSources())
	}
	c, _ := correctionCtx(t, observe.KindTool)
	notes, err := correctionNotes(c)
	if err != nil || len(notes) != 1 || !notes[0].Enforce || !strings.HasPrefix(notes[0].Ref, "stall:a:") || !strings.Contains(notes[0].Text, "the same way 2 times") {
		t.Fatalf("correctionNotes = %+v, %v, want the one stall, enforced", notes, err)
	}
}

func TestCorrectionSourceSpeaksAtToolPromptAndStopOnly(t *testing.T) {
	t.Parallel()
	c, _ := correctionCtx(t, observe.KindTool)
	for kind, speaks := range map[string]bool{
		observe.KindTool: true, observe.KindPrompt: true, observe.KindStop: true,
		observe.KindSessionEnd: false, observe.KindSessionStart: false, observe.KindPostCommit: false,
	} {
		c.Event.Kind = kind
		notes, err := correctionNotes(c)
		if err != nil || (len(notes) > 0) != speaks {
			t.Errorf("a %s event: %+v, %v, want notes: %v", kind, notes, err, speaks)
		}
	}
	// Without a store or a ticket there is nothing to look at.
	c.Event.Kind = observe.KindTool
	c.Store = nil
	if notes, err := correctionNotes(c); err != nil || notes != nil {
		t.Errorf("no store: %+v, %v, want nothing", notes, err)
	}
	c.Where.Ticket = ""
	if notes, err := correctionNotes(c); err != nil || notes != nil {
		t.Errorf("no ticket: %+v, %v, want nothing", notes, err)
	}
}

// TestCorrectionSourceKeepsItsNotesWhenADetectorFails: the observe core drops
// every note of a source that returns an error, so one that fails on the
// manifest of the repo (the scope check) must not cost the stall; what failed
// goes to the observe log.
func TestCorrectionSourceKeepsItsNotesWhenADetectorFails(t *testing.T) {
	t.Parallel()
	c, fx := correctionCtx(t, observe.KindTool)
	if err := os.WriteFile(filepath.Join(fx.RepoDir, ".claude", "jig.yaml"), []byte("workspaces: [not, a, list of maps\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	for _, a := range [][]string{{"add", "--", ".claude/jig.yaml"}, {"commit", "-q", "-m", "break the manifest"}} {
		if _, err := gitx.Run(fx.RepoDir, a...); err != nil {
			t.Fatal(err)
		}
	}
	sha, err := gitx.RevParse(fx.RepoDir, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	c.Commit = sha

	notes, err := correctionNotes(c)
	if err != nil || len(notes) != 1 || !strings.HasPrefix(notes[0].Ref, "stall:a:") {
		t.Fatalf("correctionNotes = %+v, %v, want the stall and no error", notes, err)
	}
	log, rerr := os.ReadFile(filepath.Join(home.LogsDir(fx.Home), "observe.log"))
	if rerr != nil || !strings.Contains(string(log), "note source correction") || !strings.Contains(string(log), "jig.yaml") {
		t.Errorf("observe.log = %q, %v, want a line naming the source and the manifest", log, rerr)
	}
}
