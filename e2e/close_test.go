package e2e

import (
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/store"
)

// TestClosedTicketCannotBeBuilt: a ticket closed without a pull request is
// recorded on the origin of the store, refuses the commands that build it with
// TICKET_CLOSED and the command that reopens it, shows as closed to status and
// next, and is built again once reopened.
func TestClosedTicketCannotBeBuilt(t *testing.T) {
	t.Parallel()
	fx, env := newFixture(t, fixture.Opts{})
	env = env.without("JIG_SLICE", "JIG_SESSION")
	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatal(err)
	}
	ticket := fx.Ticket

	r := runJig(t, env, fx.RepoDir, "close", ticket, "--reason", "covered", "--by", "develdeco/jig#82", "--note", "The demo kit shipped it.")
	if r.Code != 0 {
		t.Fatalf("jig close: exit %d\n%s", r.Code, r.Stdout)
	}
	for _, want := range []string{"closed:\n  ticket: " + ticket + "\n  reason: covered\n  by: develdeco/jig#82\n", "note: The demo kit shipped it.\n", "jig reopen " + ticket} {
		if !strings.Contains(r.Stdout, want) {
			t.Errorf("jig close lacks %q:\n%s", want, r.Stdout)
		}
	}
	// The record alone is committed, and pushed to the origin of the store.
	if subject := gitLog(t, fx.StoreRemote, "log", "-1", "--format=%s"); subject != ticket+": closed, covered by develdeco/jig#82" {
		t.Errorf("the origin of the store ends with %q, want the closure", subject)
	}
	if files := gitLog(t, fx.StoreRemote, "show", "--name-only", "--format=", "HEAD"); strings.TrimSpace(files) != "tickets/"+ticket+"/ticket.yaml" {
		t.Errorf("the closing commit holds %q, want that ticket.yaml alone", files)
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
		r := runJig(t, env, fx.RepoDir, args...)
		if r.Code != 1 || !strings.Contains(r.Stdout, "code: TICKET_CLOSED") || !strings.Contains(r.Stdout, "covered by develdeco/jig#82") || !strings.Contains(r.Stdout, "jig reopen "+ticket) {
			t.Errorf("jig %s: exit %d, want 1 with TICKET_CLOSED, the closure and `jig reopen %s`\n%s", strings.Join(args, " "), r.Code, ticket, r.Stdout)
		}
	}
	if got := sliceState(t, st, ticket, "a"); got != "queued" {
		t.Errorf("a refused claim left slice a %s, want queued", got)
	}
	if lines := claimLines(t, fx, ticket, "claim"); len(lines) != 0 {
		t.Errorf("a refused claim left claim lines: %+v", lines)
	}

	// Status and next say it is closed, with the closure, and offer nothing.
	r = runJig(t, env, fx.RepoDir, "status", ticket)
	for _, want := range []string{"state: closed\n", "closed:\n  reason: covered\n  by: develdeco/jig#82\n", "jig reopen " + ticket} {
		if !strings.Contains(r.Stdout, want) {
			t.Errorf("jig status lacks %q:\n%s", want, r.Stdout)
		}
	}
	n := nextJSON[struct {
		State  string `json:"state"`
		Closed string `json:"closed"`
		Ready  []any  `json:"ready"`
		Phase  struct {
			Name   string `json:"name"`
			Ready  bool   `json:"ready"`
			Code   string `json:"code"`
			Prompt string `json:"prompt"`
			For    struct {
				Ready bool   `json:"ready"`
				Code  string `json:"code"`
			} `json:"for"`
		} `json:"phase"`
	}](t, env, fx.RepoDir, ticket, "--for", "build")
	if n.State != "closed" || n.Closed != "covered by develdeco/jig#82" || len(n.Ready) != 0 {
		t.Errorf("jig next %s = %+v, want closed, covered by develdeco/jig#82, nothing ready", ticket, n)
	}
	if n.Phase.Ready || n.Phase.Code != "PHASE_CLOSED" || n.Phase.Prompt != "" || n.Phase.For.Ready || n.Phase.For.Code != "PHASE_CLOSED" {
		t.Errorf("the phase of a closed ticket = %+v, want it not ready, PHASE_CLOSED, with no prompt, whatever phase is asked", n.Phase)
	}

	// Closing it again is refused with help; reopening it opens it.
	if r = runJig(t, env, fx.RepoDir, "close", ticket, "--reason", "not-planned"); r.Code != 1 || !strings.Contains(r.Stdout, "code: TICKET_ALREADY_CLOSED") || !strings.Contains(r.Stdout, "jig reopen "+ticket) {
		t.Errorf("closing a closed ticket: exit %d, want 1 with TICKET_ALREADY_CLOSED\n%s", r.Code, r.Stdout)
	}
	if r = runJig(t, env, fx.RepoDir, "reopen", ticket); r.Code != 0 || !strings.Contains(r.Stdout, "reopened:\n  ticket: "+ticket) {
		t.Fatalf("jig reopen: exit %d\n%s", r.Code, r.Stdout)
	}
	if subject := gitLog(t, fx.StoreRemote, "log", "-1", "--format=%s"); subject != ticket+": reopened" {
		t.Errorf("the origin of the store ends with %q, want the reopening", subject)
	}
	if r = runJig(t, env, fx.RepoDir, "reopen", ticket); r.Code != 1 || !strings.Contains(r.Stdout, "code: TICKET_NOT_CLOSED") {
		t.Errorf("reopening an open ticket: exit %d, want 1 with TICKET_NOT_CLOSED\n%s", r.Code, r.Stdout)
	}
	r = runJig(t, env, fx.RepoDir, "claim", ticket, "--slice", "a", "--session", "s-1")
	if r.Code != 0 || !strings.Contains(r.Stdout, "result: claimed") {
		t.Errorf("claim after jig reopen: exit %d, want 0 claimed\n%s", r.Code, r.Stdout)
	}
	if got := sliceState(t, st, ticket, "a"); got != "building" {
		t.Errorf("after the claim slice a is %s, want building", got)
	}
}

