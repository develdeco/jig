package revieweval

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/gittest"
	"github.com/develdeco/jig/internal/session"
)

// buildClaudeStub compiles testdata/fixture/claudestub (repo root) - the
// same fake `claude` internal/session's own hermetic tests drive - into a
// temp dir and returns that dir, ready to prepend to PATH so it is found
// under its own, unremarkable name: nothing about this stub, or the dir it
// builds into, names this package or any case - "jig-claude-stub", not
// "jig-revieweval-bin", so PATH itself carries no leak-vocabulary token
// once this dir is prepended to it (the real-child leak test below scans
// PATH along with everything else in the child's env). Built once per
// call; the one test below that needs it calls this once.
func buildClaudeStub(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "jig-claude-stub")
	if err != nil {
		t.Fatalf("revieweval: create build dir: %v", err)
	}
	gittest.AtExit(func() { _ = os.RemoveAll(dir) })
	name := "claude"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	goBin := filepath.Join(runtime.GOROOT(), "bin", "go")
	if runtime.GOOS == "windows" {
		goBin += ".exe"
	}
	out := filepath.Join(dir, name)
	cmd := exec.Command(goBin, "build", "-buildvcs=false", "-o", out, filepath.Join(fixture.RepoRoot(t), "testdata", "fixture", "claudestub"))
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("revieweval: build claude stub: %v\n%s", err, output)
	}
	return dir
}

// realChildCall is one line of the claude stub's own log: its argv and cwd
// (as internal/session's own tests already read), plus what this test
// needs beyond that - its full environment and the names of its working
// directory's parent's own entries - exactly what a live reviewer or judge
// session would see.
type realChildCall struct {
	Argv          []string `json:"argv"`
	Cwd           string   `json:"cwd"`
	Env           []string `json:"env"`
	ParentEntries []string `json:"parent_entries"`
}

// readRealChildLog parses the claude stub's own log, one call per line.
func readRealChildLog(t *testing.T, path string) []realChildCall {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read claude stub log: %v", err)
	}
	var calls []realChildCall
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		if line == "" {
			continue
		}
		var c realChildCall
		if err := json.Unmarshal([]byte(line), &c); err != nil {
			t.Fatalf("parse claude stub log line %q: %v", line, err)
		}
		calls = append(calls, c)
	}
	return calls
}

// realChildScriptedBackend runs the real headless backend first - a real
// `claude` subprocess (the stub), in the dispatch's own worktree, with
// Options.Env exactly as the live path builds it - so the stub's own log
// records exactly what a live child sees. The real backend's own result is
// then ignored (the stub writes no content the reviewer/judge wire format
// would accept) and replaced with this round's scripted one - the same
// perfect-fixture/all-Same shape leakCapturingBackend (leak_test.go) plays
// back - so the case still scores as designed. worktrees records
// Dispatch.Worktree in call order, one entry per Run, so a test can match
// each logged call back to the worktree jig actually gave it.
type realChildScriptedBackend struct {
	real       session.Backend
	fixtureDir string
	worktrees  *[]string
}

