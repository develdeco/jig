package frontier

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/store"
)

// Requeue is the mechanical (no-LLM) half of a brief amendment: it
// recomputes brief.md's section hashes and re-queues every slice whose
// FromBrief cites a hash no longer present, keeping that slice's attempt
// history (attempts, and therefore its attempt log) but clearing its
// question and reason. It returns the touched slice ids, in slices.yaml
// order.
func Requeue(d Deps, ticket string, fromBriefDiff bool) ([]string, error) {
	if !fromBriefDiff {
		// --from-brief-diff is Requeue's only mode; there is nothing else
		// for this seam to do yet.
		return nil, nil
	}

	briefData, err := os.ReadFile(filepath.Join(d.Store.TicketDir(ticket), "brief.md"))
	if err != nil {
		return nil, fmt.Errorf("frontier: read brief.md: %w", err)
	}
	current := map[string]bool{}
	for _, h := range store.BriefSectionHashes(briefData) {
		current[h] = true
	}

	slices, err := d.Store.ReadSlices(ticket)
	if err != nil {
		return nil, fmt.Errorf("frontier: read slices: %w", err)
	}

	var touched []string
	for _, sl := range slices {
		for _, h := range sl.FromBrief {
			if !current[h] {
				touched = append(touched, sl.ID)
				break
			}
		}
	}

	for _, id := range touched {
		st, err := d.Store.ReadSliceState(ticket, id)
		if err != nil {
			return nil, fmt.Errorf("frontier: read slice state %s: %w", id, err)
		}
		if st.Question != "" {
			if err := d.Store.Supersede(ticket, st.Question); err != nil {
				return nil, fmt.Errorf("frontier: supersede question %s: %w", st.Question, err)
			}
		}
		st.State = "queued"
		st.Question = ""
		st.Reason = ""
		st.Signature = ""
		st.StallSummary = ""
		if err := d.Store.WriteSliceState(ticket, id, st); err != nil {
			return nil, fmt.Errorf("frontier: write slice state %s: %w", id, err)
		}
		if err := d.Journal(journal.Line{Slice: id, Event: "requeue"}); err != nil {
			return nil, fmt.Errorf("frontier: journal requeue: %w", err)
		}
	}

	if len(touched) > 0 {
		if err := d.Store.Push(fmt.Sprintf("%s: requeue from brief diff", ticket)); err != nil {
			return nil, fmt.Errorf("frontier: push: %w", err)
		}
	}

	return touched, nil
}

// RequeueSlice requeues one stalled or env-blocked slice by id, the
// mechanical half of clearing a stall or an env that has since come back
// up: state back to queued, attempts kept (so the attempt log and cap carry
// over), reason, stall signature and stall summary cleared. It refuses any
// other state - a slice with a brief section to amend goes through Requeue
// (--from-brief-diff) instead, and a needs-input slice through Answer. It
// first checks sliceID against the ticket's own slices.yaml: an absent
// state file reads as the zero-value "queued" SliceState, which would
// otherwise report a typo'd or stale id as a real slice caught in the wrong
// state, so a caller's mistake is told apart from a queued, building or
// green slice by that membership check rather than left ambiguous.
func RequeueSlice(d Deps, ticket, sliceID string) error {
	slices, err := d.Store.ReadSlices(ticket)
	if err != nil {
		return fmt.Errorf("frontier: read slices: %w", err)
	}
	known := false
	for _, sl := range slices {
		if sl.ID == sliceID {
			known = true
			break
		}
	}
	if !known {
		return &axi.Error{
			Msg:  fmt.Sprintf("ticket %s has no slice %s", ticket, sliceID),
			Code: "SLICE_NOT_FOUND",
		}
	}

	st, err := d.Store.ReadSliceState(ticket, sliceID)
	if err != nil {
		return fmt.Errorf("frontier: read slice state %s: %w", sliceID, err)
	}
	if st.State != "stalled" && st.State != "env-blocked" {
		return fmt.Errorf("frontier: requeue slice %s: state is %q, not stalled or env-blocked", sliceID, st.State)
	}
	st.State = "queued"
	st.Reason = ""
	st.Signature = ""
	st.StallSummary = ""
	if err := d.Store.WriteSliceState(ticket, sliceID, st); err != nil {
		return fmt.Errorf("frontier: write slice state %s: %w", sliceID, err)
	}
	if err := d.Journal(journal.Line{Slice: sliceID, Event: "requeue"}); err != nil {
		return fmt.Errorf("frontier: journal requeue: %w", err)
	}
	if err := d.Store.Push(fmt.Sprintf("%s: requeue slice %s", ticket, sliceID)); err != nil {
		return fmt.Errorf("frontier: push: %w", err)
	}
	return nil
}
