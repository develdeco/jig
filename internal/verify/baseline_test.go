package verify

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/home"
	"github.com/develdeco/jig/internal/journal"
)

// okLines is the output of a many-package oracle that prints a line per
// success: n lines whose timings are t.
func okLines(n int, t string) string {
	var b strings.Builder
	for i := 0; i < n; i++ {
		fmt.Fprintf(&b, "ok  \tgithub.com/example/proj/pkg%d\t%ss\n", i, t)
	}
	return b.String()
}

// passThenFail runs a passing oracle on one commit, then the failing output
// out on the next commit of the same slice, and returns the failure.
func passThenFail(t *testing.T, r *rig, pass, out string) Result {
	t.Helper()
	r.oracle.out = pass
	sha := r.commit("b", "b: one", map[string]string{"x.txt": "1"})
	if res, err := Run(r.deps, r.request("b", sha)); err != nil || res.State != journal.VerifyPass {
		t.Fatalf("passing Run = %+v, %v", res, err)
	}
	r.oracle.out, r.oracle.err = out, os.ErrInvalid
	sha = r.commit("b", "b: two", map[string]string{"x.txt": "2"})
	res, err := Run(r.deps, r.request("b", sha))
	if err != nil || res.State != journal.VerifyFail {
		t.Fatalf("failing Run = %+v, %v", res, err)
	}
	return res
}

// TestFailedSummaryNamesTheFailureAmongOkLines is the repro: the failure
// printed early in output that ends in success lines is in the summary.
func TestFailedSummaryNamesTheFailureAmongOkLines(t *testing.T) {
	t.Parallel()
	r := newRig(t, fixture.Opts{})
	failing := okLines(3, "0.5") + "boom: pkg9 broke at step 4\n" + okLines(60, "0.7")
	res := passThenFail(t, r, okLines(60, "0.3"), failing)
	if !strings.Contains(res.Summary, "boom: pkg9 broke at step 4") {
		t.Errorf("Summary = %q, want the failure line", res.Summary)
	}
	if !strings.Contains(res.Tail, "boom: pkg9 broke") {
		t.Errorf("Tail lacks the failure line: %q", res.Tail)
	}
	if got := r.lines()[len(r.lines())-1]; !strings.Contains(got.Summary, "boom") {
		t.Errorf("journal summary = %q", got.Summary)
	}
}

// TestSameFailureTwiceSharesASignatureAndDifferentOnesDoNot is what the stall
// counter relies on, with a long run of ok lines at the end each time.
func TestSameFailureTwiceSharesASignatureAndDifferentOnesDoNot(t *testing.T) {
	t.Parallel()
	r := newRig(t, fixture.Opts{})
	one := okLines(3, "0.5") + "boom: pkg9 broke at step 4\n" + okLines(60, "0.7")
	first := passThenFail(t, r, okLines(60, "0.3"), one)

	r.oracle.out = okLines(3, "9.1") + "boom: pkg9 broke at step 12\n" + okLines(60, "2.2")
	sha := r.commit("b", "b: three", map[string]string{"x.txt": "3"})
	same, err := Run(r.deps, r.request("b", sha))
	if err != nil || same.State != journal.VerifyFail {
		t.Fatalf("Run = %+v, %v", same, err)
	}
	if first.Signature == "" || first.Signature != same.Signature {
		t.Errorf("the same failure twice: signatures %q and %q, want one", first.Signature, same.Signature)
	}

	r.oracle.out = okLines(3, "0.5") + "panic: pkg2 nil map\n" + okLines(60, "0.7")
	sha = r.commit("b", "b: four", map[string]string{"x.txt": "4"})
	other, err := Run(r.deps, r.request("b", sha))
	if err != nil || other.State != journal.VerifyFail {
		t.Fatalf("Run = %+v, %v", other, err)
	}
	if other.Signature == first.Signature {
		t.Errorf("two different failures share the signature %q", other.Signature)
	}
}

// TestFailedRunWithNoBaselineKeepsTodaysTail: with no passing run of the
// command, the summary is the end of the output as before.
func TestFailedRunWithNoBaselineKeepsTodaysTail(t *testing.T) {
	t.Parallel()
	r := newRig(t, fixture.Opts{})
	r.oracle.out = okLines(3, "0.5") + "boom: early\n" + okLines(60, "0.7")
	r.oracle.err = os.ErrInvalid
	sha := r.commit("b", "b", map[string]string{"x.txt": "1"})
	res, err := Run(r.deps, r.request("b", sha))
	if err != nil || res.State != journal.VerifyFail {
		t.Fatalf("Run = %+v, %v", res, err)
	}
	if strings.Contains(res.Summary, "boom") || res.Summary != oneLine(res.Tail) || strings.Contains(res.Tail, markerOldTail) {
		t.Errorf("Summary = %q, Tail has marker %v, want today's end of the output", res.Summary, strings.Contains(res.Tail, markerOldTail))
	}
}

// TestFailedTailIsTheNewLinesThenTheLastBytes: the tail the log keeps leads
// with the lines the pass did not print, bounded, then the marker and the end.
func TestFailedTailIsTheNewLinesThenTheLastBytes(t *testing.T) {
	t.Parallel()
	r := newRig(t, fixture.Opts{})
	res := passThenFail(t, r, okLines(60, "0.3"), okLines(2, "1.5")+"boom: early\n"+okLines(60, "0.7"))
	i, j := strings.Index(res.Tail, "boom: early"), strings.Index(res.Tail, markerOldTail)
	if i != 0 || j < i || !strings.HasSuffix(res.Tail, "pkg59\t0.7s") {
		t.Errorf("Tail = %q, want the new lines, the marker, then the last bytes", res.Tail)
	}
}

// TestPassKeepsOneBaselinePerCommand: the baseline is a machine-local file
// per repo, workspace and command, replaced by the latest pass.
func TestPassKeepsOneBaselinePerCommand(t *testing.T) {
	t.Parallel()
	r := newRig(t, fixture.Opts{})
	r.oracle.out = "alpha 1\n"
	sha := r.commit("b", "b", map[string]string{"x.txt": "1"})
	if res, err := Run(r.deps, r.request("b", sha)); err != nil || res.State != journal.VerifyPass {
		t.Fatalf("Run = %+v, %v", res, err)
	}
	files, _ := filepath.Glob(filepath.Join(home.BaselinePath(r.fx.Home, "", "", ""), "..", "*.txt"))
	if len(files) != 1 {
		t.Fatalf("baseline files = %v, want one", files)
	}
	path := files[0]
	r.oracle.out = "beta 2\n"
	sha = r.commit("b", "b2", map[string]string{"x.txt": "2"})
	if res, err := Run(r.deps, r.request("b", sha)); err != nil || res.State != journal.VerifyPass {
		t.Fatalf("Run = %+v, %v", res, err)
	}
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "beta #\n" {
		t.Errorf("baseline = %q, %v, want the latest pass's lines only", data, err)
	}
}
