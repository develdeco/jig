package tui

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/develdeco/jig/internal/journal"
)

// The store moving under the screens: the idle tick compares the store's head
// and re-reads the ticket list and the open chart when it moved, and a line of
// a ticket says the list may be out of date without any commit.

const moveRosterVerb = "next --json --store /s"

// storeRig is a project screen whose clock and store head the test moves: the
// list was read when it opened, at t0.
type storeRig struct {
	*rig
	now  time.Time
	head string
	err  error
}

func newStoreRig(t *testing.T, g *rig) *storeRig {
	t.Helper()
	s := &storeRig{rig: g, now: rigNow, head: "h1"}
	g.m.now = func() time.Time { return s.now }
	g.m.cfg.StoreHead = func() (string, error) { return s.head, s.err }
	return s
}

// tick moves the clock by d and delivers one idle tick.
func (s *storeRig) tick(d time.Duration) {
	s.t.Helper()
	s.now = s.now.Add(d)
	s.send(idleMsg{})
}

// movedRoster is the store's list after another process graduated JIG-9 and
// made the chart "intake".
func movedRoster() string {
	return jsonLine(map[string]any{
		"tickets": []map[string]any{
			{"id": "JIG-1", "title": "Greeting copy", "state": "ready"},
			{"id": "JIG-9", "title": "Late arrival", "state": "ready"},
		},
		"charts": []map[string]any{{"name": "intake", "title": "Intake chart", "tickets": []string{"JIG-9"}}},
	})
}

// TestStoreMoveRereadsMain: a ticket graduated and a chart made by another
// process show on the project screen, with their titles, in the frame after the
// head changes, and not before.
func TestStoreMoveRereadsMain(t *testing.T) {
	t.Parallel()
	s := newStoreRig(t, newRig(t, "").feed())
	s.r.answer(moveRosterVerb, movedRoster(), 0)

	s.tick(5 * time.Second) // the baseline
	if v := s.view(); strings.Contains(v, "Late arrival") {
		t.Fatalf("the list was read on the baseline head:\n%s", v)
	}
	s.tick(5 * time.Second) // the same head
	if v := s.view(); strings.Contains(v, "Late arrival") {
		t.Fatalf("the list was read with the head unmoved:\n%s", v)
	}

	s.head = "h2"
	s.tick(5 * time.Second)
	v := s.view()
	for _, want := range []string{"Late arrival", "JIG-9", "1 chart"} {
		if !strings.Contains(v, want) {
			t.Errorf("the frame after the head moved lacks %q:\n%s", want, v)
		}
	}
}

// TestStoreMoveRereadsChart: the chart on the screen is read again with the
// list when the head moved, and shows what the verb now says.
func TestStoreMoveRereadsChart(t *testing.T) {
	t.Parallel()
	s := newStoreRig(t, chartScreenRig(t))
	s.tick(5 * time.Second) // the baseline
	roster, chart := s.r.ran(moveRosterVerb), s.r.ran(mergeChartVerb)

	s.r.answer(moveRosterVerb, chartRoster(checkoutListing()), 0)
	s.r.answer(mergeChartVerb, checkoutJSON(func(v map[string]any) { v["title"] = "Coupons, second draft" }), 0)
	s.head = "h2"
	s.tick(5 * time.Second)
	if got := s.r.ran(moveRosterVerb) - roster; got != 1 {
		t.Errorf("the list was read %d times for the move, want 1", got)
	}
	if got := s.r.ran(mergeChartVerb) - chart; got != 1 {
		t.Errorf("the chart was read %d times for the move, want 1", got)
	}
	if v := s.view(); !strings.Contains(v, "Coupons, second draft") {
		t.Errorf("the chart screen shows the old chart:\n%s", v)
	}
}

// TestStoreMoveLandedLine: a landing another session wrote moves no commit; the
// line the stream brings marks the list stale and the next idle tick reads it.
func TestStoreMoveLandedLine(t *testing.T) {
	t.Parallel()
	s := newStoreRig(t, newRig(t, "").feed())
	s.tick(5 * time.Second) // the baseline
	before := s.r.ran(moveRosterVerb)

	s.r.answer(moveRosterVerb, movedRoster(), 0)
	s.feed(jl(at(59, 40), "JIG-1", "", journal.EventLanded))
	if got := s.r.ran(moveRosterVerb); got != before {
		t.Fatalf("the list was read on the line itself, %d reads, want %d", got, before)
	}
	s.tick(5 * time.Second)
	if got := s.r.ran(moveRosterVerb) - before; got != 1 {
		t.Errorf("the list was read %d times after the line, want 1", got)
	}
	if v := s.view(); !strings.Contains(v, "Late arrival") {
		t.Errorf("the frame after the line lacks the new read:\n%s", v)
	}
}

