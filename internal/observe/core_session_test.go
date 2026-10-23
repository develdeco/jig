package observe

import (
	"os"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/home"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/ledger"
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

func TestToolRecordsTheCallWithoutVerifyingWhenHeadStays(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.onTicketBranch()
	r.handle(r.ev(KindSessionStart))
	res := r.handle(r.ev(KindTool, func(e *Event) {
		e.Tool, e.Command, e.Exit, e.Files = "Bash", "jig  review\tcontext JIG-1", intp(0), []string{"a.go"}
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
		l.Exit == nil || *l.Exit != 0 || len(l.Files) != 1 || l.Commit != head || l.Ticket != r.fx.Ticket || l.Branch != "jig/"+r.fx.Ticket {
		t.Errorf("tool line = %+v", l)
	}
	if len(r.started()) != 0 {
		t.Errorf("started %v, want no verification: HEAD did not move", r.started())
	}
}

func TestToolVerifiesACommitThatMovedHeadOnce(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.onTicketBranch()
	r.handle(r.ev(KindSessionStart))
	sha := r.commit("a", "a.txt")

	bash := func() { r.handle(r.ev(KindTool, func(e *Event) { e.Tool, e.Command = "Bash", "git commit -m work" })) }
	bash()
	starts := r.started()
	if len(starts) != 1 || starts[0].Path != r.d.Executable || starts[0].Dir != r.fx.RepoDir {
		t.Fatalf("started %+v, want one worker of the jig binary in the clone", starts)
	}
	args := strings.Join(starts[0].Args, " ")
	for _, want := range []string{"verify --worker", "--slice a", "--session s1", r.fx.Ticket, sha} {
		if !strings.Contains(args, want) {
			t.Errorf("worker args %q lack %q", args, want)
		}
	}
	if spawned := r.ledgerOf(journal.EventVerifySpawned); len(spawned) != 1 || spawned[0].Commit != sha || spawned[0].Slice != "a" {
		t.Errorf("verify-spawned lines = %+v, want one for %s slice a", spawned, sha)
	}
	// The next call sees HEAD where the last one left it.
	bash()
	if len(r.started()) != 1 {
		t.Errorf("a second tool call at the same HEAD started %d workers, want still 1", len(r.started()))
	}
}

func TestToolDoesNotVerifyWhatGitsHookAlreadyStarted(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.onTicketBranch()
	r.handle(r.ev(KindSessionStart))
	r.commit("a", "a.txt")
	// Git's post-commit hook runs first, as it does inside git commit.
	r.handle(Event{Harness: "git", Kind: KindPostCommit, Cwd: r.fx.RepoDir})
	if len(r.started()) != 1 {
		t.Fatalf("post-commit started %d workers, want 1", len(r.started()))
	}
	r.handle(r.ev(KindTool, func(e *Event) { e.Tool, e.Command = "Bash", "git commit -m work" }))
	if got := len(r.started()); got != 1 {
		t.Errorf("the tool call that made the commit started another worker (%d in all), want 1", got)
	}
	if got := len(r.ledgerOf(journal.EventVerifySpawned)); got != 1 {
		t.Errorf("verify-spawned lines = %d, want 1", got)
	}
}

func TestToolWithNoEarlierLineOnlyStartsWatchingHead(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.onTicketBranch()
	r.commit("a", "a.txt")
	run := func() {
		r.handle(r.ev(KindTool, func(e *Event) { e.Tool, e.Command = "Bash", "ls" }))
	}
	run() // hooks installed mid-session: nothing to compare with yet
	if len(r.started()) != 0 {
		t.Fatalf("the first call started %v, want none", r.started())
	}
	r.commit("b", "b.txt")
	run()
	if got := r.started(); len(got) != 1 || !strings.Contains(strings.Join(got[0].Args, " "), "--slice b") {
		t.Errorf("started %+v, want one worker for slice b", got)
	}
}
