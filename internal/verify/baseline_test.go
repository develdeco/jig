package verify

import (
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
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
