package e2e

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/develdeco/jig/fixture"
	"github.com/develdeco/jig/gitx"
	"github.com/develdeco/jig/journal"
	"github.com/develdeco/jig/store"
)

// jigResult is one jig subprocess invocation's captured output.
type jigResult struct {
	Stdout string
	Stderr string
	Code   int
}

// runJig runs the built jig binary with cwd and args, inheriting the test
// process's environment (which already carries JIG_HOME from t.Setenv, set
// by newFixture below). It never fails the test on a non-zero exit: callers
// assert Code themselves, since every jig subcommand's pause/stop/error
// paths are meaningful exit codes, not test failures.
func runJig(t *testing.T, cwd string, args ...string) jigResult {
	t.Helper()
	cmd := exec.Command(jigBinary, args...)
	cmd.Dir = cwd
	cmd.Env = os.Environ()
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	return jigResult{Stdout: stdout.String(), Stderr: stderr.String(), Code: exitCodeOf(t, err)}
}

// newFixture sets a fresh JIG_HOME (via t.Setenv, so it is scoped to this
// test/subtest and inherited by runJig's subprocess) and generates a fresh
// fixture under it. It returns both the fixture and the JIG_HOME path, since
// several assertions (the pool build lease, the machine mapping file) need
// to reach under JIG_HOME directly.
//
// NOTE: fixture.Generate resolves testdata/fixture relative to its own
// source file and writes the machine mapping under whatever JIG_HOME is
// already set when it is called; it does not create a home directory
// itself. That means JIG_HOME is a temp dir independent of fx.Dir, not
// "<fx.Dir>/home" as a first pass at this task assumed - this helper is the
// single place that decision lives.
func newFixture(t *testing.T, opts fixture.Opts) (fx *fixture.Fixture, home string) {
	t.Helper()
	home = t.TempDir()
	t.Setenv("JIG_HOME", home)
	fx = fixture.Generate(t, opts)
	return fx, home
}

// poolBuildLeaseDir returns the build lease directory for ticket under home,
// matching pool.Acquire's layout: <home>/pool/<repoName>/<ticket>.
func poolBuildLeaseDir(home, repoName, ticket string) string {
	return filepath.Join(home, "pool", repoName, ticket)
}

// pinnedIdentityEnv pins author/committer identity and dates for scripted
// test commits (divergent store pushes, target-branch moves), so any commit
// this suite makes outside the fake backend is still deterministic.
var pinnedIdentityEnv = []string{
	"GIT_AUTHOR_NAME=e2e-script",
	"GIT_AUTHOR_EMAIL=e2e-script@example.invalid",
	"GIT_COMMITTER_NAME=e2e-script",
	"GIT_COMMITTER_EMAIL=e2e-script@example.invalid",
	"GIT_AUTHOR_DATE=2026-01-02T00:00:00Z",
	"GIT_COMMITTER_DATE=2026-01-02T00:00:00Z",
}

// gitLog runs a git command in dir and fatals the test on failure, returning
// trimmed stdout. It works against bare repos too (git accepts a bare repo
// as its own working directory for read commands like log/show/rev-parse).
func gitLog(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := gitx.Run(dir, args...)
	if err != nil {
		t.Fatalf("git %s (in %s): %v", strings.Join(args, " "), dir, err)
	}
	return out
}

// scriptedPush clones remote to a fresh temp dir, writes content at relPath,
// commits it with a pinned identity, and pushes it back to remote on
// whatever branch the clone checked out (its default branch). It is used to
// simulate a divergent commit landing on a shared remote from outside the
// jig process under test: a second contributor pushing to the store, or the
// target branch moving upstream before publish.
func scriptedPush(t *testing.T, remote, relPath, content, message string) {
	t.Helper()
	dir := t.TempDir()
	if _, err := gitx.Run(dir, "clone", remote, "."); err != nil {
		t.Fatalf("scriptedPush: clone %s: %v", remote, err)
	}
	full := filepath.Join(dir, filepath.FromSlash(relPath))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("scriptedPush: mkdir: %v", err)
	}
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		t.Fatalf("scriptedPush: write %s: %v", full, err)
	}
	if _, err := gitx.Run(dir, "add", "-A"); err != nil {
		t.Fatalf("scriptedPush: git add: %v", err)
	}
	if _, err := gitx.RunEnv(dir, pinnedIdentityEnv, "commit", "-m", message); err != nil {
		t.Fatalf("scriptedPush: git commit: %v", err)
	}
	branch, err := gitx.Run(dir, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		t.Fatalf("scriptedPush: rev-parse HEAD: %v", err)
	}
	if _, err := gitx.Run(dir, "push", "origin", branch); err != nil {
		t.Fatalf("scriptedPush: push: %v", err)
	}
}

// readJournal opens fx's store and reads ticket's journal.
func readJournal(t *testing.T, fx *fixture.Fixture, ticket string) []journal.Line {
	t.Helper()
	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatalf("readJournal: store.Open: %v", err)
	}
	lines, err := journal.Read(st, ticket)
	if err != nil {
		t.Fatalf("readJournal: journal.Read: %v", err)
	}
	return lines
}

// findJournalLine returns the first journal line matching every non-empty
// predicate field, and whether one was found.
func findJournalLine(lines []journal.Line, event, slice, outcome string) (journal.Line, bool) {
	for _, l := range lines {
		if event != "" && l.Event != event {
			continue
		}
		if slice != "" && l.Slice != slice {
			continue
		}
		if outcome != "" && l.Outcome != outcome {
			continue
		}
		return l, true
	}
	return journal.Line{}, false
}