func (b realChildScriptedBackend) Run(d session.Dispatch) error {
	*b.worktrees = append(*b.worktrees, d.Worktree)
	// The real backend's own error is expected and ignored: the stub never
	// writes a result the wire format would accept. What this call is for
	// is entirely the side effect the stub's own log records.
	_ = b.real.Run(d)

	switch d.Slice {
	case "gate":
		caseName, err := caseNameForRunID(b.fixtureDir, d.Ticket)
		if err != nil {
			return err
		}
		path := filepath.Join(b.fixtureDir, caseName, fmt.Sprintf("round-%d.json", d.Attempt))
		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("realChildScriptedBackend: no scripted result at %s: %w", path, err)
		}
		return os.WriteFile(d.ResultJSON, data, 0o644)
	case "judge":
		sliceData, err := os.ReadFile(d.SliceJSON)
		if err != nil {
			return fmt.Errorf("realChildScriptedBackend: read %s: %w", d.SliceJSON, err)
		}
		var jf judgeFileJSON
		if err := json.Unmarshal(sliceData, &jf); err != nil {
			return fmt.Errorf("realChildScriptedBackend: parse judge.json: %w", err)
		}
		verdicts := leakVerdictsFile{Verdicts: make([]leakVerdictEntry, len(jf.Candidates))}
		for i := range verdicts.Verdicts {
			verdicts.Verdicts[i] = leakVerdictEntry{Candidate: i, Same: true}
		}
		out, err := json.Marshal(verdicts)
		if err != nil {
			return fmt.Errorf("realChildScriptedBackend: marshal verdicts.json: %w", err)
		}
		return os.WriteFile(d.ResultJSON, out, 0o644)
	default:
		return fmt.Errorf("realChildScriptedBackend: unexpected slice %q", d.Slice)
	}
}

// harnessChanges returns every entry of now that launch does not hold
// verbatim: a variable the test harness added, or one it changed, after
// the binary started. The real-child test feeds all of it to the scrub,
// PATH included: the live path passes the test binary's whole
// environment on, and a harness PATH lands after the test's own constant
// PATH, where os/exec keeps the last duplicate, so the exact comparison
// fails on it.
func harnessChanges(launch, now []string) []string {
	atLaunch := make(map[string]bool, len(launch))
	for _, kv := range launch {
		atLaunch[kv] = true
	}
	var out []string
	for _, kv := range now {
		if !atLaunch[kv] {
			out = append(out, kv)
		}
	}
	return out
}

// sameEnvName compares two environment variable names the way the host
// does: case-insensitively on Windows, exactly elsewhere.
func sameEnvName(a, b string) bool {
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}

// envValue returns name's value in env, "" when it is absent.
func envValue(env []string, name string) string {
	for _, kv := range env {
		if n, v, ok := strings.Cut(kv, "="); ok && sameEnvName(n, name) {
			return v
		}
	}
	return ""
}

// hasEnvName reports whether env holds a variable called name.
func hasEnvName(env []string, name string) bool {
	for _, kv := range env {
		if n, _, _ := strings.Cut(kv, "="); sameEnvName(n, name) {
			return true
		}
	}
	return false
}

func sameDirOrFatal(t *testing.T, a, b string) bool {
	t.Helper()
	ai, err := os.Stat(a)
	if err != nil {
		return false
	}
	bi, err := os.Stat(b)
	if err != nil {
		return false
	}
	return os.SameFile(ai, bi)
}

