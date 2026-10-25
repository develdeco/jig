package main

import (
	"bytes"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/project"
	"github.com/develdeco/jig/internal/store"
)

// TestVerifydeliverDepsCarriesTheOperatorsHome: jig gate and jig publish
// build their Deps here, and gate intent inference reads the operator's
// local agent transcripts under Deps.UserHome, so it must be the operator's
// own home directory (os.UserHomeDir), beside the jig home root it was
// handed.
func TestVerifydeliverDepsCarriesTheOperatorsHome(t *testing.T) {
	t.Parallel()
	userHome := t.TempDir()
	jigHome := t.TempDir()
	e := envFrom(map[string]string{"HOME": userHome, "USERPROFILE": userHome})

	d := verifydeliverDeps(e, nil, project.Config{}, project.MachineProject{}, jigHome)
	if d.UserHome != userHome {
		t.Errorf("Deps.UserHome = %q, want the operator's home %q", d.UserHome, userHome)
	}
	if d.Home != jigHome {
		t.Errorf("Deps.Home = %q, want the jig home %q", d.Home, jigHome)
	}
}

// TestRenderStatus checks the status renderer against a hand-constructed
// store state: slice a green (attempt 1), b queued (blocked_by a from the
// fixture's own slices.yaml), c and d queued.
func TestRenderStatus(t *testing.T) {
	t.Parallel()
	fx := newFixture(t, fixture.Opts{})

	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	if err := st.WriteSliceState(fx.Ticket, "a", store.SliceState{State: "green", Attempts: 1}); err != nil {
		t.Fatalf("write slice state a: %v", err)
	}

	got, err := RenderStatus(st, fx.Ticket)
	if err != nil {
		t.Fatalf("RenderStatus: %v", err)
	}

	want := "ticket: JIG-1\n" +
		"state: building\n" +
		"slices[4]{id,state,attempts,blocked_by,question,goal}:\n" +
		"  a,green,1,-,-,Fix Clamp so TestClamp passes.\n" +
		"  b,queued,0,a,-,\"Give Clamp a percentage-range caller, once it works.\"\n" +
		"  c,queued,0,-,-,Decide and implement the greeting's tone.\n" +
		"  d,queued,0,-,-,Smoke-test the rig environment class.\n" +
		"questions: none\n" +
		"brief:\n" +
		"  approval: none\n" +
		"brief_sections[5]{heading,slices}:\n" +
		"  Goal,-\n" +
		"  Slice A - failing clamp,a\n" +
		"  Slice B - clamp callers,b\n" +
		"  Slice C - greeting decision,c\n" +
		"  Slice D - rig smoke,d\n" +
		"help[1]:\n" +
		"  Run `jig next JIG-1` to see what is ready, then `jig claim JIG-1 --slice <slice>` to take one; ask: Build JIG-1\n"

	if got != want {
		t.Fatalf("RenderStatus mismatch:\n--- got ---\n%q\n--- want ---\n%q", got, want)
	}
}

