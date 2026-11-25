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
var roundColumns = []string{"round", "verdict", "reviewer", "harness", "model", "independent", "fix", "ask", "note", "scope", "base", "head", "files", "kept_fixed", "probes", "survived"}

// roundsBlocks is the `rounds` table of `jig status`: one row per recorded gate
// round, oldest first.
//
//	rounds[2]{round,verdict,reviewer,harness,model,independent,fix,ask,note,scope,base,head,files,kept_fixed,probes,survived}:
//	  1,findings,s-rev,claude,claude-opus-5,true,1,1,0,full,1a2b3c4...,5d6e7f8...,4,0,3,1
//	  2,clean,s-rev,claude,claude-opus-5,true,0,0,0,delta,5d6e7f8...,9a8b7c6...,2,0,0,0
//
// verdict is clean, findings or not-independent. reviewer is the session that
// reported the round, and harness and model are what the journal and the
// reviewer's own report say of it. independent is true, false or unknown for a
// round a reviewer ran. fix, ask and note count the findings by the status jig
// routed them to (open, asked, noted; a dismissed one counts in none). scope
// is full or delta, base and head are the shas the round reviewed, and files
// is how many paths the reviewer read, and kept_fixed how many keeps the round spent as fixed.
// probes and survived are how many probes the round recorded and how many of
// them survived (ADR 0036). A cell that is not known is "-", both for a round
// no reviewer ran. A ticket with no recorded round has no block.
//
// One more block prints the probes themselves, only when it has a row: probes,
// every probe of every round, oldest round first, in the order taken.
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
		probes, survived := "-", "-"
		if r.Files >= 0 {
			survivors := 0
			for _, p := range r.Probes {
				if p.Outcome == verifydeliver.ProbeSurvived {
					survivors++
				}
			}
			probes, survived = strconv.Itoa(len(r.Probes)), strconv.Itoa(survivors)
		}
		rows = append(rows, []string{
			strconv.Itoa(r.Round), verdict, dashed(r.Reviewer.Session), dashed(r.Harness), dashed(r.Model),
			dashed(string(r.Independent)), strconv.Itoa(r.Fix), strconv.Itoa(r.Ask), strconv.Itoa(r.Note),
			dashed(r.Scope), dashed(r.Base), dashed(r.Head), files, strconv.Itoa(len(r.KeptFixed)),
			probes, survived,
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
	return append(blocks, probeBlocks(recs)...), nil
}

// probeBlocks is the probes table, when a round recorded a probe: the probes as
// the rounds recorded them, with no verdict in it. A survivor is not a finding
// and is not listed twice; findings keep their own table.
func probeBlocks(recs []verifydeliver.RoundRecord) []string {
	var rows [][]string
	for _, r := range recs {
		for _, p := range r.Probes {
			rows = append(rows, []string{
				strconv.Itoa(r.Round), p.Outcome, p.File + ":" + strconv.Itoa(p.Line), p.Seam, p.Mutation, p.Command,
			})
		}
	}
	if len(rows) == 0 {
		return nil
	}
	return []string{axi.Table("probes", []string{"round", "outcome", "file:line", "seam", "mutation", "command"}, rows)}
}

// dashed is s, or "-" when it is empty: the mark a table cell uses for nothing.
func dashed(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
