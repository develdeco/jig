package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/recall"
	"github.com/develdeco/jig/internal/store"
)

// The checks `jig validate` runs beyond the ticket's own files look at the
// store around it: the tickets it waits on (a refusal when one of the edges
// on the way is cut), the work that already shipped (an advisory when a
// slice goal reads like it) and the shape of the ticket's own slice graph
// (an advisory when it holds independent concerns). The last step is the
// approval a valid brief earns.

// Advisory codes: what a valid ticket is told without being refused.
const (
	// advisorySlicesUnconnected: the slices split into groups no blocked_by
	// edge connects.
	advisorySlicesUnconnected = "SLICES_UNCONNECTED"
	// advisoryWorkCovered: a slice goal comes near work that shipped.
	advisoryWorkCovered = "WORK_COVERED"
)

// advisory is something worth a planner a second look in a ticket that is
// valid all the same.
type advisory struct {
	Code    string
	Message string
	Help    string
}

// validateUpstreamEdges follows the tickets ticket waits on, and the ones they
// wait on in turn, through ticket.yaml blocked_by, and reports each edge on
// the way that names a ticket the store does not have: a ticket behind a cut
// edge never unblocks, so neither does anything that waits on it. A ticket
// that already published is not followed, since nothing it waited on matters
// any more. The edges of ticket itself are validateTicketDeps to report; a
// record that cannot be read is the problem of its own ticket.
func validateUpstreamEdges(st *store.Store, ticket string) []string {
	var problems []string
	seen := map[string]bool{ticket: true}
	queue := []string{ticket}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		deps, err := st.ReadTicketDeps(cur)
		if err != nil {
			continue
		}
		for _, d := range deps {
			if d.Ticket == "" {
				continue
			}
			next := d.Ticket
			if resolved, err := st.ResolveTicket(d.Ticket); err == nil {
				next = resolved
			}
			if requireTicket(st, next) != nil {
				if cur != ticket {
					problems = append(problems, fmt.Sprintf("upstream ticket %s, which %s waits on: blocked_by %q is not a ticket in the store", cur, ticket, d.Ticket))
				}
				continue
			}
			if seen[next] {
				continue
			}
			seen[next] = true
			if published, err := isPublished(st, next); err == nil && published {
				continue
			}
			queue = append(queue, next)
		}
	}
	return problems
}

// validateAdvisories gathers the advisories for the slices of ticket:
// independent groups of slices first, then every slice goal that reads like
// shipped work.
func validateAdvisories(st *store.Store, ticket string, slices []store.Slice) []advisory {
	var out []advisory
	// Whether the plan is one concern is a question about the slices the
	// brief planned: a fix slice is a gate round's (from_gate), joined to the
	// work it fixes by the round, not by a blocked_by edge.
	var planned []store.Slice
	for _, s := range slices {
		if s.FromGate == 0 {
			planned = append(planned, s)
		}
	}
	if groups := sliceGroups(planned); len(groups) > 1 {
		parts := make([]string, len(groups))
		for i, g := range groups {
			parts[i] = strings.Join(g, "+")
		}
		out = append(out, advisory{
			Code:    advisorySlicesUnconnected,
			Message: fmt.Sprintf("the slices split into %d groups no blocked_by edge connects (%s): they may be independent concerns", len(groups), strings.Join(parts, " | ")),
			Help:    "If they are, give each group a ticket of its own; if one builds on another, add the blocked_by edge",
		})
	}
	return append(out, coveredAdvisories(st, ticket, slices)...)
}

// sliceGroups splits slices into the groups blocked_by connects, an edge
// counting the same either way round, each group in slices.yaml order and the
// groups in the order of their first slice. A blocked_by naming no slice
// connects nothing (validate refuses it on its own).
func sliceGroups(slices []store.Slice) [][]string {
	parent := map[string]string{}
	for _, s := range slices {
		parent[s.ID] = s.ID
	}
	find := func(x string) string {
		for parent[x] != x {
			parent[x] = parent[parent[x]]
			x = parent[x]
		}
		return x
	}
	for _, s := range slices {
		for _, b := range s.BlockedBy {
			if _, ok := parent[b]; ok {
				parent[find(s.ID)] = find(b)
			}
		}
	}
	var order []string
	groups := map[string][]string{}
	for _, s := range slices {
		root := find(s.ID)
		if _, ok := groups[root]; !ok {
			order = append(order, root)
		}
		groups[root] = append(groups[root], s.ID)
	}
	out := make([][]string, len(order))
	for i, root := range order {
		out[i] = groups[root]
	}
	return out
}

