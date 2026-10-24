package main

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/store"
)

// planStore is a store of Root alone: enough for the checks that read
// ticket files and journals, with no git behind it.
func planStore(t *testing.T) *store.Store {
	t.Helper()
	return &store.Store{Root: t.TempDir()}
}

// putTicket writes ticket id with a title and blockers (merged), and marks it
// published when asked.
func putTicket(t *testing.T, st *store.Store, id, title string, published bool, blockedBy ...string) {
	t.Helper()
	rec := store.Ticket{Title: title}
	for _, b := range blockedBy {
		rec.BlockedBy = append(rec.BlockedBy, store.TicketBlockedBy{Ticket: b, Kind: "merged"})
	}
	if err := st.CreateTicketRecord(id, rec); err != nil {
		t.Fatal(err)
	}
	if published {
		if err := journal.Append(st, id, journal.Line{Event: journal.EventPublishDone}); err != nil {
			t.Fatal(err)
		}
	}
}

func TestSliceGroups(t *testing.T) {
	t.Parallel()
	mk := func(spec ...string) []store.Slice {
		var out []store.Slice
		for _, s := range spec {
			id, deps, _ := strings.Cut(s, "<")
			sl := store.Slice{ID: id}
			if deps != "" {
				sl.BlockedBy = strings.Split(deps, ",")
			}
			out = append(out, sl)
		}
		return out
	}
	cases := []struct {
		name string
		in   []store.Slice
		want [][]string
	}{
		{"no slices", nil, [][]string{}},
		{"one slice", mk("a"), [][]string{{"a"}}},
		{"a chain is one group", mk("a", "b<a", "c<b"), [][]string{{"a", "b", "c"}}},
		{"an edge counts either way round", mk("a<c", "b", "c"), [][]string{{"a", "c"}, {"b"}}},
		{"three concerns", mk("a", "b<a", "c", "d"), [][]string{{"a", "b"}, {"c"}, {"d"}}},
		{"a join connects two chains", mk("a", "b", "c<a,b"), [][]string{{"a", "b", "c"}}},
		{"an edge to no slice connects nothing", mk("a<nope", "b"), [][]string{{"a"}, {"b"}}},
	}
	for _, c := range cases {
		if got := sliceGroups(c.in); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: sliceGroups = %v, want %v", c.name, got, c.want)
		}
	}
}

func TestValidateUpstreamEdgesReportOnlyWhatLiesBehindTheTicket(t *testing.T) {
	t.Parallel()
	st := planStore(t)
	putTicket(t, st, "T-1", "Base", false, "T-404")      // dangling, two steps up
	putTicket(t, st, "T-2", "Middle", false, "T-1")      // one step up
	putTicket(t, st, "T-3", "Top", false, "T-2", "T-9")  // T-9 is its own dangling edge
	putTicket(t, st, "T-4", "Elsewhere", false, "T-505") // dangling, but behind nothing

	got := validateUpstreamEdges(st, "T-3")
	if len(got) != 1 || !strings.Contains(got[0], "upstream ticket T-1") || !strings.Contains(got[0], `"T-404"`) || !strings.Contains(got[0], "T-3 waits on") {
		t.Errorf("validateUpstreamEdges(T-3) = %q, want T-1's cut edge alone: T-9 is validateTicketDeps to report, T-4 is not upstream", got)
	}
	if got := validateUpstreamEdges(st, "T-4"); len(got) != 0 {
		t.Errorf("validateUpstreamEdges(T-4) = %q, want none: its own edge is validateTicketDeps to report", got)
	}
}

func TestValidateUpstreamEdgesDoNotFollowPublishedTicketsOrLoop(t *testing.T) {
	t.Parallel()
	st := planStore(t)
	putTicket(t, st, "T-1", "Shipped long ago", true, "T-404")
	putTicket(t, st, "T-2", "Waits on it", false, "T-1")
	if got := validateUpstreamEdges(st, "T-2"); len(got) != 0 {
		t.Errorf("a published upstream ticket was followed: %q", got)
	}

	// A cycle ends the walk (validateTicketDeps names the cycle itself).
	putTicket(t, st, "T-5", "Five", false, "T-6")
	putTicket(t, st, "T-6", "Six", false, "T-5", "T-7")
	if got := validateUpstreamEdges(st, "T-5"); len(got) != 1 || !strings.Contains(got[0], "upstream ticket T-6") {
		t.Errorf("validateUpstreamEdges over a cycle = %q, want T-6's dangling T-7 once", got)
	}
}

