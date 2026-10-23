package ready

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/store"
)

// The two ways a slice goes back to queued by a person's word (`jig
// requeue`), which is what `jig next` then offers it for: after a brief
// amendment, or after a stall or an env that has since come back up. Unlike
// the rest of this package they write: the slice's state, a requeue line in
// the ticket's journal, and a push.

// Requeue is the mechanical (no-LLM) half of a brief amendment: it
// recomputes brief.md's section hashes and re-queues every slice whose
// FromBrief cites a hash no longer present, keeping that slice's attempt
// history (attempts, and therefore its attempt log) but clearing its
// question and reason. It returns the touched slice ids, in slices.yaml
// order.
func Requeue(st *store.Store, ticket string, fromBriefDiff bool) ([]string, error) {
	if !fromBriefDiff {
		// --from-brief-diff is Requeue's only mode; there is nothing else
		// for this seam to do yet.
		return nil, nil
	}

	// A ticket with no brief - one that adopted a branch, or was never given
	// one - has no sections to compare, so the mode has nothing to do: say
	// so, and what the slices' own remedies are, instead of failing on a
	// missing file.
	if !hasBrief(st, ticket) {
		return nil, &axi.Error{
			Msg:  fmt.Sprintf("ticket %s has no brief.md, so there are no brief sections for --from-brief-diff to compare", ticket),
			Code: "VALIDATION_ERROR",
			Help: []string{
				fmt.Sprintf("Retry a stalled slice with `jig requeue %s --slice <id>`", ticket),
				fmt.Sprintf("Answer a parked slice's question with `jig answer %s <qid> '<text>'`", ticket),
			},
		}
	}
	briefData, err := os.ReadFile(filepath.Join(st.TicketDir(ticket), "brief.md"))
	if err != nil {
		return nil, fmt.Errorf("ready: read brief.md: %w", err)
	}
	current := map[string]bool{}
	for _, h := range store.BriefSectionHashes(briefData) {
		current[h] = true
	}

	slices, err := st.ReadSlices(ticket)
	if err != nil {
		return nil, fmt.Errorf("ready: read slices: %w", err)
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
		state, err := st.ReadSliceState(ticket, id)
		if err != nil {
			return nil, fmt.Errorf("ready: read slice state %s: %w", id, err)
		}
		if state.Question != "" {
			if err := st.Supersede(ticket, state.Question); err != nil {
				return nil, fmt.Errorf("ready: supersede question %s: %w", state.Question, err)
			}
		}
		state.State = "queued"
		state.Question = ""
		state.Reason = ""
		state.Signature = ""
		state.StallSummary = ""
		if err := st.WriteSliceState(ticket, id, state); err != nil {
			return nil, fmt.Errorf("ready: write slice state %s: %w", id, err)
		}
		if err := journal.Append(st, ticket, journal.Line{Slice: id, Event: journal.EventRequeue}); err != nil {
			return nil, fmt.Errorf("ready: journal requeue: %w", err)
		}
	}

	if len(touched) > 0 {
		if err := st.Push(fmt.Sprintf("%s: requeue from brief diff", ticket)); err != nil {
			return nil, fmt.Errorf("ready: push: %w", err)
		}
	}

	return touched, nil
}

// RequeueSlice requeues one stalled or env-blocked slice by id, the
// mechanical half of clearing a stall or an env that has since come back
// up: state back to queued, attempts kept (so the attempt log and cap carry
// over), reason, stall signature and stall summary cleared. It refuses any
// other state - a slice with a brief section to amend goes through Requeue
// (--from-brief-diff) instead, and a needs-input slice through `jig answer`. It
// first checks sliceID against the ticket's own slices.yaml: an absent
// state file reads as the zero-value "queued" SliceState, which would
// otherwise report a typo'd or stale id as a real slice caught in the wrong
// state, so a caller's mistake is told apart from a queued, building or
// green slice by that membership check rather than left ambiguous.
func RequeueSlice(st *store.Store, ticket, sliceID string) error {
	slices, err := st.ReadSlices(ticket)
	if err != nil {
		return fmt.Errorf("ready: read slices: %w", err)
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

	state, err := st.ReadSliceState(ticket, sliceID)
	if err != nil {
		return fmt.Errorf("ready: read slice state %s: %w", sliceID, err)
	}
	if state.State != "stalled" && state.State != "env-blocked" {
		return fmt.Errorf("ready: requeue slice %s: state is %q, not stalled or env-blocked", sliceID, state.State)
	}
	state.State = "queued"
	state.Reason = ""
	state.Signature = ""
	state.StallSummary = ""
	if err := st.WriteSliceState(ticket, sliceID, state); err != nil {
		return fmt.Errorf("ready: write slice state %s: %w", sliceID, err)
	}
	if err := journal.Append(st, ticket, journal.Line{Slice: sliceID, Event: journal.EventRequeue}); err != nil {
		return fmt.Errorf("ready: journal requeue: %w", err)
	}
	if err := st.Push(fmt.Sprintf("%s: requeue slice %s", ticket, sliceID)); err != nil {
		return fmt.Errorf("ready: push: %w", err)
	}
	return nil
}

// hasBrief reports whether ticket has a brief.md in the store: whether there
// is a brief for `jig requeue --from-brief-diff` to diff. A brief that cannot
// be statted for any reason but its absence counts as there.
func hasBrief(st *store.Store, ticket string) bool {
	_, err := os.Stat(filepath.Join(st.TicketDir(ticket), "brief.md"))
	return !errors.Is(err, fs.ErrNotExist)
}
