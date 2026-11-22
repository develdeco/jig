package mirror

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/store"
)

// writersBindStore is a store with an origin made a writers store for alice.
func writersBindStore(t *testing.T) (alice *store.Store, work, remote string) {
	t.Helper()
	alice, work, remote = newTestRemoteStore(t)
	runGit(t, work, "config", "jig.writer", "alice")
	if _, err := alice.MigrateToWriters(); err != nil {
		t.Fatalf("MigrateToWriters: %v", err)
	}
	return alice, work, remote
}

// entryLinesOf is the claims log's entry lines.
func entryLinesOf(t *testing.T, st *store.Store) []store.ClaimLine {
	t.Helper()
	lines, err := st.Claims()
	if err != nil {
		t.Fatal(err)
	}
	var out []store.ClaimLine
	for _, l := range lines {
		if l.Kind == store.ClaimEntry {
			out = append(out, l)
		}
	}
	return out
}

// TestBindItemInWritersModeRefusesTheSecondWriter: two writers binding one
// item, the second not having taken the first's branch, is refused naming the
// first's issue, and nothing of the second's stays: no ticket, chart or entry
// folder, and its head did not move.
func TestBindItemInWritersModeRefusesTheSecondWriter(t *testing.T) {
	alice, _, remote := writersBindStore(t)
	bobDir := filepath.Join(t.TempDir(), "bob")
	runGit(t, "", "clone", remote, bobDir)
	runGit(t, bobDir, "config", "user.name", "bob person")
	runGit(t, bobDir, "config", "user.email", "bob@example.invalid")
	runGit(t, bobDir, "config", "jig.writer", "bob")
	enableSymlinks(t, bobDir)
	bob, err := store.Open(bobDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := bob.Sync(); err != nil {
		t.Fatal(err)
	}

	res, err := BindItem(context.Background(), bindDeps(alice, bindClient()), BindOpts{Ref: ItemRef{Number: 48}, Chart: "shop"})
	if err != nil {
		t.Fatalf("alice BindItem: %v", err)
	}
	if lines := entryLinesOf(t, alice); len(lines) != 1 || lines[0].Entry != "github/example/tracking/48" || lines[0].Ticket != res.Issue {
		t.Fatalf("alice's entry lines = %+v, want one for %s", lines, res.Issue)
	}

	head := strings.TrimSpace(runGit(t, bobDir, "rev-parse", "HEAD"))
	_, err = BindItem(context.Background(), bindDeps(bob, bindClient()), BindOpts{Ref: ItemRef{Number: 48}, Chart: "bobshop"})
	var ae *axi.Error
	if !errors.As(err, &ae) || ae.Code != "TRACKER_ENTRY_TAKEN" || !strings.Contains(ae.Msg, res.Issue) {
		t.Fatalf("bob BindItem err = %v, want TRACKER_ENTRY_TAKEN naming %s", err, res.Issue)
	}
	if got := strings.TrimSpace(runGit(t, bobDir, "rev-parse", "HEAD")); got != head {
		t.Errorf("bob's HEAD moved from %s to %s", head, got)
	}
	if status := strings.TrimSpace(runGit(t, bobDir, "status", "--porcelain")); status != "" {
		t.Errorf("bob's clone is dirty after the refusal:\n%s", status)
	}
	// An emptied parent folder is not tracked and is no trace of the bind.
	for _, p := range []string{"tickets", "charts", "trackers"} {
		if left := dirListing(filepath.Join(bobDir, p)); len(left) > 1 {
			t.Errorf("the refused bind left %s in bob's clone: %v", p, left)
		}
	}
}

// TestBindItemInWritersModeBindsAgainAfterAFailedBind: a bind that fails after
// its reservation (the chart cannot be made) leaves no line that refuses the
// next bind of the item, which succeeds with its line the latest.
func TestBindItemInWritersModeBindsAgainAfterAFailedBind(t *testing.T) {
	alice, work, _ := writersBindStore(t)
	d := bindDeps(alice, bindClient())
	if err := os.MkdirAll(filepath.Join(work, "charts"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(work, "charts", "shop"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, work, "add", "-A")
	runGit(t, work, "commit", "-m", "a file in the chart's way")

	if _, err := BindItem(context.Background(), d, BindOpts{Ref: ItemRef{Number: 48}, Chart: "shop"}); err == nil {
		t.Fatal("BindItem into a taken chart name: want a refusal")
	}
	if lines := entryLinesOf(t, alice); len(lines) != 1 {
		t.Fatalf("after the failed bind the entry lines = %+v, want its one reservation left", lines)
	}

	res, err := BindItem(context.Background(), d, BindOpts{Ref: ItemRef{Number: 48}, Chart: "shop2"})
	if err != nil {
		t.Fatalf("second BindItem: %v, want the item bindable again", err)
	}
	lines := entryLinesOf(t, alice)
	if len(lines) != 2 || lines[1].Ticket != res.Issue {
		t.Errorf("entry lines = %+v, want the new issue %s last", lines, res.Issue)
	}
}

func dirListing(dir string) []string {
	var out []string
	_ = filepath.Walk(dir, func(p string, _ os.FileInfo, _ error) error { out = append(out, p); return nil })
	return out
}
