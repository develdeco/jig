package main

import (
	"strings"
	"testing"
	"time"

	"github.com/develdeco/jig/internal/store"
)

// The arrow keys as a terminal sends them.
const (
	up   = "\x1b[A"
	down = "\x1b[B"
)

// untilScreen waits for the last screen to satisfy ok, and fails with it when it
// never does.
func (o *tuiOut) untilScreen(t *testing.T, what string, ok func(screen string) bool) string {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if screen := lastScreen(o.String()); ok(screen) {
			return screen
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("the screens never showed %s; the last one was:\n%s", what, lastScreen(o.String()))
	return ""
}

// words is a screen as the words it says: the frame and the padding apart.
func screenWords(screen string) string {
	return strings.Join(strings.Fields(strings.ReplaceAll(screen, "│", " ")), " ")
}

// briefPartsRig is the plan rig with a task, TK-2, whose brief has three sections
// and a slice that cites the second, approved with its sections recorded and then
// edited in that section, under the issue TK-1 whose body is its need.
func briefPartsRig(t *testing.T) chartRig {
	t.Helper()
	r := containerRig(t)
	r.put(t, "tickets/TK-1/ticket.yaml", "schema_version: 1\ntitle: Ticket TK-1\nkind: issue\nbody: |\n  The issue is that tickets of a chart read apart.\n")
	r.put(t, "tickets/TK-2/brief.md", "# TK-2\n\n## Goal\n\nDo it.\n\n## Steps\n\n- first `alpha.Clamp` step\n- second step\n\n## Done\n\nWhen it is done.\n")
	record, _, err := r.st.BriefRecord("TK-2")
	if err != nil {
		t.Fatal(err)
	}
	sum, err := r.st.BriefSHA256("TK-2")
	if err != nil {
		t.Fatal(err)
	}
	if err := r.st.WriteApproval("TK-2", store.Approval{By: "Pat Example", At: "2026-10-23T08:00:00Z", BriefSHA256: sum, Provenance: "person", Sections: record}); err != nil {
		t.Fatal(err)
	}
	// The Steps section is edited after the approval, and the slice cites it as
	// it is now.
	edited := "# TK-2\n\n## Goal\n\nDo it.\n\n## Steps\n\n- first `alpha.Clamp` step, edited\n- second step\n\n## Done\n\nWhen it is done.\n"
	r.put(t, "tickets/TK-2/brief.md", edited)
	var hash string
	for _, s := range store.BriefSections([]byte(edited)) {
		if s.Heading == "Steps" {
			hash = s.Hash
		}
	}
	r.put(t, "tickets/TK-2/slices.yaml", "slices:\n  - id: a\n    workspace: alpha\n    goal: Build a.\n    oracle: test\n    blocked_by: []\n    from_brief: [\""+hash+"\"]\n")
	return r
}

// TestTUIPipeFramesOpenTheBriefPartByPart: `jig tui` on a pipe over a real task of
// a real issue draws the brief screen as a list of its parts: the issue and its
// need, a section rendered whole with the code span's backticks, the changed word
// for the edited section and the approval naming it; enter on a cited slice opens
// the slice screen, whose section row opens the brief back at it; and from the
// ticket screen enter on brief approved opens the approval.
func TestTUIPipeFramesOpenTheBriefPartByPart(t *testing.T) {
	t.Parallel()
	r := briefPartsRig(t)
	keys, out, wait := startTUI(t, r.env(), "TK-2", "--store", r.root)
	out.until(t, "brief changed after approval")
	// A key before the roster is read is refused (the ticket reads "reading"): it is
	// pressed again until the screen opens.
	for i := 0; i < 40 && !strings.Contains(lastScreen(out.String()), "╭─ Parts"); i++ {
		keys.Write([]byte("1"))
		time.Sleep(250 * time.Millisecond)
	}
	screen := out.untilScreen(t, "the brief with its parts", func(s string) bool {
		return strings.Contains(s, "╭─ Parts") && strings.Contains(screenWords(s), "The issue is that tickets of a chart read apart.")
	})
	words := screenWords(screen)
	for _, want := range []string{"issue TK-1", "The issue is that tickets of a chart read apart.", "section Steps slice a · changed", "approval the approval of the brief the brief changed since"} {
		if !strings.Contains(words, want) {
			t.Errorf("the brief screen lacks %q:\n%s", want, screen)
		}
	}

	// The edited section: its text drawn whole, with its code span's backticks,
	// and the word that it changed since the approval.
	keys.Write([]byte(down + down)) // Goal, Steps
	screen = out.untilScreen(t, "the Steps section", func(s string) bool {
		return strings.Contains(s, "╭─ Detail · Steps") && strings.Contains(s, "alpha.Clamp")
	})
	words = screenWords(screen)
	for _, want := range []string{"• first `alpha.Clamp` step, edited", "• second step", "this section changed since the approval", "cited by"} {
		if !strings.Contains(words, want) {
			t.Errorf("the Steps section's detail lacks %q:\n%s", want, screen)
		}
	}

	// The approval names the section that changed.
	keys.Write([]byte("\x1b[F")) // end: the approval
	screen = out.untilScreen(t, "the approval", func(s string) bool { return strings.Contains(s, "╭─ Detail · approval") })
	if words = screenWords(screen); !strings.Contains(words, "changed Steps") {
		t.Errorf("the approval's detail does not name the section that changed:\n%s", screen)
	}

	// enter on a cited slice opens the slice screen, whose section row opens the
	// brief back at the section.
	keys.Write([]byte(up + up + up)) // up to the Steps section
	out.untilScreen(t, "the Steps section again", func(s string) bool { return strings.Contains(s, "╭─ Detail · Steps") })
	keys.Write([]byte("\t" + down + "\r")) // the detail pane, down from its first line to the cited slice, enter
	out.untilScreen(t, "the slice screen", func(s string) bool { return strings.Contains(s, "› slice a") })
	screen = out.untilScreen(t, "the slice's section", func(s string) bool { return strings.Contains(s, "╭─ Brief sections") })
	if words = screenWords(screen); !strings.Contains(words, "Steps changed since the approval") {
		t.Errorf("the slice screen does not list the section that cites it, changed:\n%s", screen)
	}
	keys.Write([]byte("\t\r")) // tab to the Brief sections pane, enter on its row
	screen = out.untilScreen(t, "the brief back at the section", func(s string) bool {
		return strings.Contains(s, "╭─ Parts") && strings.Contains(s, "╭─ Detail · Steps")
	})
	if !strings.Contains(screenWords(screen), "▸ section Steps") {
		t.Errorf("the brief did not open with the cursor on the section:\n%s", screen)
	}

	// From the ticket screen, enter on brief approved opens the approval.
	keys.Write([]byte("\x1b")) // esc: the slice screen
	out.untilScreen(t, "the slice screen again", func(s string) bool { return strings.Contains(s, "› slice a") })
	keys.Write([]byte("\x1b")) // the brief it came from
	out.untilScreen(t, "the brief it came from", func(s string) bool { return strings.Contains(s, "╭─ Parts") })
	keys.Write([]byte("\x1b")) // the ticket
	out.untilScreen(t, "the ticket screen", func(s string) bool { return strings.Contains(s, "╭─ Steps") })
	// The Brief pane's cursor is on the sub-step that matters, brief approved:
	// enter opens the approval.
	keys.Write([]byte("\t\r"))
	screen = out.untilScreen(t, "the approval", func(s string) bool { return strings.Contains(s, "╭─ Detail · approval") })
	if words = screenWords(screen); !strings.Contains(words, "▸ approval") || !strings.Contains(words, "changed Steps") {
		t.Errorf("enter on brief approved did not open the approval:\n%s", screen)
	}
	keys.Write([]byte("q"))
	if code := wait(); code != 0 {
		t.Fatalf("jig tui: exit %d\n%s", code, out.String())
	}
}
