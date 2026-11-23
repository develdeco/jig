package main

import (
	"encoding/json"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/mergefacts"
	"github.com/develdeco/jig/internal/phase"
	"github.com/develdeco/jig/internal/project"
	"github.com/develdeco/jig/internal/ready"
	"github.com/develdeco/jig/internal/stack"
	"github.com/develdeco/jig/internal/store"
)

const (
	digestPR2  = "https://github.com/acme/app/pull/12"
	digestPR6  = "https://github.com/acme/app/pull/16"
	digestPR11 = "https://github.com/acme/app/pull/21"
	digestPR12 = "https://github.com/acme/app/pull/22"
)

// digestRig is containerRig (the issue TK-1 with its tasks, a multi-task and
// its task, and a task of a chart with no issue) with a ticket in each of the
// states a screen words: new, ready to build, building with a failed and a
// stopped slice, in review, published with an open pull request, merged on the
// host and not recorded, landed, and closed.
func digestRig(t *testing.T) (chartRig, issueMerges) {
	t.Helper()
	r := containerRig(t)
	r.ticket(t, "TK-7", "", false)
	r.ticket(t, "TK-8", "", true)
	r.ticket(t, "TK-9", "", true)
	r.put(t, "tickets/TK-9/slices.yaml", sliceFile("alpha", "beta", "gamma", "delta"))
	r.ticket(t, "TK-10", "", true)
	r.ticket(t, "TK-11", "", true)
	r.ticket(t, "TK-12", "", true)
	r.ticket(t, "TK-13", "closed:\n  reason: covered\n  by: develdeco/jig#82\n  at: 2026-11-01\n", false)

	state := func(id, slice, st string) {
		t.Helper()
		if err := r.st.WriteSliceState(id, slice, store.SliceState{State: st, Attempts: 1}); err != nil {
			t.Fatal(err)
		}
	}
	line := func(id string, l journal.Line) {
		t.Helper()
		if err := journal.Append(r.st, id, l); err != nil {
			t.Fatal(err)
		}
	}
	// TK-9: s0 and s3 failed their last run, s1 was stopped, s2 is being built.
	state("TK-9", "s0", "building")
	state("TK-9", "s1", "building")
	state("TK-9", "s2", "building")
	state("TK-9", "s3", "building")
	line("TK-9", journal.Line{Event: journal.EventOracle, Slice: "s3", Outcome: "fail"})
	line("TK-9", journal.Line{Event: journal.EventOracle, Slice: "s0", Outcome: "fail"})
	line("TK-9", journal.Line{Event: journal.EventOracle, Slice: "s1", Outcome: "fail", Cause: journal.CauseStopped})
	// TK-10 is built and unreviewed.
	r.green(t, "TK-10")
	// Published: TK-2 into the issue's branch, TK-6 into main, TK-11 merged on
	// the host and not recorded, TK-12 landed.
	for id, url := range map[string]string{"TK-2": digestPR2, "TK-6": digestPR6, "TK-11": digestPR11, "TK-12": digestPR12} {
		r.green(t, id)
		line(id, journal.Line{Event: journal.EventPr, Outcome: "opened", URL: url})
		line(id, journal.Line{Event: journal.EventPublishDone})
	}
	line("TK-12", journal.Line{Event: journal.EventLanded})
	return r, issueMerges{
		"TK-2": openInto(digestPR2), "TK-6": openInto(digestPR6),
		"TK-11": mergedInto(digestPR11), "TK-12": mergedInto(digestPR12),
	}
}

