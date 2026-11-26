package main

import (
	"strconv"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/store"
	"github.com/develdeco/jig/internal/verify"
)

// init registers this file's section with `jig status` (sections.go).
func init() {
	registerStatusSection(statusSection{Order: 57, Name: "last_runs", Blocks: lastRunsBlocks})
}

// lastRunsBlocks is the `last_runs` section from the journal alone: no branch
// is read, so a run's covers says "not checked" unless the run itself covered
// nothing. `jig status` replaces it with the one that reads the branch
// (statusSectionsWithBranch).
func lastRunsBlocks(st *store.Store, ticket string) ([]string, error) {
	lines, err := journal.Read(st, ticket)
	if err != nil {
		return nil, err
	}
	return lastRunsTable(verify.Truth{Ticket: ticket, Lines: lines}.LastRuns()), nil
}

// lastRunsTable is the `last_runs` table of `jig status`: each slice that has
// an oracle line, with its last witnessed run's commit, its result, whether it
// covers the slice's last commit, its wall seconds and what its process tree
// used. A run whose figures could not be measured reads "not measured", never
// zero.
//
//	last_runs[1]{slice,commit,result,covers,seconds,cpu_seconds,peak_memory,basis}:
//	  clamp,0a1b2c3d4e5f,pass,yes,41,63.2,1.4 GiB,sampled peak
//
// A ticket with no oracle line has no block.
func lastRunsTable(runs []verify.LastRun) []string {
	var rows [][]string
	for _, r := range runs {
		l := r.Line
		seconds := ""
		if l.Seconds > 0 {
			seconds = strconv.Itoa(l.Seconds)
		}
		peak := verify.MemoryWords(l.PeakBytes)
		basis := ""
		if peak != "" {
			basis = verify.BasisWords(l.PeakBasis)
		}
		rows = append(rows, []string{r.Slice, shortSHA(l.Commit), r.Result, r.Covers, orNotMeasured(seconds),
			orNotMeasured(verify.CPUWords(l.CPUMs)), orNotMeasured(peak), orNotMeasured(basis)})
	}
	if len(rows) == 0 {
		return nil
	}
	return []string{axi.Table("last_runs", []string{"slice", "commit", "result", "covers", "seconds", "cpu_seconds", "peak_memory", "basis"}, rows)}
}
