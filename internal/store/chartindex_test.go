package store

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// TestChartIndexDecodesEachChartOnce: asking which issue lists each of many
// tickets decodes each chart's tickets.yaml once, not once for each ticket; a
// chart written since is decoded again and its new entry is seen.
func TestChartIndexDecodesEachChartOnce(t *testing.T) {
	t.Parallel()
	st := &Store{Root: t.TempDir()}
	for _, c := range []struct{ name, issue string }{{"a", "ISS-1"}, {"b", "ISS-2"}, {"c", "ISS-3"}} {
		write(t, st.ChartFile(c.name), fmt.Sprintf("issue: %s\ntickets:\n  - id: T-1\n    title: One\n  - id: T-2\n    title: Two\n", c.issue))
	}
	for i := 0; i < 20; i++ {
		got, err := st.IssuesOf("T-1")
		if err != nil || len(got) != 3 {
			t.Fatalf("IssuesOf = %v, %v, want the three issues", got, err)
		}
		if _, err := st.ChartsListing("T-2"); err != nil {
			t.Fatal(err)
		}
	}
	if st.charts.decodes != 3 {
		t.Errorf("%d decodes for 3 charts, want one each", st.charts.decodes)
	}

	// A chart changed since is read again.
	soon := time.Now().Add(time.Hour)
	write(t, st.ChartFile("b"), "issue: ISS-2\ntickets:\n  - id: T-1\n    title: One\n  - id: T-2\n    title: Two\n  - id: T-3\n    title: Three\n")
	if err := os.Chtimes(st.ChartFile("b"), soon, soon); err != nil {
		t.Fatal(err)
	}
	got, err := st.IssuesOf("T-3")
	if err != nil || len(got) != 1 || got[0] != "ISS-2" {
		t.Errorf("IssuesOf(T-3) = %v, %v, want ISS-2 once the chart was written", got, err)
	}
	if st.charts.decodes != 4 {
		t.Errorf("%d decodes after one chart changed, want 4", st.charts.decodes)
	}
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}
