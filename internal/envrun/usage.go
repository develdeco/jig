package envrun

import "time"

// The bases a Usage's peak memory is told in. A figure says what it is, so a
// reader never compares one basis with another.
const (
	// BasisJob is the exact peak committed memory of a Windows job object.
	BasisJob = "job"
	// BasisSampled is the largest sum of resident sets of the run's process
	// group seen by samples taken while it lived: a lower bound.
	BasisSampled = "sampled"
)

// Usage is what a run's whole process tree used. A figure that could not be
// read is zero (not measured is not a measurement of zero): CPU 0, PeakBytes 0
// and Basis "" mean it was not measured.
type Usage struct {
	// CPU is the user plus system time of the tree.
	CPU time.Duration
	// PeakBytes is the most memory the tree held at once, on Basis.
	PeakBytes uint64
	// Basis is BasisJob or BasisSampled when PeakBytes is set, else "".
	Basis string
}