// firstLine mirrors session/fake.go's firstLine: the text up to its first
// line break, or all of it if there is none. Used to derive expected commit
// messages from scenario result.json summaries without hardcoding them.
func firstLine(s string) string {
	if i := strings.IndexAny(s, "\n\r"); i >= 0 {
		return s[:i]
	}
	return s
}

// scenarioResultSummary reads a scenario attempt's result.json and returns
// its summary field.
func scenarioResultSummary(t *testing.T, scenarioDir, slice, attempt string) string {
	t.Helper()
	path := filepath.Join(scenarioDir, "slices", slice, "attempt-"+attempt, "result.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("scenarioResultSummary: read %s: %v", path, err)
	}
	var res struct {
		Summary string `json:"summary"`
	}
	if err := json.Unmarshal(data, &res); err != nil {
		t.Fatalf("scenarioResultSummary: parse %s: %v", path, err)
	}
	return res.Summary
}

// committedPatchSteps is every (slice, attempt) pair in the base fixture
// scenario whose patch.diff is non-empty and therefore produces a commit in
// the fake backend, in the order fixture/fixture_test.go's own
// TestPatchSequence applies them (that test is the generator package's own
// authority on which attempts commit). TestEndToEndTwice uses it to derive
// the expected pre-squash commit message SET without hardcoding scenario
// content twice.
var committedPatchSteps = []struct{ slice, attempt string }{
	{"a", "1"},
	{"b", "1"},
	{"b", "2"},
	{"c", "2"},
	{"d", "1"},
	{"fix-1", "1"},
}

// expectedSliceCommitMessages derives the exact commit message set the fake
// backend produces for committedPatchSteps, reading each step's scenario
// result.json rather than hardcoding the summaries.
func expectedSliceCommitMessages(t *testing.T, fx *fixture.Fixture, ticket string) []string {
	t.Helper()
	var msgs []string
	for _, step := range committedPatchSteps {
		summary := scenarioResultSummary(t, fx.ScenarioDir, step.slice, step.attempt)
		msgs = append(msgs, fmt.Sprintf("%s %s: %s", ticket, step.slice, firstLine(summary)))
	}
	return msgs
}

// assertSameMessageSet compares got and want as multisets (order-independent
// but count-sensitive), fataling with a readable diff on mismatch.
func assertSameMessageSet(t *testing.T, got, want []string) {
	t.Helper()
	gotSorted := append([]string(nil), got...)
	wantSorted := append([]string(nil), want...)
	sort.Strings(gotSorted)
	sort.Strings(wantSorted)
	if len(gotSorted) != len(wantSorted) {
		t.Fatalf("commit message count = %d, want %d\ngot:  %v\nwant: %v", len(gotSorted), len(wantSorted), gotSorted, wantSorted)
	}
	for i := range gotSorted {
		if gotSorted[i] != wantSorted[i] {
			t.Fatalf("commit message set mismatch at sorted index %d:\ngot:  %v\nwant: %v", i, gotSorted, wantSorted)
		}
	}
}

// normalizeCRLF normalizes "\r\n" to "\n" for golden comparisons, matching
// every other package's convention in this repo.
func normalizeCRLF(s string) string {
	return strings.ReplaceAll(s, "\r\n", "\n")
}

// readGolden reads a golden file under e2e/testdata/golden, normalizing line
// endings.
func readGolden(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(repoRoot, "e2e", "testdata", "golden", name))
	if err != nil {
		t.Fatalf("readGolden: %v", err)
	}
	return normalizeCRLF(string(data))
}

// assertGolden compares got (normalized) against the named golden file,
// printing both in full on mismatch since status output is short.
func assertGolden(t *testing.T, name, got string) {
	t.Helper()
	want := readGolden(t, name)
	got = normalizeCRLF(got)
	if got != want {
		t.Fatalf("output does not match golden %s:\n--- got ---\n%s\n--- want ---\n%s", name, got, want)
	}
}

// knownSliceTokens is every slice id this suite's fixtures ever mint,
// including the gate-appended fix-1, used by assertOnlySlicesListed to parse
// loosely-formatted command output without assuming an exact rendering.
var knownSliceTokens = []string{"a", "b", "c", "d", "fix-1"}

// assertOnlySlicesListed scans output for occurrences of known slice-id
// tokens (as whole words, so "a" doesn't match inside other text) and fails
// unless the set of tokens found equals want exactly. It is used where the
// contract fixes a command's behavior (e.g. requeue --from-brief-diff
// touching exactly one slice) but not its exact output format.
func assertOnlySlicesListed(t *testing.T, output string, want []string) {
	t.Helper()
	found := map[string]bool{}
	for _, field := range strings.FieldsFunc(output, func(r rune) bool {
		return !(r == '-' || (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'))
	}) {
		for _, tok := range knownSliceTokens {
			if field == tok {
				found[tok] = true
			}
		}
	}
	wantSet := map[string]bool{}
	for _, w := range want {
		wantSet[w] = true
	}
	if len(found) != len(wantSet) {
		t.Fatalf("assertOnlySlicesListed: found %v, want exactly %v\noutput:\n%s", keysOf(found), want, output)
	}
	for w := range wantSet {
		if !found[w] {
			t.Fatalf("assertOnlySlicesListed: expected %q listed, not found\noutput:\n%s", w, output)
		}
	}
}

// joinPath is filepath.Join, named for readability at call sites that build
// a path purely for a read/write helper immediately after.
func joinPath(elem ...string) string { return filepath.Join(elem...) }

func readFileOrFatal(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return data
}

func writeFileOrFatal(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func keysOf(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
