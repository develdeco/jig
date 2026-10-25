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
	text := pageText(g)
	for _, want := range []string{
		"✖ round 1  not clean  raised 1 ask",
		"reviewer (Codex · Gpt 5) · NOT independent: it built what it reviewed",
		"full 0000000..ccccccc · 4 files read",
		"reviewer · independence unknown · s-review", // round 2: nothing known of its model
		"delta ?..ddddddd", // its base is not known, and it has no findings file
		"fix budget 3 of 3 rounds used, reached",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("the screen lacks %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "files read\n") && strings.Count(text, "files read") != 1 {
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
	text := pageText(g)
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
	if text := pageText(g); !strings.Contains(text, "round 1  open      opened 09:11:00") || !strings.Contains(text, "waiting for a reviewer to take it") {
		t.Errorf("an open round nobody took:\n%s", text)
	}
	g.feed(jl(at(11, 10), "JIG-2", "", journal.EventGateReviewer, func(l *journal.Line) { l.Session, l.Attempt = "s-reviewer", 1 }))
	if text := pageText(g); !strings.Contains(text, "reviewer took it · s-review") || strings.Contains(text, "waiting for a reviewer") {
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
	text := pageText(g)
	findings, asks, _ := strings.Cut(text, "\nasks\n")
	for _, want := range []string{"round 1 findings", "to fix", "Close the file", "a leaked handle fails the next open", "dismissed", "Rename the helper"} {
		if !strings.Contains(findings, want) {
			t.Errorf("the findings lack %q:\n%s", want, text)
		}
	}
	if strings.Contains(findings, "Unchecked error") {
		t.Errorf("an ask that waits for a person is listed with the other findings:\n%s", text)
	}
	if !strings.Contains(asks, "a failed write would pass silently · the fix budget is reached") {
		t.Errorf("the ask lacks the reason and the why under it:\n%s", text)
	}
}
