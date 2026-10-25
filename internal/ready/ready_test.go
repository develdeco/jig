package ready

import (
	"reflect"
	"testing"

	"github.com/develdeco/jig/internal/store"
)

func states(pairs ...string) map[string]store.SliceState {
	m := map[string]store.SliceState{}
	for i := 0; i+1 < len(pairs); i += 2 {
		m[pairs[i]] = store.SliceState{State: pairs[i+1]}
	}
	return m
}

func ids(slices []store.Slice) []string {
	var out []string
	for _, s := range slices {
		out = append(out, s.ID)
	}
	return out
}

// TestComputeReady: a slice is ready when it is queued and every blocker is
// green; building, parked, stalled and env-blocked slices are not, nor is one
// whose blocker is anything but green, nor one with no state at all.
func TestComputeReady(t *testing.T) {
	t.Parallel()
	slices := []store.Slice{
		{ID: "a"},
		{ID: "b", BlockedBy: []string{"a"}},
		{ID: "c"},
		{ID: "d", BlockedBy: []string{"a", "c"}},
		{ID: "e"},
		{ID: "f"},
		{ID: "g"},
		{ID: "h", BlockedBy: []string{"nowhere"}},
		{ID: "i"},
	}
	st := states("a", "green", "b", "queued", "c", "building", "d", "queued", "e", "needs-input", "f", "stalled", "g", "env-blocked", "h", "queued")
	if got, want := ids(ComputeReady(slices, st)), []string{"b"}; !reflect.DeepEqual(got, want) {
		t.Errorf("ComputeReady = %v, want %v", got, want)
	}

	// Nothing started: every slice with no blocker, in slices.yaml order.
	queued := states("a", "queued", "b", "queued", "c", "queued", "d", "queued")
	two := []store.Slice{{ID: "a"}, {ID: "b", BlockedBy: []string{"a"}}, {ID: "c"}, {ID: "d", BlockedBy: []string{"c"}}}
	if got, want := ids(ComputeReady(two, queued)), []string{"a", "c"}; !reflect.DeepEqual(got, want) {
		t.Errorf("ComputeReady at the start = %v, want %v", got, want)
	}
}

// TestScheduleGroupsByRepoInOrder: slices of one repo run serially in
// slices.yaml order, repos come in the order they first appear.
func TestScheduleGroupsByRepoInOrder(t *testing.T) {
	t.Parallel()
	slices := []store.Slice{
		{ID: "a", Workspace: "alpha"},
		{ID: "e", Workspace: "gamma"},
		{ID: "b", Workspace: "beta"},
		{ID: "f", Workspace: "gamma"},
	}
	wsRepo := map[string]string{"alpha": "one", "beta": "one", "gamma": "two"}
	if got, want := Schedule(slices, wsRepo), [][]string{{"a", "b"}, {"e", "f"}}; !reflect.DeepEqual(got, want) {
		t.Errorf("Schedule = %v, want %v", got, want)
	}
	if got, want := Batches(slices, wsRepo), []Batch{{"one", []string{"a", "b"}}, {"two", []string{"e", "f"}}}; !reflect.DeepEqual(got, want) {
		t.Errorf("Batches = %v, want %v", got, want)
	}
	if got := Schedule(nil, wsRepo); len(got) != 0 {
		t.Errorf("Schedule of nothing = %v, want no batches", got)
	}
}

