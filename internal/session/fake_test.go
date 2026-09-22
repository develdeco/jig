package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/outcome"
)

// newWorktree creates a tiny git repo at t.TempDir() with a pinned local
// identity and one seed commit, standing in for a pool lease.
func newWorktree(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		if _, err := gitx.Run(dir, args...); err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
	}
	run("init", "-b", "main")
	run("config", "user.name", "jig-fixture")
	run("config", "user.email", "fixture@example.invalid")
	if err := os.WriteFile(filepath.Join(dir, "seed.txt"), []byte("seed\n"), 0o644); err != nil {
		t.Fatalf("write seed.txt: %v", err)
	}
	run("add", "-A")
	run("commit", "-m", "seed")
	return dir
}

// addFilePatch is a unified diff adding hello.txt with the given content,
// in the format `git apply` accepts for a brand-new file.
func addFilePatch(content string) string {
	return "diff --git a/hello.txt b/hello.txt\n" +
		"new file mode 100644\n" +
		"index 0000000..3b18e51\n" +
		"--- /dev/null\n" +
		"+++ b/hello.txt\n" +
		"@@ -0,0 +1 @@\n" +
		"+" + content + "\n"
}

func writeScenarioAttempt(t *testing.T, scenarioDir, slice string, attempt int, patch string, result map[string]any) {
	t.Helper()
	dir := filepath.Join(scenarioDir, "slices", slice, "attempt-"+itoa(attempt))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir attempt dir: %v", err)
	}
	if patch != "" {
		if err := os.WriteFile(filepath.Join(dir, "patch.diff"), []byte(patch), 0o644); err != nil {
			t.Fatalf("write patch.diff: %v", err)
		}
	}
	data, err := json.Marshal(result)
	if err != nil {
		t.Fatalf("marshal result.json: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "result.json"), data, 0o644); err != nil {
		t.Fatalf("write result.json: %v", err)
	}
}

func itoa(n int) string {
	return string(rune('0' + n))
}

func TestFakeBackendAppliesPatchAndCommits(t *testing.T) {
	worktree := newWorktree(t)
	scenarioDir := t.TempDir()
	writeScenarioAttempt(t, scenarioDir, "a", 1, addFilePatch("hello world"), map[string]any{
		"outcome": "green",
		"summary": "add hello file\nmore detail on a second line",
		"commit":  "@HEAD",
	})

	backend := newFakeBackend(Options{ScenarioDir: scenarioDir})
	resultPath := filepath.Join(t.TempDir(), "result.json")
	d := Dispatch{Ticket: "JIG-1", Slice: "a", Attempt: 1, Worktree: worktree, ResultJSON: resultPath}

	if err := backend.Run(d); err != nil {
		t.Fatalf("Run: %v", err)
	}

	// The patch's file appeared.
	if _, err := os.Stat(filepath.Join(worktree, "hello.txt")); err != nil {
		t.Fatalf("hello.txt not created: %v", err)
	}

	// A commit was created with the expected message shape.
	subject, err := gitx.Run(worktree, "log", "-1", "--format=%s")
	if err != nil {
		t.Fatalf("git log: %v", err)
	}
	wantSubject := "JIG-1 a: add hello file"
	if subject != wantSubject {
		t.Errorf("commit subject = %q, want %q", subject, wantSubject)
	}
	authorName, err := gitx.Run(worktree, "log", "-1", "--format=%an")
	if err != nil {
		t.Fatalf("git log author: %v", err)
	}
	if authorName != "jig-fixture" {
		t.Errorf("commit author = %q, want jig-fixture", authorName)
	}

	// ResultJSON was written with the real HEAD sha substituted for @HEAD.
	head, err := gitx.RevParse(worktree, "HEAD")
	if err != nil {
		t.Fatalf("rev-parse HEAD: %v", err)
	}
	data, err := os.ReadFile(resultPath)
	if err != nil {
		t.Fatalf("read result.json: %v", err)
	}
	res := outcome.ParseJSON("slice", data)
	if res.Outcome != outcome.Green {
		t.Errorf("outcome = %q, want green", res.Outcome)
	}
	if res.Commit != head {
		t.Errorf("commit = %q, want real HEAD sha %q", res.Commit, head)
	}
}

func TestFakeBackendMissingAttemptDir(t *testing.T) {
	worktree := newWorktree(t)
	scenarioDir := t.TempDir() // no slices/ at all

	backend := newFakeBackend(Options{ScenarioDir: scenarioDir})
	resultPath := filepath.Join(t.TempDir(), "result.json")
	d := Dispatch{Ticket: "JIG-1", Slice: "b", Attempt: 3, Worktree: worktree, ResultJSON: resultPath}

	if err := backend.Run(d); err != nil {
		t.Fatalf("Run: %v", err)
	}

	data, err := os.ReadFile(resultPath)
	if err != nil {
		t.Fatalf("read result.json: %v", err)
	}
	res := outcome.ParseJSON("slice", data)
	if res.Outcome != outcome.Failed {
		t.Errorf("outcome = %q, want failed", res.Outcome)
	}
	want := "scenario has no attempt 3 for slice b"
	if res.Summary != want {
		t.Errorf("summary = %q, want %q", res.Summary, want)
	}
}

