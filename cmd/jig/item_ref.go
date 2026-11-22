package main

import (
	"fmt"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/locate"
)

// itemBoundJSON is how a tracker item's bound is carried in JSON: the owner's
// kind and id when the item is bound, kind "no" when it is not.
type itemBoundJSON struct {
	Kind   string `json:"kind"`
	Ticket string `json:"ticket,omitempty"`
}

// itemRefJSON is what a command that was handed a tracker item's ref adds to
// its answer: the bare number, the repo and what the item is bound to.
type itemRefJSON struct {
	Ref   string        `json:"ref"`
	Repo  string        `json:"repo"`
	Bound itemBoundJSON `json:"bound"`
}

func itemBoundOf(it *locate.Item) itemBoundJSON {
	if !it.Bound {
		return itemBoundJSON{Kind: "no"}
	}
	return itemBoundJSON{Kind: it.Kind, Ticket: it.Ticket}
}

func itemRefOf(it *locate.Item) *itemRefJSON {
	if it == nil {
		return nil
	}
	return &itemRefJSON{Ref: it.Ref, Repo: it.Repo, Bound: itemBoundOf(it)}
}

// itemPairs are the lines a ref adds to a block: ref (the bare number), repo
// and bound ("issue <ID>", or the owner's kind, or "no").
func itemPairs(it *locate.Item) [][2]string {
	b := "no"
	if it.Bound {
		b = it.Kind + " " + it.Ticket
	}
	return [][2]string{{"ref", it.Ref}, {"repo", it.Repo}, {"bound", b}}
}

// itemAsk is how a session asks for the item again: the bare number, or for
// an item of another repo its URL, which is safe unquoted where owner/repo#n
// and #n are not everywhere.
func itemAsk(it *locate.Item) string {
	if it.Other {
		return fmt.Sprintf("https://github.com/%s/issues/%s", it.Repo, it.Ref)
	}
	return it.Ref
}

// shadowBlock is the advisories block of a bare number that hides a chart or
// an earlier ticket id, "" when it hides none.
func shadowBlock(it *locate.Item) string {
	if it == nil || it.Shadow == "" {
		return ""
	}
	return axi.Block{Header: "advisories[1]:", Lines: []string{"  " + it.Shadow}}.String()
}
