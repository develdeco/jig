package main

import (
	"os"
	"path/filepath"
	"sort"

	"gopkg.in/yaml.v3"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/store"
)

// init registers this file's section with `jig status` (sections.go).
func init() {
	registerStatusSection(statusSection{Order: 60, Name: "builds", Blocks: buildsBlocks})
}

// buildsBlocks is the `builds` table of `jig status`: the jig builds the
// ticket's journal lines and gate reports name, when they name more than one.
// Running several builds can be deliberate, so this only shows them and never
// refuses.
//
//	builds[2]{jig,first,last}:
//	  v0.1.3+232c016,2026-11-06T10:00:00Z,2026-11-06T11:00:00Z
//	  v0.1.3+9ab4c1e-dirty,2026-11-06T12:00:00Z,2026-11-06T13:00:00Z
//
// first and last are the timestamps of the first and last journal line each
// build wrote, "-" for a build only a round's report.yaml names. A line or
// report an older jig wrote names no build and is not counted. One build, or
// none, has no block.
func buildsBlocks(st *store.Store, ticket string) ([]string, error) {
	lines, err := journal.Read(st, ticket)
	if err != nil {
		return nil, err
	}
	type span struct{ first, last string }
	seen := map[string]*span{}
	for _, l := range lines {
		if l.Jig == "" {
			continue
		}
		s := seen[l.Jig]
		if s == nil {
			seen[l.Jig] = &span{first: l.TS, last: l.TS}
			continue
		}
		if l.TS < s.first {
			s.first = l.TS
		}
		if l.TS > s.last {
			s.last = l.TS
		}
	}
	reports, _ := filepath.Glob(filepath.Join(st.TicketDir(ticket), "gate", "round-*", "report.yaml"))
	for _, path := range reports {
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var rep struct {
			Jig string `yaml:"jig"`
		}
		if yaml.Unmarshal(data, &rep) == nil && rep.Jig != "" && seen[rep.Jig] == nil {
			seen[rep.Jig] = &span{}
		}
	}
	if len(seen) < 2 {
		return nil, nil
	}
	builds := make([]string, 0, len(seen))
	for b := range seen {
		builds = append(builds, b)
	}
	sort.Slice(builds, func(i, j int) bool {
		a, b := seen[builds[i]], seen[builds[j]]
		if (a.first == "") != (b.first == "") {
			return b.first == "" // a build with no line of its own comes last
		}
		if a.first != b.first {
			return a.first < b.first
		}
		return builds[i] < builds[j]
	})
	rows := make([][]string, 0, len(builds))
	for _, b := range builds {
		rows = append(rows, []string{b, dashed(seen[b].first), dashed(seen[b].last)})
	}
	return []string{axi.Table("builds", []string{"jig", "first", "last"}, rows)}, nil
}
