package slot

import (
	"testing"
	"time"

	"github.com/develdeco/jig/internal/machine"
)

var shareMachine = machine.Reading{CPUs: 10, MemoryBytes: 100 << 30}

func run(secs int, shared bool) Run {
	return Run{Wall: time.Duration(secs) * time.Second, Shared: shared, CPUs: shareMachine.CPUs, MemBytes: shareMachine.MemoryBytes}
}

func alone(secs ...int) []Run {
	var out []Run
	for _, s := range secs {
		out = append(out, run(s, false))
	}
	return out
}

func with(base []Run, more ...Run) []Run { return append(append([]Run{}, base...), more...) }

// TestTheShareRuleReadsRecordedRuns: recorded runs in, a level out.
func TestTheShareRuleReadsRecordedRuns(t *testing.T) {
	base := alone(100, 100, 100)
	other := run(500, true)
	other.CPUs = 4
	unmeasured := run(500, true)
	unmeasured.Wall = 0
	cases := []struct {
		name string
		runs []Run
		want int
	}{
		{"no runs", nil, 0},
		{"shared runs and none alone to compare", []Run{run(500, true), run(500, true)}, 0},
		{"shared runs past the factor", with(base, run(200, true), run(300, true)), 2},
		{"shared runs within the factor", with(base, run(120, true), run(150, true)), 0},
		{"exactly at the factor is within it", with(base, run(150, true)), 0},
		{"one past the factor", with(base, run(151, true)), 1},
		{"a run within the factor gives a level back", with(base, run(200, true), run(300, true), run(110, true)), 1},
		{"recovery is floored at zero", with(base, run(110, true), run(110, true)), 0},
		{"the level is capped", with(base, run(200, true), run(200, true), run(200, true), run(200, true), run(200, true)), 4},
		{"the median of the last 3 alone runs is the figure", with(alone(10, 100, 100, 100), run(200, true)), 1},
		{"older alone runs are not read", with(alone(10, 10, 10, 100, 100, 100), run(200, true)), 1},
		{"five runs that did not share give a level back", with(base, run(200, true), run(300, true), run(100, false), run(100, false), run(100, false), run(100, false), run(100, false)), 1},
		{"four do not", with(base, run(200, true), run(300, true), run(100, false), run(100, false), run(100, false), run(100, false)), 2},
		{"a shared run restarts the count of runs that did not share", with(base, run(200, true), run(300, true), run(100, false), run(100, false), run(100, false), run(100, false), run(110, true), run(100, false)), 1},
		{"only the last 6 shared runs count", with(base, run(200, true), run(200, true), run(200, true), run(110, true), run(110, true), run(110, true), run(110, true), run(110, true), run(110, true)), 0},
		{"runs on another machine are not read", with(base, other, other), 0},
		{"a run with no wall time is not read", with(base, unmeasured), 0},
	}
	for _, c := range cases {
		if got := Level(c.runs, shareMachine); got != c.want {
			t.Errorf("%s: level = %d, want %d", c.name, got, c.want)
		}
	}
}

// TestRunsMeasuredOnAnotherMachineGiveNoAloneFigure: a changed machine starts
// again from level 0.
func TestRunsMeasuredOnAnotherMachineGiveNoAloneFigure(t *testing.T) {
	runs := with(alone(100, 100, 100), run(300, true), run(300, true))
	if got := Level(runs, shareMachine); got != 2 {
		t.Fatalf("level on the machine measured on = %d, want 2", got)
	}
	if got := Level(runs, machine.Reading{CPUs: 12, MemoryBytes: shareMachine.MemoryBytes}); got != 0 {
		t.Errorf("level on a machine with other CPUs = %d, want 0", got)
	}
	if got := Level(runs, machine.Reading{CPUs: shareMachine.CPUs, MemoryBytes: 64 << 30}); got != 0 {
		t.Errorf("level on a machine with other memory = %d, want 0", got)
	}
}

// TestTheVerdictSaysWhy: the reason counts the shared runs that ran over.
func TestTheVerdictSaysWhy(t *testing.T) {
	v := Judge(with(alone(100, 100, 100), run(300, true), run(110, true), run(300, true)), shareMachine)
	if v.Level != 1 || v.Shared != 3 || v.Slow != 2 {
		t.Fatalf("verdict = %+v, want level 1, 3 shared, 2 slow", v)
	}
	want := "asks for more of the machine because 2 of its last 3 shared runs ran over 1.5 times the alone time"
	if got := v.Why(); got != want {
		t.Errorf("why = %q, want %q", got, want)
	}
	if got := (Verdict{}).Why(); got != "" {
		t.Errorf("why at level 0 = %q, want none", got)
	}
}

// TestARaisedNeedGrowsByAQuarterOfTheGapPerLevel: and the top level is the
// whole machine, never more.
func TestARaisedNeedGrowsByAQuarterOfTheGapPerLevel(t *testing.T) {
	capCores, capMem := capacity(shareMachine)
	cores, mem := 1.0, uint64(5<<30)
	for level := range MaxLevel {
		c, m, whole := Raise(cores, mem, level, shareMachine)
		wantC := cores + (capCores-cores)*float64(level)/MaxLevel
		wantM := mem + uint64(float64(capMem-mem)*float64(level)/MaxLevel)
		if whole || c != wantC || m != wantM {
			t.Errorf("level %d: %v, %d, whole %v; want %v, %d, not whole", level, c, m, whole, wantC, wantM)
		}
	}
	if _, _, whole := Raise(cores, mem, MaxLevel, shareMachine); !whole {
		t.Error("the top level does not ask for the whole machine")
	}
	if _, _, whole := Raise(cores, mem, MaxLevel+3, shareMachine); !whole {
		t.Error("a level past the top does not ask for the whole machine")
	}
	if c, m, _ := Raise(capCores+3, capMem*2, 2, shareMachine); c != capCores+3 || m != capMem*2 {
		t.Errorf("a need past the machine was changed to %v, %d", c, m)
	}
	if c, m, _ := Raise(2, 0, 2, shareMachine); m != 0 || c <= 2 {
		t.Errorf("an unbudgeted memory became %d, cores %v", m, c)
	}
}
