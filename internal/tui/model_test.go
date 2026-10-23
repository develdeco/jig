package tui

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/events"
	"github.com/develdeco/jig/internal/journal"
)

// screens walks the nav stack of g.
func screens(g *rig) []string {
	var out []string
	for _, e := range g.m.nav {
		out = append(out, e.screen.String())
	}
	return out
}

func TestOpenAndBackWalkTheFiveScreens(t *testing.T) {
	t.Parallel()
	g := mainRig(t)
	want := func(screen string, stack ...string) {
		t.Helper()
		if g.m.Screen() != screen || !reflect.DeepEqual(screens(g), stack) {
			t.Fatalf("on %s with the stack %v, want %s with %v", g.m.Screen(), screens(g), screen, stack)
		}
	}
	want("main", "main")

	g.press("enter")
	want("ticket", "main", "ticket")
	if got := g.m.top().ticket; got != "JIG-1" {
		t.Fatalf("opened %s, want the first ticket JIG-1", got)
	}
	g.press("enter") // the build row
	want("build", "main", "ticket", "build")
	g.press("down", "enter") // slice b
	want("slice", "main", "ticket", "build", "slice")
	if got := g.m.top().slice; got != "b" {
		t.Fatalf("opened slice %q, want b", got)
	}
	g.press("esc", "esc")
	want("ticket", "main", "ticket")
	g.press("down", "enter") // the review row
	want("review", "main", "ticket", "review")
	g.press("esc", "down", "enter") // the publish row also opens the review
	want("review", "main", "ticket", "review")
	g.press("esc", "esc")
	want("main", "main")
	g.press("esc") // nowhere further back
	want("main", "main")
}

func TestATicketGivenOpensItsScreenWithTheProjectBehindIt(t *testing.T) {
	t.Parallel()
	g := newRig(t, "JIG-2")
	g.feed(jig1()...)
	if got := screens(g); !reflect.DeepEqual(got, []string{"main", "ticket"}) {
		t.Fatalf("stack = %v, want the main screen under the ticket", got)
	}
	g.press("esc")
	if g.m.Screen() != "main" || !strings.Contains(g.view(), "JIG-1") {
		t.Errorf("back from the ticket: on %s\n%s", g.m.Screen(), g.view())
	}
}

func TestOpeningAQuestionGoesToItsSlice(t *testing.T) {
	t.Parallel()
	g := newRig(t, "JIG-1")
	g.feed(jig1()...).press("down", "down", "down") // the question under "needs a person"
	g.press("enter")
	if g.m.Screen() != "slice" || g.m.top().slice != "c" {
		t.Fatalf("on %s %q, want slice c", g.m.Screen(), g.m.top().slice)
	}
}

func TestTheCursorStaysOnItsScreen(t *testing.T) {
	t.Parallel()
	g := mainRig(t)
	g.press("down", "down", "down", "down", "down")
	if got := g.m.top().sel; got != 2 {
		t.Errorf("cursor = %d after moving past the last of 3 tickets, want 2", got)
	}
	g.press("up", "up", "up", "up")
	if got := g.m.top().sel; got != 0 {
		t.Errorf("cursor = %d after moving past the first, want 0", got)
	}
	g.press("down", "enter", "esc")
	if got := g.m.top().sel; got != 1 {
		t.Errorf("cursor = %d on the main screen after going in and back, want it where it was (1)", got)
	}
}

func TestAnswerRunsTheVerbAsAPerson(t *testing.T) {
	t.Parallel()
	g := newRig(t, "JIG-1")
	g.feed(jig1()...).press("a")
	if g.m.input == nil || g.m.input.qid != "q-001" {
		t.Fatalf("a opened %+v, want the answer to q-001, the only open question", g.m.input)
	}
	if !strings.Contains(g.view(), "Formal or casual?") {
		t.Errorf("the prompt does not show the question:\n%s", g.view())
	}
	g.press("Casual,", "space", "please.")
	g.press("enter")

	want := []string{"answer --as person --store /s JIG-1 q-001 -- Casual, please."}
	if got := g.r.lines(); !reflect.DeepEqual(got, want) {
		t.Fatalf("commands run = %q, want %q", got, want)
	}
	if g.m.input != nil {
		t.Errorf("the prompt is still open after sending")
	}
	if v := g.view(); !strings.Contains(v, "jig answer --as person JIG-1 q-001 -- Casual, please.") {
		t.Errorf("the screen does not show the command that ran (without the store flag):\n%s", v)
	}
}

