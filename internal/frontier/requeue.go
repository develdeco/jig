package frontier

import (
	"fmt"
	"os"
	"path/filepath"

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
