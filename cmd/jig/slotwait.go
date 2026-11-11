package main

import (
	"slices"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/project"
	"github.com/develdeco/jig/internal/slot"
	"github.com/develdeco/jig/internal/store"
)

// suiteWaits are the runs of ticket that wait for the project's suite slot
// now (internal/slot). Best effort: a slot directory that cannot be read says
// nothing, since the line is advice and the state does not hang on it.
func suiteWaits(jigHome, projectName, ticket string, lines []journal.Line) []slot.Wait {
	if jigHome == "" || projectName == "" {
		return nil
	}
	waits, err := slot.Waits(jigHome, projectName, ticket, lines)
	if err != nil {
		return nil
	}
	return waits
}

// waitRows are waits as the rows of a `waiting` table: the slice that waits,
// or "gate" for the ticket's gate round, and the line that says so.
func waitRows(waits []slot.Wait) [][]string {
	var rows [][]string
	for _, w := range waits {
		slice := w.Slice
		if slice == "" {
			slice = "gate"
		}
		rows = append(rows, []string{slice, w.Line})
	}
	return rows
}

// waitingBlocks is the `waiting` table of `jig status`: present only while a
// slice's verification or the ticket's gate round waits for the suite slot, in
// any project the store serves. It reads the slots under jigHome, the jig home
// of the command that runs it.
//
//	waiting[1]{slice,line}:
//	  a,waiting for the suite slot: T-1/gate is running its suite (1 waiting)
func waitingBlocks(jigHome string, cfg project.Config) func(*store.Store, string) ([]string, error) {
	var names []string
	for _, n := range append([]string{cfg.Name}, cfg.Projects...) {
		if n != "" && !slices.Contains(names, n) {
			names = append(names, n)
		}
	}
	return func(st *store.Store, ticket string) ([]string, error) {
		if len(names) == 0 {
			return nil, nil
		}
		lines, err := journal.Read(st, ticket)
		if err != nil {
			return nil, err
		}
		var rows [][]string
		for _, name := range names {
			rows = append(rows, waitRows(suiteWaits(jigHome, name, ticket, lines))...)
		}
		if len(rows) == 0 {
			return nil, nil
		}
		return []string{axi.Table("waiting", []string{"slice", "line"}, rows)}, nil
	}
}
