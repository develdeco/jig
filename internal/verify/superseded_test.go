package verify

import (
	"testing"

	"github.com/develdeco/jig/internal/journal"
)

// TestSupersededFollowsTheCommitsNotTheJournal: the outcome of a newer commit
// of the same slice makes an older commit's history, whichever was journaled
// first; another slice's outcome, an unordered commit or the commit itself do
// not.
func TestSupersededFollowsTheCommitsNotTheJournal(t *testing.T) {
	t.Parallel()
	// c1 <- c2 <- c3 on one line of history; x is on no line with them.
	parent := map[string]string{"c2": "c1", "c3": "c2"}
	isAncestor := func(a, d string) bool {
		for c := d; c != ""; c = parent[c] {
			if c == a {
				return true
			}
		}
		return false
	}
	lines := []journal.Line{
		{Event: journal.EventOracle, Slice: "b", Commit: "c2", Outcome: "pass"},
		{Event: journal.EventVerified, Slice: "b", Commit: "c2"},
		{Event: journal.EventOracle, Slice: "b", Commit: "c1", Outcome: "fail"},
		{Event: journal.EventOracle, Slice: "a", Commit: "c3", Outcome: "pass"},
		{Event: journal.EventVerifyPending, Slice: "b", Commit: "c3"},
		{Event: journal.EventOracle, Slice: "b", Commit: "x", Outcome: "fail"},
	}
	cases := []struct {
		name  string
		slice string
		sha   string
		want  bool
	}{
		{"an older commit, though journaled after the newer one", "b", "c1", true},
		{"the newest commit with an outcome", "b", "c2", false},
		{"a newer commit only pending, nothing outcome yet", "b", "c3", false},
		{"a commit git cannot order against the others", "b", "x", false},
	}
	for _, c := range cases {
		if got := Superseded(lines, c.slice, c.sha, isAncestor); got != c.want {
			t.Errorf("%s: Superseded(%s, %s) = %v, want %v", c.name, c.slice, c.sha, got, c.want)
		}
	}
	if Superseded([]journal.Line{{Event: journal.EventOracle, Slice: "a", Commit: "c3", Outcome: "pass"}}, "b", "c1", isAncestor) {
		t.Error("another slice's newer outcome made slice b's history")
	}
	if Superseded(lines, "b", "c1", nil) {
		t.Error("with no way to order commits, nothing is history")
	}
}
