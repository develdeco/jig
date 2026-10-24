package e2e

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/store"
)

// nextTicketOut is the part of `jig next <ticket> --json` these tests read.
type nextTicketOut struct {
	Ticket    string `json:"ticket"`
	State     string `json:"state"`
	Branch    string `json:"branch"`
	BlockedBy []struct {
		Ticket string `json:"ticket"`
		Kind   string `json:"kind"`
	} `json:"blocked_by"`
	Ready []struct {
		Repo   string   `json:"repo"`
		Slices []string `json:"slices"`
	} `json:"ready"`
	Topology []struct {
		ID        string   `json:"id"`
		Workspace string   `json:"workspace"`
		Repo      string   `json:"repo"`
		State     string   `json:"state"`
		Wave      int      `json:"wave"`
		BlockedBy []string `json:"blocked_by"`
	} `json:"topology"`
}

// nextStoreOut is the part of `jig next --json` these tests read.
type nextStoreOut struct {
	Tickets []struct {
		ID        string `json:"id"`
		Title     string `json:"title"`
		State     string `json:"state"`
		BlockedBy []struct {
			Ticket string `json:"ticket"`
			Kind   string `json:"kind"`
		} `json:"blocked_by"`
		Ready []string `json:"ready"`
	} `json:"tickets"`
	Next     []string `json:"next"`
	Overlaps []struct {
		Repo    string   `json:"repo"`
		File    string   `json:"file"`
		Tickets []string `json:"tickets"`
	} `json:"overlaps"`
}

func nextJSON[T any](t *testing.T, env jigEnv, cwd string, args ...string) T {
	t.Helper()
	r := runJig(t, env, cwd, append([]string{"next", "--json"}, args...)...)
	if r.Code != 0 || r.Stderr != "" {
		t.Fatalf("jig next --json %v: exit %d\nstdout:\n%s\nstderr:\n%s", args, r.Code, r.Stdout, r.Stderr)
	}
	var out T
	if err := json.Unmarshal([]byte(r.Stdout), &out); err != nil {
		t.Fatalf("jig next --json %v: not one JSON object: %v\n%s", args, err, r.Stdout)
	}
	return out
}

// readyRepos flattens a ticket's ready sets to "repo: a+b" strings.
func readyRepos(n nextTicketOut) []string {
	var out []string
	for _, b := range n.Ready {
		out = append(out, b.Repo+": "+strings.Join(b.Slices, "+"))
	}
	return out
}

func setStates(t *testing.T, st *store.Store, ticket string, states map[string]string) {
	t.Helper()
	for slice, state := range states {
		if err := st.WriteSliceState(ticket, slice, store.SliceState{State: state}); err != nil {
			t.Fatalf("WriteSliceState(%s, %s): %v", slice, state, err)
		}
	}
}

