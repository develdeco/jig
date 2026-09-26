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

// harnessParentEnv splits what the test harness introduced after launch -
// every entry of now that is not in launch verbatim, a variable it added
// or changed - into what the real-child test feeds the scrub and the PATH
// entries the harness added. PATH is the one list the host fills, so it is
// never fed whole: only the entries missing from the launch PATH come
// back, for the caller to scan, while the child keeps its own PATH.
func harnessParentEnv(launch, now []string) (feed, addedPath []string) {
	atLaunch := make(map[string]bool, len(launch))
	for _, kv := range launch {
		atLaunch[kv] = true
	}
	for _, kv := range now {
		if atLaunch[kv] {
			continue
		}
		if name, _, _ := strings.Cut(kv, "="); sameEnvName(name, "PATH") {
			addedPath = addedPathEntries(envValue(launch, "PATH"), envValue(now, "PATH"))
			continue
		}
		feed = append(feed, kv)
	}
	return feed, addedPath
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

// addedPathEntries returns the entries of the PATH list now that the PATH
// list launch does not hold.
func addedPathEntries(launch, now string) []string {
	had := map[string]bool{}
	for _, e := range filepath.SplitList(launch) {
		had[e] = true
	}
	var out []string
	for _, e := range filepath.SplitList(now) {
		if e != "" && !had[e] {
			out = append(out, e)
		}
	}
	return out
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
// process's own: one value of every kind the live path must drop, each
// spelled so it would also trip the leak vocabulary if it got through,
// beside the one knob the stub itself reads. That keeps the check strict -
// every value the child sees is scanned - and independent of the host: an
// ambient variable of the machine running the test (a CI runner's branch
// name, say) is the operator's own environment, which the eval passes
// through by design and does not claim to scrub. What the test harness
// itself adds after launch is not ambient, and is checked here as the live
// path would pass it. The temp root every path here sits under is ambient
// like the operator's variables: the test runs under a deliberately
// hostile one (useHostileTempRoot), and the leak check leaves it out.
func TestRunCaseRealChildSeesNoLeak(t *testing.T) {
	// Taken before this test changes anything itself, so the difference
	// from the launch environment is exactly what the harness introduced.
	atStart := os.Environ()
	useHostileTempRoot(t)
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
	// strings. The PATH is a constant outside every temp root, so the one
	// value this test chooses for the child depends on nothing about the
	// host.
	pkgDir := filepath.FromSlash("/work/internal/revieweval")

	// planted holds one entry of every kind dispatchEnv must drop, each
	// value naming the eval so a leak of it would also trip the
	// vocabulary. PWD and OLDPWD are pinned where they are dropped
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
		"PATH=" + filepath.FromSlash("/nonexistent/bin"),
		"CLAUDE_STUB_LOG=" + logFile,
	}, planted...)
	// Plus everything the test binary's harness introduced after launch
	// (TestMain, gittest.Run, anything a later helper adds): the live path
	// passes the test binary's whole environment through dispatchEnv, so
	// each of these reaches a live session unless dispatchEnv drops it.
	// The operator's launch environment stays out: it is ambient, outside
	// what the eval scrubs. PATH is the one list the host fills, so for it
	// only the entries the harness added are checked, here, while the
	// child keeps the constant PATH above.
	feed, addedPath := harnessParentEnv(launchEnv, atStart)
	if !hasEnvName(feed, "GIT_CONFIG_GLOBAL") {
		t.Fatalf("the harness diff misses GIT_CONFIG_GLOBAL, which gittest.Run always sets after launch: %q", feed)
	}
	for _, seg := range addedPath {
		for _, h := range leakHits("PATH entry the harness added "+seg, "", seg) {
			t.Error(h)
		}
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

	calls := readRealChildLog(t, logFile)
	const wantCalls = 4 // round 1 gate + judge, round 2 gate + judge
	if len(calls) != wantCalls {
		t.Fatalf("claude stub ran %d times, want %d (reviewer and judge, rounds 1 and 2)", len(calls), wantCalls)
	}
	if len(worktrees) != len(calls) {
		t.Fatalf("test bug: recorded %d worktrees for %d calls", len(worktrees), len(calls))
	}

	// What the child may carry: the synthetic PATH, the stub's own knob,
	// the PWD the backend sets, and on Windows the SYSTEMROOT os/exec
	// always adds when an explicit Env omits it.
	allowed := map[string]bool{"PATH": true, "CLAUDE_STUB_LOG": true, "PWD": true}
	if runtime.GOOS == "windows" {
		allowed["SYSTEMROOT"] = true
	}
	for i, call := range calls {
		for _, kv := range call.Env {
			name, _, _ := strings.Cut(kv, "=")
			if !allowed[strings.ToUpper(name)] {
				t.Errorf("call %d: child env carries %q, which the parent environment's scrub should have dropped or never had", i, kv)
			}
			if strings.HasPrefix(name, "CLAUDE_STUB_") {
				continue // the stub's own knob, test scaffolding for itself
			}
			for _, h := range leakHits(fmt.Sprintf("call %d: child env variable %q", i, kv), "", kv) {
				t.Error(h)
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

// TestHarnessParentEnvFeedsChangesAndReturnsAddedPath pins how what the
// harness introduced is split: a changed or added variable is fed to the
// scrub, an unchanged or removed one is not, PATH is never fed whole, and
// only the PATH entries the harness added come back.
func TestHarnessParentEnvFeedsChangesAndReturnsAddedPath(t *testing.T) {
	list := func(entries ...string) string { return strings.Join(entries, string(os.PathListSeparator)) }
	launch := []string{"TMP=/t", "KEEP=1", "GONE=1", "PATH=" + list("/a", "/b")}

	t.Run("changed-and-added-variables-are-fed", func(t *testing.T) {
		feed, added := harnessParentEnv(launch, []string{"TMP=/other", "KEEP=1", "NEW=x", "PATH=" + list("/a", "/b")})
		if strings.Join(feed, ",") != "TMP=/other,NEW=x" {
			t.Errorf("feed = %q, want the changed TMP and the new NEW only", feed)
		}
		if len(added) != 0 {
			t.Errorf("added PATH entries = %q, want none", added)
		}
	})
	t.Run("an-added-path-entry-comes-back-and-path-is-never-fed", func(t *testing.T) {
		feed, added := harnessParentEnv(launch, []string{"TMP=/t", "KEEP=1", "PATH=" + list("/revieweval-bin", "/a", "/b")})
		if len(feed) != 0 {
			t.Errorf("feed = %q, want nothing: PATH is scanned by entry, never fed", feed)
		}
		if strings.Join(added, ",") != "/revieweval-bin" {
			t.Errorf("added PATH entries = %q, want [/revieweval-bin]", added)
		}
	})
	t.Run("a-reordered-path-adds-nothing", func(t *testing.T) {
		feed, added := harnessParentEnv(launch, []string{"TMP=/t", "KEEP=1", "PATH=" + list("/b", "/a")})
		if len(feed) != 0 || len(added) != 0 {
			t.Errorf("feed = %q, added = %q, want both empty", feed, added)
		}
	})
	if runtime.GOOS == "windows" {
		t.Run("path-under-another-case-is-still-path", func(t *testing.T) {
			feed, added := harnessParentEnv([]string{"Path=" + list("/a")}, []string{"Path=" + list("/x", "/a")})
			if len(feed) != 0 || strings.Join(added, ",") != "/x" {
				t.Errorf("feed = %q, added = %q, want no feed and [/x]", feed, added)
			}
		})
	}
}
