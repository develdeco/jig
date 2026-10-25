package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/phase"
	"github.com/develdeco/jig/internal/store"
)

// askSpec is one asked finding of a test's round 1. workspace and oracle are
// the build target jig derived for it: both set is an ask a bare keep builds,
// either empty one that a keep must be given the missing part of.
type askSpec struct {
	id, workspace, oracle string
}

// writeAsks records round 1's findings.yaml holding specs as asked findings.
func writeAsks(t *testing.T, storeDir, ticket string, specs ...askSpec) {
	t.Helper()
	dir := filepath.Join(storeDir, "tickets", ticket, "gate", "round-1")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir round dir: %v", err)
	}
	var b strings.Builder
	b.WriteString("scope: diff\nreviewed_paths:\n  - go.mod\nfindings:\n")
	for _, s := range specs {
		fmt.Fprintf(&b, "  - id: %s\n    file: go.mod\n    line: 1\n    title: title of %s\n    detail: d\n    action: ask\n"+
			"    risk: medium\n    risk_rationale: r\n    status: asked\n    recurrences: 0\n", s.id, s.id)
		if s.workspace != "" {
			fmt.Fprintf(&b, "    workspace: %s\n", s.workspace)
		}
		if s.oracle != "" {
			fmt.Fprintf(&b, "    oracle: %s\n", s.oracle)
		}
	}
	b.WriteString("summary: round 1\n")
	if err := os.WriteFile(filepath.Join(dir, "findings.yaml"), []byte(b.String()), 0o644); err != nil {
		t.Fatalf("write findings.yaml: %v", err)
	}
}

// greenTicket opens the fixture's store with every slice green, so the help
// names the gate directly.
func greenTicket(t *testing.T, fx *fixture.Fixture) *store.Store {
	t.Helper()
	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	for _, id := range []string{"a", "b", "c", "d"} {
		if err := st.WriteSliceState(fx.Ticket, id, store.SliceState{State: "green", Attempts: 1}); err != nil {
			t.Fatalf("write slice state %s: %v", id, err)
		}
	}
	return st
}

// triageAs runs `jig triage` for ticket's ask id with the decision and flags,
// as a person at a terminal or as an agent, and returns its exit code.
func triageAs(t *testing.T, fx *fixture.Fixture, person bool, id, decision string, flags ...string) int {
	t.Helper()
	e := testEnv(fx.Home)
	if person {
		e = e.atTerminal()
	}
	args := append([]string{"triage", fx.Ticket, id, decision}, flags...)
	out, code := runMain(t, e, "", append(args, "--store", fx.StoreDir)...)
	if code != 0 {
		t.Logf("jig triage %s %s exit = %d\n%s", id, decision, code, out)
	}
	return code
}

// askedPhaseCode is the code internal/phase gives a ticket whose asks wait for
// a decision ("" when it waits on none): status and the phase read the triage
// lines by one rule, and this is how a test sees that they do.
func askedPhaseCode(t *testing.T, st *store.Store, ticket string) string {
	t.Helper()
	p, err := derivePhase(st, ticket)
	if err != nil {
		t.Fatalf("derivePhase: %v", err)
	}
	if p.Code == phase.CodeAsksUndecided {
		return p.Code
	}
	return ""
}

// askRow is the outstanding_asks row of id in a rendered status, "" when it
// has none.
func askRow(status, id string) string {
	for _, line := range strings.Split(status, "\n") {
		if strings.HasPrefix(line, "  "+id+",") {
			return strings.TrimPrefix(line, "  ")
		}
	}
	return ""
}

const asksHeader = "outstanding_asks[%d]{id,risk,file:line,title,why,decision,decided_by}:\n"

