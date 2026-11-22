package locate

import (
	"errors"
	"github.com/develdeco/jig/internal/axi"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/project"
	"github.com/develdeco/jig/internal/store"
)

// itemWorld is a mapped project whose store has a GitHub tracker, a ticket
// DEMO-1 (an issue) and DEMO-2 (a task).
type itemWorld struct {
	*world
	store string
	clone string
}

func newItemWorld(t *testing.T) *itemWorld {
	t.Helper()
	w := newWorld(t)
	storeDir := w.store("demo-tickets", "demo")
	yaml := "schema_version: 2\nname: demo\nkeys:\n  DEMO: everything\ntrackers:\n  - github:\n      repo: Example/Tracking\n      project: https://github.com/users/example/projects/7\nplatform: platform/\nrepos: []\n"
	if err := os.WriteFile(filepath.Join(storeDir, "project.yaml"), []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	clone := w.repo("app", "")
	w.mapProject("demo", project.MachineProject{Store: storeDir, Clones: map[string]string{"app": clone}})
	st := openStore(t, storeDir)
	for id, kind := range map[string]string{"DEMO-1": store.KindIssue, "DEMO-2": store.KindTask} {
		if err := st.CreateTicketRecord(id, store.Ticket{Title: id, Kind: kind}); err != nil {
			t.Fatal(err)
		}
	}
	return &itemWorld{world: w, store: storeDir, clone: clone}
}

// record writes the record of issue n of repo (lower-case scope) with body.
func (iw *itemWorld) record(scope, n, body string) string {
	iw.t.Helper()
	dir := filepath.Join(iw.store, "trackers", "github", filepath.FromSlash(scope), n)
	mkdir(iw.t, dir)
	path := filepath.Join(dir, "record.yaml")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		iw.t.Fatal(err)
	}
	return path
}

func (iw *itemWorld) locateErr(word string) error {
	_, err := Locate(iw.home, iw.clone, Opts{Ticket: word})
	return err
}

// TestLocateItemRef: a tracker item's ref is tried before a ticket id, an
// alias or a chart; a bound one names its owner from the tree alone, an
// unbound one names no ticket, and a word that is not ref-shaped is read as
// before.
func TestLocateItemRef(t *testing.T) {
	t.Parallel()
	iw := newItemWorld(t)
	iw.record("example/tracking", "48", "ticket: DEMO-1\nissue: 48\n")
	iw.record("example/tracking", "50", "ticket: DEMO-2\nissue: 50\n")

	for _, word := range []string{"48", "#48", "example/tracking#48", "EXAMPLE/Tracking#48", "https://github.com/Example/Tracking/issues/48"} {
		got := iw.locate(iw.clone, Opts{Ticket: word})
		if got.Ticket != "DEMO-1" || got.TicketHow != HowArgItem || got.Item == nil || !got.Item.Bound || got.Item.Kind != store.KindIssue {
			t.Errorf("%q: ticket %q (%s), item %+v, want DEMO-1 by arg-item, bound as an issue", word, got.Ticket, got.TicketHow, got.Item)
			continue
		}
		if got.Item.Ref != "48" || got.Item.Repo != "Example/Tracking" || got.Item.Other {
			t.Errorf("%q: item %+v, want ref 48 of the tracker repo as project.yaml spells it", word, got.Item)
		}
		if !explained(got, "ticket: DEMO-1 (the tracker item 48") {
			t.Errorf("%q: explain = %v, want the ref as how", word, got.Explain)
		}
	}

	if got := iw.locate(iw.clone, Opts{Ticket: "50"}); got.Ticket != "DEMO-2" || got.Item.Kind != store.KindTask {
		t.Errorf("a task's item: ticket %q, item %+v, want DEMO-2 as a task", got.Ticket, got.Item)
	}

	got := iw.locate(iw.clone, Opts{Ticket: "49"})
	if got.Ticket != "" || got.Item == nil || got.Item.Bound || got.Item.Ref != "49" {
		t.Errorf("unbound: ticket %q, item %+v, want no ticket and an unbound item", got.Ticket, got.Item)
	}
	got = iw.locate(iw.clone, Opts{Ticket: "other/repo#48"})
	if got.Ticket != "" || got.Item == nil || got.Item.Bound || !got.Item.Other || got.Item.Repo != "other/repo" {
		t.Errorf("another repo's item 48: ticket %q, item %+v, want unbound and Other", got.Ticket, got.Item)
	}

	// Not ref-shaped: a ticket id (even one that is unknown) and a chart name
	// are never read as a ref.
	for _, word := range []string{"DEMO-1", "DEMO-404", "shop"} {
		got := iw.locate(iw.clone, Opts{Ticket: word})
		if got.Item != nil || got.Ticket != word || got.TicketHow == HowArgItem {
			t.Errorf("%q: item %+v, ticket %q (%s), want it read as before", word, got.Item, got.Ticket, got.TicketHow)
		}
	}
}

