package tui

import (
	"reflect"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/develdeco/jig/internal/axi"
)

// plainStyles draw no color, as a pipe or NO_COLOR does; colorStyles draw with
// the colors of a terminal.
func plainStyles() styles { return styles{r: lipgloss.NewRenderer(&strings.Builder{})} }
func colorStyles() styles { return styles{r: colored()} }

// TestParseBriefTextTakesEachSectionsBodyWhole: the verb prints each section's
// body after its tables as an indented block of its own, and the reader keeps
// every body whole: a heading that holds a comma or a quote, a blank line inside
// a body, a body that is empty, and a line that looks like a block of the verb's
// own.
func TestParseBriefTextTakesEachSectionsBodyWhole(t *testing.T) {
	t.Parallel()
	out := strings.Join([]string{
		axi.KV("brief", [][2]string{{"ticket", "JIG-1"}, {"title", "Greeting: copy"}, {"illustration", "yes"}}),
		axi.Table("sections", []string{"heading", "summary"}, [][]string{
			{"Goal, in short", "Say hello. Say goodbye."},
			{"Empty", ""},
			{"Tone", "Casual"},
		}),
		"section: " + axi.Quote("Goal, in short") + "\n  Say hello. Say goodbye.\n\n  - one\n    - two\n\n  help[1]: not a block\n  odd[1]{x}: nope",
		"section: Empty",
		"section: Tone\n  Casual",
		axi.Help("serve it"),
	}, "\n") + "\n"
	got := parseBriefText(out, 0)
	want := briefText{
		Title: "Greeting: copy", Illustration: true,
		Sections: []briefSection{
			{"Goal, in short", "Say hello. Say goodbye.", "Say hello. Say goodbye.\n\n- one\n  - two\n\nhelp[1]: not a block\nodd[1]{x}: nope"},
			{"Empty", "", ""},
			{"Tone", "Casual", "Casual"},
		},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("brief text = %+v, want %+v", got, want)
	}
	if i, ok := got.sectionAt(2, "Tone"); !ok || i != 2 {
		t.Errorf("section of Tone at 2 = %d, %v", i, ok)
	}
	if i, ok := got.sectionAt(0, "Tone"); !ok || i != 2 {
		t.Errorf("section found by heading when the position holds another = %d, %v", i, ok)
	}
	if _, ok := (*briefText)(nil).sectionAt(0, "Tone"); ok {
		t.Error("an unread brief has a section")
	}
	if refused := parseBriefText("error: ticket JIG-1 has no brief.md\ncode: VALIDATION_ERROR\n", 2); refused.Err != "ticket JIG-1 has no brief.md" {
		t.Errorf("a refusal reads as %+v", refused)
	}
	if odd := parseBriefText("panic\n", 3); !strings.Contains(odd.Err, "exited 3") {
		t.Errorf("an exit with no refusal reads as %+v", odd)
	}
}

// TestBriefMarkdownRendersWhatTheBrowserPageRenders: each part of markdown the
// browser page draws is drawn as lines of at most the width, none of it cut: a
// heading, a paragraph broken at words, a bullet with its depth, a numbered item
// with its number, a quote with its bar, a fence indented with its fence lines
// dropped, a table as written and a link as its text and address.
func TestBriefMarkdownRendersWhatTheBrowserPageRenders(t *testing.T) {
	t.Parallel()
	md := strings.Join([]string{
		"### The rule",
		"",
		"One paragraph that is long enough to need breaking",
		"at the words of a narrow room.",
		"",
		"- first item",
		"  goes on here",
		"  - nested",
		"- second",
		"",
		"1. one",
		"2. two",
		"",
		"> quoted words",
		"> more",
		"",
		"```",
		"code  stays",
		"```",
		"",
		"| a | b |",
		"|---|---|",
		"",
		"See [the page](https://example.test/x) now.",
	}, "\n")
	got := plainStyles().briefMarkdown(md, 30)
	want := []string{
		"The rule",
		"",
		"One paragraph that is long",
		"enough to need breaking at the",
		"words of a narrow room.",
		"",
		"• first item goes on here",
		"  • nested",
		"• second",
		"",
		"1. one",
		"2. two",
		"",
		"│ quoted words more",
		"",
		"  code  stays",
		"",
		"| a | b |",
		"|---|---|",
		"",
		"See the page",
		"(https://example.test/x) now.",
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("rendered:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(want, "\n"))
	}
	for _, l := range got {
		if width(l) > 30 {
			t.Errorf("line %q is %d cells, over the room", l, width(l))
		}
	}
	// A line of code or a table row wider than the room goes on the next line,
	// whole.
	long := plainStyles().briefMarkdown("```\n"+strings.Repeat("x", 25)+"\n```", 12)
	if strings.Join(long, "") != "  "+strings.Repeat("x", 10)+"  "+strings.Repeat("x", 10)+"  "+strings.Repeat("x", 5) {
		t.Errorf("a long line of code is cut: %q", long)
	}
}

// TestBriefMarkdownKeepsBackticksInAPlainFrame: in a plain frame bold shows as
// its bare words and a code span keeps its backticks, so nothing that marked a
// word is lost; in a color terminal they take styles instead, read from the
// escape codes the text carries.
func TestBriefMarkdownKeepsBackticksInAPlainFrame(t *testing.T) {
	t.Parallel()
	md := "Run `jig status` and read **the whole thing** twice."
	plain := plainStyles().briefMarkdown(md, 80)
	if want := []string{"Run `jig status` and read the whole thing twice."}; !reflect.DeepEqual(plain, want) {
		t.Errorf("plain frame = %q, want %q", plain, want)
	}
	color := colorStyles().briefMarkdown(md, 80)
	if len(color) != 1 {
		t.Fatalf("color frame = %q", color)
	}
	line := color[0]
	if !strings.Contains(line, "\x1b[1m") {
		t.Errorf("bold carries no bold code: %q", line)
	}
	if !strings.Contains(line, "\x1b[36m") {
		t.Errorf("a code span carries no key color: %q", line)
	}
	if bare := stripANSI(line); bare != "Run jig status and read the whole thing twice." {
		t.Errorf("color frame without its codes = %q, want the words without backticks or asterisks", bare)
	}
	// A heading is bold in a color terminal and a quote has a dim bar.
	head := colorStyles().briefMarkdown("## Title here", 80)
	if len(head) != 1 || !strings.Contains(head[0], "\x1b[1m") || stripANSI(head[0]) != "Title here" {
		t.Errorf("heading = %q", head)
	}
}
