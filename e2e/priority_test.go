package e2e

import (
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/store"
)

// priorityOut is the part of `jig next --json` the priority tests read.
type priorityOut struct {
	Tickets []struct {
		ID   string `json:"id"`
		Rank *int   `json:"rank"`
	} `json:"tickets"`
	Next        []string `json:"next"`
	SharedRanks []struct {
		Rank    int      `json:"rank"`
		Tickets []string `json:"tickets"`
	} `json:"shared_ranks"`
	WorkspaceOverlaps []struct {
		Repo      string   `json:"repo"`
		Workspace string   `json:"workspace"`
		Tickets   []string `json:"tickets"`
	} `json:"workspace_overlaps"`
}

// ranks maps each ticket of the listing to its rank, absent for none.
func (o priorityOut) ranks() map[string]int {
	out := map[string]int{}
	for _, t := range o.Tickets {
		if t.Rank != nil {
			out[t.ID] = *t.Rank
		}
	}
	return out
}

// addTicket records ticket id with one slice per workspace given (slices x1,
// x2, ... in the order named), none of them blocked, so it is ready unless
// blockedBy says otherwise.
func addTicket(t *testing.T, st *store.Store, id, title string, blockedBy []store.TicketBlockedBy, workspaces ...string) {
	t.Helper()
	if err := st.CreateTicketRecord(id, store.Ticket{Title: title, BlockedBy: blockedBy}); err != nil {
		t.Fatalf("CreateTicketRecord %s: %v", id, err)
	}
	var slices []store.Slice
	for i, ws := range workspaces {
		slices = append(slices, store.Slice{ID: "x" + string(rune('1'+i)), Workspace: ws, Goal: "Do it.", Oracle: "test", BlockedBy: []string{}})
	}
	if err := st.AppendSlices(id, slices); err != nil {
		t.Fatalf("AppendSlices %s: %v", id, err)
	}
}

// rankTicket runs `jig rank` and fails the test unless it exits 0.
func rankTicket(t *testing.T, env jigEnv, cwd string, args ...string) jigResult {
	t.Helper()
	r := runJig(t, env, cwd, append([]string{"rank"}, args...)...)
	if r.Code != 0 || r.Stderr != "" {
		t.Fatalf("jig rank %v: exit %d\nstdout:\n%s\nstderr:\n%s", args, r.Code, r.Stdout, r.Stderr)
	}
	return r
}

