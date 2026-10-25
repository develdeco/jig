package tui

import (
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/events"
	"github.com/develdeco/jig/internal/journal"
)

// TestEveryKindOfLineNamesTheSubstepItAdvances: an activity line carries the
// label of the sub-step it advances, in the words of the sub-steps block, and
// says what happened in words: no kind of line falls to "event outcome".
func TestEveryKindOfLineNamesTheSubstepItAdvances(t *testing.T) {
	t.Parallel()
	g := newRig(t, "JIG-6")
	tk := ticketOf("JIG-6")
	g.m.w.tickets["JIG-6"] = tk
	labels := subLabels{"slice:a": "slice a", "round:2": "round 2", "slice:fix-1": "fix slice fix-1 (round 1)"}

	at2 := func(mod func(*journal.Line)) func(*journal.Line) {
		return func(l *journal.Line) { l.Attempt = 2; mod(l) }
	}
	none := func(*journal.Line) {}
	for _, tc := range []struct {
		line  events.Envelope
		label string
		says  string
	}{
		{jl(at(1, 0), "JIG-6", "a", journal.EventClaim, with("s-builder", "", "", "", "", "")), "slice a", "builder took it"},
		{jl(at(1, 1), "JIG-6", "a", journal.EventRelease, with("s-builder", "", "", "", "", "")), "slice a", "builder let go of it"},
		{jl(at(1, 2), "JIG-6", "a", journal.EventDispatch), "slice a", "jig started a builder"},
		{jl(at(1, 3), "JIG-6", "a", journal.EventVerifyPending, with("s-builder", sha('a'), "", "Add the cart", "", "")), "slice a", "verifying aaaaaaa"},
		{jl(at(1, 4), "JIG-6", "a", journal.EventOracle, with("", sha('a'), "pass", "", "", ""), func(l *journal.Line) { l.Command, l.Seconds = "go test ./cart", 3 }), "slice a", "`go test ./cart` passed in 3s"},
		{jl(at(1, 5), "JIG-6", "a", journal.EventVerified, with("", sha('a'), "", "", "", "")), "slice a", "verified aaaaaaa"},
		{jl(at(1, 6), "JIG-6", "a", journal.EventVerifyError, with("", "", "", "no worker", "", "")), "slice a", "could not be verified: no worker"},
		{jl(at(1, 7), "JIG-6", "a", journal.EventQuestion, with("s-builder", "", "", "Formal?", "q-001", "agent")), "slice a", "builder asks: Formal?"},
		{jl(at(1, 8), "JIG-6", "a", journal.EventAnswer, with("s-person", "", "", "Casual.", "q-001", "person")), "slice a", "you answered the question: Casual."},
		{jl(at(1, 9), "JIG-6", "a", journal.EventStall, with("", "", "", "oracle kept failing", "", "")), "slice a", "stalled: oracle kept failing"},
		{jl(at(1, 10), "JIG-6", "a", journal.EventRequeue), "slice a", "back in the queue"},
		{jl(at(1, 11), "JIG-6", "a", journal.EventNote, with("", "", "", "careful here", "", "")), "slice a", "note: careful here"},
		{jl(at(1, 12), "JIG-6", "a", journal.EventResult, with("s-builder", "", "green", "", "", ""), func(l *journal.Line) { l.Attempt = 1 }), "slice a", "builder's attempt ended green (attempt 1)"},
		{jl(at(1, 13), "JIG-6", "a", journal.EventRecorded, with("", sha('a'), "", "", "", ""), func(l *journal.Line) { l.Recordings = []journal.Recording{{File: "x.png"}} }), "slice a", "recorded 1 file of the build's scenarios at aaaaaaa"},
		{jl(at(1, 14), "JIG-6", "", journal.EventEnvUp), "build", "its environment came up"},
		{jl(at(1, 15), "JIG-6", "", journal.EventDriver, with("", "", "", "ran 2 slices", "", "")), "build", "jig's driver: ran 2 slices"},
		{jl(at(1, 16), "JIG-6", "fix-1", journal.EventClaim, with("s-builder", "", "", "", "", "")), "fix slice fix-1 (round 1)", "builder took it"},
		{jl(at(2, 0), "JIG-6", "", journal.EventApproval, with("s-planner", "", "", "Ada <a@example.invalid>", "", "person")), "brief approved", "by Ada"},
		{jl(at(2, 1), "JIG-6", "", journal.EventRevalidate), "slices written", "the brief was checked again"},
		{jl(at(3, 0), "JIG-6", "", journal.EventGateOpen, at2(none)), "round 2", "opened"},
		{jl(at(3, 1), "JIG-6", "", journal.EventGateReviewer, at2(with("s-reviewer", "", "", "", "", ""))), "round 2", "reviewer took it"},
		{jl(at(3, 2), "JIG-6", "", journal.EventGateRound, at2(with("s-reviewer", "", "", "", "", ""))), "round 2", "reviewer finished with findings"},
		{jl(at(3, 3), "JIG-6", "", journal.EventGateClean, at2(with("s-reviewer", "", "", "", "", ""))), "round 2", "reviewer finished: clean"},
		{jl(at(3, 4), "JIG-6", "", journal.EventTriage, with("s-person", "", "dismiss", "not a bug", "r2-f1", "person")), "asks", "you dismissed r2-f1: not a bug"},
		{jl(at(3, 5), "JIG-6", "fix-1", journal.EventFixSlice), "fixes", "sent back fix-1"},
		{jl(at(3, 6), "JIG-6", "", journal.EventRoute, with("", "", "", "1 fix, 1 ask", "", "")), "review", "the round's findings were routed: 1 fix, 1 ask"},
		{jl(at(3, 7), "JIG-6", "", journal.EventAdopt, func(l *journal.Line) { l.Branch = "feature/x" }), "review", "the ticket took over feature/x"},
		{jl(at(4, 0), "JIG-6", "", journal.EventPrBody, func(l *journal.Line) { l.Commit = sha('c') }), "body", "written for ccccccc"},
		{jl(at(4, 1), "JIG-6", "", journal.EventPublishPicks, with("", sha('c'), "refused", "no files", "", "")), "body", "demo refused: no files"},
		{jl(at(4, 2), "JIG-6", "", journal.EventPublishApproved, with("", sha('c'), "", "", "", "person")), "approval", "you approved ccccccc"},
		{jl(at(4, 3), "JIG-6", "", journal.EventPr, func(l *journal.Line) { l.Outcome, l.URL = "opened", "https://example.test/pull/1" }), "pull request", "opened https://example.test/pull/1"},
		{jl(at(4, 4), "JIG-6", "", journal.EventSquash, with("", sha('d'), "", "", "", "")), "pull request", "the branch was squashed ddddddd"},
		{jl(at(4, 5), "JIG-6", "", journal.EventChangelog), "changelog", "written"},
		{jl(at(4, 6), "JIG-6", "", journal.EventMemorize, with("", "", "", "use the cart api", "", "")), "changelog", "what the ticket taught was memorized: use the cart api"},
		{jl(at(4, 7), "JIG-6", "", journal.EventPublishDone), "published", "finished"},
	} {
		l := tc.line
		tk.lines = append(tk.lines, l)
		if got := tk.labelOf(labels, l); got != tc.label {
			t.Errorf("%s names %q, want %q", l.Event, got, tc.label)
		}
		got := g.m.say(tk, l)
		if !strings.Contains(got, tc.says) {
			t.Errorf("%s says %q, want it to say %q", l.Event, got, tc.says)
		}
		if raw := strings.TrimSpace(l.Event + " " + firstOf(l.Summary, l.Outcome)); got == raw {
			t.Errorf("%s says %q, the words of a kind of line nobody wrote words for", l.Event, got)
		}
		line := g.m.activityLine(tk, labels, l)
		if want := tc.label + " · "; !strings.Contains(line, want) {
			t.Errorf("%s as an activity line is %q, want it to carry %q", l.Event, line, want)
		}
	}
}

