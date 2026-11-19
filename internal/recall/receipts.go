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
// an agent with a spend line stamped at or after the ticket's first claim,
// less the subagents a gate-round or gate-clean line names as a round's
// reviewer (a session named as reviewer that also claimed is still a builder,
// as it is no subagent). A builder's peak is the largest of its lines, so a
// later line never lowers it.
func FactsOf(lines []journal.Line) Facts {
	var f Facts
	var firstClaim time.Time
	reviewers := map[string]bool{}
	for _, l := range lines {
		switch l.Event {
		case journal.EventPublishDone:
			f.At = l.TS
			f.Change = l.Change
		case journal.EventClaim:
			if at, err := time.Parse(time.RFC3339Nano, l.TS); err == nil && (firstClaim.IsZero() || at.Before(firstClaim)) {
				firstClaim = at
			}
		case journal.EventGateRound, journal.EventGateClean:
			if l.Session != "" && l.Agent != "" {
				reviewers[spend.Key(l.Session, l.Agent)] = true
			}
		}
	}
	if firstClaim.IsZero() {
		return f // nothing was built through jig: nobody to name
	}
	built := map[string]bool{}
	peak := map[string]int64{}
	for _, l := range lines {
		if l.Event != journal.EventSpend || l.Session == "" || l.Spend == nil {
			continue
		}
		k := spend.Key(l.Session, l.Agent)
		peak[k] = max(peak[k], l.Spend.Peak)
		// A stamp that does not parse is no proof the line is from before.
		if at, err := time.Parse(time.RFC3339Nano, l.TS); err != nil || !at.Before(firstClaim) {
			built[k] = true
		}
	}
	for k := range built {
		if !reviewers[k] {
			f.Peak = max(f.Peak, peak[k])
		}
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