// TestRenderStatusParked exercises the needs-input path: an open question
// should drive the "parked" state, the parked custody table with its resume
// command, and the matching hint. Slice c has FromBrief sections (see
// testdata/fixture/slices.yaml), but this question carries no
// "flawed-brief" Reason, so resumeCommand's other requirement is unmet and
// the resume command is the plain answer form (TestRenderStatusParkedFlawedBrief
// covers the same slice with that Reason set, where the amend-brief-then-
// requeue form applies instead). The parked cell also renders with no
// quoting: resumeCommand's placeholder is single-quoted so the whole value
// contains no character axi.Quote must escape, unlike the double-quoted
// form this replaced (see internal/axi's TestQuoteUnchangedWhenPlain and
// TestTableQuotesCells assertions).
func TestRenderStatusParked(t *testing.T) {
	t.Parallel()
	fx := newFixture(t, fixture.Opts{})

	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	if err := st.WriteSliceState(fx.Ticket, "a", store.SliceState{State: "green", Attempts: 1}); err != nil {
		t.Fatalf("write slice state a: %v", err)
	}
	if err := st.WriteSliceState(fx.Ticket, "c", store.SliceState{State: "needs-input", Attempts: 1, Question: "q-001"}); err != nil {
		t.Fatalf("write slice state c: %v", err)
	}
	if err := st.WriteQuestion(fx.Ticket, store.Question{ID: "q-001", Slice: "c", Status: "open", Body: "Formal or casual greeting?"}); err != nil {
		t.Fatalf("write question: %v", err)
	}

	got, err := RenderStatus(st, fx.Ticket)
	if err != nil {
		t.Fatalf("RenderStatus: %v", err)
	}

	if !strings.Contains(got, "state: parked\n") {
		t.Errorf("expected parked state, got:\n%s", got)
	}
	if !strings.Contains(got, "questions[1]{id,slice,status}:\n  q-001,c,open\n") {
		t.Errorf("expected questions table, got:\n%s", got)
	}
	wantParked := "parked[1]{slice,question,resume}:\n" +
		"  c,q-001,jig answer JIG-1 q-001 '<text>'\n"
	if !strings.Contains(got, wantParked) {
		t.Errorf("expected parked table %q, got:\n%s", wantParked, got)
	}
	wantHint := "  Run `jig answer JIG-1 q-001 '<text>'` to answer and resume\n"
	if !strings.HasSuffix(got, wantHint) {
		t.Errorf("expected answer hint suffix %q, got:\n%s", wantHint, got)
	}
}

// TestRenderStatusParkedNoFromBrief checks resumeCommand's other branch: a
// needs-input slice with no FromBrief section (a hand-written slice, or a
// gate fix slice) resumes with --answer, since --from-brief-diff could never
// touch it. The parked cell also renders with no quoting, for the same
// reason TestRenderStatusParked notes.
func TestRenderStatusParkedNoFromBrief(t *testing.T) {
	t.Parallel()
	fx := newFixture(t, fixture.Opts{})

	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	if err := st.AppendSlices(fx.Ticket, []store.Slice{{
		ID:        "fix-1",
		Workspace: "alpha",
		Goal:      "Fix the thing the gate flagged.",
		Oracle:    "test",
		FromGate:  1,
	}}); err != nil {
		t.Fatalf("AppendSlices: %v", err)
	}
	if err := st.WriteSliceState(fx.Ticket, "fix-1", store.SliceState{State: "needs-input", Attempts: 1, Question: "q-001"}); err != nil {
		t.Fatalf("write slice state fix-1: %v", err)
	}
	if err := st.WriteQuestion(fx.Ticket, store.Question{ID: "q-001", Slice: "fix-1", Status: "open", Body: "Which workspace should absorb this fix?"}); err != nil {
		t.Fatalf("write question: %v", err)
	}

	got, err := RenderStatus(st, fx.Ticket)
	if err != nil {
		t.Fatalf("RenderStatus: %v", err)
	}

	wantParked := "parked[1]{slice,question,resume}:\n" +
		"  fix-1,q-001,jig answer JIG-1 q-001 '<text>'\n"
	if !strings.Contains(got, wantParked) {
		t.Errorf("expected parked table %q, got:\n%s", wantParked, got)
	}
	wantHint := "  Run `jig answer JIG-1 q-001 '<text>'` to answer and resume\n"
	if !strings.HasSuffix(got, wantHint) {
		t.Errorf("expected answer hint suffix %q, got:\n%s", wantHint, got)
	}
}

