package main

import (
	"bytes"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/project"
	"github.com/develdeco/jig/internal/ready"
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

// fakeMerges says the tickets in it have merged.
type fakeMerges map[string]bool

func (f fakeMerges) Of(id string) (ready.Merge, error) {
	return ready.Merge{Published: true, Repos: []ready.RepoMerge{{Met: f[id]}}}, nil
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
	if err := journal.Append(r.st, "T-3", journal.Line{Event: journal.EventPublishDone}); err != nil {
		t.Fatal(err)
	}
	r.branch("T-1-closed", topChanged)
	r.branch("T-2-open", topChangedAlt)
	r.branch("T-3-merged", topChangedNew)
	r.branch("T-4-open", "FOUR\n2\n3\n4\n5\n6\n7\n8\n9\n10\n")
	r.branch("T-9-nobody", "NINE\n2\n3\n4\n5\n6\n7\n8\n9\n10\n")

	_, pairs := scanOverlaps(r.st, r.cfg, r.mp, ticketOpenFilter(r.st, fakeMerges{"T-3": true}), "")
	if got, want := pairsOf(pairs), []string{"T-2+T-4"}; !reflect.DeepEqual(got, want) {
		t.Errorf("pairs = %v, want %v: only open tickets are paired", got, want)
	}
	var out bytes.Buffer
	printConflicts(&out, r.st, r.cfg, r.mp, fakeMerges{"T-3": true}, "T-4")
	for _, leftover := range []string{"T-1", "T-3", "T-9"} {
		if strings.Contains(out.String(), leftover) {
			t.Errorf("the advice names %s, which is not an open ticket:\n%s", leftover, out.String())
		}
	}
	if !strings.Contains(out.String(), "app,T-2,f.txt,T-2-open") {
		t.Errorf("the advice lacks the open ticket's row:\n%s", out.String())
	}
	// A published ticket whose work has not merged is still open.
	_, pairs = scanOverlaps(r.st, r.cfg, r.mp, ticketOpenFilter(r.st, fakeMerges{"T-3": false}), "T-4")
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
