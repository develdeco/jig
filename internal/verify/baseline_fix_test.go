package verify

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/outcome"
	"github.com/develdeco/jig/internal/store"
)

// baselineOf is the baseline a pass of out leaves, as failureOutput reads it.
func baselineOf(out string) map[string]bool { return parseBaseline(baselineText(out)) }

// TestKilledRunSummaryCarriesTheTimeout: with a baseline and a new line, a
// run the wedge guard killed still says it did not finish.
func TestKilledRunSummaryCarriesTheTimeout(t *testing.T) {
	t.Parallel()
	killed := timedOut("the oracle")
	base := baselineOf("ok  pkg/a 0.3s\n")
	out := "ok  pkg/a 0.4s\n=== slow test started in pkg/b\n"
	tail, summary := failureOutput(out, killed, base)
	if !strings.HasSuffix(summary, "did not finish within 30m0s") || !strings.HasPrefix(summary, "=== slow test started in pkg/b") {
		t.Errorf("summary = %q, want the new line then the timeout", summary)
	}
	if !strings.HasPrefix(tail, "=== slow test started in pkg/b\n"+killed.Error()+"\n"+markerOldTail) {
		t.Errorf("tail = %q", tail)
	}
	// The signature tracks the wedge: another noise line gives another one,
	// the same noise the same one.
	_, again := failureOutput("ok  pkg/a 9.9s\n=== slow test started in pkg/b\n", killed, base)
	if outcome.Signature("s", outcome.CodeBug, summary) != outcome.Signature("s", outcome.CodeBug, again) {
		t.Errorf("the same wedge twice: %q vs %q", summary, again)
	}
	// With no new line, and with no baseline, it is today's, which ends with it.
	for _, b := range []map[string]bool{base, nil} {
		_, s := failureOutput("ok  pkg/a 0.4s\n", killed, b)
		if !strings.HasSuffix(s, "did not finish within 30m0s") {
			t.Errorf("summary = %q", s)
		}
	}
	// A plain failure does not gain the text.
	if _, s := failureOutput(out, errors.New("exit status 1"), base); strings.Contains(s, "did not finish") {
		t.Errorf("summary = %q", s)
	}
}

// TestKilledRunSummaryKeepsTheTimeoutWhenNewLinesFillIt: new lines past the
// summary's bound do not push the timeout out of the summary.
func TestKilledRunSummaryKeepsTheTimeoutWhenNewLinesFillIt(t *testing.T) {
	t.Parallel()
	killed := timedOut("the oracle")
	base := baselineOf("ok  pkg/a 0.3s\n")
	out := "ok  pkg/a 0.4s\n" + strings.Repeat("=== RUN   TestSlowThing/case_x  waiting for the server to come up\n", 12)
	_, summary := failureOutput(out, killed, base)
	if !strings.HasSuffix(summary, "did not finish within 30m0s") || !strings.HasPrefix(summary, "=== RUN") || len(summary) > summaryBytes {
		t.Errorf("summary = %q (%d bytes)", summary, len(summary))
	}
	long := "ok  pkg/a 0.4s\n" + strings.Repeat("x", 2*summaryBytes) + "\n"
	if _, s := failureOutput(long, killed, base); !strings.HasSuffix(s, "did not finish within 30m0s") || len(s) > summaryBytes {
		t.Errorf("summary of one long line = %q", s)
	}
}

// TestNormalizeKeepsPassingLinesOutOfTheSummary: digit runs of any length and
// trailing whitespace or CR do not make a line new.
func TestNormalizeKeepsPassingLinesOutOfTheSummary(t *testing.T) {
	t.Parallel()
	pass := "ok  \tpkg/a\t9.8s\nlistening on :8080 after 3 retries and 12 tries\nTests: 5 passed\n"
	fail := "ok  \tpkg/a\t10.2s\r\nlistening on :80 after 14 retries and 7 tries  \r\nTests: 5 passed \r\nboom: broke\r\n"
	base := baselineOf(pass)
	if got := newLines(fail, base); len(got) != 1 || got[0] != "boom: broke" {
		t.Fatalf("newLines = %q, want only the failure", got)
	}
	_, s1 := failureOutput(fail, os.ErrInvalid, base)
	_, s2 := failureOutput(strings.ReplaceAll(fail, "10.2s", "100.45s"), os.ErrInvalid, base)
	if s1 != "boom: broke" || s1 != s2 {
		t.Errorf("summaries %q and %q, want the failure alone, twice the same", s1, s2)
	}
	if normalizeLine("  a 12 b 345 c  \r") != "a # b # c" {
		t.Errorf("normalizeLine = %q", normalizeLine("  a 12 b 345 c  \r"))
	}
}

