package main

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/gitx"
)

// TestBoundItemChain is the whole path over a real origin and clone with a
// fake client: a bind of #48, the checkpoint sync after it, which opens no
// second issue and updates #48 in place by node id with no drift line, a sync
// run on demand, and a second bind of the
// item refused naming the issue and its chart, with the origin untouched.
func TestBoundItemChain(t *testing.T) {
	t.Parallel()
	e := testEnv(t.TempDir())
	clone := filepath.Join(t.TempDir(), "clone")
	remote := newTestOriginCloneWithGitHubTracker(t, clone)
	stub := itemStub()
	e.mirrorClient = stub

	out, code := runMain(t, e, "", "chart", "new", "shop", "--item", "48", "--store", clone)
	if code != 0 {
		t.Fatalf("bind: exit %d\n%s", code, out)
	}
	if strings.Contains(out, "drift") {
		t.Errorf("the first sync reported drift on a bound item:\n%s", out)
	}
	if stub.created != 0 || len(stub.updated) != 1 || stub.updated[0] != "NODE_48" {
		t.Fatalf("after the bind: created %d, updated %v; want #48 updated in place and nothing opened", stub.created, stub.updated)
	}
	data, ok := treeRecordOf(t, clone, "DEMO-1")
	if !ok || !strings.Contains(data, "issue: 48") || !strings.Contains(data, "synced:") {
		t.Errorf("record after the first sync = %q, %v, want #48's with the sync's own state", data, ok)
	}

	out, code = runMain(t, e, "", "trackers", "sync", "--store", clone)
	if code != 0 {
		t.Fatalf("second sync: exit %d\n%s", code, out)
	}
	if stub.created != 0 {
		t.Errorf("second sync opened %d issues", stub.created)
	}

	head, err := gitx.Run("", "--git-dir", remote, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if out, code := runMain(t, e, "", "chart", "new", "shop", "--item", "48", "--store", clone); code == 0 || !strings.Contains(out, "TRACKER_ENTRY_TAKEN") {
		t.Errorf("rebind under the bound chart's own name: exit %d, want TRACKER_ENTRY_TAKEN, not a name refusal\n%s", code, out)
	}
	out, code = runMain(t, e, "", "chart", "new", "again", "--item", "#48", "--store", clone)
	if code == 0 || !strings.Contains(out, "TRACKER_ENTRY_TAKEN") || !strings.Contains(out, "DEMO-1 (chart shop)") {
		t.Errorf("second bind: exit %d, want TRACKER_ENTRY_TAKEN naming DEMO-1 and its chart\n%s", code, out)
	}
	if now, _ := gitx.Run("", "--git-dir", remote, "rev-parse", "HEAD"); now != head {
		t.Errorf("the refused bind moved the origin from %s to %s", head, now)
	}
	if stub.created != 0 {
		t.Errorf("the refused bind opened %d issues", stub.created)
	}
}
