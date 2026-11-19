package recall

import (
	"sort"
	"time"

	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/spend"
)

// MaxReceipts is the most shipped tickets a report lists as receipts.
const MaxReceipts = 10

// Facts are what a shipped ticket's journal says about how big its change was
// and how much context its builders held: the time of its last publish-done
// line, the size that line recorded (nil when it recorded none), and the
// largest peak among the sessions and agents that built its slices (0 when no
// builder's line carries a peak).
type Facts struct {
	At     string
	Change *journal.ChangeSize
	Peak   int64
}

// FactsOf reads the Facts out of a ticket's journal. A builder is a session or
// an agent whose spend line was written on a slice; its peak is the largest of
// its lines, so a later line never lowers it.
func FactsOf(lines []journal.Line) Facts {
	var f Facts
	for _, l := range lines {
		if l.Event == journal.EventPublishDone {
			f.At = l.TS
			f.Change = l.Change
		}
	}
	built := map[string]bool{}
	peak := map[string]int64{}
	for _, l := range lines {
		if l.Event != journal.EventSpend || l.Session == "" || l.Spend == nil {
			continue
		}
		k := spend.Key(l.Session, l.Agent)
		if l.Slice != "" {
			built[k] = true
		}
		peak[k] = max(peak[k], l.Spend.Peak)
	}
	for k := range built {
		f.Peak = max(f.Peak, peak[k])
	}
	return f
}

// Receipt is a shipped ticket as a sizing planner reads it: the files its
// change touched, their bytes at the base, and its builder's peak context.
type Receipt struct {
	Ticket string `json:"ticket"`
	Files  int    `json:"files"`
	Bytes  int64  `json:"bytes"`
	Peak   int64  `json:"peak"`
}

// receipts are the last MaxReceipts shipped tickets with a recorded size and
// a builder peak, newest first. A ticket with either missing is left out,
// not listed as zero.
func receipts(tickets []Ticket) []Receipt {
	type row struct {
		r   Receipt
		at  time.Time
		pos int
	}
	var rows []row
	for i, t := range tickets {
		f := t.Facts
		if !t.Shipped || f == nil || f.Change == nil || f.Peak <= 0 {
			continue
		}
		at, _ := time.Parse(time.RFC3339Nano, f.At)
		rows = append(rows, row{r: Receipt{Ticket: t.ID, Files: f.Change.Files, Bytes: f.Change.Bytes, Peak: f.Peak}, at: at, pos: i})
	}
	// A stamp that does not parse is the zero time and sorts oldest; equal
	// stamps keep the store order, the later ticket first.
	sort.SliceStable(rows, func(i, j int) bool {
		if !rows[i].at.Equal(rows[j].at) {
			return rows[i].at.After(rows[j].at)
		}
		return rows[i].pos > rows[j].pos
	})
	out := []Receipt{}
	for _, r := range rows {
		if len(out) == MaxReceipts {
			break
		}
		out = append(out, r.r)
	}
	return out
}
