package tui

import (
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/events"
	"github.com/develdeco/jig/internal/journal"
)

// briefRigWith is the brief screen of JIG-1 at w x h, over the status and the
// brief text given.
func briefRigWith(t *testing.T, w, h int, status, text string) *rig {
	t.Helper()
	g := newRig(t, "JIG-1")
	g.r.answer("status JIG-1 --store /s", status, 0)
	g.r.answer("brief show JIG-1 --text --store /s", text, 0)
	g.feed(append([]events.Envelope{
		jl(at(0, 0), "JIG-1", "", journal.EventApproval, with("s-planner", "", "", "Ada Lovelace <ada@example.invalid>", "0123456789ab", "agent")),
	}, jig1Records()...)...).press("up", "enter")
	if g.m.Screen() != "brief" {
		t.Fatalf("on %s, want brief", g.m.Screen())
	}
	return g.sized(w, h)
}

// rowsOf are the rows of the list titled title as the words each says, one
// line a row, in the order the view draws them.
func rowsOf(view, title string) []string {
	var rows []string
	for _, l := range strings.Split(between(view, "╭─ "+title, "╰"), "\n")[1:] {
		if w := readWords(l); w != "" {
			rows = append(rows, w)
		}
	}
	return rows
}

// TestTheBriefScreenIsOneListOfParts: the brief's sections and slices are one
// list, Parts, with a column for what each is, its name and where it stands, the
// sections in the brief's order and then the slices, and no list of its own for
// either. At the narrow and the wide size the cursor's row is lit and its detail
// is under the list.
func TestTheBriefScreenIsOneListOfParts(t *testing.T) {
	t.Parallel()
	for _, size := range [][2]int{{60, 20}, {100, 30}, {160, 40}} {
		g := briefRigWith(t, size[0], size[1], statusJIG1Brief(), briefTextJIG1())
		view := g.view()
		if strings.Contains(view, "╭─ Sections") || strings.Contains(view, "╭─ Slices") {
			t.Errorf("%dx%d: the screen still has a list of its own for sections or slices:\n%s", size[0], size[1], view)
		}
		head := readWords(between(view, "╭─ Parts", "╰"))
		for _, want := range []string{"9 parts", "kind part stands at", "section Goal", "Detail"} {
			if want != "Detail" && !strings.Contains(head, want) {
				t.Errorf("%dx%d: the Parts list lacks %q:\n%s", size[0], size[1], want, view)
			}
		}
		if !strings.Contains(view, "▸ section") || !strings.Contains(view, "╭─ Detail · Goal") {
			t.Errorf("%dx%d: no lit first row, or no detail of it under the list:\n%s", size[0], size[1], view)
		}
	}

	g := briefRigWith(t, 100, 60, statusJIG1Brief(), briefTextJIG1())
	rows := rowsOf(g.view(), "Parts")
	var names []string
	for _, r := range rows {
		f := strings.Fields(strings.TrimPrefix(r, "▸ "))
		if len(f) >= 2 {
			names = append(names, f[0]+" "+f[1])
		}
	}
	want := []string{"kind part", "section Goal", "section Greeting", "section Farewell", "section Tone", "slice a", "slice b", "slice c", "slice d", "approval the"}
	if strings.Join(names, "|") != strings.Join(want, "|") {
		t.Errorf("the parts are %q, want %q", names, want)
	}
	// A section says which slices cite it, a slice its state.
	if got := strings.Join(rows, "\n"); !strings.Contains(got, "section Tone cited by 2") || !strings.Contains(got, "slice c Pick the tone ? needs-input") {
		t.Errorf("the rows do not say where each part stands:\n%s", got)
	}
	// A section opens nothing and a slice opens its screen.
	g.press("enter")
	if g.m.Screen() != "brief" {
		t.Errorf("a section row opened %s", g.m.Screen())
	}
	g.focus("a").press("enter")
	if g.m.Screen() != "slice" {
		t.Errorf("a slice row opened %s, want slice", g.m.Screen())
	}
}

// TestASectionsDetailIsItsTextRenderedWhole: the detail of a section draws its
// text as markdown, not as the lines the verb printed: bullets, a code span and
// bold in a plain frame, a fence's lines indented, and nothing cut when the text
// is taller than the room.
func TestASectionsDetailIsItsTextRenderedWhole(t *testing.T) {
	t.Parallel()
	body := strings.Join([]string{
		"  Use the **first name**. Fall back to `friend` when there is none.",
		"",
		"  - trim it",
		"    - and the surname",
		"  - never twice",
		"",
		"  1. say hello",
		"  2. say goodbye",
		"",
		"  ```",
		"  Hello, <name>",
		"  ```",
		"",
		"  See [the style guide](https://example.test/style).",
	}, "\n")
	text := strings.Replace(briefTextJIG1(),
		"section: Greeting\n  Use the first name. Fall back to \"friend\" when there is none.\n\n  A name is trimmed.",
		"section: Greeting\n"+body, 1)
	g := briefRigWith(t, 100, 60, statusJIG1Brief(), text).focus("section-2")
	detail := between(g.view(), "╭─ Detail · Greeting", "╰")
	for _, want := range []string{
		"Use the first name. Fall back to `friend` when there is none.",
		"• trim it",
		"  • and the surname",
		"• never twice",
		"1. say hello",
		"2. say goodbye",
		"    Hello, <name>",
		"See the style guide (https://example.test/style).",
	} {
		if !strings.Contains(unframed(detail), want) {
			t.Errorf("the detail of Greeting lacks %q:\n%s", want, detail)
		}
	}
	for _, raw := range []string{"**", "```", "[the style guide]", "  - trim it"} {
		if strings.Contains(detail, raw) {
			t.Errorf("the detail of Greeting shows the markup %q it should have drawn:\n%s", raw, detail)
		}
	}

	// Taller than its room, the text is windowed with a mark inside the box and
	// is whole when the pane is zoomed.
	g.sized(100, 22)
	view := g.view()
	if !strings.Contains(between(view, "╭─ Detail · Greeting", "╰"), "↓ ") {
		t.Errorf("a text taller than its room is not marked as cut:\n%s", view)
	}
	g.tabTo("Detail · Greeting").press("z")
	if !strings.Contains(g.view(), "See the style guide (https://example.test/style).") {
		t.Errorf("the text is not whole when zoomed:\n%s", g.view())
	}
}

