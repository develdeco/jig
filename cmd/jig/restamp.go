package main

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/store"
)

// restampResult is what a restamp did: a row per restamped slice (id, state,
// the headings it is now stamped from) and a row per stale hash it could not
// resolve (slice id, hash).
type restampResult struct {
	restamped  [][]string
	unresolved [][]string
}

// restampTicket implements the work of `jig validate <T> --restamp`: every
// slice whose from_brief cites a hash no current brief section has gets the
// current hash of the section that hash was the hash of, found in the store's
// history of brief.md. The slice's state, attempts and verified commit are
// not touched, so a green slice stays green; `jig requeue --from-brief-diff`
// is the way to rebuild one, and it runs first because it needs the stale
// stamp to find the slice.
//
// A slice that is building refuses the whole restamp, writing nothing: its
// builder works against the brief it was handed. A slice with a hash that
// history cannot resolve, or whose heading is gone, is left as it is and
// listed as unresolved; the rest are restamped.
func restampTicket(st *store.Store, ticket string) (restampResult, error) {
	var res restampResult
	brief, err := os.ReadFile(filepath.Join(st.TicketDir(ticket), "brief.md"))
	if err != nil {
		if os.IsNotExist(err) {
			return res, &axi.Error{
				Msg:  fmt.Sprintf("ticket %s has no brief.md, so there are no brief sections to restamp from", ticket),
				Code: "VALIDATION_ERROR",
			}
		}
		return res, err
	}
	current := store.BriefSectionHashes(brief)
	headingOf := map[string]string{}
	for h, hash := range current {
		headingOf[hash] = h
	}
	all, err := st.ReadSlices(ticket)
	if err != nil {
		return res, err
	}

	var stale []store.Slice
	var staleHashes []string
	for _, sl := range all {
		isStale := false
		for _, h := range sl.FromBrief {
			if _, ok := headingOf[h]; !ok {
				isStale = true
				staleHashes = append(staleHashes, h)
			}
		}
		if isStale {
			stale = append(stale, sl)
		}
	}
	states := map[string]string{}
	for _, sl := range stale {
		state, err := st.ReadSliceState(ticket, sl.ID)
		if err != nil {
			return res, err
		}
		states[sl.ID] = state.State
		if state.State == "building" {
			return res, &axi.Error{
				Msg:  fmt.Sprintf("slice %s is building, so its stamp is not restamped under its builder", sl.ID),
				Code: "SLICE_BUILDING",
				Help: []string{
					fmt.Sprintf("Release it with `jig claim %s --slice %s --release`, or wait for its builder; `jig requeue %s --from-brief-diff` tells a builder the brief changed", ticket, sl.ID, ticket),
				},
			}
		}
	}
	if len(stale) == 0 {
		return res, nil
	}

	oldHeading := st.BriefHeadings(ticket, staleHashes)
	stamps := map[string][]string{}
	var journals []journal.Line
	for _, sl := range stale {
		var next []string
		ok := true
		for _, h := range sl.FromBrief {
			if _, isCurrent := headingOf[h]; isCurrent {
				next = append(next, h)
				continue
			}
			heading, found := oldHeading[h]
			newHash, exists := current[heading]
			if !found || !exists {
				ok = false
				res.unresolved = append(res.unresolved, []string{sl.ID, h})
				continue
			}
			next = append(next, newHash)
		}
		if !ok {
			continue
		}
		next = dedupe(next)
		stamps[sl.ID] = next
		var headings []string
		for _, h := range next {
			headings = append(headings, headingOf[h])
		}
		sections := strings.Join(headings, ", ")
		res.restamped = append(res.restamped, []string{sl.ID, states[sl.ID], sections})
		journals = append(journals, journal.Line{Slice: sl.ID, Event: journal.EventRestamp, Ref: sl.ID, Summary: sections})
	}
	if len(stamps) == 0 {
		return res, nil
	}
	if err := st.RestampSlices(ticket, stamps); err != nil {
		return res, err
	}
	for _, line := range journals {
		if err := journal.Append(st, ticket, line); err != nil {
			return res, err
		}
	}
	if err := st.Push(fmt.Sprintf("%s: restamp slices from the brief", ticket)); err != nil {
		return res, err
	}
	return res, nil
}

// dedupe drops repeats, keeping the first of each.
func dedupe(in []string) []string {
	var out []string
	for _, s := range in {
		if !slices.Contains(out, s) {
			out = append(out, s)
		}
	}
	return out
}
