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
var roundColumns = []string{"round", "verdict", "reviewer", "harness", "model", "independent", "fix", "ask", "note", "scope", "base", "head", "files", "kept_fixed", "probes", "survived", "skipped", "unprobed"}

// roundsBlocks is the `rounds` table of `jig status`: one row per recorded gate
// round, oldest first.
//
//	rounds[2]{round,verdict,reviewer,harness,model,independent,fix,ask,note,scope,base,head,files,kept_fixed,probes,survived,skipped,unprobed}:
//	  1,findings,s-rev,claude,claude-opus-5,true,1,1,0,full,1a2b3c4...,5d6e7f8...,4,0,3,1,0,2
//	  2,clean,s-rev,claude,claude-opus-5,true,0,0,0,delta,5d6e7f8...,9a8b7c6...,2,0,0,0,1,-
//
// verdict is clean, findings or not-independent. reviewer is the session that
// reported the round, and harness and model are what the journal and the
// reviewer's own report say of it. independent is true, false or unknown for a
// round a reviewer ran. fix, ask and note count the findings by the status jig
// routed them to (open, asked, noted; a dismissed one counts in none). scope
// is full or delta, base and head are the shas the round reviewed, and files
// is how many paths the reviewer read, and kept_fixed how many keeps the round spent as fixed.
// probes, survived, skipped and unprobed are the round's ledger (ADR 0036):
// how many probes it recorded, how many of them survived, how many skips, and
// how many hunks it left unprobed ("-" when the round did not record that
// list). A cell that is not known is "-", all four for a round no reviewer
// ran. A ticket with no recorded round has no block.
//
// Three more blocks print the ledger itself, each only when it has a row:
// probes (every probe of every round, oldest round first, in the order
// taken), probe_skips (every skip) and unprobed (the latest recording round's
// hunks).
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
		probes, survived, skipped, unprobed := "-", "-", "-", "-"
		if r.Files >= 0 {
			survivors := 0
			for _, p := range r.Probes {
				if p.Outcome == verifydeliver.ProbeSurvived {
					survivors++
				}
			}
			probes, survived, skipped = strconv.Itoa(len(r.Probes)), strconv.Itoa(survivors), strconv.Itoa(len(r.Skipped))
			if r.UnprobedRecorded {
				unprobed = strconv.Itoa(len(r.Unprobed))
			}
		}
		rows = append(rows, []string{
			strconv.Itoa(r.Round), verdict, dashed(r.Reviewer.Session), dashed(r.Harness), dashed(r.Model),
			dashed(string(r.Independent)), strconv.Itoa(r.Fix), strconv.Itoa(r.Ask), strconv.Itoa(r.Note),
			dashed(r.Scope), dashed(r.Base), dashed(r.Head), files, strconv.Itoa(len(r.KeptFixed)),
			probes, survived, skipped, unprobed,
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
	return append(blocks, ledgerBlocks(recs)...), nil
}

// ledgerBlocks are the probes, probe_skips and unprobed tables: the ledger as
// the rounds recorded it, with no verdict in it. A survivor is not a finding
// and is not listed twice; findings keep their own table.
func ledgerBlocks(recs []verifydeliver.RoundRecord) []string {
	var probeRows, skipRows, unprobedRows [][]string
	latest := -1
	for i, r := range recs {
		for _, p := range r.Probes {
			probeRows = append(probeRows, []string{
				strconv.Itoa(r.Round), p.Outcome, p.File + ":" + strconv.Itoa(p.Line), p.Seam, p.Mutation, p.Command,
			})
		}
		for _, sk := range r.Skipped {
			skipRows = append(skipRows, []string{strconv.Itoa(r.Round), sk.File, skipRange(sk), sk.Why})
		}
		if r.UnprobedRecorded {
			latest = i
		}
	}
	if latest >= 0 {
		for _, h := range recs[latest].Unprobed {
			unprobedRows = append(unprobedRows, []string{
				strconv.Itoa(recs[latest].Round), h.File, strconv.Itoa(h.From), strconv.Itoa(h.To),
			})
		}
	}
	var blocks []string
	if len(probeRows) > 0 {
		blocks = append(blocks, axi.Table("probes", []string{"round", "outcome", "file:line", "seam", "mutation", "command"}, probeRows))
	}
	if len(skipRows) > 0 {
		blocks = append(blocks, axi.Table("probe_skips", []string{"round", "file", "range", "why"}, skipRows))
	}
	if len(unprobedRows) > 0 {
		blocks = append(blocks, axi.Table("unprobed", []string{"round", "file", "from", "to"}, unprobedRows))
	}
	return blocks
}

// skipRange is a skip's lines as from-to, or "whole file" for one that names
// no lines.
func skipRange(sk verifydeliver.SkipRecord) string {
	if sk.From == 0 && sk.To == 0 {
		return "whole file"
	}
	return strconv.Itoa(sk.From) + "-" + strconv.Itoa(sk.To)
}

// dashed is s, or "-" when it is empty: the mark a table cell uses for nothing.
func dashed(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