func TestFakeBackendNeedsInputPassesThrough(t *testing.T) {
	worktree := newWorktree(t)
	before, err := gitx.Run(worktree, "log", "--format=%H")
	if err != nil {
		t.Fatalf("git log: %v", err)
	}

	scenarioDir := t.TempDir()
	writeScenarioAttempt(t, scenarioDir, "c", 1, "", map[string]any{
		"outcome":  "needs-input",
		"summary":  "need direction on tone",
		"question": "Formal or casual greeting?",
	})

	backend := newFakeBackend(Options{ScenarioDir: scenarioDir})
	resultPath := filepath.Join(t.TempDir(), "result.json")
	d := Dispatch{Ticket: "JIG-1", Slice: "c", Attempt: 1, Worktree: worktree, ResultJSON: resultPath}

	if err := backend.Run(d); err != nil {
		t.Fatalf("Run: %v", err)
	}

	after, err := gitx.Run(worktree, "log", "--format=%H")
	if err != nil {
		t.Fatalf("git log: %v", err)
	}
	if before != after {
		t.Errorf("worktree history changed for a needs-input attempt: before %q after %q", before, after)
	}

	data, err := os.ReadFile(resultPath)
	if err != nil {
		t.Fatalf("read result.json: %v", err)
	}
	res := outcome.ParseJSON("slice", data)
	if res.Outcome != outcome.NeedsInput {
		t.Errorf("outcome = %q, want needs-input", res.Outcome)
	}
	if res.Question != "Formal or casual greeting?" {
		t.Errorf("question = %q, want preserved verbatim", res.Question)
	}
}

// TestFakeBackendGatePlayback checks the gate-review dispatch path: it
// copies the scenario's review-result.json verbatim into ResultJSON and
// never touches the worktree.
func TestFakeBackendGatePlayback(t *testing.T) {
	worktree := newWorktree(t)
	before, err := gitx.Run(worktree, "log", "--format=%H")
	if err != nil {
		t.Fatalf("git log: %v", err)
	}

	scenarioDir := t.TempDir()
	roundDir := filepath.Join(scenarioDir, "gate", "round-1")
	if err := os.MkdirAll(roundDir, 0o755); err != nil {
		t.Fatalf("mkdir round dir: %v", err)
	}
	want := []byte(`{"findings":[{"file":"billing/invoices.go","line":42,"title":"x","detail":"y","action":"fix","risk":"high","risk_rationale":"z","oracle":"test"}],"reviewed_paths":["billing/invoices.go"],"summary":"s"}`)
	if err := os.WriteFile(filepath.Join(roundDir, "review-result.json"), want, 0o644); err != nil {
		t.Fatalf("write review-result.json: %v", err)
	}

	backend := newFakeBackend(Options{ScenarioDir: scenarioDir})
	resultPath := filepath.Join(t.TempDir(), "result.json")
	d := Dispatch{Ticket: "JIG-1", Slice: "gate", Attempt: 1, Worktree: worktree, ResultJSON: resultPath}
	if err := backend.Run(d); err != nil {
		t.Fatalf("Run: %v", err)
	}

	got, err := os.ReadFile(resultPath)
	if err != nil {
		t.Fatalf("read result.json: %v", err)
	}
	if string(got) != string(want) {
		t.Errorf("result.json = %s, want verbatim %s", got, want)
	}

	after, err := gitx.Run(worktree, "log", "--format=%H")
	if err != nil {
		t.Fatalf("git log: %v", err)
	}
	if before != after {
		t.Errorf("worktree history changed by a gate dispatch: before %q after %q", before, after)
	}
}

// TestFakeBackendGateMissingRoundErrors checks that a gate round the
// scenario has no coverage for fails loudly instead of a silent clean.
func TestFakeBackendGateMissingRoundErrors(t *testing.T) {
	worktree := newWorktree(t)
	scenarioDir := t.TempDir() // no gate/ at all

	backend := newFakeBackend(Options{ScenarioDir: scenarioDir})
	resultPath := filepath.Join(t.TempDir(), "result.json")
	d := Dispatch{Ticket: "JIG-1", Slice: "gate", Attempt: 2, Worktree: worktree, ResultJSON: resultPath}
	err := backend.Run(d)
	if err == nil {
		t.Fatal("Run: expected an error for a missing gate round, got nil")
	}
	if !strings.Contains(err.Error(), "scenario has no gate round 2 review-result.json") {
		t.Errorf("error = %v, want it to name the missing gate round", err)
	}
	if _, statErr := os.Stat(resultPath); statErr == nil {
		t.Error("ResultJSON was written despite the error")
	}
}
