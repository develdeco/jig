package main

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/mergefacts"
	"github.com/develdeco/jig/internal/project"
	"github.com/develdeco/jig/internal/ready"
	"github.com/develdeco/jig/internal/repohost"
	"github.com/develdeco/jig/internal/store"
)

// conflictRig is a store and a clone "app" of a project, with tickets minted
// and a base commit of f.txt on main.
type conflictRig struct {
	t   *testing.T
	st  *store.Store
	dir string
	cfg project.Config
	mp  project.MachineProject
}

func newConflictRig(t *testing.T, tickets ...string) conflictRig {
	t.Helper()
	_, storeRoot := setupGraduateStore(t)
	st, err := store.Open(storeRoot)
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range tickets {
		if err := st.CreateTicketRecord(id, store.Ticket{Title: id}); err != nil {
			t.Fatal(err)
		}
	}
	dir := cmdGitRepo(t, filepath.Join(t.TempDir(), "app"))
	r := conflictRig{t: t, st: st, dir: dir,
		cfg: project.Config{Name: "demo", Repos: []project.Repo{{Remote: "git@example.invalid:demo/app.git"}}},
		mp:  project.MachineProject{Clones: map[string]string{"app": dir}}}
	r.write("1\n2\n3\n4\n5\n6\n7\n8\n9\n10\n")
	gitIn(t, dir, "add", "-A")
	gitIn(t, dir, "commit", "-m", "base")
	return r
}

func (r conflictRig) write(body string) {
	r.t.Helper()
	if err := os.WriteFile(filepath.Join(r.dir, "f.txt"), []byte(body), 0o644); err != nil {
		r.t.Fatal(err)
	}
}

// branch cuts name from main with f.txt changed to body.
func (r conflictRig) branch(name, body string) {
	r.t.Helper()
	gitIn(r.t, r.dir, "switch", "-c", name, "main")
	r.write(body)
	gitIn(r.t, r.dir, "commit", "-am", name)
	gitIn(r.t, r.dir, "switch", "main")
}

const (
	topChanged    = "TOP\n2\n3\n4\n5\n6\n7\n8\n9\n10\n"
	topChangedAlt = "ALT\n2\n3\n4\n5\n6\n7\n8\n9\n10\n"
	topChangedNew = "NEW\n2\n3\n4\n5\n6\n7\n8\n9\n10\n"
	bottomChanged = "1\n2\n3\n4\n5\n6\n7\n8\n9\nBOTTOM\n"
)

// fakeMerges is a merge source whose pull request states are given by url. It
// answers the merge facts nothing, as the open filter asks only for the states.
type fakeMerges map[string]string

func (f fakeMerges) Of(string) (ready.Merge, error) { return ready.Merge{}, nil }

func (f fakeMerges) PullRequests() mergefacts.PRs { return fakePRs(f) }

type fakePRs map[string]string

func (f fakePRs) Read(url string) mergefacts.PRRead { return mergefacts.PRRead{State: f[url]} }

// publish records that ticket id published with a pull request at url.
func publish(t *testing.T, st *store.Store, id, url string) {
	t.Helper()
	if url != "" {
		if err := journal.Append(st, id, journal.Line{Event: journal.EventPr, URL: url}); err != nil {
			t.Fatal(err)
		}
	}
	if err := journal.Append(st, id, journal.Line{Event: journal.EventPublishDone}); err != nil {
		t.Fatal(err)
	}
}

// pairsOf names the pairs of a scan as "A+B".
func pairsOf(pairs []testedPair) []string {
	out := []string{}
	for _, p := range pairs {
		out = append(out, p.A+"+"+p.B)
	}
	return out
}

