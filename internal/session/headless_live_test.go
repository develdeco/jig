package session

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/develdeco/jig/internal/claudetest"
	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/outcome"
)

// TestHeadlessLiveCLI is the headless backend's contract test against the
// real Claude Code CLI: the local `claude` binary runs a scripted session
// through jig's generated settings, the CLI's own permission system, and
// the real `jig _screen` hook, with a mock Messages API on loopback
// (claudetest) standing in for the model - no credentials and no network.
// It is opt-in, since its result depends on whichever CLI version is
// installed:
//
//	JIG_LIVE_CLAUDE=1 go test ./internal/session -run Live
//
// CI's claude-cli job runs it against the latest CLI release.
//
// Each scripted tool call probes one grant or one denial of the permission
// model (docs/adr/0008-headless-permission-model.md), and the session ends
// by writing result.json exactly as a real one must.
func TestHeadlessLiveCLI(t *testing.T) {
	if os.Getenv("JIG_LIVE_CLAUDE") == "" {
		t.Skip("set JIG_LIVE_CLAUDE=1 to run the headless contract test against the local claude CLI")
	}
	if _, err := exec.LookPath("claude"); err != nil {
		t.Fatalf("JIG_LIVE_CLAUDE is set but claude is not on PATH: %v", err)
	}
	jig := filepath.Join(buildBinary(t, filepath.Join("cmd", "jig"), "jig"), "jig")
	if runtime.GOOS == "windows" {
		jig += ".exe"
	}

	t.Run("build", func(t *testing.T) { liveBuildSession(t, jig) })
	t.Run("reviewer", func(t *testing.T) { liveReviewerSession(t, jig) })
}