// TestRosterDigestEqualsNextTicket: for every ticket the roster's digest has
// the phases, current phase, kind, parent, closed reason and pull requests that
// `jig next <ticket>` gives it, read against the same store and merge facts.
func TestRosterDigestEqualsNextTicket(t *testing.T) {
	t.Parallel()
	r, merges := digestRig(t)
	cfg, err := project.LoadStoreConfig(r.root)
	if err != nil {
		t.Fatal(err)
	}
	mp := project.MachineProject{}
	fleet, err := nextForStore(r.st, cfg, mp, merges)
	if err != nil {
		t.Fatal(err)
	}
	if len(fleet.Tickets) != 13 {
		t.Fatalf("the roster has %d tickets, want 13", len(fleet.Tickets))
	}
	for _, row := range fleet.Tickets {
		d := row.Digest
		if d == nil {
			t.Errorf("%s has no digest", row.ID)
			continue
		}
		n, err := nextForTicket(r.st, cfg, mp, row.ID, "", false, "", merges)
		if err != nil {
			t.Fatal(err)
		}
		wantKind := n.Kind
		if wantKind == "" {
			wantKind = store.KindTask
		}
		if d.Kind != wantKind || d.Parent != n.Parent || d.Closed != n.Closed {
			t.Errorf("%s: digest kind %q parent %q closed %q, jig next has %q %q %q", row.ID, d.Kind, d.Parent, d.Closed, wantKind, n.Parent, n.Closed)
		}
		if n.Phase == nil {
			if d.Current != "" || len(d.Phases) != 0 {
				t.Errorf("%s: jig next has no phase, the digest has %q %v", row.ID, d.Current, d.Phases)
			}
			if d.State != n.State {
				t.Errorf("%s: the container's digest state is %q, jig next says %q", row.ID, d.State, n.State)
			}
			if !reflect.DeepEqual(d.Children, n.Children) {
				t.Errorf("%s: children %v, jig next has %v", row.ID, d.Children, n.Children)
			}
			continue
		}
		if d.Current != n.Phase.Name || d.Ready != n.Phase.Ready || !reflect.DeepEqual(d.Phases, n.Phase.Phases) {
			t.Errorf("%s: digest %s %+v, jig next has %s %+v", row.ID, d.Current, d.Phases, n.Phase.Name, n.Phase.Phases)
		}
		if len(d.Phases) != 5 {
			t.Errorf("%s: %d phases, want five with Land", row.ID, len(d.Phases))
		}
		// Each pull request goes where the stack row says, in the state the
		// shared rows or the merge facts give.
		for _, pr := range d.PRs {
			found := false
			for _, s := range n.Stack {
				if s.Repo == pr.Repo && s.PRTarget == pr.Target {
					found = true
				}
			}
			if len(n.Stack) == 0 && pr.Target == "main" {
				found = true
			}
			for _, s := range n.Shared {
				if s.PR == pr.URL && s.Branch == pr.Target && s.State == pr.State {
					found = true
				}
			}
			if !found {
				t.Errorf("%s: pull request %+v matches no stack or shared row of jig next (%+v, %+v)", row.ID, pr, n.Stack, n.Shared)
			}
		}
	}

	byID := map[string]nextDigest{}
	for _, row := range fleet.Tickets {
		byID[row.ID] = *row.Digest
	}
	issue := byID["TK-1"]
	if issue.Kind != store.KindIssue || len(issue.Children) == 0 || issue.Slices != nil {
		t.Errorf("the issue's digest = %+v, want its kind and its children, no slice counts", issue)
	}
	// A task's parent is the one ticket its pull request goes into: its
	// multi-task, else the issue whose chart lists it.
	for id, want := range map[string]string{"TK-1": "", "TK-2": "TK-1", "TK-3": "TK-1", "TK-4": "TK-1", "TK-5": "TK-4", "TK-6": "", "TK-8": ""} {
		if got := byID[id].Parent; got != want {
			t.Errorf("%s's digest says its parent is %q, want %q", id, got, want)
		}
	}
	if byID["TK-4"].Kind != store.KindMultiTask || byID["TK-4"].State != issue.State {
		t.Errorf("the multi-task's digest = %+v, want its kind and its own pull request's state", byID["TK-4"])
	}
	if got := byID["TK-13"]; got.Closed != "covered by develdeco/jig#82" {
		t.Errorf("TK-13's closed reason = %q", got.Closed)
	}
	// The pull request into the issue's branch names it, the one into the
	// target names the target, and the numbers come from the URLs.
	into, err := nextForTicket(r.st, cfg, mp, "TK-2", "", false, "", merges)
	if err != nil || len(into.Stack) != 1 {
		t.Fatalf("TK-2: %+v, %v", into, err)
	}
	if want := []nextDigestPR{{Repo: "app", Number: 12, URL: digestPR2, State: "open", Target: into.Stack[0].PRTarget}}; !reflect.DeepEqual(byID["TK-2"].PRs, want) || into.Stack[0].PRTarget == "main" {
		t.Errorf("TK-2's pull requests = %+v, want %+v (into its issue's branch)", byID["TK-2"].PRs, want)
	}
	if want := []nextDigestPR{{Repo: "app", Number: 16, URL: digestPR6, State: "open", Target: "main"}}; !reflect.DeepEqual(byID["TK-6"].PRs, want) {
		t.Errorf("TK-6's pull requests = %+v, want %+v", byID["TK-6"].PRs, want)
	}
	// What a ticket waits for names the pull request the wait rests on.
	if b := byID["TK-3"].BlockedBy; len(b) != 1 || b[0].Ticket != "TK-2" || len(b[0].PRs) != 1 || b[0].PRs[0].URL != digestPR2 || b[0].PRs[0].State != ready.PROpen {
		t.Errorf("TK-3's blockers = %+v, want TK-2 with its open pull request", b)
	}
	for id, want := range map[string]nextDigestPR{
		"TK-11": {Repo: "app", Number: 21, URL: digestPR11, State: "merged", Target: "main"},
		"TK-12": {Repo: "app", Number: 22, URL: digestPR12, State: "merged", Target: "main"},
	} {
		if got := byID[id].PRs; len(got) != 1 || got[0] != want {
			t.Errorf("%s's pull requests = %+v, want %+v", id, got, want)
		}
	}
	if got := byID["TK-11"].PRs; len(got) != 1 || got[0].State != "merged" {
		t.Errorf("TK-11's pull requests = %+v, want merged", got)
	}
	if got := byID["TK-9"].Slices; got == nil || *got != (nextSliceCounts{Total: 4, Building: 1, Failed: 2, Stopped: 1}) {
		t.Errorf("TK-9's slice counts = %+v, want one building, two failed, one stopped", got)
	}
	if byID["TK-3"].Slices == nil || byID["TK-3"].Slices.Queued != 1 || byID["TK-3"].Slices.Total != 1 {
		t.Errorf("TK-3's slice counts = %+v, want its one queued slice", byID["TK-3"].Slices)
	}
	if got := byID["TK-10"].Slices; got == nil || *got != (nextSliceCounts{Total: 1, Green: 1}) {
		t.Errorf("TK-10's slice counts = %+v", got)
	}
	if byID["TK-7"].Phases[0].Name != "brief" || byID["TK-7"].PRs == nil {
		t.Errorf("TK-7's digest = %+v, want its phases and no null pull requests", byID["TK-7"])
	}
	// The digest is JSON only: the roster's text names none.
	if text, code := r.run(t, "next"); code != 0 {
		t.Errorf("jig next: exit %d", code)
	} else if strings.Contains(text, "digest") {
		t.Errorf("the text roster names the digest:\n%s", text)
	}
	out, code := r.run(t, "next", "--json")
	var roster struct {
		Tickets []struct {
			ID     string          `json:"id"`
			Digest json.RawMessage `json:"digest"`
		} `json:"tickets"`
	}
	if err := json.Unmarshal([]byte(out), &roster); code != 0 || err != nil {
		t.Fatalf("jig next --json: exit %d, %v", code, err)
	}
	for _, row := range roster.Tickets {
		if len(row.Digest) == 0 {
			t.Errorf("%s has no digest in the JSON roster", row.ID)
		}
	}
}