// TestNextTicketsWaitingForAClosedTicket: next lists a closed ticket as closed
// and never as ready, a ticket that waits for a ticket closed as covered is
// released with no merge to look for, and one that waits for a ticket closed as
// not planned stays held, saying it needs planning again, in the listing and in
// the claim.
func TestNextTicketsWaitingForAClosedTicket(t *testing.T) {
	t.Parallel()
	fx, env := newFixture(t, fixture.Opts{})
	env = env.without("JIG_SLICE", "JIG_SESSION")
	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatal(err)
	}
	writeBlockedTicket(t, st, "merged")
	approve(t, st)
	blocker := fx.Ticket

	if row := listed(t, env, fx.RepoDir, "JIG-2"); row.State != "blocked" {
		t.Fatalf("before the closure JIG-2 is %s, want blocked by %s", row.State, blocker)
	}

	// Covered: the blocker is closed and ready for nothing; JIG-2 is released.
	if r := runJig(t, env, fx.RepoDir, "close", blocker, "--reason", "covered", "--by", "develdeco/jig#82"); r.Code != 0 {
		t.Fatalf("jig close: exit %d\n%s", r.Code, r.Stdout)
	}
	list := nextJSON[nextStoreOut](t, env, fx.RepoDir)
	states := map[string]string{}
	for _, row := range list.Tickets {
		states[row.ID] = row.State
		if row.ID == blocker && len(row.Ready) != 0 {
			t.Errorf("%s is closed and lists ready slices %v", blocker, row.Ready)
		}
	}
	if states[blocker] != "closed" || states["JIG-2"] != "ready" {
		t.Errorf("states after the covered closure = %v, want %s closed and JIG-2 ready", states, blocker)
	}
	if want := []string{"JIG-2"}; strings.Join(list.Next, ",") != strings.Join(want, ",") {
		t.Errorf("jig next offers %v, want only %v (a closed ticket is never next)", list.Next, want)
	}
	r := runJig(t, env, fx.RepoDir, "claim", "JIG-2", "--slice", "x", "--unattended", "--session", "s-1")
	if r.Code != 0 || strings.Contains(r.Stdout, "warnings") || !strings.Contains(r.Stdout, "result: claimed") {
		t.Errorf("unattended claim of a ticket whose blocker is covered: exit %d, want 0, no warning\n%s", r.Code, r.Stdout)
	}

	// Not planned: the blocker will never be done, so JIG-2 is held.
	if r := runJig(t, env, fx.RepoDir, "reopen", blocker); r.Code != 0 {
		t.Fatalf("jig reopen: exit %d\n%s", r.Code, r.Stdout)
	}
	if r := runJig(t, env, fx.RepoDir, "close", blocker, "--reason", "not-planned", "--note", "Decided against it."); r.Code != 0 {
		t.Fatalf("jig close: exit %d\n%s", r.Code, r.Stdout)
	}
	row := listed(t, env, fx.RepoDir, "JIG-2")
	if row.State != "blocked" || len(row.BlockedBy) != 1 || row.BlockedBy[0].Ticket != blocker || !strings.Contains(row.BlockedBy[0].Fact, "closed as not planned") {
		t.Errorf("JIG-2 after the not-planned closure = %+v, want blocked by %s, closed as not planned", row, blocker)
	}
	r = runJig(t, env, fx.RepoDir, "next")
	for _, want := range []string{blocker + ",", ",closed,", "waiting[1]{ticket,blocker,fact}:", "closed as not planned"} {
		if !strings.Contains(r.Stdout, want) {
			t.Errorf("jig next lacks %q:\n%s", want, r.Stdout)
		}
	}
	r = runJig(t, env, fx.RepoDir, "claim", "JIG-2", "--slice", "y", "--unattended", "--session", "s-1")
	if r.Code != 1 || !strings.Contains(r.Stdout, "code: CLAIM_BLOCKER_NOT_PLANNED") || !strings.Contains(r.Stdout, "jig reopen "+blocker) {
		t.Errorf("unattended claim of a ticket whose blocker is not planned: exit %d, want 1 with CLAIM_BLOCKER_NOT_PLANNED and the way to reopen it\n%s", r.Code, r.Stdout)
	}
}

