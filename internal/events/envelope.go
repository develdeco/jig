package events

import (
	"time"

	"github.com/develdeco/jig/internal/journal"
)

// The two places a line comes from, as Envelope.Src names them.
const (
	// SrcJournal is a ticket's journal.ndjson in a store: shared by every
	// machine that clones the store.
	SrcJournal = "journal"
	// SrcLedger is the machine ledger under the jig home: this machine's own
	// record of its sessions, tools and workers.
	SrcLedger = "ledger"
)

// Envelope is one line of the stream: a journal line as it was written, with
// the file kind it came from. It encodes flat - src and store beside the
// line's own keys - so a reader that knows journal.Line reads an envelope
// as it is.
type Envelope struct {
	// Src is SrcJournal or SrcLedger.
	Src string `json:"src"`
	// Store is the name of the project whose store a journal line is from.
	// It is empty on a ledger line, which belongs to the machine and not to
	// a store.
	Store string `json:"store,omitempty"`
	journal.Line
}

// At is the instant the line carries in ts, and false when it carries none or
// one that is not RFC 3339 (a hand edit). Lines written by older jig versions
// stamp whole seconds and newer ones nanoseconds; both parse.
func (e Envelope) At() (time.Time, bool) {
	t, err := time.Parse(time.RFC3339Nano, e.TS)
	return t, err == nil
}