// TestPlace: a single-repo project owns every workspace, written bare; with
// several repos a slice names its repo as "<repo>/<id>", a bare id belongs to
// the one repo that declares it, and the ones nobody declares, or several do,
// are reported and owned by no repo.
func TestPlace(t *testing.T) {
	t.Parallel()
	slices := []store.Slice{{ID: "a", Workspace: "alpha"}, {ID: "b", Workspace: "beta"}, {ID: "e", Workspace: "gamma"}, {ID: "x", Workspace: "stray"}, {ID: "y", Workspace: "stray"}}

	one := Place([]Repo{{Name: "solo"}}, slices)
	for ws, repo := range one.Repo {
		if repo != "solo" || one.ID[ws] != ws {
			t.Errorf("single repo: workspace %s -> %s as %s, want solo as itself", ws, repo, one.ID[ws])
		}
	}
	if len(one.Unplaced) != 0 || len(one.Ambiguous) != 0 {
		t.Errorf("single repo: unplaced %v, ambiguous %v, want none", one.Unplaced, one.Ambiguous)
	}
	if got := Place([]Repo{{Name: "solo"}}, []store.Slice{{ID: "q", Workspace: "solo/alpha"}}); got.Repo["solo/alpha"] != "solo" || got.ID["solo/alpha"] != "solo/alpha" {
		t.Errorf("single repo: a workspace that begins with the repo's name is still the bare id: %+v", got)
	}

	got := Place([]Repo{
		{Name: "first", Workspaces: []string{"alpha", "beta"}},
		{Name: "second", Workspaces: []string{"gamma", "beta"}},
	}, slices)
	want := map[string]string{"alpha": "first", "beta": Unplaced, "gamma": "second", "stray": Unplaced}
	if !reflect.DeepEqual(got.Repo, want) {
		t.Errorf("Place.Repo = %v, want %v", got.Repo, want)
	}
	if !reflect.DeepEqual(got.Unplaced, []string{"stray"}) || !reflect.DeepEqual(got.Ambiguous, []string{"beta"}) {
		t.Errorf("Place: unplaced %v, ambiguous %v, want [stray] and [beta]", got.Unplaced, got.Ambiguous)
	}
	if want := []string{"first", "second"}; !reflect.DeepEqual(got.Declared["beta"], want) {
		t.Errorf("Place: beta is declared by %v, want %v", got.Declared["beta"], want)
	}

	// A slice that names its repo is never ambiguous, whatever the manifests
	// say, and its id is the one after the repo's name.
	named := Place([]Repo{
		{Name: "first", Workspaces: []string{"alpha", "beta"}},
		{Name: "second", Workspaces: []string{"gamma", "beta"}},
	}, []store.Slice{{ID: "a", Workspace: "second/beta"}, {ID: "b", Workspace: "first/beta"}, {ID: "c", Workspace: "third/beta"}, {ID: "d", Workspace: "first/"}})
	if wantRepo := map[string]string{"second/beta": "second", "first/beta": "first", "third/beta": Unplaced, "first/": Unplaced}; !reflect.DeepEqual(named.Repo, wantRepo) {
		t.Errorf("Place.Repo with named repos = %v, want %v", named.Repo, wantRepo)
	}
	if named.ID["second/beta"] != "beta" || named.ID["first/beta"] != "beta" || len(named.Ambiguous) != 0 {
		t.Errorf("Place.ID = %v, ambiguous %v, want beta for both named, none ambiguous", named.ID, named.Ambiguous)
	}

	none := Place(nil, slices)
	if none.Repo["alpha"] != Unplaced || len(none.Unplaced) != 4 {
		t.Errorf("no repos: %+v, want every workspace unplaced", none)
	}
}

// TestBuiltIn: the repos a ticket's slices build in come in the project's
// order; a project of one repo is that repo, and a ticket with no slice placed
// in a repo is taken to build in all of them.
func TestBuiltIn(t *testing.T) {
	t.Parallel()
	repos := []Repo{{Name: "api", Workspaces: []string{"server"}}, {Name: "web", Workspaces: []string{"ui"}}, {Name: "docs", Workspaces: []string{"site"}}}
	for _, c := range []struct {
		name   string
		repos  []Repo
		slices []store.Slice
		want   []string
	}{
		{"several, in the project's order", repos, []store.Slice{{ID: "a", Workspace: "site"}, {ID: "b", Workspace: "server"}}, []string{"api", "docs"}},
		{"named by its repo", repos, []store.Slice{{ID: "a", Workspace: "web/ui"}}, []string{"web"}},
		{"none placed", repos, []store.Slice{{ID: "a", Workspace: "stray"}}, []string{"api", "web", "docs"}},
		{"no slices", repos, nil, []string{"api", "web", "docs"}},
		{"one repo", []Repo{{Name: "solo"}}, []store.Slice{{ID: "a", Workspace: "anything"}}, []string{"solo"}},
		{"no repo", nil, []store.Slice{{ID: "a", Workspace: "alpha"}}, []string{}},
	} {
		if got := BuiltIn(c.repos, c.slices); !reflect.DeepEqual(got, c.want) {
			t.Errorf("%s: BuiltIn = %v, want %v", c.name, got, c.want)
		}
	}
}

