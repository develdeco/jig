package main

import (
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/mergefacts"
	"github.com/develdeco/jig/internal/phase"
	"github.com/develdeco/jig/internal/project"
	"github.com/develdeco/jig/internal/ready"
	"github.com/develdeco/jig/internal/repohost"
	"github.com/develdeco/jig/internal/stack"
	"github.com/develdeco/jig/internal/store"
)

// nextDigest is where one ticket stands, in the words every screen shows: the
// reading of a ticket that `jig next` gives for each row of the roster and that
// `jig next <ticket>` gives in its own fields (phase, kind, parent, closed,
// stack and shared), built by the same functions, so two readers of the same
// facts cannot differ. It is JSON only.
type nextDigest struct {
	// Kind is task, issue or multi-task; Parent is a task's multi-task or
	// issue, "" for none.
	Kind   string `json:"kind"`
	Parent string `json:"parent,omitempty"`
	// Behind says the ticket published and is owed a merge before the publish
	// that follows it (isBehind), the one way a stack plan undoes a step: its
	// publish is current again, and it is not done.
	Behind bool `json:"behind,omitempty"`
	// Current is the phase the ticket stands at and Phases its five phases,
	// Land included, as `jig next <ticket>` prints them. Neither is set for a
	// ticket that is not a task, nor for one whose phase cannot be derived
	// (PhaseProblem says why): nothing is guessed.
	Current string `json:"current,omitempty"`
	// Ready says Current can start now.
	Ready        bool           `json:"ready"`
	Phases       []nextPhaseRow `json:"phases,omitempty"`
	PhaseProblem string         `json:"phase_problem,omitempty"`
	// State is an issue's or multi-task's own pull request: waiting, ready,
	// published or landed.
	State  string `json:"state,omitempty"`
	Closed string `json:"closed,omitempty"`
	// PRs is one row for each recorded pull request.
	PRs       []nextDigestPR `json:"prs"`
	BlockedBy []nextBlocker  `json:"blocked_by,omitempty"`
	// Slices counts a task's slices; nil for an issue or a multi-task.
	Slices *nextSliceCounts `json:"slices,omitempty"`
	// Children are an issue's entries or a multi-task's tasks.
	Children []nextChild `json:"children,omitempty"`
}

// nextDigestPR is one pull request a ticket recorded. Number is 0 when the URL
// is not one of the shape GitHub gives. State is open, merged or closed as the
// merge facts say, and unknown when they say nothing.
type nextDigestPR struct {
	Repo   string `json:"repo,omitempty"`
	Number int    `json:"number"`
	URL    string `json:"url"`
	State  string `json:"state"`
	// Target is the branch the pull request goes into.
	Target string `json:"target,omitempty"`
}

// nextSliceCounts is how many of a ticket's slices are in each state. Failed
// and Stopped are slices the store holds building whose last covering run
// failed and judged the code (failed), or was ended before it could (stopped);
// Building counts the rest.
type nextSliceCounts struct {
	Total      int `json:"total"`
	Green      int `json:"green"`
	Building   int `json:"building"`
	Queued     int `json:"queued"`
	NeedsInput int `json:"needs_input"`
	Failed     int `json:"failed"`
	Stopped    int `json:"stopped"`
}

// parentOf is the ticket a ticket's pull request goes into: its multi-task, or
// the issue whose chart lists it. A shared task, which two issues list, and a
// ticket no model chart lists have none: the shared rows name the issues.
func parentOf(st *store.Store, id string, rec store.Ticket) string {
	containers, err := st.ContainersOf(id, rec)
	if err != nil || len(containers) != 1 {
		return ""
	}
	return containers[0]
}

// phaseRows are a derived ticket's phases as `jig next <ticket>` prints them.
func phaseRows(t phase.Ticket) []nextPhaseRow {
	rows := make([]nextPhaseRow, 0, len(t.Phases))
	for _, ph := range t.Phases {
		rows = append(rows, nextPhaseRow{Name: string(ph.Name), State: ph.State, Text: ph.Text})
	}
	return rows
}

