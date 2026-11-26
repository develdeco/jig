package main

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/develdeco/jig/internal/journal"
)

// queueRowOf is the index of the line of frame that is the row of id: an icon,
// then the id, after a cursor mark and indentation.
func queueRowOf(frame, id string) int {
	for i, l := range strings.Split(frame, "\n") {
		if f := strings.Fields(strings.TrimLeft(l, " │▸")); len(f) > 2 && f[1] == id {
			return i
		}
	}
	return -1
}

// col is the screen column the text starts at in line.
func col(line, text string) int {
	return utf8.RuneCountInString(line[:strings.Index(line, text)])
}

// screenOnRow waits for the last screen to have the cursor on the row of id.
func screenOnRow(t *testing.T, out *tuiOut, id string) string {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		frame := lastScreen(out.String())
		if i := queueRowOf(frame, id); i >= 0 && strings.Contains(strings.Split(frame, "\n")[i], "│▸") {
			return frame
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("the cursor never stood on %s; the last screen was:\n%s", id, lastScreen(out.String()))
	return ""
}

// queueHeadingOf is the index of the line of frame that is the top edge of the
// panel titled title.
func queueHeadingOf(frame, title string) int {
	for i, l := range strings.Split(frame, "\n") {
		if strings.HasPrefix(l, "  ╭─ "+title+" ") {
			return i
		}
	}
	return -1
}

// panelCount is the count at the right of the top edge of the panel titled
// title in frame, or "" when it has none.
func panelCount(frame, title string) string {
	for _, l := range strings.Split(frame, "\n") {
		if strings.HasPrefix(l, "  ╭─ "+title+" ") {
			f := strings.Fields(strings.TrimSuffix(strings.TrimSpace(l), "╮"))
			return strings.TrimSpace(f[len(f)-2])
		}
	}
	return ""
}

// foldState is whether the fold panel titled title in frame is "closed" (its one
// row offers to show the rows) or "open", or "" when the frame has no such panel.
func foldState(frame, title string) string {
	in := false
	for _, l := range strings.Split(frame, "\n") {
		switch {
		case strings.HasPrefix(l, "  ╭─ "+title+" "):
			in = true
		case in && strings.Contains(l, "enter shows them"):
			return "closed"
		case in && strings.Contains(l, "enter folds them"):
			return "open"
		case in && strings.HasPrefix(l, "  ╰"):
			return ""
		}
	}
	return ""
}

// TestTUIMainQueueFrames: `jig tui` on a pipe over a store with an issue and the
// tasks of it (one waits to land), a plain ticket whose brief waits for approval,
// a ticket being built, tickets that landed and a closed one draws main as the
// queue of what the person does next: the issue with its task under it and the
// plain ticket under needs you, the building ticket under running, and done as one
// fold row. A key to the task, then a change of the store that takes a row out
// from above it, leaves the cursor on the task; enter on the issue prints its plan.
func TestTUIMainQueueFrames(t *testing.T) {
	r, _ := digestRig(t)
	down := filepath.Join(t.TempDir(), "gh-is-down")
	e := r.env().withLookPath(func(string) (string, error) { return down, nil })
	scope := []string{"--store", r.root}

	keys, out, wait := startTUI(t, e, scope...)
	frame := screenWith(t, out, "╭─ Running ")
	issue, task := queueRowOf(frame, "TK-1"), queueRowOf(frame, "TK-2")
	if issue < 0 || task != issue+1 {
		t.Errorf("the issue's task is not the row under it:\n%s", frame)
	}
	lines := strings.Split(frame, "\n")
	if col(lines[task], "TK-2") <= col(lines[issue], "TK-1") {
		t.Errorf("the task is not drawn in from its issue:\n%s", frame)
	}
	needs, running, ready := queueHeadingOf(frame, "Needs you"), queueHeadingOf(frame, "Running"), queueHeadingOf(frame, "Ready")
	if tk7 := queueRowOf(frame, "TK-7"); !(needs < tk7 && tk7 < running) {
		t.Errorf("the plain ticket whose brief waits is not under needs you:\n%s", frame)
	}
	if tk9 := queueRowOf(frame, "TK-9"); !(running < tk9 && tk9 < ready) {
		t.Errorf("the ticket being built is not under running:\n%s", frame)
	}
	// The last panel is below the first screen: the end of the list is its one
	// fold row.
	keys.Write([]byte("\x1b[F"))
	foot := screenWith(t, out, "╭─ Done ")
	if queueRowOf(foot, "TK-12") >= 0 || foldState(foot, "Done") != "closed" {
		t.Errorf("done is not one fold row:\n%s", foot)
	}
	keys.Write([]byte("\x1b[H"))
	screenOnRow(t, out, "TK-1")

	// The cursor goes to the task, and on to the plain ticket whose brief waits;
	// a landing another session records takes TK-6 out of needs you from above
	// it, and the cursor stays on its ticket.
	keys.Write([]byte("\x1b[B"))
	screenOnRow(t, out, "TK-2")
	keys.Write([]byte("\x1b[B\x1b[B\x1b[B\x1b[B\x1b[B"))
	screenOnRow(t, out, "TK-7")
	if err := journal.Append(r.st, "TK-6", journal.Line{Event: journal.EventLanded}); err != nil {
		t.Fatal(err)
	}
	var moved string
	for i := 0; i < 2000 && panelCount(moved, "Needs you") != "3"; i++ {
		moved = lastScreen(out.String())
		time.Sleep(10 * time.Millisecond)
	}
	if i := queueRowOf(moved, "TK-7"); i < 0 || !strings.Contains(strings.Split(moved, "\n")[i], "│▸") {
		t.Errorf("the cursor left its ticket when the store moved:\n%s", moved)
	}

	// s lays the list out by chart: the issue heads its tasks, the tickets in no
	// chart come last, and the cursor stays on its ticket; s again brings the
	// list by state back.
	keys.Write([]byte("s"))
	byChart := screenWith(t, out, "in no chart")
	if issue, noChart, tk7 := queueRowOf(byChart, "TK-1"), queueHeadingOf(byChart, "No chart"), queueRowOf(byChart, "TK-7"); issue < 0 || noChart < 0 || !(issue < noChart && noChart < tk7) {
		t.Errorf("the list by chart:\n%s", byChart)
	}
	if i := queueRowOf(byChart, "TK-7"); i < 0 || !strings.Contains(strings.Split(byChart, "\n")[i], "│▸") {
		t.Errorf("the cursor left its ticket across s:\n%s", byChart)
	}
	keys.Write([]byte("s"))
	back := screenWith(t, out, "s by chart")
	if strings.Contains(back, "in no chart") {
		t.Errorf("s again did not bring the list by state back:\n%s", back)
	}

	// enter on the issue prints its plan.
	keys.Write([]byte("\x1b[H\r"))
	plan := screenWith(t, out, "Tasks")
	if !strings.Contains(plan, "TK-1") {
		t.Errorf("enter on the issue did not open its plan:\n%s", plan)
	}
	keys.Write([]byte("q"))
	if wait() != 0 {
		t.Fatalf("jig tui exited non-zero\n%s", out.String())
	}
}