// TestStoreMoveQuiet: a store that does not move starts no verb, whatever the
// head read does: the baseline, an unchanged head, a failed read, and a stale
// mark inside IdleAfter of the last list read.
func TestStoreMoveQuiet(t *testing.T) {
	t.Parallel()
	s := newStoreRig(t, newRig(t, "").feed())
	base := s.r.total()
	verbs := func(what string) {
		t.Helper()
		if got := s.r.total() - base; got != 0 {
			t.Fatalf("%s started %d verbs, want none", what, got)
		}
	}

	s.tick(5 * time.Second)
	verbs("the baseline read")
	s.tick(5 * time.Second)
	verbs("an unchanged head")

	s.err, s.head = errors.New("no git"), "h2"
	s.tick(5 * time.Second)
	verbs("a failed head read")

	// The failed read kept the baseline: the head it hid is seen once it reads,
	// and a read inside IdleAfter of the last list read waits for the floor.
	s.err = nil
	s.now = s.now.Add(5 * time.Second)
	s.m.lastRoster, s.m.rosterEnded = s.now, s.now
	s.tick(time.Second)
	verbs("a stale mark inside IdleAfter")
	if !s.m.rosterStale {
		t.Fatal("the moved head was not kept as a stale mark inside IdleAfter")
	}
	s.tick(3 * time.Second)
	if got := s.r.total() - base; got != 1 {
		t.Errorf("the stale mark past IdleAfter started %d verbs, want the one list read", got)
	}
}

// TestStoreMoveReadsOnceAtATime: a stale list is not read again while a read of
// it is out.
func TestStoreMoveReadsOnceAtATime(t *testing.T) {
	t.Parallel()
	s := newStoreRig(t, newRig(t, "").feed())
	s.tick(5 * time.Second)
	s.m.rosterStale = true
	s.m.rosterBusy = true
	base := s.r.total()
	s.tick(5 * time.Second)
	if got := s.r.total() - base; got != 0 {
		t.Errorf("a read was started with one out: %d verbs", got)
	}
}

// TestStoreMoveHistoryReplayReadsNothing: the lines the store already held when
// the screens opened come through the stream after the first list read, and
// stamp earlier than it: they mark nothing stale.
func TestStoreMoveHistoryReplayReadsNothing(t *testing.T) {
	t.Parallel()
	g := newRig(t, "")
	s := newStoreRig(t, g)
	g.feed(jl(at(1, 0), "JIG-1", "a", journal.EventClaim), jl(at(2, 0), "JIG-1", "a", journal.EventVerified))
	base := s.r.total()
	s.tick(5 * time.Second)
	s.tick(5 * time.Second)
	if got := s.r.total() - base; got != 0 {
		t.Errorf("the replay of history started %d verbs, want none", got)
	}
}

// TestStoreMoveBaselineIsReadAtStart: the head is read when the screens open,
// so a commit in the first second is a move, not the baseline.
func TestStoreMoveBaselineIsReadAtStart(t *testing.T) {
	t.Parallel()
	g := newRig(t, "")
	s := newStoreRig(t, g)
	g.feed()
	s.r.answer(moveRosterVerb, movedRoster(), 0)
	s.head = "h2"
	s.tick(5 * time.Second)
	if v := s.view(); !strings.Contains(v, "Late arrival") {
		t.Errorf("a commit before the first tick was taken as the baseline:\n%s", v)
	}
}

// TestStoreMoveChartWaitsForTheRosterRead: while a list read is out, the chart
// on the screen is not read for the move; the next tick reads both.
func TestStoreMoveChartWaitsForTheRosterRead(t *testing.T) {
	t.Parallel()
	s := newStoreRig(t, chartScreenRig(t))
	s.tick(5 * time.Second) // the baseline
	s.head = "h2"
	s.m.rosterBusy = true
	chart := s.r.ran(mergeChartVerb)
	s.tick(5 * time.Second)
	if got := s.r.ran(mergeChartVerb) - chart; got != 0 {
		t.Fatalf("the chart was read %d times with a list read out, want 0", got)
	}
	s.m.rosterBusy = false
	s.tick(5 * time.Second)
	if got := s.r.ran(mergeChartVerb) - chart; got != 1 {
		t.Errorf("the chart was read %d times once the list read ended, want 1", got)
	}
}

// TestStoreMoveIgnoresLedgerLines: a session's tool calls reach the stream as
// ledger lines of the ticket and change nothing the list shows.
func TestStoreMoveIgnoresLedgerLines(t *testing.T) {
	t.Parallel()
	s := newStoreRig(t, newRig(t, "").feed())
	s.tick(5 * time.Second)
	s.feed(ll(at(59, 40), "JIG-1", journal.EventTool))
	base := s.r.total()
	s.tick(5 * time.Second)
	if got := s.r.total() - base; got != 0 {
		t.Errorf("a ledger line started %d verbs, want none", got)
	}
}

// TestStoreMoveSpacingCountsFromTheReadsEnd: a list read that takes longer than
// the spacing is not followed by another at once: the spacing runs from the end
// of the read, not from its start.
func TestStoreMoveSpacingCountsFromTheReadsEnd(t *testing.T) {
	t.Parallel()
	s := newStoreRig(t, newRig(t, "").feed())
	s.tick(10 * time.Second)
	// A read starts and takes ten seconds; the store moved meanwhile.
	s.m.rosterBusy, s.m.lastRoster = true, s.now
	s.now = s.now.Add(10 * time.Second)
	s.m.onRoster(rosterMsg{})
	s.m.rosterStale = true
	base := s.r.total()
	s.tick(time.Second)
	if got := s.r.total() - base; got != 0 {
		t.Errorf("a list read ended a second ago and was read again: %d verbs", got)
	}
	s.tick(5 * time.Second)
	if got := s.r.total() - base; got == 0 {
		t.Errorf("a stale list was not read once the spacing had passed since the read's end")
	}
}
