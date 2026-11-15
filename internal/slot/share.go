package slot

import (
	"fmt"
	"slices"
	"time"

	"github.com/develdeco/jig/internal/machine"
)

// The constants of the share rule. They are the same for any project and not a
// setting; they live here together so a later change can make them measured.
const (
	// SlowFactor is how many times its alone time a shared run may take and
	// still be within the factor.
	SlowFactor = 1.5
	// AloneRuns is how many of the latest runs that did not share make the
	// alone figure (their median wall time).
	AloneRuns = 3
	// SharedRuns is how many of the latest shared runs the rule reads, so an
	// old slowdown ages out.
	SharedRuns = 6
	// DecayRuns is how many consecutive runs that did not share give back one
	// level: a project held at a high level mostly runs alone, so it could
	// never recover on shared runs.
	DecayRuns = 5
	// MaxLevel is the highest level; it asks for the whole machine.
	MaxLevel = 4
)

// Run is one recorded heavy run of a project's oracle command on a machine, as
// the rule reads it.
type Run struct {
	// Wall is how long the run took; a run with no wall time is not read.
	Wall time.Duration
	// Shared is whether another heavy run held a share at any moment of it.
	Shared bool
	// CPUs and MemBytes are the machine's counts it was measured on.
	CPUs     int
	MemBytes uint64
}

// Verdict is what the rule found in a project's runs.
type Verdict struct {
	// Level is how much extra share the next run asks for, 0 to MaxLevel.
	Level int
	// Shared is how many shared runs the rule read, and Slow how many of those
	// ran past the factor.
	Shared, Slow int
}

// Why is the plain reason a run asks for more than it measured, "" at level 0.
func (v Verdict) Why() string {
	if v.Level == 0 {
		return ""
	}
	return fmt.Sprintf("asks for more of the machine because %d of its last %d shared runs ran over %g times the alone time", v.Slow, v.Shared, SlowFactor)
}

// Level is Judge's level.
func Level(runs []Run, m machine.Reading) int { return Judge(runs, m).Level }

// Judge reads a project's recorded runs of one oracle command, oldest first,
// against the machine m as it is now. Runs measured on other counts of CPUs
// or memory are not read, so a changed machine starts at level 0, and so does
// a project with no run that ran alone to compare with. The rule is a pure
// function of its input.
//
// Folding the runs oldest first, a shared run past SlowFactor times the alone
// figure adds a level, one within it removes a level, and every DecayRuns
// consecutive runs that did not share remove one more. Only the last
// SharedRuns shared runs, and the runs after the first of them, count.
func Judge(runs []Run, m machine.Reading) Verdict {
	var same []Run
	for _, r := range runs {
		if r.Wall > 0 && r.CPUs == m.CPUs && r.MemBytes == m.MemoryBytes {
			same = append(same, r)
		}
	}
	var alone []time.Duration
	for _, r := range same {
		if !r.Shared {
			alone = append(alone, r.Wall)
		}
	}
	if len(alone) == 0 {
		return Verdict{}
	}
	alone = alone[max(0, len(alone)-AloneRuns):]
	slices.Sort(alone)
	figure := alone[len(alone)/2]
	if len(alone)%2 == 0 {
		figure = (alone[len(alone)/2-1] + alone[len(alone)/2]) / 2
	}
	limit := time.Duration(float64(figure) * SlowFactor)

	start, shared := len(same), 0
	for start > 0 && shared < SharedRuns {
		start--
		if same[start].Shared {
			shared++
		}
	}
	var v Verdict
	streak := 0
	for _, r := range same[start:] {
		switch {
		case !r.Shared:
			if streak++; streak == DecayRuns {
				streak = 0
				v.Level = max(v.Level-1, 0)
			}
		case r.Wall > limit:
			streak = 0
			v.Shared++
			v.Slow++
			v.Level = min(v.Level+1, MaxLevel)
		default:
			streak = 0
			v.Shared++
			v.Level = max(v.Level-1, 0)
		}
	}
	return v
}

// Raise is the need of cores and peak memory mem raised by level against
// machine m: each level adds a quarter of the gap to what the budget has of
// the machine, and the top level asks for all of it (whole), which runs alone.
// A need that is not budgeted (mem 0) stays so, and one already past the
// machine is left as it is.
func Raise(cores float64, mem uint64, level int, m machine.Reading) (raisedCores float64, raisedMem uint64, whole bool) {
	level = max(0, min(level, MaxLevel))
	if level == MaxLevel {
		return cores, mem, true
	}
	capCores, capMem := capacity(m)
	f := float64(level) / MaxLevel
	if gap := capCores - cores; gap > 0 {
		cores += gap * f
	}
	if mem > 0 && capMem > mem {
		mem += uint64(float64(capMem-mem) * f)
	}
	return cores, mem, false
}
