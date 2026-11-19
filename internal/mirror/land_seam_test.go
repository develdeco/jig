package mirror

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/mirror/github"
)

// treeProbeClient looks at the store's working tree each time the sync asks
// GitHub to place a board item, which the sync does after the record it just
// wrote.
type treeProbeClient struct {
	*fakeClient
	root   string
	probes int
	dirty  []string
}

func (c *treeProbeClient) PlaceItem(ctx context.Context, p github.ProjectV2, contentID, status, storeID string) (string, error) {
	c.probes++
	if out, err := gitx.Run(c.root, "status", "--porcelain"); err != nil {
		c.dirty = append(c.dirty, err.Error())
	} else if strings.TrimSpace(out) != "" {
		c.dirty = append(c.dirty, out)
	}
	return c.fakeClient.PlaceItem(ctx, p, contentID, status, storeID)
}

// TestSeamASyncLandsEverythingTheStoreHeldAndKeepsItsWritesOutOfTheTree: the
// rule behind `jig trackers sync`. The store holds a ticket with no issue and
// a spend line a session wrote outside any checkpoint. The sync's own writes
// are not in the working tree while it runs (another builder's claim on the
// clone never meets them unstaged), and the landing that follows commits the
// leftover and pushes the lot: the tree is clean, origin is level with the
// store, and origin holds the spend line and the record as the sync left it.
func TestSeamASyncLandsEverythingTheStoreHeldAndKeepsItsWritesOutOfTheTree(t *testing.T) {
	st, work, remote := newTestRemoteStore(t)
	addRepoToProjectYAML(t, work)
	mintTestTicket(t, st, "DEMO-1", "First ticket")
	runGit(t, work, "push", "origin", "main")

	journal := filepath.Join(work, "tickets", "DEMO-1", "journal.ndjson")
	spend := `{"ts":"2026-10-28T10:00:00Z","ticket":"DEMO-1","event":"spend","session":"s-1"}` + "\n"
	if err := os.WriteFile(journal, []byte(spend), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg := loadCfg(t, st)
	// The sequence `jig trackers sync` runs: commit and push the leftovers,
	// hold the sync's writes, sync, land.
	if err := st.Sync(); err != nil {
		t.Fatalf("Store.Sync: %v", err)
	}
	st.HoldTrackerWrites()
	probe := &treeProbeClient{fakeClient: &fakeClient{prs: map[string][]github.PullRequest{}}, root: work}
	if _, err := Sync(Deps{Store: st, Cfg: cfg, Client: probe}, SyncOpts{Unbounded: true}); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if probe.probes == 0 {
		t.Fatal("the sync never placed a board item, so the working tree was never looked at")
	}
	if len(probe.dirty) != 0 {
		t.Errorf("the working tree held the sync's writes before the landing:\n%s", strings.Join(probe.dirty, "\n"))
	}
	if err := st.LandTrackerWrites(); err != nil {
		t.Fatalf("LandTrackerWrites: %v", err)
	}

	if got := strings.TrimSpace(runGit(t, work, "status", "--porcelain")); got != "" {
		t.Errorf("the store is left dirty:\n%s", got)
	}
	if local, origin := strings.TrimSpace(runGit(t, work, "rev-parse", "HEAD")), strings.TrimSpace(runGit(t, remote, "rev-parse", "main")); local != origin {
		t.Errorf("origin is at %s, want it level with the store at %s", origin, local)
	}
	if shown := runGit(t, remote, "show", "main:tickets/DEMO-1/journal.ndjson"); !strings.Contains(shown, `"event":"spend"`) {
		t.Errorf("origin's journal = %q, want the spend line the landing committed", shown)
	}
	rel := filepath.ToSlash(filepath.Join("trackers", "github", "example", "tracking", "1", "record.yaml"))
	if shown := runGit(t, remote, "show", "main:"+rel); !strings.Contains(shown, "synced:") {
		t.Errorf("origin's record lacks the sync's synced: fields:\n%s", shown)
	}
}
