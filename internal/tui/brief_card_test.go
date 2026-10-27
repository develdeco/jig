package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/develdeco/jig/internal/journal"
)

// TestABriefBeingDraftedOpensIntoThePlannersCard: a session that mints a
// ticket is tied to it in the ledger before any journal line names it, so the
// brief's sub-step under way opens into a card held by the planner, with what
// it did since, in the words it gave each call. Once the brief is written its
// line names its sections, the slices' card shows them, and once the slices
// are written the approval's card lists them. What the session did before it
// minted the ticket is not the ticket's.
func TestABriefBeingDraftedOpensIntoThePlannersCard(t *testing.T) {
	t.Parallel()
	g, advance := quietRig(t, draftStatus(nil))
	g.send(tea.WindowSizeMsg{Width: 100, Height: 40})
	g.feed(
		ll(at(20, 0), "", journal.EventSessionStart, func(l *journal.Line) { l.Session, l.Harness, l.Model = "s-p", "claude", "claude-sonnet-5" }),
		tool(at(20, 30), "s-p", "go test ./...", said("Run the tests to see the bug")),
		ll(at(21, 0), "SHOP-1", journal.EventSessionTouch, func(l *journal.Line) { l.Session = "s-p" }),
		tool(at(21, 1), "s-p", `jig ticket new --title "Cart totals"`, said("Mint a ticket for the bug")),
		tool(at(22, 0), "s-p", "jig skill intake", said("Read the intake skill")),
	)
	rows := strings.Split(g.view(), "\n")
	block := strings.Join(panelLines(rows, "Brief ─"), "\n")
	for _, want := range []string{
		"◐ brief drafted   drafted ── slices ── approved", "9m 00s",
		"planner  Claude Code · Sonnet 5",
		"now      8m 00s  · Read the intake skill",
		"○ slices written  after the brief",
		"○ brief approved  after the slices",
	} {
		if !strings.Contains(block, want) {
			t.Errorf("while the brief is drafted the block lacks %q:\n%s", want, block)
		}
	}
	act := strings.Join(panelLines(rows, "Activity"), "\n")
	for _, want := range []string{"09:22:00 brief·drafted · Read the intake skill", "09:21:01 brief·drafted · Mint a ticket for the bug"} {
		if !strings.Contains(act, want) {
			t.Errorf("the activity lacks %q:\n%s", want, act)
		}
	}
	if strings.Contains(act, "Run the tests to see the bug") {
		t.Errorf("the activity shows a call the session made before it minted the ticket:\n%s", act)
	}

	// brief.md is written: three sections.
	g.r.answer("status SHOP-1 --store /s", draftStatus(sections(3)), 0)
	advance(4 * time.Second)
	g.send(idleMsg{})
	g.feed(tool(at(30, 10), "s-p", "jig validate SHOP-1 --check", said("Check the brief")))
	advance(20 * time.Second)
	block = strings.Join(panelLines(strings.Split(g.view(), "\n"), "Brief ─"), "\n")
	for _, want := range []string{
		"✓ brief drafted   3 sections: Section 1, Section 2, Section 3",
		"◐ slices written  drafted ━━ slices ── approved",
		"sections Section 1 · Section 2 · Section 3",
		"now      14s     · Check the brief",
	} {
		if !strings.Contains(block, want) {
			t.Errorf("once the brief is written the block lacks %q:\n%s", want, block)
		}
	}
	if strings.Contains(block, "Read the intake skill") {
		t.Errorf("the slices' live line is a call made before the brief was written:\n%s", block)
	}

	// slices.yaml is written: the approval is the person's.
	g.r.answer("status SHOP-1 --store /s", draftStatus(sections(3), "round", "clamp"), 0)
	advance(4 * time.Second)
	g.send(idleMsg{})
	block = strings.Join(panelLines(strings.Split(g.view(), "\n"), "Brief ─"), "\n")
	for _, want := range []string{
		"✓ slices written  2 slices: round, clamp",
		"? brief approved  drafted ━━ slices ━━ approved",
		"slices   round, clamp",
	} {
		if !strings.Contains(block, want) {
			t.Errorf("once the slices are written the block lacks %q:\n%s", want, block)
		}
	}
}

// TestScreensOpenedOnNoTicketOpenTheFirstOneASessionCreates: `jig tui` with no
// ticket names none in advance. The list says what it waits for, and the first
// ticket the stream names after the screens started opens, with the list one
// step back; a ticket from before they started does not, nor does a new one
// once a key was pressed.
func TestScreensOpenedOnNoTicketOpenTheFirstOneASessionCreates(t *testing.T) {
	t.Parallel()
	g := newRig(t, "")
	g.r.answer("next --json --store /s", `{"tickets":[]}`+"\n", 0)
	g.feed()
	if v := g.view(); !strings.Contains(v, "the first ticket a session creates opens here") {
		t.Errorf("an empty list does not say a new ticket opens:\n%s", v)
	}
	g.feed(jl(at(10, 0), "OLD-1", "", journal.EventApproval, with("s-x", "", "", "Dana <dana@example.invalid>", "", "person")))
	if g.m.Screen() != "main" {
		t.Fatalf("a ticket from before the screens started opened the %s screen", g.m.Screen())
	}
	g.feed(ll(at(59, 40), "SHOP-1", journal.EventSessionTouch, func(l *journal.Line) { l.Session = "s-p" }))
	if g.m.Screen() != "ticket" || g.m.top().ticket != "SHOP-1" {
		t.Fatalf("a ticket minted after the screens started did not open: screen %s on %q", g.m.Screen(), g.m.top().ticket)
	}
	g.press("esc")
	if g.m.Screen() != "main" {
		t.Errorf("esc from the ticket that opened went to the %s screen, want the list", g.m.Screen())
	}
	g.feed(ll(at(59, 50), "SHOP-2", journal.EventSessionTouch, func(l *journal.Line) { l.Session = "s-q" }))
	if g.m.Screen() != "main" {
		t.Errorf("a second new ticket opened once the person had used the keys")
	}

	h := newRig(t, "")
	h.feed()
	h.press("down")
	h.feed(ll(at(59, 40), "SHOP-1", journal.EventSessionTouch, func(l *journal.Line) { l.Session = "s-p" }))
	if h.m.Screen() != "main" {
		t.Errorf("a new ticket opened after the person pressed a key")
	}
}
