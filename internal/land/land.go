// Package land is the facts of the Land phase: what a ticket's landing reads
// and what it writes. Landing is the merge of a published ticket's pull
// requests, which the session makes at the person's word and jig only names and
// records (ADR 0061): Judge says whether the landing is in order, the refusal
// with the tickets that must land first or the merge to make, and Landed says
// what the journal already records.
//
// Order is the chart's blocked_by edges and the parent links, nothing else, and
// it is one list, Rules: a later rule is one more entry here, and `jig land`
// does not change.
package land

import (
	"fmt"
	"strings"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/project"
	"github.com/develdeco/jig/internal/ready"
	"github.com/develdeco/jig/internal/repohost"
	"github.com/develdeco/jig/internal/stack"
	"github.com/develdeco/jig/internal/store"
	"github.com/develdeco/jig/internal/verifydeliver"
)

// The codes of a refusal to land.
const (
	// CodeNotPublished: the ticket has not published, a repo has no pull
	// request, or the ticket owes a restack or retarget (it is behind).
	CodeNotPublished = "LAND_NOT_PUBLISHED"
	// CodeWaits: a ticket that must land first has not.
	CodeWaits = "LAND_WAITS"
	// CodeBaseDiffers: the pull request's base is not the stack plan's
	// PRTarget; `jig publish` points it right.
	CodeBaseDiffers = "LAND_BASE_DIFFERS"
	// CodeClosed: the recorded pull request was closed without merging.
	CodeClosed = "LAND_CLOSED"
)

// Merges says whether the work of a ticket has merged: internal/mergefacts'
// Reader is one.
type Merges interface {
	Of(ticket string) (ready.Merge, error)
	OfFor(ticket, issue string) (ready.Merge, error)
}

// Deps is what the facts are read from.
type Deps struct {
	Store  *store.Store
	Cfg    project.Config
	Clones map[string]string
	Merges Merges
	// Status reads one pull request from the host (repohost.PRStatus with the
	// command's gh); nil, or known false, is a host jig cannot ask.
	Status func(url string) (repohost.Status, bool)
	// Methods reads the merge methods the pull request's repo allows
	// (repohost.AllowedMergeMethods); nil or known false is not known.
	Methods func(url string) (repohost.MergeMethods, bool)
	// Plan reads a ticket's stack plan for an issue (stack.ReadFor over the
	// store, the project and the clones); nil reads it so.
	Plan func(ticket, issue string) (stack.Plan, error)
}

// Refusal is a landing that is out of order. Nothing is written for one.
type Refusal struct {
	Code string
	Msg  string
	// First are the tickets that must land first, in the order the rules found
	// them.
	First []string
	Help  []string
}

// AxiError is the refusal as the command line shows it.
func (r *Refusal) AxiError() *axi.Error {
	return &axi.Error{Msg: r.Msg, Code: r.Code, Help: r.Help}
}

// Landing is one landed line: a repo's pull request, merged.
type Landing struct {
	// Repo is the repo's name in a project of several repos, "" in a project of
	// one.
	Repo string
	// PR is the pull request, Base the branch it merged into, Into the
	// container it went into ("" for the target) and Commit the merge commit
	// when the host gave one.
	PR, Base, Into, Commit string
	// Provenance is journal.ProvenanceAgent, ProvenancePerson or
	// ProvenanceHost.
	Provenance string
}

// Line is the journal line that records l for ticket.
func (l Landing) Line(ticket string) journal.Line {
	return journal.Line{
		Ticket: ticket, Event: journal.EventLanded, Repo: l.Repo, URL: l.PR,
		Base: l.Base, Into: l.Into, Commit: l.Commit, Provenance: l.Provenance,
	}
}

// Row is one merge the session is to make: a pull request not yet merged.
type Row struct {
	Repo, PR, Base string
	// Method is squash, merge or rebase, and Command the `gh pr merge` that
	// makes it.
	Method, Command string
	// Conflicting says the host reports the pull request CONFLICTING; Resolve
	// is then what resolves it.
	Conflicting bool
	Resolve     string
}

// Verdict is what Judge says of a landing.
type Verdict struct {
	Ticket string
	// Into is the container the landing is into, "" for the target.
	Into string
	// Refusal is set when the landing is out of order; nothing else is then
	// said.
	Refusal *Refusal
	// Merge are the merges to make, one per repo not yet merged.
	Merge []Row
	// Record are the landed lines to write: a repo whose pull request is merged
	// on the host and has no line. Their Provenance is empty; the caller says
	// who made them known.
	Record []Landing
	// Landed are the lines already on record, one per repo.
	Landed []Landing
	// Done says every repo of the ticket has a line.
	Done bool
	// Notes say what could not be read, never a refusal.
	Notes []string
}

