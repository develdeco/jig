package verify

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/outcome"
)

// TestOutputTailAndOneLine pin the two copies of the frontier's text helpers:
// the log keeps the last 4,000 bytes of an output, the journal the last 600 of
// it flattened to one line starting at a line boundary.
func TestOutputTailAndOneLine(t *testing.T) {
	t.Parallel()
	if got := outputTail("  short \n"); got != "short" {
		t.Errorf("outputTail trims: %q", got)
	}
	long := strings.Repeat("x", 3000) + "\n" + strings.Repeat("y", 3000)
	if got := outputTail(long); len(got) != 4000 || !strings.HasSuffix(got, "y") {
		t.Errorf("outputTail kept %d bytes, want the last 4000", len(got))
	}

	if got := oneLine("a  b\n\tc\r\n d"); got != "a b c d" {
		t.Errorf("oneLine = %q, want the words on one line", got)
	}
	var b strings.Builder
	for i := 0; i < 100; i++ {
		b.WriteString("line number " + strings.Repeat("z", 20) + "\n")
	}
	got := oneLine(b.String())
	if len(got) > 600 || strings.Contains(got, "\n") || !strings.HasPrefix(got, "line number") {
		t.Errorf("oneLine = %d bytes %q..., want at most 600 starting at a line boundary", len(got), got[:30])
	}
	// Two identical failures summarize identically whatever came before them.
	if oneLine("noise one\n"+b.String()) != oneLine("different noise\n"+b.String()) {
		t.Error("the same tail summarized differently with different text before it")
	}
}

// greenRepo is a repo with a start commit, a commit on top of it and one past
// that, and a second line of history that does not contain them.
type greenRepo struct {
	dir                   string
	start, work, past, up string
}

func newGreenRepo(t *testing.T) greenRepo {
	t.Helper()
	dir := t.TempDir()
	commit := func(name, content string) string {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := gitx.Run(dir, "add", "-A"); err != nil {
			t.Fatal(err)
		}
		if _, err := gitx.Run(dir, "commit", "-m", name); err != nil {
			t.Fatal(err)
		}
		sha, err := gitx.RevParse(dir, "HEAD")
		if err != nil {
			t.Fatal(err)
		}
		return sha
	}
	if _, err := gitx.Run(dir, "init", "-b", "main"); err != nil {
		t.Fatal(err)
	}
	g := greenRepo{dir: dir}
	g.start = commit("start.txt", "start")
	g.work = commit("work.txt", "work")
	g.past = commit("past.txt", "past")
	if _, err := gitx.Run(dir, "checkout", "-b", "side", g.start); err != nil {
		t.Fatal(err)
	}
	g.up = commit("side.txt", "side")
	if _, err := gitx.Run(dir, "checkout", "main"); err != nil {
		t.Fatal(err)
	}
	return g
}

// TestVerifyGreenChecksTheCommitAgainstTheStartSha covers the frontier's
// verifyGreen as `jig verify` keeps it, with HEAD at g.past: each way a
// claimed commit fails to be new work on the branch says so, and the artifacts
// are looked for in the commit's tree.
func TestVerifyGreenChecksTheCommitAgainstTheStartSha(t *testing.T) {
	t.Parallel()
	g := newGreenRepo(t)
	if _, err := gitx.Run(g.dir, "checkout", "--detach", g.past); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		name   string
		start  string
		res    outcome.Result
		reason string // a fragment of the reason; "" for a pass
	}{
		{"a descendant", g.start, outcome.Result{Commit: g.work}, ""},
		{"the tip itself", g.start, outcome.Result{Commit: g.past}, ""},
		{"an artifact in the commit", g.start, outcome.Result{Commit: g.work, Artifacts: []string{"work.txt"}}, ""},
		{"no commit", g.start, outcome.Result{}, "commit missing"},
		{"the start sha", g.start, outcome.Result{Commit: g.start}, "no new work"},
		{"a commit git does not have", g.start, outcome.Result{Commit: "0123456789012345678901234567890123456789"}, "not found"},
		{"not a descendant of the start sha", g.work, outcome.Result{Commit: g.up}, "not a descendant"},
		{"a commit off HEAD", g.start, outcome.Result{Commit: g.up}, "not reachable from HEAD"},
		{"an artifact not in the commit's tree", g.start, outcome.Result{Commit: g.work, Artifacts: []string{"past.txt"}}, "artifact past.txt missing"},
	} {
		reason, ok := verifyGreen(g.dir, c.start, c.res)
		if c.reason == "" {
			if !ok || reason != "" {
				t.Errorf("%s: verifyGreen = %q, %v, want a pass", c.name, reason, ok)
			}
			continue
		}
		if ok || !strings.Contains(reason, c.reason) {
			t.Errorf("%s: verifyGreen = %q, %v, want a refusal saying %q", c.name, reason, ok, c.reason)
		}
	}
}

// TestCleanHeadIsTheCommitOnlyForACleanTree: the evidence rule of the
// frontier's oracle line, kept for the checkout's own check.
func TestCleanHeadIsTheCommitOnlyForACleanTree(t *testing.T) {
	t.Parallel()
	g := newGreenRepo(t)
	if got := cleanHead(g.dir); got != g.past {
		t.Errorf("cleanHead of a clean tree = %q, want %s", got, g.past)
	}
	if got := dirtyTree(g.dir); got != "" {
		t.Errorf("dirtyTree of a clean tree = %q", got)
	}
	if err := os.WriteFile(filepath.Join(g.dir, "scratch.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := cleanHead(g.dir); got != "" {
		t.Errorf("cleanHead with an untracked file = %q, want none", got)
	}
	if got := dirtyTree(g.dir); !strings.Contains(got, "scratch.txt") {
		t.Errorf("dirtyTree = %q, want it to name scratch.txt", got)
	}
	if got := cleanHead(filepath.Join(g.dir, "missing")); got != "" {
		t.Errorf("cleanHead of a missing dir = %q", got)
	}
}

// TestStartShaPrefersTheRecordedOne: the recorded file wins, the merge base
// with origin's target comes next, and with neither the commit is held to
// nothing.
func TestStartShaPrefersTheRecordedOne(t *testing.T) {
	t.Parallel()
	g := newGreenRepo(t)
	if got := startSHA(g.dir, g.past, "  "+g.work+"\n", "main"); got != g.work {
		t.Errorf("startSHA with a recorded sha = %q, want %s trimmed", got, g.work)
	}
	if got := startSHA(g.dir, g.past, "", ""); got != "" {
		t.Errorf("startSHA with nothing = %q, want none", got)
	}
	if got := startSHA(g.dir, g.past, "", "main"); got != "" {
		t.Errorf("startSHA with no origin/main = %q, want none", got)
	}
	// origin/main at the work commit: the merge base of past and it is work.
	if _, err := gitx.Run(g.dir, "update-ref", "refs/remotes/origin/main", g.work); err != nil {
		t.Fatal(err)
	}
	if got := startSHA(g.dir, g.past, "", "main"); got != g.work {
		t.Errorf("startSHA from origin/main = %q, want %s", got, g.work)
	}
}
