package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/develdeco/jig/internal/events"
	"github.com/develdeco/jig/internal/journal"
)

// longQuestion is a builder's question the way builders write them: the
// context first, a paragraph break kept, and the ask with its default in the
// last line.
const longQuestion = "The greeting copy has two registers in the spec, and the product notes do not choose between them for a first visit to the landing page.\n\nFormal reads as a letter and casual as a chat.\nWhich register should the greeting use? Default: casual."

// questionTicket is a ticket whose slice c asks longQuestion.
func questionTicket() []events.Envelope {
	return []events.Envelope{
		jl(at(0, 1), "JIG-1", "c", journal.EventClaim, with("s-builder", "", "", "", "", "")),
		jl(at(2, 0), "JIG-1", "c", journal.EventQuestion, with("s-builder", "", "", longQuestion, "q-001", "agent")),
	}
}

func sized(g *rig, w, h int) *rig {
	g.t.Helper()
	g.send(tea.WindowSizeMsg{Width: w, Height: h})
	return g
}

// readWords is the words of a screen in reading order.
func readWords(view string) string { return strings.Join(strings.Fields(view), " ") }

// between is the text of a screen after the first from and before the next to.
func between(view, from, to string) string {
	_, rest, _ := strings.Cut(view, from)
	out, _, _ := strings.Cut(rest, to)
	return out
}

func TestWrapWholeCutsNothing(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name, text string
		w          int
		want       []string
	}{
		{"one line", "Formal or casual?", 40, []string{"Formal or casual?"}},
		{"breaks on spaces", "aa bb cc dd", 5, []string{"aa bb", "cc dd"}},
		{"keeps line breaks", "one\ntwo three", 40, []string{"one", "two three"}},
		{"keeps a blank line", "one\n\ntwo", 40, []string{"one", "", "two"}},
		{"a word wider than a line is cut", "abcdefgh ij", 3, []string{"abc", "def", "gh", "ij"}},
		{"trailing breaks go", "one\n\n", 40, []string{"one"}},
		{"no room gives no lines", "one", 0, nil},
	}
	for _, c := range cases {
		got := wrapWhole(c.text, c.w)
		if strings.Join(got, "|") != strings.Join(c.want, "|") || (got == nil) != (c.want == nil) {
			t.Errorf("%s: wrapWhole(%q, %d) = %q, want %q", c.name, c.text, c.w, got, c.want)
		}
		for _, l := range got {
			if strings.Contains(l, "…") || width(l) > c.w {
				t.Errorf("%s: line %q is cut or too wide for %d", c.name, l, c.w)
			}
		}
	}
}

func TestQuestionWholeInNeedsYou(t *testing.T) {
	t.Parallel()
	for _, w := range []int{100, 40, 24} {
		g := sized(newRig(t, "JIG-1"), w, 80)
		g.feed(questionTicket()...)
		view := g.view()
		// The activity panel keeps one cut line per entry; the question does not.
		for _, l := range strings.Split(between(view, "\n  needs you\n", "Activity"), "\n") {
			if strings.HasPrefix(l, "      ") && strings.Contains(l, "…") {
				t.Errorf("at %d columns the question is cut at %q:\n%s", w, l, view)
			}
		}
		if !strings.Contains(readWords(view), readWords(longQuestion)) {
			t.Errorf("at %d columns the question is not drawn whole and in order:\n%s", w, view)
		}
		if w == 100 {
			for _, want := range []string{
				"  ? builder asks about slice c  q-001\n",
				"\n      Which register should the greeting use? Default: casual.\n",
				"\n      Formal reads as a letter and casual as a chat.\n",
			} {
				if !strings.Contains(view+"\n", want) {
					t.Errorf("the screen lacks the line %q:\n%s", want, view)
				}
			}
			if !strings.Contains(view, "  q-001\n      The greeting copy has two registers in the spec, and the product notes do not choose") {
				t.Errorf("the question does not follow its row indented:\n%s", view)
			}
			if !strings.Contains(view, "chat.\n      Which") {
				t.Errorf("the question's line breaks are not kept:\n%s", view)
			}
		}
		for _, l := range strings.Split(view, "\n") {
			if width(l) > w {
				t.Errorf("at %d columns the line %q is %d wide", w, l, width(l))
			}
		}
	}
}