// TestNextOrdersReadyTicketsByRank: three ready tickets come out by rank, then
// by id, with the unranked last, and a rank two of them hold is flagged; a
// ticket that is not ready is not in the order and does not make a rank shared.
func TestNextOrdersReadyTicketsByRank(t *testing.T) {
	t.Parallel()
	fx, env := newFixture(t, fixture.Opts{})
	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	addTicket(t, st, "JIG-2", "Second", nil, "beta")
	addTicket(t, st, "JIG-3", "Third", nil, "beta")

	// Nobody ranked anything: id order, and nothing to flag or to read.
	o := nextJSON[priorityOut](t, env, fx.RepoDir)
	if want := []string{"JIG-1", "JIG-2", "JIG-3"}; !reflect.DeepEqual(o.Next, want) || len(o.SharedRanks) != 0 || len(o.ranks()) != 0 {
		t.Fatalf("unranked: next = %v, shared = %+v, ranks = %v, want %v and neither", o.Next, o.SharedRanks, o.ranks(), want)
	}
	if r := runJig(t, env, fx.RepoDir, "next"); !strings.Contains(r.Stdout, "Order the ready tickets with `jig rank <ticket> <n>`") {
		t.Errorf("jig next with three unranked ready tickets does not offer jig rank:\n%s", r.Stdout)
	}

	// Two of them share rank 2, and JIG-1 is unranked: rank, then id, then the
	// unranked. The shared rank is flagged.
	rankTicket(t, env, fx.RepoDir, "JIG-3", "2")
	rankTicket(t, env, fx.RepoDir, "JIG-2", "2")
	o = nextJSON[priorityOut](t, env, fx.RepoDir)
	if want := []string{"JIG-2", "JIG-3", "JIG-1"}; !reflect.DeepEqual(o.Next, want) {
		t.Errorf("JIG-2 and JIG-3 at rank 2: next = %v, want %v", o.Next, want)
	}
	if len(o.SharedRanks) != 1 || o.SharedRanks[0].Rank != 2 || !reflect.DeepEqual(o.SharedRanks[0].Tickets, []string{"JIG-2", "JIG-3"}) {
		t.Errorf("shared_ranks = %+v, want rank 2 shared by JIG-2 and JIG-3", o.SharedRanks)
	}
	if want := map[string]int{"JIG-2": 2, "JIG-3": 2}; !reflect.DeepEqual(o.ranks(), want) {
		t.Errorf("ranks = %v, want %v", o.ranks(), want)
	}
	r := runJig(t, env, fx.RepoDir, "next")
	for _, want := range []string{
		"next[3]{ticket,rank,title}:\n  JIG-2,2,Second\n  JIG-3,2,Third\n  JIG-1,-,\n",
		"warning: rank 2 is shared by JIG-2 and JIG-3: they are ordered by id\n",
		"Run `jig next JIG-2` to see what is ready to build there",
	} {
		if !strings.Contains(r.Stdout, want) {
			t.Errorf("jig next lacks %q:\n%s", want, r.Stdout)
		}
	}

	// A ticket that waits for JIG-1 is not ready: it is not in the order and
	// its rank is nobody's to share.
	addTicket(t, st, "JIG-4", "Fourth", []store.TicketBlockedBy{{Ticket: "JIG-1", Kind: "merged"}}, "beta")
	rankTicket(t, env, fx.RepoDir, "JIG-4", "2")
	o = nextJSON[priorityOut](t, env, fx.RepoDir)
	if want := []string{"JIG-2", "JIG-3", "JIG-1"}; !reflect.DeepEqual(o.Next, want) || len(o.SharedRanks) != 1 || len(o.SharedRanks[0].Tickets) != 2 {
		t.Errorf("with JIG-4 blocked at rank 2: next = %v, shared = %+v, want %v and JIG-2, JIG-3 only", o.Next, o.SharedRanks, want)
	}
	if o.ranks()["JIG-4"] != 2 {
		t.Errorf("the blocked JIG-4 does not carry its rank: %v", o.ranks())
	}

	// A lower rank goes first, and the shared rank is shared no more.
	rankTicket(t, env, fx.RepoDir, "JIG-3", "1")
	o = nextJSON[priorityOut](t, env, fx.RepoDir)
	if want := []string{"JIG-3", "JIG-2", "JIG-1"}; !reflect.DeepEqual(o.Next, want) || len(o.SharedRanks) != 0 {
		t.Errorf("JIG-3 at rank 1: next = %v, shared = %+v, want %v and nothing shared", o.Next, o.SharedRanks, want)
	}
	if r := runJig(t, env, fx.RepoDir, "next"); strings.Contains(r.Stdout, "is shared by") {
		t.Errorf("jig next flags a shared rank when no ready tickets share one:\n%s", r.Stdout)
	}

	// Clearing a rank sends the ticket to the back, by id among the unranked.
	rankTicket(t, env, fx.RepoDir, "JIG-3", "--clear")
	o = nextJSON[priorityOut](t, env, fx.RepoDir)
	if want := []string{"JIG-2", "JIG-1", "JIG-3"}; !reflect.DeepEqual(o.Next, want) {
		t.Errorf("JIG-3 cleared: next = %v, want %v", o.Next, want)
	}
}

