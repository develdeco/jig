package main

import (
	"regexp"
	"strings"
	"testing"
	"time"
)

// TestKeepsNextWarnsOfReadyTicketsInOneWorkspace: `jig next` with no ticket
// keeps its own warning and table for ready tickets that build in one
// workspace, and says nothing once the later one is stacked on the earlier.
func TestKeepsNextWarnsOfReadyTicketsInOneWorkspace(t *testing.T) {
	t.Parallel()
	one := "repos:\n  - remote: /srv/app.git\n    label: app\n"
	r := newChartRig(t)
	r.overlapRig(t, one, map[string][2]string{
		"TK-1": {"", sliceFile("alpha")},
		"TK-2": {"", sliceFile("alpha")},
		// A second workspace in the project, or the rule says nothing.
		"TK-3": {"", sliceFile("beta")},
	})
	approveBrief(t, r.st, "TK-1")
	approveBrief(t, r.st, "TK-2")

	out, code := r.run(t, "next")
	if code != 0 {
		t.Fatalf("jig next: exit %d\n%s", code, out)
	}
	for _, want := range []string{"warning: ready tickets build in the same workspace\n", "app,alpha,TK-1+TK-2\n"} {
		if !strings.Contains(out, want) {
			t.Errorf("jig next lacks %q:\n%s", want, out)
		}
	}

	r.put(t, "tickets/TK-2/ticket.yaml", "schema_version: 1\ntitle: Ticket TK-2\nblocked_by:\n  - ticket: TK-1\n    kind: stacked\n")
	if out, _ := r.run(t, "next"); strings.Contains(out, "workspace_overlap") {
		t.Errorf("jig next with the edge still says overlap:\n%s", out)
	}
}

// TestKeepsChartHistoryInNextJSONHasShasAndUTCTimes: `jig next <chart> --json`
// lists the commits of the chart's own folder newest first, the map edit
// first, and neither another chart's commit nor a ticket's, with 40-hex shas
// and RFC 3339 UTC times; the text form has no history.
func TestKeepsChartHistoryInNextJSONHasShasAndUTCTimes(t *testing.T) {
	t.Parallel()
	r := newChartRig(t)
	r.shop(t, false)
	gitIn(t, r.root, "init", "-b", "main")
	commit := func(path, content, subject string) {
		t.Helper()
		r.put(t, path, content)
		gitIn(t, r.root, "add", "-A")
		gitIn(t, r.root, "commit", "-m", subject)
	}
	commit("charts/checkout/map.md", "# Coupons at checkout\n", "chart checkout: graduate")
	commit("charts/other/map.md", "# Another chart\n", "other: a map of its own")
	commit("tickets/SHOP-1/scratch.md", "a file of the ticket\n", "SHOP-1: a file of the ticket only")
	commit("charts/checkout/map.md", "# Coupons at checkout\n\n## Destination\n\nMore.\n", "checkout: a code names its shop")

	n := r.next(t, "checkout")
	if len(n.History) != 2 || n.History[0].Subject != "checkout: a code names its shop" || n.History[1].Subject != "chart checkout: graduate" {
		t.Fatalf("history = %+v, want the map edit then the graduation and nothing else", n.History)
	}
	hex40 := regexp.MustCompile(`^[0-9a-f]{40}$`)
	var newer time.Time
	for i, h := range n.History {
		if !hex40.MatchString(h.SHA) {
			t.Errorf("history[%d].sha = %q, want 40 hex", i, h.SHA)
		}
		at, err := time.Parse(time.RFC3339, h.Time)
		if err != nil || !strings.HasSuffix(h.Time, "Z") {
			t.Errorf("history[%d].time = %q (%v), want RFC 3339 in UTC, ending in Z", i, h.Time, err)
		}
		if i > 0 && at.After(newer) {
			t.Errorf("history[%d] is newer than history[%d]: want newest first", i, i-1)
		}
		newer = at
	}
	if head := gitIn(t, r.root, "rev-parse", "HEAD"); n.History[0].SHA != head {
		t.Errorf("history[0].sha = %q, want the store's HEAD %q", n.History[0].SHA, head)
	}
	if text, _ := r.run(t, "next", "checkout"); strings.Contains(text, "history") {
		t.Errorf("the text form has a history, which is for --json only:\n%s", text)
	}
}