// TestStatusShowsAPendingDecisionOnItsAsk: an ask a person or an agent decided
// with `jig triage` stays listed, with the decision and who made it, until the
// round that applies it closes. Nobody decided the other ask, and its row
// says nothing.
func TestStatusShowsAPendingDecisionOnItsAsk(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		who    string
		person bool
	}{{"person", true}, {"agent", false}} {
		t.Run(tc.who, func(t *testing.T) {
			t.Parallel()
			fx := newFixture(t, fixture.Opts{})
			st := greenTicket(t, fx)
			writeAsks(t, fx.StoreDir, fx.Ticket, askSpec{id: "r1-f1"}, askSpec{id: "r1-f2"})

			if code := triageAs(t, fx, tc.person, "r1-f1", "dismiss", "--reason", "not a problem"); code != 0 {
				t.Fatalf("jig triage exit = %d, want 0", code)
			}
			got, err := RenderStatus(st, fx.Ticket)
			if err != nil {
				t.Fatalf("RenderStatus: %v", err)
			}
			if !strings.Contains(got, fmt.Sprintf(asksHeader, 2)) {
				t.Errorf("status lacks the decision columns:\n%s", got)
			}
			if want := `r1-f1,medium,"go.mod:1",title of r1-f1,,dismiss,` + tc.who; askRow(got, "r1-f1") != want {
				t.Errorf("row of the decided ask = %q, want %q", askRow(got, "r1-f1"), want)
			}
			if want := `r1-f2,medium,"go.mod:1",title of r1-f2,,,`; askRow(got, "r1-f2") != want {
				t.Errorf("row of the undecided ask = %q, want %q", askRow(got, "r1-f2"), want)
			}
			wantHelp := "help[1]:\n  Run `jig triage JIG-1 <id> keep|dismiss` to decide the 1 undecided ask, then `jig gate JIG-1` to apply the decisions; " +
				"the 1 decided ask is applied when the next round closes\n"
			if !strings.HasSuffix(got, wantHelp) {
				t.Errorf("help does not count the undecided ask and name the decided one:\n--- got ---\n%s--- want suffix ---\n%s", got, wantHelp)
			}
		})
	}
}

// TestStatusHelpWithEveryAskDecided: once the last ask is decided, nothing is
// left to triage and the gate is the step, which applies the decisions.
func TestStatusHelpWithEveryAskDecided(t *testing.T) {
	t.Parallel()
	fx := newFixture(t, fixture.Opts{})
	st := greenTicket(t, fx)
	writeAsks(t, fx.StoreDir, fx.Ticket, askSpec{id: "r1-f1"}, askSpec{id: "r1-f2"})
	for _, id := range []string{"r1-f1", "r1-f2"} {
		if code := triageAs(t, fx, true, id, "dismiss"); code != 0 {
			t.Fatalf("jig triage %s exit = %d", id, code)
		}
	}
	got, err := RenderStatus(st, fx.Ticket)
	if err != nil {
		t.Fatalf("RenderStatus: %v", err)
	}
	wantHelp := "help[1]:\n  Run `jig gate JIG-1` to apply the decisions: the 2 decided asks are applied when the next round closes\n"
	if !strings.HasSuffix(got, wantHelp) {
		t.Errorf("help does not point at the gate alone:\n--- got ---\n%s--- want suffix ---\n%s", got, wantHelp)
	}
	if code := askedPhaseCode(t, st, fx.Ticket); code != "" {
		t.Errorf("status counts every ask as decided, but the phase still waits on them: %s", code)
	}
}