// countingHost is a repo host that counts the pull requests it is asked about.
type countingHost struct {
	mu    sync.Mutex
	asked []string
}

func (h *countingHost) ask(url string) (string, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.asked = append(h.asked, url)
	return ready.PROpen, true
}

// TestRosterDigestAsksTheHostOnlyAsTheListingDoes: the roster asks the host
// about the pull request a blocker waits on, as it did before the digest, and
// about no other: a pull request nobody asked about reads unknown in the
// digest, and one the listing asked about reads what the host said.
func TestRosterDigestAsksTheHostOnlyAsTheListingDoes(t *testing.T) {
	t.Parallel()
	r, _ := digestRig(t)
	cfg, err := project.LoadStoreConfig(r.root)
	if err != nil {
		t.Fatal(err)
	}
	host := &countingHost{}
	reader := &mergefacts.Reader{Store: r.st, Cfg: cfg, PRs: &mergefacts.PRCache{Ask: host.ask, MaxAge: time.Minute, Path: filepath.Join(t.TempDir(), "prs.json")}}
	fleet, err := nextForStore(r.st, cfg, project.MachineProject{}, reader)
	if err != nil {
		t.Fatal(err)
	}
	// TK-3 waits for TK-2 to merge: that is the one pull request the listing
	// asks about.
	if !reflect.DeepEqual(host.asked, []string{digestPR2}) {
		t.Errorf("the roster asked the host about %v, want only %s", host.asked, digestPR2)
	}
	states := map[string]string{}
	for _, row := range fleet.Tickets {
		for _, pr := range row.Digest.PRs {
			states[row.ID] = pr.State
		}
	}
	if states["TK-2"] != "open" {
		t.Errorf("TK-2's pull request reads %q, want what the host said (open)", states["TK-2"])
	}
	for _, id := range []string{"TK-6", "TK-11", "TK-12"} {
		if states[id] != "unknown" {
			t.Errorf("%s's pull request reads %q with no cached answer, want unknown, never open", id, states[id])
		}
	}
}

