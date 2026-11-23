package main

import "testing"

// TestTheVerbOfAnArgvIgnoresTheProgramsName: git's trace names the program as it
// was started (git, git.exe, a path), so the verb is the first word after it that
// is no option, and the counts of the overlap tests hold on any git layout.
func TestTheVerbOfAnArgvIgnoresTheProgramsName(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		argv []string
		want string
	}{
		{[]string{"git", "-c", "maintenance.auto=false", "diff", "--name-only"}, "diff"},
		{[]string{"git.exe", "-c", "maintenance.auto=false", "merge-tree", "--write-tree"}, "merge-tree"},
		{[]string{`C:\Program Files\Git\mingw64\bin\git.exe`, "-c", "maintenance.auto=false", "-c", "x.y=z", "for-each-ref"}, "for-each-ref"},
		{[]string{"git"}, ""},
	} {
		if got := verbOf(c.argv); got != c.want {
			t.Errorf("verbOf(%q) = %q, want %q", c.argv, got, c.want)
		}
	}
	if got := askedOf([][]string{{"git.exe", "diff"}, {"git", "diff"}, {"git.exe", "log"}}, "diff"); got != 2 {
		t.Errorf("askedOf counted %d diffs, want 2", got)
	}
}
