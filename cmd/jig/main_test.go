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

// TestRenderStatus checks the status renderer against a hand-constructed
// store state: slice a green (attempt 1), b queued (blocked_by a from the
// fixture's own slices.yaml), c and d queued.
func TestRenderStatus(t *testing.T) {
	t.Setenv("JIG_HOME", t.TempDir())
	fx := fixture.Generate(t, fixture.Opts{})

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
		"slices[4]{id,state,attempts,blocked_by,question}:\n" +
		"  a,green,1,-,-\n" +
		"  b,queued,0,a,-\n" +
		"  c,queued,0,-,-\n" +
		"  d,queued,0,-,-\n" +
		"questions: none\n" +
		"help[1]:\n" +
		"  Run `jig run JIG-1` to work the frontier\n"

	if got != want {
		t.Fatalf("RenderStatus mismatch:\n--- got ---\n%q\n--- want ---\n%q", got, want)
	}
}

// TestRenderStatusParked exercises the needs-input path: an open question
// should drive the "parked" state, the parked custody table with its resume
// command, and the matching hint. Slice c has FromBrief sections (see
// testdata/fixture/slices.yaml), but this question carries no
// "flawed-brief" Reason, so resumeCommand's other requirement is unmet and
// the resume command is the plain --answer form (TestRenderStatusParkedFlawedBrief
// covers the same slice with that Reason set, where the amend-brief-then-
// requeue form applies instead). The parked cell also renders with no
// quoting: resumeCommand's placeholder is single-quoted so the whole value
// contains no character axi.Quote must escape, unlike the double-quoted
// form this replaced (see internal/axi's TestQuoteUnchangedWhenPlain and
// TestTableQuotesCells assertions).
func TestRenderStatusParked(t *testing.T) {
	t.Setenv("JIG_HOME", t.TempDir())
	fx := fixture.Generate(t, fixture.Opts{})

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
		"  c,q-001,jig run JIG-1 --answer q-001 '<text>'\n"
	if !strings.Contains(got, wantParked) {
		t.Errorf("expected parked table %q, got:\n%s", wantParked, got)
	}
	wantHint := "  Run `jig run JIG-1 --answer q-001 '<text>'` to answer and resume\n"
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
	t.Setenv("JIG_HOME", t.TempDir())
	fx := fixture.Generate(t, fixture.Opts{})

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
		"  fix-1,q-001,jig run JIG-1 --answer q-001 '<text>'\n"
	if !strings.Contains(got, wantParked) {
		t.Errorf("expected parked table %q, got:\n%s", wantParked, got)
	}
	wantHint := "  Run `jig run JIG-1 --answer q-001 '<text>'` to answer and resume\n"
	if !strings.HasSuffix(got, wantHint) {
		t.Errorf("expected answer hint suffix %q, got:\n%s", wantHint, got)
	}
}

