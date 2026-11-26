package tui

import (
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/journal"
)

// TestSliceScreenNamesTheAskerAndTheAnswererAsTheTimelineDoes: a builder that also drafted
// the brief is the builder in the Questions panel as in the timeline, and an
// answer names who gave it, a person as "you" and a session by its role.
func TestSliceScreenNamesTheAskerAndTheAnswererAsTheTimelineDoes(t *testing.T) {
	t.Parallel()
	for name, answer := range map[string]struct {
		prov, session, want string
	}{
		"a person": {"person", "", "answered by you"},
		"an agent": {"agent", "s-orch", "answered by"},
	} {
		lines := storyLines()
		for i, l := range lines {
			if l.Event == journal.EventAnswer {
				lines[i].Line.Provenance, lines[i].Line.Session = answer.prov, answer.session
			}
		}
		g := openSlice(t, answeredStatus(), lines, "c", 100, 40)
		panel := readWords(between(pageText(g), "Questions", "Timeline"))
		if !strings.Contains(panel, "asked by builder") || strings.Contains(panel, "planner") {
			t.Errorf("%s: the Questions panel does not name the asker the builder:\n%s", name, pageText(g))
		}
		if !strings.Contains(panel, answer.want) {
			t.Errorf("%s: the answered question does not say %q:\n%s", name, answer.want, pageText(g))
		}
	}
}

// TestSliceScreenDrawsAnAnswerWhole: a long answer wraps whole at 60x20 and never
// ends in an ellipsis.
func TestSliceScreenDrawsAnAnswerWhole(t *testing.T) {
	t.Parallel()
	answer := strings.Repeat("Formal, and every farewell names the visitor by first name. ", 3) + "END"
	lines := storyLines()
	for i, l := range lines {
		if l.Event == journal.EventAnswer {
			lines[i].Line.Summary = answer
		}
	}
	g := openSlice(t, answeredStatus(), lines, "c", 60, 20)
	page := readWords(pageText(g))
	if !strings.Contains(page, strings.Join(strings.Fields(answer), " ")) {
		t.Errorf("the answer is not whole:\n%s", pageText(g))
	}
	if strings.Contains(between(pageText(g), "Questions", "Timeline"), "…") {
		t.Errorf("the Questions panel ends a line in an ellipsis:\n%s", pageText(g))
	}
}

// answeredStatus is the fix round's status with the question of slice c answered.
func answeredStatus() string {
	return strings.Replace(fixRoundStatus(), "q-001,c,open", "q-001,c,answered", 1)
}