// coveredAdvisories compares each slice goal of ticket to the titles of the
// tickets that shipped and of the ledger entries, and advises on the ones
// that come near (recall.CoveredBy). Records that cannot be read are skipped:
// each is the business of the validate of its own ticket.
func coveredAdvisories(st *store.Store, ticket string, slices []store.Slice) []advisory {
	goals := make([]string, len(slices))
	for i, sl := range slices {
		goals[i] = sl.Goal
	}
	shipped := shippedTicketsNear(st, goals)
	var entries []recall.LedgerEntry
	if data, err := os.ReadFile(filepath.Join(st.Root, "ledger.md")); err == nil {
		entries = recall.ParseLedger(string(data))
	}
	var out []advisory
	for _, sl := range slices {
		for _, c := range recall.CoveredBy(sl.Goal, shipped, entries, ticket) {
			out = append(out, advisory{
				Code:    advisoryWorkCovered,
				Message: fmt.Sprintf("slice %s: its goal comes near shipped %s %q (%.2f)", sl.ID, c.Ticket, c.Title, c.Score),
				Help:    fmt.Sprintf("Read what %s changed before building it again; drop the slice if it is done", c.Ticket),
			})
		}
	}
	return out
}

// shippedTicketsNear lists the tickets of the store that published and whose
// title comes near one of goals. A journal is read whole, so only the tickets
// whose title could matter have theirs read.
func shippedTicketsNear(st *store.Store, goals []string) []recall.Ticket {
	ids, err := st.TicketIDs()
	if err != nil {
		return nil
	}
	var out []recall.Ticket
	for _, id := range ids {
		rec, err := st.ReadTicket(id)
		if err != nil || !comesNearAny(rec.Title, goals) {
			continue
		}
		if published, err := isPublished(st, id); err != nil || !published {
			continue
		}
		out = append(out, recall.Ticket{ID: id, Title: rec.Title, Shipped: true})
	}
	return out
}

// comesNearAny reports whether title comes near any of goals.
func comesNearAny(title string, goals []string) bool {
	for _, g := range goals {
		if recall.Similarity(g, title) >= recall.Threshold {
			return true
		}
	}
	return false
}

// advisoryTable renders the advisories as a table, "" when there are none.
func advisoryTable(as []advisory) string {
	if len(as) == 0 {
		return ""
	}
	rows := make([][]string, len(as))
	for i, a := range as {
		rows[i] = []string{a.Code, a.Message, a.Help}
	}
	return axi.Table("advisories", []string{"code", "message", "help"}, rows)
}

// Approval results: what recording the approval of a valid brief did, or
// that --check left it alone.
const (
	approvalRecorded  = "recorded"
	approvalUnchanged = "unchanged"
	approvalChecked   = "not recorded (--check)"
)

// provenanceRank orders who an approval speaks for: the word of a person is
// worth more than an agent relaying it.
func provenanceRank(p string) int {
	switch p {
	case "person":
		return 2
	case "agent":
		return 1
	}
	return 0
}

// recordApproval records that the brief of ticket, whose hash is sum, was found
// valid by the person whose git identity the store resolves, speaking as
// provenance: the approval file, an approval line in the journal, and a push.
// A brief already approved as it is, by a word at least as strong, is left as
// it is. The identity is looked up before anything is written, so a machine
// with none is refused (IDENTITY_REQUIRED) with the store untouched.
func recordApproval(e env, st *store.Store, ticket, sum, provenance string) (store.Approval, string, error) {
	if prev, ok, err := st.ReadApproval(ticket); err == nil && ok &&
		strings.EqualFold(prev.BriefSHA256, sum) && provenanceRank(prev.Provenance) >= provenanceRank(provenance) {
		return prev, approvalUnchanged, nil
	}
	who, err := gitx.ResolveIdentity(st.Root)
	if err != nil {
		return store.Approval{}, "", err
	}
	a := store.Approval{
		By: who.Name, Email: who.Email,
		At:          e.now().UTC().Format(time.RFC3339),
		BriefSHA256: sum, Provenance: provenance,
	}
	if err := st.WriteApproval(ticket, a); err != nil {
		return store.Approval{}, "", err
	}
	err = journal.Append(st, ticket, journal.Line{
		TS: stamp(e), Event: journal.EventApproval, Provenance: provenance,
		Ref: sum[:12], Summary: flatten(fmt.Sprintf("%s <%s>", a.By, a.Email), summaryMax),
	})
	if err != nil {
		return store.Approval{}, "", err
	}
	if err := st.Push(fmt.Sprintf("%s: brief approved", ticket)); err != nil {
		return store.Approval{}, "", err
	}
	return a, approvalRecorded, nil
}