// Options are what a landing is asked with.
type Options struct {
	// Issue is the issue a shared task is landed into; "" for any other.
	Issue string
	// Method overrides the merge method ("squash", "merge" or "rebase").
	Method string
}

// Scene is the ticket a rule reads.
type Scene struct {
	Deps
	Ticket string
	Rec    store.Ticket
	// Into is the container the ticket lands into, "" for the target.
	Into string
}

// Before is a ticket that must land before another, with why.
type Before struct {
	Ticket, Why string
	// Into is the container the ticket is to land into, for the command that
	// lands it: a task two issues share lands per issue.
	Into string
}

// Rule answers "what must land before this".
type Rule struct {
	Name   string
	Before func(Scene) ([]Before, error)
}

// Rules is the order of landing, one entry per rule. A rule that is not here
// is no rule: `jig land` reads this list and nothing else.
var Rules = []Rule{
	{Name: "merged dependency", Before: mergedDependencies},
	{Name: "stacked parent", Before: stackedParents},
	{Name: "tasks of a multi-task", Before: tasksOfMultiTask},
	{Name: "tickets of an issue", Before: ticketsOfIssue},
}

// intoOf is the container ticket lands into. A ticket with at most one is
// landed into it (the target when it has none); a shared task needs issue to be
// one of its issues, and is refused with SHARED_TASK_ISSUE_NEEDED otherwise,
// listing each issue with the pull request it has or none.
func intoOf(st *store.Store, ticket string, rec store.Ticket, issue string) (string, error) {
	containers, err := st.ContainersOf(ticket, rec)
	if err != nil {
		return "", fmt.Errorf("land: read the containers of %s: %w", ticket, err)
	}
	if len(containers) <= 1 {
		if issue != "" && (len(containers) == 0 || containers[0] != issue) {
			return "", &axi.Error{
				Msg:  fmt.Sprintf("%s is not landed into %s: it is not a task of that issue", ticket, issue),
				Code: verifydeliver.SharedTaskIssueNeeded,
				Help: []string{fmt.Sprintf("Land %s without --issue: only a task two issues share takes it.", ticket)},
			}
		}
		if len(containers) == 1 {
			return containers[0], nil
		}
		return "", nil
	}
	for _, c := range containers {
		if c == issue {
			return issue, nil
		}
	}
	state := make([]string, len(containers))
	for i, c := range containers {
		lines, err := journal.ReadFor(st, ticket, c)
		if err != nil {
			return "", err
		}
		state[i] = c + ": no pull request"
		for _, l := range lines {
			if l.Event == journal.EventPr && l.URL != "" {
				state[i] = c + ": " + l.URL
			}
		}
	}
	msg := fmt.Sprintf("%s is a shared task: say which issue to land it into with --issue", ticket)
	if issue != "" {
		msg = fmt.Sprintf("%s is a shared task, and %s is not one of its issues", ticket, issue)
	}
	return "", &axi.Error{
		Msg:  msg + " (" + strings.Join(state, "; ") + ")",
		Code: verifydeliver.SharedTaskIssueNeeded,
		Help: []string{fmt.Sprintf("Run `jig land %s --issue <ID>`, one of %s.", ticket, strings.Join(containers, ", "))},
	}
}

// Landed is what the journal records of ticket's landing, one Landing per repo
// (the latest line of each), for the issue asked about: a shared task needs
// issue (SHARED_TASK_ISSUE_NEEDED otherwise), and one issue's landing says
// nothing of the other's. A ticket that is not landed has none.
func Landed(st *store.Store, ticket, issue string) ([]Landing, error) {
	rec, err := st.ReadTicket(ticket)
	if err != nil {
		return nil, err
	}
	into, err := intoOf(st, ticket, rec, issue)
	if err != nil {
		return nil, err
	}
	return landedLines(st, ticket, into)
}

// landedLines is the latest landed line of each repo, for the container into.
func landedLines(st *store.Store, ticket, into string) ([]Landing, error) {
	lines, err := journal.ReadFor(st, ticket, into)
	if err != nil {
		return nil, err
	}
	var out []Landing
	for _, l := range lines {
		if l.Event != journal.EventLanded {
			continue
		}
		got := Landing{Repo: l.Repo, PR: l.URL, Base: l.Base, Into: l.Into, Commit: l.Commit, Provenance: l.Provenance}
		replaced := false
		for i := range out {
			if out[i].Repo == got.Repo {
				out[i], replaced = got, true
			}
		}
		if !replaced {
			out = append(out, got)
		}
	}
	return out, nil
}

// resolve is the current id of a ticket named by a blocker.
func resolve(st *store.Store, id string) string {
	if r, err := st.ResolveTicket(id); err == nil {
		return r
	}
	return id
}

