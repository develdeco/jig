package main

import (
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// screenWhere waits for the last screen the pipe printed to satisfy ok.
func screenWhere(t *testing.T, out *tuiOut, what string, ok func(frame string) bool) string {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if frame := lastScreen(out.String()); frame != "" && ok(frame) {
			return frame
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("the last screen never %s; it was:\n%s", what, lastScreen(out.String()))
	return ""
}

// onRow says the cursor of the frame stands on the row of id.
func onRow(frame, id string) bool {
	i := queueRowOf(frame, id)
	return i >= 0 && strings.HasPrefix(strings.Split(frame, "\n")[i], "▸")
}

// TestTUISearchOnAPipe: `jig tui` on a pipe over a store with an issue and its
// tasks, a published ticket with a pull request, a ticket whose brief waits and
// a closed one, the cursor on a ticket below the first screen: `/` and the
// letters of an id narrow main as each is typed, the header counting the
// matches; a pull request's number puts its ticket under the cursor; `issue`
// lists the issues alone; a task's words keep its issue's row; enter opens the
// ticket and esc comes back to the narrowed list; esc clears and the cursor is
// on the ticket it started on. No frame holds an escape sequence.
func TestTUISearchOnAPipe(t *testing.T) {
	r, _ := digestRig(t)
	down := filepath.Join(t.TempDir(), "gh-is-down")
	e := r.env().withLookPath(func(string) (string, error) { return down, nil })
	keys, out, wait := startTUI(t, e, "--store", r.root)
	send := func(s string) { t.Helper(); keys.Write([]byte(s)) }
	// typeIn types the letters one at a time, each waiting for its frame.
	typeIn := func(query string) string {
		t.Helper()
		var frame string
		for i, c := range query {
			send(string(c))
			want := "search “" + query[:i+1] + "”"
			if strings.TrimSpace(query[:i+1]) == "" {
				want = "search ·"
			}
			frame = screenWith(t, out, want)
		}
		return frame
	}
	rowsOf := func(frame string) []string {
		var ids []string
		for _, l := range strings.Split(frame, "\n") {
			if f := strings.Fields(strings.TrimLeft(l, "▸ ")); len(f) > 2 && strings.HasPrefix(f[1], "TK-") {
				ids = append(ids, f[1])
			}
		}
		return ids
	}

	screenWith(t, out, "running (")
	// The cursor goes down to the plain ticket whose brief waits.
	send("\x1b[B")
	screenOnRow(t, out, "TK-2")
	send("\x1b[B\x1b[B\x1b[B\x1b[B\x1b[B")
	screenOnRow(t, out, "TK-7")

	// `/` opens the line and every row is there; the letters of an id narrow
	// main as each is typed.
	send("/")
	frame := screenWith(t, out, "/ ▏")
	if !strings.Contains(frame, "esc clears") || !strings.Contains(frame, "/ ▏") || !onRow(frame, "TK-7") {
		t.Errorf("the open line, before a letter:\n%s", frame)
	}
	typeIn("tk-1")
	frame = screenWhere(t, out, "narrowed to the TK-1 ids", func(f string) bool {
		return strings.Contains(f, "search “tk-1”") && queueRowOf(f, "TK-7") < 0
	})
	if got := strings.Join(rowsOf(frame), " "); !strings.Contains(got, "TK-1") || !strings.Contains(got, "TK-12") {
		t.Errorf("/tk-1 drew %q", got)
	}
	send("2")
	frame = screenWithout(t, out, "search “tk-12”", "TK-11")
	if got := strings.Join(rowsOf(frame), " "); got != "TK-12" {
		t.Errorf("/tk-12 drew %q, want TK-12 alone:\n%s", got, frame)
	}
	if !strings.Contains(frame, "1 of ") || !strings.Contains(frame, "esc clears") || !onRow(frame, "TK-12") {
		t.Errorf("the header does not count the match, or the cursor is not on it:\n%s", frame)
	}

	// esc clears it, and the cursor is where it started.
	send("\x1b")
	screenWhere(t, out, "cleared with the cursor back on TK-7", func(f string) bool {
		return !searching(f) && onRow(f, "TK-7")
	})

	// A pull request's number puts its ticket under the cursor.
	send("/")
	screenWith(t, out, "/ ▏")
	typeIn("16")
	frame = screenWithout(t, out, "search “16”", "TK-7")
	if got := strings.Join(rowsOf(frame), " "); got != "TK-6" || !onRow(frame, "TK-6") {
		t.Errorf("/16 drew %q, want the ticket of pull request 16 under the cursor:\n%s", got, frame)
	}
	send("\x7f\x7f")
	screenWith(t, out, "/ ▏")

	// `issue` lists the issues alone.
	typeIn("issue")
	frame = screenWithout(t, out, "search “issue”", "TK-6")
	if got := strings.Join(rowsOf(frame), " "); got != "TK-1" {
		t.Errorf("/issue drew %q, want the issue alone:\n%s", got, frame)
	}
	send("\x1b")
	screenWhere(t, out, "cleared", func(f string) bool { return !searching(f) })

	// A task's words keep its issue's row, the task under it.
	send("/")
	screenWith(t, out, "/ ▏")
	typeIn("tk-2")
	frame = screenWithout(t, out, "search “tk-2”", "TK-6")
	issue, task := queueRowOf(frame, "TK-1"), queueRowOf(frame, "TK-2")
	if issue < 0 || task != issue+1 || !onRow(frame, "TK-2") {
		t.Errorf("/tk-2 did not keep the issue's row over the task under the cursor:\n%s", frame)
	}

	// enter opens the ticket, esc comes back to the narrowed list.
	send("\r")
	screenWith(t, out, "› TK-2")
	send("\x1b")
	frame = screenWhere(t, out, "back on the narrowed list", func(f string) bool {
		return strings.Contains(f, "search “tk-2”") && onRow(f, "TK-2")
	})
	if strings.Contains(frame, "/ ▏") || strings.Contains(frame, "TK-6") {
		t.Errorf("the list coming back is not the narrowed one with the line closed:\n%s", frame)
	}

	// esc clears it, the cursor on the ticket the search started on.
	send("\x1b")
	screenWhere(t, out, "cleared with the cursor back on TK-7", func(f string) bool {
		return !searching(f) && onRow(f, "TK-7")
	})

	// q is text while the line is open, and quits once it is closed.
	send("/")
	screenWith(t, out, "/ ▏")
	typeIn("q")
	send("\x1b")
	screenWhere(t, out, "cleared", func(f string) bool { return !searching(f) })
	send("q")
	if code := wait(); code != 0 {
		t.Fatalf("exit %d, want 0\n%s", code, out.String())
	}
	if got := out.String(); strings.Contains(got, "\x1b") {
		t.Errorf("a pipe got an escape sequence:\n%q", got)
	}
}

// searching says the header of the frame is a search's.
func searching(frame string) bool {
	head, _, _ := strings.Cut(frame, "\n")
	return strings.Contains(head, "search")
}

// screenWithout waits for the last screen to hold want and not hold gone.
func screenWithout(t *testing.T, out *tuiOut, want, gone string) string {
	t.Helper()
	return screenWhere(t, out, "held "+want+" without "+gone, func(f string) bool {
		return strings.Contains(f, want) && !strings.Contains(f, gone)
	})
}