// TestStatusFlagsAKeepWithNoBuildTarget: a keep that names no workspace or
// oracle for an ask that lacks them is a decision the round cannot apply
// (route.go leaves the ask asked), so status says so and counts the ask as
// undecided. Naming the target is a re-decision, which `jig triage` accepts
// for an ask that already has a pending decision.
func TestStatusFlagsAKeepWithNoBuildTarget(t *testing.T) {
	t.Parallel()
	fx := newFixture(t, fixture.Opts{})
	st := greenTicket(t, fx)
	writeAsks(t, fx.StoreDir, fx.Ticket, askSpec{id: "r1-f1"}, askSpec{id: "r1-f2", workspace: "alpha", oracle: "test"})

	for _, id := range []string{"r1-f1", "r1-f2"} {
		if code := triageAs(t, fx, true, id, "keep", "--reason", "fix it"); code != 0 {
			t.Fatalf("jig triage %s keep exit = %d, want 0", id, code)
		}
	}
	got, err := RenderStatus(st, fx.Ticket)
	if err != nil {
		t.Fatalf("RenderStatus: %v", err)
	}
	if want := `r1-f1,medium,"go.mod:1",title of r1-f1,,keep (no build target),person`; askRow(got, "r1-f1") != want {
		t.Errorf("row of the keep with no target = %q, want %q", askRow(got, "r1-f1"), want)
	}
	if want := `r1-f2,medium,"go.mod:1",title of r1-f2,,keep,person`; askRow(got, "r1-f2") != want {
		t.Errorf("row of the keep of a buildable ask = %q, want it unflagged: %q", askRow(got, "r1-f2"), want)
	}
	wantHelp := "help[1]:\n  Run `jig triage JIG-1 <id> keep|dismiss` to decide the 1 undecided ask, then `jig gate JIG-1` to apply the decisions; " +
		"a keep with no build target needs --workspace or --oracle, or the round leaves it asked; the 1 decided ask is applied when the next round closes\n"
	if !strings.HasSuffix(got, wantHelp) {
		t.Errorf("help does not say what the keep lacks:\n--- got ---\n%s--- want suffix ---\n%s", got, wantHelp)
	}

	// The re-decision: the same ask, now with its target.
	if code := triageAs(t, fx, true, "r1-f1", "keep", "--reason", "fix it", "--workspace", "alpha", "--oracle", "test"); code != 0 {
		t.Fatalf("a re-decision of an ask with a pending decision: exit = %d, want 0", code)
	}
	got, err = RenderStatus(st, fx.Ticket)
	if err != nil {
		t.Fatalf("RenderStatus: %v", err)
	}
	if want := `r1-f1,medium,"go.mod:1",title of r1-f1,,keep,person`; askRow(got, "r1-f1") != want {
		t.Errorf("row after the re-decision = %q, want %q", askRow(got, "r1-f1"), want)
	}
	// And a decision can change again: the last line stands.
	if code := triageAs(t, fx, false, "r1-f1", "dismiss"); code != 0 {
		t.Fatalf("a second re-decision: exit = %d, want 0", code)
	}
	got, err = RenderStatus(st, fx.Ticket)
	if err != nil {
		t.Fatalf("RenderStatus: %v", err)
	}
	if want := `r1-f1,medium,"go.mod:1",title of r1-f1,,dismiss,agent`; askRow(got, "r1-f1") != want {
		t.Errorf("row after the second re-decision = %q, want %q", askRow(got, "r1-f1"), want)
	}
}

// TestStatusDoesNotShowASpentDecisionAsPending: a decision whose triage line
// precedes the last gate-round line is one a round already took. An ask that is
// still outstanding after that round (a keep it could not build) is undecided
// again, and one decided after the round is pending.
func TestStatusDoesNotShowASpentDecisionAsPending(t *testing.T) {
	t.Parallel()
	fx := newFixture(t, fixture.Opts{})
	st := greenTicket(t, fx)
	writeAsks(t, fx.StoreDir, fx.Ticket, askSpec{id: "r1-f1"}, askSpec{id: "r1-f2"})

	if code := triageAs(t, fx, true, "r1-f1", "keep"); code != 0 {
		t.Fatalf("jig triage exit = %d", code)
	}
	if err := journal.Append(st, fx.Ticket, journal.Line{Event: journal.EventGateRound, Attempt: 2}); err != nil {
		t.Fatal(err)
	}
	if code := triageAs(t, fx, true, "r1-f2", "dismiss"); code != 0 {
		t.Fatalf("jig triage exit = %d", code)
	}

	got, err := RenderStatus(st, fx.Ticket)
	if err != nil {
		t.Fatalf("RenderStatus: %v", err)
	}
	if want := `r1-f1,medium,"go.mod:1",title of r1-f1,,,`; askRow(got, "r1-f1") != want {
		t.Errorf("row of the spent decision = %q, want no decision: %q", askRow(got, "r1-f1"), want)
	}
	if want := `r1-f2,medium,"go.mod:1",title of r1-f2,,dismiss,person`; askRow(got, "r1-f2") != want {
		t.Errorf("row of the decision after the round = %q, want %q", askRow(got, "r1-f2"), want)
	}
	if code := askedPhaseCode(t, st, fx.Ticket); code != phase.CodeAsksUndecided {
		t.Errorf("the phase code = %q, want %s: the spent decision leaves r1-f1 undecided there too", code, phase.CodeAsksUndecided)
	}
}
