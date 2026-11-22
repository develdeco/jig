package mirror

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/mirror/github"
)

// TestBindItemKeepsAnOutsideBlockerAndRemovesAStoreOne: a bound item blocked
// on the tracker by an issue the store has no record of and by one it holds
// keeps the first edge and loses the second at the first sync, and the bind
// says so for the second.
func TestBindItemKeepsAnOutsideBlockerAndRemovesAStoreOne(t *testing.T) {
	st, _, _ := newTestRemoteStore(t)
	client := bindClient()
	client.items[50] = github.ItemDetail{Number: 50, NodeID: "NODE_50", Title: "Held", Open: true}
	client.items[48] = github.ItemDetail{
		Number: 48, NodeID: "NODE_48", Title: "Coupons", Open: true,
		BlockedBy: []github.BlockerRef{{Number: 40, NodeID: "NODE_40"}, {Number: 50, NodeID: "NODE_50"}},
	}
	d := bindDeps(st, client)
	held, err := BindItem(context.Background(), d, BindOpts{Ref: ItemRef{Number: 50}, Chart: "held"})
	if err != nil {
		t.Fatal(err)
	}
	res, err := BindItem(context.Background(), d, BindOpts{Ref: ItemRef{Number: 48}, Chart: "shop"})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Advisories) != 1 || !strings.Contains(res.Advisories[0], "#50") || !strings.Contains(res.Advisories[0], held.Issue) ||
		!strings.Contains(res.Advisories[0], "removes the link unless "+held.Issue+" is in the issue's blocked_by") {
		t.Fatalf("advisories = %q, want one naming #50 and %s", res.Advisories, held.Issue)
	}

	client.blockedByOf = map[string][]string{"NODE_48": {"NODE_40", "NODE_50"}}
	if _, err := Sync(d, SyncOpts{}); err != nil {
		t.Fatalf("Sync: %v", err)
	}
	got := client.blockedByOf["NODE_48"]
	if !containsString(got, "NODE_40") || containsString(got, "NODE_50") {
		t.Errorf("blockedByOf = %v, want the outside edge kept and the store one removed", got)
	}
}

// TestBindItemRefusesAMovedItem: an item that answers by another number than
// the ref's is refused before the claim.
func TestBindItemRefusesAMovedItem(t *testing.T) {
	st, work, _ := newTestRemoteStore(t)
	client := bindClient()
	client.items[48] = github.ItemDetail{Number: 77, NodeID: "NODE_77", Title: "Moved", Open: true}
	_, err := BindItem(context.Background(), bindDeps(st, client), BindOpts{Ref: ItemRef{Number: 48}, Chart: "shop"})
	var ae *axi.Error
	if !errors.As(err, &ae) || ae.Code != "ITEM_NOT_FOUND" || !strings.Contains(ae.Msg, "#77") {
		t.Errorf("err = %v, want ITEM_NOT_FOUND naming #77", err)
	}
	for _, dir := range []string{"tickets", "charts", "trackers"} {
		if _, err := os.Stat(filepath.Join(work, dir)); err == nil {
			t.Errorf("a refusal left %s behind", dir)
		}
	}
}

// rejectPushesTo makes the bare remote refuse every push to a ref under
// prefix, as a branch protection would.
func rejectPushesTo(t *testing.T, remote, prefix string) {
	t.Helper()
	hook := "#!/bin/sh\nwhile read old new ref; do\n  case \"$ref\" in\n    " + prefix + "*) echo rejected >&2; exit 1;;\n  esac\ndone\n"
	if err := os.WriteFile(filepath.Join(remote, "hooks", "pre-receive"), []byte(hook), 0o755); err != nil {
		t.Fatal(err)
	}
}

// TestBindItemInWritersModeReleasesTheEntryWhenTheClaimFails: the push of the
// bind's commit is refused after the record and link were written and the
// entry reserved. Nothing of the bind is left (the folders, the record, the
// link), the entry is released so another writer binds the item, and this
// writer's re-run succeeds.
func TestBindItemInWritersModeReleasesTheEntryWhenTheClaimFails(t *testing.T) {
	alice, work, remote := writersBindStore(t)
	d := bindDeps(alice, bindClient())
	head := strings.TrimSpace(runGit(t, work, "rev-parse", "HEAD"))
	rejectPushesTo(t, remote, "refs/heads/writers/")

	_, err := BindItem(context.Background(), d, BindOpts{Ref: ItemRef{Number: 48}, Chart: "shop"})
	if err == nil {
		t.Fatal("BindItem with a refused push: want an error")
	}
	if status := strings.TrimSpace(runGit(t, work, "status", "--porcelain")); status != "" {
		t.Errorf("clone is dirty after the failed bind:\n%s", status)
	}
	if got := strings.TrimSpace(runGit(t, work, "rev-parse", "HEAD")); got != head {
		t.Errorf("HEAD moved from %s to %s", head, got)
	}
	for _, dir := range []string{"tickets", "charts", "trackers"} {
		if left := filesUnder(filepath.Join(work, dir)); len(left) > 0 {
			t.Errorf("the failed bind left %s: %v", dir, left)
		}
	}
	if _, err := os.Stat(filepath.Join(work, "trackers", "github", "example", "tracking", "48")); err == nil {
		t.Errorf("the entry folder survived the failed bind")
	}
	lines, _ := alice.Claims()
	last := lines[len(lines)-1]
	if last.Kind != "entry-release" || last.Entry != "github/example/tracking/48" {
		t.Fatalf("last claims line = %+v, want the entry released", last)
	}

	if err := os.Remove(filepath.Join(remote, "hooks", "pre-receive")); err != nil {
		t.Fatal(err)
	}
	if _, err := BindItem(context.Background(), d, BindOpts{Ref: ItemRef{Number: 48}, Chart: "shop"}); err != nil {
		t.Fatalf("re-run: %v", err)
	}
}

// filesUnder lists what is under dir that is not a folder: an emptied parent
// folder is untracked and no trace of a bind, a file or a link is.
func filesUnder(dir string) []string {
	var out []string
	_ = filepath.Walk(dir, func(p string, fi os.FileInfo, _ error) error {
		if fi != nil && !fi.IsDir() {
			out = append(out, p)
		}
		return nil
	})
	return out
}
