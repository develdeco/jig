package machine

import (
	"runtime"
	"testing"
)

// TestReadIsPositive: the real read says the machine has CPUs, and memory on
// the systems that read it.
func TestReadIsPositive(t *testing.T) {
	r := Read()
	if r.CPUs < 1 {
		t.Errorf("CPUs = %d, want at least 1", r.CPUs)
	}
	switch runtime.GOOS {
	case "linux", "darwin", "windows":
		if r.MemoryBytes == 0 {
			t.Errorf("MemoryBytes = 0 on %s, want the total memory", runtime.GOOS)
		}
	}
}