// TestAdviceLeavesOutBranchesOfTicketsThatAreNotOpen: a ticket closed without a
// pull request, one whose work merged, and a branch bound to no ticket at all
// are no open ticket: the open ticket is not told to stack on any of them,
// though their branches change the same lines.
func TestAdviceLeavesOutBranchesOfTicketsThatAreNotOpen(t *testing.T) {
	r := newConflictRig(t, "T-2", "T-3", "T-4")
	if err := r.st.CreateTicketRecord("T-1", store.Ticket{Title: "closed", Closed: &store.Closed{Reason: store.CloseNotPlanned}}); err != nil {
		t.Fatal(err)
	}
	publish(t, r.st, "T-3", "https://github.com/demo/app/pull/3")
	r.branch("T-1-closed", topChanged)
	r.branch("T-2-open", topChangedAlt)
	r.branch("T-3-merged", topChangedNew)
	r.branch("T-4-open", "FOUR\n2\n3\n4\n5\n6\n7\n8\n9\n10\n")
	r.branch("T-9-nobody", "NINE\n2\n3\n4\n5\n6\n7\n8\n9\n10\n")

	merged := fakeMerges{"https://github.com/demo/app/pull/3": ready.PRMerged}
	stillOpen := fakeMerges{"https://github.com/demo/app/pull/3": ready.PROpen}
	_, pairs := scanOverlaps(r.st, r.cfg, r.mp, ticketOpenFilter(r.st, merged), "")
	if got, want := pairsOf(pairs), []string{"T-2+T-4"}; !reflect.DeepEqual(got, want) {
		t.Errorf("pairs = %v, want %v: only open tickets are paired", got, want)
	}
	var out bytes.Buffer
	printConflicts(&out, r.st, r.cfg, r.mp, merged, "T-4")
	for _, leftover := range []string{"T-1", "T-3", "T-9"} {
		if strings.Contains(out.String(), leftover) {
			t.Errorf("the advice names %s, which is not an open ticket:\n%s", leftover, out.String())
		}
	}
	if !strings.Contains(out.String(), "app,T-2,f.txt,T-2-open") {
		t.Errorf("the advice lacks the open ticket's row:\n%s", out.String())
	}
	// A published ticket whose work has not merged is still open.
	_, pairs = scanOverlaps(r.st, r.cfg, r.mp, ticketOpenFilter(r.st, stillOpen), "T-4")
	if got := pairsOf(pairs); !reflect.DeepEqual(got, []string{"T-2+T-4", "T-3+T-4"}) {
		t.Errorf("pairs = %v, want T-3 (published, not merged) paired too", got)
	}
}

// TestAdviceForATicketMergesOnlyItsOwnPairs: with a ticket named, the pairs
// that do not hold it are not merged, however many share a file.
func TestAdviceForATicketMergesOnlyItsOwnPairs(t *testing.T) {
	r := newConflictRig(t, "T-1", "T-2", "T-3")
	r.branch("T-1-a", topChanged)
	r.branch("T-2-b", topChangedAlt)
	r.branch("T-3-c", "THREE\n2\n3\n4\n5\n6\n7\n8\n9\n10\n")

	_, all := scanOverlaps(r.st, r.cfg, r.mp, nil, "")
	if got := pairsOf(all); !reflect.DeepEqual(got, []string{"T-1+T-2", "T-1+T-3", "T-2+T-3"}) {
		t.Fatalf("pairs of the store = %v, want all three", got)
	}
	_, own := scanOverlaps(r.st, r.cfg, r.mp, nil, "T-2")
	if got := pairsOf(own); !reflect.DeepEqual(got, []string{"T-1+T-2", "T-2+T-3"}) {
		t.Errorf("pairs of T-2 = %v, want only those holding T-2", got)
	}
}