// TestNextWarnsOfReadyTicketsInTheSameWorkspace: two ready tickets whose slices
// build in the same workspace of a repo are named as a pair with the
// workspace, in the order to start them; a workspace no repo declares and a
// ticket that is not ready are not.
func TestNextWarnsOfReadyTicketsInTheSameWorkspace(t *testing.T) {
	t.Parallel()
	fx, env := newFixture(t, fixture.Opts{SecondRepo: true})
	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	// JIG-1 builds in alpha and beta of fixture-repo and gamma of second-repo.
	// JIG-2 builds in gamma, JIG-3 in alpha (written with its repo) and in a
	// workspace no repo declares.
	addTicket(t, st, "JIG-2", "Second", nil, "gamma")
	addTicket(t, st, "JIG-3", "Third", nil, "fixture-repo/alpha", "delta")

	type pair struct{ repo, workspace, tickets string }
	pairs := func(o priorityOut) []pair {
		var out []pair
		for _, w := range o.WorkspaceOverlaps {
			out = append(out, pair{w.Repo, w.Workspace, strings.Join(w.Tickets, "+")})
		}
		return out
	}

	o := nextJSON[priorityOut](t, env, fx.RepoDir)
	want := []pair{{"second-repo", "gamma", "JIG-1+JIG-2"}, {"fixture-repo", "alpha", "JIG-1+JIG-3"}}
	if !reflect.DeepEqual(pairs(o), want) {
		t.Fatalf("workspace_overlaps = %v, want %v", pairs(o), want)
	}
	r := runJig(t, env, fx.RepoDir, "next")
	for _, line := range []string{
		"warning: ready tickets build in the same workspace\n",
		"workspace_overlap[2]{repo,workspace,tickets}:\n  second-repo,gamma,JIG-1+JIG-2\n  fixture-repo,alpha,JIG-1+JIG-3\n",
		"Do not run tickets that share a workspace",
	} {
		if !strings.Contains(r.Stdout, line) {
			t.Errorf("jig next lacks %q:\n%s", line, r.Stdout)
		}
	}

	// The pairs name the ticket to start first first.
	rankTicket(t, env, fx.RepoDir, "JIG-3", "1")
	rankTicket(t, env, fx.RepoDir, "JIG-2", "2")
	rankTicket(t, env, fx.RepoDir, "JIG-1", "3")
	o = nextJSON[priorityOut](t, env, fx.RepoDir)
	want = []pair{{"fixture-repo", "alpha", "JIG-3+JIG-1"}, {"second-repo", "gamma", "JIG-2+JIG-1"}}
	if !reflect.DeepEqual(pairs(o), want) {
		t.Errorf("ranked 3, 2, 1: workspace_overlaps = %v, want %v", pairs(o), want)
	}

	// A ticket whose slice is being built is not ready: it is not warned of.
	setStates(t, st, "JIG-2", map[string]string{"x1": "building"})
	o = nextJSON[priorityOut](t, env, fx.RepoDir)
	if want := []pair{{"fixture-repo", "alpha", "JIG-3+JIG-1"}}; !reflect.DeepEqual(pairs(o), want) {
		t.Errorf("JIG-2 building: workspace_overlaps = %v, want %v", pairs(o), want)
	}

	// With nothing ready there is nothing to overlap.
	setStates(t, st, "JIG-3", map[string]string{"x1": "building", "x2": "building"})
	setStates(t, st, "JIG-1", map[string]string{"a": "building", "c": "building", "d": "building", "e": "building"})
	o = nextJSON[priorityOut](t, env, fx.RepoDir)
	if len(o.WorkspaceOverlaps) != 0 || len(o.Next) != 0 {
		t.Errorf("nothing ready: next = %v, workspace_overlaps = %v, want none", o.Next, pairs(o))
	}
	if r := runJig(t, env, fx.RepoDir, "next"); strings.Contains(r.Stdout, "workspace_overlap") {
		t.Errorf("jig next names a workspace overlap with no ready ticket:\n%s", r.Stdout)
	}
}

