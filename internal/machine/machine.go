// Package machine reads what the machine running jig has: its logical CPUs
// and its total physical memory. Both are read when a heavy run asks for its
// share (internal/slot), never kept, since they are what the share is cut
// from.
package machine

import "runtime"

// Reading is what the machine has.
type Reading struct {
	// CPUs is the number of logical CPUs the process may use.
	CPUs int
	// MemoryBytes is the total physical memory, 0 when this system does not
	// say (memory is then not budgeted).
	MemoryBytes uint64
}

// Read reads the machine as it is now.
func Read() Reading {
	return Reading{CPUs: runtime.NumCPU(), MemoryBytes: totalMemory()}
}
