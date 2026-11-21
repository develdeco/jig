//go:build !windows

package envrun

import "testing"

// TestGroupRSSKiBSumsOnlyTheRunsGroup: a ps listing is summed for the run's
// process group alone, whatever the column widths, and a line that is not a
// pair of numbers is skipped.
func TestGroupRSSKiBSumsOnlyTheRunsGroup(t *testing.T) {
	out := "  100  2048\n  100   512\n  200 99999\nnot a line\n  100 abc\n\n 100 1\n"
	if got, want := groupRSSKiB(out, 100), uint64(2048+512+1); got != want {
		t.Errorf("group 100 = %d KiB, want %d", got, want)
	}
	if got := groupRSSKiB(out, 300); got != 0 {
		t.Errorf("a group with no process = %d, want 0", got)
	}
}
