package tui

import (
	"time"

	"github.com/develdeco/jig/internal/events"
	"github.com/develdeco/jig/internal/phase"
)

// What a screen reads of a ticket's journal (its lines of a step, its rounds,
// its questions, its landings, its publish) is a reading of every line the
// ticket has, and a ticket that has run long has many. The reading stands for as
// long as what it was read from does: the screens draw the same ticket many times
// between two lines, a key's frame, a tick or a batch of the stream for another
// ticket among them, and a frame must cost what it draws, not the history of the
// tickets it draws. A reading is taken again when a line, a status or a roster
// read changes what it was made from.

// memoKey names what a ticket's readings were made from: the lines, the ledger
// (its sequence, since its length stops growing at its cap), the lines the
// screens wrote themselves, and the status and the digest as last read.
type memoKey struct {
	lines, ledger, ledgerSeq, seen, older int
	// info counts the changes to what the ledger said of the sessions, which
	// names the people in a ticket's words whatever ticket the line was on.
	info   int
	status *statusInfo
	digest *digestInfo
}

// ticketMemo holds the readings of one ticket under one key.
type ticketMemo struct {
	key memoKey
	// zone is the zone the readings that show a time were made in.
	zone      *time.Location
	stepLines map[int][]events.Envelope
	rounds    *[]roundRow
	questions *[]questionRow
	landed    *[]landedAt
	publish   *publishInfo
	phase     *phase.Ticket
	clocks    *clocks
	clocksAt  int64
}

// memoFor is the readings of t as it stands now: those of the last ask while
// nothing they were made from changed, else none.
func (t *ticket) memoFor() *ticketMemo {
	info := 0
	if t.w != nil {
		info = t.w.infoSeq
	}
	k := memoKey{len(t.lines), len(t.ledger), t.ledgerSeq, len(t.seen), len(t.older), info, t.status, t.digest}
	if t.memo == nil || t.memo.key != k {
		t.memo = &ticketMemo{key: k}
	}
	return t.memo
}

// clocksOf is the work clocks of t as it stands, at the second now is in: the
// time a screen shows is counted to the second, so the same count serves every
// frame within it, and a ticket's lines are walked once for them, not once for
// each frame that draws a time. Only a screen that shows a time asks for it.
func (m *Model) clocksOf(t *ticket) clocks {
	memo := t.memoFor()
	if now := m.now().Unix(); memo.clocks == nil || memo.clocksAt != now {
		c := m.clocks(t, m.phaseOf(t))
		memo.clocks, memo.clocksAt = &c, now
	}
	return *memo.clocks
}