// TestAdviceUsesOriginsBranchOnlyWithoutALocalOne: a ticket whose branch exists
// only as origin/<branch> is tested against the other's local branch and named
// by that ref; a ticket with both refs is tested with its local one.
func TestAdviceUsesOriginsBranchOnlyWithoutALocalOne(t *testing.T) {
	r := newConflictRig(t, "T-1", "T-2", "T-3")
	remote := cmdBareRemote(t, "origin.git")
	gitIn(t, r.dir, "remote", "add", "origin", remote)
	r.branch("T-1-local", topChanged)
	r.branch("T-2-pushed", topChangedAlt)
	r.branch("T-3-both", bottomChanged)
	gitIn(t, r.dir, "push", "origin", "T-2-pushed", "T-3-both")
	gitIn(t, r.dir, "branch", "-D", "T-2-pushed")
	// T-3's local branch moves on to conflict with T-1; its pushed one does not.
	gitIn(t, r.dir, "switch", "T-3-both")
	r.write("THREE\n2\n3\n4\n5\n6\n7\n8\n9\nBOTTOM\n")
	gitIn(t, r.dir, "commit", "-am", "local only")
	gitIn(t, r.dir, "switch", "main")

	_, pairs := scanOverlaps(r.st, r.cfg, r.mp, nil, "")
	byPair := map[string]testedPair{}
	for _, p := range pairs {
		byPair[p.A+"+"+p.B] = p
	}
	p := byPair["T-1+T-2"]
	if p.BranchA != "T-1-local" || p.BranchB != "origin/T-2-pushed" || p.State != pairConflict {
		t.Errorf("T-1+T-2 = %+v, want T-1-local against origin/T-2-pushed, conflicting", p)
	}
	p = byPair["T-1+T-3"]
	if p.BranchB != "T-3-both" || p.State != pairConflict {
		t.Errorf("T-1+T-3 = %+v, want T-3's local branch, which conflicts (its pushed one does not)", p)
	}
	rows := findOverlaps(r.st, r.cfg, r.mp, nil)
	if len(rows) != 1 || !reflect.DeepEqual(rows[0].Branches, []string{"T-1-local", "T-3-both", "origin/T-2-pushed"}) {
		t.Errorf("rows = %+v, want T-3 by its local branch alone, T-2 by origin's", rows)
	}
	var out bytes.Buffer
	printConflicts(&out, r.st, r.cfg, r.mp, nil, "T-1")
	if !strings.Contains(out.String(), "app,T-2,f.txt,origin/T-2-pushed") {
		t.Errorf("the conflict table does not name origin/T-2-pushed:\n%s", out.String())
	}
}

// TestStoreOverlapsMarkEachRow: the store-level overlap rows say conflict (with
// the files git reports) or clean.
func TestStoreOverlapsMarkEachRow(t *testing.T) {
	r := newConflictRig(t, "T-1", "T-2", "T-3")
	r.branch("T-1-a", topChanged)
	r.branch("T-2-b", topChangedAlt)
	r.branch("T-3-c", bottomChanged)

	rows := findOverlaps(r.st, r.cfg, r.mp, nil)
	if len(rows) != 1 || rows[0].File != "f.txt" {
		t.Fatalf("rows = %+v, want f.txt only", rows)
	}
	if rows[0].Conflict != pairConflict || !reflect.DeepEqual(rows[0].Files, []string{"f.txt"}) {
		t.Errorf("row = %+v, want a conflict in f.txt (T-3 merges cleanly with both, T-1 and T-2 do not)", rows[0])
	}
	// Without the conflicting one, the sharing pair merges cleanly.
	gitIn(t, r.dir, "branch", "-D", "T-2-b")
	rows = findOverlaps(r.st, r.cfg, r.mp, nil)
	if len(rows) != 1 || rows[0].Conflict != pairClean || len(rows[0].Files) != 0 {
		t.Errorf("rows = %+v, want one clean row", rows)
	}
	text := strings.Join(nextStore{Overlaps: rows, Tickets: []nextRow{{ID: "T-1"}}}.blocks(), "\n")
	if !strings.Contains(text, "overlap[1]{repo,file,tickets,conflict}:\n  app,f.txt,T-1+T-3,clean") {
		t.Errorf("the table lacks the clean row:\n%s", text)
	}
}

// TestNextTicketHelpStacksOnAConflict: jig next <T> carries the stacking line
// for a ticket with conflicts, ahead of its own help.
func TestNextTicketHelpStacksOnAConflict(t *testing.T) {
	n := nextTicket{Ticket: "T-2", State: ready.StateActive, Ready: []nextBatch{},
		Conflicts: []ticketConflict{{Repo: "app", Ticket: "T-1", File: "f.txt", Branch: "T-1-a", First: "T-1"}}}
	text := strings.Join(n.blocks(false), "\n")
	for _, want := range []string{"conflict[1]{repo,ticket,file,branch}:", "app,T-1,f.txt,T-1-a", "Stack it with `jig depend T-2 --on T-1 --kind stacked`, or wait for T-1 to merge"} {
		if !strings.Contains(text, want) {
			t.Errorf("jig next T-2 lacks %q:\n%s", want, text)
		}
	}
}