// TestADigestIsBehindByTheRuleOfJigNext: a published ticket whose stack plan owes
// its branch a merge is behind when it is done, or built again, reviewed and at
// its publish with every slice green; one with no merge owed, one that is not
// done and one built again but not yet reviewed are not. jig next <ticket> turns
// its state behind by the same rule (isBehind).
func TestADigestIsBehindByTheRuleOfJigNext(t *testing.T) {
	t.Parallel()
	facts := phase.Facts{
		Ticket: "JIG-1", BriefExists: true, Sections: 4, Approval: phase.ApprovalCurrent, ApprovedBy: "Dana", ApprovedHow: "person",
		Published: true,
		Slices:    []phase.Slice{{ID: "a", State: "green"}},
	}
	lines := []journal.Line{
		{Event: journal.EventApproval, Provenance: "person"},
		{Event: journal.EventClaim, Slice: "a"}, {Event: journal.EventVerified, Slice: "a", Commit: "c1"},
		{Event: journal.EventGateOpen, Attempt: 1}, {Event: journal.EventGateClean, Attempt: 1},
		{Event: journal.EventPr, URL: "https://example.test/pull/7", Commit: "c1"}, {Event: journal.EventPublishDone},
	}
	owed := []phase.Restack{{Repo: "app", Kind: phase.RestackTarget, Ref: "origin/main"}}

	if isBehind(phase.Derive(facts, lines), ready.StateDone) {
		t.Errorf("a published ticket owed no merge is behind")
	}
	facts.Restacks = owed
	owing := phase.Derive(facts, lines)
	if !isBehind(owing, ready.StateDone) {
		t.Errorf("a published ticket owed a merge is not behind")
	}
	if isBehind(owing, ready.StateGreen) || isBehind(owing, ready.StateReady) {
		t.Errorf("a ticket whose state is not done is behind, though it is not rebuilt")
	}

	// Built again: a fix slice queued after the publish.
	facts.Slices = append(facts.Slices, phase.Slice{ID: "b", State: "queued"})
	if rebuilt := phase.Derive(facts, lines); isBehind(rebuilt, ready.StateGreen) {
		t.Errorf("a ticket built again and not reviewed is behind")
	}
	facts.Slices[1].State = "green"
	lines = append(lines,
		journal.Line{Event: journal.EventClaim, Slice: "b"}, journal.Line{Event: journal.EventVerified, Slice: "b", Commit: "c2"},
		journal.Line{Event: journal.EventGateOpen, Attempt: 2}, journal.Line{Event: journal.EventGateClean, Attempt: 2})
	if reviewed := phase.Derive(facts, lines); !isBehind(reviewed, ready.StateGreen) {
		t.Errorf("a ticket built again, reviewed and at its publish is not behind")
	}

	// The digest carries it, from the same rule.
	r, _ := digestRig(t)
	data, err := readTicketData(r.st, "TK-2")
	if err != nil {
		t.Fatal(err)
	}
	facts.Slices = facts.Slices[:1]
	owingNow := phase.Derive(facts, lines[:7])
	if d := taskDigest(r.st, project.Config{}, nil, "TK-2", data, ready.StateDone, &owingNow, "", stack.Plan{Ticket: "TK-2"}, nil, nil, nil); !d.Behind {
		t.Errorf("the digest of a published ticket owed a merge is not behind: %+v", d)
	}
	if d := taskDigest(r.st, project.Config{}, nil, "TK-2", data, ready.StateReady, &owingNow, "", stack.Plan{Ticket: "TK-2"}, nil, nil, nil); d.Behind {
		t.Errorf("the digest of a ticket whose state is ready is behind")
	}
	if d := taskDigest(r.st, project.Config{}, nil, "TK-2", data, ready.StateDone, nil, "its slices.yaml cannot be read", stack.Plan{Ticket: "TK-2"}, nil, nil, nil); d.Behind || d.PhaseProblem == "" {
		t.Errorf("the digest of a ticket whose phase could not be derived is behind, or lost its problem: %+v", d)
	}
}

