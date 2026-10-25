package e2e

// End-to-end tests of what `jig tui` shows of a ticket's phases: that it puts
// the ticket where `jig next` says it stands, and that it waits for a ticket
// that does not exist yet.

import (
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/events"
	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/store"
	"github.com/develdeco/jig/internal/tui"
)

// TestTUIAndNextAgree: a ticket goes from a drafted brief to a published pull
// request, and at each state the screens put it where `jig next` does: the same
// current phase, the same answer to whether that phase can start, the same
// state for each of the four phases. The screens read `jig status` and the
// journal, as `jig tui` does; `jig next` reads the store.
func TestTUIAndNextAgree(t *testing.T) {
	t.Parallel()
	fx, env := newFixture(t, fixture.Opts{})
	T := fx.Ticket

	agree := func(when string) {
		t.Helper()
		next := nextJSON[phaseOut](t, env, fx.RepoDir, T)
		status := runJig(t, env, fx.RepoDir, "status", T)
		if status.Code != 0 {
			t.Fatalf("%s: jig status exit = %d\nstdout:\n%s\nstderr:\n%s", when, status.Code, status.Stdout, status.Stderr)
		}
		var lines []events.Envelope
		for _, l := range readJournal(t, fx, T) {
			lines = append(lines, events.Envelope{Src: events.SrcJournal, Store: "demo", Line: l})
		}
		got := tui.StandingOf(T, status.Stdout, lines)
		p := next.Phase
		if got.Current != p.Name || got.Ready != p.Ready {
			t.Errorf("%s: the screens stand at %s (ready %v), jig next at %s (ready %v: %s)", when, got.Current, got.Ready, p.Name, p.Ready, p.Reason)
		}
		for i, ph := range p.Phases {
			if got.States[i] != ph.State {
				t.Errorf("%s: the screens have %s %s, jig next %s", when, ph.Name, got.States[i], ph.State)
			}
		}
	}

	// A brief nobody approved, then an approved one.
	agree("a drafted brief")
	if r := runJig(t, env, fx.RepoDir, "validate", T); r.Code != 0 {
		t.Fatalf("jig validate exit = %d\nstdout:\n%s\nstderr:\n%s", r.Code, r.Stdout, r.Stderr)
	}
	agree("an approved brief")

	// A slice is claimed, then every slice is green.
	if r := runJig(t, env.with("JIG_SESSION=s-claim"), fx.RepoDir, "claim", T, "--slice", "a"); r.Code != 0 {
		t.Fatalf("jig claim exit = %d\nstdout:\n%s\nstderr:\n%s", r.Code, r.Stdout, r.Stderr)
	}
	agree("a build under way")
	b := buildTicketOn(t, fx, env, true)
	agree("a build done")

	// Round 1 is open, and then closed with one ask for a person.
	b.openRound(t)
	agree("a round open")
	reviewer := newHarness(t, fx, env, "s-reviewer")
	if res := reviewer.Jig("review", "context", T); res.Code != 0 {
		t.Fatalf("jig review context exit = %d\nstdout:\n%s\nstderr:\n%s", res.Code, res.Stdout, res.Stderr)
	}
	reviewer.Bash("git log --oneline -3")
	if res := reviewer.Jig("review", "done", T, "--result", reviewResult(t, b.st, T, 1, true)); !strings.Contains(res.Stdout, "round: 1") {
		t.Fatalf("jig review done (round 1) exit = %d\nstdout:\n%s\nstderr:\n%s", res.Code, res.Stdout, res.Stderr)
	}
	agree("a round with an ask to decide")

	// The ask is dismissed: the next round is ready to open.
	if r := runJig(t, env, fx.RepoDir, "triage", T, "r1-f1", "dismiss"); r.Code != 0 {
		t.Fatalf("jig triage exit = %d\nstdout:\n%s\nstderr:\n%s", r.Code, r.Stdout, r.Stderr)
	}
	agree("an ask decided")

	// Round 2 is clean.
	if res := b.builder.Jig("gate", T); res.Code != 0 {
		t.Fatalf("jig gate (round 2) exit = %d\nstdout:\n%s\nstderr:\n%s", res.Code, res.Stdout, res.Stderr)
	}
	agree("round 2 open")
	if res := reviewer.Jig("review", "context", T); res.Code != 0 {
		t.Fatalf("jig review context (round 2) exit = %d\nstdout:\n%s\nstderr:\n%s", res.Code, res.Stdout, res.Stderr)
	}
	if res := reviewer.Jig("review", "done", T, "--result", reviewResult(t, b.st, T, 2, false)); res.Code != 0 || !strings.Contains(res.Stdout, "verdict: clean") {
		t.Fatalf("jig review done (round 2) exit = %d\nstdout:\n%s\nstderr:\n%s", res.Code, res.Stdout, res.Stderr)
	}
	agree("a clean round")

	// The ticket is published.
	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := journal.Append(st, T, journal.Line{Event: journal.EventPublishDone}); err != nil {
		t.Fatal(err)
	}
	agree("a published ticket")
}

// TestTUIWaitsForATicketThatIsNotMintedYet: `jig tui` on the id the next ticket
// will get opens its screen saying it waits, as a demo does when the screens
// start before the session plans the ticket, and fills in once the ticket is
// minted. An id the store would not mint is refused.
func TestTUIWaitsForATicketThatIsNotMintedYet(t *testing.T) {
	t.Parallel()
	fx, env := newFixture(t, fixture.Opts{})

	for _, bad := range []string{"jig-9", "NOPE-1", "JIG-0"} {
		if r := runJig(t, env, fx.RepoDir, "tui", bad); r.Code != 2 || strings.Contains(r.Stdout, "\f") {
			t.Errorf("jig tui %s: exit %d, want a refusal and no screen\n%s", bad, r.Code, r.Stdout)
		}
	}

	tui := startJig(t, env, fx.RepoDir, "tui", "JIG-2")
	screens := tuiScreens{t: t, p: tui}
	screens.until("waiting for JIG-2 to be created")

	minted := runJig(t, env, fx.RepoDir, "ticket", "new", "--title", "The second ticket")
	if minted.Code != 0 || !strings.Contains(minted.Stdout, "id: JIG-2") {
		t.Fatalf("jig ticket new exit = %d, want JIG-2\nstdout:\n%s\nstderr:\n%s", minted.Code, minted.Stdout, minted.Stderr)
	}
	screens.until("The second ticket", "╭─ Steps", "being drafted")
	screens.press("q")
	if res := tui.Stop(); res.Code != 0 {
		t.Fatalf("jig tui exit = %d, want 0 after q\nstderr:\n%s", res.Code, res.Stderr)
	}
}