func TestValidateUpstreamEdgesFollowAnAliasToItsTicket(t *testing.T) {
	t.Parallel()
	st := planStore(t)
	if err := st.CreateTicketRecord("DEMO-1", store.Ticket{Title: "Renamed", Aliases: []string{"T-1"},
		BlockedBy: []store.TicketBlockedBy{{Ticket: "DEMO-404", Kind: "merged"}}}); err != nil {
		t.Fatal(err)
	}
	putTicket(t, st, "DEMO-2", "Waits on the old id", false, "T-1")
	got := validateUpstreamEdges(st, "DEMO-2")
	if len(got) != 1 || !strings.Contains(got[0], "upstream ticket DEMO-1") {
		t.Errorf("validateUpstreamEdges = %q, want the renamed ticket reached through its alias", got)
	}
}

func TestValidateAdvisoriesNameShippedWorkTheGoalComesNear(t *testing.T) {
	t.Parallel()
	st := planStore(t)
	putTicket(t, st, "T-1", "Fix Clamp so the upper bound holds", true)
	putTicket(t, st, "T-2", "Greeting tone for the formal case", false) // open: not covered work yet
	ledger := "# Ledger\n\n## OLD-7 - Smoke the rig environment class\n\n**Answers:**\nnone\n" +
		"\n## T-9 - Own entry about a brand new thing\n\n**Answers:**\nnone\n"
	if err := os.WriteFile(filepath.Join(st.Root, "ledger.md"), []byte(ledger), 0o644); err != nil {
		t.Fatal(err)
	}
	slices := []store.Slice{
		{ID: "a", Goal: "Fix Clamp so TestClamp passes with the upper bound"},
		{ID: "b", Goal: "Greeting tone for the formal case"},
		{ID: "c", Goal: "Smoke-test the rig environment class"},
		{ID: "d", Goal: "A brand new thing"},
	}
	got := validateAdvisories(st, "T-9", slices)
	var codes []string
	for _, a := range got {
		codes = append(codes, a.Code)
	}
	if want := []string{advisorySlicesUnconnected, advisoryWorkCovered, advisoryWorkCovered}; !reflect.DeepEqual(codes, want) {
		t.Fatalf("advisories = %+v, want the unconnected slices, then slices a and c as covered", got)
	}
	if m := got[1].Message; !strings.Contains(m, "slice a") || !strings.Contains(m, "T-1") || !strings.Contains(m, "0.57") {
		t.Errorf("first covered advisory = %q, want slice a against T-1 at 0.57", m)
	}
	if m := got[2].Message; !strings.Contains(m, "slice c") || !strings.Contains(m, "OLD-7") {
		t.Errorf("second covered advisory = %q, want slice c against the ledger entry OLD-7", m)
	}
	if table := advisoryTable(got); !strings.HasPrefix(table, "advisories[3]{code,message,help}:\n  SLICES_UNCONNECTED,") {
		t.Errorf("advisoryTable = %q", table)
	}
	if advisoryTable(nil) != "" {
		t.Error("advisoryTable of no advisories is not empty")
	}
}

// TestValidateAdvisoriesLeaveFixSlicesOutOfTheGroups: the slices a gate
// round appended (from_gate) have no blocked_by edge to the planned work, and
// say nothing about whether the plan is one concern.
func TestValidateAdvisoriesLeaveFixSlicesOutOfTheGroups(t *testing.T) {
	t.Parallel()
	st := planStore(t)
	slices := []store.Slice{
		{ID: "round", Goal: "Discount once"},
		{ID: "clamp", Goal: "Clamp the percent", BlockedBy: []string{"round"}},
		{ID: "fix-1-cart-test", Goal: "Fix these gate findings", FromGate: 1},
		{ID: "fix-2-cart-test", Goal: "Fix these gate findings", FromGate: 2},
	}
	for _, a := range validateAdvisories(st, "T-9", slices) {
		if a.Code == advisorySlicesUnconnected {
			t.Errorf("advisory %q: fix slices counted as unconnected groups", a.Message)
		}
	}
}

