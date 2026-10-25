package tui

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/events"
	"github.com/develdeco/jig/internal/journal"
)

// pageText is the whole page of the screen on top, one line each, before the
// window cuts it to the terminal.
func pageText(g *rig) string {
	var lines []string
	for _, l := range g.m.page().lines {
		lines = append(lines, l.text)
	}
	return strings.Join(lines, "\n")
}

// reads is how many times the model ran a command that starts with verb.
func (f *fakeRunner) reads(verb string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		if c.Args[0] == verb {
			n++
		}
	}
	return n
}

func TestWrapBreaksOnSpacesAndMarksWhatItCut(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		text  string
		w, n  int
		lines []string
	}{
		{"fits", "one two three", 20, 2, []string{"one two three"}},
		{"breaks on a space", "one two three four", 9, 3, []string{"one two", "three", "four"}},
		{"cuts what is over, with an ellipsis", "one two three four five", 9, 2, []string{"one two", "three…"}},
		{"a word wider than a line is cut where the line ends", "abcdefghij kl", 4, 5, []string{"abcd", "efgh", "ij", "kl"}},
		{"newlines are spaces", "one\n\ntwo", 20, 2, []string{"one two"}},
		{"no room, no lines", "one", 0, 2, nil},
		{"no text, no lines", "  ", 10, 2, nil},
	} {
		if got := wrap(tc.text, tc.w, tc.n); !reflect.DeepEqual(got, tc.lines) {
			t.Errorf("%s: wrap(%q, %d, %d) = %q, want %q", tc.name, tc.text, tc.w, tc.n, got, tc.lines)
		}
	}
}

func TestShareRowsGivesEachTextItsOwnWhenBothFit(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ a, b, room, wantA, wantB int }{
		{3, 2, 10, 3, 2}, // both fit
		{7, 5, 6, 4, 2},  // neither does: three parts of five and two
		{7, 1, 6, 5, 1},  // the short one leaves its rows to the other
		{1, 9, 6, 1, 5},
		{7, 5, 1, 1, 0}, // a single row is the body's
		{7, 5, 0, 0, 0},
		{0, 5, 3, 0, 3},
	} {
		if a, b := shareRows(tc.a, tc.b, tc.room); a != tc.wantA || b != tc.wantB {
			t.Errorf("shareRows(%d, %d, %d) = %d, %d, want %d, %d", tc.a, tc.b, tc.room, a, b, tc.wantA, tc.wantB)
		}
	}
	if got := clip([]string{"a", "b", "c"}, 2); !reflect.DeepEqual(got, []string{"a", "b …"}) {
		t.Errorf("clip = %q, want the last line to end in an ellipsis", got)
	}
}

// TestParseBriefTextTakesTheTablesAndPassesOverTheBodies: the verb prints each
// section body after its tables as an indented block of its own, and a heading
// may be quoted.
func TestParseBriefTextTakesTheTablesAndPassesOverTheBodies(t *testing.T) {
	t.Parallel()
	out := strings.Join([]string{
		axi.KV("brief", [][2]string{{"ticket", "JIG-1"}, {"title", "Greeting: copy"}, {"illustration", "yes"}}),
		axi.Table("sections", []string{"heading", "summary"}, [][]string{
			{"Goal, in short", "Say hello. Say goodbye."},
			{"Tone", "Casual"},
		}),
		"section: " + axi.Quote("Goal, in short") + "\n  Say hello. Say goodbye.\n\n  help[1]: not a block\n  odd[1]{x}: nope",
		"section: Tone\n  Casual",
		axi.Help("serve it"),
	}, "\n") + "\n"
	got := parseBriefText(out, 0)
	want := briefText{
		Title: "Greeting: copy", Illustration: true,
		Sections: []briefSection{{"Goal, in short", "Say hello. Say goodbye."}, {"Tone", "Casual"}},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("brief text = %+v, want %+v", got, want)
	}
	if s := got.summaryOf(1, "Tone"); s != "Casual" {
		t.Errorf("summary of Tone = %q", s)
	}
	if s := got.summaryOf(0, "Tone"); s != "Casual" {
		t.Errorf("summary found by heading when the position holds another = %q", s)
	}
	if s := (*briefText)(nil).summaryOf(0, "Tone"); s != "" {
		t.Errorf("summary of an unread brief = %q", s)
	}
	if refused := parseBriefText("error: ticket JIG-1 has no brief.md\ncode: VALIDATION_ERROR\n", 2); refused.Err != "ticket JIG-1 has no brief.md" {
		t.Errorf("a refusal reads as %+v", refused)
	}
	if odd := parseBriefText("panic\n", 3); !strings.Contains(odd.Err, "exited 3") {
		t.Errorf("an exit with no refusal reads as %+v", odd)
	}
}

