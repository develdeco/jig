package main

import (
	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/store"
)

// init registers this file's section with `jig status` (sections.go).
func init() {
	registerStatusSection(statusSection{Order: 55, Name: "light", Blocks: lightBlocks})
}

// lightBlocks is the `light` table of `jig status`: each slice whose latest
// verified commit passed on a light delta, with how it stood, so a pass that
// ran the lint alone is not read as a whole-suite run of its own.
//
//	light[1]{slice,commit,pass}:
//	  notes,0a1b2c3d4e5f,"lint only, reusing 9f8e7d6c5b4a"
//
// A ticket with no such slice has no block.
func lightBlocks(st *store.Store, ticket string) ([]string, error) {
	lines, err := journal.Read(st, ticket)
	if err != nil {
		return nil, err
	}
	latest := map[string]string{} // slice -> the commit of its latest verified line
	var order []string
	for _, l := range lines {
		if l.Event != journal.EventVerified || l.Slice == "" || l.Commit == "" {
			continue
		}
		if _, ok := latest[l.Slice]; !ok {
			order = append(order, l.Slice)
		}
		latest[l.Slice] = l.Commit
	}
	var rows [][]string
	for _, slice := range order {
		v := journal.VerificationState(lines, latest[slice], slice)
		if w := v.LightWords(); w != "" {
			rows = append(rows, []string{slice, shortSHA(latest[slice]), w})
		}
	}
	if len(rows) == 0 {
		return nil, nil
	}
	return []string{axi.Table("light", []string{"slice", "commit", "pass"}, rows)}, nil
}
