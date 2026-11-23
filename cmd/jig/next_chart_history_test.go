package main

import (
	"slices"
	"testing"
)

// TestNextChartHistoryLeavesOutHousekeeping: `jig next <chart> --json` lists
// the real commits of a chart, newest first, when its newest commits are the
// store's own housekeeping.
func TestNextChartHistoryLeavesOutHousekeeping(t *testing.T) {
	t.Parallel()
	r := newChartRig(t)
	r.shop(t, false)
	gitIn(t, r.root, "init", "-b", "main")
	commit := func(content, subject string) {
		t.Helper()
		r.put(t, "charts/checkout/map.md", content)
		gitIn(t, r.root, "add", "-A")
		gitIn(t, r.root, "commit", "-m", subject)
	}
	commit("# Coupons at checkout\n", "chart checkout: graduate")
	commit("# Coupons at checkout\n\nA\n", "checkout: a code names its shop")
	commit("# Coupons at checkout\n\nB\n", "jig: record uncommitted store state")
	commit("# Coupons at checkout\n\nC\n", "jig: record the tracker mirror's sync")
	commit("# Coupons at checkout\n\nD\n", "Apply a coupon code: github issue develdeco/shop#3")

	n := r.next(t, "checkout")
	var got []string
	for _, h := range n.History {
		got = append(got, h.Subject)
	}
	if want := []string{"checkout: a code names its shop", "chart checkout: graduate"}; !slices.Equal(got, want) {
		t.Errorf("history = %q, want %q", got, want)
	}
}