// TestCloseRefusesWhatItCannotRecord: a closure that is not a record the store
// accepts is a validation error that writes and pushes nothing, and
// jig validate reports a closed entry edited by hand the same way.
func TestCloseRefusesWhatItCannotRecord(t *testing.T) {
	t.Parallel()
	fx, env := newFixture(t, fixture.Opts{})
	env = env.without("JIG_SLICE", "JIG_SESSION")
	ticket := fx.Ticket
	head := gitLog(t, fx.StoreRemote, "rev-parse", "HEAD")

	for name, c := range map[string]struct {
		args []string
		want string
	}{
		"no reason":                  {[]string{"close", ticket}, "jig close requires --reason"},
		"an unknown reason":          {[]string{"close", ticket, "--reason", "done"}, `reason "done" is none of covered, superseded, not-planned`},
		"covered with no by":         {[]string{"close", ticket, "--reason", "covered"}, "needs --by"},
		"not planned with a by":      {[]string{"close", ticket, "--reason", "not-planned", "--by", "JIG-2"}, "has no by"},
		"superseded by no ticket":    {[]string{"close", ticket, "--reason", "superseded", "--by", "JIG-404"}, `--by "JIG-404" is not a ticket of the store`},
		"superseded by itself":       {[]string{"close", ticket, "--reason", "superseded", "--by", ticket}, "cannot be superseded by itself"},
		"a ticket the store lacks":   {[]string{"close", "JIG-404", "--reason", "not-planned"}, "not found in the store"},
		"reopen of a missing ticket": {[]string{"reopen", "JIG-404"}, "not found in the store"},
	} {
		r := runJig(t, env, fx.RepoDir, c.args...)
		if r.Code != 2 || !strings.Contains(r.Stdout, "code: VALIDATION_ERROR") || !strings.Contains(r.Stdout, c.want) {
			t.Errorf("%s: jig %s: exit %d, want 2 with VALIDATION_ERROR saying %q\n%s", name, strings.Join(c.args, " "), r.Code, c.want, r.Stdout)
		}
	}
	if got := gitLog(t, fx.StoreRemote, "rev-parse", "HEAD"); got != head {
		t.Errorf("a refused closure moved the origin of the store from %s to %s", head, got)
	}

	// An entry edited by hand is checked by jig validate like any other field.
	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.CloseTicket(ticket, store.Closed{Reason: store.CloseSuperseded, By: "JIG-404"}); err != nil {
		t.Fatal(err)
	}
	r := runJig(t, env, fx.RepoDir, "validate", ticket)
	if r.Code != 2 || !strings.Contains(r.Stdout, "code: VALIDATION_ERROR") || !strings.Contains(r.Stdout, `ticket.yaml: closed: by "JIG-404" is not a ticket of the store`) {
		t.Errorf("jig validate of a ticket superseded by a missing ticket: exit %d, want 2 naming the entry\n%s", r.Code, r.Stdout)
	}
}
