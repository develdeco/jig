package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/journal"
)

// TestTheLogTagsEachCallWithWhatItWasFor: one session plans, builds, reviews
// through a subagent and publishes. A call it makes between two sub-steps is
// tagged with the step it led to (the build skill read after the approval is
// the build's); the call that closed a round, which the harness records just
// after the verdict it wrote, is the round's; a call while the pull request is
// opened is the pull request's. The pull request's holder is named as the
// builder it was, though it planned first.
func TestTheLogTagsEachCallWithWhatItWasFor(t *testing.T) {
	t.Parallel()
	status := strings.Join([]string{"ticket: SHOP-1", "state: green",
		axi.Table("slices", []string{"id", "state", "attempts", "blocked_by", "question", "goal"}, [][]string{
			{"round", "green", "1", "-", "-", "Round to the cent"},
		}), "questions: none"}, "\n") + "\n"
	g := newRig(t, "SHOP-1")
	g.r.answer("status SHOP-1 --store /s", status, 0)
	g.send(tea.WindowSizeMsg{Width: 100, Height: 50})
	g.feed(
		ll(at(0, 0), "", journal.EventSessionStart, func(l *journal.Line) { l.Session, l.Harness, l.Model = "s-b", "claude", "claude-sonnet-5" }),
		ll(at(1, 0), "SHOP-1", journal.EventSessionTouch, func(l *journal.Line) { l.Session = "s-b" }),
		tool(at(1, 10), "s-b", "jig skill intake", said("Read the intake skill")),
		jl(at(2, 0), "SHOP-1", "", journal.EventApproval, with("s-b", "", "", "Dana <dana@example.invalid>", "0123456789ab", "agent")),
		tool(at(2, 10), "s-b", "jig skill build", said("Read the build skill")),
		jl(at(3, 0), "SHOP-1", "round", journal.EventClaim, with("s-b", "", "", "", "", "")),
		tool(at(3, 10), "s-b", "go test ./...", said("Run the tests")),
		jl(at(3, 30), "SHOP-1", "round", journal.EventVerified, with("", sha('a'), "", "", "", "")),
		jl(at(4, 0), "SHOP-1", "", journal.EventGateOpen, func(l *journal.Line) { l.Attempt = 1 }),
		jl(at(4, 10), "SHOP-1", "", journal.EventGateReviewer, func(l *journal.Line) { l.Attempt, l.Session, l.Model = 1, "s-b", "claude-opus-5" }),
		jl(at(5, 0), "SHOP-1", "", journal.EventGateClean, func(l *journal.Line) { l.Attempt, l.Session = 1, "s-b" }),
		tool(at(5, 1), "s-b", "jig review done SHOP-1", said("Close the review round")),
		jl(at(5, 30), "SHOP-1", "", journal.EventPrBody, func(l *journal.Line) { l.Commit = sha('a') }),
		tool(at(5, 40), "s-b", "git push origin SHOP-1-round", said("Push the branch")),
	)
	rows := strings.Split(g.view(), "\n")
	act := strings.Join(panelLines(rows, "Activity"), "\n")
	for _, want := range []string{
		"09:02:10 build          · Read the build skill",
		"09:03:10 build·round    · Run the tests",
		"09:05:01 review·r1      · Close the review round",
		"09:05:40 publish·pr     · Push the branch",
	} {
		if !strings.Contains(act, want) {
			t.Errorf("the activity lacks %q:\n%s", want, act)
		}
	}
	block := strings.Join(panelLines(rows, "Publish ─"), "\n")
	if !strings.Contains(block, "by       builder · Claude Code · Sonnet 5") {
		t.Errorf("the pull request's holder is not named as the builder:\n%s", block)
	}

	// The pull request is recorded: one call writes its line, the
	// changelog's and the publish's end at once. The push stays the pull
	// request's.
	g.feed(
		jl(at(6, 0), "SHOP-1", "", journal.EventChangelog),
		jl(at(6, 0), "SHOP-1", "", journal.EventPr, func(l *journal.Line) { l.URL = "https://example.test/acme/shop/pull/1" }),
		jl(at(6, 0), "SHOP-1", "", journal.EventPublishDone),
	)
	act = strings.Join(panelLines(strings.Split(g.view(), "\n"), "Activity"), "\n")
	if line := lineWith(act, "09:05:40"); !strings.Contains(line, "publish·pr ") || !strings.Contains(line, "Push the branch") {
		t.Errorf("once the pull request is recorded the push is not the pull request's:\n%s", act)
	}
}

// TestALiveLineSaysHowLongAgo: the live line gives how long ago its move was
// made in a few cells: "0s" under a second, span's words above it.
func TestALiveLineSaysHowLongAgo(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		d    time.Duration
		want string
	}{{0, "0s"}, {400 * time.Millisecond, "0s"}, {12 * time.Second, "12s"}, {75 * time.Second, "1m 15s"}} {
		if got := ago(tc.d); got != tc.want {
			t.Errorf("ago(%v) = %q, want %q", tc.d, got, tc.want)
		}
	}
}

// lineWith is the first line of text that holds part, "" when none does.
func lineWith(text, part string) string {
	for _, line := range strings.Split(text, "\n") {
		if strings.Contains(line, part) {
			return line
		}
	}
	return ""
}
