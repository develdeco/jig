package store

import (
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"testing"
)

// TestChartHistoryLeavesOutHousekeeping: the store's own bookkeeping commits
// are not in a chart's history, a subject that merely starts with the same
// words is, and the limit is filled with real commits behind a run of
// housekeeping ones.
func TestChartHistoryLeavesOutHousekeeping(t *testing.T) {
	t.Parallel()
	st, work := newTestStandaloneStore(t)
	n := 0
	commit := func(subject string) {
		t.Helper()
		n++
		path := "charts/demo/map.md"
		if err := os.MkdirAll(filepath.Join(work, "charts", "demo"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(work, filepath.FromSlash(path)), []byte(subject+strconv.Itoa(n)), 0o644); err != nil {
			t.Fatal(err)
		}
		runGit(t, work, "add", path)
		runGit(t, work, "commit", "-m", subject)
	}
	commit("chart demo: graduate SHOP-1")
	commit("demo: edit the map")
	for i := 0; i < 5; i++ {
		commit(leftoversMessage)
		commit(trackerLandMessage)
		commit("Add the coupon: github issue develdeco/shop#12")
	}
	// Not housekeeping: the same words with more after them, or less before.
	commit(leftoversMessage + " by hand")
	commit("github issue develdeco/shop#12")
	commit("Add the coupon: github issue develdeco/shop#12 reopened")

	subjects := func(limit int) []string {
		t.Helper()
		got, err := st.ChartHistory("demo", limit)
		if err != nil {
			t.Fatalf("ChartHistory: %v", err)
		}
		var out []string
		for _, e := range got {
			out = append(out, e.Subject)
		}
		return out
	}
	want := []string{
		"Add the coupon: github issue develdeco/shop#12 reopened",
		"github issue develdeco/shop#12",
		leftoversMessage + " by hand",
		"demo: edit the map",
		"chart demo: graduate SHOP-1",
	}
	if got := subjects(10); !slices.Equal(got, want) {
		t.Errorf("ChartHistory(10) = %q, want %q", got, want)
	}
	// The limit counts commits kept: the last two real ones sit behind 15
	// housekeeping ones.
	if got := subjects(5); !slices.Equal(got, want) {
		t.Errorf("ChartHistory(5) = %q, want %q", got, want)
	}
	if got := subjects(4); !slices.Equal(got, want[:4]) {
		t.Errorf("ChartHistory(4) = %q, want %q", got, want[:4])
	}
}
