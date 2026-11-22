package spend

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/ledger"
	"github.com/develdeco/jig/internal/locate"
	"github.com/develdeco/jig/internal/observe"
	"github.com/develdeco/jig/internal/project"
	"github.com/develdeco/jig/internal/store"
)

// TestSubagentSpendLineIsWrittenOnItsClaimedTicket: a subagent that claimed a
// slice and worked from the session's directory (on no ticket) has its spend
// line written on the ticket it claimed at its stop, once for each change of
// its total.
func TestSubagentSpendLineIsWrittenOnItsClaimedTicket(t *testing.T) {
	t.Parallel()
	fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatal(err)
	}
	// The worktree is on the ticket's branch; the clone, where the session
	// stands, stays on its default branch.
	wt := filepath.Join(fx.Dir, "wt")
	if _, err := gitx.Run(fx.RepoDir, "worktree", "add", "-q", "-b", "jig/"+fx.Ticket, wt); err != nil {
		t.Fatal(err)
	}
	transcript := filepath.Join(fx.Dir, "s1.jsonl")
	agentTranscript := AgentTranscript(transcript, "a-1")
	writeLines(t, transcript, asst("s1", "main", "claude-sonnet-4-5", 100, 0, 0, 0, 0))
	writeLines(t, agentTranscript, asst("s1", "m1", "claude-sonnet-4-5", 5000, 0, 0, 0, 0))

	now := time.Now()
	d := observe.Deps{
		JigHome: fx.Home,
		Now:     func() time.Time { now = now.Add(time.Second); return now },
		OpenStore: func(w locate.Where) (*store.Store, project.Config, error) {
			cfg, err := project.Load(filepath.Join(w.StorePath, "project.yaml"))
			if err != nil {
				return nil, cfg, err
			}
			s, err := store.Open(w.StorePath)
			return s, cfg, err
		},
		Sources: []observe.NamedSource{{Name: "spend", Fn: Notes}},
	}
	handle := func(e observe.Event) {
		t.Helper()
		e.Harness, e.Session, e.Cwd, e.Transcript = "claude", "s1", fx.RepoDir, transcript
		if _, err := observe.Handle(d, e); err != nil {
			t.Fatalf("Handle(%s): %v", e.Kind, err)
		}
	}
	agentLines := func() []journal.Line {
		t.Helper()
		lines, err := journal.Read(st, fx.Ticket)
		if err != nil {
			t.Fatal(err)
		}
		var out []journal.Line
		for _, l := range lines {
			if l.Event == journal.EventSpend && l.Agent == "a-1" {
				out = append(out, l)
			}
		}
		return out
	}

	if err := journal.Append(st, fx.Ticket, journal.Line{TS: ledger.Stamp(now), Event: journal.EventClaim, Slice: "a", Session: "s1"}); err != nil {
		t.Fatal(err)
	}
	handle(observe.Event{Kind: observe.KindTool, Agent: "a-1", Tool: "Bash", Command: "jig claim " + fx.Ticket + " --slice a"})
	handle(observe.Event{Kind: observe.KindTool, Agent: "a-1", Tool: "Bash", Command: "go test ./..."})

	handle(observe.Event{Kind: observe.KindStop, Agent: "a-1"})
	if got := agentLines(); len(got) != 1 {
		t.Fatalf("the subagent's spend lines on %s = %+v, want one at its stop", fx.Ticket, got)
	}
	handle(observe.Event{Kind: observe.KindStop, Agent: "a-1"})
	if got := agentLines(); len(got) != 1 {
		t.Errorf("spend lines after a second stop with the same total = %d, want still one", len(got))
	}
	writeLines(t, agentTranscript,
		asst("s1", "m1", "claude-sonnet-4-5", 5000, 0, 0, 0, 0),
		asst("s1", "m2", "claude-sonnet-4-5", 7000, 0, 0, 0, 0))
	handle(observe.Event{Kind: observe.KindStop, Agent: "a-1"})
	if got := agentLines(); len(got) != 2 {
		t.Errorf("spend lines after the total changed = %d, want a second", len(got))
	}
}
