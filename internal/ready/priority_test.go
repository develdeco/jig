package ready

import (
	"reflect"
	"testing"

	"github.com/develdeco/jig/internal/store"
)

func rank(n int) *int { return &n }

// TestByPriority: ranked tickets come first, lowest rank first; tickets of
// one rank and the unranked keep the order they were given in.
func TestByPriority(t *testing.T) {
	t.Parallel()
	got := ByPriority([]Ranked{
		{ID: "JIG-1"},
		{ID: "JIG-2", Rank: rank(3)},
		{ID: "JIG-3"},
		{ID: "JIG-4", Rank: rank(1)},
		{ID: "JIG-5", Rank: rank(3)},
		{ID: "JIG-6", Rank: rank(-2)},
		{ID: "JIG-7", Rank: rank(0)},
	})
	if want := []string{"JIG-6", "JIG-7", "JIG-4", "JIG-2", "JIG-5", "JIG-1", "JIG-3"}; !reflect.DeepEqual(got, want) {
		t.Errorf("ByPriority = %v, want %v", got, want)
	}
	if got := ByPriority(nil); len(got) != 0 {
		t.Errorf("ByPriority(nil) = %v, want none", got)
	}
}

// TestByPriorityLeavesItsArgumentAlone: the order is returned, not applied to
// the slice the caller passed.
func TestByPriorityLeavesItsArgumentAlone(t *testing.T) {
	t.Parallel()
	in := []Ranked{{ID: "JIG-1"}, {ID: "JIG-2", Rank: rank(1)}}
	ByPriority(in)
	if in[0].ID != "JIG-1" || in[1].ID != "JIG-2" {
		t.Errorf("the argument was reordered: %+v", in)
	}
}

// TestSharedRanks: a rank two or more tickets hold is listed with them, lowest
// rank first; a rank one ticket holds, and no rank at all, are not.
func TestSharedRanks(t *testing.T) {
	t.Parallel()
	got := SharedRanks([]Ranked{
		{ID: "JIG-1", Rank: rank(5)},
		{ID: "JIG-2", Rank: rank(2)},
		{ID: "JIG-3"},
		{ID: "JIG-4", Rank: rank(5)},
		{ID: "JIG-5", Rank: rank(2)},
		{ID: "JIG-6", Rank: rank(5)},
		{ID: "JIG-7", Rank: rank(9)},
		{ID: "JIG-8"},
	})
	want := []SharedRank{
		{Rank: 2, Tickets: []string{"JIG-2", "JIG-5"}},
		{Rank: 5, Tickets: []string{"JIG-1", "JIG-4", "JIG-6"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("SharedRanks = %+v, want %+v", got, want)
	}
	if got := SharedRanks([]Ranked{{ID: "JIG-1"}, {ID: "JIG-2"}}); len(got) != 0 {
		t.Errorf("two unranked tickets share a rank: %+v", got)
	}
}

// TestSites: each site once, in the order the slices name them; a workspace
// no repo owns, or several do, is no site, and a written "<repo>/<id>" is the
// id in its repo.
func TestSites(t *testing.T) {
	t.Parallel()
	repos := []Repo{
		{Name: "api", Workspaces: []string{"core", "shared"}},
		{Name: "web", Workspaces: []string{"ui", "shared"}},
	}
	slices := []store.Slice{
		{ID: "a", Workspace: "core"},
		{ID: "b", Workspace: "ui"},
		{ID: "c", Workspace: "core"},
		{ID: "d", Workspace: "shared"},
		{ID: "e", Workspace: "web/shared"},
		{ID: "f", Workspace: "nowhere"},
	}
	got := Sites(slices, Place(repos, slices))
	want := []Site{{"api", "core"}, {"web", "ui"}, {"web", "shared"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Sites = %v, want %v", got, want)
	}

	// With one repo, every workspace is its own.
	one := []Repo{{Name: "api"}}
	got = Sites(slices[:2], Place(one, slices[:2]))
	if want := []Site{{"api", "core"}, {"api", "ui"}}; !reflect.DeepEqual(got, want) {
		t.Errorf("Sites in one repo = %v, want %v", got, want)
	}
}

// TestOverlaps: a pair of tickets with a site in common is named with it, one
// entry per site; tickets that share a name of workspace in different repos
// do not overlap.
func TestOverlaps(t *testing.T) {
	t.Parallel()
	got := Overlaps([]Footprint{
		{Ticket: "JIG-1", Sites: []Site{{"api", "core"}, {"web", "ui"}}},
		{Ticket: "JIG-2", Sites: []Site{{"web", "ui"}, {"api", "core"}}},
		{Ticket: "JIG-3", Sites: []Site{{"web", "core"}, {"web", "ui"}}},
		{Ticket: "JIG-4"},
	})
	want := []Overlap{
		{Site: Site{"api", "core"}, Tickets: [2]string{"JIG-1", "JIG-2"}},
		{Site: Site{"web", "ui"}, Tickets: [2]string{"JIG-1", "JIG-2"}},
		{Site: Site{"web", "ui"}, Tickets: [2]string{"JIG-1", "JIG-3"}},
		{Site: Site{"web", "ui"}, Tickets: [2]string{"JIG-2", "JIG-3"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Overlaps = %+v, want %+v", got, want)
	}
	if got := Overlaps([]Footprint{{Ticket: "JIG-1", Sites: []Site{{"api", "core"}}}}); len(got) != 0 {
		t.Errorf("one ticket overlaps itself: %+v", got)
	}
}