func TestAnswerAcceptsTextThatStartsWithADash(t *testing.T) {
	t.Parallel()
	g := newRig(t, "JIG-1")
	g.feed(jig1()...).press("a", "-v", "space", "--force", "enter")
	want := []string{"answer --as person --store /s JIG-1 q-001 -- -v --force"}
	if got := g.r.lines(); !reflect.DeepEqual(got, want) {
		t.Fatalf("commands run = %q, want %q: text after -- is never a flag", got, want)
	}
}

func TestAnswerCanBeAbandonedOrLeftEmpty(t *testing.T) {
	t.Parallel()
	g := newRig(t, "JIG-1")
	g.feed(jig1()...).press("a", "enter")
	if g.m.input == nil {
		t.Fatalf("enter on an empty answer closed the prompt")
	}
	g.press("x", "backspace", "enter", "space", "enter")
	if g.m.input == nil || len(g.r.lines()) != 0 {
		t.Fatalf("an empty or blank answer was sent: %q", g.r.lines())
	}
	g.press("esc")
	if g.m.input != nil || len(g.r.lines()) != 0 {
		t.Errorf("esc did not abandon the answer: input %+v, commands %q", g.m.input, g.r.lines())
	}
	// Keys typed in the prompt are text: q does not quit.
	_, cmd := g.m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'a'}})
	g.settle(cmd)
	_, cmd = g.m.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'q'}})
	if cmd != nil {
		t.Errorf("q in the prompt returned %T, want it typed", cmd())
	}
	if got := string(g.m.input.value); got != "q" {
		t.Errorf("typed %q, want q", got)
	}
}

func TestAnswerAsksWhichQuestionWhenSeveralAreOpen(t *testing.T) {
	t.Parallel()
	g := newRig(t, "JIG-1")
	g.r.answer("status JIG-1 --store /s", strings.Join([]string{
		"ticket: JIG-1", "state: parked",
		axi.Table("slices", []string{"id", "state", "attempts", "blocked_by", "question"}, [][]string{
			{"c", "needs-input", "1", "-", "q-001"}, {"d", "needs-input", "1", "-", "q-002"},
		}),
		axi.Table("questions", []string{"id", "slice", "status"}, [][]string{{"q-001", "c", "open"}, {"q-002", "d", "open"}}),
	}, "\n")+"\n", 0)
	g.feed(append(jig1(), jl(at(3, 0), "JIG-1", "d", journal.EventQuestion, with("s-builder", "", "", "Tabs or spaces?", "q-002", "agent")))...)

	g.press("a")
	if g.m.input != nil || !strings.Contains(g.m.notice, "several questions") {
		t.Fatalf("a with two questions open and the cursor on no question: input %+v, notice %q", g.m.input, g.m.notice)
	}
	g.press("down", "down", "down", "down") // the second question
	if ref := g.m.selected(); ref == nil || ref.id != "q-002" {
		t.Fatalf("cursor is on %+v, want q-002", ref)
	}
	g.press("a", "Tabs", "enter")
	want := []string{"answer --as person --store /s JIG-1 q-002 -- Tabs"}
	if got := g.r.lines(); !reflect.DeepEqual(got, want) {
		t.Errorf("commands run = %q, want %q", got, want)
	}
}

func TestAnswerRefusesAQuestionThatIsAnswered(t *testing.T) {
	t.Parallel()
	g := newRig(t, "JIG-1")
	g.r.answer("status JIG-1 --store /s", strings.Replace(statusJIG1(), "q-001,c,open", "q-001,c,answered", 1), 0)
	g.feed(append(jig1(), jl(at(3, 0), "JIG-1", "c", journal.EventAnswer, with("s-person", "", "", "Casual", "q-001", "person")))...)
	g.press("a")
	if g.m.input != nil || !strings.Contains(g.m.notice, "no question is waiting") {
		t.Errorf("a with every question answered: input %+v, notice %q", g.m.input, g.m.notice)
	}
}