// TestFailureWithNoNewLineKeepsTodaysSummary: a failure whose lines all
// normalize into the pass's is summarized as today, not as nothing, so two
// such failures still differ; a pass with empty output leaves no baseline.
func TestFailureWithNoNewLineKeepsTodaysSummary(t *testing.T) {
	t.Parallel()
	base := baselineOf("Tests: 42 passed, 0 failed\n")
	tail, summary := failureOutput("Tests: 41 passed, 1 failed\n", os.ErrInvalid, base)
	if summary != "Tests: 41 passed, 1 failed" || tail != "Tests: 41 passed, 1 failed" {
		t.Errorf("tail %q, summary %q, want today's", tail, summary)
	}
	if baselineOf("") != nil || baselineOf("\n  \n") != nil {
		t.Error("a pass with no output must leave no baseline")
	}
	tail, summary = failureOutput("boom\n", os.ErrInvalid, baselineOf(""))
	if tail != "boom" || summary != "boom" {
		t.Errorf("tail %q, summary %q, want today's", tail, summary)
	}
}

// TestFailureOutputBounds: the summary stays within 600 bytes and the new
// lines of the tail within 4,000, on whole lines.
func TestFailureOutputBounds(t *testing.T) {
	t.Parallel()
	base := baselineOf("ok\n")
	var b strings.Builder
	for i := 0; i < 400; i++ {
		b.WriteString("failure line " + strings.Repeat("x", 40) + string(rune('a'+i%26)) + string(rune('a'+i/26%26)) + "\n")
	}
	tail, summary := failureOutput(b.String(), os.ErrInvalid, base)
	if len(summary) > summaryBytes || len(summary) < summaryBytes-60 {
		t.Errorf("summary is %d bytes, want just under %d", len(summary), summaryBytes)
	}
	head := tail[:strings.Index(tail, "\n"+markerOldTail)]
	if len(head) > tailBytes || len(head) < tailBytes-60 {
		t.Errorf("new lines in the tail are %d bytes, want just under %d", len(head), tailBytes)
	}
	for _, l := range strings.Split(head, "\n") {
		if !strings.HasPrefix(l, "failure line ") || len(l) != len("failure line ")+42 {
			t.Fatalf("a line was cut or lost: %q", l)
		}
	}
	// The tail's new lines are longer than the summary's.
	if strings.Count(head, "\n") <= strings.Count(summary, " failure line")+1 {
		t.Error("the tail must hold more new lines than the summary")
	}
}

func TestHeadBytes(t *testing.T) {
	t.Parallel()
	if got := headBytes([]string{"aaaa", "bb", "cc"}, 7); got != "aaaa\nbb" {
		t.Errorf("headBytes = %q, want whole lines up to the bound", got)
	}
	// A line that does not fit ends the head; a later short one does not
	// slip past it.
	if got := headBytes([]string{"aaaa", "bbbbbbbb", "c"}, 7); got != "aaaa" {
		t.Errorf("headBytes = %q", got)
	}
	if got := headBytes([]string{strings.Repeat("z", 30), "b"}, 10); got != strings.Repeat("z", 10) {
		t.Errorf("headBytes = %q, want the first line cut to the bound", got)
	}
}

// TestBaselineBelongsToItsRepoAndWorkspace: a pass in one repo or workspace
// leaves no baseline for the same command in another.
func TestBaselineBelongsToItsRepoAndWorkspace(t *testing.T) {
	t.Parallel()
	r := newRig(t, fixture.Opts{})
	mk := func(repo, ws string) *run {
		return &run{d: Deps{JigHome: r.fx.Home, Repo: repo}, sl: store.Slice{Workspace: ws}}
	}
	a := mk("repo-a", "root")
	a.keepBaseline("go test", "alpha 1\n")
	if got := a.baselinePath("go test"); mustRead(t, got) != "alpha #\n" {
		t.Fatal("the pass must leave its baseline")
	}
	for _, other := range []*run{mk("repo-b", "root"), mk("repo-a", "web"), mk("", ""), mk("repo-a", "")} {
		if _, err := os.Stat(other.baselinePath("go test")); err == nil {
			t.Errorf("repo %q workspace %q shares the baseline of repo-a/root", other.d.Repo, other.sl.Workspace)
		}
		if _, s := other.failureOf("alpha 2\nboom\n", os.ErrInvalid, "go test"); s != "alpha 2 boom" {
			t.Errorf("repo %q workspace %q: summary %q, want today's", other.d.Repo, other.sl.Workspace, s)
		}
	}
}

func mustRead(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
