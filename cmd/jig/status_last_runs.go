package main

import (
	"strconv"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/store"
)

// init registers this file's section with `jig status` (sections.go).
func init() {
	registerStatusSection(statusSection{Order: 57, Name: "last_runs", Blocks: lastRunsBlocks})
}

// lastRunsBlocks is the `last_runs` table of `jig status`: each slice that has
// an oracle line, with its latest run's commit, wall seconds and what its
// process tree used. A run whose figures could not be measured reads
// "not measured", never zero.
//
//	last_runs[1]{slice,commit,seconds,cpu_seconds,peak_memory,basis}:
//	  clamp,0a1b2c3d4e5f,41,63.2,1.4 GiB,sampled peak
//
// A ticket with no oracle line has no block.
func lastRunsBlocks(st *store.Store, ticket string) ([]string, error) {
	lines, err := journal.Read(st, ticket)
	if err != nil {
		return nil, err
	}
	latest := map[string]journal.Line{}
	var order []string
	for _, l := range lines {
		if l.Event != journal.EventOracle || l.Slice == "" {
			continue
		}
		if _, ok := latest[l.Slice]; !ok {
			order = append(order, l.Slice)
		}
		latest[l.Slice] = l
	}
	var rows [][]string
	for _, slice := range order {
		l := latest[slice]
		seconds := ""
		if l.Seconds > 0 {
			seconds = strconv.Itoa(l.Seconds)
		}
		peak := memoryWords(l.PeakBytes)
		basis := ""
		if peak != "" {
			basis = basisWords(l.PeakBasis)
		}
		rows = append(rows, []string{slice, shortSHA(l.Commit), orNotMeasured(seconds),
			orNotMeasured(cpuWords(l.CPUMs)), orNotMeasured(peak), orNotMeasured(basis)})
	}
	if len(rows) == 0 {
		return nil, nil
	}
	return []string{axi.Table("last_runs", []string{"slice", "commit", "seconds", "cpu_seconds", "peak_memory", "basis"}, rows)}, nil
}
