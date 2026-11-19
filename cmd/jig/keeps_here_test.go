package main

import (
	"strings"
	"testing"
)

// TestKeepsHereIsSilentOutsideAProject: a directory in no project gets no
// output at all and exit 0, so a hook can run jig here everywhere; --explain is
// the one way to ask how that was found.
func TestKeepsHereIsSilentOutsideAProject(t *testing.T) {
	t.Parallel()
	ie := newInstallerEnv(t)
	e := ie.env.inDir(ie.root)
	for _, args := range [][]string{{"here"}, {"here", "--json"}, {"here", "JIG-1"}} {
		if out, code := runMain(t, e, "", args...); code != 0 || out != "" {
			t.Errorf("jig %s outside a project: exit %d, output %q, want 0 and nothing", strings.Join(args, " "), code, out)
		}
	}
	if out, code := runMain(t, e, "", "here", "--explain"); code != 0 || !strings.Contains(out, "none:") {
		t.Errorf("jig here --explain outside a project: exit %d, output %q, want the explain block", code, out)
	}
}
