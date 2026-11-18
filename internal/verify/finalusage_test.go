package verify

import (
	"testing"
	"time"

	"github.com/develdeco/jig/internal/envrun"
	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/journal"
)

// finalLine is the one final-oracle line the journal holds.
func (r *rig) finalLine() journal.Line {
	r.t.Helper()
	for _, l := range r.lines() {
		if l.Event == journal.EventFinalOracle {
			return l
		}
	}
	r.t.Fatal("no final-oracle line")
	return journal.Line{}
}

// TestAFinalRunLineCarriesWhatTheRunUsed: the final-oracle line holds the CPU
// time, peak and basis the run returned, and none when the run measured
// nothing.
func TestAFinalRunLineCarriesWhatTheRunUsed(t *testing.T) {
	t.Parallel()
	t.Run("measured", func(t *testing.T) {
		t.Parallel()
		r := newRig(t, fixture.Opts{})
		r.deps.Oracle = nil
		r.deps.OracleUsage = func(cmd, dir string, env []string, limit time.Duration) (string, envrun.Usage, error) {
			return "out", envrun.Usage{CPU: 4200 * time.Millisecond, PeakBytes: 300 << 20, Basis: envrun.BasisSampled}, nil
		}
		r.fixClamp()
		if _, err := RunFinal(r.deps, r.finalRequest()); err != nil {
			t.Fatalf("RunFinal: %v", err)
		}
		l := r.finalLine()
		if l.CPUMs != 4200 || l.PeakBytes != 300<<20 || l.PeakBasis != "sampled" {
			t.Errorf("final-oracle line = %+v, want 4200 ms, 300 MiB, sampled", l)
		}
	})
	t.Run("unmeasured", func(t *testing.T) {
		t.Parallel()
		r := newRig(t, fixture.Opts{})
		r.fixClamp()
		if _, err := RunFinal(r.deps, r.finalRequest()); err != nil {
			t.Fatalf("RunFinal: %v", err)
		}
		if l := r.finalLine(); l.CPUMs != 0 || l.PeakBytes != 0 || l.PeakBasis != "" {
			t.Errorf("final-oracle line = %+v, want no figures", l)
		}
	})
}
