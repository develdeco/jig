package gitx

import "testing"

// standingStream is one fast-import stream of four branches: base, ahead (base
// and one more), side (base and a different one) and other (a root of its
// own, sharing nothing).
const standingStream = `commit refs/heads/base
committer t <t@example.invalid> 1700000000 +0000
data 4
base
M 100644 inline f.txt
data 4
base

commit refs/heads/ahead
committer t <t@example.invalid> 1700000100 +0000
data 5
ahead
from refs/heads/base
M 100644 inline g.txt
data 5
ahead

commit refs/heads/side
committer t <t@example.invalid> 1700000200 +0000
data 4
side
from refs/heads/base
M 100644 inline h.txt
data 4
side

commit refs/heads/other
committer t <t@example.invalid> 1700000300 +0000
data 5
other
M 100644 inline o.txt
data 5
other

`

// byAncestry is the answer Standing replaced: the tips equal, or one ancestry
// question each way.
func byAncestry(t *testing.T, dir, a, b string) string {
	t.Helper()
	ancAB, err := IsAncestor(dir, a, b)
	if err != nil {
		t.Fatal(err)
	}
	ancBA, err := IsAncestor(dir, b, a)
	if err != nil {
		t.Fatal(err)
	}
	switch {
	case ancAB && ancBA:
		return "equal"
	case ancAB:
		return "behind"
	case ancBA:
		return "ahead"
	}
	return "diverged"
}

// byCounts reads Standing's counts the way pool.Compare does.
func byCounts(ahead, behind int) string {
	switch {
	case ahead == 0 && behind == 0:
		return "equal"
	case ahead == 0:
		return "behind"
	case behind == 0:
		return "ahead"
	}
	return "diverged"
}

// TestStandingMatchesTheTwoAncestryCalls: for tips that are equal, behind,
// ahead, forked and unrelated, the counts say what merge-base --is-ancestor
// asked both ways does, and are the commits each side has the other lacks. An
// unknown revision is an error.
func TestStandingMatchesTheTwoAncestryCalls(t *testing.T) {
	dir := streamRepo(t, "standing", standingStream)
	for _, c := range []struct {
		name, a, b, want string
		ahead, behind    int
	}{
		{"equal", "base", "base", "equal", 0, 0},
		{"behind", "base", "ahead", "behind", 0, 1},
		{"ahead", "ahead", "base", "ahead", 1, 0},
		{"forked", "ahead", "side", "diverged", 1, 1},
		{"unrelated", "base", "other", "diverged", 1, 1},
	} {
		if got := byAncestry(t, dir, c.a, c.b); got != c.want {
			t.Fatalf("%s: the two ancestry calls say %s, the case says %s", c.name, got, c.want)
		}
		ahead, behind, err := Standing(dir, c.a, c.b)
		if err != nil {
			t.Fatalf("%s: Standing: %v", c.name, err)
		}
		if got := byCounts(ahead, behind); got != c.want {
			t.Errorf("%s: Standing = %d ahead, %d behind, which is %s; the two ancestry calls say %s", c.name, ahead, behind, got, c.want)
		}
		if ahead != c.ahead || behind != c.behind {
			t.Errorf("%s: Standing = %d, %d; want %d, %d", c.name, ahead, behind, c.ahead, c.behind)
		}
	}
	if _, _, err := Standing(dir, "base", "no-such-rev"); err == nil {
		t.Error("an unknown revision was not an error")
	}
}