// TestRankWritesTicketRecord: jig rank writes the rank into the ticket's
// ticket.yaml, commits that record alone and pushes it to the store's origin;
// the rest of the record is kept; a rank already held is left as it is;
// --clear removes the key; and a call that cannot name a rank writes nothing.
func TestRankWritesTicketRecord(t *testing.T) {
	t.Parallel()
	fx, env := newFixture(t, fixture.Opts{})
	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	addTicket(t, st, "JIG-2", "Second", []store.TicketBlockedBy{{Ticket: "JIG-1", Kind: "stacked"}}, "beta")
	head := func() string { return gitLog(t, fx.StoreDir, "rev-parse", "HEAD") }

	r := rankTicket(t, env, fx.RepoDir, "JIG-2", "4")
	if !strings.Contains(r.Stdout, "rank:\n  ticket: JIG-2\n  rank: 4\n  result: recorded\n") {
		t.Errorf("jig rank JIG-2 4 says:\n%s", r.Stdout)
	}
	rec, err := st.ReadTicket("JIG-2")
	if err != nil || rec.Rank == nil || *rec.Rank != 4 {
		t.Fatalf("ticket JIG-2 after jig rank = %+v, %v, want rank 4", rec, err)
	}
	if rec.Title != "Second" || !reflect.DeepEqual(rec.BlockedBy, []store.TicketBlockedBy{{Ticket: "JIG-1", Kind: "stacked"}}) {
		t.Errorf("jig rank changed the rest of the record: %+v", rec)
	}
	if data := readFileOrFatal(t, st.TicketFilePath("JIG-2")); !strings.Contains(string(data), "\nrank: 4\n") {
		t.Errorf("ticket.yaml lacks the rank key:\n%s", data)
	}

	// One commit of that record alone, already on the origin.
	if subject := gitLog(t, fx.StoreDir, "log", "-1", "--format=%s"); subject != "JIG-2: rank 4" {
		t.Errorf("store commit = %q, want \"JIG-2: rank 4\"", subject)
	}
	if files := gitLog(t, fx.StoreDir, "show", "--name-only", "--format=", "HEAD"); strings.TrimSpace(files) != "tickets/JIG-2/ticket.yaml" {
		t.Errorf("the rank commit holds %q, want tickets/JIG-2/ticket.yaml alone", files)
	}
	if origin := gitLog(t, fx.StoreRemote, "rev-parse", "HEAD"); origin != head() {
		t.Errorf("the origin is at %s, want the rank commit %s", origin, head())
	}

	// The same rank again is not a change: no commit.
	before := head()
	if r := rankTicket(t, env, fx.RepoDir, "JIG-2", "4"); !strings.Contains(r.Stdout, "result: unchanged") || head() != before {
		t.Errorf("jig rank JIG-2 4 again: head moved = %v, says:\n%s", head() != before, r.Stdout)
	}

	// A new rank replaces the old.
	rankTicket(t, env, fx.RepoDir, "JIG-2", "1")
	if rec, _ := st.ReadTicket("JIG-2"); rec.Rank == nil || *rec.Rank != 1 {
		t.Errorf("rank after jig rank JIG-2 1 = %v, want 1", rec.Rank)
	}

	// jig validate takes the record with its rank.
	if r := runJig(t, env, fx.RepoDir, "validate", "JIG-2", "--check"); strings.Contains(r.Stdout, "ticket.yaml") {
		t.Errorf("jig validate JIG-2 --check complains of ticket.yaml:\n%s", r.Stdout)
	}

	// --clear removes the key and keeps the rest.
	r = rankTicket(t, env, fx.RepoDir, "JIG-2", "--clear")
	if !strings.Contains(r.Stdout, "rank: none") || !strings.Contains(r.Stdout, "result: recorded") {
		t.Errorf("jig rank JIG-2 --clear says:\n%s", r.Stdout)
	}
	rec, _ = st.ReadTicket("JIG-2")
	if rec.Rank != nil || rec.Title != "Second" || len(rec.BlockedBy) != 1 {
		t.Errorf("record after --clear = %+v, want no rank and the rest kept", rec)
	}
	if data := readFileOrFatal(t, st.TicketFilePath("JIG-2")); strings.Contains(string(data), "rank") {
		t.Errorf("ticket.yaml still has a rank key after --clear:\n%s", data)
	}
	if subject := gitLog(t, fx.StoreDir, "log", "-1", "--format=%s"); subject != "JIG-2: clear rank" {
		t.Errorf("store commit after --clear = %q, want \"JIG-2: clear rank\"", subject)
	}

	// Clearing what is not there writes nothing, not even a record.
	before = head()
	if r := rankTicket(t, env, fx.RepoDir, "JIG-1", "--clear"); !strings.Contains(r.Stdout, "result: unchanged") || head() != before {
		t.Errorf("jig rank JIG-1 --clear on an unranked ticket: head moved = %v, says:\n%s", head() != before, r.Stdout)
	}
	if _, err := os.Stat(st.TicketFilePath("JIG-1")); !os.IsNotExist(err) {
		t.Errorf("--clear wrote a ticket.yaml for JIG-1 (stat err = %v)", err)
	}

	// Calls that name no rank, or a ticket that is not there, change nothing.
	for _, args := range [][]string{
		{"JIG-2"},
		{"JIG-2", "0"},
		{"JIG-2", "soon"},
		{"JIG-2", "3", "--clear"},
		{"JIG-9", "1"},
		{},
	} {
		r := runJig(t, env, fx.RepoDir, append([]string{"rank"}, args...)...)
		if r.Code == 0 || !strings.Contains(r.Stdout, "error") {
			t.Errorf("jig rank %v: exit %d, want a refusal:\n%s", args, r.Code, r.Stdout)
		}
	}
	if head() != before {
		t.Errorf("a refused jig rank moved the store's head")
	}
}
