package verify

import (
	"fmt"
	"strconv"

	"github.com/develdeco/jig/internal/journal"
)

// CPUWords tells a run's CPU time in seconds with one decimal; "" when the
// line carries none.
func CPUWords(cpuMs int64) string {
	if cpuMs <= 0 {
		return ""
	}
	return strconv.FormatFloat(float64(cpuMs)/1000, 'f', 1, 64)
}

// MemoryWords tells a run's peak memory in MiB, or in GiB with one decimal
// from one GiB up, never less than 1 MiB; "" when the line carries none.
func MemoryWords(bytes uint64) string {
	const mib, gib = 1 << 20, 1 << 30
	switch {
	case bytes == 0:
		return ""
	case bytes >= gib:
		return fmt.Sprintf("%.1f GiB", float64(bytes)/gib)
	default:
		return fmt.Sprintf("%d MiB", max(1, (bytes+mib/2)/mib))
	}
}

// BasisWords tells how a peak was measured: the exact committed memory of a
// Windows job, or a sampled lower bound; "" when the line names none.
func BasisWords(basis string) string {
	switch basis {
	case journal.PeakBasisJob:
		return "job"
	case journal.PeakBasisSampled:
		return "sampled peak"
	}
	return basis
}
