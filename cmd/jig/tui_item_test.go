package main

import (
	"strconv"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/store"
	"gopkg.in/yaml.v3"
)

// bindItemTo writes the tracker record of item number n of the project's tracker
// for the owner, as the mirror does.
func bindItemTo(t *testing.T, clone string, owner store.TrackerOwner, n int) {
	t.Helper()
	st, err := store.Open(clone)
	if err != nil {
		t.Fatal(err)
	}
	var fields yaml.Node
	if err := fields.Encode(map[string]any{"repo": "example/tracking", "issue": n}); err != nil {
		t.Fatal(err)
	}
	e := store.TrackerEntry{Provider: store.ProviderGitHub, Scope: "example/tracking", ID: strconv.Itoa(n)}
	if err := st.WriteTrackerRecord(e, owner, &fields, "# test\n"); err != nil {
		t.Fatal(err)
	}
}

// TestTUIItemRef: jig tui reads a tracker item's number as jig next does,
// before a chart or a ticket: a bound issue opens its plan, a bound task its
// ticket, a chart's own record that chart; an item with no record is refused
// with the help to run jig next; the parser's refusal is the parser's; with no
// tracker the word is read as before.
func TestTUIItemRef(t *testing.T) {
	t.Parallel()
	e, clone := boundItemClone(t)
	e.mirrorClient = &noCallsClient{t: t}
	st, err := store.Open(clone)
	if err != nil {
		t.Fatal(err)
	}
	putTicket(t, st, "DEMO-2", "Coupon model", false)
	bindItemTo(t, clone, store.TrackerOwner{Ticket: "DEMO-2"}, 52)

	frames := func(word, until string) string {
		t.Helper()
		keys, out, wait := startTUI(t, e, word, "--store", clone)
		out.until(t, until)
		keys.Write([]byte("q"))
		if code := wait(); code != 0 {
			t.Fatalf("jig tui %s: exit %d\n%s", word, code, out.String())
		}
		return out.String()
	}

	// A bound issue opens its plan, by every form of its ref.
	for _, word := range []string{"48", "#48", "example/tracking#48", "https://github.com/example/tracking/issues/48"} {
		frame := frames(word, "Need")
		if !strings.Contains(frame, "› shop") || !strings.Contains(frame, "Need") || strings.Contains(frame, "1 Brief") {
			t.Errorf("jig tui %s: the frames are not the plan of the issue:\n%s", word, frame)
		}
	}

	// A bound task opens its ticket.
	if frame := frames("52", "Brief"); !strings.Contains(frame, "› DEMO-2") || !strings.Contains(frame, "Brief") {
		t.Errorf("jig tui 52: the frames are not the task's ticket:\n%s", frame)
	}

	// A record a chart owns opens that chart, and a chart's name still does.
	writeChart(t, clone, "old", "tickets: []\n")
	bindItemTo(t, clone, store.TrackerOwner{Chart: "old"}, 53)
	if frame := frames("53", "old"); !strings.Contains(frame, "› old") {
		t.Errorf("jig tui 53: the frames are not the chart old:\n%s", frame)
	}
	if frame := frames("old", "old"); !strings.Contains(frame, "› old") {
		t.Errorf("jig tui old: the frames are not the chart old:\n%s", frame)
	}

	// An item with no record is refused, and the help runs jig next.
	for _, word := range []string{"501", "#501"} {
		out, code := runMain(t, e, "", "tui", word, "--store", clone)
		if code == 0 || !strings.Contains(out, "501 is an unbound tracker item") || !strings.Contains(out, "jig next 501") || !strings.Contains(out, "VALIDATION_ERROR") {
			t.Errorf("jig tui %s: exit %d, want the refusal with the help:\n%s", word, code, out)
		}
	}
	if out, _ := runMain(t, e, "", "tui", "other/repo#7", "--store", clone); !strings.Contains(out, "7 is an unbound tracker item") || !strings.Contains(out, "jig next https://github.com/other/repo/issues/7") {
		t.Errorf("another repo's item: want it asked by address:\n%s", out)
	}

	// A ref the parser refuses is the parser's refusal.
	if out, code := runMain(t, e, "", "tui", "#abc", "--store", clone); code == 0 || !strings.Contains(out, "is not a tracker item ref") {
		t.Errorf("a refused ref: exit %d\n%s", code, out)
	}

	// The ticket's own id and the chart's name read as they did.
	if frame := frames("DEMO-2", "Brief"); !strings.Contains(frame, "› DEMO-2") {
		t.Errorf("jig tui DEMO-2:\n%s", frame)
	}

	// With no tracker the word is read as before: a number nothing names is
	// not found.
	plain := containerRig(t)
	if out, code := runMain(t, plain.env(), "", "tui", "501", "--store", plain.root); code == 0 || !strings.Contains(out, "ticket 501 not found") {
		t.Errorf("no tracker: exit %d, want ticket 501 not found:\n%s", code, out)
	}
}
