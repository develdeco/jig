package tui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// TestAgeReadsTheLargestWholeUnit: a row's age is the time since its latest
// line from the model's clock, in seconds under a minute, then minutes, hours
// and days, each whole and the next unit taken at its first moment; a row with
// no line has none.
func TestAgeReadsTheLargestWholeUnit(t *testing.T) {
	t.Parallel()
	g := newRig(t, "")
	atClock(g, "2026-11-20T12:00:00Z")
	now := g.m.now()
	for _, c := range []struct {
		ago  time.Duration
		want string
	}{
		{0, "0s"},
		{42 * time.Second, "42s"},
		{59*time.Second + 900*time.Millisecond, "59s"},
		{time.Minute, "1m"},
		{4*time.Minute + 59*time.Second, "4m"},
		{59 * time.Minute, "59m"},
		{time.Hour, "1h"},
		{3*time.Hour + 20*time.Minute, "3h"},
		{23*time.Hour + 59*time.Minute, "23h"},
		{24 * time.Hour, "1d"},
		{13*24*time.Hour + 5*time.Hour, "13d"},
		{-5 * time.Second, "0s"},
	} {
		if got := g.m.ageOf(now.Add(-c.ago)); got != c.want {
			t.Errorf("%v ago reads %q, want %q", c.ago, got, c.want)
		}
	}
	if got := g.m.ageOf(time.Time{}); got != "" {
		t.Errorf("a row with no line reads %q, want nothing", got)
	}
}

// TestMainSectionsArePanelsWithNamedColumns: main draws each group as a panel
// with a capitalized title and its count, a list under the names of its columns,
// a fold as a panel of its one row, and every row the cursor can stand on in a
// panel; at three sizes.
func TestMainSectionsArePanelsWithNamedColumns(t *testing.T) {
	t.Parallel()
	for _, size := range layoutSizes {
		g := chartRig(t)
		g.send(tea.WindowSizeMsg{Width: size[0], Height: 40})
		pg := g.m.mainPage()
		for _, title := range []string{"Needs you", "Running", "Waiting", "Not started"} {
			if summaryOf(pg, title) == "" {
				t.Errorf("%v: main has no panel %q", size, title)
			}
		}
		lines := strings.Split(stripANSI(g.view()), "\n")
		title, inside := "", 0
		for i, l := range lines {
			switch {
			case strings.HasPrefix(l, "  ╭─ "):
				title, _, _ = strings.Cut(strings.TrimPrefix(l, "  ╭─ "), " ─")
				inside = 0
			case strings.HasPrefix(l, "  ╰"):
				title = ""
			case strings.HasPrefix(l, "  │"):
				inside++
				if inside == 1 && title != "Not started" && title != "Done" && !strings.Contains(l, "ticket") {
					t.Errorf("%v: the first line of %s is %q, want the names of its columns", size, title, l)
				}
				if title == "Not started" && !strings.Contains(l, "enter shows them") {
					t.Errorf("%v: the fold %s reads %q, want its one row", size, title, l)
				}
			case strings.HasPrefix(l, "▸"):
				t.Errorf("%v: the cursor's row is outside a panel at line %d: %q", size, i, l)
			}
		}
	}
}

// TestMainTitleColumnFillsTheWidthLeft: a title as long as a sentence is drawn
// whole in the width the id, the steps, the words and the age leave, and its
// row ends with its age, at three sizes.
func TestMainTitleColumnFillsTheWidthLeft(t *testing.T) {
	t.Parallel()
	const title = "Reconcile every invoice against the ledger of the quarter"
	for _, size := range [][2]int{{80, 24}, {100, 30}, {230, 50}} {
		g := newRig(t, "")
		atClock(g, "2026-10-23T09:14:00Z")
		g.feed(jl(at(2, 0), "LONG-1", "", "approval"))
		g.m.w.setRoster([]rosterRow{{ID: "LONG-1", Title: title,
			Digest: task("build", true, "done|approved", "current|ready to build", "pending|after the build", "pending|after a clean review", "pending|after the publish")}})
		g.m.w.gen++
		g.send(tea.WindowSizeMsg{Width: size[0], Height: size[1]})
		lines := strings.Split(stripANSI(g.view()), "\n")
		block := rowBlock(lines, "LONG-1")
		if block == nil {
			t.Fatalf("%v: LONG-1 is not drawn:\n%s", size, strings.Join(lines, "\n"))
		}
		words := strings.Fields(strings.Join(block, " "))
		text := strings.Join(words, " ")
		pos := 0
		for _, w := range strings.Fields(title) {
			i := strings.Index(text[pos:], w)
			if i < 0 {
				t.Errorf("%v: the title is not whole in %q", size, text)
				break
			}
			pos += i + len(w)
		}
		first := strings.TrimRight(strings.TrimSuffix(strings.TrimRight(block[0], " "), "│"), " ")
		if !strings.HasSuffix(first, "12m") {
			t.Errorf("%v: the row does not end with its age: %q", size, block[0])
		}
		if size[0] >= 230 && len(block) != 1 {
			t.Errorf("%v: at a wide terminal the row takes %d lines:\n%s", size, len(block), fmt.Sprint(block))
		}
	}
}
