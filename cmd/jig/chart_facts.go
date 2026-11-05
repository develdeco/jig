package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/phase"
	"github.com/develdeco/jig/internal/project"
	"github.com/develdeco/jig/internal/recall"
	"github.com/develdeco/jig/internal/store"
)

// chartMap is what a chart's map.md says, read for the chart's standing: its
// sections, and whether there is a map.md at all. jig reads it, never writes it.
type chartMap struct {
	recall.MapSections
	Exists bool
}

// deriveChart is where chart name stands (internal/phase's DeriveChart) over
// the facts chartFacts gathered, with the sections of its map.md. Every command
// that says where a chart stands asks it, so `jig next <chart>`, `jig status
// <chart>` and the TUI's chart screen agree.
func deriveChart(st *store.Store, cfg project.Config, mp project.MachineProject, name string, merges mergeSource) (phase.Chart, chartMap, error) {
	return deriveChartNamed(st, cfg, mp, name, merges, nil)
}

// deriveChartNamed is deriveChart over the tickets a prompt named (ids of the
// chart, checked by namedTickets); none means the whole chart.
func deriveChartNamed(st *store.Store, cfg project.Config, mp project.MachineProject, name string, merges mergeSource, named []string) (phase.Chart, chartMap, error) {
	facts, err := chartFacts(st, cfg, mp, name, merges)
	if err != nil {
		return phase.Chart{}, chartMap{}, err
	}
	facts.Named = named
	m := chartMap{MapSections: recall.ParseMap("")}
	if data, err := os.ReadFile(st.ChartMapFile(name)); err == nil {
		m = chartMap{MapSections: recall.ParseMap(string(data)), Exists: true}
	}
	return phase.DeriveChart(facts), m, nil
}

// chartFacts reads each entry of chart name's tickets.yaml with what the store
// says of it. A graduated entry takes its edges and rank from its ticket, the
// source of truth once it is minted (graduate only advises on a drift); one with
// no id yet takes them from the chart's refs, which resolve as graduate
// resolves them, and a ref that does not makes the entry a row with its problem.
// A ticket that cannot be read is a row with its problem too, never a reason to
// hide the chart. Each edge is judged as a claim judges it
// (blockerProblem), so a stacked edge is met by the parent's clean gate round
// and a merged one by what merges says. A published ticket stacked on another
// is read with its stack plan (derivePhasePlanned): one whose branch is owed a
// merge is work of the run, not done. One that is not owed it yet, because its
// parent's pull request is still open, is done and waits on that merge: its
// stacked needs name the parent's open pull requests (layOpenParents).
func chartFacts(st *store.Store, cfg project.Config, mp project.MachineProject, name string, merges mergeSource) (phase.ChartFacts, error) {
	entries, err := st.ReadChart(name)
	if err != nil {
		return phase.ChartFacts{}, &axi.Error{
			Msg:  fmt.Sprintf("failed to read chart %q: %v", name, err),
			Code: "VALIDATION_ERROR",
			Help: []string{fmt.Sprintf("Check that charts/%s/tickets.yaml exists and is valid YAML", name)},
		}
	}
	// The refs of a graduated entry are history: its ticket holds the edges.
	scoped := make([]store.ChartEntry, len(entries))
	copy(scoped, entries)
	for i := range scoped {
		if scoped[i].ID != "" {
			scoped[i].BlockedBy = nil
		}
	}
	// A ref that does not resolve is its entry's problem, never a reason to
	// refuse the chart: the entry is a blocked row that says what is wrong, and
	// the verdict names `jig validate`, which lists every problem of the chart.
	refs, refProblems := resolveChartRefs(st, name, scoped)
	problemOf := map[int][]string{}
	for _, p := range refProblems {
		problemOf[p.Entry] = append(problemOf[p.Entry], p.Text())
	}

	facts := phase.ChartFacts{Name: name}
	for i, e := range entries {
		pos := i + 1
		ce := phase.ChartEntry{Pos: pos, ID: e.ID, Title: e.Title}
		if e.ID == "" {
			if reasons := problemOf[pos]; len(reasons) > 0 {
				ce.Problem = strings.Join(reasons, "; ")
			}
			// The needs are the refs that did resolve.
			for _, r := range refs[i] {
				if r.knownID != "" {
					ce.Needs = append(ce.Needs, chartNeed(st, fmt.Sprintf("%s#%d", name, pos), r.knownID, r.kind, merges))
					continue
				}
				ce.Needs = append(ce.Needs, phase.ChartNeed{
					Pos: r.pending, Kind: r.kind,
					Why: fmt.Sprintf("%s#%d waits for entry #%d, which has no ticket yet", name, pos, r.pending),
				})
			}
			facts.Entries = append(facts.Entries, ce)
			continue
		}
		rec, err := st.ReadTicket(e.ID)
		if err != nil {
			ce.Problem = err.Error()
			facts.Entries = append(facts.Entries, ce)
			continue
		}
		if rec.Title != "" {
			ce.Title = rec.Title
		}
		ce.Rank = rec.Rank
		for _, b := range rec.BlockedBy {
			blocker := b.Ticket
			if resolved, err := st.ResolveTicket(b.Ticket); err == nil {
				blocker = resolved
			}
			ce.Needs = append(ce.Needs, chartNeed(st, e.ID, blocker, b.Kind, merges))
		}
		if t, err := derivePhasePlanned(st, cfg, mp, e.ID, rec, merges); err != nil {
			ce.Problem = err.Error()
		} else {
			ce.Ticket = &t
			if t.Code == phase.CodeDone {
				layOpenParents(st, ce.Needs, e.ID, merges)
			}
		}
		facts.Entries = append(facts.Entries, ce)
	}
	return facts, nil
}

