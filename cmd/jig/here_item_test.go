package main

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/mirror/github"
)

// noCallsClient is a GitHub client that fails the test on any call: a command
// that must stay offline is given it, never a nil client (which falls back to
// the real gh token). Every method but the one overridden is the embedded nil
// interface's, so a call to it panics and fails the test too.
type noCallsClient struct {
	github.Client
	t *testing.T
}

func (c *noCallsClient) ItemByNumber(context.Context, string, string, int) (github.ItemDetail, error) {
	c.t.Error("the command asked the host for an item")
	return github.ItemDetail{}, nil
}

// boundItemClone is a store with a GitHub tracker whose item 48 is bound to
// the issue DEMO-1 (by `jig chart new shop --item 48`), and the env that did it.
func boundItemClone(t *testing.T) (env, string) {
	t.Helper()
	e := testEnv(t.TempDir())
	clone := filepath.Join(t.TempDir(), "clone")
	newTestOriginCloneWithGitHubTracker(t, clone)
	e.mirrorClient = &stubGitHub{items: map[int]github.ItemDetail{
		48: {Number: 48, NodeID: "NODE_48", Title: "Coupons", Body: "text\n", URL: "https://github.com/example/tracking/issues/48", Open: true},
	}}
	if out, code := runMain(t, e, "", "chart", "new", "shop", "--item", "48", "--store", clone); code != 0 {
		t.Fatalf("bind: exit %d\n%s", code, out)
	}
	return e, clone
}

// TestHereItemRef: `jig here <ref>` names the bound issue from the store alone
// (text, JSON, --explain), says an unbound item is read by `jig next`, never
// prints a help line that starts a word with #, and tells what a bare number
// hides.
func TestHereItemRef(t *testing.T) {
	t.Parallel()
	e, clone := boundItemClone(t)
	// Nothing past this point may ask the host.
	e.mirrorClient = &noCallsClient{t: t}

	out, code := runMain(t, e, "", "here", "48", "--store", clone, "--explain")
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, out)
	}
	for _, want := range []string{"ticket: DEMO-1", "ticket_how: arg-item", "item:\n  ref: 48\n  repo: example/tracking\n  bound: issue DEMO-1", "the tracker item 48 of example/tracking is bound to it", "jig next DEMO-1"} {
		if !strings.Contains(out, want) {
			t.Errorf("bound, text lacks %q:\n%s", want, out)
		}
	}
	plain, _ := runMain(t, e, "", "here", "DEMO-1", "--store", clone)
	if !strings.Contains(plain, "ticket_how: arg\n") || strings.Contains(plain, "item:") {
		t.Errorf("the issue's own id: want how arg and no item block:\n%s", plain)
	}

	out, code = runMain(t, e, "", "here", "https://github.com/example/tracking/issues/48", "--store", clone, "--json")
	var got struct {
		Ticket struct{ ID, How string }
		Item   struct {
			Ref, Repo string
			Bound     struct{ Kind, Ticket string }
		}
	}
	if code != 0 || json.Unmarshal([]byte(out), &got) != nil || got.Ticket.ID != "DEMO-1" || got.Ticket.How != "arg-item" || got.Item.Ref != "48" || got.Item.Repo != "example/tracking" || got.Item.Bound.Kind != "issue" || got.Item.Bound.Ticket != "DEMO-1" {
		t.Errorf("bound, json: exit %d, %+v\n%s", code, got, out)
	}

	out, code = runMain(t, e, "", "here", "49", "--store", clone)
	if code != 0 {
		t.Fatalf("unbound: exit %d\n%s", code, out)
	}
	for _, want := range []string{"item:\n  ref: 49\n  repo: example/tracking\n  bound: no", "Run `jig next 49` to read it; `jig here` reads no tracker"} {
		if !strings.Contains(out, want) {
			t.Errorf("unbound, text lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "ticket: ") {
		t.Errorf("unbound names a ticket:\n%s", out)
	}
	out, _ = runMain(t, e, "", "here", "other/repo#49", "--store", clone)
	if !strings.Contains(out, "Run `jig next https://github.com/other/repo/issues/49` to read it") || strings.Contains(out, " #") {
		t.Errorf("another repo's item: help should ask by URL:\n%s", out)
	}
	out, _ = runMain(t, e, "", "here", "49", "--store", clone, "--json")
	if !strings.Contains(out, `"item":{"ref":"49","repo":"example/tracking","bound":{"kind":"no"}}`) {
		t.Errorf("unbound, json lacks the item:\n%s", out)
	}

	if out, code := runMain(t, e, "", "here", "#abc", "--store", clone); code == 0 || !strings.Contains(out, "is not a tracker item ref") {
		t.Errorf("a refused ref: exit %d, want the parser's refusal:\n%s", code, out)
	}
}

// TestHereItemRefShadow: a bare number that is also a chart's name still reads
// as the item, with the advisory naming how to reach the chart.
func TestHereItemRefShadow(t *testing.T) {
	t.Parallel()
	e, clone := boundItemClone(t)
	e.mirrorClient = &noCallsClient{t: t}
	writeChart(t, clone, "49", "issue: DEMO-1\nentries: []\n")
	out, code := runMain(t, e, "", "here", "49", "--store", clone)
	if code != 0 || !strings.Contains(out, "advisories[1]:") || !strings.Contains(out, "49 is also the name of a chart") {
		t.Errorf("exit %d, want the shadow advisory:\n%s", code, out)
	}
}

// TestChartNewNameDigits: a name of digits only is a tracker item's number and
// is refused with its reason; a name with a letter is not.
func TestChartNewNameDigits(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"48", "007"} {
		err := checkNewChartName(name)
		if err == nil || !strings.Contains(err.Error(), "digits only is a tracker item's number") {
			t.Errorf("%q: err = %v, want the digits-only reason", name, err)
		}
	}
	for _, name := range []string{"48a", "a48", "shop-1", "4-8"} {
		if err := checkNewChartName(name); err != nil {
			t.Errorf("%q: err = %v, want it accepted", name, err)
		}
	}
}