// TestNextReadySetsAndTickets: a two-repo ticket's ready slices come out per
// repo as slices build and finish, with the branch to create; and across the
// store the tickets come in order, a blocked one named with its blocker until
// the blocker publishes.
func TestNextReadySetsAndTickets(t *testing.T) {
	t.Parallel()
	fx, env := newFixture(t, fixture.Opts{SecondRepo: true})
	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}

	// Nothing started: every slice with no blocker is ready, in the repo its
	// workspace belongs to; b waits for a.
	n := nextJSON[nextTicketOut](t, env, fx.RepoDir, "JIG-1")
	if n.Ticket != "JIG-1" || n.State != "ready" || n.Branch != "jig/JIG-1" {
		t.Errorf("ticket = %q, state = %q, branch = %q, want JIG-1, ready, jig/JIG-1", n.Ticket, n.State, n.Branch)
	}
	if want := []string{"fixture-repo: a+c+d", "second-repo: e"}; !reflect.DeepEqual(readyRepos(n), want) {
		t.Errorf("ready = %v, want %v", readyRepos(n), want)
	}
	r := runJig(t, env, fx.RepoDir, "next", "JIG-1")
	for _, want := range []string{
		"next:\n  ticket: JIG-1\n  state: ready\n  branch: jig/JIG-1\n",
		"ready[2]{repo,slices}:\n  fixture-repo,a+c+d\n  second-repo,e\n",
		"2 repos have ready slices (fixture-repo, second-repo)",
		"git switch -c jig/JIG-1",
	} {
		if !strings.Contains(r.Stdout, want) {
			t.Errorf("jig next JIG-1 lacks %q:\n%s", want, r.Stdout)
		}
	}

	// The same from the store, and from another repo's clone.
	if got := nextJSON[nextTicketOut](t, env, fx.StoreDir, "JIG-1"); !reflect.DeepEqual(readyRepos(got), readyRepos(n)) {
		t.Errorf("from the store: ready = %v, want %v", readyRepos(got), readyRepos(n))
	}

	// The topology names every slice's repo and how deep it waits.
	topo := nextJSON[nextTicketOut](t, env, fx.RepoDir, "JIG-1", "--topology")
	waves := map[string]int{}
	repos := map[string]string{}
	for _, s := range topo.Topology {
		waves[s.ID], repos[s.ID] = s.Wave, s.Repo
	}
	if len(topo.Topology) != 5 || waves["a"] != 0 || waves["b"] != 1 || repos["e"] != "second-repo" || repos["c"] != "fixture-repo" {
		t.Errorf("topology = %+v, want five slices, b one wave after a, e in second-repo", topo.Topology)
	}
	if r := runJig(t, env, fx.RepoDir, "next", "JIG-1", "--topology"); !strings.Contains(r.Stdout, "topology[5]{slice,workspace,repo,state,wave,blocked_by}:\n  a,alpha,fixture-repo,queued,0,-\n  b,alpha,fixture-repo,queued,1,a\n") {
		t.Errorf("jig next JIG-1 --topology lacks the topology table:\n%s", r.Stdout)
	}

	// a is green and c is being built: b opens up, c is no longer on offer.
	setStates(t, st, "JIG-1", map[string]string{"a": "green", "c": "building"})
	n = nextJSON[nextTicketOut](t, env, fx.RepoDir, "JIG-1")
	if want := []string{"fixture-repo: b+d", "second-repo: e"}; !reflect.DeepEqual(readyRepos(n), want) {
		t.Errorf("after a green and c building: ready = %v, want %v", readyRepos(n), want)
	}

	// Everything in flight: nothing ready, work active; everything green: it
	// waits for its gate.
	setStates(t, st, "JIG-1", map[string]string{"b": "building", "d": "needs-input", "e": "building"})
	n = nextJSON[nextTicketOut](t, env, fx.RepoDir, "JIG-1")
	if n.State != "active" || len(n.Ready) != 0 {
		t.Errorf("all in flight: state = %q, ready = %v, want active and nothing ready", n.State, readyRepos(n))
	}
	if r := runJig(t, env, fx.RepoDir, "next", "JIG-1"); !strings.Contains(r.Stdout, "ready: none\n") {
		t.Errorf("jig next JIG-1 with nothing ready lacks \"ready: none\":\n%s", r.Stdout)
	}
	setStates(t, st, "JIG-1", map[string]string{"a": "green", "b": "green", "c": "green", "d": "green", "e": "green"})
	if n = nextJSON[nextTicketOut](t, env, fx.RepoDir, "JIG-1"); n.State != "green" {
		t.Errorf("all green: state = %q, want green", n.State)
	}
	setStates(t, st, "JIG-1", map[string]string{"a": "queued", "b": "queued", "c": "queued", "d": "queued", "e": "queued"})

	// Tickets across the store: JIG-2 waits for JIG-1 to merge, JIG-3 has no
	// slices yet.
	if err := st.CreateTicketRecord("JIG-2", store.Ticket{Title: "Second", BlockedBy: []store.TicketBlockedBy{{Ticket: "JIG-1", Kind: "merged"}}}); err != nil {
		t.Fatalf("CreateTicketRecord JIG-2: %v", err)
	}
	if err := st.AppendSlices("JIG-2", []store.Slice{{ID: "x", Workspace: "alpha", Goal: "Do x.", Oracle: "test", BlockedBy: []string{}}}); err != nil {
		t.Fatalf("AppendSlices JIG-2: %v", err)
	}
	if err := st.CreateTicketRecord("JIG-3", store.Ticket{Title: "Third"}); err != nil {
		t.Fatalf("CreateTicketRecord JIG-3: %v", err)
	}
	all := nextJSON[nextStoreOut](t, env, fx.RepoDir)
	if len(all.Tickets) != 3 || all.Tickets[0].ID != "JIG-1" || all.Tickets[1].ID != "JIG-2" || all.Tickets[2].ID != "JIG-3" {
		t.Fatalf("tickets = %+v, want JIG-1, JIG-2, JIG-3 in order", all.Tickets)
	}
	states := []string{all.Tickets[0].State, all.Tickets[1].State, all.Tickets[2].State}
	if want := []string{"ready", "blocked", "new"}; !reflect.DeepEqual(states, want) {
		t.Errorf("ticket states = %v, want %v", states, want)
	}
	if b := all.Tickets[1].BlockedBy; len(b) != 1 || b[0].Ticket != "JIG-1" || b[0].Kind != "merged" {
		t.Errorf("JIG-2 blocked_by = %+v, want JIG-1 (merged)", b)
	}
	if !reflect.DeepEqual(all.Tickets[0].Ready, []string{"a", "c", "d", "e"}) || !reflect.DeepEqual(all.Next, []string{"JIG-1"}) {
		t.Errorf("JIG-1 ready = %v, next = %v, want a c d e and JIG-1", all.Tickets[0].Ready, all.Next)
	}
	r = runJig(t, env, fx.RepoDir, "next")
	for _, want := range []string{
		"tickets[3]{id,title,state,blocked_by,ready}:\n",
		"  JIG-1,,ready,-,a+c+d+e\n",
		"  JIG-2,Second,blocked,JIG-1(merged),-\n",
		"  JIG-3,Third,new,-,-\n",
		"Run `jig next JIG-1`",
	} {
		if !strings.Contains(r.Stdout, want) {
			t.Errorf("jig next lacks %q:\n%s", want, r.Stdout)
		}
	}
	blocked := nextJSON[nextTicketOut](t, env, fx.RepoDir, "JIG-2")
	if blocked.State != "blocked" || len(blocked.Ready) != 0 || len(blocked.BlockedBy) != 1 {
		t.Errorf("jig next JIG-2: state = %q, ready = %v, blocked_by = %+v, want blocked with nothing ready", blocked.State, readyRepos(blocked), blocked.BlockedBy)
	}

	// JIG-1 publishes: it is done, and JIG-2's blocker is met.
	if err := journal.Append(st, "JIG-1", journal.Line{Event: journal.EventPublishDone}); err != nil {
		t.Fatalf("journal.Append publish-done: %v", err)
	}
	all = nextJSON[nextStoreOut](t, env, fx.RepoDir)
	states = []string{all.Tickets[0].State, all.Tickets[1].State, all.Tickets[2].State}
	if want := []string{"done", "ready", "new"}; !reflect.DeepEqual(states, want) {
		t.Errorf("after JIG-1 published: ticket states = %v, want %v", states, want)
	}
	if !reflect.DeepEqual(all.Next, []string{"JIG-2"}) {
		t.Errorf("after JIG-1 published: next = %v, want JIG-2", all.Next)
	}
	unblocked := nextJSON[nextTicketOut](t, env, fx.RepoDir, "JIG-2")
	// JIG-2 has a title ("second"), so its branch is its id and the slug of it.
	if want := []string{"fixture-repo: x"}; unblocked.State != "ready" || !reflect.DeepEqual(readyRepos(unblocked), want) || unblocked.Branch != "JIG-2-second" {
		t.Errorf("jig next JIG-2: state = %q, ready = %v, branch = %q, want ready, %v, JIG-2-second", unblocked.State, readyRepos(unblocked), unblocked.Branch, want)
	}

	if r := runJig(t, env, fx.RepoDir, "next", "JIG-99"); r.Code == 0 || !strings.Contains(r.Stdout, "JIG-99") {
		t.Errorf("jig next JIG-99: exit %d, want a refusal naming the ticket:\n%s", r.Code, r.Stdout)
	}
}

