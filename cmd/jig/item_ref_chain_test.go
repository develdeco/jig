package main

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/mirror/github"
)

// decodeObject reads one JSON object.
func decodeObject(t *testing.T, s string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		t.Fatalf("not a JSON object: %v\n%s", err, s)
	}
	return m
}

// TestItemRefChain walks a tracker item through jig as a session meets it,
// through run with the GitHub client faked: read unbound with nothing written,
// bound by `jig chart new --item`, then asked for by its number with no
// network, by `jig next` and `jig here` alike.
func TestItemRefChain(t *testing.T) {
	t.Parallel()
	e := testEnv(t.TempDir())
	clone := filepath.Join(t.TempDir(), "clone")
	newTestOriginCloneWithGitHubTracker(t, clone)
	c := &readsClient{stubGitHub: &stubGitHub{items: map[int]github.ItemDetail{
		48: {Number: 48, NodeID: "NODE_48", Title: "Coupons at checkout", Body: itemBody, URL: "https://github.com/example/tracking/issues/48", Open: true, Author: "sam"},
		49: {Number: 49, NodeID: "NODE_49", Title: "Dark mode", Body: "b", URL: "https://github.com/example/tracking/issues/49", Open: true},
	}}}
	e.mirrorClient = c

	// Unbound: the read and the help, and the store as it was.
	before := storeState(t, clone)
	out, code := runMain(t, e, "", "next", "48", "--store", clone)
	if code != 0 || !strings.Contains(out, "  bound: no\n") || !strings.Contains(out, "title: Coupons at checkout") ||
		!strings.Contains(out, "The verdict makes its one issue and chart: jig chart new <name> --item 48") ||
		!strings.Contains(out, "Once bound, jig next 48 answers as the issue does, with no network") {
		t.Fatalf("unbound: exit %d\n%s", code, out)
	}
	if after := storeState(t, clone); after != before || len(c.asked) != 1 {
		t.Errorf("the read changed the store or read more than one item (asked %v):\n%s\n--\n%s", c.asked, before, after)
	}

	// Bound: the issue and its chart.
	if out, code := runMain(t, e, "", "chart", "new", "shop", "--item", "48", "--store", clone); code != 0 {
		t.Fatalf("bind: exit %d\n%s", code, out)
	}
	c.asked = nil

	// The ref answers as the issue does, plus its three lines, with no network.
	plain, pcode := runMain(t, e, "", "next", "DEMO-1", "--store", clone)
	got, gcode := runMain(t, e, "", "next", "48", "--store", clone)
	if pcode != 0 || gcode != 0 || withoutItemLines(got) != plain || !strings.Contains(got, "next:\n  ref: 48\n  repo: example/tracking\n  bound: issue DEMO-1\n") {
		t.Errorf("jig next 48 (exit %d) is not jig next DEMO-1 (exit %d) plus the lines:\n%s\n--- plain:\n%s", gcode, pcode, got, plain)
	}
	pj, _ := runMain(t, e, "", "next", "DEMO-1", "--store", clone, "--json")
	gj, _ := runMain(t, e, "", "next", "48", "--store", clone, "--json")
	pm, gm := decodeObject(t, pj), decodeObject(t, gj)
	if gm["ref"] != "48" || gm["repo"] != "example/tracking" || !reflect.DeepEqual(gm["bound"], map[string]any{"kind": "issue", "ticket": "DEMO-1"}) {
		t.Errorf("json lacks ref, repo and bound: %s", gj)
	}
	delete(gm, "ref")
	delete(gm, "repo")
	delete(gm, "bound")
	if !reflect.DeepEqual(pm, gm) {
		t.Errorf("jig next 48 --json is not jig next DEMO-1 --json:\n%s\n%s", gj, pj)
	}

	// jig here names the issue offline.
	out, code = runMain(t, e, "", "here", "48", "--store", clone)
	if code != 0 || !strings.Contains(out, "ticket: DEMO-1\n") || !strings.Contains(out, "bound: issue DEMO-1") {
		t.Errorf("jig here 48: exit %d\n%s", code, out)
	}
	if len(c.asked) != 0 {
		t.Errorf("a bound ref asked the host: %v", c.asked)
	}

	// An unbound ref with no token is exit 1, with the cause and no bind help.
	c.err = &axi.Error{Msg: "gh CLI is not installed", Code: "GH_NOT_INSTALLED"}
	out, code = runMain(t, e, "", "next", "49", "--store", clone)
	if code != 1 || !strings.Contains(out, "item 49 of example/tracking could not be read: gh CLI is not installed") || strings.Contains(out, "jig chart new") {
		t.Errorf("no token: exit %d\n%s", code, out)
	}

	// A bare jig next still lists the store.
	if out, code := runMain(t, e, "", "next", "--store", clone); code != 0 || !strings.Contains(out, "DEMO-1") || strings.Contains(out, "  ref: ") {
		t.Errorf("jig next: exit %d\n%s", code, out)
	}
}
