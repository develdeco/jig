package verifydeliver

import (
	"testing"
	"time"

	"github.com/develdeco/jig/internal/envrun"
	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/manifest"
)

// TestTheGatesOracleLineCarriesWhatItsRunUsed: the gate times and measures its
// own run, and the gate-oracle line it journals holds the figures; a run that
// measured nothing journals a line without them.
func TestTheGatesOracleLineCarriesWhatItsRunUsed(t *testing.T) {
	t.Parallel()
	fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
	d := newDeps(t, fx)
	man := manifest.Manifest{
		Workspaces: []manifest.Workspace{{ID: "root", Path: "."}},
		Oracles:    map[string]string{"measured": "echo a", "bare": "echo b"},
	}
	run := func(cmd, dir string) (string, envrun.Usage, error) {
		if cmd == "echo a" {
			return "", envrun.Usage{CPU: 2500 * time.Millisecond, PeakBytes: 64 << 20, Basis: envrun.BasisJob}, nil
		}
		return "", envrun.Usage{}, nil
	}
	runs, err := oracleSuiteRuns(fx.RepoDir, man, nil, run, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := journalGateRuns(d, fx.Ticket, fx.RepoDir, runs); err != nil {
		t.Fatal(err)
	}
	lines, err := journal.Read(d.Store, fx.Ticket)
	if err != nil {
		t.Fatal(err)
	}
	seen := 0
	for _, l := range lines {
		if l.Event != journal.EventGateOracle {
			continue
		}
		seen++
		switch l.Oracle {
		case "measured":
			if l.CPUMs != 2500 || l.PeakBytes != 64<<20 || l.PeakBasis != "job" {
				t.Errorf("measured gate-oracle line = %+v", l)
			}
		case "bare":
			if l.CPUMs != 0 || l.PeakBytes != 0 || l.PeakBasis != "" {
				t.Errorf("an unmeasured run's line carries figures: %+v", l)
			}
		}
	}
	if seen != 2 {
		t.Errorf("%d gate-oracle lines, want 2", seen)
	}
}

// TestTheRealGateOracleRunReturnsWhatItsTreeUsed: the gate's real oracle run
// (shellOracle) measures the tree of the command it ran, so the gate-oracle
// line built from it has figures.
func TestTheRealGateOracleRunReturnsWhatItsTreeUsed(t *testing.T) {
	t.Parallel()
	// A shell loop that does a measurable amount of work on any OS.
	cmd := `i=0; while [ $i -lt 20000 ]; do i=$((i+1)); done`
	_, use, err := shellOracle(cmd, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if use.CPU <= 0 {
		t.Errorf("usage = %+v, want the CPU time of the command's tree", use)
	}
	if (use.PeakBytes == 0) != (use.Basis == "") {
		t.Errorf("usage = %+v: peak and basis disagree", use)
	}
}
