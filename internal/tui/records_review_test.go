package tui

import (
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/events"
	"github.com/develdeco/jig/internal/journal"
)

// reviewStatus is a status of JIG-2 with the given rounds and findings tables
// (nil for none) and the asks of statusJIG2.
func reviewStatus(rounds, findings [][]string) string {
	parts := []string{
		"ticket: JIG-2", "state: green", "fix_budget: 3 of 3 rounds used, reached",
		axi.Table("slices", []string{"id", "state", "attempts", "blocked_by", "question", "goal", "workspace", "oracle", "from_gate"}, [][]string{
			{"a", "green", "1", "-", "-", "Load the config", "alpha", "unit", "-"},
		}),
		"questions: none",
		axi.Table("outstanding_asks", []string{"id", "risk", "file:line", "title", "why"}, [][]string{
			{"r1-f1", "high", "alpha/a.go:3", "Unchecked error", "the fix budget is reached"},
		}),
	}
	if rounds != nil {
		parts = append(parts, axi.Table("rounds", roundColumns, rounds))
	}
	if findings != nil {
		parts = append(parts, axi.Table("findings", findingColumns, findings))
	}
	return strings.Join(append(parts, axi.Help("Run `jig triage JIG-2 <id> keep|dismiss`")), "\n") + "\n"
}

// TestReviewScreenSaysWhoReviewedEachRoundAndHowFarTheyStood: a round names its
// reviewer with the harness and model the status gives when the screens have
// not seen the session start, says whether they were independent, and says what
// range and how many paths they read; a round the builder reviewed is not clean.
func TestReviewScreenSaysWhoReviewedEachRoundAndHowFarTheyStood(t *testing.T) {
	t.Parallel()
	g := newRig(t, "JIG-2")
	g.r.answer("status JIG-2 --store /s", reviewStatus([][]string{
		{"1", "not-independent", "s-reviewer", "codex", "gpt-5", "false", "0", "1", "0", "full", sha('0'), sha('c'), "4"},
		{"2", "findings", "s-reviewer", "-", "-", "unknown", "0", "0", "0", "delta", "-", sha('d'), "-"},
	}, nil), 0)
	g.feed(append(jig2Mid(),
		jl(at(12, 30), "JIG-2", "", journal.EventGateOpen),
		jl(at(12, 50), "JIG-2", "", journal.EventGateRound, func(l *journal.Line) { l.Attempt, l.Session = 2, "s-reviewer" }),
	)...).press("enter")
	// The rows say how each round stands; who reviewed it and how far they read
	// are the detail of the round under the cursor.
	if text := readWords(pageText(g)); !strings.Contains(text, "✗ round 1 not clean 1 ask") || !strings.Contains(text, "fix budget 3 of 3 rounds used, reached") || !strings.Contains(text, "?..ddddddd") {
		t.Errorf("the rows do not say how the rounds stand:\n%s", text)
	}
	text := readWords(pageText(g.focus("1")))
	for _, want := range []string{
		"builder reviewing its own change (Codex · Gpt 5)",
		"full 0000000..ccccccc · 4 files read",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the detail of round 1 lacks %q:\n%s", want, text)
		}
	}
	text = readWords(pageText(g.focus("2")))
	for _, want := range []string{
		"reviewer · independence unknown · s-review", // round 2: nothing known of its model
		"delta ?..ddddddd", // its base is not known, and it has no findings file
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the detail of round 2 lacks %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "files read") {
		t.Errorf("a round with no findings file claims files read:\n%s", text)
	}
}

// TestReviewScreenNamesTheModelTheRoundRanOn: the harness is the one the
// session's start line gave, which the status cannot see, and the model is the
// one the round's row names, since a reviewer subagent runs on a model of its
// own and the session started on its parent's.
func TestReviewScreenNamesTheModelTheRoundRanOn(t *testing.T) {
	t.Parallel()
	g := newRig(t, "JIG-2")
	g.r.answer("status JIG-2 --store /s", reviewStatus([][]string{
		{"1", "findings", "s-reviewer", "codex", "claude-opus-5", "true", "0", "1", "0", "full", sha('0'), sha('c'), "4"},
	}, nil), 0)
	g.feed(append([]events.Envelope{
		ll(at(10, 59), "JIG-2", journal.EventSessionStart, func(l *journal.Line) { l.Session, l.Harness, l.Model = "s-reviewer", "claude", "claude-sonnet-5" }),
	}, jig2Mid()...)...).press("enter")
	text := pageText(g.focus("1"))
	if !strings.Contains(text, "reviewer (Claude Code · Opus 5) · independent") || strings.Contains(text, "Codex") || strings.Contains(text, "Sonnet 5) · independent") {
		t.Errorf("the screen does not use the harness the ledger gave and the model the round ran on:\n%s", text)
	}
}