func TestRecordApprovalWritesOnceAndLetsAPersonUpgradeAnAgent(t *testing.T) {
	t.Parallel()
	fx := newFixture(t, fixture.Opts{})
	e := testEnv(fx.Home).withClock(fixedClock(time.Date(2026, 10, 23, 12, 0, 0, 0, time.UTC)))
	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatal(err)
	}
	sum, err := st.BriefSHA256(fx.Ticket)
	if err != nil {
		t.Fatal(err)
	}
	approvals := func() int {
		lines, err := journal.Read(st, fx.Ticket)
		if err != nil {
			t.Fatal(err)
		}
		n := 0
		for _, l := range lines {
			if l.Event == journal.EventApproval {
				n++
			}
		}
		return n
	}
	step := func(what, sum, provenance, wantResult string, wantLines int) {
		t.Helper()
		a, result, err := recordApproval(e, st, fx.Ticket, sum, provenance)
		if err != nil || result != wantResult {
			t.Fatalf("%s: recordApproval = %+v, %q, %v, want %q", what, a, result, err, wantResult)
		}
		if n := approvals(); n != wantLines {
			t.Errorf("%s: %d approval lines, want %d", what, n, wantLines)
		}
		got, ok, err := st.ReadApproval(fx.Ticket)
		if err != nil || !ok || got != a {
			t.Errorf("%s: the approval on disk = %+v, %v, %v, want %+v", what, got, ok, err, a)
		}
	}

	step("an agent validates", sum, "agent", approvalRecorded, 1)
	a, _, _ := st.ReadApproval(fx.Ticket)
	if a.BriefSHA256 != sum || a.Provenance != "agent" || a.At != "2026-10-23T12:00:00Z" || a.By == "" || a.Email == "" {
		t.Errorf("approval = %+v, want the brief hash, agent, the clock and the identity git resolves", a)
	}
	step("the same agent again", sum, "agent", approvalUnchanged, 1)
	step("a person says the same", sum, "person", approvalRecorded, 2)
	step("an agent after the person", sum, "agent", approvalUnchanged, 2)
	other := strings.Repeat("ab", 32)
	step("the brief changed", other, "agent", approvalRecorded, 3)
	if got, _, _ := st.ReadApproval(fx.Ticket); got.BriefSHA256 != other || got.Provenance != "agent" {
		t.Errorf("approval = %+v, want it for the changed brief, as an agent now", got)
	}
}

func TestRecallTicketsCarryStateShippedAndBlockers(t *testing.T) {
	t.Parallel()
	st := planStore(t)
	putTicket(t, st, "T-1", "Shipped", true)
	putTicket(t, st, "T-2", "Waits on a shipped one", false, "T-1")
	putTicket(t, st, "T-3", "Waits on an open one", false, "T-2")
	got, err := recallTickets(st)
	if err != nil {
		t.Fatal(err)
	}
	var rows []string
	for _, tk := range got {
		var blockers []string
		for _, b := range tk.BlockedBy {
			blockers = append(blockers, b.Ticket+"/"+b.Kind)
		}
		rows = append(rows, fmt.Sprintf("%s %s shipped=%v %v", tk.ID, tk.State, tk.Shipped, blockers))
	}
	want := []string{
		"T-1 done shipped=true []",
		"T-2 new shipped=false [T-1/merged]",
		"T-3 blocked shipped=false [T-2/merged]",
	}
	if !reflect.DeepEqual(rows, want) {
		t.Errorf("recallTickets = %q, want %q", rows, want)
	}
	if got[1].Title != "Waits on a shipped one" {
		t.Errorf("title = %q", got[1].Title)
	}
}

// TestValidateApprovesAsAPersonOnlyAtATerminal: the approval says whether a
// person was at the keyboard, which is whether stdin is a terminal.
func TestValidateApprovesAsAPersonOnlyAtATerminal(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name     string
		e        func(env) env
		wantProv string
	}{
		{"piped", func(e env) env { return e }, "agent"},
		{"at a terminal", env.atTerminal, "person"},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Parallel()
			fx := newFixture(t, fixture.Opts{})
			e := c.e(testEnv(fx.Home))
			out, code := runMain(t, e, "", "validate", fx.Ticket, "--store", fx.StoreDir)
			if code != 0 || !strings.HasPrefix(out, "valid: yes\napproval:\n  result: recorded\n") ||
				!strings.Contains(out, "  provenance: "+c.wantProv+"\n") {
				t.Fatalf("validate: exit %d, want valid and recorded with provenance %s\n%s", code, c.wantProv, out)
			}
			st, err := store.Open(fx.StoreDir)
			if err != nil {
				t.Fatal(err)
			}
			if a, ok, err := st.ReadApproval(fx.Ticket); err != nil || !ok || a.Provenance != c.wantProv {
				t.Errorf("approval = %+v, %v, %v, want provenance %s", a, ok, err, c.wantProv)
			}
		})
	}
}
