package verify

import (
	"errors"
	"testing"
	"time"

	"github.com/develdeco/jig/internal/envrun"
	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/journal"
)

// oracleLine is the one oracle line the journal holds.
func (r *rig) oracleLine() journal.Line {
	r.t.Helper()
	for _, l := range r.lines() {
		if l.Event == journal.EventOracle {
			return l
		}
	}
	r.t.Fatal("no oracle line")
	return journal.Line{}
}

// TestTheOracleLineCarriesWhatTheRunUsed: a run that passed or failed ends
// with a line holding the CPU time, peak and basis the run returned, beside
// its seconds.
func TestTheOracleLineCarriesWhatTheRunUsed(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name string
		err  error
	}{{"pass", nil}, {"fail", errors.New("exit status 1")}} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			r := newRig(t, fixture.Opts{})
			r.deps.Oracle = nil
			r.deps.OracleUsage = func(cmd, dir string, env []string, limit time.Duration) (string, envrun.Usage, error) {
				return "out", envrun.Usage{CPU: 4200 * time.Millisecond, PeakBytes: 300 << 20, Basis: envrun.BasisSampled}, c.err
			}
			sha := r.fixClamp()
			if _, err := Run(r.deps, r.request("a", sha)); err != nil {
				t.Fatalf("Run: %v", err)
			}
			l := r.oracleLine()
			if l.CPUMs != 4200 || l.PeakBytes != 300<<20 || l.PeakBasis != "sampled" || l.Seconds < 1 {
				t.Errorf("oracle line = %+v, want 4200 ms, 300 MiB, sampled", l)
			}
		})
	}
}

// TestAnOracleThatMeasuresNothingWritesALineWithoutFigures: an injected
// oracle returns no usage; the line has none and the verdict is unchanged.
func TestAnOracleThatMeasuresNothingWritesALineWithoutFigures(t *testing.T) {
	t.Parallel()
	r := newRig(t, fixture.Opts{})
	sha := r.fixClamp()
	res, err := Run(r.deps, r.request("a", sha))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.State != journal.VerifyPass {
		t.Errorf("state = %s, want pass", res.State)
	}
	if l := r.oracleLine(); l.CPUMs != 0 || l.PeakBytes != 0 || l.PeakBasis != "" {
		t.Errorf("oracle line = %+v, want no figures", l)
	}
}

// TestARunJigStoppedKeepsItsLineWithoutFigures: a run ended because jig was
// stopped reached no verdict, so its line carries nothing it used.
func TestARunJigStoppedKeepsItsLineWithoutFigures(t *testing.T) {
	t.Parallel()
	r := newRig(t, fixture.Opts{})
	r.deps.Oracle = nil
	r.deps.OracleUsage = func(cmd, dir string, env []string, limit time.Duration) (string, envrun.Usage, error) {
		return "", envrun.Usage{}, envrun.Stopped("the oracle")
	}
	sha := r.fixClamp()
	_, _ = Run(r.deps, r.request("a", sha))
	if l := r.oracleLine(); l.Cause != journal.CauseStopped || l.CPUMs != 0 || l.PeakBytes != 0 {
		t.Errorf("oracle line = %+v, want a stopped line with no figures", l)
	}
}

// TestARealOracleRunEndsWithTheFiguresOfItsTree: with no oracle injected, the
// run goes through the real path (sh, the job or group, the measuring), and the
// line it writes carries what the tree used.
func TestARealOracleRunEndsWithTheFiguresOfItsTree(t *testing.T) {
	t.Parallel()
	r := newRig(t, fixture.Opts{})
	r.deps.Oracle = nil
	sha := r.fixClamp()
	res, err := Run(r.deps, r.request("a", sha))
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if res.State != journal.VerifyPass {
		t.Fatalf("state = %s, want pass: %s", res.State, res.Tail)
	}
	l := r.oracleLine()
	if l.CPUMs <= 0 {
		t.Errorf("oracle line = %+v, want the CPU time of the tree that ran", l)
	}
	if (l.PeakBytes == 0) != (l.PeakBasis == "") {
		t.Errorf("oracle line = %+v: peak and basis disagree", l)
	}
}