// TestALineWithNoAttemptNamesTheRoundByItsPlace: a gate line that carries no
// attempt belongs to the round after the ones that closed before it.
func TestALineWithNoAttemptNamesTheRoundByItsPlace(t *testing.T) {
	t.Parallel()
	tk := ticketOf("JIG-2", jig2()...)
	var got []string
	for _, l := range tk.lines {
		if strings.HasPrefix(l.Event, "gate-") {
			got = append(got, l.Event+":"+tk.subKey(l))
		}
	}
	want := "gate-open:round:1 gate-round:round:1 gate-open:round:2 gate-clean:round:2"
	if strings.Join(got, " ") != want {
		t.Errorf("gate lines name %v, want %s", got, want)
	}
}

// TestActivityLabelsAreTheWordsOfTheSubstepsBlock: where a sub-step has a
// label in the block, the line that advances it carries the very same words,
// and the words an activity line falls back on when the block no longer lists
// the sub-step are the ones internal/phase gives it.
func TestActivityLabelsAreTheWordsOfTheSubstepsBlock(t *testing.T) {
	t.Parallel()
	fixSlice := strings.Join([]string{"ticket: JIG-6", "state: building",
		axi.Table("slices", []string{"id", "state", "attempts", "blocked_by", "question", "goal"}, [][]string{
			{"a", "green", "1", "-", "-", "Do a"}, {"fix-1", "queued", "0", "-", "-", "Fix it"},
		}), "questions: none",
		axi.KV("brief", [][2]string{{"approval", "current"}, {"approved_by", "Ada"}, {"provenance", "person"}}),
		axi.Table("brief_sections", []string{"heading", "slices"}, [][]string{{"Goal", "a"}}),
		axi.Table("outstanding_asks", []string{"id", "risk", "file:line", "title", "why"}, [][]string{{"r1-f1", "low", "a.go:1", "Name", "-"}}),
	}, "\n") + "\n"
	fixLines := []events.Envelope{
		jl(at(30, 0), "JIG-6", "a", journal.EventClaim, with("s-builder", "", "", "", "", "")),
		jl(at(30, 10), "JIG-6", "a", journal.EventVerified, with("", sha('a'), "", "", "", "")),
		jl(at(31, 0), "JIG-6", "", journal.EventGateOpen),
		jl(at(31, 30), "JIG-6", "", journal.EventGateRound, func(l *journal.Line) { l.Attempt = 1 }),
		jl(at(31, 31), "JIG-6", "fix-1", journal.EventFixSlice, func(l *journal.Line) { l.Attempt = 1 }),
	}
	found := map[string]string{} // key to the label some fixture's block has for it
	for _, tc := range []struct {
		ticket, status string
		lines          []events.Envelope
	}{
		{"JIG-6", fixSlice, fixLines},
		{"JIG-2", statusJIG2Done(), jig2()},
		{"JIG-2", statusJIG2(), jig2Mid()},
		{"SHOP-1", statusSHOP1(), shop1()},
	} {
		g := newRig(t, tc.ticket)
		g.r.answer("status "+tc.ticket+" --store /s", tc.status, 0)
		g.feed(tc.lines...)
		tk := g.m.w.tickets[tc.ticket]
		labels := g.m.subLabelsOf(tk)
		for key, label := range labels {
			found[key] = label
		}
		for _, l := range tk.lines {
			key := tk.subKey(l)
			if label, listed := labels[key]; key != "" && listed {
				if got := tk.labelOf(labels, l); got != label {
					t.Errorf("%s line names %q, the block says %q", l.Event, got, label)
				}
			}
		}
	}
	for key, want := range fallbackLabels {
		if got, ok := found[key]; !ok {
			t.Errorf("no fixture lists the sub-step %q, so its fallback label %q is untested", key, want)
		} else if got != want {
			t.Errorf("the fallback label of %q is %q, the block says %q", key, want, got)
		}
	}
	if found["slice:fix-1"] != "fix slice fix-1 (round 1)" {
		t.Errorf("the fix slice's label = %q, want it to say the round that sent it back", found["slice:fix-1"])
	}
}