// TestLocateItemRefRefusals: the parser's refusal comes back as it is, a record
// that cannot be followed is an error naming its path, and a project with no
// tracker is refused.
func TestLocateItemRefRefusals(t *testing.T) {
	t.Parallel()
	iw := newItemWorld(t)
	missing := iw.record("example/tracking", "60", "ticket: DEMO-9\n")
	owned := iw.record("example/tracking", "61", "chart: shop\n")
	broken := iw.record("example/tracking", "62", "ticket: [unclosed\n")

	for _, word := range []string{"#abc", "owner/repo#x", "https://github.com/example/tracking/pull/48", "x://y"} {
		err := iw.locateErr(word)
		if err == nil || !strings.Contains(err.Error(), "is not a tracker item ref") || strings.Contains(err.Error(), "not found") {
			t.Errorf("%q: err = %v, want the parser's refusal", word, err)
		}
	}
	for ref, path := range map[string]string{"60": missing, "61": owned, "62": broken} {
		err := iw.locateErr(ref)
		if err == nil || !strings.Contains(err.Error(), path) {
			t.Errorf("%s: err = %v, want an error naming %s", ref, err, path)
		}
	}
	if err := iw.locateErr("60"); err == nil || !strings.Contains(err.Error(), "DEMO-9") {
		t.Errorf("owner missing: err = %v, want the owner named", err)
	}

	bare := newWorld(t)
	storeDir := bare.store("bare-tickets", "bare")
	clone := bare.repo("app", "")
	bare.mapProject("bare", project.MachineProject{Store: storeDir, Clones: map[string]string{"app": clone}})
	// A ref is a ref only with a tracker: without one every word is read as it
	// was before refs, even one the parser would refuse.
	for _, word := range []string{"48", "#abc", "o/r#1"} {
		got, err := Locate(bare.home, clone, Opts{Ticket: word})
		if err != nil || got.Item != nil || got.Ticket != word || got.TicketHow != HowArg {
			t.Errorf("no tracker, %q: %+v, %v, want the word as the ticket and no item", word, got, err)
		}
	}
}

// TestLocateItemRefChartOwner: a record whose owner key is chart: binds the
// item to that chart, and names no ticket.
func TestLocateItemRefChartOwner(t *testing.T) {
	t.Parallel()
	iw := newItemWorld(t)
	iw.record("example/tracking", "70", "chart: plan\n")
	mkdir(t, filepath.Join(iw.store, "charts", "plan"))
	if err := os.WriteFile(filepath.Join(iw.store, "charts", "plan", "tickets.yaml"), []byte("tickets: []\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got := iw.locate(iw.clone, Opts{Ticket: "70"})
	if got.Ticket != "" || got.Item == nil || !got.Item.Bound || got.Item.Chart != "plan" || got.Item.Kind != "chart" {
		t.Errorf("ticket %q, item %+v, want the item bound to chart plan and no ticket", got.Ticket, got.Item)
	}
}

// TestLocateItemRefShadowHelp: a bare number that also names a chart or an
// alias and fails as an item carries the line that says how to reach what it
// hides, in the error's help.
func TestLocateItemRefShadowHelp(t *testing.T) {
	t.Parallel()
	iw := newItemWorld(t)
	iw.record("example/tracking", "80", "ticket: DEMO-9\n")
	mkdir(t, filepath.Join(iw.store, "charts", "80"))
	if err := os.WriteFile(filepath.Join(iw.store, "charts", "80", "tickets.yaml"), []byte("tickets: []\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := iw.locateErr("80")
	var ae *axi.Error
	if !errors.As(err, &ae) || !strings.Contains(strings.Join(ae.Help, "\n"), "jig status 80") {
		t.Errorf("err = %v, want help naming `jig status 80`, which reaches a chart with no issue", err)
	}
}

// TestLocateItemRefShadows: a bare number still reads as the item when it is
// also an earlier ticket id or a chart's name, and says what it hides.
func TestLocateItemRefShadows(t *testing.T) {
	t.Parallel()
	iw := newItemWorld(t)
	iw.record("example/tracking", "48", "ticket: DEMO-1\n")
	st := openStore(t, iw.store)
	if err := st.CreateTicketRecord("DEMO-3", store.Ticket{Title: "old", Aliases: []string{"77"}}); err != nil {
		t.Fatal(err)
	}
	mkdir(t, filepath.Join(iw.store, "charts", "48"))
	if err := os.WriteFile(filepath.Join(iw.store, "charts", "48", "tickets.yaml"), []byte("issue: DEMO-1\nentries: []\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got := iw.locate(iw.clone, Opts{Ticket: "48"})
	if got.Ticket != "DEMO-1" || got.Item == nil || !strings.Contains(got.Item.Shadow, "name of a chart") || !strings.Contains(got.Item.Shadow, "DEMO-1") {
		t.Errorf("chart 48: ticket %q, item %+v, want the item with a shadow line naming the chart's issue", got.Ticket, got.Item)
	}
	got = iw.locate(iw.clone, Opts{Ticket: "77"})
	if got.Item == nil || got.Item.Bound || !strings.Contains(got.Item.Shadow, "earlier id of ticket DEMO-3") {
		t.Errorf("alias 77: item %+v, want an unbound item shadowing DEMO-3", got.Item)
	}
	if got := iw.locate(iw.clone, Opts{Ticket: "#48"}); got.Item == nil || got.Item.Shadow != "" {
		t.Errorf("#48: item %+v, want no shadow line: only a bare number hides a chart", got.Item)
	}
}
