package land

import (
	"fmt"

	"github.com/develdeco/jig/internal/phase"
	"github.com/develdeco/jig/internal/project"
	"github.com/develdeco/jig/internal/ready"
	"github.com/develdeco/jig/internal/store"
)

// The states a multi-task or an issue is in, as `jig next` says them.
const (
	// StateWaiting: a child has not landed, or a precondition is unmet.
	StateWaiting = "waiting"
	// StateReady: every child has landed and the container has no pull request.
	StateReady = "ready"
	// StatePublished: the container has a pull request; the Land phase is next.
	StatePublished = "published"
	// StateLanded: the container's own pull request has landed too.
	StateLanded = "landed"
)

// Container is what a precondition is asked about.
type Container struct {
	ID       string   // the container's ticket id
	Kind     string   // store.KindMultiTask or store.KindIssue
	Branch   string   // its branch, one name in every repo
	Children []string // its children, as Children reads them
	Repos    []string // the repos it builds in
	// Deps is what ContainerOf read it with: the store and the clones a
	// precondition reads the container's journal and branch tips from.
	Deps Deps
}

// Unmet is one thing a container's publish still waits for.
type Unmet struct {
	Code   string // a stable code, such as LAND_SUITE_NOT_RUN
	Why    string // a sentence
	Prompt string // what to ask to meet it, "" when nothing a session can do
}

// A Precondition returns what is unmet, or none.
type Precondition func(c Container) ([]Unmet, error)

// Preconditions is the registry of what a container's publish waits for besides
// its children. Its one entry is an issue's suite (suitePrecondition); a
// multi-task has none. `jig next` and `jig publish` read this list and nothing
// else.
var Preconditions = []Precondition{suitePrecondition}

// Check runs every precondition in order and returns all that is unmet.
func Check(c Container) ([]Unmet, error) {
	var out []Unmet
	for _, p := range Preconditions {
		unmet, err := p(c)
		if err != nil {
			return nil, err
		}
		out = append(out, unmet...)
	}
	return out, nil
}

// Wait is a child of a container that has not landed, with where it stands:
// "published" (its pull request is open), "building", or why it is closed
// without landing.
type Wait struct {
	Ticket, Standing string
}

// Waiting are the children of container that have not landed, in the order
// Children names them, each with its standing. It reads the children only: the
// container's own pull request is no child's. A child counts as landed as the
// Land rules count it (waitsOn): a task by its merge into this container, a
// multi-task by its own pull request, a shared task by the pull request into
// this issue.
func Waiting(d Deps, container string) ([]Wait, error) {
	rec, err := d.Store.ReadTicket(container)
	if err != nil {
		return nil, err
	}
	var out []Wait
	for _, id := range Children(d.Store, container, rec) {
		b, err := waitsOn(d, id, container)
		if err != nil {
			return nil, err
		}
		if len(b) == 0 {
			continue
		}
		standing := b[0].Why
		if crec, err := d.Store.ReadTicket(id); err == nil && crec.Closed == nil {
			m, err := mergedInto(d, id, container)
			if err != nil {
				return nil, err
			}
			standing = "building"
			if m.Published {
				standing = "published"
			}
		}
		out = append(out, Wait{Ticket: id, Standing: standing})
	}
	more, err := notGraduated(d, container, rec)
	if err != nil {
		return nil, err
	}
	return append(out, more...), nil
}

// NotGraduated is the standing of a chart entry that has no ticket yet.
const NotGraduated = "not graduated"

// notGraduated are the chart entries of container's work that have no ticket
// yet, each named by its chart position and title: a top-level entry of an
// issue's chart, or an entry under the multi-task. Planned work no ticket holds
// is not landed, so it holds the container.
func notGraduated(d Deps, container string, rec store.Ticket) ([]Wait, error) {
	issue := container
	if rec.KindOrDefault() == store.KindMultiTask {
		issues, err := d.Store.IssuesOf(container)
		if err != nil || len(issues) == 0 {
			return nil, err
		}
		issue = issues[0]
	}
	chart, err := d.Store.ChartOfIssue(issue)
	if err != nil || chart == "" {
		return nil, err
	}
	entries, err := d.Store.ReadChart(chart)
	if err != nil {
		return nil, err
	}
	self := 0
	for i, e := range entries {
		if e.ID == container {
			self = i + 1
		}
	}
	var out []Wait
	for i, e := range entries {
		if e.ID != "" {
			continue
		}
		under := e.Under != "" && (e.Under == container || (self > 0 && e.Under == fmt.Sprintf("#%d", self)))
		if rec.KindOrDefault() == store.KindMultiTask && !under {
			continue
		}
		if rec.KindOrDefault() == store.KindIssue && e.Under != "" {
			continue
		}
		out = append(out, Wait{Ticket: fmt.Sprintf("#%d %s", i+1, e.Title), Standing: NotGraduated})
	}
	return out, nil
}

