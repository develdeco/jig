package tui

import (
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/axi"
)

// TestStatusSlicesCarryTheFindingsOfAFixSlice: the slices table's findings
// column is read into the slice as a list of ids, "-" is none, and a status that
// predates the column reads as none rather than failing.
func TestStatusSlicesCarryTheFindingsOfAFixSlice(t *testing.T) {
	t.Parallel()
	cols := []string{"id", "state", "attempts", "blocked_by", "question", "goal", "workspace", "oracle", "from_gate"}
	with := "ticket: JIG-9\nstate: building\n" + axi.Table("slices", append(append([]string{}, cols...), "findings"), [][]string{
		{"a", "green", "1", "-", "-", "Build it", "root", "go test", "-", "-"},
		{"fix-1", "queued", "0", "a", "-", "Fix it", "root", "go test", "2", "r1-f1+r1-f2"},
	})
	got := parseStatus(with).Slices
	if len(got) != 2 || len(got[0].Findings) != 0 || strings.Join(got[1].Findings, ",") != "r1-f1,r1-f2" || got[1].FromGate != 2 {
		t.Errorf("slices with the column = %+v", got)
	}
	without := "ticket: JIG-9\nstate: building\n" + axi.Table("slices", cols, [][]string{
		{"fix-1", "queued", "0", "a", "-", "Fix it", "root", "go test", "2"},
	})
	got = parseStatus(without).Slices
	if len(got) != 1 || len(got[0].Findings) != 0 || got[0].FromGate != 2 {
		t.Errorf("slices without the column = %+v", got)
	}
}