// taskPlan is the stack plan of a task and the rows of a shared one, which say
// what its pull requests go into. Only a ticket stacked on another, or shared by
// two issues, has a plan worth the git it costs: any other can be owed no merge,
// and its pull request goes into its container's branch (digestPRs).
func taskPlan(st *store.Store, cfg project.Config, mp project.MachineProject, merges mergeSource, id string, rec store.Ticket) (stack.Plan, []nextIssueRow, error) {
	containers, _ := st.ContainersOf(id, rec)
	if !stack.StackedOnAny(rec) && len(containers) < 2 {
		return stack.Plan{Ticket: id}, nil, nil
	}
	plan, err := stack.Read(stackInput(st, cfg, mp, merges), id)
	if err != nil {
		return stack.Plan{}, nil, err
	}
	shared, _, err := sharedRows(st, stackInput(st, cfg, mp, merges), merges, id)
	return plan, shared, err
}

// linePRs are the pull requests a journal records: the latest, and the latest
// in each repo that names one.
func linePRs(lines []journal.Line) (latest string, byRepo map[string]string) {
	for _, l := range lines {
		if l.Event != journal.EventPr || l.URL == "" {
			continue
		}
		latest = l.URL
		if l.Repo != "" {
			if byRepo == nil {
				byRepo = map[string]string{}
			}
			byRepo[l.Repo] = l.URL
		}
	}
	return latest, byRepo
}

// taskDigest is where a task stands: the phases derived with its stack plan
// laid on them (derived, nil with problem when they could not be) and the rest
// read beside them. blocked is what the ticket waits for, as the caller read it.
func taskDigest(st *store.Store, cfg project.Config, merges mergeSource, id string, data ticketData, state string, derived *phase.Ticket, problem string, plan stack.Plan, shared []nextIssueRow, lines []journal.Line, blocked []nextBlocker) nextDigest {
	d := nextDigest{Kind: store.KindTask, Parent: parentOf(st, id, data.rec), BlockedBy: blocked, PhaseProblem: problem}
	if data.rec.Closed != nil {
		d.Closed = data.rec.Closed.Phrase()
	}
	if derived != nil {
		d.Current, d.Ready, d.Phases = string(derived.Current), derived.Ready, phaseRows(*derived)
		d.Behind = isBehind(*derived, state)
	}
	d.PRs = digestPRs(st, cfg, merges, id, data.rec, plan, shared, lines)
	d.Slices = sliceCounts(data, lines)
	return d
}

// digestPRs are the pull requests a task recorded: a shared task's from its
// rows, one for each issue and repo, any other's from its journal and plan.
func digestPRs(st *store.Store, cfg project.Config, merges mergeSource, id string, rec store.Ticket, plan stack.Plan, shared []nextIssueRow, lines []journal.Line) []nextDigestPR {
	out := []nextDigestPR{}
	if len(shared) > 0 {
		for _, r := range shared {
			if r.PR == "" || r.PR == "-" {
				continue
			}
			out = append(out, nextDigestPR{Repo: r.Repo, Number: prNumber(r.PR), URL: r.PR, State: r.State, Target: r.Branch})
		}
		return out
	}
	latest, byRepo := linePRs(lines)
	if latest == "" {
		return out
	}
	var merge ready.Merge
	if merges != nil {
		merge, _ = merges.Of(id)
	}
	repos := plan.Repos
	if len(repos) == 0 {
		// A ticket stacked on nothing has no plan row: its pull request goes
		// into its container's branch, else the target of each repo.
		for _, r := range cfg.Repos {
			repos = append(repos, stack.Repo{Repo: r.Name(), PRTarget: containerTarget(st, id, rec, r.TargetBranch())})
		}
	}
	if len(repos) == 0 {
		repos = []stack.Repo{{PRTarget: containerTarget(st, id, rec, project.Repo{}.TargetBranch())}}
	}
	for _, r := range repos {
		url := byRepo[r.Repo]
		if url == "" && len(repos) == 1 {
			url = latest
		}
		if url == "" {
			continue
		}
		out = append(out, nextDigestPR{Repo: r.Repo, Number: prNumber(url), URL: url, State: prState(merge, r.Repo, len(repos) > 1), Target: r.PRTarget})
	}
	return out
}

// containerTarget is the branch a ticket's pull request goes into when it is
// stacked on nothing: its container's branch (the issue's, or the multi-task's),
// else target.
func containerTarget(st *store.Store, id string, rec store.Ticket, target string) string {
	container, ok, err := st.ContainerOf(id, rec)
	if err != nil || !ok {
		return target
	}
	crec, err := st.ReadTicket(container)
	if err != nil {
		return target
	}
	if branch, err := st.ResolveTicketBranch(container, crec, target); err == nil {
		return branch
	}
	return target
}