// TestNextWarnsOfOverlappingBranches: local branches of two tickets that change
// the same file since main are named; a branch that binds no ticket, and a file
// only one ticket changes, are not.
func TestNextWarnsOfOverlappingBranches(t *testing.T) {
	t.Parallel()
	fx, env := newFixture(t, fixture.Opts{})
	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	if err := st.CreateTicketRecord("JIG-2", store.Ticket{Title: "Second"}); err != nil {
		t.Fatalf("CreateTicketRecord JIG-2: %v", err)
	}

	branchChanging := func(branch string, files map[string]string) {
		t.Helper()
		gitLog(t, fx.RepoDir, "switch", "-c", branch, "main")
		for rel, content := range files {
			if err := os.WriteFile(filepath.Join(fx.RepoDir, filepath.FromSlash(rel)), []byte(content), 0o644); err != nil {
				t.Fatalf("write %s: %v", rel, err)
			}
		}
		gitLog(t, fx.RepoDir, "add", "-A")
		commitPinned(t, fx.RepoDir, "change on "+branch)
		gitLog(t, fx.RepoDir, "switch", "main")
	}
	branchChanging("jig/JIG-1", map[string]string{"alpha/alpha.go": "package alpha\n\n// one\n", "beta/one.go": "package beta\n"})
	branchChanging("jig/JIG-2", map[string]string{"alpha/alpha.go": "package alpha\n\n// two\n", "beta/two.go": "package beta\n"})
	branchChanging("scratch", map[string]string{"alpha/alpha.go": "package alpha\n\n// scratch\n"})

	out := nextJSON[nextStoreOut](t, env, fx.RepoDir)
	if len(out.Overlaps) != 1 || out.Overlaps[0].Repo != "fixture-repo" || out.Overlaps[0].File != "alpha/alpha.go" || !reflect.DeepEqual(out.Overlaps[0].Tickets, []string{"JIG-1", "JIG-2"}) {
		t.Errorf("overlaps = %+v, want only alpha/alpha.go of fixture-repo, for JIG-1 and JIG-2", out.Overlaps)
	}
	r := runJig(t, env, fx.RepoDir, "next")
	for _, want := range []string{
		"warning: local branches of different tickets change the same files\n",
		"overlap[1]{repo,file,tickets}:\n  fixture-repo,alpha/alpha.go,JIG-1+JIG-2\n",
	} {
		if !strings.Contains(r.Stdout, want) {
			t.Errorf("jig next lacks %q:\n%s", want, r.Stdout)
		}
	}
}