// TestASharedTaskHasNoParentAndATaskOfAnIssueHasIt: a task one issue's chart lists
// goes into that issue; a task two issues list goes into each, so it names
// neither as its parent (its shared rows name them).
func TestASharedTaskHasNoParentAndATaskOfAnIssueHasIt(t *testing.T) {
	t.Parallel()
	r := sharedRig(t)
	for id, want := range map[string]string{"SHOP-3": "", "SHOP-4": "SHOP-1", "SHOP-5": "SHOP-2", "SHOP-1": ""} {
		rec, err := r.st.ReadTicket(id)
		if err != nil {
			t.Fatal(err)
		}
		if got := parentOf(r.st, id, rec); got != want {
			t.Errorf("the parent of %s is %q, want %q", id, got, want)
		}
	}
}

// TestPhaseRowsCarryWhatAStepsMarkReadsOfItsSubSteps: a phase with a sub-step
// that needs the person, or one that is done, or a brief changed after its
// approval, says so in its row, and one with none says none.
func TestPhaseRowsCarryWhatAStepsMarkReadsOfItsSubSteps(t *testing.T) {
	t.Parallel()
	var d phase.Ticket
	d.Phases[0] = phase.Phase{Name: phase.Brief, Changed: true, Subs: []phase.Sub{{State: phase.SubPending}, {State: phase.SubNeedsYou}, {State: phase.SubPending}}}
	d.Phases[1] = phase.Phase{Name: phase.Build, Subs: []phase.Sub{{State: phase.SubActive}}}
	d.Phases[3] = phase.Phase{Name: phase.Publish, Subs: []phase.Sub{{State: phase.SubActive}, {State: phase.SubDone}, {State: phase.SubPending}}}
	rows := phaseRows(d)
	for i, want := range []struct{ needsYou, subDone, changed bool }{
		{true, false, true}, {false, false, false}, {false, false, false}, {false, true, false}, {false, false, false},
	} {
		if got := (struct{ needsYou, subDone, changed bool }{rows[i].NeedsYou, rows[i].SubDone, rows[i].Changed}); got != want {
			t.Errorf("row %d carries %+v, want %+v", i, got, want)
		}
	}
}

// TestSliceCountsCountStalledAndEnvBlockedSlices: a slice the store holds
// stalled or env-blocked is counted as such, so a screen that has not read the
// ticket's status draws the build as the one that has.
func TestSliceCountsCountStalledAndEnvBlockedSlices(t *testing.T) {
	t.Parallel()
	var data ticketData
	data.in.States = map[string]store.SliceState{}
	for id, state := range map[string]string{"a": "green", "b": "stalled", "c": "env-blocked", "d": "queued", "e": "needs-input"} {
		data.in.Slices = append(data.in.Slices, store.Slice{ID: id})
		data.in.States[id] = store.SliceState{State: state}
	}
	if got, want := *sliceCounts(data, nil), (nextSliceCounts{Total: 5, Green: 1, Queued: 1, NeedsInput: 1, Stalled: 1, EnvBlocked: 1}); got != want {
		t.Errorf("counts = %+v, want %+v", got, want)
	}
}

// TestTheRostersDigestNamesWhatAStepsMarkReads: the digest prints the fields a
// screen draws a step's mark from under the names the screens read them by, so
// a rename on either side breaks a test, not every screen's mark at once.
func TestTheRostersDigestNamesWhatAStepsMarkReads(t *testing.T) {
	t.Parallel()
	row, err := json.Marshal(nextPhaseRow{NeedsYou: true, SubDone: true, Changed: true})
	if err != nil {
		t.Fatal(err)
	}
	counts, err := json.Marshal(nextSliceCounts{Stalled: 1, EnvBlocked: 1})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"needs_you":true`, `"sub_done":true`, `"changed":true`, `"stalled":1`, `"env_blocked":1`} {
		if !strings.Contains(string(row)+string(counts), want) {
			t.Errorf("the digest prints %s %s, want %s", row, counts, want)
		}
	}
}
