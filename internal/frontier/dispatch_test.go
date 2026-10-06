package frontier

import (
	"strings"
	"testing"
)

// TestRenderDispatchPromptMatchesGolden pins the build dispatch prompt to a
// golden text transcribed here, not derived from dispatchPromptTemplate, so
// any change to what a builder is told shows up as a diff of this text.
func TestRenderDispatchPromptMatchesGolden(t *testing.T) {
	t.Parallel()

	got := renderDispatchPrompt("a", "JIG-1", "say hello", "go test ./alpha/...", "/abs/a.attempt-1.slice.json", "/abs/a.attempt-1.result.json", true)
	want := "You are a jig build session for slice a of ticket JIG-1.\n" +
		"Work ONLY in this worktree. Goal: say hello\n" +
		"Oracle (green = done): go test ./alpha/...\n" +
		"While you work, run only the tests that cover your change. When you report green, jig runs the oracle and hands you its output if it fails.\n" +
		"Read your inputs from slice.json at /abs/a.attempt-1.slice.json (brief sections by path, attempt log, prior answer, oracle_seconds: how long jig's last run of the oracle took on this ticket, 0 before the first, and earlier_slices: what this ticket's verified slices did and the files they changed).\n" +
		"Commit as you land. When finished write result.json at /abs/a.attempt-1.result.json with exactly one JSON object: " +
		`{"outcome": "green|code-bug|flawed-brief|oracle-wrong|blocked-by-env|needs-input|failed", "summary": "...", "commit": "<sha>", "question": "only for needs-input", "artifacts": ["relative paths"]}`
	if got != want {
		t.Errorf("prompt does not match the golden text.\ngot:\n%s\nwant:\n%s", got, want)
	}

	// A backend that cannot resume a session: a red oracle run fails the
	// attempt, so the builder runs the oracle once itself.
	got = renderDispatchPrompt("a", "JIG-1", "say hello", "go test ./alpha/...", "/abs/a.attempt-1.slice.json", "/abs/a.attempt-1.result.json", false)
	want = strings.Replace(want,
		"When you report green, jig runs the oracle and hands you its output if it fails.\n",
		"When you report green, jig runs the oracle, and a red run fails the attempt, so run it once yourself before you report green.\n", 1)
	if got != want {
		t.Errorf("prompt without fix turns does not match the golden text.\ngot:\n%s\nwant:\n%s", got, want)
	}
}

// TestRenderOracleFixPromptMatchesGolden pins the turn jig hands a builder's
// own session when its oracle run at a claimed green comes back red.
func TestRenderOracleFixPromptMatchesGolden(t *testing.T) {
	t.Parallel()

	got := renderOracleFixPrompt("go test ./alpha/...", 42, "--- FAIL: TestHello", "/abs/a.attempt-1.result.json")
	want := "jig ran the oracle `go test ./alpha/...` on your commit, and it failed after 42 s. Its output ends:\n" +
		"--- FAIL: TestHello\n" +
		"Fix the cause, commit, and write result.json at /abs/a.attempt-1.result.json again, as before."
	if got != want {
		t.Errorf("prompt does not match the golden text.\ngot:\n%s\nwant:\n%s", got, want)
	}
}

// TestRenderDirtyTreePromptMatchesGolden pins the next turn jig hands a
// builder's own session when it reports green with uncommitted changes.
func TestRenderDirtyTreePromptMatchesGolden(t *testing.T) {
	t.Parallel()

	got := renderDirtyTreePrompt("go test ./alpha/...", " M alpha/alpha.go", "/abs/a.attempt-1.result.json")
	want := "jig did not run the oracle `go test ./alpha/...`: your working tree has uncommitted changes to tracked files, so the commit you reported is not what a run would test:\n" +
		" M alpha/alpha.go\n" +
		"Commit or revert them, and write result.json at /abs/a.attempt-1.result.json again, as before."
	if got != want {
		t.Errorf("prompt does not match the golden text.\ngot:\n%s\nwant:\n%s", got, want)
	}
}
