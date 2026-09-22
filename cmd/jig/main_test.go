package main

import (
	"bytes"
	"errors"
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
// should drive the "parked" state, the parked custody table with its answer
// resume command, and the answer hint.
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
		"  c,q-001,\"jig run JIG-1 --answer q-001 \\\"<text>\\\"\"\n"
	if !strings.Contains(got, wantParked) {
		t.Errorf("expected parked table %q, got:\n%s", wantParked, got)
	}
	wantHint := "  Run `jig run JIG-1 --answer q-001 \"<text>\"` to answer and resume\n"
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
		"  c,q-001,jig requeue JIG-1 --from-brief-diff\n"
	if !strings.Contains(got, wantParked) {
		t.Errorf("expected flawed-brief parked table %q, got:\n%s", wantParked, got)
	}
	// The help hint must offer the exact same resume command as the parked
	// table's "resume" column, not the generic --answer form: the question
	// is not going to be answered with text, the brief needs amending.
	wantHint := "  Run `jig requeue JIG-1 --from-brief-diff` to amend the brief and resume\n"
	if !strings.HasSuffix(got, wantHint) {
		t.Errorf("expected flawed-brief hint suffix %q, got:\n%s", wantHint, got)
	}
}

// TestRenderStatusStalled exercises the stalled path: a stalled slice should
// drive the "stalled" state (outranking a simultaneously parked slice), the
// stalled custody table with its stall signature, and the stalled
// remediation hint - even while another slice is separately parked, proving
// stalled beats parked for the state line without hiding the parked table.
func TestRenderStatusStalled(t *testing.T) {
	t.Setenv("JIG_HOME", t.TempDir())
	fx := fixture.Generate(t, fixture.Opts{})

	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	if err := st.WriteSliceState(fx.Ticket, "a", store.SliceState{State: "stalled", Attempts: 2, Reason: "stall", Signature: "a|code-bug|nil pointer"}); err != nil {
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
	wantStalled := "stalled[1]{slice,reason,signature}:\n" +
		"  a,stall,a|code-bug|nil pointer\n"
	if !strings.Contains(got, wantStalled) {
		t.Errorf("expected stalled table %q, got:\n%s", wantStalled, got)
	}
	if !strings.Contains(got, "parked[1]{slice,question,resume}:\n") {
		t.Errorf("expected the parked table to still render alongside stalled, got:\n%s", got)
	}
	// The hint keeps the open question's priority even though the state
	// line reports "stalled" (see nextStepHint's doc comment).
	wantHint := "  Run `jig run JIG-1 --answer q-001 \"<text>\"` to answer and resume\n"
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
	wantStalled := "stalled[1]{slice,reason,signature}:\n  a,attempt-cap,-\n"
	if !strings.Contains(got, wantStalled) {
		t.Errorf("expected stalled table with '-' signature %q, got:\n%s", wantStalled, got)
	}
}

// TestRenderStatusStalledHintFromGate: a stalled slice with no FromBrief
// (every gate fix slice) must not be offered the `--from-brief-diff`
// remedy, since frontier.Requeue only touches a slice whose FromBrief
// cites a hash that is gone - a fix slice has none, so that requeue would
// silently do nothing and the hint would repeat forever. The hint must
// also name no command at all here (there is none that would resolve it)
// and must not claim where the slice came from (a hand-written slice with
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

	if strings.Contains(got, "requeue") {
		t.Errorf("hint names a requeue command for a slice with no FromBrief, want no command named, got:\n%s", got)
	}
	if strings.Contains(got, "gate round") {
		t.Errorf("hint claims where the slice came from, want no origin claim, got:\n%s", got)
	}
	wantHint := "  Slice fix-1 is stalled (attempt-cap): it has no brief section to amend\n"
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

// TestScreenDenyAllow drives the _screen hook handler directly against a
// denied git-push call and an allowed git-status call.
func TestScreenDenyAllow(t *testing.T) {
	deny := `{"tool_name":"Bash","tool_input":{"command":"git push origin main"}}`
	var out bytes.Buffer
	runScreen(strings.NewReader(deny), &out)
	if out.Len() == 0 {
		t.Fatal("expected deny output for git push, got none")
	}
	if !strings.Contains(out.String(), `"permissionDecision":"deny"`) {
		t.Fatalf("expected permissionDecision deny, got: %s", out.String())
	}
	if !strings.Contains(out.String(), "git push") {
		t.Fatalf("expected reason to mention git push, got: %s", out.String())
	}

	allow := `{"tool_name":"Bash","tool_input":{"command":"git status"}}`
	out.Reset()
	runScreen(strings.NewReader(allow), &out)
	if out.Len() != 0 {
		t.Fatalf("expected no output for an allowed command, got: %s", out.String())
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