// TestRenderStatusParkedFlawedBrief checks the parked table's other resume
// form: a needs-input slice whose reason is "flawed-brief" resumes via
// requeue --from-brief-diff, not --answer, since the way out is amending the
// brief and requeuing the slice, not answering the question with text.
func TestRenderStatusParkedFlawedBrief(t *testing.T) {
	t.Setenv("JIG_HOME", t.TempDir())
	fx := fixture.Generate(t, fixture.Opts{})

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

// TestGateFixSliceFlawedBriefResumesWithAnswer drives a real gate round
// through `jig run`/`jig gate` (fake backend, scripted gate source) until a
// gate fix slice - which never has FromBrief - is parked on a flawed-brief
// question. Before resumeCommand keyed off the slice's own structure, this
// was the exact regression the review caught: routeQuestion sets Reason
// "flawed-brief" from the build session's outcome alone, so a fix slice
// landed on the same Reason as a brief-derived slice, and the old
// Reason-keyed resumeCommand printed `jig requeue ... --from-brief-diff` -
// a command that can never touch a slice with no FromBrief hash to
// recompute, wedging the ticket forever.
func TestGateFixSliceFlawedBriefResumesWithAnswer(t *testing.T) {
	t.Setenv("JIG_HOME", t.TempDir())
	fx := fixture.Generate(t, fixture.Opts{ScenarioBranch: "fix-flawed-brief"})

	runArgs := func(extra ...string) []string {
		return append([]string{"run", fx.Ticket, "--backend", "fake", "--scenario", fx.ScenarioDir, "--store", fx.StoreDir}, extra...)
	}

	// 1. a, b, d green; c parks on q-001 (see testdata/fixture/scenario).
	var buf1 bytes.Buffer
	if code := Main(runArgs(), &buf1, strings.NewReader("")); code != 2 {
		t.Fatalf("run 1 exit = %d, want 2 (paused at q-001)\n%s", code, buf1.String())
	}

	// 2. answer q-001: every base slice reaches green.
	var buf2 bytes.Buffer
	if code := Main(runArgs("--answer", "q-001", "Casual."), &buf2, strings.NewReader("")); code != 0 {
		t.Fatalf("run 2 (answer) exit = %d, want 0\n%s", code, buf2.String())
	}

	// 3. gate round 1 appends fix-1 (see testdata/fixture/scenario/gate).
	var buf3 bytes.Buffer
	gateCode := Main([]string{"gate", fx.Ticket, "--scenario", fx.ScenarioDir, "--store", fx.StoreDir}, &buf3, strings.NewReader(""))
	if gateCode != 0 {
		t.Fatalf("gate exit = %d, want 0\n%s", gateCode, buf3.String())
	}

	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	slices, err := st.ReadSlices(fx.Ticket)
	if err != nil {
		t.Fatalf("ReadSlices: %v", err)
	}
	fix1, ok := sliceByID(slices, "fix-1")
	if !ok {
		t.Fatalf("expected gate round 1 to append fix-1; got %+v", slices)
	}
	if len(fix1.FromBrief) != 0 {
		t.Fatalf("fix-1.FromBrief = %v, want none (test setup is wrong)", fix1.FromBrief)
	}

	// 4. run again: fix-1 dispatches to the scripted flawed-brief outcome
	// (testdata/fixture/scenario-branches/fix-flawed-brief) and parks.
	var buf4 bytes.Buffer
	if code := Main(runArgs(), &buf4, strings.NewReader("")); code != 2 {
		t.Fatalf("run 3 (fix-1) exit = %d, want 2 (paused)\n%s", code, buf4.String())
	}
	fix1State, err := st.ReadSliceState(fx.Ticket, "fix-1")
	if err != nil {
		t.Fatalf("ReadSliceState fix-1: %v", err)
	}
	if fix1State.State != "needs-input" || fix1State.Reason != "flawed-brief" {
		t.Fatalf("fix-1 state = %+v, want needs-input/flawed-brief (test setup is wrong)", fix1State)
	}

	got, err := RenderStatus(st, fx.Ticket)
	if err != nil {
		t.Fatalf("RenderStatus: %v", err)
	}
	wantResume := fmt.Sprintf("jig run %s --answer %s '<text>'", fx.Ticket, fix1State.Question)
	if !strings.Contains(got, wantResume) {
		t.Errorf("expected fix-1's resume command %q, got:\n%s", wantResume, got)
	}
	if strings.Contains(got, "--from-brief-diff") {
		t.Errorf("fix-1 (no FromBrief) was offered --from-brief-diff, which can never touch it, got:\n%s", got)
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
	t.Setenv("JIG_HOME", t.TempDir())
	fx := fixture.Generate(t, fixture.Opts{})

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
	wantHint := "  Run `jig run JIG-1 --answer q-001 '<text>'` to answer and resume\n"
	if !strings.HasSuffix(got, wantHint) {
		t.Errorf("expected answer hint suffix %q, got:\n%s", wantHint, got)
	}
}

// TestRenderStatusStalledHint checks the stalled hint text itself, with no
// open question in the way.
func TestRenderStatusStalledHint(t *testing.T) {
	t.Setenv("JIG_HOME", t.TempDir())
	fx := fixture.Generate(t, fixture.Opts{})

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
// remedy, since frontier.Requeue only touches a slice whose FromBrief
// cites a hash that is gone - a fix slice has none, so that requeue would
// silently do nothing and the hint would repeat forever. Its way out is
// `jig requeue --slice`, the only command that can ever touch it, and the
// hint must not claim where the slice came from (a hand-written slice with
// no FromBrief looks identical to this fixture).
func TestRenderStatusStalledHintFromGate(t *testing.T) {
	t.Setenv("JIG_HOME", t.TempDir())
	fx := fixture.Generate(t, fixture.Opts{})

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
	t.Setenv("JIG_HOME", t.TempDir())
	fx := fixture.Generate(t, fixture.Opts{})

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
	t.Setenv("JIG_HOME", t.TempDir())
	fx := fixture.Generate(t, fixture.Opts{})

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
	t.Setenv("JIG_HOME", t.TempDir())
	fx := fixture.Generate(t, fixture.Opts{})

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
	cases := []struct {
		name      string
		reason    string
		fromBrief []string
		want      string
	}{
		{"flawed brief with sections", "flawed-brief", []string{"abc"}, "jig requeue T-1 --from-brief-diff"},
		{"flawed brief without sections", "flawed-brief", nil, "jig run T-1 --answer q-001 '<text>'"},
		{"plain question with sections", "", []string{"abc"}, "jig run T-1 --answer q-001 '<text>'"},
		{"plain question without sections", "", nil, "jig run T-1 --answer q-001 '<text>'"},
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
	t.Setenv("JIG_HOME", t.TempDir())
	fx := fixture.Generate(t, fixture.Opts{})

	var buf bytes.Buffer
	code := Main([]string{"validate", fx.Ticket, "--store", fx.StoreDir}, &buf, strings.NewReader(""))
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
	slices := []store.Slice{
		{ID: "a", BlockedBy: []string{"b"}, Oracle: "test", Workspace: "root"},
		{ID: "b", BlockedBy: []string{"a"}, Oracle: "test", Workspace: "root"},
	}
	if cyc := findBlockedByCycle(slices); cyc == "" {
		t.Fatal("expected a cycle to be detected")
	}
}

// TestScreenDenyAllow drives the _screen hook handler directly: a denied
// git-push call gets a deny decision; a passing call to a tool the screen
// grants (Bash, Read) gets an allow decision, since that allow is the
// tool's only grant in a headless session; a passing edit-tool call and
// malformed input get no decision at all, leaving the call to the session's
// permission rules.
func TestScreenDenyAllow(t *testing.T) {
	cases := []struct {
		name  string
		input string
		want  string // exact stdout
	}{
		{
			"denied git push",
			`{"tool_name":"Bash","tool_input":{"command":"git push origin main"}}`,
			`{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"Blocked ` + "`git push`" + `: history must not be pushed from a screened session."},"systemMessage":"Blocked ` + "`git push`" + `: history must not be pushed from a screened session."}` + "\n",
		},
		{
			"denied secret read",
			`{"tool_name":"Read","tool_input":{"file_path":"/repo/.env"}}`,
			`{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"Blocked: ` + "`/repo/.env`" + ` may hold live credentials."},"systemMessage":"Blocked: ` + "`/repo/.env`" + ` may hold live credentials."}` + "\n",
		},
		{
			"granted Bash",
			`{"tool_name":"Bash","tool_input":{"command":"git status"}}`,
			`{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"allow","permissionDecisionReason":"screened by jig"}}` + "\n",
		},
		{
			"granted Read",
			`{"tool_name":"Read","tool_input":{"file_path":"/store/T-1/work/a.attempt-1.slice.json"}}`,
			`{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"allow","permissionDecisionReason":"screened by jig"}}` + "\n",
		},
		{
			"passing Write left to the rules",
			`{"tool_name":"Write","tool_input":{"file_path":"/wt/main.go","content":"x"}}`,
			"",
		},
		{
			"denied secret Write",
			`{"tool_name":"Write","tool_input":{"file_path":"/wt/.env","content":"x"}}`,
			`{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"deny","permissionDecisionReason":"Blocked: ` + "`/wt/.env`" + ` may hold live credentials."},"systemMessage":"Blocked: ` + "`/wt/.env`" + ` may hold live credentials."}` + "\n",
		},
		{
			"malformed input",
			`{"tool_name":`,
			"",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var out bytes.Buffer
			runScreen(strings.NewReader(c.input), &out)
			if out.String() != c.want {
				t.Fatalf("runScreen(%s) =\n%q\nwant\n%q", c.input, out.String(), c.want)
			}
		})
	}
}

// TestScreenDeniesUnreadableInput drives the _screen hook handler - the
// same runScreen the real `jig _screen` binary runs on its stdin/stdout -
// with payload shapes an adversarial review found allowed: a known tool
// whose required argument is missing, sent under the wrong key, or sent
// with a type SecretPath cannot read (a list instead of a string, and the
// like) must get a deny decision instead of silently falling through to
// the session's permission rules.
func TestScreenDeniesUnreadableInput(t *testing.T) {
	cases := []struct {
		name  string
		input string
	}{
		{"Bash command under the wrong key", `{"tool_name":"Bash","tool_input":{"cmd":"git push"}}`},
		{"Bash empty tool_input", `{"tool_name":"Bash","tool_input":{}}`},
		{"Bash no tool_input at all", `{"tool_name":"Bash"}`},
		{"Bash command is a list", `{"tool_name":"Bash","tool_input":{"command":["git","push"]}}`},
		{"Read file_path is missing", `{"tool_name":"Read","tool_input":{}}`},
		{"Read file_path is a number", `{"tool_name":"Read","tool_input":{"file_path":7}}`},
		{"Glob pattern is missing", `{"tool_name":"Glob","tool_input":{"path":"."}}`},
		{"Grep path is a number", `{"tool_name":"Grep","tool_input":{"pattern":"x","path":7}}`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var out bytes.Buffer
			runScreen(strings.NewReader(c.input), &out)
			if !strings.Contains(out.String(), `"permissionDecision":"deny"`) {
				t.Fatalf("runScreen(%s) = %q, want a deny decision", c.input, out.String())
			}
			if !strings.Contains(out.String(), "cannot be judged") {
				t.Errorf("runScreen(%s) = %q, want a reason saying the call cannot be judged", c.input, out.String())
			}
		})
	}
}

// TestScreenGrantsEveryToolWithValidInput drives the _screen hook handler
// for every tool a passing screen grants (screen.Granted), pinning that a
// well-formed call to each still gets its allow decision through the real
// hook path, not only Bash and Read.
func TestScreenGrantsEveryToolWithValidInput(t *testing.T) {
	cases := []struct {
		tool  string
		input string
	}{
		{"Bash", `{"command":"git status"}`},
		{"Read", `{"file_path":"/store/T-1/work/a.attempt-1.slice.json"}`},
		{"Glob", `{"pattern":"**/*.go"}`},
		{"Grep", `{"pattern":"func main"}`},
	}
	for _, c := range cases {
		t.Run(c.tool, func(t *testing.T) {
			var out bytes.Buffer
			runScreen(strings.NewReader(`{"tool_name":"`+c.tool+`","tool_input":`+c.input+`}`), &out)
			want := `{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"allow","permissionDecisionReason":"screened by jig"}}` + "\n"
			if out.String() != want {
				t.Fatalf("runScreen(%s %s) = %q, want %q", c.tool, c.input, out.String(), want)
			}
		})
	}
}

// TestHelpContainsCommands checks that bare `jig` help output names every
// non-hidden command and flag in commandTable, and that hidden ones (the
// _screen command, gate's --pr flag) are absent from it.
func TestHelpContainsCommands(t *testing.T) {
	var buf bytes.Buffer
	code := Main(nil, &buf, strings.NewReader(""))
	if code != 0 {
		t.Fatalf("jig (bare) exit code = %d", code)
	}
	out := buf.String()
	for _, c := range commandTable {
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
				if strings.Contains(out, row) {
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
	t.Setenv("JIG_HOME", t.TempDir())
	fx := fixture.Generate(t, fixture.Opts{})

	cfg, err := project.Load(fx.StoreDir + "/project.yaml")
	if err != nil {
		t.Fatalf("project.Load: %v", err)
	}
	if _, err := project.InitProject(fx.StoreDir, nil); err != nil {
		t.Fatalf("InitProject: %v", err)
	}

	// No --store and a cwd that resolves nothing: --project alone must
	// still find the store through the machine mapping.
	st, gotCfg, _, err := resolveStoreForProject(cfg.Name, "")
	if err != nil {
		t.Fatalf("resolveStoreForProject: %v", err)
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
	t.Setenv("JIG_HOME", t.TempDir())

	_, _, _, err := resolveStoreForProject("no-such-project", "")
	var ae *axi.Error
	if !errors.As(err, &ae) || ae.Code != "VALIDATION_ERROR" {
		t.Fatalf("err = %v, want *axi.Error VALIDATION_ERROR", err)
	}
}

// TestNormalizeFlagErr checks that every message shape the stdlib flag
// package produces gets its single-dash flag reference rewritten to jig's
// own double-dash convention.
func TestNormalizeFlagErr(t *testing.T) {
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
	var buf bytes.Buffer
	code := Main([]string{"bogus"}, &buf, strings.NewReader(""))
	if code != 2 {
		t.Fatalf("exit code = %d, want 2; output:\n%s", code, buf.String())
	}
	if !strings.Contains(buf.String(), "VALIDATION_ERROR") {
		t.Fatalf("expected VALIDATION_ERROR, got:\n%s", buf.String())
	}
}
