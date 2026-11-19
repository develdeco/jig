package main

import (
	"strings"
	"testing"
)

// TestKeepsIgnoreSaysWhatItDid: jig ignore makes jig leave a directory alone
// and says so, says when it already was, and --remove takes it back and says
// when there was nothing to take back.
func TestKeepsIgnoreSaysWhatItDid(t *testing.T) {
	t.Parallel()
	e := testEnv(t.TempDir())
	dir := t.TempDir()

	for _, tc := range []struct {
		args   []string
		status string
	}{
		{[]string{"ignore", dir}, "status: ignored\n"},
		{[]string{"ignore", dir}, "status: already ignored\n"},
		{[]string{"ignore", dir, "--remove"}, "status: tracked again\n"},
		{[]string{"ignore", dir, "--remove"}, "status: not ignored\n"},
	} {
		out, code := runMain(t, e, "", tc.args...)
		if code != 0 || !strings.Contains(out, tc.status) {
			t.Errorf("jig %s: exit %d, want %q\n%s", strings.Join(tc.args, " "), code, tc.status, out)
		}
	}
}

// TestKeepsMapRefusesWhatCannotBeMapped: jig map refuses a repo the project
// does not declare (naming the ones it does), a path that is not a directory,
// and an argument that is not <repo>=<path>; a clone it can map it records.
func TestKeepsMapRefusesWhatCannotBeMapped(t *testing.T) {
	t.Parallel()
	_, _, e := workFixture(t)
	scratch := t.TempDir()

	out, code := runMain(t, e, "", "map", "nope="+scratch)
	if code != 2 || !strings.Contains(out, `declares no repo "nope"`) || !strings.Contains(out, "fixture-repo") {
		t.Errorf("jig map nope=...: exit %d, want 2 naming the repos the project declares:\n%s", code, out)
	}
	out, code = runMain(t, e, "", "map", "fixture-repo="+scratch+"/missing")
	if code != 2 || !strings.Contains(out, "not a directory") {
		t.Errorf("jig map to a missing path: exit %d, want 2 saying it is not a directory:\n%s", code, out)
	}
	if out, code := runMain(t, e, "", "map", "fixture-repo"); code != 2 {
		t.Errorf("jig map fixture-repo (no path): exit %d, want 2:\n%s", code, out)
	}

	out, code = runMain(t, e, "", "map", "fixture-repo="+scratch)
	if code != 0 || !strings.Contains(out, "map:\n  project: fixture\n  repo: fixture-repo\n") {
		t.Errorf("jig map fixture-repo=...: exit %d\n%s", code, out)
	}
}
