package tui

import (
	"strconv"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/events"
	"github.com/develdeco/jig/internal/journal"
)

// storyLines is a ticket in its fix round as the journal tells it: a session
// that drafted the brief builds slice b, whose first run failed and whose second
// passed with recordings, makes calls while it holds the slice, and the question
// of slice c was asked and answered.
func storyLines() []events.Envelope {
	lines := append([]events.Envelope{jl(at(0, 0), "JIG-1", "", journal.EventApproval, with("s-builder", "", "", "", "", ""))}, jig1Records()...)
	return append(lines,
		jl(at(2, 40), "JIG-1", "c", journal.EventAnswer, with("s-person", "", "", "Formal.", "q-001", "person")),
		ll(at(1, 20), "JIG-1", journal.EventTool, func(l *journal.Line) { l.Session, l.Tool, l.Command = "s-builder", "Bash", "go build ./..." }),
		ll(at(1, 30), "JIG-1", journal.EventTool, func(l *journal.Line) { l.Session, l.Tool, l.Command = "s-builder", "Bash", "go vet ./..." }),
		jl(at(3, 0), "JIG-1", "b", journal.EventOracle, with("s-builder", sha('f'), "pass", "", "", ""), func(l *journal.Line) {
			l.Command, l.Seconds, l.Subject = "go test ./...", 40, "Say goodbye, kindly"
			l.CPUMs, l.PeakBytes, l.PeakBasis = 63200, 1536<<20, journal.PeakBasisSampled
		}),
		jl(at(3, 1), "JIG-1", "b", journal.EventRecorded, with("", sha('f'), "", "", "", ""), func(l *journal.Line) {
			l.Recordings = []journal.Recording{{File: "bye-1.webm", Size: 2048, Scenario: "goodbye", Flow: "happy", Step: 2, Caption: "the farewell"}}
		}),
		jl(at(3, 2), "JIG-1", "b", journal.EventVerified, with("", sha('f'), "", "", "", "")),
	)
}

// TestSliceScreenListsEveryCommitWithItsRuns: every commit of the slice is a row,
// none counted away, each with its runs under it in the one set of words: the
// result, the time, what the run used when it measured, and who ran it, with the
// command only when it is not the slice's oracle and the reason a run covers
// nothing.
func TestSliceScreenListsEveryCommitWithItsRuns(t *testing.T) {
	t.Parallel()
	lines := storyLines()
	for i, d := range []byte("12345") {
		lines = append(lines, jl(at(10+i, 0), "JIG-1", "b", journal.EventOracle, with("s-builder", sha(d), "fail", "boom "+string(d), "", ""), func(l *journal.Line) {
			l.Command, l.Seconds, l.Subject = "go test ./...", 30, "attempt "+string(d)
			if d == '3' {
				l.Command, l.Uncovered = "go vet ./...", journal.UncoveredHeadMoved
			}
		}))
	}
	g := openSlice(t, fixRoundStatus(), lines, "b", 100, 90)
	page := pageText(g)
	if strings.Contains(page, "earlier commit") {
		t.Errorf("the slice screen counts commits away:\n%s", page)
	}
	for _, want := range []string{"1111111", "2222222", "3333333", "4444444", "5555555", "bbbbbbb", "eeeeeee", "fffffff", "attempt 1", "Say goodbye, kindly"} {
		if !strings.Contains(page, want) {
			t.Errorf("the commits panel lacks %q:\n%s", want, page)
		}
	}
	words := readWords(between(page, "Commits", "Calls"))
	for _, want := range []string{
		"oracle passed in 40s · cpu 63.2s · peak 1.5 GiB (sampled peak) · run by builder, witnessed by jig",
		"`go vet ./...` failed in 30s · covers nothing: head moved · run by builder, witnessed by jig",
	} {
		if !strings.Contains(words, want) {
			t.Errorf("the runs do not read %q:\n%s", want, page)
		}
	}
	if strings.Contains(words, "`go test ./...`") {
		t.Errorf("a run says the slice's own oracle:\n%s", page)
	}
	if strings.Contains(words, "jig ran it") || strings.Contains(words, "the session ran it") || strings.Contains(words, "planner") {
		t.Errorf("a run is worded by who started it or by its runner's first role:\n%s", page)
	}
}

// TestSliceScreenShowsAFailedRunsSummaryWhole: a failed run shows its summary as
// the oracle line recorded it, however long, and one that recorded none says
// where to read it.
func TestSliceScreenShowsAFailedRunsSummaryWhole(t *testing.T) {
	t.Parallel()
	var long []string
	for i := 0; i < 60; i++ {
		long = append(long, "word"+strconv.Itoa(i+10))
	}
	summary := strings.Join(long, " ")
	lines := append(jig1Records(),
		jl(at(4, 0), "JIG-1", "b", journal.EventOracle, with("s-builder", sha('7'), "fail", summary, "", ""), func(l *journal.Line) { l.Seconds = 5 }),
		jl(at(5, 0), "JIG-1", "b", journal.EventOracle, with("s-builder", sha('8'), "fail", "", "", ""), func(l *journal.Line) { l.Seconds = 5 }),
	)
	g := openSlice(t, fixRoundStatus(), lines, "b", 60, 40)
	page := readWords(pageText(g))
	if !strings.Contains(strings.ReplaceAll(page, " ", ""), strings.ReplaceAll(summary, " ", "")) {
		t.Errorf("the failed run's summary is not whole:\n%s", pageText(g))
	}
	if !strings.Contains(page, "no output was recorded: the verify log on the machine that ran it has it") {
		t.Errorf("a failure with no output does not say where to read it:\n%s", pageText(g))
	}
}

