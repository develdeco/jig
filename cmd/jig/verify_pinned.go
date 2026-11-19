package main

import (
	"fmt"
	"strconv"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/verify"
)

// pinnedHead is the commit the run's pinned-text look is made at: where HEAD
// was when the command ended, else the commit the run started at.
func pinnedHead(res verify.Result) string {
	if res.HeadEnd != "" {
		return res.HeadEnd
	}
	return res.SHA
}

// pinnedBlocks is what a run shows beside its result about the files that
// still hold text the change removed: a table of each hit and the file the
// sentence left, a line counting the hits not shown, or one line saying why the
// look could not be made. A run with no hit shows nothing.
func pinnedBlocks(p verify.Pinned) []string {
	var blocks []string
	if len(p.Hits) > 0 {
		rows := make([][]string, len(p.Hits))
		for i, h := range p.Hits {
			rows[i] = []string{fmt.Sprintf("%s:%d", h.File, h.Line), h.Left}
		}
		blocks = append(blocks, axi.Table("pinned_text", []string{"file:line", "removed_from"}, rows))
	}
	if p.More > 0 {
		blocks = append(blocks, axi.KV("pinned_text_more", [][2]string{{"not_shown", strconv.Itoa(p.More)}}))
	}
	if p.Note != "" {
		blocks = append(blocks, axi.KV("pinned_text_note", [][2]string{{"message", "files holding removed text were not looked up: " + p.Note}}))
	}
	return blocks
}

// pinnedJSON is the part of a run's --json report that carries the same rows.
type pinnedJSON struct {
	Pinned     []pinnedHitJSON `json:"pinned"`
	PinnedMore int             `json:"pinned_more,omitempty"`
	PinnedNote string          `json:"pinned_note,omitempty"`
}

type pinnedHitJSON struct {
	File        string `json:"file"`
	Line        int    `json:"line"`
	RemovedFrom string `json:"removed_from"`
}

func pinnedJSONOf(p verify.Pinned) pinnedJSON {
	hits := make([]pinnedHitJSON, len(p.Hits))
	for i, h := range p.Hits {
		hits[i] = pinnedHitJSON{File: h.File, Line: h.Line, RemovedFrom: h.Left}
	}
	return pinnedJSON{Pinned: hits, PinnedMore: p.More, PinnedNote: p.Note}
}