// commitPinned commits what is staged in dir under the suite's pinned identity.
func commitPinned(t *testing.T, dir, msg string) {
	t.Helper()
	if _, err := gitx.RunEnv(dir, pinnedIdentityEnv, "commit", "-m", msg); err != nil {
		t.Fatalf("git commit in %s: %v", dir, err)
	}
}

// TestNextListsATicketItCannotRead: a ticket whose slices.yaml does not parse
// is listed as invalid, with the file and the problem, and the store's other
// tickets are listed as ever; recall reads past it too.
func TestNextListsATicketItCannotRead(t *testing.T) {
	t.Parallel()
	fx, env := newFixture(t, fixture.Opts{})
	mint := runJig(t, env, fx.StoreDir, "ticket", "new", "--title", "Broken slices")
	if mint.Code != 0 {
		t.Fatalf("jig ticket new exit = %d\n%s", mint.Code, mint.Stdout)
	}
	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatal(err)
	}
	broken := filepath.Join(st.TicketDir("JIG-2"), "slices.yaml")
	writeFileOrFatal(t, broken, []byte("slices:\n  - id: a\n    workspace: alpha\n    env: {}\n"))

	n := nextJSON[struct {
		Tickets []struct {
			ID, State, Problem string
		} `json:"tickets"`
	}](t, env, fx.StoreDir)
	states := map[string]string{}
	problems := map[string]string{}
	for _, tk := range n.Tickets {
		states[tk.ID], problems[tk.ID] = tk.State, tk.Problem
	}
	if states["JIG-1"] != "ready" || states["JIG-2"] != "invalid" || !strings.Contains(problems["JIG-2"], "slices.yaml") {
		t.Fatalf("tickets = %+v, want JIG-1 ready and JIG-2 invalid naming its slices.yaml", n.Tickets)
	}
	r := runJig(t, env, fx.StoreDir, "next")
	if r.Code != 0 || !strings.Contains(r.Stdout, "invalid[1]{id,problem}:") || !strings.Contains(r.Stdout, "jig validate JIG-2") {
		t.Errorf("jig next = exit %d\n%s\nwant the invalid ticket's problem and a way out", r.Code, r.Stdout)
	}
	if r := runJig(t, env, fx.StoreDir, "recall", "clamp"); r.Code != 0 {
		t.Errorf("jig recall with an invalid ticket = exit %d\n%s", r.Code, r.Stdout)
	}
}