// sinceStatus is statusJIG1Brief with the since_approval column the sections
// say.
func sinceStatus(since ...string) string {
	old := "brief_sections[4]{heading,slices}:\n  Goal,-\n  Greeting,a\n  Farewell,b\n  Tone,c+d\n"
	rows := []string{"Goal,-,", "Greeting,a,", "Farewell,b,", "Tone,c+d,"}
	for i, s := range since {
		rows[i] += s
	}
	status := strings.Replace(statusJIG1Brief(), old, "brief_sections[4]{heading,slices,since_approval}:\n  "+strings.Join(rows, "\n  ")+"\n", 1)
	return strings.Replace(status, "approval: current", "approval: changed", 1)
}

// TestASectionSaysWhetherItChangedSinceTheApproval: a section edited or added
// since the approval says so in its row and in its detail, one that is as it was
// approved says that, and a status that records none of it (an older approval)
// says it was not recorded and never says a section changed.
func TestASectionSaysWhetherItChangedSinceTheApproval(t *testing.T) {
	t.Parallel()
	g := briefRigWith(t, 100, 60, sinceStatus("same", "changed", "same", "-"), briefTextJIG1())
	view := g.view()
	rows := strings.Join(rowsOf(view, "Parts"), "\n")
	if !strings.Contains(rows, "section Greeting cited by 1 · changed") || strings.Contains(rows, "Goal no slice · changed") {
		t.Errorf("only the section that changed says so in its row:\n%s", rows)
	}
	if d := readWords(between(view, "╭─ Detail · Goal", "╰")); !strings.Contains(d, "this section is as it was approved") {
		t.Errorf("the detail of an unchanged section lacks it:\n%s", view)
	}
	g.focus("section-2")
	if d := readWords(between(g.view(), "╭─ Detail · Greeting", "╰")); !strings.Contains(d, "this section changed since the approval") {
		t.Errorf("the detail of a changed section lacks it:\n%s", g.view())
	}
	g.focus("section-4")
	if d := readWords(between(g.view(), "╭─ Detail · Tone", "╰")); !strings.Contains(d, "which sections changed was not recorded for this approval") || strings.Contains(d, "changed since") {
		t.Errorf("the detail of a section the approval did not record says the wrong thing:\n%s", g.view())
	}

	// A brief nobody approved has nothing to be changed since.
	none := strings.Replace(sinceStatus("-", "-", "-", "-"), "approval: changed", "approval: none", 1)
	g = briefRigWith(t, 100, 60, none, briefTextJIG1()).focus("section-2")
	if d := readWords(between(g.view(), "╭─ Detail · Greeting", "╰")); strings.Contains(d, "changed") || strings.Contains(d, "approved") {
		t.Errorf("a brief nobody approved says something of the approval:\n%s", g.view())
	}
}

// TestASectionWhoseTextIsNotReadStillShowsItsHeadingAndItsSlices: before the
// brief's text is read the detail says so, and when the text cannot be read it
// says why; either way the heading, the slices that cite the section and what
// the approval says of it are there.
func TestASectionWhoseTextIsNotReadStillShowsItsHeadingAndItsSlices(t *testing.T) {
	t.Parallel()
	g := briefRigWith(t, 100, 60, sinceStatus("same", "same", "same", "changed"), briefTextJIG1()).focus("section-4")
	g.m.w.tickets["JIG-1"].brief = nil
	d := readWords(between(g.view(), "╭─ Detail · Tone", "╰"))
	for _, want := range []string{"heading Tone", "c · needs-input", "d · queued", "this section changed since the approval", "text reading the brief's text"} {
		if !strings.Contains(d, want) {
			t.Errorf("the detail of a section whose text is not read lacks %q:\n%s", want, d)
		}
	}

	g = briefRigWith(t, 100, 60, statusJIG1Brief(), "error: ticket JIG-1 has no brief.md\ncode: VALIDATION_ERROR\n").focus("section-4")
	d = readWords(between(g.view(), "╭─ Detail · Tone", "╰"))
	for _, want := range []string{"heading Tone", "c · needs-input", "the brief's text is not available: ticket JIG-1 has no brief.md"} {
		if !strings.Contains(d, want) {
			t.Errorf("the detail of a section whose text cannot be read lacks %q:\n%s", want, d)
		}
	}
}