// TestRenderStatusParkedFlawedBrief checks the parked table's other resume
// form: a needs-input slice whose reason is "flawed-brief" resumes via
// requeue --from-brief-diff, not --answer, since the way out is amending the
// brief and requeuing the slice, not answering the question with text.
func TestRenderStatusParkedFlawedBrief(t *testing.T) {
	t.Parallel()
	fx := newFixture(t, fixture.Opts{})

	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	if err := st.WriteSliceState(fx.Ticket, "c", store.SliceState{State: "needs-input", Attempts: 1, Question: "q-001", Reason: "flawed-brief"}); err != nil {
		t.Fatalf("write slice state c: %v", err)
	}
	if err := st.WriteQuestion(fx.Ticket, store.Question{ID: "q-001", Slice: "c", Status: "open", Body: "brief.md section(s) foo look wrong"}); err != nil {
		t.Fatalf("write question: %v", err)
	}

	got, err := RenderStatus(st, fx.Ticket)
	if err != nil {
		t.Fatalf("RenderStatus: %v", err)
	}

	wantParked := "parked[1]{slice,question,resume}:\n" +
		"  c,q-001,\"amend the brief, then run jig requeue JIG-1 --from-brief-diff\"\n"
	if !strings.Contains(got, wantParked) {
		t.Errorf("expected flawed-brief parked table %q, got:\n%s", wantParked, got)
	}
	// The help hint must offer the exact same resume command as the parked
	// table's "resume" column, not the generic --answer form: the question
	// is not going to be answered with text, the brief needs amending. Both
	// say the brief is amended first, not last: run as printed with no
	// amendment, `jig requeue --from-brief-diff` requeues nothing and the
	// next status is byte-identical.
	wantHint := "  Amend the brief, then run `jig requeue JIG-1 --from-brief-diff` to resume\n"
	if !strings.HasSuffix(got, wantHint) {
		t.Errorf("expected flawed-brief hint suffix %q, got:\n%s", wantHint, got)
	}
}

// TestGateFixSliceFlawedBriefResumesWithAnswer: a gate fix slice - which never
// has FromBrief - that a builder judged flawed-brief and parked on a plain
// question is resumed by answering it. Amending the brief is the remedy only
// for a slice that cites brief sections, so the question is plain, with no
// flawed-brief reason, and status resumes the slice by answering. Before that,
// the builder's outcome alone set Reason "flawed-brief", so a fix slice landed
// on the same Reason as a brief-derived slice, and a Reason-keyed
// resumeCommand printed `jig requeue ... --from-brief-diff` - a command that
// can never touch a slice with no FromBrief hash to recompute, wedging the
// ticket forever. resumeCommand keys off the slice's own structure too, so a
// store an older jig wrote, whose fix slice still carries the reason, is
// resumed by answering as well; the test checks both.
func TestGateFixSliceFlawedBriefResumesWithAnswer(t *testing.T) {
	t.Parallel()
	fx := newFixture(t, fixture.Opts{})
	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}

	// A gate round's fix slice (see testdata/fixture/scenario/gate): it cites
	// no brief section, and it is parked on a question.
	if err := st.AppendSlices(fx.Ticket, []store.Slice{{
		ID: "fix-1", Workspace: "alpha", Goal: "Add a doc comment to Add explaining its behavior.",
		Oracle: "test", BlockedBy: []string{}, FromGate: 1,
	}}); err != nil {
		t.Fatalf("AppendSlices: %v", err)
	}
	slices, err := st.ReadSlices(fx.Ticket)
	if err != nil {
		t.Fatalf("ReadSlices: %v", err)
	}
	fix1, ok := sliceByID(slices, "fix-1")
	if !ok {
		t.Fatalf("fix-1 is not in %+v", slices)
	}
	if len(fix1.FromBrief) != 0 {
		t.Fatalf("fix-1.FromBrief = %v, want none (test setup is wrong)", fix1.FromBrief)
	}
	if err := st.WriteQuestion(fx.Ticket, store.Question{ID: "q-001", Slice: "fix-1", Status: "open", Body: "The brief is silent on this; which way?"}); err != nil {
		t.Fatalf("WriteQuestion: %v", err)
	}
	fix1State := store.SliceState{State: "needs-input", Attempts: 1, Question: "q-001"}

	wantResume := fmt.Sprintf("jig answer %s %s '<text>'", fx.Ticket, fix1State.Question)
	for _, reason := range []string{"", "flawed-brief"} {
		fix1State.Reason = reason
		if err := st.WriteSliceState(fx.Ticket, "fix-1", fix1State); err != nil {
			t.Fatalf("WriteSliceState fix-1: %v", err)
		}
		got, err := RenderStatus(st, fx.Ticket)
		if err != nil {
			t.Fatalf("RenderStatus: %v", err)
		}
		if !strings.Contains(got, wantResume) {
			t.Errorf("reason %q: expected fix-1's resume command %q, got:\n%s", reason, wantResume, got)
		}
		if strings.Contains(got, "--from-brief-diff") {
			t.Errorf("reason %q: fix-1 (no FromBrief) was offered --from-brief-diff, which can never touch it, got:\n%s", reason, got)
		}
	}
}

