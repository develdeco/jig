package main

import (
	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/home"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/project"
	"github.com/develdeco/jig/internal/slot"
	"github.com/develdeco/jig/internal/store"
)

// init registers this file's section with `jig status` (sections.go).
func init() {
	registerStatusSection(statusSection{Order: 70, Name: "waiting", Blocks: waitingBlocks})
}

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
// slice's verification or the ticket's gate round waits for the suite slot.
//
//	waiting[1]{slice,line}:
//	  a,waiting for the suite slot: T-1/gate is running its suite (1 waiting)
func waitingBlocks(st *store.Store, ticket string) ([]string, error) {
	cfg, err := project.LoadStoreConfig(st.Root)
	if err != nil {
		return nil, nil
	}
	name := cfg.Name
	if name == "" && len(cfg.Projects) == 1 {
		name = cfg.Projects[0]
	}
	root, err := home.Root()
	if err != nil {
		return nil, nil
	}
	lines, err := journal.Read(st, ticket)
	if err != nil {
		return nil, err
	}
	rows := waitRows(suiteWaits(root, name, ticket, lines))
	if len(rows) == 0 {
		return nil, nil
	}
	return []string{axi.Table("waiting", []string{"slice", "line"}, rows)}, nil
}
