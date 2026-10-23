package correct

import (
	"fmt"
	"slices"
	"strings"

	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/store"
)

// A Drift is a verified slice that cites brief sections the brief no longer
// has: its work was verified against words that have since changed.
type Drift struct {
	Slice string
	// Gone are the hashes the slice cites that match no section of the brief
	// now.
	Gone []string
}

// Verified lists the slices the journal shows verified: those with a verified
// line, not followed by a requeue of the slice (a requeue puts it back to be
// built, so what it was verified against no longer stands for it).
func Verified(lines []journal.Line) map[string]bool {
	out := map[string]bool{}
	for _, l := range lines {
		switch l.Event {
		case journal.EventVerified:
			out[l.Slice] = true
		case journal.EventRequeue:
			delete(out, l.Slice)
		}
	}
	return out
}

// BriefDrift compares the sections of brief (store.BriefSectionHashes) with
// the hashes the verified slices were cut from (their from_brief) and returns
// the slices of all that cite one the brief no longer has, in the order of
// all. A slice that cites no section, or that is not verified, drifts from
// nothing.
func BriefDrift(brief []byte, all []store.Slice, verified map[string]bool) []Drift {
	current := map[string]bool{}
	for _, hash := range store.BriefSectionHashes(brief) {
		current[hash] = true
	}
	var out []Drift
	for _, sl := range all {
		if !verified[sl.ID] {
			continue
		}
		var gone []string
		for _, hash := range sl.FromBrief {
			if !current[hash] {
				gone = append(gone, hash)
			}
		}
		if len(gone) > 0 {
			out = append(out, Drift{Slice: sl.ID, Gone: gone})
		}
	}
	return out
}

// driftRef fingerprints a drift: which slices cite which vanished sections.
// The same drift has the same Ref until the brief or the slices change.
func driftRef(drifts []Drift) string {
	var parts []string
	for _, d := range drifts {
		for _, hash := range d.Gone {
			parts = append(parts, d.Slice+":"+hash)
		}
	}
	slices.Sort(parts)
	return "brief:" + fingerprint(strings.Join(parts, "\n"))
}

// driftText is what the session is told of a drift.
func driftText(ticket string, drifts []Drift) string {
	names := make([]string, len(drifts))
	sections := 0
	for i, d := range drifts {
		names[i] = d.Slice
		sections += len(d.Gone)
	}
	return fmt.Sprintf("the brief changed under verified work: %s %s %s %d %s the brief no longer has. `jig requeue %s --from-brief-diff` queues %s again.",
		plural(len(names), "slice", "slices"), strings.Join(names, ", "), plural(len(names), "cites", "cite"),
		sections, plural(sections, "section", "sections"), ticket, plural(len(names), "it", "them"))
}