// TestSplitWorkspace: only a repo's own name and a slash make a workspace
// qualified.
func TestSplitWorkspace(t *testing.T) {
	t.Parallel()
	repos := []string{"api", "web"}
	for _, c := range []struct {
		written, repo, id string
		ok                bool
	}{
		{"api/server", "api", "server", true},
		{"web/ui/admin", "web", "ui/admin", true},
		{"server", "", "server", false},
		{"apix/server", "", "apix/server", false},
		{"api/", "", "api/", false},
		{"api", "", "api", false},
	} {
		repo, id, ok := SplitWorkspace(c.written, repos)
		if repo != c.repo || id != c.id || ok != c.ok {
			t.Errorf("SplitWorkspace(%q) = %q, %q, %v, want %q, %q, %v", c.written, repo, id, ok, c.repo, c.id, c.ok)
		}
	}
}

func TestWaves(t *testing.T) {
	t.Parallel()
	slices := []store.Slice{
		{ID: "a"},
		{ID: "b", BlockedBy: []string{"a"}},
		{ID: "c", BlockedBy: []string{"a", "b"}},
		{ID: "d", BlockedBy: []string{"outside"}},
		{ID: "x", BlockedBy: []string{"y"}},
		{ID: "y", BlockedBy: []string{"x"}},
		{ID: "z", BlockedBy: []string{"x"}},
	}
	want := map[string]int{"a": 0, "b": 1, "c": 2, "d": 0, "x": -1, "y": -1, "z": -1}
	if got := Waves(slices); !reflect.DeepEqual(got, want) {
		t.Errorf("Waves = %v, want %v", got, want)
	}
}

// TestTickets: done beats blocked beats the slice states; a blocker is met only
// by a ticket of the list that published; a blocked or done ticket offers no
// slices.
func TestTickets(t *testing.T) {
	t.Parallel()
	open := []store.Slice{{ID: "a"}, {ID: "b", BlockedBy: []string{"a"}}}
	status := func(ts ...Ticket) map[string]Status {
		out := map[string]Status{}
		for _, s := range Tickets(ts) {
			out[s.ID] = s
		}
		return out
	}
	got := status(
		Ticket{ID: "T-1", Slices: open, States: states("a", "queued", "b", "queued")},
		Ticket{ID: "T-2", Blockers: []Blocker{{"T-1", "merged"}}, Slices: open, States: states("a", "queued", "b", "queued")},
		Ticket{ID: "T-3"},
		Ticket{ID: "T-4", Published: true, Slices: open, States: states("a", "green", "b", "green")},
		Ticket{ID: "T-5", Blockers: []Blocker{{"T-4", "stacked"}}, Slices: open, States: states("a", "queued", "b", "queued")},
		Ticket{ID: "T-6", Slices: open, States: states("a", "green", "b", "green")},
		Ticket{ID: "T-7", Slices: open, States: states("a", "building", "b", "queued")},
		Ticket{ID: "T-8", Blockers: []Blocker{{"T-404", "merged"}}},
		Ticket{ID: "T-9", Blockers: []Blocker{{"T-4", "merged"}, {"T-1", "stacked"}}, Slices: open, States: states("a", "queued", "b", "queued")},
	)
	wantState := map[string]string{
		"T-1": StateReady, "T-2": StateBlocked, "T-3": StateNew, "T-4": StateDone, "T-5": StateReady,
		"T-6": StateGreen, "T-7": StateActive, "T-8": StateBlocked, "T-9": StateBlocked,
	}
	for id, want := range wantState {
		if got[id].State != want {
			t.Errorf("%s: state = %q, want %q", id, got[id].State, want)
		}
	}
	if !reflect.DeepEqual(got["T-1"].Ready, []string{"a"}) || !reflect.DeepEqual(got["T-5"].Ready, []string{"a"}) {
		t.Errorf("ready slices: T-1 %v, T-5 %v, want [a] each", got["T-1"].Ready, got["T-5"].Ready)
	}
	for _, id := range []string{"T-2", "T-4", "T-8", "T-9"} {
		if len(got[id].Ready) != 0 {
			t.Errorf("%s (%s) offers slices %v, want none", id, got[id].State, got[id].Ready)
		}
	}
	if b := got["T-2"].Blocked; !reflect.DeepEqual(b, []Blocker{{"T-1", "merged"}}) {
		t.Errorf("T-2 blocked by %v, want T-1 merged", b)
	}
	if b := got["T-9"].Blocked; !reflect.DeepEqual(b, []Blocker{{"T-1", "stacked"}}) {
		t.Errorf("T-9 blocked by %v, want only T-1 (T-4 published)", b)
	}
	if b := got["T-8"].Blocked; len(b) != 1 || b[0].Ticket != "T-404" {
		t.Errorf("T-8 blocked by %v, want the missing T-404 to count as unmet", b)
	}
}