// TestAdviceLeavesOutPublishedTicketsWhoseMergeIsNotKnown: the reader `jig here`,
// gate and publish use asks no host. A published ticket whose pull request it
// cannot tell the state of (a squash merge leaves nothing in the clone, and the
// cache holds nothing) is left out, as is one with no pull request recorded;
// one the cache knows is open stays; one it knows merged is left out.
func TestAdviceLeavesOutPublishedTicketsWhoseMergeIsNotKnown(t *testing.T) {
	r := newConflictRig(t, "T-1", "T-2", "T-3", "T-4", "T-5")
	const pull = "https://github.com/demo/app/pull/"
	for id, n := range map[string]string{"T-2": "2", "T-3": "3", "T-4": "4"} {
		if err := journal.Append(r.st, id, journal.Line{Event: journal.EventPr, URL: pull + n}); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []string{"T-2", "T-3", "T-4", "T-5"} {
		if err := journal.Append(r.st, id, journal.Line{Event: journal.EventPublishDone}); err != nil {
			t.Fatal(err)
		}
	}
	r.branch("T-1-hand", topChanged)
	r.branch("T-2-unknown", topChangedAlt)
	r.branch("T-3-open", topChangedNew)
	r.branch("T-4-merged", "FOUR\n2\n3\n4\n5\n6\n7\n8\n9\n10\n")
	r.branch("T-5-unrecorded", "FIVE\n2\n3\n4\n5\n6\n7\n8\n9\n10\n")

	known := map[string]string{pull + "3": repohost.StateOpen, pull + "4": repohost.StateMerged}
	reader := &mergefacts.Reader{Store: r.st, Cfg: r.cfg, Clones: r.mp.Clones, PRs: &mergefacts.PRCache{
		Ask: func(url string) (string, bool) { s, ok := known[url]; return s, ok },
	}}
	_, pairs := scanOverlaps(r.st, r.cfg, r.mp, ticketOpenFilter(r.st, reader), "T-1")
	if got, want := pairsOf(pairs), []string{"T-1+T-3"}; !reflect.DeepEqual(got, want) {
		t.Errorf("pairs = %v, want %v: T-2 (state unknown), T-4 (merged) and T-5 (no pull request) are left out", got, want)
	}
	// With no reader a published ticket cannot be told either.
	_, pairs = scanOverlaps(r.st, r.cfg, r.mp, ticketOpenFilter(r.st, nil), "T-1")
	if len(pairs) != 0 {
		t.Errorf("pairs = %v, want none: no published ticket is known to be open", pairsOf(pairs))
	}
}

// TestAdviceMergesNothingForATicketThatSharesNoFile: the early skip. A ticket
// whose branch changes a file no other open ticket changes is told nothing
// and no merge is made for it, however many other pairs share files.
func TestAdviceMergesNothingForATicketThatSharesNoFile(t *testing.T) {
	r := newConflictRig(t, "T-1", "T-2", "T-3")
	r.branch("T-1-a", topChanged)
	r.branch("T-2-b", topChangedAlt)
	gitIn(t, r.dir, "switch", "-c", "T-3-c", "main")
	if err := os.WriteFile(filepath.Join(r.dir, "g.txt"), []byte("own file"+string(rune(10))), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, r.dir, "add", "-A")
	gitIn(t, r.dir, "commit", "-m", "g")
	gitIn(t, r.dir, "switch", "main")

	calls := 0
	real := testPairWith
	testPairWith = func(dir, a, b string) (string, []string) { calls++; return real(dir, a, b) }
	defer func() { testPairWith = real }()

	if _, pairs := scanOverlaps(r.st, r.cfg, r.mp, nil, "T-3"); len(pairs) != 0 || calls != 0 {
		t.Errorf("T-3 shares no file: pairs = %v, merges = %d, want none", pairsOf(pairs), calls)
	}
	if _, pairs := scanOverlaps(r.st, r.cfg, r.mp, nil, "T-1"); len(pairs) != 1 || calls != 1 {
		t.Errorf("T-1 shares f.txt with T-2: pairs = %v, merges = %d, want one of each", pairsOf(pairs), calls)
	}
}

// TestScanWithMoreBranchesThanDiffSlots: nine bound branches against four diff
// slots, and against one: every branch's files arrive, the rows and pairs come
// out in the same order each time, and the scan finishes. A slot that is never
// released would hang the fifth branch (or the second, with one slot), so the
// scan runs under a timeout and fails instead of hanging.
func TestScanWithMoreBranchesThanDiffSlots(t *testing.T) {
	ids := []string{"T-1", "T-2", "T-3", "T-4", "T-5", "T-6", "T-7", "T-8", "T-9"}
	r := newConflictRig(t, ids...)
	for i, id := range ids {
		r.branch(id+"-b", strings.Repeat("x", i+1)+"\n2\n3\n4\n5\n6\n7\n8\n9\n10\n")
	}
	real := diffConcurrency
	defer func() { diffConcurrency = real }()

	var first []string
	for _, slots := range []int{4, 1, 4} {
		diffConcurrency = func() int { return slots }
		type result struct {
			rows  []nextOverlap
			pairs []testedPair
		}
		done := make(chan result, 1)
		go func() {
			rows, pairs := scanOverlaps(r.st, r.cfg, r.mp, nil, "")
			done <- result{rows, pairs}
		}()
		var got result
		select {
		case got = <-done:
		case <-time.After(60 * time.Second):
			t.Fatalf("the scan with %d diff slots and nine branches did not finish: a slot is never released", slots)
		}
		if len(got.rows) != 1 || len(got.rows[0].Tickets) != 9 || len(got.rows[0].Branches) != 9 {
			t.Fatalf("slots %d: rows = %+v, want one row of f.txt holding all nine tickets and branches", slots, got.rows)
		}
		if len(got.pairs) != 36 {
			t.Fatalf("slots %d: %d pairs, want all 36", slots, len(got.pairs))
		}
		order := pairsOf(got.pairs)
		if first == nil {
			first = order
		} else if !reflect.DeepEqual(order, first) {
			t.Errorf("slots %d: pairs came in another order than with four slots:\n%v\n%v", slots, order, first)
		}
	}
}

// TestAdviceJudgesAPublishedTicketByItsLatestPullRequest: a ticket whose first
// pull request (pull/3) still reads open but was replaced by a later one of the
// same repo (pull/4, merged) is not open; one whose later pull request is open
// is, whatever the earlier one reads.
func TestAdviceJudgesAPublishedTicketByItsLatestPullRequest(t *testing.T) {
	r := newConflictRig(t, "T-1", "T-2", "T-3")
	const pull = "https://github.com/demo/app/pull/"
	for _, id := range []string{"T-2", "T-3"} {
		if err := journal.Append(r.st, id, journal.Line{Event: journal.EventPr, URL: pull + "3"}); err != nil {
			t.Fatal(err)
		}
		if err := journal.Append(r.st, id, journal.Line{Event: journal.EventPr, URL: pull + "4"}); err != nil {
			t.Fatal(err)
		}
		if err := journal.Append(r.st, id, journal.Line{Event: journal.EventPublishDone}); err != nil {
			t.Fatal(err)
		}
	}
	r.branch("T-1-a", topChanged)
	r.branch("T-2-b", topChangedAlt)
	r.branch("T-3-c", topChangedNew)
	// T-2's list reads 3 open and 4 merged; T-3's later one is open: the
	// source answers by url, so give T-3 its own pair of urls.
	if err := journal.Append(r.st, "T-3", journal.Line{Event: journal.EventPr, URL: pull + "5"}); err != nil {
		t.Fatal(err)
	}
	states := fakeMerges{pull + "3": ready.PROpen, pull + "4": ready.PRMerged, pull + "5": ready.PROpen}
	_, pairs := scanOverlaps(r.st, r.cfg, r.mp, ticketOpenFilter(r.st, states), "T-1")
	if got, want := pairsOf(pairs), []string{"T-1+T-3"}; !reflect.DeepEqual(got, want) {
		t.Errorf("pairs = %v, want %v: T-2's latest pull request is merged, T-3's is open", got, want)
	}
}

// TestStoreOverlapsMarkAnUntriedPairUntested: two open tickets' branches share
// a file but git cannot try their merge, so the row says untested, never clean.
func TestStoreOverlapsMarkAnUntriedPairUntested(t *testing.T) {
	r := newConflictRig(t, "T-1", "T-2")
	r.branch("T-1-a", topChanged)
	r.branch("T-2-b", topChangedAlt)
	real := testPairWith
	testPairWith = func(dir, a, b string) (string, []string) {
		// The merge fails as a merge-tree call that errors does.
		return real(dir, a, "no-such-ref")
	}
	t.Cleanup(func() { testPairWith = real })

	rows := findOverlaps(r.st, r.cfg, r.mp, nil)
	if len(rows) != 1 || rows[0].File != "f.txt" {
		t.Fatalf("rows = %+v, want f.txt only", rows)
	}
	if rows[0].Conflict != pairUntested || len(rows[0].Files) != 0 {
		t.Errorf("row = %+v, want untested with no files: git cannot try the merge", rows[0])
	}
}

// TestStoreOverlapsLeaveOutTicketsThatAreNotOpen: findOverlaps given an open
// filter leaves out the branches of a ticket the filter rejects, so a file only
// that ticket shares with another has no row.
func TestStoreOverlapsLeaveOutTicketsThatAreNotOpen(t *testing.T) {
	r := newConflictRig(t, "T-1", "T-2", "T-3")
	r.branch("T-1-a", topChanged)
	r.branch("T-2-b", topChangedAlt)
	r.branch("T-3-c", topChangedNew)

	rows := findOverlaps(r.st, r.cfg, r.mp, func(id string) bool { return id != "T-3" })
	if len(rows) != 1 || !reflect.DeepEqual(rows[0].Tickets, []string{"T-1", "T-2"}) {
		t.Fatalf("rows = %+v, want one row for T-1 and T-2 alone", rows)
	}
	rows = findOverlaps(r.st, r.cfg, r.mp, func(id string) bool { return id == "T-1" })
	if len(rows) != 0 {
		t.Errorf("rows = %+v, want none: one open ticket shares with nobody", rows)
	}
}

// TestAdviceTestsEachBranchOfATicketThatHasSeveral: a ticket whose first branch
// merges cleanly but whose second conflicts is told it conflicts, naming the
// second branch.
func TestAdviceTestsEachBranchOfATicketThatHasSeveral(t *testing.T) {
	r := newConflictRig(t, "T-1", "T-2")
	r.branch("T-1-a", bottomChanged)
	r.branch("T-1-b", topChanged)
	r.branch("T-2-x", topChangedAlt)

	rows := findOverlaps(r.st, r.cfg, r.mp, nil)
	if len(rows) != 1 || rows[0].File != "f.txt" || rows[0].Conflict != pairConflict {
		t.Fatalf("rows = %+v, want one conflicting row naming f.txt", rows)
	}
	_, pairs := scanOverlaps(r.st, r.cfg, r.mp, nil, "")
	found := false
	for _, p := range pairs {
		if p.State == pairConflict && p.BranchA == "T-1-b" {
			found = true
		}
	}
	if !found {
		t.Errorf("pairs = %+v, want a conflicting pair on T-1-b", pairs)
	}
	var out bytes.Buffer
	printConflicts(&out, r.st, r.cfg, r.mp, nil, "T-2")
	if !strings.Contains(out.String(), "app,T-1,f.txt,T-1-b") {
		t.Errorf("the advice for T-2 does not list T-1-b:\n%s", out.String())
	}
}

// TestAdviceMergesNoBranchThatLacksTheSharedFile: a branch that changes only
// another file is not merged against the other ticket.
func TestAdviceMergesNoBranchThatLacksTheSharedFile(t *testing.T) {
	r := newConflictRig(t, "T-1", "T-2")
	r.branch("T-1-a", topChanged)
	gitIn(t, r.dir, "switch", "-c", "T-1-other", "main")
	if err := os.WriteFile(filepath.Join(r.dir, "g.txt"), []byte("g\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, r.dir, "add", "-A")
	gitIn(t, r.dir, "commit", "-m", "other file")
	gitIn(t, r.dir, "switch", "main")
	r.branch("T-2-x", topChangedAlt)

	_, pairs := scanOverlaps(r.st, r.cfg, r.mp, nil, "")
	if len(pairs) != 1 {
		t.Errorf("pairs = %+v, want one pair", pairs)
	}
	for _, p := range pairs {
		if p.BranchA == "T-1-other" || p.BranchB == "T-1-other" {
			t.Errorf("pair %+v merges a branch that lacks the shared file", p)
		}
	}
}
