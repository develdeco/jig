package observe

import (
	"os"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/home"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/ledger"
	"github.com/develdeco/jig/internal/locate"
)

func TestSessionStartRecordsTheSessionAndGivesTheContext(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.onTicketBranch()
	head, _ := gitx.RevParse(r.fx.RepoDir, "HEAD")

	res := r.handle(r.ev(KindSessionStart, func(e *Event) { e.Model = "m1" }))
	if !strings.Contains(res.Context, "ticket: "+r.fx.Ticket) || res.Block != "" || res.Message != "" {
		t.Fatalf("Result = %+v, want the context of the located ticket", res)
	}
	starts := r.ledgerOf(journal.EventSessionStart)
	if len(starts) != 1 {
		t.Fatalf("ledger session-start lines = %+v, want one", starts)
	}
	l := starts[0]
	if l.Session != "s1" || l.Harness != "claude" || l.Ticket != r.fx.Ticket || l.Model != "m1" || l.Commit != head {
		t.Errorf("session-start = %+v, want session s1 of claude on %s at %s", l, r.fx.Ticket, head)
	}
	got, err := ledger.Sessions(r.fx.Home)
	if err != nil || got["s1"].Ticket != r.fx.Ticket || got["s1"].Clone == "" || !got["s1"].Live() {
		t.Errorf("Sessions = %+v, %v, want s1 live on %s with its clone", got["s1"], err, r.fx.Ticket)
	}
	// Every event of a harness leaves a hook or a tool line carrying its name.
	if hooks := r.hooksOf(KindSessionStart); len(hooks) != 1 || hooks[0].Harness != "claude" || hooks[0].Session != "s1" {
		t.Errorf("hook lines of the start = %+v, want one from claude", hooks)
	}
}

func TestSessionStartHandsTheSessionToItsCommands(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.onTicketBranch()
	const ask = "jig knows this session as s1"
	var got []map[string]string
	d := r.d
	d.SetEnv = func(vars map[string]string) (bool, error) { got = append(got, vars); return true, nil }

	// The harness sets JIG_SESSION, so the context does not ask for it.
	res, err := Handle(d, r.ev(KindSessionStart))
	if err != nil || len(got) != 1 || len(got[0]) != 1 || got[0][locate.SessionEnv] != "s1" {
		t.Fatalf("SetEnv calls = %v (%v), want one with %s=s1", got, err, locate.SessionEnv)
	}
	if strings.Contains(res.Context, ask) || !strings.Contains(res.Context, "ticket: "+r.fx.Ticket) {
		t.Errorf("context = %q, want the ticket and no request for the session", res.Context)
	}

	// A harness that cannot set it, or has no way to: the context asks.
	d.SetEnv = func(map[string]string) (bool, error) { return false, nil }
	if res := r.handle(r.ev(KindSessionStart)); !strings.Contains(res.Context, ask) {
		t.Errorf("context with no SetEnv = %q, want it to ask for the session", res.Context)
	}
	if res, _ := Handle(d, r.ev(KindSessionStart)); !strings.Contains(res.Context, ask) {
		t.Errorf("context when SetEnv sets nothing = %q, want it to ask for the session", res.Context)
	}

	// Outside every project the variable is still set, for a project the
	// session works in later, and nothing else happens.
	got = nil
	d.SetEnv = func(vars map[string]string) (bool, error) { got = append(got, vars); return true, nil }
	before := len(r.ledger())
	res, err = Handle(d, r.ev(KindSessionStart, func(e *Event) { e.Cwd = t.TempDir() }))
	if err != nil || !res.Empty() || len(got) != 1 || got[0][locate.SessionEnv] != "s1" || len(r.ledger()) != before {
		t.Errorf("start outside a project = %+v, %v, SetEnv %v, ledger %d lines after %d, want only the variable set", res, err, got, len(r.ledger()), before)
	}
}

func TestEventsOutsideAProjectAreSilentAndWriteNothing(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	elsewhere := t.TempDir()
	for _, kind := range []string{KindSessionStart, KindPrompt, KindTool, KindStop, KindSessionEnd} {
		res, err := Handle(r.d, r.ev(kind, func(e *Event) { e.Cwd, e.Command = elsewhere, "ls" }))
		if err != nil || !res.Empty() {
			t.Errorf("%s outside a project = %+v, %v, want nothing", kind, res, err)
		}
	}
	if _, err := os.Stat(home.LedgerPath(r.fx.Home)); !os.IsNotExist(err) {
		t.Errorf("the ledger exists after events outside a project (stat: %v)", err)
	}
	if len(r.started()) != 0 {
		t.Errorf("started %v", r.started())
	}
}

func TestToolStartsNothingForACommitThatMovedHead(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.onTicketBranch()
	r.handle(r.ev(KindSessionStart))
	r.commit("a", "a.txt")
	r.handle(r.ev(KindTool, func(e *Event) { e.Tool, e.Command = "Bash", "git commit -m work" }))
	if got := r.started(); len(got) != 0 {
		t.Errorf("the tool call that made a commit left %+v, want nothing started", got)
	}
	if got := len(r.ledgerOf(journal.EventTool)); got != 1 {
		t.Errorf("tool lines = %d, want the call recorded", got)
	}
}

func TestToolRecordsTheCallWithoutVerifyingWhenHeadStays(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.onTicketBranch()
	r.handle(r.ev(KindSessionStart))
	res := r.handle(r.ev(KindTool, func(e *Event) {
		e.Tool, e.Command, e.Exit, e.Files = "Bash", "jig  review\tcontext JIG-1", intp(0), []string{"a.go"}
		e.Description = "Open the review\nround"
	}))
	if !res.Empty() {
		t.Errorf("Result = %+v, want nothing to say", res)
	}
	tools := r.ledgerOf(journal.EventTool)
	if len(tools) != 1 {
		t.Fatalf("tool lines = %+v, want one", tools)
	}
	l := tools[0]
	head, _ := gitx.RevParse(r.fx.RepoDir, "HEAD")
	if l.Session != "s1" || l.Harness != "claude" || l.Tool != "Bash" || l.Command != "jig review context JIG-1" ||
		l.Description != "Open the review round" || l.Exit == nil || *l.Exit != 0 || len(l.Files) != 1 || l.Commit != head || l.Ticket != r.fx.Ticket || l.Branch != "jig/"+r.fx.Ticket {
		t.Errorf("tool line = %+v", l)
	}
	if len(r.started()) != 0 {
		t.Errorf("started %v, want no verification: HEAD did not move", r.started())
	}
}