func TestTriageDecidesTheAskUnderTheCursor(t *testing.T) {
	t.Parallel()
	g := newRig(t, "JIG-2")
	g.feed(jig2Mid()...).press("down", "enter") // the review screen
	g.press("down", "d")                        // r1-f2
	g.press("up", "k")                          // r1-f1
	want := []string{
		"triage JIG-2 r1-f2 dismiss --store /s",
		"triage JIG-2 r1-f1 keep --store /s",
	}
	if got := g.r.lines(); !reflect.DeepEqual(got, want) {
		t.Fatalf("commands run = %q, want %q", got, want)
	}
}

func TestTriageOfTheOnlyUndecidedAskNeedsNoCursor(t *testing.T) {
	t.Parallel()
	g := newRig(t, "JIG-2")
	g.feed(jig2Mid()...)
	g.press("d")
	if g.m.notice == "" || len(g.r.lines()) != 0 {
		t.Fatalf("d with two asks undecided and the cursor on none ran %q (notice %q), want it to ask which", g.r.lines(), g.m.notice)
	}
	// r1-f1 is decided by a person; r1-f2 is the one left.
	g.feed(jl(at(12, 0), "JIG-2", "", journal.EventTriage, with("s-person", "", "dismiss", "", "r1-f1", "person")))
	g.press("k")
	want := []string{"triage JIG-2 r1-f2 keep --store /s"}
	if got := g.r.lines(); !reflect.DeepEqual(got, want) {
		t.Errorf("commands run = %q, want %q", got, want)
	}
	if v := g.view(); !strings.Contains(v, "dismiss (person)") {
		t.Errorf("the decision recorded for r1-f1 is not on the screen:\n%s", v)
	}
}

func TestGateRunsOnTheOpenTicket(t *testing.T) {
	t.Parallel()
	g := newRig(t, "JIG-2")
	g.feed(jig2Mid()...).press("g")
	want := []string{"gate JIG-2 --store /s"}
	if got := g.r.lines(); !reflect.DeepEqual(got, want) {
		t.Fatalf("commands run = %q, want %q", got, want)
	}
}

func TestGateOnTheMainScreenIsForTheTicketUnderTheCursor(t *testing.T) {
	t.Parallel()
	g := mainRig(t)
	g.press("down", "g")
	want := []string{"gate JIG-2 --store /s"}
	if got := g.r.lines(); !reflect.DeepEqual(got, want) {
		t.Fatalf("commands run = %q, want %q", got, want)
	}
}

func TestPublishApprovesThenPushesAndOnlyOnYes(t *testing.T) {
	t.Parallel()
	g := newRig(t, "JIG-2")
	g.feed(jig2Mid()...)
	g.press("p")
	if g.m.ask == nil || !strings.Contains(g.view(), "publish JIG-2") {
		t.Fatalf("p did not ask first:\n%s", g.view())
	}
	if len(g.r.lines()) != 0 {
		t.Fatalf("p ran %q before it was confirmed", g.r.lines())
	}
	g.press("n")
	if g.m.ask != nil || len(g.r.lines()) != 0 {
		t.Fatalf("n: ask %+v, commands %q, want the question gone and nothing run", g.m.ask, g.r.lines())
	}
	g.press("p", "y")
	want := []string{"publish JIG-2 --approve --store /s", "publish JIG-2 --yes --store /s"}
	if got := g.r.lines(); !reflect.DeepEqual(got, want) {
		t.Fatalf("commands run = %q, want the approval and then the publish %q", got, want)
	}
	g.press("p", "enter")
	if got := len(g.r.lines()); got != 4 {
		t.Errorf("enter did not confirm: %d commands, want 4", got)
	}
}

func TestPublishStopsWhenTheApprovalIsRefused(t *testing.T) {
	t.Parallel()
	g := newRig(t, "JIG-2")
	g.r.answer("publish JIG-2 --approve --store /s", "error: the latest round is not clean\ncode: PUBLISH_NOT_CLEAN\n", 1)
	g.feed(jig2Mid()...).press("p", "y")
	want := []string{"publish JIG-2 --approve --store /s"}
	if got := g.r.lines(); !reflect.DeepEqual(got, want) {
		t.Fatalf("commands run = %q, want only the refused approval: nothing is pushed on a refusal", got)
	}
	v := g.view()
	for _, w := range []string{"✖ jig publish JIG-2 --approve", "code: PUBLISH_NOT_CLEAN"} {
		if !strings.Contains(v, w) {
			t.Errorf("the screen lacks %q:\n%s", w, v)
		}
	}
}

