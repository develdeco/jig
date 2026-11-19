package main

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestKeepsNextListsATicketItCannotRead: a ticket whose slices.yaml does not
// parse is listed as invalid, with the file and the problem and a way out, and
// the store's other tickets are listed as ever; recall reads past it too, and
// what its record says (what it waits for) still counts.
func TestKeepsNextListsATicketItCannotRead(t *testing.T) {
	t.Parallel()
	r := newChartRig(t)
	r.ticket(t, "SHOP-1", "", true)
	r.ticket(t, "SHOP-2", "", true)
	r.put(t, "tickets/SHOP-2/slices.yaml", "slices:\n  - id: a\n    workspace: alpha\n    env: {}\n")

	type row struct {
		ID        string `json:"id"`
		State     string `json:"state"`
		Problem   string `json:"problem"`
		BlockedBy []struct {
			Ticket string `json:"ticket"`
		} `json:"blocked_by"`
	}
	list := func() map[string]row {
		t.Helper()
		out, code := r.run(t, "next", "--json")
		var n struct {
			Tickets []row `json:"tickets"`
		}
		if err := json.Unmarshal([]byte(out), &n); code != 0 || err != nil {
			t.Fatalf("jig next --json: exit %d, %v\n%s", code, err, out)
		}
		rows := map[string]row{}
		for _, tk := range n.Tickets {
			rows[tk.ID] = tk
		}
		return rows
	}

	rows := list()
	if rows["SHOP-1"].State != "ready" || rows["SHOP-2"].State != "invalid" || !strings.Contains(rows["SHOP-2"].Problem, "slices.yaml") {
		t.Fatalf("tickets = %+v, want SHOP-1 ready and SHOP-2 invalid naming its slices.yaml", rows)
	}
	out, code := r.run(t, "next")
	if code != 0 || !strings.Contains(out, "invalid[1]{id,problem}:") || !strings.Contains(out, "jig validate SHOP-2") {
		t.Errorf("jig next = exit %d\n%s\nwant the invalid ticket's problem and a way out", code, out)
	}
	if out, code := r.run(t, "recall", "clamp"); code != 0 {
		t.Errorf("jig recall with an invalid ticket = exit %d\n%s", code, out)
	}

	// What its record says still counts: it waits for SHOP-1.
	r.put(t, "tickets/SHOP-2/ticket.yaml", "schema_version: 1\ntitle: Ticket SHOP-2\nblocked_by:\n  - ticket: SHOP-1\n    kind: merged\n")
	again := list()["SHOP-2"]
	if again.State != "invalid" || len(again.BlockedBy) != 1 || again.BlockedBy[0].Ticket != "SHOP-1" {
		t.Errorf("SHOP-2 = %+v, want invalid and still blocked by SHOP-1", again)
	}
}
