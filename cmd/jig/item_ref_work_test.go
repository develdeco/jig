package main

import (
	"path/filepath"
	"strings"
	"testing"
)

// TestItemRefWork: a verb that resolves a ticket through resolveWork acts on a
// bound ref's owner and names it, and refuses an unbound ref with its own code,
// never "none was named".
func TestItemRefWork(t *testing.T) {
	t.Parallel()
	e, clone := boundItemClone(t)
	e.mirrorClient = &noCallsClient{t: t}

	out, code := runMain(t, e, "", "context", "48", "--store", clone)
	if !strings.Contains(out, "48 is now DEMO-1\n") || !strings.Contains(out, "DEMO-1") || strings.Contains(out, "none was named") {
		t.Errorf("a bound ref: exit %d, want the verb to act on DEMO-1 and name it:\n%s", code, out)
	}

	out, code = runMain(t, e, "", "context", "49", "--store", clone)
	if code == 0 || !strings.Contains(out, "code: ITEM_NOT_A_TICKET") || !strings.Contains(out, "no ticket is bound to tracker item 49") || !strings.Contains(out, "Run `jig next 49` to read it") || strings.Contains(out, "none was named") {
		t.Errorf("an unbound ref: exit %d, want its own refusal:\n%s", code, out)
	}
}

// TestItemRefNoTracker: with no GitHub tracker an all-digit word is read as it
// was before refs, by every verb that takes one.
func TestItemRefNoTracker(t *testing.T) {
	t.Parallel()
	e := testEnv(t.TempDir())
	bare := filepath.Join(t.TempDir(), "bare")
	newTestOriginClone(t, bare)
	for _, verb := range []string{"next", "here", "context"} {
		out, _ := runMain(t, e, "", verb, "49", "--store", bare)
		if strings.Contains(out, "trackers:") || strings.Contains(out, "not a tracker item") {
			t.Errorf("jig %s 49 with no tracker gives the tracker refusal:\n%s", verb, out)
		}
	}
}
