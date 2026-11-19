package verify

import (
	"fmt"
	"sort"
	"strings"

	"github.com/develdeco/jig/internal/gitx"
)

const (
	// pinnedMinWords is the fewest words a removed sentence has to be taken.
	pinnedMinWords = 8
	// pinnedCap is the most hits shown; the rest are counted.
	pinnedCap = 20
)

// PinnedHit is one tracked file that still holds a sentence the change
// removed: File and Line say where, Left the file the sentence left.
type PinnedHit struct {
	File string
	Line int
	Left string
}

// Pinned is what a run shows about the files that still hold text the change
// removed. Hits are in path order, at most pinnedCap of them; More counts the
// hits not shown. Note is why the look could not be made, "" when it was. It is
// advice shown beside the result: it never changes a run's outcome.
type Pinned struct {
	Hits []PinnedHit
	More int
	Note string
}

// PinnedText looks, for the run that ended at head in d.Clone, at the change as
// a whole (the base the gate's full scope uses, to head) for sentences it
// removed and reports each tracked file at head that still holds one. A line
// the change itself added is not a hit, and a sentence its own file still holds
// is not removed. A look that cannot be made returns the reason in Note and no
// hits.
func PinnedText(d Deps, ticket, head string) Pinned {
	x := &run{d: d, r: Request{Ticket: ticket}, sha: head, dir: d.Clone}
	base := x.changedBase()
	if base == "" {
		return Pinned{Note: "no base for the change is known"}
	}
	removed, added, err := gitx.RemovedSentences(d.Clone, base, head, pinnedMinWords)
	if err != nil {
		return Pinned{Note: fmt.Sprintf("the removed text could not be read: %v", err)}
	}
	if len(removed) == 0 {
		return Pinned{}
	}
	seen := map[string]bool{}
	var patterns []string
	for _, r := range removed {
		if !seen[r.Text] {
			seen[r.Text] = true
			patterns = append(patterns, r.Text)
		}
	}
	found, err := gitx.GrepFixed(d.Clone, head, patterns)
	if err != nil {
		return Pinned{Note: fmt.Sprintf("the files could not be searched: %v", err)}
	}
	if len(found) == 0 {
		return Pinned{}
	}
	addedAt := map[string]bool{}
	for _, a := range added {
		addedAt[fmt.Sprintf("%s:%d", a.Path, a.Line)] = true
	}
	// A sentence a file removed and still holds is not removed from it.
	stillHeld := map[[2]string]bool{}
	for _, h := range found {
		for _, r := range removed {
			if r.Path == h.Path && strings.Contains(h.Text, r.Text) {
				stillHeld[[2]string{r.Path, r.Text}] = true
			}
		}
	}
	uniq := map[PinnedHit]bool{}
	var hits []PinnedHit
	for _, h := range found {
		if addedAt[fmt.Sprintf("%s:%d", h.Path, h.Line)] {
			continue
		}
		for _, r := range removed {
			if !strings.Contains(h.Text, r.Text) || stillHeld[[2]string{r.Path, r.Text}] {
				continue
			}
			if hit := (PinnedHit{File: h.Path, Line: h.Line, Left: r.Path}); !uniq[hit] {
				uniq[hit] = true
				hits = append(hits, hit)
			}
		}
	}
	sort.Slice(hits, func(i, j int) bool {
		a, b := hits[i], hits[j]
		if a.File != b.File {
			return a.File < b.File
		}
		if a.Line != b.Line {
			return a.Line < b.Line
		}
		return a.Left < b.Left
	})
	p := Pinned{Hits: hits}
	if len(hits) > pinnedCap {
		p.Hits, p.More = hits[:pinnedCap], len(hits)-pinnedCap
	}
	return p
}