// TestSliceScreenListsTheRecordingsOfACommit: the files a commit's recorded line
// names are listed under it with the scenario, caption, flow, step, name and size.
func TestSliceScreenListsTheRecordingsOfACommit(t *testing.T) {
	t.Parallel()
	g := openSlice(t, fixRoundStatus(), storyLines(), "b", 100, 60)
	if page := readWords(pageText(g)); !strings.Contains(page, "recorded: goodbye · the farewell · flow happy step 2 · bye-1.webm · 2.0 KiB") {
		t.Errorf("the recordings of the commit are not listed:\n%s", pageText(g))
	}
}

// TestSliceScreenListsTheCallsItsHolderMade: the calls the holder made while it
// held the slice are a panel of their own, one line each, the newest first; a
// slice whose holder made none has no panel.
func TestSliceScreenListsTheCallsItsHolderMade(t *testing.T) {
	t.Parallel()
	g := openSlice(t, fixRoundStatus(), storyLines(), "b", 100, 60)
	calls := between(pageText(g), "Calls", "Timeline")
	first, second := strings.Index(calls, "go vet ./..."), strings.Index(calls, "go build ./...")
	if first < 0 || second < first {
		t.Errorf("the calls are not listed newest first:\n%s", calls)
	}
	if g := openSlice(t, fixRoundStatus(), storyLines(), "a", 100, 60); strings.Contains(pageText(g), "Calls") {
		t.Errorf("a slice whose holder made no call has a Calls panel:\n%s", pageText(g))
	}
}

// TestSliceScreenTimelineIsNotCappedAndNeverSpeaksOfARequestedVerification: the
// timeline is every line of the slice, and no line of an old worker's request.
func TestSliceScreenTimelineIsNotCappedAndNeverSpeaksOfARequestedVerification(t *testing.T) {
	t.Parallel()
	var lines []events.Envelope
	for i := 0; i < 60; i++ {
		lines = append(lines, jl(at(i, 0), "JIG-1", "a", journal.EventClaim, with("s-builder", "", "", "", "", "")))
	}
	lines = append(lines, jl(at(30, 30), "JIG-1", "a", journal.EventVerifyPending, with("s-builder", sha('a'), "", "asked", "", "")))
	g := openSlice(t, fixRoundStatus(), lines, "a", 100, 40)
	page := pageText(g)
	if n := strings.Count(between(page, "Timeline\n", "\n\n"), "took it"); n != 60 {
		t.Errorf("the timeline has %d lines of the slice, want all 60:\n%s", n, page)
	}
	if strings.Contains(page, "verification requested") || strings.Contains(page, "older") {
		t.Errorf("the timeline speaks of a requested verification or of lines left out:\n%s", page)
	}
}

// TestTheSliceScreenTellsTheWholeStoryAtEverySize: a ticket in its fix round at
// five sizes: no field of the slice is cut, the builder has one name, the failed
// run's summary is whole, enter on a slice named by "after" opens it and esc
// returns, and the fix slice opens with its finding.
func TestTheSliceScreenTellsTheWholeStoryAtEverySize(t *testing.T) {
	t.Parallel()
	subject := "The slice screen lists every commit with its runs, the agent's calls and the whole timeline"
	lines := append(storyLines(), jl(at(3, 5), "JIG-1", "b", journal.EventOracle, with("s-builder", sha('9'), "pass", "", "", ""), func(l *journal.Line) { l.Subject = subject }))
	for _, size := range [][2]int{{60, 20}, {80, 24}, {100, 30}, {160, 40}, {230, 50}} {
		w, h := size[0], size[1]
		g := openSlice(t, fixRoundStatus(), lines, "b", w, h)
		for _, want := range []string{
			"Say goodbye, politely", "go test ./...", "held by builder", "builder took it", subject,
			"--- FAIL: TestB (0.00s) b_test.go:12: want 2, got 3 FAIL exit status 1",
			"recorded: goodbye · the farewell · flow happy step 2 · bye-1.webm · 2.0 KiB",
			"run by builder, witnessed by jig",
		} {
			// What is read is the window the screen draws, and what the keys bring
			// into it.
			fresh := openSlice(t, fixRoundStatus(), lines, "b", w, h)
			if found, _ := reaches(fresh, want, 60); !found {
				t.Errorf("at %dx%d the story of b lacks %q in what the screen draws:\n%s", w, h, want, fresh.view())
			}
		}
		page := readWords(pageText(g))
		if strings.Contains(page, "planner") {
			t.Errorf("at %dx%d the builder is named the planner:\n%s", w, h, pageText(g))
		}
		for _, l := range strings.Split(g.view(), "\n") {
			if width(l) > w {
				t.Errorf("at %dx%d a row is %d cells: %q", w, h, width(l), l)
			}
		}

		g = openSlice(t, fixRoundStatus(), storyLines(), "d", w, h)
		g.focus("b").press("enter")
		if g.m.top().slice != "b" {
			t.Errorf("at %dx%d enter on the slice d waits for opened %q", w, h, g.m.top().slice)
		}
		g.press("esc")
		if g.m.top().slice != "d" {
			t.Errorf("at %dx%d esc came back to %q, want d", w, h, g.m.top().slice)
		}

		g = openSlice(t, fixRoundStatus(), storyLines(), "fix-2-app", w, h)
		if page := pageText(g); !strings.HasPrefix(page, "Finding") || strings.Contains(page, "Fix these gate findings") {
			t.Errorf("at %dx%d the fix slice does not open with its finding:\n%s", w, h, page)
		}
	}
}
