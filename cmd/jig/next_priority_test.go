package main

import "testing"

// TestAndList: words read as prose.
func TestAndList(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		words []string
		want  string
	}{
		{nil, ""},
		{[]string{"JIG-1"}, "JIG-1"},
		{[]string{"JIG-1", "JIG-2"}, "JIG-1 and JIG-2"},
		{[]string{"JIG-1", "JIG-2", "JIG-3"}, "JIG-1, JIG-2 and JIG-3"},
	} {
		if got := andList(tc.words); got != tc.want {
			t.Errorf("andList(%v) = %q, want %q", tc.words, got, tc.want)
		}
	}
}

// TestNextStoreOffersRankOnlyWhenTheOrderIsOpen: the hint to rank appears when
// several tickets are ready and one has no rank, and not for one ready ticket,
// for tickets that are all ranked, or for an unranked ticket that is not ready.
func TestNextStoreOffersRankOnlyWhenTheOrderIsOpen(t *testing.T) {
	t.Parallel()
	one := 1
	ranked := func(id string) nextRow { return nextRow{ID: id, Rank: &one} }
	unranked := func(id string) nextRow { return nextRow{ID: id} }
	for _, tc := range []struct {
		name string
		n    nextStore
		want bool
	}{
		{"one ready ticket", nextStore{Tickets: []nextRow{unranked("A")}, Next: []string{"A"}}, false},
		{"all ranked", nextStore{Tickets: []nextRow{ranked("A"), ranked("B")}, Next: []string{"A", "B"}}, false},
		{"one unranked of two", nextStore{Tickets: []nextRow{ranked("A"), unranked("B")}, Next: []string{"A", "B"}}, true},
		{"the unranked is not ready", nextStore{Tickets: []nextRow{ranked("A"), ranked("B"), unranked("C")}, Next: []string{"A", "B"}}, false},
	} {
		if got := tc.n.unrankedAmongMany(); got != tc.want {
			t.Errorf("%s: unrankedAmongMany = %v, want %v", tc.name, got, tc.want)
		}
	}
}