// TestRenderStatusStalled exercises the stalled path: a stalled slice should
// drive the "stalled" state (outranking a simultaneously parked slice), the
// stalled custody table with its human-readable stall summary (not the
// normalized signature, which stays the matching key underneath), and the
// stalled remediation hint - even while another slice is separately parked,
// proving stalled beats parked for the state line without hiding the parked
// table.
func TestRenderStatusStalled(t *testing.T) {
	t.Parallel()
	fx := newFixture(t, fixture.Opts{})

	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	if err := st.WriteSliceState(fx.Ticket, "a", store.SliceState{State: "stalled", Attempts: 2, Reason: "stall", Signature: "a|code-bug|nil pointer", StallSummary: "nil pointer dereference in Clamp"}); err != nil {
		t.Fatalf("write slice state a: %v", err)
	}
	if err := st.WriteSliceState(fx.Ticket, "c", store.SliceState{State: "needs-input", Attempts: 1, Question: "q-001"}); err != nil {
		t.Fatalf("write slice state c: %v", err)
	}
	if err := st.WriteQuestion(fx.Ticket, store.Question{ID: "q-001", Slice: "c", Status: "open", Body: "Formal or casual greeting?"}); err != nil {
		t.Fatalf("write question: %v", err)
	}

	got, err := RenderStatus(st, fx.Ticket)
	if err != nil {
		t.Fatalf("RenderStatus: %v", err)
	}

	if !strings.Contains(got, "state: stalled\n") {
		t.Errorf("expected stalled state (outranking parked), got:\n%s", got)
	}
	wantStalled := "stalled[1]{slice,reason,summary}:\n" +
		"  a,stall,nil pointer dereference in Clamp\n"
	if !strings.Contains(got, wantStalled) {
		t.Errorf("expected stalled table %q, got:\n%s", wantStalled, got)
	}
	if !strings.Contains(got, "parked[1]{slice,question,resume}:\n") {
		t.Errorf("expected the parked table to still render alongside stalled, got:\n%s", got)
	}
	// The hint keeps the open question's priority even though the state
	// line reports "stalled" (see nextStepHint's doc comment). The open
	// question is a plain one, so it is answered rather than requeued (see
	// TestRenderStatusParked).
	wantHint := "  Run `jig answer JIG-1 q-001 '<text>'` to answer and resume\n"
	if !strings.HasSuffix(got, wantHint) {
		t.Errorf("expected answer hint suffix %q, got:\n%s", wantHint, got)
	}
}

