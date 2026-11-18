package tui

import "testing"

// TestAnOldSlotWaitLineIsShownWithoutAnError: a journal written when runs
// waited for a suite slot holds slot-wait and slot-taken lines. The activity
// list and the ticket's state read them as any event the screens have no
// special case for: they draw a row, never a panic or an empty one.
func TestAnOldSlotWaitLineIsShownWithoutAnError(t *testing.T) {
	t.Parallel()
	g := newRig(t, "JIG-6")
	tk := ticketOf("JIG-6")
	g.m.w.tickets["JIG-6"] = tk
	for _, ev := range []string{"slot-wait", "slot-taken"} {
		for _, slice := range []string{"a", ""} {
			l := jl(at(3, 1), "JIG-6", slice, ev, with("", "", "", "waiting for the suite slot", "", ""))
			tk.lines = append(tk.lines, l)
			if got := describe(l); got == "" {
				t.Errorf("describe(%s, slice %q) is empty", ev, slice)
			}
			if got := g.m.say(tk, l); got == "" {
				t.Errorf("say(%s, slice %q) is empty", ev, slice)
			}
			if eventState(l) == "" {
				t.Errorf("eventState(%s, slice %q) is empty", ev, slice)
			}
			_ = stepOfLine(l)
			_ = tk.labelOf(subLabels{}, l)
		}
	}
	_ = tk.stepLines(stepBuild)
	_ = tk.stepLines(stepReview)
}
