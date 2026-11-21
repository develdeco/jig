package spend

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/journal"
)

// TestPeakIsTheLargestSingleRequest: the peak of a transcript is the input,
// cache reads and cache writes of its largest message, with the lines of one
// message merged first and output left out.
func TestPeakIsTheLargestSingleRequest(t *testing.T) {
	t.Parallel()
	path := filepath.Join(t.TempDir(), "s-1.jsonl")
	writeLines(t, path,
		asst("s-1", "m1", "claude-sonnet-4-5", 100, 9000, 1000, 200, 0), // 1300
		asst("s-1", "m1", "claude-sonnet-4-5", 100, 9000, 1000, 200, 0), // a repeat is not a second request
		asst("s-1", "m2", "claude-sonnet-4-5", 5, 1, 4000, 300, 0),      // 4305
		asst("s-1", "m3", "claude-sonnet-4-5", 7, 1, 2000, 0, 0),        // 2007
	)
	got, err := FromTranscript(path, "s-1", nil)
	if err != nil || got.Peak != 4305 {
		t.Errorf("Peak = %d, %v, want 4305 (output is not context, repeats are one request)", got.Peak, err)
	}
	if sp := got.Spend(); sp.Peak != 4305 {
		t.Errorf("Spend().Peak = %d, want 4305", sp.Peak)
	}
}

// TestPeakOfAnAgentIsItsOwnTranscript: an agent's peak is its transcript's,
// not the session's or another agent's.
func TestPeakOfAnAgentIsItsOwnTranscript(t *testing.T) {
	t.Parallel()
	path := agentTranscripts(t)
	got, err := FromAgent(path, "s-1", "a-2", nil)
	if err != nil || got.Peak != 10 {
		t.Errorf("agent Peak = %d, %v, want 10", got.Peak, err)
	}
	own, err := FromSession(path, "s-1", map[string]bool{"a-1": true, "a-2": true, "a-3": true}, nil)
	if err != nil || own.Peak != 100000 {
		t.Errorf("session Peak = %d, %v, want 100000 of the main transcript", own.Peak, err)
	}
}

// TestPeakOnASpendLineNeverLowers: the peak rides on the spend line and
// round trips through the journal; a ticket's peak is the largest of its
// lines, an older line that has none is unknown rather than zero, and a later
// line never lowers it.
func TestPeakOnASpendLineNeverLowers(t *testing.T) {
	t.Parallel()
	l := AgentLine("2026-01-01T10:00:00Z", "s", "a-1", "claude", "x", Totals{Input: 1, Peak: 900})
	if l.Spend.Peak != 900 {
		t.Fatalf("line peak = %d, want 900", l.Spend.Peak)
	}
	b, err := json.Marshal(l)
	if err != nil || !strings.Contains(string(b), `"peak":900`) {
		t.Fatalf("json = %s, %v, want the peak recorded", b, err)
	}
	var back journal.Line
	if err := json.Unmarshal(b, &back); err != nil || back.Spend.Peak != 900 {
		t.Fatalf("round trip = %+v, %v", back.Spend, err)
	}
	old := spendLine("2026-01-01T10:00:01Z", "s", 5, 5, 0)
	ob, _ := json.Marshal(old)
	if strings.Contains(string(ob), "peak") {
		t.Errorf("a line with no peak wrote one: %s", ob)
	}
	lower := spendLine("2026-01-01T10:00:02Z", "s", 6, 6, 0)
	lower.Agent = "a-1"
	lower.Spend.Peak = 400
	if got := FromLines([]journal.Line{l, old, lower}).Peak; got != 900 {
		t.Errorf("FromLines Peak = %d, want 900: a later line never lowers it", got)
	}
	if got := FromLines([]journal.Line{old}).Peak; got != 0 {
		t.Errorf("FromLines Peak of a line without one = %d, want 0 (unknown)", got)
	}
	if got := Sum(Totals{Peak: 3}, Totals{Peak: 8}, Totals{}).Peak; got != 8 {
		t.Errorf("Sum Peak = %d, want 8", got)
	}
}