// TestRenderStatusStalledHint checks the stalled hint text itself, with no
// open question in the way.
func TestRenderStatusStalledHint(t *testing.T) {
	t.Parallel()
	fx := newFixture(t, fixture.Opts{})

	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	if err := st.WriteSliceState(fx.Ticket, "a", store.SliceState{State: "stalled", Attempts: 3, Reason: "attempt-cap"}); err != nil {
		t.Fatalf("write slice state a: %v", err)
	}

	got, err := RenderStatus(st, fx.Ticket)
	if err != nil {
		t.Fatalf("RenderStatus: %v", err)
	}

	wantHint := "  Slice a is stalled (attempt-cap): amend the brief, then run `jig requeue JIG-1 --from-brief-diff`\n"
	if !strings.HasSuffix(got, wantHint) {
		t.Errorf("expected stalled hint suffix %q, got:\n%s", wantHint, got)
	}
	wantStalled := "stalled[1]{slice,reason,summary}:\n  a,attempt-cap,-\n"
	if !strings.Contains(got, wantStalled) {
		t.Errorf("expected stalled table with '-' summary %q, got:\n%s", wantStalled, got)
	}
}

// TestRenderStatusStalledHintFromGate: a stalled slice with no FromBrief
// (every gate fix slice) must not be offered the `--from-brief-diff`
// remedy, since ready.Requeue only touches a slice whose FromBrief
// cites a hash that is gone - a fix slice has none, so that requeue would
// silently do nothing and the hint would repeat forever. Its way out is
// `jig requeue --slice`, the only command that can ever touch it, and the
// hint must not claim where the slice came from (a hand-written slice with
// no FromBrief looks identical to this fixture).
func TestRenderStatusStalledHintFromGate(t *testing.T) {
	t.Parallel()
	fx := newFixture(t, fixture.Opts{})

	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	if err := st.AppendSlices(fx.Ticket, []store.Slice{{
		ID:        "fix-1",
		Workspace: "alpha",
		Goal:      "Fix the thing the gate flagged.",
		Oracle:    "test",
		FromGate:  1,
	}}); err != nil {
		t.Fatalf("AppendSlices: %v", err)
	}
	if err := st.WriteSliceState(fx.Ticket, "fix-1", store.SliceState{State: "stalled", Attempts: 3, Reason: "attempt-cap"}); err != nil {
		t.Fatalf("write slice state fix-1: %v", err)
	}

	got, err := RenderStatus(st, fx.Ticket)
	if err != nil {
		t.Fatalf("RenderStatus: %v", err)
	}

	if strings.Contains(got, "--from-brief-diff") {
		t.Errorf("hint offers --from-brief-diff for a slice with no FromBrief, which can never touch it, got:\n%s", got)
	}
	if strings.Contains(got, "gate round") {
		t.Errorf("hint claims where the slice came from, want no origin claim, got:\n%s", got)
	}
	wantHint := "  Slice fix-1 is stalled (attempt-cap): run `jig requeue JIG-1 --slice fix-1`\n"
	if !strings.HasSuffix(got, wantHint) {
		t.Errorf("expected stalled-from-gate hint suffix %q, got:\n%s", wantHint, got)
	}
}

// TestRenderStatusEnvBlocked checks the state: line's third precedence tier:
// a ticket with no needs-input or stalled slice but at least one env-blocked
// slice reports "env-blocked", the old combined "paused" value's other half
// now that parked means needs-input specifically.
func TestRenderStatusEnvBlocked(t *testing.T) {
	t.Parallel()
	fx := newFixture(t, fixture.Opts{})

	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	if err := st.WriteSliceState(fx.Ticket, "a", store.SliceState{State: "green", Attempts: 1}); err != nil {
		t.Fatalf("write slice state a: %v", err)
	}
	if err := st.WriteSliceState(fx.Ticket, "d", store.SliceState{State: "env-blocked", Attempts: 1, Reason: "blocked-by-env"}); err != nil {
		t.Fatalf("write slice state d: %v", err)
	}

	got, err := RenderStatus(st, fx.Ticket)
	if err != nil {
		t.Fatalf("RenderStatus: %v", err)
	}
	if !strings.Contains(got, "state: env-blocked\n") {
		t.Errorf("expected env-blocked state, got:\n%s", got)
	}
	// No parked or stalled table: env-blocked is neither.
	if strings.Contains(got, "parked[") || strings.Contains(got, "stalled[") {
		t.Errorf("env-blocked slice must not render a parked or stalled table, got:\n%s", got)
	}
	wantHint := "  Slice d is env-blocked (blocked-by-env): bring the env up, then run `jig requeue JIG-1 --slice d`\n"
	if !strings.HasSuffix(got, wantHint) {
		t.Errorf("expected env-blocked hint suffix %q, got:\n%s", wantHint, got)
	}
}

