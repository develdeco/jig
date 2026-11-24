package store

import (
	"fmt"
	"os"
	"testing"
	"time"
)

// TestContainersOfReadsEachChartOnce: with the charts pinned, the issue of each
// of many tickets is a lookup: the chart directory is listed once and each chart
// is read once, however many tickets are asked about and however many times each
// is, and the answers are the ones an unpinned store gives.
func TestContainersOfReadsEachChartOnce(t *testing.T) {
	t.Parallel()
	st := &Store{Root: t.TempDir()}
	const charts, perChart = 12, 5
	var tickets []string
	for c := 0; c < charts; c++ {
		body := fmt.Sprintf("issue: ISS-%d\ntickets:\n", c)
		for i := 0; i < perChart; i++ {
			id := fmt.Sprintf("T-%d-%d", c, i)
			tickets = append(tickets, id)
			body += fmt.Sprintf("  - id: %s\n    title: t\n", id)
		}
		// A ticket two charts list has two containers.
		body += "  - id: T-shared\n    title: shared\n"
		write(t, st.ChartFile(fmt.Sprintf("c%02d", c)), body)
	}
	tickets = append(tickets, "T-shared", "T-none")
	want := map[string][]string{}
	for _, id := range tickets {
		got, err := st.ContainersOf(id, Ticket{})
		if err != nil {
			t.Fatal(err)
		}
		want[id] = got
	}
	if len(want["T-shared"]) != charts || len(want["T-none"]) != 0 || len(want["T-3-2"]) != 1 {
		t.Fatalf("unpinned answers are off: shared %v, none %v, T-3-2 %v", want["T-shared"], want["T-none"], want["T-3-2"])
	}

	pinned := &Store{Root: st.Root}
	unpin := pinned.PinCharts()
	defer unpin()
	for round := 0; round < 3; round++ {
		for _, id := range tickets {
			got, err := pinned.ContainersOf(id, Ticket{})
			if err != nil {
				t.Fatal(err)
			}
			if fmt.Sprint(got) != fmt.Sprint(want[id]) {
				t.Fatalf("%s: pinned containers %v, want %v", id, got, want[id])
			}
			if _, err := pinned.ChartNames(); err != nil {
				t.Fatal(err)
			}
		}
	}
	if pinned.charts.decodes != charts {
		t.Errorf("%d chart reads for %d charts and %d tickets, want one each", pinned.charts.decodes, charts, len(tickets))
	}
	if pinned.charts.namesReads != 1 {
		t.Errorf("the chart directory was listed %d times, want once", pinned.charts.namesReads)
	}

	// Unpinned, the store looks again: a chart written since is seen.
	unpin()
	soon := time.Now().Add(time.Hour)
	write(t, pinned.ChartFile("c00"), "issue: ISS-0\ntickets:\n  - id: T-new\n    title: new\n")
	if err := os.Chtimes(pinned.ChartFile("c00"), soon, soon); err != nil {
		t.Fatal(err)
	}
	if got, err := pinned.IssuesOf("T-new"); err != nil || len(got) != 1 {
		t.Errorf("IssuesOf(T-new) = %v, %v after unpinning, want its issue", got, err)
	}
}
