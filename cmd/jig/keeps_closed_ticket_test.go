package main

import (
	"strings"
	"testing"
)

// TestKeepsClosedTicketRefusesTheVerbsThatBuildIt: a ticket closed without a
// pull request refuses the commands that build it with TICKET_CLOSED and the
// command that reopens it, shows as closed to status and next, and is built
// again once reopened.
func TestKeepsClosedTicketRefusesTheVerbsThatBuildIt(t *testing.T) {
	t.Parallel()
	fx, st, e := workFixture(t)
	ticket := fx.Ticket

	out, code := runMain(t, e, "", "close", ticket, "--reason", "covered", "--by", "develdeco/jig#82", "--note", "The demo kit shipped it.")
	if code != 0 {
		t.Fatalf("jig close: exit %d\n%s", code, out)
	}
	for _, want := range []string{"closed:\n  ticket: " + ticket + "\n  reason: covered\n  by: develdeco/jig#82\n", "note: The demo kit shipped it.\n", "jig reopen " + ticket} {
		if !strings.Contains(out, want) {
			t.Errorf("jig close lacks %q:\n%s", want, out)
		}
	}
	if rec, err := st.ReadTicket(ticket); err != nil || rec.Closed == nil || rec.Closed.Reason != "covered" || rec.Closed.By != "develdeco/jig#82" || rec.Closed.At == "" {
		t.Fatalf("the record after jig close: %+v (err %v), want the closure", rec.Closed, err)
	}

	// Every command that builds it refuses, with the way back.
	for _, args := range [][]string{
		{"claim", ticket, "--slice", "a", "--session", "s-1"},
		{"gate", ticket},
		{"publish", ticket},
		{"publish", ticket, "--body"},
		{"requeue", ticket, "--from-brief-diff"},
		{"review", "context", ticket},
	} {
		out, code := runMain(t, e, "", args...)
		if code != 1 || !strings.Contains(out, "code: TICKET_CLOSED") || !strings.Contains(out, "covered by develdeco/jig#82") || !strings.Contains(out, "jig reopen "+ticket) {
			t.Errorf("jig %s: exit %d, want 1 with TICKET_CLOSED, the closure and `jig reopen %s`\n%s", strings.Join(args, " "), code, ticket, out)
		}
	}
	if ss, _ := st.ReadSliceState(ticket, "a"); ss.State != "queued" {
		t.Errorf("a refused claim left slice a %s, want queued", ss.State)
	}
	if lines := linesOf(t, st, ticket, "claim"); len(lines) != 0 {
		t.Errorf("a refused claim left claim lines: %+v", lines)
	}

	// Status and next say it is closed, with the closure, and offer nothing.
	out, _ = runMain(t, e, "", "status", ticket)
	for _, want := range []string{"state: closed\n", "closed:\n  reason: covered\n  by: develdeco/jig#82\n", "jig reopen " + ticket} {
		if !strings.Contains(out, want) {
			t.Errorf("jig status lacks %q:\n%s", want, out)
		}
	}
	out, _ = runMain(t, e, "", "next", ticket, "--for", "build", "--json")
	for _, want := range []string{`"state":"closed"`, `"closed":"covered by develdeco/jig#82"`, `"code":"PHASE_CLOSED"`} {
		if !strings.Contains(out, want) {
			t.Errorf("jig next %s --for build --json lacks %s:\n%s", ticket, want, out)
		}
	}

	// Reopening it opens it again.
	if out, code := runMain(t, e, "", "reopen", ticket); code != 0 || !strings.Contains(out, "reopened:\n  ticket: "+ticket) {
		t.Fatalf("jig reopen: exit %d\n%s", code, out)
	}
	out, code = runMain(t, e, "", "claim", ticket, "--slice", "a", "--session", "s-1")
	if code != 0 || !strings.Contains(out, "result: claimed") {
		t.Errorf("claim after jig reopen: exit %d, want 0 claimed\n%s", code, out)
	}
}
