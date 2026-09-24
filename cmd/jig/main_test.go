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

// TestRenderStatusPaused exercises the needs-input path: an open question
// should drive both the "paused" state and the answer hint.
func TestRenderStatusPaused(t *testing.T) {
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

	if !strings.Contains(got, "state: paused\n") {
		t.Errorf("expected paused state, got:\n%s", got)
	}
	if !strings.Contains(got, "questions[1]{id,slice,status}:\n  q-001,c,open\n") {
		t.Errorf("expected questions table, got:\n%s", got)
	}
	wantHint := "  Run `jig run JIG-1 --answer q-001 \"<text>\"` to answer and resume\n"
	if !strings.HasSuffix(got, wantHint) {
		t.Errorf("expected answer hint suffix %q, got:\n%s", wantHint, got)
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
