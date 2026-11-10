package main

import (
	"strconv"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/store"
	"github.com/develdeco/jig/internal/verifydeliver"
)

// init registers this file's section with `jig status` (sections.go).
func init() {
	registerStatusSection(statusSection{Order: 200, Name: "rounds", Blocks: roundsBlocks})
}

// roundColumns are the columns of the rounds table.
var roundColumns = []string{"round", "verdict", "reviewer", "harness", "model", "independent", "fix", "ask", "note", "scope", "base", "head", "files", "kept_fixed"}

// roundsBlocks is the `rounds` table of `jig status`: one row per recorded gate
// round, oldest first.
//
//	rounds[2]{round,verdict,reviewer,harness,model,independent,fix,ask,note,scope,base,head,files,kept_fixed}:
//	  1,findings,s-rev,claude,claude-opus-5,true,1,1,0,full,1a2b3c4...,5d6e7f8...,4,0
//	  2,clean,s-rev,claude,claude-opus-5,true,0,0,0,delta,5d6e7f8...,9a8b7c6...,2,0
//
// verdict is clean, findings or not-independent. reviewer is the session that
// reported the round, and harness and model are what the journal and the
// reviewer's own report say of it. independent is true, false or unknown for a
// round a reviewer ran. fix, ask and note count the findings by the status jig
// routed them to (open, asked, noted; a dismissed one counts in none). scope
// is full or delta, base and head are the shas the round reviewed, and files
// is how many paths the reviewer read, and kept_fixed how many keeps the round spent as fixed. A cell that is not known is "-". A
// ticket with no recorded round has no block.
func roundsBlocks(st *store.Store, ticket string) ([]string, error) {
	recs, err := verifydeliver.ReadRounds(st, ticket)
	if err != nil {
		return nil, err
	}
	if len(recs) == 0 {
		return nil, nil
	}
	rows := make([][]string, 0, len(recs))
	for _, r := range recs {
		files := "-"
		if r.Files >= 0 {
			files = strconv.Itoa(r.Files)
		}
		verdict := r.Verdict
		if r.CarriedFrom > 0 {
			verdict += " (carried over from round " + strconv.Itoa(r.CarriedFrom) + ")"
		}
		rows = append(rows, []string{
			strconv.Itoa(r.Round), verdict, dashed(r.Reviewer.Session), dashed(r.Harness), dashed(r.Model),
			dashed(string(r.Independent)), strconv.Itoa(r.Fix), strconv.Itoa(r.Ask), strconv.Itoa(r.Note),
			dashed(r.Scope), dashed(r.Base), dashed(r.Head), files, strconv.Itoa(len(r.KeptFixed)),
		})
	}
	blocks := []string{axi.Table("rounds", roundColumns, rows)}
	// The lines a round's change adds that name something only the store
	// holds, from each round's report: a block only when a round holds some.
	var refRows [][]string
	var rest []verifydeliver.StoreRef
	for _, r := range recs {
		for _, ref := range r.StoreRefs {
			if len(refRows) == verifydeliver.StoreRefsLimit {
				rest = append(rest, ref)
				continue
			}
			refRows = append(refRows, []string{strconv.Itoa(r.Round), ref.Repo, ref.File + ":" + strconv.Itoa(ref.Line), ref.ID})
		}
	}
	if len(refRows) > 0 {
		if len(rest) > 0 {
			refRows = append(refRows, []string{verifydeliver.StoreRefsMore(rest), "", "", ""})
		}
		blocks = append(blocks, axi.Table("store_references", []string{"round", "repo", "file:line", "id"}, refRows))
	}
	return blocks, nil
}

// dashed is s, or "-" when it is empty: the mark a table cell uses for nothing.
func dashed(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
