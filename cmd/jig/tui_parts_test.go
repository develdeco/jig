package main

import (
	"regexp"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/journal"
)

// parts are the words a panel's frame and the padding of its columns leave of a
// screen, as a person reads them.
func partsWords(screen string) string {
	return strings.Join(strings.Fields(strings.ReplaceAll(screen, "│", " ")), " ")
}

// TestTUIPipeFramesUseOneSetOfParts: `jig tui` on a pipe over a real store draws
// main as panels, each list under the names of its columns, a ticket whose
// title is as long as a sentence whole with its age last, and a ticket's build
// screen as a Slices panel; the cursor's row is inside a panel and a pipe gets
// no escape sequence.
func TestTUIPipeFramesUseOneSetOfParts(t *testing.T) {
	t.Parallel()
	fx, st, e := workFixture(t)
	const title = "Reconcile every invoice against the ledger of the quarter"
	text, code := runMain(t, e, "", "ticket", "new", "--title", title)
	if code != 0 {
		t.Fatalf("jig ticket new: exit %d\n%s", code, text)
	}
	long := regexp.MustCompile(`[A-Z]+-\d+`).FindString(text)
	if long == "" {
		t.Fatalf("jig ticket new printed no id:\n%s", text)
	}
	for _, id := range []string{fx.Ticket, long} {
		if err := journal.Append(st, id, journal.Line{Event: journal.EventClaim, Slice: "a", Session: "s-1"}); err != nil {
			t.Fatal(err)
		}
	}

	keys, out, wait := startTUI(t, e)
	out.untilWords(t, "ticket title steps where it stands age")
	// A ticket nobody began is in the fold at the end of the list.
	out.until(t, "╭─ Not started ")
	keys.Write([]byte("\x1b[F\r"))
	out.untilFold(t, "Not started", "open")
	out.until(t, long)
	frame := lastScreen(out.String())
	if !strings.Contains(frame, "╭─ ") || strings.Contains(frame, "\x1b") {
		t.Errorf("main is not drawn as panels on a pipe:\n%s", frame)
	}
	var row string
	for _, l := range strings.Split(frame, "\n") {
		if strings.Contains(l, long+" ") {
			row = l
		}
		if strings.Contains(l, "▸") && !strings.Contains(l, "│▸") {
			t.Errorf("the cursor's row is outside a panel: %q", l)
		}
	}
	// The title is whole: its words in order across the row and the line it
	// wrapped onto.
	words := partsWords(frame)
	pos := 0
	for _, w := range strings.Fields(title) {
		i := strings.Index(words[pos:], w)
		if i < 0 {
			t.Errorf("the long title is not whole at %q:\n%s", w, frame)
			break
		}
		pos += i + len(w)
	}
	if !regexp.MustCompile(` \d+[smhd] *│$`).MatchString(row) {
		t.Errorf("the row of the long title does not end with its age: %q\n%s", row, frame)
	}

	// A task's build screen is a Slices panel under its names.
	keys.Write([]byte("\x1b[H\r2"))
	out.untilWords(t, "slice state tries goal")
	if build := lastScreen(out.String()); !strings.Contains(build, "╭─ Slices ") {
		t.Errorf("the build screen has no Slices panel:\n%s", build)
	}
	keys.Write([]byte("q"))
	if code := wait(); code != 0 {
		t.Fatalf("exit %d\n%s", code, out.String())
	}
}
