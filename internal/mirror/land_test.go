package mirror

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/mirror/github"
	"github.com/develdeco/jig/internal/store"
)

// TestTheLastCheckpointOfATicketLeavesOriginWithItsPullRequestsRecord is the
// gap itself, at the mirror's seam: a ticket is mirrored while its work
// goes on, then `jig publish` makes its last checkpoint, whose sync finds the
// pull request and writes the record's prs: entry and its synced status
// ("In review") into the working tree. No checkpoint follows to carry them,
// so the checkpoint's own landing has to: origin ends holding the record as
// that sync left it, with the working tree clean and the store level with
// origin. The creation's claim keeps its own commit and push, as before.
func TestTheLastCheckpointOfATicketLeavesOriginWithItsPullRequestsRecord(t *testing.T) {
	st, work, remote := newTestRemoteStore(t)
	addRepoToProjectYAML(t, work)
	mintTestTicket(t, st, "DEMO-1", "First ticket")
	runGit(t, work, "push", "origin", "main")

	cfg := loadCfg(t, st)
	client := &fakeClient{prs: map[string][]github.PullRequest{}}
	var warnings []string
	st.Warn = func(format string, args ...any) { warnings = append(warnings, format) }
	st.AfterCheckpoint = func(s *store.Store) error {
		_, err := Sync(Deps{Store: s, Cfg: cfg, Client: client}, SyncOpts{})
		return err
	}

	// A mid-ticket checkpoint: the ticket gets its issue, and the sync's
	// record writes (synced:, the board item) are landed with it.
	if err := st.Push("DEMO-1: slice a verified"); err != nil {
		t.Fatalf("Push: %v", err)
	}
	// The ticket's pull request opens, and publish makes the last checkpoint:
	// nothing of its own to commit, only the sync's writes.
	client.prs["example/demo@1-first-ticket"] = []github.PullRequest{{Number: 5, NodeID: "PR_5", State: "OPEN"}}
	if err := st.Push("DEMO-1: publish"); err != nil {
		t.Fatalf("Push: %v", err)
	}

	if len(warnings) != 0 {
		t.Fatalf("warnings = %v, want none", warnings)
	}
	rel := filepath.ToSlash(filepath.Join("trackers", "github", "example", "tracking", "1.yaml"))
	shown := runGit(t, remote, "show", "main:"+rel)
	for _, want := range []string{"prs:", "number: 5", "node_id: PR_5", "status: In review"} {
		if !strings.Contains(shown, want) {
			t.Errorf("origin's %s lacks %q:\n%s", rel, want, shown)
		}
	}
	if got := strings.TrimSpace(runGit(t, work, "status", "--porcelain")); got != "" {
		t.Errorf("the sync's writes stayed in the working tree:\n%s", got)
	}
	if local, origin := strings.TrimSpace(runGit(t, work, "rev-parse", "HEAD")), strings.TrimSpace(runGit(t, remote, "rev-parse", "main")); local != origin {
		t.Errorf("origin is at %s, want it level with the store at %s", origin, local)
	}
}