// TestRunCaseRealChildSeesNoLeak drives one real multi-round case (reviewer
// and judge dispatches in both rounds - forgotten-finding's own gold match
// in round 1 and round 2 both give the judge at least one candidate)
// through the real headless backend: a real `claude` subprocess, in its
// own worktree, with Options.Env built by dispatchEnv, as the live path
// builds it. It checks every call the stub logged against what a live
// session could actually read: its own environment (leakCapturingBackend,
// leak_test.go, never execs a real child and so never exercises env
// inheritance at all), its working directory and the directory beside its
// worktree.
//
// The parent environment dispatchEnv filters is synthetic, not this test
// process's own: one value of every kind the live path must drop, beside
// the one knob the stub itself reads, plus every variable the test harness
// added or changed after launch, which the live path would pass on too.
// The child's environment is then compared value by value with what it
// must be - the synthetic PATH, the stub's knob, PWD on the worktree, and
// on Windows the SYSTEMROOT os/exec adds - so any variable the scrub lets
// through fails, whatever its value. Nothing here depends on the machine
// running the test: an ambient variable of the host (a CI runner's branch
// name, say) never enters the parent, and no value is judged by the words
// in it, so the host's temp root does not matter either.
func TestRunCaseRealChildSeesNoLeak(t *testing.T) {
	if launchEnv == nil {
		t.Fatal("launchEnv is nil: TestMain must snapshot os.Environ() first thing, before gittest.Run or any other harness setup")
	}
	// Taken before this test changes anything itself, so the difference
	// from the launch environment is exactly what the harness introduced.
	atStart := os.Environ()
	claudeDir := buildClaudeStub(t)
	// The backend finds the stub on this process's own PATH; the child's
	// PATH is whatever the synthetic parent environment below says.
	t.Setenv("PATH", claudeDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	logFile := filepath.Join(t.TempDir(), "claude.log")
	// Also on this process's own environment: if the backend ever ignored
	// Options.Env, the child would inherit everything here, still log, and
	// fail below on each variable it should never have had.
	t.Setenv("CLAUDE_STUB_LOG", logFile)

	// None of these paths has to exist; the child only ever sees them as
	// strings.
	pkgDir := filepath.FromSlash("/work/internal/revieweval")
	childPath := filepath.FromSlash("/nonexistent/bin")

	// planted holds one entry of every kind dispatchEnv must drop. PWD and
	// OLDPWD are pinned where they are dropped
	// (dispatchEnv's and childEnv's own tests): here the backend's own PWD
	// replaces the planted one before the child starts, so this test only
	// sees the combined result, which is that PWD is the worktree.
	planted := []string{
		"JIG_REVIEWEVAL_BACKEND=headless",
		"GIT_CONFIG_GLOBAL=" + filepath.Join(pkgDir, "revieweval-gittest", "gitconfig"),
		"GIT_CONFIG_NOSYSTEM=revieweval",
		"PWD=" + pkgDir,
		"OLDPWD=" + pkgDir,
		"=C:=" + pkgDir,
		"_=" + filepath.Join(pkgDir, "revieweval.test"),
		"GOCOVERDIR=" + filepath.Join(pkgDir, "revieweval-cover"),
	}
	if runtime.GOOS == "windows" {
		// Windows names are not case sensitive, so an owned variable may
		// arrive in any case; these prove the live wiring hands
		// dispatchEnv the host's own rule.
		planted = append(planted,
			"Jig_Revieweval_Backend=headless",
			"Git_Config_Global="+filepath.Join(pkgDir, "revieweval-gittest", "gitconfig"))
	}
	parentEnv := append([]string{
		"PATH=" + childPath,
		"CLAUDE_STUB_LOG=" + logFile,
	}, planted...)
	// Plus everything the test binary's harness added or changed after
	// launch (TestMain, gittest.Run, anything a later helper adds): the
	// live path passes the test binary's whole environment through
	// dispatchEnv, so each of these reaches a live session unless
	// dispatchEnv drops it. The operator's launch environment stays out: it
	// is ambient, outside what the eval scrubs.
	feed := harnessChanges(launchEnv, atStart)
	if !hasEnvName(feed, "GIT_CONFIG_GLOBAL") {
		t.Fatalf("the harness diff misses GIT_CONFIG_GLOBAL, which gittest.Run always sets after launch: %q", feed)
	}
	parentEnv = append(parentEnv, feed...)

	jigBin := buildJigBinary(t)
	real, err := liveBackend("headless", jigBin, parentEnv)
	if err != nil {
		t.Fatalf("liveBackend(headless): %v", err)
	}

	var worktrees []string
	backend := realChildScriptedBackend{real: real, fixtureDir: resultsDir("perfect"), worktrees: &worktrees}
	judge := &ModelJudge{Backend: backend, Model: "fixture-model"}

	c := loadEvalCase(t, "forgotten-finding")
	if len(c.Rounds) < 2 {
		t.Fatalf("test bug: forgotten-finding has %d rounds, want at least 2", len(c.Rounds))
	}

	workDir, err := os.MkdirTemp("", "jig-")
	if err != nil {
		t.Fatalf("create work dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(workDir) }()

	cs, err := RunCase(workDir, c, backend, judge, "fixture-model")
	if err != nil {
		t.Fatalf("RunCase: %v", err)
	}
	if !cs.Passed {
		t.Fatalf("RunCase: want the case to pass (perfect fixtures, a judge confirming everything Same): %+v", cs.Rounds)
	}

	if _, err := os.Stat(logFile); err != nil {
		t.Fatalf("the claude stub wrote no log at %s (%v): no child received CLAUDE_STUB_LOG, so either the stub never ran or the environment the backend gave it did not come from the parent environment passed to liveBackend", logFile, err)
	}
	calls := readRealChildLog(t, logFile)
	const wantCalls = 4 // round 1 gate + judge, round 2 gate + judge
	if len(calls) != wantCalls {
		t.Fatalf("claude stub ran %d times, want %d (reviewer and judge, rounds 1 and 2)", len(calls), wantCalls)
	}
	if len(worktrees) != len(calls) {
		t.Fatalf("test bug: recorded %d worktrees for %d calls", len(worktrees), len(calls))
	}

	// Exactly what the child may carry, at exactly these values: the
	// synthetic PATH, the stub's own knob, and on Windows the SYSTEMROOT
	// os/exec always adds when an explicit Env omits it. PWD is compared
	// with the worktree below.
	want := map[string]string{"PATH": childPath, "CLAUDE_STUB_LOG": logFile}
	if runtime.GOOS == "windows" {
		want["SYSTEMROOT"] = envValue(launchEnv, "SYSTEMROOT")
	}
	for i, call := range calls {
		for _, kv := range call.Env {
			name, val, _ := strings.Cut(kv, "=")
			if strings.EqualFold(name, "PWD") {
				continue
			}
			if w, ok := want[strings.ToUpper(name)]; !ok || w != val {
				t.Errorf("call %d: child env carries %q; want only PATH, CLAUDE_STUB_LOG and PWD (and SYSTEMROOT on Windows) at their exact values", i, kv)
			}
		}

		var pwd string
		var hasPWD bool
		for _, kv := range call.Env {
			if name, val, ok := strings.Cut(kv, "="); ok && strings.EqualFold(name, "PWD") {
				pwd, hasPWD = val, true
			}
		}
		if !hasPWD {
			t.Errorf("call %d: child env carries no PWD, want it set to the dispatch worktree %q", i, worktrees[i])
		} else if !sameDirOrFatal(t, pwd, worktrees[i]) {
			t.Errorf("call %d: PWD=%q, want the dispatch worktree %q", i, pwd, worktrees[i])
		}
		if !sameDirOrFatal(t, call.Cwd, worktrees[i]) {
			t.Errorf("call %d: cwd=%q, want the dispatch worktree %q", i, call.Cwd, worktrees[i])
		}

		parent := map[string]bool{}
		for _, e := range call.ParentEntries {
			parent[e] = true
		}
		if len(parent) != 2 || !parent["repo"] || !parent["store"] {
			t.Errorf("call %d: worktree's parent holds %v, want exactly [repo store]", i, call.ParentEntries)
		}
	}
}

// TestHarnessChangesIsTheVerbatimDiff pins what the real-child test
// feeds the scrub from the harness: every variable added or changed since
// launch, PATH included, and nothing unchanged or removed.
func TestHarnessChangesIsTheVerbatimDiff(t *testing.T) {
	list := func(entries ...string) string { return strings.Join(entries, string(os.PathListSeparator)) }
	launch := []string{"TMP=/t", "KEEP=1", "GONE=1", "PATH=" + list("/a", "/b")}
	now := []string{"TMP=/other", "KEEP=1", "NEW=x", "PATH=" + list("/revieweval-bin", "/a", "/b")}
	got := strings.Join(harnessChanges(launch, now), ",")
	want := "TMP=/other,NEW=x,PATH=" + list("/revieweval-bin", "/a", "/b")
	if got != want {
		t.Errorf("harnessChanges = %q, want %q: the changed TMP, the new NEW and the changed PATH, never KEEP or GONE", got, want)
	}
}
