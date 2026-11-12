package verify

import (
	"os"
	"testing"

	"github.com/develdeco/jig/internal/envrun"
	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/journal"
)

// TestOracleFailLineNamesItsCause: a run the wedge guard killed and a run jig's
// own stop ended each journal a fail line that names why, and a run that failed
// with test output journals none.
func TestOracleFailLineNamesItsCause(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name  string
		err   error
		cause string
	}{
		{"timeout", timedOut("go test ./..."), journal.CauseTimeout},
		{"stopped", envrun.Stopped("go test ./..."), journal.CauseStopped},
		{"test failure", os.ErrInvalid, ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			r := newRig(t, fixture.Opts{})
			r.oracle.out, r.oracle.err = "--- FAIL: TestX\n", c.err
			sha := r.commit("b", "b: one", map[string]string{"x.txt": "1"})
			// A stopped run ends with the stop error; the others with a fail.
			_, _ = Run(r.deps, r.request("b", sha))
			var fails []journal.Line
			for _, l := range r.lines() {
				if l.Event == journal.EventOracle && l.Outcome == "fail" {
					fails = append(fails, l)
				}
			}
			if len(fails) != 1 {
				t.Fatalf("fail lines = %+v, want one", fails)
			}
			if fails[0].Cause != c.cause {
				t.Errorf("Cause = %q, want %q", fails[0].Cause, c.cause)
			}
		})
	}
}
