package screen

import (
	"bytes"
	"strings"
	"testing"
)

// TestScreenDenyAllow drives the hook handler directly: a denied
// git-push call gets a deny decision; a passing call to a tool the screen
// grants (Bash, Read) gets an allow decision, since that allow is the
// tool's only grant in a headless session; a passing edit-tool call and
// malformed input get no decision at all, leaving the call to the session's
// permission rules.
func TestScreenDenyAllow(t *testing.T) {
	t.Parallel()
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
			Hook(strings.NewReader(c.input), &out)
			if out.String() != c.want {
				t.Fatalf("Hook(%s) =\n%q\nwant\n%q", c.input, out.String(), c.want)
			}
		})
	}
}

// TestScreenDeniesUnreadableInput drives the hook handler - the
// same Hook the compiled hook program runs on its stdin/stdout -
// with payload shapes an adversarial review found allowed: a known tool
// whose required argument is missing, sent under the wrong key, or sent
// with a type SecretPath cannot read (a list instead of a string, and the
// like) must get a deny decision instead of silently falling through to
// the session's permission rules.
func TestScreenDeniesUnreadableInput(t *testing.T) {
	t.Parallel()
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
			Hook(strings.NewReader(c.input), &out)
			if !strings.Contains(out.String(), `"permissionDecision":"deny"`) {
				t.Fatalf("Hook(%s) = %q, want a deny decision", c.input, out.String())
			}
			if !strings.Contains(out.String(), "cannot be judged") {
				t.Errorf("Hook(%s) = %q, want a reason saying the call cannot be judged", c.input, out.String())
			}
		})
	}
}

// TestScreenGrantsEveryToolWithValidInput drives the hook handler
// for every tool a passing screen grants (screen.Granted), pinning that a
// well-formed call to each still gets its allow decision through the real
// hook path, not only Bash and Read.
func TestScreenGrantsEveryToolWithValidInput(t *testing.T) {
	t.Parallel()
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
			Hook(strings.NewReader(`{"tool_name":"`+c.tool+`","tool_input":`+c.input+`}`), &out)
			want := `{"hookSpecificOutput":{"hookEventName":"PreToolUse","permissionDecision":"allow","permissionDecisionReason":"screened by jig"}}` + "\n"
			if out.String() != want {
				t.Fatalf("Hook(%s %s) = %q, want %q", c.tool, c.input, out.String(), want)
			}
		})
	}
}