// TestBriefTextIsReadOnceAndKeptUntilABriefLineArrives: the screen reads the
// brief when it opens, not again while the brief is as it was, and again when a
// line about the brief arrives, at once if the screen is open.
func TestBriefTextIsReadOnceAndKeptUntilABriefLineArrives(t *testing.T) {
	t.Parallel()
	g := newRig(t, "JIG-1")
	g.r.answer("status JIG-1 --store /s", statusJIG1Brief(), 0)
	g.r.answer("brief show JIG-1 --text --store /s", briefTextJIG1(), 0)
	g.feed(jig1()...)
	if n := g.r.reads("brief"); n != 0 {
		t.Fatalf("the ticket screen read the brief %d times, want none", n)
	}
	g.press("up", "enter")
	if g.m.Screen() != "brief" || g.r.reads("brief") != 1 {
		t.Fatalf("on %s after %d reads, want the brief screen after one", g.m.Screen(), g.r.reads("brief"))
	}
	if !strings.Contains(pageText(g), "Use the first name") {
		t.Errorf("the screen does not show the text it read:\n%s", pageText(g))
	}
	g.press("esc", "enter")
	if n := g.r.reads("brief"); n != 1 {
		t.Errorf("coming back to a brief that did not change read it again: %d reads", n)
	}
	g.press("r") // a refresh reads the text again when the brief is open
	if n := g.r.reads("brief"); n != 2 {
		t.Errorf("a refresh read the brief %d times in all, want 2", n)
	}

	g.press("esc")
	g.feed(jl(at(5, 0), "JIG-1", "", journal.EventRevalidate, with("s-planner", "", "", "", "", "agent")))
	if n := g.r.reads("brief"); n != 2 {
		t.Errorf("a brief line read the text %d times while the screen was closed, want it left for when the screen opens", n)
	}
	g.press("enter")
	if n := g.r.reads("brief"); n != 3 {
		t.Errorf("opening the screen after a brief line read the text %d times in all, want 3", n)
	}
	g.feed(jl(at(5, 30), "JIG-1", "", journal.EventApproval, with("s-planner", "", "", "Ada Lovelace <ada@example.invalid>", "0123456789ab", "agent")))
	if n := g.r.reads("brief"); n != 4 {
		t.Errorf("a brief line with the screen open read the text %d times in all, want 4", n)
	}
	g.feed(jl(at(6, 0), "JIG-1", "b", journal.EventClaim, with("s-builder", "", "", "", "", "")))
	if n := g.r.reads("brief"); n != 4 {
		t.Errorf("a line about a slice read the brief again: %d reads", n)
	}
}

