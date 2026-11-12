package main

import (
	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/store"
)

// init registers this file's section with `jig status` (sections.go).
func init() {
	registerStatusSection(statusSection{Order: 56, Name: "uncounted", Blocks: uncountedBlocks})
}

// uncountedBlocks is the `uncounted` table of `jig status`: each slice whose
// latest oracle run ended without judging the code (a timeout or a stop) and
// has not verified since, so that run is not read as a failing test.
//
//	uncounted[1]{slice,commit,run}:
//	  notes,0a1b2c3d4e5f,"timed out, not counted"
//
// A ticket with no such slice has no block.
func uncountedBlocks(st *store.Store, ticket string) ([]string, error) {
	lines, err := journal.Read(st, ticket)
	if err != nil {
		return nil, err
	}
	type latest struct {
		commit string
		words  string
	}
	last := map[string]latest{} // slice -> its latest oracle or verified line
	var order []string
	for _, l := range lines {
		if l.Slice == "" || l.Commit == "" || (l.Event != journal.EventOracle && l.Event != journal.EventVerified) {
			continue
		}
		if _, ok := last[l.Slice]; !ok {
			order = append(order, l.Slice)
		}
		cur := latest{commit: l.Commit}
		if l.Event == journal.EventOracle && l.Outcome == "fail" {
			cur.words = journal.Verification{State: journal.VerifyFail, Cause: l.Cause}.NotCounted()
		}
		last[l.Slice] = cur
	}
	var rows [][]string
	for _, slice := range order {
		if c := last[slice]; c.words != "" {
			rows = append(rows, []string{slice, shortSHA(c.commit), c.words})
		}
	}
	if len(rows) == 0 {
		return nil, nil
	}
	return []string{axi.Table("uncounted", []string{"slice", "commit", "run"}, rows)}, nil
}