// TestReviewScreenNamesTheReviewerOfTheOpenRound: a round that is open has no
// closing line to name its reviewer; the line they took it with does.
func TestReviewScreenNamesTheReviewerOfTheOpenRound(t *testing.T) {
	t.Parallel()
	g := newRig(t, "JIG-2")
	g.feed(jl(at(11, 0), "JIG-2", "", journal.EventGateOpen)).press("enter")
	if g.m.Screen() != "review" {
		t.Fatalf("on %s, want the review screen", g.m.Screen())
	}
	if text := pageText(g.focus("1")); !strings.Contains(readWords(text), "round 1 open 09:11:00") || !strings.Contains(text, "waiting for a reviewer to take it") {
		t.Errorf("an open round nobody took:\n%s", text)
	}
	g.feed(jl(at(11, 10), "JIG-2", "", journal.EventGateReviewer, func(l *journal.Line) { l.Session, l.Attempt = "s-reviewer", 1 }))
	if text := pageText(g.focus("1")); !strings.Contains(text, "reviewer took it · s-review") || strings.Contains(text, "waiting for a reviewer") {
		t.Errorf("an open round a reviewer took:\n%s", text)
	}
}

// TestReviewScreenListsWhatTheRoundFoundBesideTheAsks: the findings that are not
// asks waiting for a person are listed with the reason the reviewer gave; the
// asks keep their own rows, with the reason under each.
func TestReviewScreenListsWhatTheRoundFoundBesideTheAsks(t *testing.T) {
	t.Parallel()
	g := newRig(t, "JIG-2")
	g.r.answer("status JIG-2 --store /s", reviewStatus([][]string{
		{"1", "findings", "s-reviewer", "-", "-", "true", "1", "1", "1", "full", sha('0'), sha('c'), "4"},
	}, [][]string{
		{"r1-f1", "1", "ask", "high", "alpha/a.go:3", "Unchecked error", "asked", "a failed write would pass silently", "", ""},
		{"r1-f2", "1", "fix", "medium", "alpha/a.go:12", "Close the file", "open", "a leaked handle fails the next open", "", ""},
		{"r1-f3", "1", "note", "low", "beta/b.go:2", "Rename the helper", "dismissed", "a clearer name", "", ""},
	}), 0)
	g.feed(jig2Mid()...).press("enter")
	// The cursor opens on the ask, whose detail is its finding whole and why it
	// waits.
	if ask := readWords(pageText(g)); !strings.Contains(ask, "finding high · alpha/a.go:3 · Unchecked error reason a failed write would pass silently waits because the fix budget is reached") {
		t.Errorf("the detail of the ask lacks the reason and the why:\n%s", ask)
	}
	// The round's detail lists each finding it raised with its reason.
	round := readWords(pageText(g.focus("1")))
	for _, want := range []string{
		"high alpha/a.go:3 Unchecked error → ask a failed write would pass silently",
		"medium alpha/a.go:12 Close the file → to fix a leaked handle fails the next open",
		"low beta/b.go:2 Rename the helper → dismissed a clearer name",
	} {
		if !strings.Contains(round, want) {
			t.Errorf("the detail of the round lacks %q:\n%s", want, round)
		}
	}
}

// TestReviewScreenSaysACleanRoundRaisedNotesNotFixes: the screen reads the
// counts the status gives, which are those jig routed the findings to, so a
// clean round whose findings became notes raised notes and no fix.
func TestReviewScreenSaysACleanRoundRaisedNotesNotFixes(t *testing.T) {
	t.Parallel()
	g := newRig(t, "JIG-2")
	g.r.answer("status JIG-2 --store /s", reviewStatus([][]string{
		{"1", "clean", "s-reviewer", "-", "-", "true", "0", "0", "3", "full", sha('0'), sha('c'), "4"},
	}, nil), 0)
	g.feed(jig2Mid()...).press("enter")
	text := pageText(g)
	if !strings.Contains(readWords(text), "3 notes") {
		t.Errorf("the screen does not say the round raised 3 notes:\n%s", text)
	}
}