// TestRenderStatusParkedOutranksEnvBlocked checks the state: line's own
// precedence between needs-input and env-blocked directly: with one slice
// of each, the overall state must report "parked" (a human is needed),
// never "env-blocked" - matching the doc comment on RenderStatus's
// precedence switch (parked outranks env-blocked, which outranks green).
func TestRenderStatusParkedOutranksEnvBlocked(t *testing.T) {
	t.Parallel()
	fx := newFixture(t, fixture.Opts{})

	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	if err := st.WriteSliceState(fx.Ticket, "c", store.SliceState{State: "needs-input", Attempts: 1, Question: "q-001"}); err != nil {
		t.Fatalf("write slice state c: %v", err)
	}
	if err := st.WriteQuestion(fx.Ticket, store.Question{ID: "q-001", Slice: "c", Status: "open", Body: "Formal or casual greeting?"}); err != nil {
		t.Fatalf("write question: %v", err)
	}
	if err := st.WriteSliceState(fx.Ticket, "d", store.SliceState{State: "env-blocked", Attempts: 1, Reason: "blocked-by-env"}); err != nil {
		t.Fatalf("write slice state d: %v", err)
	}

	got, err := RenderStatus(st, fx.Ticket)
	if err != nil {
		t.Fatalf("RenderStatus: %v", err)
	}
	if !strings.Contains(got, "state: parked\n") {
		t.Errorf("expected parked to outrank env-blocked on the state: line, got:\n%s", got)
	}
}

// TestRenderStatusStalledOutranksEnvBlockedHint checks nextStepHint's
// precedence between the two stuck states directly: slice c (env-blocked)
// sorts before slice d (stalled) in testdata/fixture/slices.yaml, so the
// hint must still report the stalled slice, not the env-blocked one -
// matching the state: line's own stalled-beats-env-blocked precedence in
// RenderStatus, not just the order slices.yaml happens to list them in
// (which a single combined loop would get wrong here).
func TestRenderStatusStalledOutranksEnvBlockedHint(t *testing.T) {
	t.Parallel()
	fx := newFixture(t, fixture.Opts{})

	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	// c sorts between a and d in testdata/fixture/slices.yaml, so marking it
	// env-blocked while d (later) is stalled proves the precedence is a
	// real two-pass check, not an accident of iteration order.
	if err := st.WriteSliceState(fx.Ticket, "c", store.SliceState{State: "env-blocked", Attempts: 1, Reason: "blocked-by-env"}); err != nil {
		t.Fatalf("write slice state c: %v", err)
	}
	if err := st.WriteSliceState(fx.Ticket, "d", store.SliceState{State: "stalled", Attempts: 3, Reason: "attempt-cap"}); err != nil {
		t.Fatalf("write slice state d: %v", err)
	}

	got, err := RenderStatus(st, fx.Ticket)
	if err != nil {
		t.Fatalf("RenderStatus: %v", err)
	}
	if !strings.Contains(got, "state: stalled\n") {
		t.Errorf("expected stalled to outrank env-blocked on the state: line, got:\n%s", got)
	}
	wantHint := "  Slice d is stalled (attempt-cap): amend the brief, then run `jig requeue JIG-1 --from-brief-diff`\n"
	if !strings.HasSuffix(got, wantHint) {
		t.Errorf("expected the stalled slice's hint, not the env-blocked one's, suffix %q, got:\n%s", wantHint, got)
	}
}