// mergedInto is whether ticket's work has merged, for the container into when
// the ticket is a shared task that goes into it.
func mergedInto(d Deps, ticket, into string) (ready.Merge, error) {
	if into != "" {
		rec, err := d.Store.ReadTicket(ticket)
		if err != nil {
			return ready.Merge{}, err
		}
		cs, err := d.Store.ContainersOf(ticket, rec)
		if err != nil {
			return ready.Merge{}, err
		}
		if len(cs) > 1 {
			for _, c := range cs {
				if c == into {
					return d.Merges.OfFor(ticket, into)
				}
			}
		}
	}
	return d.Merges.Of(ticket)
}

// waitsOn is the Before of ticket id when its work has not merged, none when it
// has or when its closure releases whoever waits for it.
func waitsOn(d Deps, id, into string) ([]Before, error) {
	rec, err := d.Store.ReadTicket(id)
	if err != nil {
		return []Before{{Ticket: id, Why: "it is not a ticket of the store", Into: into}}, nil
	}
	if rec.Closed != nil {
		if store.Releases(rec.Closed.Reason) {
			return nil, nil
		}
		return []Before{{Ticket: id, Why: ready.NotPlanned, Into: into}}, nil
	}
	m, err := mergedInto(d, id, into)
	if err != nil {
		return nil, err
	}
	if m.Met() {
		return nil, nil
	}
	return []Before{{Ticket: id, Why: m.Why(), Into: into}}, nil
}

// blockersOfKind are the tickets rec names as blockers whose kind is stacked
// (stacked true) or not.
func blockersOfKind(st *store.Store, rec store.Ticket, stacked bool) []string {
	var out []string
	for _, b := range rec.BlockedBy {
		if (b.Kind == stack.KindStacked) == stacked {
			out = append(out, resolve(st, b.Ticket))
		}
	}
	return out
}

func mergedDependencies(s Scene) ([]Before, error) {
	return waitsOnAll(s, blockersOfKind(s.Store, s.Rec, false), s.Into)
}

func stackedParents(s Scene) ([]Before, error) {
	return waitsOnAll(s, blockersOfKind(s.Store, s.Rec, true), s.Into)
}

// waitsOnAll is the Before of each of ids, read for the container into: a
// dependency lands into the ticket's own container, and a container's child
// lands into the container itself (the ticket), as Complete reads it.
func waitsOnAll(s Scene, ids []string, into string) ([]Before, error) {
	var out []Before
	for _, id := range ids {
		b, err := waitsOn(s.Deps, id, into)
		if err != nil {
			return nil, err
		}
		out = append(out, b...)
	}
	return out, nil
}

func tasksOfMultiTask(s Scene) ([]Before, error) {
	if s.Rec.KindOrDefault() != store.KindMultiTask {
		return nil, nil
	}
	return waitsOnAll(s, Children(s.Store, s.Ticket, s.Rec), s.Ticket)
}

func ticketsOfIssue(s Scene) ([]Before, error) {
	if s.Rec.KindOrDefault() != store.KindIssue {
		return nil, nil
	}
	return waitsOnAll(s, Children(s.Store, s.Ticket, s.Rec), s.Ticket)
}

// Children are the tickets a container lands: every task under a multi-task,
// and for an issue every top-level ticket of its chart, that is every entry not
// under a multi-task. Any other ticket has none.
func Children(st *store.Store, container string, rec store.Ticket) []string {
	switch rec.KindOrDefault() {
	case store.KindMultiTask:
		all, err := st.Tickets()
		if err != nil {
			return nil
		}
		var out []string
		for _, id := range all {
			if r, err := st.ReadTicket(id); err == nil && r.Parent == container {
				out = append(out, id)
			}
		}
		return out
	case store.KindIssue:
		chart, err := st.ChartOfIssue(container)
		if err != nil || chart == "" {
			return nil
		}
		entries, err := st.ReadChart(chart)
		if err != nil {
			return nil
		}
		var out []string
		for _, e := range entries {
			if e.ID == "" {
				continue
			}
			if r, err := st.ReadTicket(e.ID); err == nil && r.Parent == "" {
				out = append(out, e.ID)
			}
		}
		return out
	}
	return nil
}

// Complete says the container is landed: its children have landed in its
// branch and its own pull request has. waiting are the children that have not.
func Complete(d Deps, container string) (done bool, waiting []string, err error) {
	rec, err := d.Store.ReadTicket(container)
	if err != nil {
		return false, nil, err
	}
	for _, id := range Children(d.Store, container, rec) {
		b, err := waitsOn(d, id, container)
		if err != nil {
			return false, nil, err
		}
		for _, w := range b {
			waiting = append(waiting, w.Ticket)
		}
	}
	if len(waiting) > 0 {
		return false, waiting, nil
	}
	own, err := Landed(d.Store, container, "")
	if err != nil {
		return false, nil, err
	}
	m, err := d.Merges.Of(container)
	if err != nil {
		return false, nil, err
	}
	return len(own) > 0 && len(own) >= len(m.Repos), nil, nil
}
