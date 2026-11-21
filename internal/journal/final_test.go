package journal

import "testing"

// TestFinalRunAtAnExactHeadOnly: the final run at a head is the latest
// covering final-oracle line of that repo at exactly that commit. A pass on
// another commit (even one with the same tree), an uncovered run, a run jig's
// stop ended, a slice's own oracle line and another repo's run are none.
func TestFinalRunAtAnExactHeadOnly(t *testing.T) {
	t.Parallel()
	final := func(commit, outcome string, mod func(*Line)) Line {
		l := Line{Event: EventFinalOracle, Commit: commit, Outcome: outcome, Command: "go test ./a", HeadEnd: commit}
		if mod != nil {
			mod(&l)
		}
		return l
	}
	for _, c := range []struct {
		name  string
		lines []Line
		repo  string
		head  string
		want  string // the outcome, "" for none
	}{
		{"no lines", nil, "", shaA, ""},
		{"a pass at the head", []Line{final(shaA, "pass", nil)}, "", shaA, "pass"},
		{"a fail at the head", []Line{final(shaA, "fail", nil)}, "", shaA, "fail"},
		{"a pass at another commit is none", []Line{final(shaB, "pass", nil)}, "", shaA, ""},
		{"the latest run wins", []Line{final(shaA, "fail", nil), final(shaA, "pass", nil)}, "", shaA, "pass"},
		{"a later run at another commit changes nothing", []Line{final(shaA, "pass", nil), final(shaB, "fail", nil)}, "", shaA, "pass"},
		{"an uncovered run is none", []Line{final(shaA, "pass", func(l *Line) { l.Uncovered = UncoveredDirtyAfter })}, "", shaA, ""},
		{"an uncovered later run leaves the covering one", []Line{final(shaA, "pass", nil), final(shaA, "fail", func(l *Line) { l.Uncovered = UncoveredHeadMoved })}, "", shaA, "pass"},
		{"a run jig stopped is none", []Line{final(shaA, "fail", func(l *Line) { l.Cause = CauseStopped })}, "", shaA, ""},
		{"a slice's oracle line is not a final run", []Line{{Event: EventOracle, Slice: "a", Commit: shaA, Outcome: "pass"}}, "", shaA, ""},
		{"another repo's run is none", []Line{final(shaA, "pass", func(l *Line) { l.Repo = "web" })}, "api", shaA, ""},
		{"the repo's own run", []Line{final(shaA, "pass", func(l *Line) { l.Repo = "web" })}, "web", shaA, "pass"},
		{"an empty head is none", []Line{final("", "pass", nil)}, "", "", ""},
	} {
		got, ok := FinalRunAt(c.lines, c.repo, c.head)
		if ok != (c.want != "") || got.Outcome != c.want {
			t.Errorf("%s: FinalRunAt = %q, %v, want %q", c.name, got.Outcome, ok, c.want)
		}
	}
}