// TestResumeCommandChoosesFromReasonAndBriefSections pins which command
// clears a parked slice's custody. Amending the brief and requeuing is the
// remedy only when the slice was parked for a flawed brief and has brief
// sections for --from-brief-diff to notice; every other parked slice, which
// is the common case, is resumed by answering its open question.
func TestResumeCommandChoosesFromReasonAndBriefSections(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name      string
		reason    string
		fromBrief []string
		want      string
	}{
		{"flawed brief with sections", "flawed-brief", []string{"abc"}, "jig requeue T-1 --from-brief-diff"},
		{"flawed brief without sections", "flawed-brief", nil, "jig answer T-1 q-001 '<text>'"},
		{"plain question with sections", "", []string{"abc"}, "jig answer T-1 q-001 '<text>'"},
		{"plain question without sections", "", nil, "jig answer T-1 q-001 '<text>'"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			sl := store.Slice{ID: "a", FromBrief: c.fromBrief}
			ss := store.SliceState{State: "needs-input", Question: "q-001", Reason: c.reason}
			if got := resumeCommand("T-1", sl, ss); got != c.want {
				t.Errorf("resumeCommand = %q, want %q", got, c.want)
			}
		})
	}
}

// TestValidateFixture checks `jig validate` against the fixture's committed
// brief/slices/manifest: it must report valid.
func TestValidateFixture(t *testing.T) {
	t.Parallel()
	fx := newFixture(t, fixture.Opts{})
	e := testEnv(fx.Home)

	var buf bytes.Buffer
	code := run(e, []string{"validate", fx.Ticket, "--store", fx.StoreDir}, &buf, strings.NewReader(""))
	if code != 0 {
		t.Fatalf("jig validate exit code = %d, output:\n%s", code, buf.String())
	}
	if !strings.HasPrefix(buf.String(), "valid: yes\n") {
		t.Fatalf("expected \"valid: yes\", got:\n%s", buf.String())
	}
}

// TestValidateCatchesCycle checks that a blocked_by cycle is reported as a
// validation problem instead of hanging or panicking.
func TestValidateCatchesCycle(t *testing.T) {
	t.Parallel()
	slices := []store.Slice{
		{ID: "a", BlockedBy: []string{"b"}, Oracle: "test", Workspace: "root"},
		{ID: "b", BlockedBy: []string{"a"}, Oracle: "test", Workspace: "root"},
	}
	if cyc := findBlockedByCycle(slices); cyc == "" {
		t.Fatal("expected a cycle to be detected")
	}
}

// TestHelpContainsCommands checks that bare `jig` help output names every
// non-hidden command and flag in commandTable, and that hidden ones (the
// observe command, gate's --scenario flag) are absent from it. A hidden flag
// that shares its name with a visible flag of another command is named in the
// help for that command, and the check for the hidden one skips it.
func TestHelpContainsCommands(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	code := Main(nil, &buf, strings.NewReader(""))
	if code != 0 {
		t.Fatalf("jig (bare) exit code = %d", code)
	}
	out := buf.String()
	visibleElsewhere := func(name string) bool {
		for _, c := range commandTable() {
			for _, f := range c.Flags {
				if f.Name == name && !f.Hidden && !c.Hidden {
					return true
				}
			}
		}
		return false
	}
	for _, c := range commandTable() {
		if c.Hidden {
			if strings.Contains(out, c.Name) {
				t.Errorf("help output unexpectedly contains hidden command %q", c.Name)
			}
			continue
		}
		if !strings.Contains(out, c.Name) {
			t.Errorf("help output missing command %q", c.Name)
		}
		for _, f := range c.Flags {
			row := "--" + f.Name + ","
			if f.Hidden {
				if strings.Contains(out, row) && !visibleElsewhere(f.Name) {
					t.Errorf("help output unexpectedly contains hidden flag %q for command %q", f.Name, c.Name)
				}
				continue
			}
			if !strings.Contains(out, row) {
				t.Errorf("help output missing flag %q for command %q", f.Name, c.Name)
			}
		}
	}
}

