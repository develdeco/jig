package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/phase"
	"github.com/develdeco/jig/internal/store"
)

// TestClaimProblemsNameThePromptThatMakesTheTicketReady: a claim that is
// warned or refused says what to ask: the brief's approval is planning, a
// blocker is whatever phase the blocker is at, and blockers that wait for one
// another do not send the derivation round in circles.
func TestClaimProblemsNameThePromptThatMakesTheTicketReady(t *testing.T) {
	t.Parallel()
	st := checkStore(t, "merged")

	got, err := claimProblems(st, "JIG-2", claimDeps{})
	if err != nil || len(got) != 2 {
		t.Fatalf("claimProblems = %+v, %v, want the blocker and the approval", got, err)
	}
	// JIG-1 has no brief and no slices: it is being planned.
	if !strings.Contains(got[0].Help, "; ask: Plan JIG-1") {
		t.Errorf("the blocker's help = %q, want it to ask for the blocker's phase", got[0].Help)
	}
	if !strings.HasSuffix(got[1].Help, "; ask: Plan JIG-2 (approve its brief)") {
		t.Errorf("the approval's help = %q, want it to ask to plan, with what that does", got[1].Help)
	}

	// JIG-1 builds: it has an approved brief and a slice nobody started.
	put := func(ticket, name, content string) {
		path := filepath.Join(st.TicketDir(ticket), name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	put("JIG-1", "brief.md", "# First\n\n## Goal\n\nDo it.\n")
	put("JIG-1", "slices.yaml", "slices:\n  - id: a\n    workspace: w\n    goal: g\n    oracle: test\n    blocked_by: []\n    from_brief: []\n")
	approveBrief(t, st, "JIG-1")
	if got, _ = claimProblems(st, "JIG-2", claimDeps{}); !strings.Contains(got[0].Help, "; ask: Build JIG-1") {
		t.Errorf("the blocker's help = %q, want it to ask to build the blocker", got[0].Help)
	}

	// JIG-1 waits for JIG-2 in turn: the blockers' prompts stop at the first
	// repeat instead of asking for each other for good.
	put("JIG-1", "ticket.yaml", "schema_version: 1\ntitle: First\nblocked_by:\n  - ticket: JIG-2\n    kind: merged\n")
	if got, _ = claimProblems(st, "JIG-2", claimDeps{}); len(got) == 0 || strings.Contains(got[0].Help, "ask: ") {
		t.Errorf("blockers that wait for one another: %+v, want no prompt for the repeat", got)
	}
	f, _, err := phaseFacts(st, "JIG-2")
	if err != nil {
		t.Fatal(err)
	}
	if len(f.Blockers) != 1 || f.Blockers[0].Ticket != "JIG-1" || f.Blockers[0].Prompt != "" {
		t.Errorf("blockers in a cycle = %+v, want JIG-1 with no prompt", f.Blockers)
	}
}

// TestPhaseAdviceIsOneLinePerState: the line that says what to do, and the
// prompt that asks for it, for a ticket at each phase and in each way it can be
// held up.
func TestPhaseAdviceIsOneLinePerState(t *testing.T) {
	t.Parallel()
	const T = "SHOP-1"
	approved := phase.Facts{Ticket: T, BriefExists: true, Sections: 2, Approval: phase.ApprovalCurrent, ApprovedBy: "Dana",
		Slices: []phase.Slice{{ID: "a", State: "queued"}}}
	green := approved
	green.Slices = []phase.Slice{{ID: "a", State: "green"}}
	open := green
	open.Open = &phase.OpenRound{Round: 2}
	asked := green
	asked.Asks = []phase.Ask{{ID: "r1-f1"}}
	published := green
	published.Published = true
	drafted := approved
	drafted.Approval = phase.ApprovalNone
	blocked := approved
	blocked.Blockers = []phase.Blocker{{Ticket: "SHOP-0", Kind: "merged", Reason: "SHOP-1 waits for SHOP-0, which has not published", Prompt: "Build SHOP-0"}}
	round := func(n int, event string) journal.Line { return journal.Line{Event: event, Attempt: n} }

	for _, tc := range []struct {
		name  string
		facts phase.Facts
		lines []journal.Line
		want  string
	}{
		{"nothing drafted", phase.Facts{Ticket: T}, nil, intakeHint(T)},
		{"a brief that waits for the person", drafted, nil,
			"The brief of SHOP-1 waits for the person's approval: run `jig validate SHOP-1` once they have confirmed it; ask: Plan SHOP-1 (approve its brief)"},
		{"a build to start", approved, nil,
			"Run `jig next SHOP-1` to see what is ready, then `jig claim SHOP-1 --slice <slice>` to take one; ask: Build SHOP-1"},
		{"a build held up by a blocker", blocked, nil, "SHOP-1 waits for SHOP-0, which has not published; ask: Build SHOP-0"},
		{"a first round to open", green, nil, "Run `jig gate SHOP-1` to open a gate round; ask: Review SHOP-1"},
		{"a round open", open, []journal.Line{round(2, journal.EventGateOpen)},
			"round 2 of SHOP-1 is open and waits for a reviewer: the reviewer ends it with `jig review done SHOP-1`; ask: Review SHOP-1"},
		{"asks to decide", asked, []journal.Line{round(1, journal.EventGateRound)},
			"1 ask of SHOP-1 waits for a person to decide (`jig triage SHOP-1 <id> keep|dismiss`); ask: Review SHOP-1"},
		{"a next round to open", green, []journal.Line{round(1, journal.EventGateRound)}, "Run `jig gate SHOP-1` to open the next gate round; ask: Review SHOP-1"},
		{"a clean round", green, []journal.Line{round(1, journal.EventGateClean)}, "Run `jig publish SHOP-1` to open or update the PR; ask: Publish SHOP-1"},
		{"published", published, nil, "SHOP-1 is published: run `jig next` to see the tickets that are left"},
	} {
		if got := phaseAdvice(phase.Derive(tc.facts, tc.lines), T); got != tc.want {
			t.Errorf("%s:\n got %q\nwant %q", tc.name, got, tc.want)
		}
	}
}

// TestPhaseFactsReadTheStore: the facts of a ticket are what its files say, and
// a gate round's verdict is the report's, as publish reads it.
func TestPhaseFactsReadTheStore(t *testing.T) {
	t.Parallel()
	st := checkStore(t, "merged")
	put := func(name, content string) {
		path := filepath.Join(st.TicketDir("JIG-2"), name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	put("slices.yaml", "slices:\n  - id: a\n    workspace: w\n    goal: first\n    oracle: test\n    blocked_by: []\n    from_brief: []\n  - id: b\n    workspace: w\n    goal: second\n    oracle: test\n    blocked_by: [a]\n    from_brief: []\n  - id: r1-fix-1\n    workspace: w\n    goal: fix\n    oracle: test\n    blocked_by: []\n    from_brief: []\n    from_gate: 1\n")
	if err := st.WriteSliceState("JIG-2", "a", store.SliceState{State: "green", Attempts: 1}); err != nil {
		t.Fatal(err)
	}
	if err := st.WriteSliceState("JIG-2", "b", store.SliceState{State: "needs-input", Question: "q-001"}); err != nil {
		t.Fatal(err)
	}
	if err := st.WriteQuestion("JIG-2", store.Question{ID: "q-001", Slice: "b", Status: "open", Body: "which?"}); err != nil {
		t.Fatal(err)
	}
	approveBrief(t, st, "JIG-2")
	for n, body := range map[int]string{1: "verdict: fix-slices\nreviewer:\n  session: s-rev\n", 2: "verdict: clean\nindependent: false\n", 3: "verdict: clean\n", 4: "not: [yaml"} {
		dir := filepath.Join(st.TicketDir("JIG-2"), "gate", fmt.Sprintf("round-%d", n))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "report.yaml"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := journal.Append(st, "JIG-2", journal.Line{Event: journal.EventPublishDone}); err != nil {
		t.Fatal(err)
	}

	f, lines, err := phaseFacts(st, "JIG-2")
	if err != nil {
		t.Fatal(err)
	}
	if len(lines) != 1 || !f.Published || f.Ticket != "JIG-2" || f.Adopted {
		t.Errorf("facts = %+v with %d lines, want published and not adopted", f, len(lines))
	}
	if !f.BriefExists || f.Sections != 1 || f.Approval != phase.ApprovalCurrent || f.ApprovedBy != "P" {
		t.Errorf("brief facts = %+v, want a brief of one section, approved by P", f)
	}
	var slices []string
	for _, s := range f.Slices {
		slices = append(slices, fmt.Sprintf("%s:%s:%d:%v", s.ID, s.State, s.FromGate, s.OpenQuestion))
	}
	if want := "a:green:0:false b:needs-input:0:true r1-fix-1:queued:1:false"; strings.Join(slices, " ") != want {
		t.Errorf("slices = %v, want %s", slices, want)
	}
	var rounds []string
	for _, r := range f.Rounds {
		rounds = append(rounds, fmt.Sprintf("%d:%s:%s", r.N, r.Verdict, r.Reviewer))
	}
	if want := "1:fix-slices:s-rev 2:not-independent: 3:clean: 4::"; strings.Join(rounds, " ") != want {
		t.Errorf("rounds = %v, want %s (an unreadable report is a round with no verdict)", rounds, want)
	}
	if len(f.Blockers) != 1 || f.Blockers[0].Ticket != "JIG-1" || f.Blockers[0].Kind != "merged" {
		t.Errorf("blockers = %+v, want JIG-1", f.Blockers)
	}
}

// TestNextAsksOnlyAboutATicketAndAPhase: --for names a phase and needs a ticket.
func TestNextAsksOnlyAboutATicketAndAPhase(t *testing.T) {
	t.Parallel()
	e := testEnv(t.TempDir()) // both refusals come before any store is read
	for _, tc := range []struct {
		args []string
		want string
	}{
		{[]string{"next", "--for", "build"}, "--for asks about a ticket"},
		{[]string{"next", "JIG-1", "--for", "deploy"}, `--for "deploy" is none of the phases`},
	} {
		out, code := runMain(t, e, "", tc.args...)
		if code != 2 || !strings.Contains(out, "code: VALIDATION_ERROR") || !strings.Contains(out, tc.want) {
			t.Errorf("jig %s: exit %d, want a VALIDATION_ERROR saying %q\n%s", strings.Join(tc.args, " "), code, tc.want, out)
		}
	}
}