func TestOneLineQuestionIsOneRow(t *testing.T) {
	t.Parallel()
	g := newRig(t, "JIG-1")
	g.feed(jig1()...)
	view := g.view()
	if !strings.Contains(view, "  ? builder asks about slice c  q-001\n      Formal or casual?\n") {
		t.Errorf("a one-line question is not its row and its line:\n%s", view)
	}
}

func TestSliceQuestionWhole(t *testing.T) {
	t.Parallel()
	for _, w := range []int{100, 36} {
		g := sized(newRig(t, "JIG-1"), w, 80)
		g.feed(questionTicket()...)
		g.m.nav = append(g.m.nav, navEntry{screen: screenSlice, ticket: "JIG-1", slice: "c"})
		view := g.view()
		if !strings.Contains(readWords(view), readWords(longQuestion)) {
			t.Errorf("at %d columns the slice's question is not whole:\n%s", w, view)
		}
	}
}

func TestAnswerPromptGrowsToFit(t *testing.T) {
	t.Parallel()
	g := sized(newRig(t, "JIG-1"), 70, 40)
	g.feed(questionTicket()...)
	g.press("a", "Casual")
	view := g.view()
	lines := strings.Split(view, "\n")
	if len(lines) != 40 {
		t.Errorf("the screen is %d rows, want 40", len(lines))
	}
	// The footer begins at the last rule; the Activity above it has cut lines of
	// its own.
	rule := 0
	for i, l := range lines {
		if strings.HasPrefix(strings.TrimSpace(l), "──────") {
			rule = i
		}
	}
	foot := strings.Join(lines[rule:], "\n")
	if !strings.Contains(readWords(foot), readWords("slice c asked: "+longQuestion)) {
		t.Errorf("the prompt does not show the question whole:\n%s", view)
	}
	tail := lines[len(lines)-2:]
	if !strings.Contains(tail[0], "answer q-001 › Casual▏") || !strings.Contains(tail[1], "enter send · esc cancel") {
		t.Errorf("the input line and the hints are not last: %q", tail)
	}
	if strings.Contains(foot, "…") {
		t.Errorf("the prompt cuts the question:\n%s", foot)
	}
	short := len(g.m.footer(70))

	// A resize re-wraps the question the person is answering.
	g.send(tea.WindowSizeMsg{Width: 40, Height: 40})
	if got := len(g.m.footer(40)); got <= short {
		t.Errorf("the prompt at 40 columns is %d rows, want more than the %d it took at 70", got, short)
	}
	view = g.view()
	lines = strings.Split(view, "\n")
	if len(lines) != 40 {
		t.Errorf("after the resize the screen is %d rows, want 40", len(lines))
	}
	if !strings.Contains(readWords(view), readWords("slice c asked: "+longQuestion)) {
		t.Errorf("after the resize the prompt does not show the question whole:\n%s", view)
	}
	if g.m.input == nil || string(g.m.input.value) != "Casual" {
		t.Errorf("the resize lost what was typed: %+v", g.m.input)
	}
}

func TestAnswerPromptTooShort(t *testing.T) {
	t.Parallel()
	g := sized(newRig(t, "JIG-1"), 70, 14)
	g.feed(questionTicket()...)
	g.press("a")
	if g.m.input != nil {
		t.Fatalf("a prompt opened on a screen too short to show the question whole:\n%s", g.view())
	}
	want := "the screen is too short to show the whole question q-001: it needs 18 rows, this has 14"
	if g.m.notice != want {
		t.Errorf("notice = %q, want %q", g.m.notice, want)
	}

	// With the rows it names the prompt opens.
	g.send(tea.WindowSizeMsg{Width: 70, Height: 18})
	g.press("a")
	if g.m.input == nil {
		t.Fatalf("no prompt at the height the notice named: %q", g.m.notice)
	}
	if got := len(strings.Split(g.view(), "\n")); got != 18 {
		t.Errorf("the screen is %d rows, want 18", got)
	}

	// One row less and there is none.
	g.press("esc")
	g.send(tea.WindowSizeMsg{Width: 70, Height: 17})
	g.press("a")
	if g.m.input != nil {
		t.Errorf("a prompt opened one row under what the notice named")
	}
}