// ContainerOf is what a precondition is asked about container: its kind, its
// branch, its children, and the repos its tasks build in (every repo of the
// project when none is placed yet).
func ContainerOf(d Deps, container string) (Container, error) {
	rec, err := d.Store.ReadTicket(container)
	if err != nil {
		return Container{}, err
	}
	c := Container{ID: container, Kind: rec.KindOrDefault(), Children: Children(d.Store, container, rec), Deps: d}
	targets := projectTargets(d.Cfg)
	if c.Branch, err = d.Store.ResolveTicketBranch(container, rec, targets[0]); err != nil {
		return Container{}, err
	}
	repos := ready.ProjectRepos(d.Cfg, d.Clones)
	tasks, err := d.Store.TasksUnder(container, rec)
	if err != nil {
		return Container{}, err
	}
	used := map[string]bool{}
	for _, id := range tasks {
		slices, err := d.Store.ReadSlices(id)
		if err != nil {
			continue
		}
		for _, name := range ready.BuiltIn(repos, slices) {
			used[name] = true
		}
	}
	for _, r := range repos {
		if len(used) == 0 || used[r.Name] {
			c.Repos = append(c.Repos, r.Name)
		}
	}
	return c, nil
}

// projectTargets are the target branches of the project's repos, one each; the
// project's default when it names none.
func projectTargets(cfg project.Config) []string {
	var out []string
	seen := map[string]bool{}
	for _, r := range cfg.Repos {
		if t := r.TargetBranch(); !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
	}
	if len(out) == 0 {
		out = append(out, project.Repo{}.TargetBranch())
	}
	return out
}

// Readiness is where a multi-task or an issue stands before its own pull
// request: what it waits for, or that it is ready to publish.
type Readiness struct {
	State string
	// Waits are the children not landed, Unmet the preconditions not met.
	Waits []Wait
	Unmet []Unmet
	// Waived are the suite waivers standing at the issue's tips, in words.
	Waived []string
	// Prompt is what to ask next: Publish T when ready, Land T when published,
	// and "" when waiting.
	Prompt string
}

// ReadinessOf says where container stands. A container with no children (a
// chart not yet graduated) waits, with no row; every child landed and every
// precondition met is ready until the container has a pull request.
func ReadinessOf(d Deps, container string) (Readiness, error) {
	c, err := ContainerOf(d, container)
	if err != nil {
		return Readiness{}, err
	}
	own, err := Landed(d.Store, container, "")
	if err != nil {
		return Readiness{}, err
	}
	m, err := d.Merges.Of(container)
	if err != nil {
		return Readiness{}, err
	}
	if m.Published {
		if len(own) > 0 && len(own) >= len(m.Repos) {
			return Readiness{State: StateLanded}, nil
		}
		return Readiness{State: StatePublished, Prompt: phase.Prompt(phase.Land, container)}, nil
	}
	r := Readiness{State: StateWaiting}
	if r.Waits, err = Waiting(d, container); err != nil {
		return Readiness{}, err
	}
	if len(r.Waits) > 0 || len(c.Children) == 0 {
		return r, nil
	}
	if r.Unmet, err = Check(c); err != nil {
		return Readiness{}, err
	}
	if r.Waived, err = SuiteWaivers(d, c); err != nil {
		return Readiness{}, err
	}
	if len(r.Unmet) > 0 {
		for _, u := range r.Unmet {
			if u.Prompt != "" {
				r.Prompt = u.Prompt
				break
			}
		}
		return r, nil
	}
	r.State, r.Prompt = StateReady, phase.Prompt(phase.Publish, container)
	return r, nil
}
