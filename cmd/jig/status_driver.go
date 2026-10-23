package main

import (
	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/store"
)

// init registers this file's section with `jig status` (sections.go).
func init() {
	registerStatusSection(statusSection{Order: 100, Name: "driver", Blocks: driverBlocks})
}

// driverBlocks is the `driver` block of `jig status`: the writer driving the
// ticket, from the claims log every writer shares (store.Driver), so it names
// someone on another machine too. A ticket no one drives has no block.
func driverBlocks(st *store.Store, ticket string) ([]string, error) {
	d, ok, err := st.Driver(ticket)
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, nil
	}
	pairs := [][2]string{{"ticket", ticket}, {"writer", d.Writer}}
	for _, kv := range [][2]string{{"author", d.Author}, {"session", d.Session}, {"since", d.TS}} {
		if kv[1] != "" {
			pairs = append(pairs, kv)
		}
	}
	return []string{axi.KV("driver", pairs)}, nil
}
