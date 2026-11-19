package main

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

// TestKeepsNextOrdersReadyTicketsByRankAndWarnsOfSharedRanks: `jig next` with
// no ticket lists the ready tickets by rank, lowest first, then by id, the
// unranked last; a rank two ready tickets hold is listed in shared_ranks and
// warned of in the text.
func TestKeepsNextOrdersReadyTicketsByRankAndWarnsOfSharedRanks(t *testing.T) {
	t.Parallel()
	r := newChartRig(t)
	r.ticket(t, "SHOP-1", "rank: 3\n", true)
	r.ticket(t, "SHOP-2", "rank: 1\n", true)
	r.ticket(t, "SHOP-3", "rank: 1\n", true)
	r.ticket(t, "SHOP-4", "", true)
	r.ticket(t, "SHOP-5", "rank: 2\n", true)

	out, code := r.run(t, "next", "--json")
	var n struct {
		Next        []string `json:"next"`
		SharedRanks []struct {
			Rank    int      `json:"rank"`
			Tickets []string `json:"tickets"`
		} `json:"shared_ranks"`
	}
	if err := json.Unmarshal([]byte(out), &n); code != 0 || err != nil {
		t.Fatalf("jig next --json: exit %d, %v\n%s", code, err, out)
	}
	if want := []string{"SHOP-2", "SHOP-3", "SHOP-5", "SHOP-1", "SHOP-4"}; !reflect.DeepEqual(n.Next, want) {
		t.Errorf("next = %v, want %v", n.Next, want)
	}
	if len(n.SharedRanks) != 1 || n.SharedRanks[0].Rank != 1 || !reflect.DeepEqual(n.SharedRanks[0].Tickets, []string{"SHOP-2", "SHOP-3"}) {
		t.Errorf("shared_ranks = %+v, want rank 1 held by SHOP-2 and SHOP-3", n.SharedRanks)
	}

	text, code := r.run(t, "next")
	if code != 0 || !strings.Contains(text, "warning: rank 1 is shared by SHOP-2 and SHOP-3: they are ordered by id") {
		t.Errorf("jig next = exit %d, want the shared-rank warning:\n%s", code, text)
	}
}

// TestKeepsNextSaysWhatEachBlockedTicketWaitsFor: a ticket that waits for a
// blocker is listed in the waiting table with the blocker and the fact it
// misses.
func TestKeepsNextSaysWhatEachBlockedTicketWaitsFor(t *testing.T) {
	t.Parallel()
	r := newChartRig(t)
	r.ticket(t, "SHOP-1", "", true)
	r.ticket(t, "SHOP-2", "blocked_by:\n  - ticket: SHOP-1\n    kind: merged\n", true)

	out, code := r.run(t, "next")
	if code != 0 || !strings.Contains(out, "waiting[1]{ticket,blocker,fact}:\n  SHOP-2,SHOP-1,it has not published\n") {
		t.Errorf("jig next = exit %d, want SHOP-2 waiting for SHOP-1 with a fact:\n%s", code, out)
	}

	js, code := r.run(t, "next", "--json")
	var l struct {
		Tickets []struct {
			ID        string `json:"id"`
			BlockedBy []struct {
				Ticket string `json:"ticket"`
				Fact   string `json:"fact"`
			} `json:"blocked_by"`
		} `json:"tickets"`
	}
	if err := json.Unmarshal([]byte(js), &l); code != 0 || err != nil {
		t.Fatalf("jig next --json: exit %d, %v\n%s", code, err, js)
	}
	var got string
	for _, tk := range l.Tickets {
		if tk.ID == "SHOP-2" && len(tk.BlockedBy) == 1 && tk.BlockedBy[0].Ticket == "SHOP-1" {
			got = tk.BlockedBy[0].Fact
		}
	}
	if got != "it has not published" {
		t.Errorf("SHOP-2's blocked_by fact = %q, want %q:\n%s", got, "it has not published", js)
	}
}