// TestBriefScreenShowsTheHeadingsWhenTheTextIsNotThere: a store that cannot
// print the text, or a brief that has none, leaves the sections the status
// names, and says why there are no words.
func TestBriefScreenShowsTheHeadingsWhenTheTextIsNotThere(t *testing.T) {
	t.Parallel()
	g := newRig(t, "JIG-1")
	g.r.answer("status JIG-1 --store /s", statusJIG1Brief(), 0)
	g.r.answer("brief show JIG-1 --text --store /s", "error: ticket JIG-1 has no brief.md\ncode: VALIDATION_ERROR\n", 2)
	g.feed(jig1()...).press("up", "enter")
	text := pageText(g)
	for _, want := range []string{"the brief's text is not available: ticket JIG-1 has no brief.md", "Greeting", "slice c, d", "workspace app · oracle unit"} {
		if !strings.Contains(text, want) {
			t.Errorf("the screen lacks %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "Use the first name") {
		t.Errorf("the screen shows text it could not read:\n%s", text)
	}
}

func TestBriefScreenSaysWhenTheBriefHasAnIllustration(t *testing.T) {
	t.Parallel()
	g := newRig(t, "JIG-1")
	g.r.answer("status JIG-1 --store /s", statusJIG1Brief(), 0)
	g.r.answer("brief show JIG-1 --text --store /s", strings.Replace(briefTextJIG1(), "illustration: no", "illustration: yes", 1), 0)
	g.feed(jig1()...).press("up", "enter")
	if text := pageText(g); !strings.Contains(text, "it has an illustration: `jig brief show JIG-1` serves it") {
		t.Errorf("the screen does not point at the illustration:\n%s", text)
	}
}

// slicesStatus is a status of JIG-1 with the given slice rows, on the columns
// the slices table has now.
func slicesStatus(rows ...[]string) string {
	return strings.Join([]string{
		"ticket: JIG-1", "state: building",
		axi.Table("slices", []string{"id", "state", "attempts", "blocked_by", "question", "goal", "workspace", "oracle", "from_gate"}, rows),
		"questions: none",
		axi.Help("Run `jig next JIG-1`"),
	}, "\n") + "\n"
}

// TestBuildScreenListsTheLastCommitsAndCountsTheEarlier: a slice that was
// checked more often than the screen has room for lists the latest commits, each
// with its runs, and counts the ones before.
func TestBuildScreenListsTheLastCommitsAndCountsTheEarlier(t *testing.T) {
	t.Parallel()
	g := newRig(t, "JIG-1")
	g.r.answer("status JIG-1 --store /s", slicesStatus([]string{"a", "building", "5", "-", "-", "Greet", "app", "unit", "-"}), 0)
	var lines []events.Envelope
	for i, d := range []byte("12345") {
		lines = append(lines,
			jl(at(i, 0), "JIG-1", "a", journal.EventVerifyPending, with("s-builder", sha(d), "", fmt.Sprintf("attempt %d", i+1), "", "")),
			jl(at(i, 30), "JIG-1", "a", journal.EventOracle, with("", sha(d), "fail", "boom "+string(d), "", ""), func(l *journal.Line) { l.Command, l.Seconds = "go test ./...", 30 }))
	}
	g.feed(lines...).press("enter")
	text, _, _ := strings.Cut(pageText(g), "activity") // the records, not the activity under them
	if !strings.Contains(text, "+2 earlier commits") {
		t.Errorf("the screen does not count the commits it left out:\n%s", text)
	}
	if strings.Contains(text, "1111111") || strings.Contains(text, "2222222") || !strings.Contains(text, "3333333") || !strings.Contains(text, "5555555 “attempt 5”") {
		t.Errorf("the screen does not list the last three commits:\n%s", text)
	}
	// Only the output of the latest run is shown: the commit before it failed too.
	if !strings.Contains(text, "boom 5") || strings.Contains(text, "boom 4") {
		t.Errorf("the screen shows the output of a run that is not the latest:\n%s", text)
	}
	if !strings.Contains(text, "all of it is in the verify log") {
		t.Errorf("the screen does not say where the whole output is:\n%s", text)
	}
}

// TestBuildScreenSaysWhereToReadAFailureThatLeftNoOutput: an oracle line with no
// output names no tail, and the screen says where to look; a slice that went
// green after failing shows no output at all.
func TestBuildScreenSaysWhereToReadAFailureThatLeftNoOutput(t *testing.T) {
	t.Parallel()
	g := newRig(t, "JIG-1")
	g.r.answer("status JIG-1 --store /s", slicesStatus([]string{"a", "building", "1", "-", "-", "Greet", "app", "unit", "-"}), 0)
	g.feed(
		jl(at(0, 1), "JIG-1", "a", journal.EventVerifyPending, with("s-builder", sha('a'), "", "first", "", "")),
		jl(at(0, 20), "JIG-1", "a", journal.EventOracle, with("", sha('a'), "fail", "", "", "")),
	).press("enter")
	if text := pageText(g); !strings.Contains(text, "no output was recorded: the verify log on the machine that ran it") {
		t.Errorf("the screen does not say where to read a failure with no output:\n%s", text)
	}

	g.feed(
		jl(at(1, 1), "JIG-1", "a", journal.EventVerifyPending, with("s-builder", sha('b'), "", "second", "", "")),
		jl(at(1, 20), "JIG-1", "a", journal.EventOracle, with("", sha('b'), "pass", "", "", "")),
		jl(at(1, 21), "JIG-1", "a", journal.EventVerified, with("", sha('b'), "", "", "", "")),
	)
	if text := pageText(g); strings.Contains(text, "no output was recorded") || strings.Contains(text, "verify log") {
		t.Errorf("a slice that went green still shows the output of a failure:\n%s", text)
	}
}

// TestBuildScreenShowsAVerificationThatCouldNotRun: a verify-error line is the
// last word on its commit.
func TestBuildScreenShowsAVerificationThatCouldNotRun(t *testing.T) {
	t.Parallel()
	g := newRig(t, "JIG-1")
	g.r.answer("status JIG-1 --store /s", slicesStatus([]string{"a", "building", "1", "-", "-", "Greet", "app", "unit", "-"}), 0)
	g.feed(
		jl(at(0, 1), "JIG-1", "a", journal.EventVerifyPending, with("s-builder", sha('a'), "", "first", "", "")),
		jl(at(0, 20), "JIG-1", "a", journal.EventVerifyError, with("", sha('a'), "", "no worktree", "", "")),
	).press("enter")
	if text := pageText(g); !strings.Contains(text, "could not be verified: no worktree") || !strings.Contains(text, "✖ aaaaaaa “first”") {
		t.Errorf("the screen does not show the commit that could not be verified:\n%s", text)
	}
}

// TestBuildScreenGivesTheFacts: the slice row ends in the workspace and oracle
// when they fit, and a fix slice names its round.
func TestBuildScreenGivesTheFacts(t *testing.T) {
	t.Parallel()
	g := newRig(t, "JIG-1")
	g.r.answer("status JIG-1 --store /s", slicesStatus(
		[]string{"a", "green", "1", "-", "-", "Greet", "app", "unit", "-"},
		[]string{"fix-1", "queued", "0", "a", "-", "Check the error that the long goal of this fix slice goes on about and on", "app", "go test ./...", "1"},
	), 0)
	g.feed(jl(at(0, 1), "JIG-1", "a", journal.EventClaim, with("s-builder", "", "", "", "", ""))).press("enter")
	text := pageText(g)
	if !strings.Contains(text, "Greet  workspace app · oracle unit") {
		t.Errorf("the short row does not end in its facts:\n%s", text)
	}
	if !strings.Contains(text, "\n         workspace app · oracle go test ./... · fix slice of round 1 · after a") {
		t.Errorf("the facts a long row has no room for are not on the line below it:\n%s", text)
	}
}