func TestAnEventForTheOpenTicketReadsItsStatusAgain(t *testing.T) {
	t.Parallel()
	g := newRig(t, "JIG-1")
	g.feed(jig1()...)
	statuses := func(ticket string) int {
		n := 0
		for _, c := range g.r.calls {
			if c.Line == "status "+ticket+" --store /s" {
				n++
			}
		}
		return n
	}
	before := statuses("JIG-1")
	if before == 0 {
		t.Fatalf("opening the ticket read no status")
	}
	g.send(eventsMsg{jl(at(3, 0), "JIG-1", "b", journal.EventVerified, with("", sha('b'), "", "", "", ""))})
	if got := statuses("JIG-1"); got != before+1 {
		t.Errorf("status reads of the open ticket: %d after an event for it, want %d", got, before+1)
	}
	g.send(eventsMsg{jl(at(3, 1), "JIG-9", "a", journal.EventClaim)})
	if got := statuses("JIG-1"); got != before+1 {
		t.Errorf("an event for another ticket read the open one again (%d reads)", got)
	}
	if got := statuses("JIG-9"); got != 0 {
		t.Errorf("an event for a ticket nobody has open read its status (%d reads)", got)
	}
}

func TestTheMainScreenReadsNoStatus(t *testing.T) {
	t.Parallel()
	g := mainRig(t)
	for _, c := range g.r.calls {
		if c.Args[0] == "status" {
			t.Errorf("the main screen ran %q", c.Line)
		}
	}
	if !strings.Contains(g.view(), "Greeting copy") {
		t.Errorf("the roster's titles are not on the main screen:\n%s", g.view())
	}
}

func TestAStatusThatIsRefusedIsANotice(t *testing.T) {
	t.Parallel()
	g := newRig(t, "JIG-1")
	g.r.answer("status JIG-1 --store /s", "error: ticket JIG-1 not found in the store\ncode: VALIDATION_ERROR\n", 2)
	g.feed(jig1()...)
	if !strings.Contains(g.m.notice, "not found in the store") {
		t.Errorf("notice = %q, want the refusal", g.m.notice)
	}
	if !strings.Contains(g.view(), "JIG-1") {
		t.Errorf("the screen is gone after a refused status:\n%s", g.view())
	}
}

func TestQuitKeys(t *testing.T) {
	t.Parallel()
	for _, k := range []tea.KeyMsg{{Type: tea.KeyRunes, Runes: []rune{'q'}}, {Type: tea.KeyCtrlC}} {
		g := mainRig(t)
		_, cmd := g.m.Update(k)
		if cmd == nil {
			t.Fatalf("%v returned no command", k)
		}
		if _, ok := cmd().(tea.QuitMsg); !ok {
			t.Errorf("%v did not quit", k)
		}
	}
}

func TestResizeRedrawsWithinTheNewSize(t *testing.T) {
	t.Parallel()
	g := mainRig(t)
	g.send(tea.WindowSizeMsg{Width: 40, Height: 10})
	lines := strings.Split(g.view(), "\n")
	if len(lines) != 10 {
		t.Errorf("drew %d lines for a 10 line screen:\n%s", len(lines), g.view())
	}
	for _, l := range lines {
		if width(l) > 40 {
			t.Errorf("line %q is %d cells wide on a 40 cell screen", l, width(l))
		}
	}
}

func TestACursorBelowTheWindowScrollsIntoView(t *testing.T) {
	t.Parallel()
	g := newRig(t, "")
	var envs []events.Envelope
	for i := 0; i < 30; i++ {
		envs = append(envs, jl(at(5, i), fmt.Sprintf("JIG-%d", 100+i), "a", journal.EventClaim, with("s", "", "", "", "", "")))
	}
	g.send(tea.WindowSizeMsg{Width: 80, Height: 12})
	g.feed(envs...)
	if v := g.view(); !strings.Contains(v, "JIG-100") || strings.Contains(v, "JIG-129") {
		t.Fatalf("the top of 30 tickets in 12 lines:\n%s", v)
	}
	g.press("end")
	v := g.view()
	if !strings.Contains(v, "▸ ◐ JIG-129") || strings.Contains(v, "JIG-100") {
		t.Errorf("the cursor is not in view after end:\n%s", v)
	}
	if lines := strings.Count(v, "\n") + 1; lines != 12 {
		t.Errorf("drew %d lines in a 12 line window", lines)
	}
}