// layOpenParents names, on each stacked need of published ticket, the pull
// requests of the parent that have not merged. The ticket is done, but the
// parent's merge owes its pull request a retarget (ADR 0038), a change the chart
// has to be read again for: a need with pull requests is where the chart's run
// ends. The parent's gate round does not matter here (a re-gate of the parent
// leaves the need unmet for a while), but a parent that is closed is not built
// on (stack.Read), so it owes nothing. Nor does a ticket whose own work has
// merged, or a parent with no recorded pull request, which gives nothing to
// wait on. It reads what the needs were judged by (Of is memoized), so it asks
// the host no more than judging them did.
func layOpenParents(st *store.Store, needs []phase.ChartNeed, ticket string, merges mergeSource) {
	if merges == nil {
		return
	}
	if own, err := merges.Of(ticket); err != nil || own.Met() {
		return
	}
	for i, n := range needs {
		if n.Kind != "stacked" {
			continue
		}
		if parent, err := st.ReadTicket(n.Ticket); err != nil || parent.Closed != nil {
			continue
		}
		if prs := unmergedPRs(merges, n.Ticket); len(prs) > 0 {
			needs[i].PRs = prs
			needs[i].Why = fmt.Sprintf("%s is stacked on %s, whose work has not merged: once it has, the pull request of %s is retargeted", ticket, n.Ticket, ticket)
		}
	}
}

// chartNeed is the edge from dependent (a ticket, or a chart entry that has no
// ticket yet) to blocker, judged as a claim judges it. A blocker the store
// cannot read is a need that is never met.
func chartNeed(st *store.Store, dependent, blocker, kind string, merges mergeSource) phase.ChartNeed {
	if kind == "" {
		kind = "merged"
	}
	n := phase.ChartNeed{Ticket: blocker, Kind: kind}
	p, err := blockerProblem(st, dependent, blocker, kind, claimDeps{merges: merges})
	switch {
	case err != nil:
		n.Code, n.Why = phase.CodeNeedUnknown, fmt.Sprintf("%s waits for %s, which cannot be read: %v", dependent, blocker, err)
	case p == nil:
		n.Met = true
	default:
		n.Code, n.Why, n.PRs = p.Code, p.Message, mergePRs(merges, blocker, kind)
	}
	return n
}

// wordIsATicket reports whether word reads as a ticket of the store and so
// never as a chart: an alias two tickets claim, an id jig cannot work, or a
// ticket the store has under that id or alias. chartNamed and `jig chart new`
// share this one rule, so a chart made under a name is a chart reached by it.
func wordIsATicket(st *store.Store, word string) bool {
	id, err := st.ResolveTicket(word)
	return err != nil || checkTicketID(id) != nil || st.TicketExists(id)
}

// chartNamed is the chart a word names: a ticket of that name (or alias) wins,
// then a chart that has a tickets.yaml. A ticket id is KEY-n and a chart name a
// word, so the two seldom clash; when they do the ticket is the answer.
func chartNamed(st *store.Store, word string) (string, bool) {
	if word == "" {
		return "", false
	}
	if wordIsATicket(st, word) {
		return "", false
	}
	if validateChartName(word) != nil {
		return "", false
	}
	if _, err := os.Stat(st.ChartFile(word)); err != nil {
		return "", false
	}
	return word, true
}

// orChartHint is err, the refusal for a word that is no ticket of the store,
// made to name charts too when the store has any: the word may have been meant
// as one. Any other error, or a store with no charts, is err as it was.
func orChartHint(st *store.Store, word string, err error) error {
	if err == nil {
		return err
	}
	names, cerr := st.ChartNames()
	if cerr != nil || len(names) == 0 || checkTicketID(word) != nil || st.TicketExists(word) {
		return err
	}
	return &axi.Error{
		Msg:  fmt.Sprintf("no ticket or chart named %q in the store at %s", word, st.Root),
		Code: "VALIDATION_ERROR",
		Help: []string{
			"Run `jig next` to list the tickets",
			"The charts are " + strings.Join(names, ", ") + ": `jig next <chart>` says where one stands",
		},
	}
}