// liveBuildSession runs a build-shaped dispatch: the screen denies a push,
// the shell carries jig's command timeout, reads outside the lease are granted, edits are granted in the lease and
// on the result file only, and a commit lands with nothing but the
// session's own change in it.
func liveBuildSession(t *testing.T, jig string) {
	root := t.TempDir()
	worktree := filepath.Join(root, "lease")
	work := filepath.Join(root, "store", "T-1", "work")
	outside := filepath.Join(root, "outside")
	for _, dir := range []string{worktree, work, outside} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	liveRepo(t, worktree)

	d := Dispatch{
		Ticket:     "T-1",
		Slice:      "a",
		Attempt:    1,
		Worktree:   worktree,
		SliceJSON:  filepath.Join(work, "a.attempt-1.slice.json"),
		ResultJSON: filepath.Join(work, "a.attempt-1.result.json"),
		Model:      "claude-haiku-4-5",
		Prompt:     "You are a jig build session for slice a of ticket T-1.",
		Screen:     true,
	}
	if err := os.WriteFile(d.SliceJSON, []byte(`{"id":"a","goal":"say hello"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	// The model writes through the paths its session is given, spelled as
	// sessionView spells them, as a real session reads them from its prompt
	// and its working directory; the dispatch keeps the temp dir's own
	// spelling, which on a CI runner goes through an 8.3 short name. The
	// writes that must be denied are spelled long too, so that the rules
	// are what denies them, not the CLI's refusal of any short spelling.
	view := sessionView(d)
	sibling := filepath.Join(filepath.Dir(view.ResultJSON), "a.attempt-1.other.json")
	outsideFile := filepath.Join(longPath(outside), "x.txt")

	sess := &claudetest.Session{Steps: []claudetest.Step{
		{Name: "screen denies a push", Call: claudetest.Bash("git push origin HEAD"), WantErr: "Blocked `git push`"},
		{Name: "the shell waits 30 minutes on a command and runs nothing in the background", Call: claudetest.Bash(`echo "default=$BASH_DEFAULT_TIMEOUT_MS max=$BASH_MAX_TIMEOUT_MS nobg=$CLAUDE_CODE_DISABLE_BACKGROUND_TASKS"`), WantOut: "default=1800000 max=1800000 nobg=1"},
		{Name: "read slice.json outside the lease", Call: claudetest.Tool("Read", map[string]any{"file_path": d.SliceJSON}), WantOut: `"goal":"say hello"`},
		{Name: "write outside the lease", Call: claudetest.Write(outsideFile, "x"), WantDenied: true},
		{Name: "write in the lease", Call: claudetest.Write(filepath.Join(view.Worktree, "hello.txt"), "hello\n")},
		{Name: "write a sibling of result.json", Call: claudetest.Write(sibling, "x"), WantDenied: true},
		{Name: "commit", Call: claudetest.Bash("git add -A && git -c user.name=jig-test -c user.email=test@example.invalid commit -q -m hello && git rev-parse HEAD")},
		{Name: "write result.json", Call: func(prior []claudetest.ToolResult) claudetest.ToolCall {
			sha := regexp.MustCompile(`[0-9a-f]{40}`).FindString(prior[6].Content)
			return claudetest.ToolCall{Name: "Write", Input: map[string]any{"file_path": view.ResultJSON, "content": `{"outcome":"green","summary":"live contract","commit":"` + sha + `"}`}}
		}},
	}}
	runLive(t, jig, sess, d)

	for _, path := range []string{outsideFile, sibling} {
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Errorf("%s exists after a denied write (stat: %v)", path, err)
		}
	}
	data, err := os.ReadFile(d.ResultJSON)
	if err != nil {
		t.Fatalf("read result.json: %v", err)
	}
	res := outcome.ParseJSON("slice", data)
	head, err := gitx.RevParse(worktree, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if res.Outcome != outcome.Green || res.Commit != head {
		t.Errorf("result = %+v, want green at the lease HEAD %s", res, head)
	}
	files, err := gitx.Run(worktree, "show", "--name-only", "--format=", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(files) != "hello.txt" {
		t.Errorf("the session's commit holds %q, want only hello.txt: no dispatch plumbing belongs in the lease", files)
	}
	if status, err := gitx.Run(worktree, "status", "--porcelain"); err != nil || status != "" {
		t.Errorf("lease not clean after the session: %q (%v)", status, err)
	}
}

// liveReviewerSession runs a gate-reviewer-shaped dispatch (Slice "gate",
// review.json as its input): it reads review.json from the store, reads the
// diff through git, and writes its result.json.
func liveReviewerSession(t *testing.T, jig string) {
	root := t.TempDir()
	worktree := filepath.Join(root, "gate-lease")
	work := filepath.Join(root, "store", "T-1", "work")
	for _, dir := range []string{worktree, work} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	base := liveRepo(t, worktree)
	if err := os.WriteFile(filepath.Join(worktree, "a.go"), []byte("package a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := gitx.Run(worktree, "add", "-A"); err != nil {
		t.Fatal(err)
	}
	if _, err := gitx.RunEnv(worktree, liveIdentity, "commit", "-q", "-m", "change"); err != nil {
		t.Fatal(err)
	}
	head, err := gitx.RevParse(worktree, "HEAD")
	if err != nil {
		t.Fatal(err)
	}

	d := Dispatch{
		Ticket:     "T-1",
		Slice:      "gate",
		Attempt:    1,
		Worktree:   worktree,
		SliceJSON:  filepath.Join(work, "gate.round-1.review.json"),
		ResultJSON: filepath.Join(work, "gate.round-1.result.json"),
		Model:      "claude-haiku-4-5",
		Prompt:     "You are a jig gate reviewer for round 1 of ticket T-1.",
		Screen:     true,
	}
	if err := os.WriteFile(d.SliceJSON, []byte(`{"ticket":"T-1","round":1,"scope":"full"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	review := `{"verdict":"clean","findings":[],"closures":[],"summary":"live contract"}`
	view := sessionView(d)

	sess := &claudetest.Session{Steps: []claudetest.Step{
		{Name: "read review.json", Call: claudetest.Tool("Read", map[string]any{"file_path": d.SliceJSON}), WantOut: `"scope":"full"`},
		{Name: "read the diff", Call: claudetest.Bash("git diff --stat " + base + ".." + head), WantOut: "a.go"},
		{Name: "write result.json", Call: claudetest.Write(view.ResultJSON, review)},
	}}
	runLive(t, jig, sess, d)

	if got, err := os.ReadFile(d.ResultJSON); err != nil || string(got) != review {
		t.Errorf("result.json = %q (%v), want %q", got, err, review)
	}
	if after, err := gitx.RevParse(worktree, "HEAD"); err != nil || after != head {
		t.Errorf("gate lease HEAD moved to %s (%v), want %s", after, err, head)
	}
}

// liveIdentity pins the identity of the commits the test itself makes.
var liveIdentity = []string{
	"GIT_AUTHOR_NAME=jig-test", "GIT_AUTHOR_EMAIL=test@example.invalid",
	"GIT_COMMITTER_NAME=jig-test", "GIT_COMMITTER_EMAIL=test@example.invalid",
}

// liveRepo initializes dir as a git repo with one empty commit and returns
// that commit's sha.
func liveRepo(t *testing.T, dir string) string {
	t.Helper()
	if _, err := gitx.Run(dir, "init", "-q", "-b", "main"); err != nil {
		t.Fatal(err)
	}
	if _, err := gitx.RunEnv(dir, liveIdentity, "commit", "-q", "--allow-empty", "-m", "base"); err != nil {
		t.Fatal(err)
	}
	sha, err := gitx.RevParse(dir, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	return sha
}

// runLive points the CLI at a mock Messages API scripted with sess, runs d
// through the headless backend with jig as the screen hook, and checks
// every scripted step ran with the expected outcome. It stops the test
// only when the session ran fewer steps than scripted; a step with the
// wrong outcome is reported and the caller's own checks on disk still run.
func runLive(t *testing.T, jig string, sess *claudetest.Session, d Dispatch) {
	t.Helper()
	claudetest.Serve(t, &claudetest.API{Route: func(string) *claudetest.Session { return sess }})

	backend, err := New("headless", Options{ScreenBinary: jig})
	if err != nil {
		t.Fatalf("New(headless): %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- backend.Run(d) }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(5 * time.Minute):
		t.Fatal("claude did not finish within 5 minutes")
	}

	sess.Check(t)
	if len(sess.Results()) < len(sess.Steps) {
		t.FailNow()
	}
}
