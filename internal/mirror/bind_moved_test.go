package mirror

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/mirror/github"
	"github.com/develdeco/jig/internal/store"
)

// TestBindItemRefusesAMovedItem: an item that answers by another number, or
// from another repository under the same number, is refused before the claim
// with help that points at where it lives and suggests no --item.
func TestBindItemRefusesAMovedItem(t *testing.T) {
	for name, answer := range map[string]github.ItemDetail{
		"another number":     {Number: 77, Repo: "example/tracking", NodeID: "NODE_77", Title: "Moved", URL: "https://github.com/example/tracking/issues/77", Open: true},
		"another repository": {Number: 48, Repo: "other/place", NodeID: "NODE_X", Title: "Moved", URL: "https://github.com/other/place/issues/48", Open: true},
	} {
		t.Run(name, func(t *testing.T) {
			st, work, _ := newTestRemoteStore(t)
			client := bindClient()
			client.items[48] = answer
			_, err := BindItem(context.Background(), bindDeps(st, client), BindOpts{Ref: ItemRef{Number: 48}, Chart: "shop"})
			var ae *axi.Error
			if !errors.As(err, &ae) || ae.Code != "ITEM_NOT_FOUND" || !strings.Contains(ae.Msg, answer.URL) {
				t.Fatalf("err = %v, want ITEM_NOT_FOUND naming %s", err, answer.URL)
			}
			if help := strings.Join(ae.Help, " "); strings.Contains(help, "--item") || !strings.Contains(help, answer.Repo) {
				t.Errorf("help = %q, want where the item lives and no --item", help)
			}
			for _, dir := range []string{"tickets", "charts", "trackers"} {
				if _, err := os.Stat(filepath.Join(work, dir)); err == nil {
					t.Errorf("a refusal left %s behind", dir)
				}
			}
		})
	}
}

// TestBindItemAdvisesTheLinksTheFirstSyncRemoves: a bound item that blocks a
// store issue and is blocked by an issue of a record whose ticket is gone is
// told what the first sync does with each link, and the sync then does it:
// the one definition of held serves both.
func TestBindItemAdvisesTheLinksTheFirstSyncRemoves(t *testing.T) {
	st, _, _ := newTestRemoteStore(t)
	client := bindClient()
	client.items[50] = github.ItemDetail{Number: 50, NodeID: "NODE_50", Title: "Held", Open: true}
	client.items[48] = github.ItemDetail{
		Number: 48, NodeID: "NODE_48", Title: "Coupons", Open: true,
		BlockedBy: []github.BlockerRef{{Number: 60, NodeID: "NODE_60"}},
		Blocking:  []github.BlockerRef{{Number: 50, NodeID: "NODE_50"}},
	}
	d := bindDeps(st, client)
	held, err := BindItem(context.Background(), d, BindOpts{Ref: ItemRef{Number: 50}, Chart: "held"})
	if err != nil {
		t.Fatal(err)
	}
	// An orphan record: its ticket does not exist, its record is in the tree.
	orphan := githubEntry("example/tracking", 60)
	fields := githubRecord{Repo: "example/tracking", Issue: 60, NodeID: "NODE_60"}
	var node yaml.Node
	if err := node.Encode(fields); err != nil {
		t.Fatal(err)
	}
	if err := st.WriteTrackerRecord(orphan, store.TrackerOwner{Ticket: "DEMO-77"}, &node, recordHeader); err != nil {
		t.Fatal(err)
	}

	res, err := BindItem(context.Background(), d, BindOpts{Ref: ItemRef{Number: 48}, Chart: "shop"})
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(res.Advisories, "\n")
	for _, want := range []string{
		"blocked on the tracker by #60, which is DEMO-77; the first sync removes that link unless DEMO-77 is in " + res.Issue + "'s blocked_by",
		"48 blocks #50 on the tracker, which is " + held.Issue + "; the first sync removes that link unless " + res.Issue + " is in " + held.Issue + "'s blocked_by",
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("advisories lack %q:\n%s", want, joined)
		}
	}

	client.blockedByOf = map[string][]string{"NODE_48": {"NODE_60"}, "NODE_50": {"NODE_48"}}
	if _, err := Sync(d, SyncOpts{}); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	if containsString(client.blockedByOf["NODE_48"], "NODE_60") || containsString(client.blockedByOf["NODE_50"], "NODE_48") {
		t.Errorf("blockedByOf = %v, want both advised links removed", client.blockedByOf)
	}
}
