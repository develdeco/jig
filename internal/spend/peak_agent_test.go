package spend

import (
	"path/filepath"
	"testing"
)

// TestPeakOfASessionLeavesOutAnAgentWithNoLineOfItsOwn: an agent whose
// transcript the session's total still holds (it has no spend line yet) adds
// its tokens but not its requests: the session's peak is its own transcript's,
// whichever of the two asked more.
func TestPeakOfASessionLeavesOutAnAgentWithNoLineOfItsOwn(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "s-1.jsonl")
	writeLines(t, path, asst("s-1", "main", "claude-sonnet-4-5", 1000, 0, 0, 0, 0))
	writeLines(t, filepath.Join(dir, "s-1", "subagents", "agent-a-1.jsonl"), asst("s-1", "m-a", "claude-sonnet-4-5", 50000, 0, 0, 0, 0))

	for name, got := range map[string]func() (Totals, error){
		"FromSession":    func() (Totals, error) { return FromSession(path, "s-1", nil, nil) },
		"FromTranscript": func() (Totals, error) { return FromTranscript(path, "s-1", nil) },
	} {
		tot, err := got()
		if err != nil || tot.Peak != 1000 || tot.Input != 51000 {
			t.Errorf("%s: peak %d, input %d, %v, want peak 1000 of the main transcript and input 51000 of both", name, tot.Peak, tot.Input, err)
		}
	}
	agent, err := FromAgent(path, "s-1", "a-1", nil)
	if err != nil || agent.Peak != 50000 {
		t.Errorf("FromAgent peak = %d, %v, want 50000", agent.Peak, err)
	}
}
