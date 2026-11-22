package main

import (
	"encoding/json"
	"fmt"
	"strings"

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

// withItemBlocks lays a ref's answer on the blocks of the ticket or chart it
// resolved to: ref, repo and bound at the top of the next block, and the
// advisory a bare number owes after the rest, before the help.
func withItemBlocks(blocks []string, it *locate.Item) []string {
	if it == nil {
		return blocks
	}
	out := make([]string, 0, len(blocks)+1)
	done := false
	for _, b := range blocks {
		if !done && strings.HasPrefix(b, "next:\n") {
			head, rest, _ := strings.Cut(b, "\n")
			var lines []string
			for _, p := range itemPairs(it) {
				lines = append(lines, "  "+p[0]+": "+axi.Quote(p[1]))
			}
			b = head + "\n" + strings.Join(lines, "\n") + "\n" + rest
			done = true
		}
		out = append(out, b)
	}
	if sb := shadowBlock(it); sb != "" {
		at := len(out)
		if at > 0 && strings.HasPrefix(out[at-1], "help[") {
			at--
		}
		out = append(out[:at], append([]string{sb}, out[at:]...)...)
	}
	return out
}

// withItemJSON lays ref, repo and bound (and the advisory a bare number owes)
// at the top of the JSON object of the ticket or chart a ref resolved to.
func withItemJSON(data []byte, it *locate.Item) ([]byte, error) {
	if it == nil {
		return data, nil
	}
	if len(data) < 2 || data[0] != '{' {
		return nil, fmt.Errorf("item ref: the answer is not a JSON object")
	}
	fields := []struct {
		key string
		val any
	}{{"ref", it.Ref}, {"repo", it.Repo}, {"bound", itemBoundOf(it)}}
	if it.Shadow != "" {
		fields = append(fields, struct {
			key string
			val any
		}{"advisories", []string{it.Shadow}})
	}
	out := []byte{'{'}
	for i, f := range fields {
		v, err := json.Marshal(f.val)
		if err != nil {
			return nil, err
		}
		if i > 0 {
			out = append(out, ',')
		}
		out = append(out, fmt.Sprintf("%q:%s", f.key, v)...)
	}
	if data[1] != '}' {
		out = append(out, ',')
	}
	return append(out, data[1:]...), nil
}