// prNumber is the number of a pull request URL, 0 when it names none.
func prNumber(url string) int {
	ref, err := repohost.ParsePRURL(url)
	if err != nil {
		return 0
	}
	return ref.Number
}

// sliceCounts counts a task's slices (lines are its journal) by the state the store holds, and
// separates, among those it holds building, the ones whose last covering run
// failed or was stopped: the store sets a failed slice back to building, and
// the run says it failed.
func sliceCounts(data ticketData, lines []journal.Line) *nextSliceCounts {
	c := &nextSliceCounts{Total: len(data.in.Slices)}
	for _, s := range data.in.Slices {
		switch data.in.States[s.ID].State {
		case "green":
			c.Green++
		case "queued":
			c.Queued++
		case "needs-input":
			c.NeedsInput++
		case "building":
			switch lastRun(lines, s.ID) {
			case runFailed:
				c.Failed++
			case runStopped:
				c.Stopped++
			default:
				c.Building++
			}
		}
	}
	return c
}

type runEnd int

const (
	runNone runEnd = iota
	runFailed
	runStopped
)

// lastRun is how the latest covering run of a slice that nothing verified since
// ended: failed when it judged the code, stopped when a stop or a timeout ended
// it first.
func lastRun(lines []journal.Line, slice string) runEnd {
	end := runNone
	for _, l := range lines {
		if l.Slice != slice {
			continue
		}
		switch l.Event {
		case journal.EventVerified:
			end = runNone
		case journal.EventOracle:
			switch {
			case l.Uncovered != "":
			case l.Outcome != "fail":
				end = runNone
			case l.Cause == "":
				end = runFailed
			default:
				end = runStopped
			}
		}
	}
	return end
}

// containerDigest is where an issue or a multi-task stands: its own pull
// request's state (waiting, ready, published or landed) and its children.
func containerDigest(st *store.Store, cfg project.Config, mp project.MachineProject, merges mergeSource, data ticketData, kind string, blocked []nextBlocker) nextDigest {
	n := nextNotATask(st, data, kind)
	d := nextDigest{Kind: kind, Parent: parentOf(st, data.in.ID, data.rec), BlockedBy: blocked, Children: n.Children, PRs: []nextDigestPR{}}
	if data.rec.Closed != nil {
		d.Closed = data.rec.Closed.Phrase()
	}
	r, err := containerReadiness(st, cfg, mp, merges, data.in.ID)
	if err != nil {
		d.PhaseProblem = err.Error()
		return d
	}
	d.State = r.State
	return d
}

// digestMerges is the merge source a listing states each ticket's standing
// from: the reader's own cache, asking the host nothing, so the listing makes
// no host call for a ticket it did not already make to judge a blocker. A source
// that is no reader (a fixed one) is used as it is.
func digestMerges(m mergeSource) mergeSource {
	if c, ok := m.(interface{ Cached() *mergefacts.Reader }); ok {
		return c.Cached()
	}
	return m
}

// rosterDigest is the digest of one row of the listing. A ticket whose files
// could not be read says so and nothing else.
func rosterDigest(st *store.Store, cfg project.Config, mp project.MachineProject, merges mergeSource, data ticketData, state string, blocked []nextBlocker) nextDigest {
	kind := data.rec.KindOrDefault()
	if data.problem != "" {
		return nextDigest{Kind: kind, PhaseProblem: data.problem, PRs: []nextDigestPR{}, BlockedBy: blocked}
	}
	if kind != store.KindTask {
		return containerDigest(st, cfg, mp, merges, data, kind, blocked)
	}
	id := data.in.ID
	plan, shared, err := taskPlan(st, cfg, mp, merges, id, data.rec)
	if err != nil {
		return taskDigest(st, cfg, merges, id, data, state, nil, err.Error(), stack.Plan{Ticket: id}, nil, nil, blocked)
	}
	t, lines, err := derivePhaseStackedLines(st, id, merges, plan)
	if err != nil {
		return taskDigest(st, cfg, merges, id, data, state, nil, err.Error(), plan, shared, nil, blocked)
	}
	return taskDigest(st, cfg, merges, id, data, state, &t, "", plan, shared, lines, blocked)
}
