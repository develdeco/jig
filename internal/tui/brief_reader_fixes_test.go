package tui

import (
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/axi"
)

// TestAStyledWordBrokenAtItsColumnLosesNoCharacter: text is cut as text and
// styled after, so a code span wider than its column, drawn in color, reads in
// the lines it breaks to exactly as the word does, at any width, and each line is
// within it.
func TestAStyledWordBrokenAtItsColumnLosesNoCharacter(t *testing.T) {
	t.Parallel()
	word := "~/.config/jig-runs/gate7-audit/follow-ups/gate7-audit-low-notes.md/and/then/some/more/path/segments/to/pass/ninety-three"
	for _, w := range []int{40, 64, 93} {
		lines := colorStyles().briefMarkdown("`"+word+"`", w)
		var got strings.Builder
		for _, l := range lines {
			if width(l) > w {
				t.Errorf("%d: line %q is %d cells", w, stripANSI(l), width(l))
			}
			got.WriteString(stripANSI(l))
		}
		if got.String() != word {
			t.Errorf("%d: the lines read %q, want the word %q", w, got.String(), word)
		}
		if len(lines) < 2 {
			t.Errorf("%d: the word was not broken: %q", w, lines)
		}
		// Every piece is closed: no color runs onto what follows it.
		for _, l := range lines {
			if strings.Contains(l, "\x1b[") && !strings.HasSuffix(l, "\x1b[0m") && !strings.HasSuffix(l, "\x1b[m") {
				t.Errorf("%d: line %q leaves a style open", w, l)
			}
		}
	}
	// A word wrapWhole breaks reads the same.
	if got := strings.Join(wrapWhole(word, 40), ""); got != word {
		t.Errorf("wrapWhole lost characters: %q", got)
	}
}

// citedByFive is a status of JIG-1 whose section Rules is cited by five slices
// with ids as long as the ones a real store has.
// longHeading is a heading as long as the ones a real brief has.
const longHeading = "The ticket screen's Brief sub-steps open their part"

func citedByFive() (status, text string) {
	ids := []string{"approval-records-sections", "parts-list-and-section-text", "section-slice-links", "approval-part", "issue-part"}
	var rows [][]string
	for _, id := range ids {
		rows = append(rows, []string{id, "queued", "0", "-", "-", "Goal of " + id, "app", "unit", "-"})
	}
	status = strings.Join([]string{
		"ticket: JIG-1", "state: building",
		axi.Table("slices", []string{"id", "state", "attempts", "blocked_by", "question", "goal", "workspace", "oracle", "from_gate"}, rows),
		"questions: none",
		axi.KV("brief", [][2]string{{"approval", "current"}, {"approved_by", "Ada Lovelace"}, {"identity", "Ada Lovelace"}, {"approved_at", "2026-10-23T09:00:00Z"}, {"provenance", "person"}}),
		axi.Table("brief_sections", []string{"heading", "slices"}, [][]string{{"Rules", strings.Join(ids, "+")}, {longHeading, "-"}}),
		axi.Help("Run `jig next JIG-1`"),
	}, "\n") + "\n"
	text = strings.Join([]string{
		axi.KV("brief", [][2]string{{"ticket", "JIG-1"}, {"title", "Greeting copy"}, {"illustration", "no"}}),
		axi.Table("sections", []string{"heading", "summary"}, [][]string{{"Rules", "Rules."}, {longHeading, "Long."}}),
		"section: Rules\n  Rules.", axi.Help("serve it"),
	}, "\n") + "\n"
	return status, text
}

// TestAnIdIsNeverBrokenMidWord: an id is a measured column or text that wraps at
// its spaces, never cut inside: the section cited by five slices names them whole
// in its row, and a slice's row and its detail have its id whole.
func TestAnIdIsNeverBrokenMidWord(t *testing.T) {
	t.Parallel()
	ids := []string{"approval-records-sections", "parts-list-and-section-text", "section-slice-links", "approval-part", "issue-part"}
	status, text := citedByFive()
	for _, size := range [][2]int{{60, 20}, {80, 24}, {100, 30}} {
		g := briefRigWith(t, size[0], size[1], status, text)
		words := readWords(between(g.view(), "╭─ Parts", "╰"))
		for _, id := range ids[:1] {
			if !strings.Contains(words, id) {
				t.Errorf("%v: the section's row does not name %q whole:\n%s", size, id, g.view())
			}
		}
		// A long heading wraps whole, with no ellipsis.
		g.focus("section-2")
		panel := between(g.view(), "╭─ Parts", "╰")
		heading := false
		for _, line := range strings.Split(panel, "\n") {
			// The heading is in the part column: the text of its lines from the
			// "section" row on, up to the next column's words.
			heading = heading || strings.Contains(line, "The ticket screen's Brief")
		}
		if !heading || !strings.Contains(panel, "sub-steps open") && !strings.Contains(panel, "sub-steps") || strings.Contains(panel, "…") {
			t.Errorf("%v: the long heading is cut:\n%s", size, g.view())
		}
		for _, id := range ids {
			g.focus(id)
			if strings.Contains(between(g.view(), "╭─ Parts", "╰"), "…") {
				t.Errorf("%v: the row of slice %s is cut with an ellipsis:\n%s", size, id, g.view())
			}
			row := readWords(between(g.view(), "╭─ Parts", "╰"))
			if !strings.Contains(row, "slice "+id) {
				t.Errorf("%v: the row of slice %s has its id broken:\n%s", size, id, g.view())
			}
		}
	}
}

// TestACurrentApprovalSaysNothingOfChangedSections: a section's detail says which
// sections changed only when the approval is not current: under a current one,
// whether the status carries the column or not, it has no word on it.
func TestACurrentApprovalSaysNothingOfChangedSections(t *testing.T) {
	t.Parallel()
	for _, status := range []string{statusJIG1Brief(), strings.Replace(sinceStatus("same", "same", "same", "same"), "approval: changed", "approval: current", 1)} {
		g := briefRigWith(t, 100, 60, status, briefTextJIG1()).focus("section-2")
		d := readWords(between(g.view(), "╭─ Detail · Greeting", "╰"))
		for _, not := range []string{"not recorded", "as it was approved", "changed since"} {
			if strings.Contains(d, not) {
				t.Errorf("a current approval's section detail says %q:\n%s", not, g.view())
			}
		}
	}
}
