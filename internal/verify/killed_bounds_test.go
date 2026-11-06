package verify

import (
	"errors"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/journal"
)

// killedRun runs a passing oracle, then a failing one that printed out and
// was killed by the wedge guard, whose error embeds a command of cmdLen bytes.
func killedRun(t *testing.T, r *rig, pass, out string, cmdLen int) Result {
	t.Helper()
	r.oracle.out = pass
	sha := r.commit("b", "b: one", map[string]string{"x.txt": "1"})
	if res, err := Run(r.deps, r.request("b", sha)); err != nil || res.State != journal.VerifyPass {
		t.Fatalf("passing Run = %+v, %v", res, err)
	}
	r.oracle.out = out
	r.oracle.err = errors.New(`shell "` + strings.Repeat("go test -run X ./... && ", cmdLen/24) + `": did not finish within 30m0s`)
	sha = r.commit("b", "b: two", map[string]string{"x.txt": "2"})
	res, err := Run(r.deps, r.request("b", sha))
	if err != nil || res.State != journal.VerifyFail {
		t.Fatalf("killed Run = %+v, %v", res, err)
	}
	return res
}

// TestKilledRunWithALongCommandKeepsItsBounds is the repro: a run the wedge
// guard killed, its error embedding a long oracle command and many new lines
// after a baseline, has a one-line summary within its bound that names a
// failing line, and a signature that keeps the timeout.
func TestKilledRunWithALongCommandKeepsItsBounds(t *testing.T) {
	t.Parallel()
	r := newRig(t, fixture.Opts{})
	newOut := okLines(3, "0.5") + strings.Repeat("=== RUN   TestSlowThing/case_x  waiting for the server to come up\n", 12) + "--- FAIL: TestSlowThing\n"
	res := killedRun(t, r, okLines(60, "0.3"), newOut, 700)
	if strings.Contains(res.Summary, "\n") || len(res.Summary) > summaryBytes {
		t.Errorf("Summary is %d bytes (%q), want one line within %d", len(res.Summary), res.Summary, summaryBytes)
	}
	if !strings.Contains(res.Summary, "=== RUN") {
		t.Errorf("Summary = %q, want a failing line", res.Summary)
	}
	if !strings.Contains(res.Signature, "did not finish within") {
		t.Errorf("Signature = %q, want the timeout", res.Signature)
	}
	plain := passThenFail(t, newRig(t, fixture.Opts{}), okLines(60, "0.3"), newOut)
	if plain.Signature == res.Signature {
		t.Errorf("a kill and a plain failure share the signature %q", res.Signature)
	}
}
