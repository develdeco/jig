package setup

import (
	"io/fs"
	"strings"
	"testing"

	"github.com/develdeco/jig/skills"
)

// TestKeepsTheStubNeverAsksWhetherToTakeTheTickets: the stub jig attach writes
// carries no question whether to take a chart's tickets on.
func TestKeepsTheStubNeverAsksWhetherToTakeTheTickets(t *testing.T) {
	t.Parallel()
	dirs, err := fs.ReadDir(skills.FS, ".")
	if err != nil {
		t.Fatal(err)
	}
	var entries []StubEntry
	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		body, err := fs.ReadFile(skills.FS, d.Name()+"/SKILL.md")
		if err != nil {
			t.Fatal(err)
		}
		entries = append(entries, StubEntry{Name: d.Name(), Summary: SkillSummary(body)})
	}
	stub := string(StubBody(entries))
	if stub == "" {
		t.Fatal("the stub is empty")
	}
	if strings.Contains(stub, "ask whether to take its tickets on") {
		t.Errorf("the stub still asks whether to take the tickets on:\n%s", stub)
	}
}