func TestJoinBlockers(t *testing.T) {
	t.Parallel()
	if got := JoinBlockers(nil); got != "-" {
		t.Errorf("JoinBlockers(nil) = %q, want -", got)
	}
	if got, want := JoinBlockers([]Blocker{{"T-1", "merged"}, {"T-2", "stacked"}, {"T-3", ""}}), "T-1(merged)+T-2(stacked)+T-3"; got != want {
		t.Errorf("JoinBlockers = %q, want %q", got, want)
	}
}

// TestTicketsClosed: a closed ticket is closed whatever else it is, and offers
// no slices; a covered or superseded blocker releases the tickets that wait for
// it, of either kind and whatever merge was read for it, and a blocker closed as
// not planned holds them, saying they need planning again.
func TestTicketsClosed(t *testing.T) {
	t.Parallel()
	open := []store.Slice{{ID: "a"}}
	covered := &store.Closed{Reason: store.CloseCovered, By: "o/r#82"}
	superseded := &store.Closed{Reason: store.CloseSuperseded, By: "T-9"}
	notPlanned := &store.Closed{Reason: store.CloseNotPlanned}
	queued := states("a", "queued")
	out := map[string]Status{}
	for _, s := range Tickets([]Ticket{
		{ID: "C-1", Closed: covered, Published: true, Merge: &Merge{Published: true, Repos: []RepoMerge{{Why: "its PR is open"}}}, Slices: open, States: queued},
		{ID: "C-2", Closed: superseded, Slices: open, States: queued},
		{ID: "C-3", Closed: notPlanned, Slices: open, States: queued},
		{ID: "C-4", Closed: covered, Published: true, Slices: open, States: states("a", "green")},
		{ID: "C-5", Closed: covered, Blockers: []Blocker{{"C-3", "merged"}}, Slices: open, States: queued},
		{ID: "D-1", Blockers: []Blocker{{"C-1", "merged"}}, Slices: open, States: queued},
		{ID: "D-2", Blockers: []Blocker{{"C-2", "stacked"}}, Slices: open, States: queued},
		{ID: "D-3", Blockers: []Blocker{{"C-3", "merged"}}, Slices: open, States: queued},
		{ID: "D-4", Blockers: []Blocker{{"C-3", "stacked"}}, Slices: open, States: queued},
	}) {
		out[s.ID] = s
	}
	for id, want := range map[string]string{
		"C-1": StateClosed, "C-2": StateClosed, "C-3": StateClosed, "C-4": StateClosed, "C-5": StateClosed,
		"D-1": StateReady, "D-2": StateReady, "D-3": StateBlocked, "D-4": StateBlocked,
	} {
		if out[id].State != want {
			t.Errorf("%s: state = %q, want %q", id, out[id].State, want)
		}
	}
	for _, id := range []string{"C-1", "C-2", "C-3", "C-4", "C-5"} {
		if len(out[id].Ready) != 0 {
			t.Errorf("%s is closed and offers slices %v, want none", id, out[id].Ready)
		}
	}
	if len(out["C-5"].Blocked) != 0 {
		t.Errorf("C-5 is closed and still lists blockers %v, want none", out["C-5"].Blocked)
	}
	for _, id := range []string{"D-3", "D-4"} {
		if why := out[id].Why["C-3"]; why != NotPlanned {
			t.Errorf("%s waits for a ticket closed as not planned: why = %q, want %q", id, why, NotPlanned)
		}
	}
	if !reflect.DeepEqual(out["D-1"].Ready, []string{"a"}) {
		t.Errorf("D-1 is released by its covered blocker and offers %v, want [a]", out["D-1"].Ready)
	}
}
