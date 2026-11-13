//go:build !linux && !darwin && !windows

package machine

// totalMemory is 0 where this system's total memory is not read: memory is
// then not budgeted.
func totalMemory() uint64 { return 0 }