// TestResolveStoreForProjectUsesMachineMapping checks that --project
// resolves the store via the per-machine project mapping ahead of the
// --store/cwd fallback resolveStore itself falls through to.
func TestResolveStoreForProjectUsesMachineMapping(t *testing.T) {
	t.Parallel()
	jigHome := t.TempDir()
	fx := newFixture(t, fixture.Opts{Home: jigHome})
	e := testEnv(jigHome)

	cfg, err := project.Load(fx.StoreDir + "/project.yaml")
	if err != nil {
		t.Fatalf("project.Load: %v", err)
	}
	if _, err := project.InitProject(fx.Home, fx.StoreDir, nil); err != nil {
		t.Fatalf("InitProject: %v", err)
	}

	// No --store and a cwd that resolves nothing: --project alone must
	// still find the store through the machine mapping.
	st, gotCfg, _, gotHome, err := resolveStoreForProject(e, cfg.Name, "", &bytes.Buffer{})
	if err != nil {
		t.Fatalf("resolveStoreForProject: %v", err)
	}
	if gotHome != jigHome {
		t.Fatalf("jig home = %q, want JIG_HOME's %q", gotHome, jigHome)
	}
	if st.Root != fx.StoreDir {
		t.Fatalf("st.Root = %q, want %q", st.Root, fx.StoreDir)
	}
	if gotCfg.Name != cfg.Name {
		t.Fatalf("cfg.Name = %q, want %q", gotCfg.Name, cfg.Name)
	}
}

// TestResolveStoreForProjectUnknownName checks that an unmapped --project
// name is refused rather than silently falling back to cwd resolution.
func TestResolveStoreForProjectUnknownName(t *testing.T) {
	t.Parallel()
	e := testEnv(t.TempDir())

	_, _, _, _, err := resolveStoreForProject(e, "no-such-project", "", &bytes.Buffer{})
	var ae *axi.Error
	if !errors.As(err, &ae) || ae.Code != "VALIDATION_ERROR" {
		t.Fatalf("err = %v, want *axi.Error VALIDATION_ERROR", err)
	}
}

// TestNormalizeFlagErr checks that every message shape the stdlib flag
// package produces gets its single-dash flag reference rewritten to jig's
// own double-dash convention.
func TestNormalizeFlagErr(t *testing.T) {
	t.Parallel()
	cases := []struct {
		in   string
		want string
	}{
		{`flag provided but not defined: -nonexistent`, `flag provided but not defined: --nonexistent`},
		{`flag needs an argument: -branch`, `flag needs an argument: --branch`},
		{`invalid value "notanumber" for flag -pr: parse error`, `invalid value "notanumber" for flag --pr: parse error`},
		{`invalid value "badformat" for flag -clone: --clone must be name=path, got "badformat"`, `invalid value "badformat" for flag --clone: --clone must be name=path, got "badformat"`},
		{`invalid boolean value "notabool" for -standalone: parse error`, `invalid boolean value "notabool" for --standalone: parse error`},
		{`bad flag syntax: -=x`, `bad flag syntax: -=x`}, // no known flag name to rewrite; left as-is
	}
	for _, c := range cases {
		if got := normalizeFlagErr(errors.New(c.in)); got != c.want {
			t.Errorf("normalizeFlagErr(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// TestUnknownCommand checks the VALIDATION_ERROR/exit-2 path for an
// unrecognized subcommand.
func TestUnknownCommand(t *testing.T) {
	t.Parallel()
	var buf bytes.Buffer
	code := Main([]string{"bogus"}, &buf, strings.NewReader(""))
	if code != 2 {
		t.Fatalf("exit code = %d, want 2; output:\n%s", code, buf.String())
	}
	if !strings.Contains(buf.String(), "VALIDATION_ERROR") {
		t.Fatalf("expected VALIDATION_ERROR, got:\n%s", buf.String())
	}
}
