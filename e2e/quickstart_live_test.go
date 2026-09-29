package e2e

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"testing"

	"github.com/develdeco/jig/internal/claudetest"
)

// TestQuickstartLive runs README's Quickstart the way a new user does, with
// the jig binary under test and the real Claude Code CLI: it installs the
// session skills, initializes a standalone store beside a small Go repo,
// mints a ticket, writes its brief and slices as the intake skill would,
// validates them, and solves the ticket on the headless backend. Only the
// model is scripted (claudetest): the build session lands a test, and the
// gate's reviewer finds nothing, so the chain must end in a published
// branch. It is opt-in, like the headless contract test, since it needs
// `claude` on PATH: CI's claude-cli job runs it on every change, and the
// release workflows run it against the release's own binaries
// (JIG_E2E_BINARY). v0.1.1 failed here at its first dispatch.
func TestQuickstartLive(t *testing.T) {
	if os.Getenv("JIG_LIVE_CLAUDE") == "" {
		t.Skip("set JIG_LIVE_CLAUDE=1 to run the Quickstart against the local claude CLI")
	}
	if _, err := exec.LookPath("claude"); err != nil {
		t.Fatalf("JIG_LIVE_CLAUDE is set but claude is not on PATH: %v", err)
	}
	t.Setenv("JIG_HOME", t.TempDir())

	skills := t.TempDir()
	mustExitZero(t, runJig(t, skills, "skills", "install", "--project"), "jig skills install --project")
	if _, err := os.Stat(filepath.Join(skills, ".claude", "skills", "intake", "SKILL.md")); err != nil {
		t.Fatalf("jig skills install --project did not install the intake skill the Quickstart drafts with: %v", err)
	}

	ws := t.TempDir()
	repo := filepath.Join(ws, "demo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	writeFileOrFatal(t, filepath.Join(repo, "go.mod"), []byte("module example.com/demo\n\ngo 1.22\n"))
	writeFileOrFatal(t, filepath.Join(repo, "double.go"), []byte("package demo\n\n// Double returns twice n.\nfunc Double(n int) int { return n * 2 }\n"))
	gitLog(t, repo, "init", "-q", "-b", "main")
	gitLog(t, repo, "add", "-A")
	gitLog(t, repo, "commit", "-q", "-m", "initial commit")

	mustExitZero(t, runJig(t, repo, "init", "--standalone"), "jig init --standalone")
	store := filepath.Join(ws, "demo-tickets")
	r := runJig(t, repo, "ticket", "new", "--title", "Cover Double with a test")
	mustExitZero(t, r, "jig ticket new")
	if !strings.Contains(r.Stdout, "id: T-1") {
		t.Fatalf("jig ticket new minted something other than T-1:\n%s", r.Stdout)
	}

	ticket := filepath.Join(store, "T-1")
	writeFileOrFatal(t, filepath.Join(ticket, "brief.md"), []byte(quickstartBrief))
	slices := filepath.Join(ticket, "slices.yaml")
	writeFileOrFatal(t, slices, []byte(quickstartSlices("")))
	r = runJig(t, repo, "validate", "T-1")
	mustExitZero(t, r, "jig validate T-1 (to print the section hashes)")
	writeFileOrFatal(t, slices, []byte(quickstartSlices(sectionHash(t, r.Stdout, "Slice A - double test"))))
	r = runJig(t, repo, "validate", "T-1")
	mustExitZero(t, r, "jig validate T-1")
	if !strings.Contains(r.Stdout, "valid: yes") {
		t.Fatalf("jig validate T-1 did not print valid: yes:\n%s", r.Stdout)
	}

	var (
		mu              sync.Mutex
		builds, reviews []*claudetest.Session
	)
	claudetest.Serve(t, &claudetest.API{Route: func(prompt string) *claudetest.Session {
		mu.Lock()
		defer mu.Unlock()
		switch {
		case strings.Contains(prompt, "You are a jig build session"):
			s := quickstartBuild(t, prompt)
			builds = append(builds, s)
			return s
		case strings.Contains(prompt, "You are reviewing round"):
			s := quickstartReview(t, prompt)
			reviews = append(reviews, s)
			return s
		}
		return nil
	}})

	r = runJig(t, repo, "solve", "T-1", "--backend", "headless", "--yes")
	mu.Lock()
	defer mu.Unlock()
	if r.Code != 0 {
		t.Fatalf("jig solve T-1 --backend headless --yes exit = %d, want 0 (%d build, %d review sessions dispatched)\nstdout:\n%s\nstderr:\n%s", r.Code, len(builds), len(reviews), r.Stdout, r.Stderr)
	}
	if len(builds) != 1 || len(reviews) != 1 {
		t.Errorf("dispatched %d build and %d review sessions, want one of each\nstdout:\n%s", len(builds), len(reviews), r.Stdout)
	}
	for _, s := range append(builds, reviews...) {
		s.Check(t)
	}

	if test := gitLog(t, repo, "show", "jig/T-1:double_test.go"); !strings.Contains(test, "func TestDouble") {
		t.Errorf("published branch jig/T-1 carries a double_test.go without the session's TestDouble:\n%s", test)
	}
	if body, err := os.ReadFile(filepath.Join(ticket, "pr", "demo.md")); err != nil || len(body) == 0 {
		t.Errorf("publish wrote no PR body at T-1/pr/demo.md in the store (%d bytes, %v)", len(body), err)
	}
}

// mustExitZero fails t now unless r exited 0.
func mustExitZero(t *testing.T, r jigResult, what string) {
	t.Helper()
	if r.Code != 0 {
		t.Fatalf("%s exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", what, r.Code, r.Stdout, r.Stderr)
	}
}

// quickstartBrief is the ticket's brief.md, as the intake skill would draft
// it: each decision marked with its provenance.
const quickstartBrief = `# Cover Double with a test

## Goal

Add a test for ` + "`Double`" + ` so the module has a passing oracle. (user-confirmed)

## Slice A - double test

Write ` + "`TestDouble`" + ` covering zero, a positive and a negative input. (defaulted)
`

// quickstartSlices renders slices.yaml with fromBrief as slice a's brief
// section hash, or an empty list before validate has printed one.
func quickstartSlices(fromBrief string) string {
	from := "[]"
	if fromBrief != "" {
		from = `["` + fromBrief + `"]`
	}
	return "slices:\n  - id: a\n    workspace: root\n    goal: Add TestDouble.\n    oracle: test\n    blocked_by: []\n    from_brief: " + from + "\n"
}

// sectionHash finds heading's sha256 in the section table `jig validate`
// prints, for the intake skill's paste-and-rerun step.
func sectionHash(t *testing.T, validateOut, heading string) string {
	t.Helper()
	for _, line := range strings.Split(validateOut, "\n") {
		if h, ok := strings.CutPrefix(strings.TrimSpace(line), heading+","); ok {
			return strings.TrimSpace(h)
		}
	}
	t.Fatalf("jig validate printed no hash for section %q:\n%s", heading, validateOut)
	return ""
}

// quickstartTest is the test file the scripted build session lands.
const quickstartTest = `package demo

import "testing"

func TestDouble(t *testing.T) {
	for _, c := range []struct{ in, want int }{{0, 0}, {2, 4}, {-3, -6}} {
		if got := Double(c.in); got != c.want {
			t.Errorf("Double(%d) = %d, want %d", c.in, got, c.want)
		}
	}
}
`

var (
	resultPathInPrompt = regexp.MustCompile(`write result\.json at (.+?) with exactly one JSON object`)
	reviewPathInPrompt = regexp.MustCompile(`review\.json at (.+?)\.\r?\n`)
	commitSHA          = regexp.MustCompile(`[0-9a-f]{40}`)
)

// promptPath returns re's path in prompt, or "" (reported on t) when the
// prompt names none; Route runs on the server's goroutine, so this uses
// t.Errorf.
func promptPath(t *testing.T, re *regexp.Regexp, prompt string) string {
	m := re.FindStringSubmatch(prompt)
	if m == nil {
		t.Errorf("dispatch prompt does not match %s:\n%s", re, prompt)
		return ""
	}
	return m[1]
}

// quickstartBuild scripts slice a's build session from its prompt: find
// the lease, write the test into it, commit, and write result.json where
// the prompt says.
func quickstartBuild(t *testing.T, prompt string) *claudetest.Session {
	result := promptPath(t, resultPathInPrompt, prompt)
	return &claudetest.Session{Steps: []claudetest.Step{
		{Name: "find the lease", Call: claudetest.Bash("git rev-parse --show-toplevel")},
		{Name: "write the test", Call: func(prior []claudetest.ToolResult) claudetest.ToolCall {
			lease := filepath.FromSlash(firstLine(strings.TrimSpace(prior[0].Content)))
			return claudetest.ToolCall{Name: "Write", Input: map[string]any{"file_path": filepath.Join(lease, "double_test.go"), "content": quickstartTest}}
		}},
		{Name: "commit", Call: claudetest.Bash("git add double_test.go && git -c user.name=jig-e2e -c user.email=e2e@example.invalid commit -q -m 'test: cover Double' && git rev-parse HEAD")},
		{Name: "write result.json", Call: func(prior []claudetest.ToolResult) claudetest.ToolCall {
			sha := commitSHA.FindString(prior[2].Content)
			return claudetest.ToolCall{Name: "Write", Input: map[string]any{"file_path": result, "content": `{"outcome":"green","summary":"TestDouble covers zero, a positive and a negative input","commit":"` + sha + `"}`}}
		}},
	}}
}

// quickstartReview scripts the gate reviewer's session from its prompt:
// read review.json, then write a result with no findings that lists every
// path review.json says it must review.
func quickstartReview(t *testing.T, prompt string) *claudetest.Session {
	review := promptPath(t, reviewPathInPrompt, prompt)
	result := promptPath(t, resultPathInPrompt, prompt)
	return &claudetest.Session{Steps: []claudetest.Step{
		{Name: "read review.json", Call: claudetest.Bash("cat " + shellQuote(filepath.ToSlash(review))), WantOut: `"must_review"`},
		{Name: "write result.json", Call: func(prior []claudetest.ToolResult) claudetest.ToolCall {
			var req struct {
				MustReview []string `json:"must_review"`
			}
			if err := json.Unmarshal([]byte(prior[0].Content), &req); err != nil {
				t.Errorf("review.json as the session read it is not JSON: %v\n%s", err, prior[0].Content)
			}
			data, _ := json.Marshal(map[string]any{"findings": []any{}, "reviewed_paths": req.MustReview, "summary": "no findings"})
			return claudetest.ToolCall{Name: "Write", Input: map[string]any{"file_path": result, "content": string(data)}}
		}},
	}}
}

// shellQuote single-quotes s for the session's POSIX shell.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}
