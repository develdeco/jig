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
		if f := strings.Fields(strings.TrimLeft(l, "▸ ")); len(f) > 2 && f[1] == id {
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
		if i := queueRowOf(frame, id); i >= 0 && strings.HasPrefix(strings.Split(frame, "\n")[i], "▸") {
			return frame
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("the cursor never stood on %s; the last screen was:\n%s", id, lastScreen(out.String()))
	return ""
}

// queueHeadingOf is the index of the line of frame that is the heading or fold
// row of a group.
func queueHeadingOf(frame, group string) int {
	for i, l := range strings.Split(frame, "\n") {
		if strings.HasPrefix(strings.TrimLeft(l, "▸▾ "), group) {
			return i
		}
	}
	return -1
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
	frame := screenWith(t, out, "running (")
	issue, task := queueRowOf(frame, "TK-1"), queueRowOf(frame, "TK-2")
	if issue < 0 || task != issue+1 {
		t.Errorf("the issue's task is not the row under it:\n%s", frame)
	}
	lines := strings.Split(frame, "\n")
	if col(lines[task], "TK-2") <= col(lines[issue], "TK-1") {
		t.Errorf("the task is not drawn in from its issue:\n%s", frame)
	}
	needs, running, done := queueHeadingOf(frame, "needs you ("), queueHeadingOf(frame, "running ("), queueHeadingOf(frame, "done (")
	if tk7 := queueRowOf(frame, "TK-7"); !(needs < tk7 && tk7 < running) {
		t.Errorf("the plain ticket whose brief waits is not under needs you:\n%s", frame)
	}
	if tk9 := queueRowOf(frame, "TK-9"); !(running < tk9 && tk9 < done) {
		t.Errorf("the ticket being built is not under running:\n%s", frame)
	}
	if queueRowOf(frame, "TK-12") >= 0 || !strings.Contains(frame, "done (") || !strings.Contains(frame, "enter shows them") {
		t.Errorf("done is not one fold row:\n%s", frame)
	}

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
	moved := screenWith(t, out, "needs you (3)")
	if i := queueRowOf(moved, "TK-7"); i < 0 || !strings.HasPrefix(strings.Split(moved, "\n")[i], "▸") {
		t.Errorf("the cursor left its ticket when the store moved:\n%s", moved)
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
