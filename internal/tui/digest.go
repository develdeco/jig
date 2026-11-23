package tui

import (
	"strconv"
	"strings"

	"github.com/develdeco/jig/internal/phase"
)

// digestInfo is where `jig next --json` says one ticket stands: its kind, the
// five phases with their words (Land included), the pull requests it recorded,
// and its slices counted. The screens take a ticket's phase words from it and
// guess none: it is the same reading `jig next <ticket>` gives.
type digestInfo struct {
	Kind, Parent, Current string
	Ready                 bool
	// Behind says the ticket published and owes a merge before its publish
	// again.
	Behind bool
	Phases []digestPhase
	// PhaseProblem is why the phases could not be told; State is an issue's or
	// multi-task's own pull request (waiting, ready, published or landed).
	PhaseProblem, State string
	Closed              string
	PRs                 []digestPR
	Slices              *digestSlices
}

// digestPhase is one phase's state (done, current or pending) and its words.
type digestPhase struct{ Name, State, Text string }

// digestPR is a pull request a ticket recorded, and where it goes.
type digestPR struct {
	Repo   string
	Number int
	URL    string
	State  string
	Target string
}

// digestSlices counts a ticket's slices by state.
type digestSlices struct {
	Total, Green, Building, Queued, NeedsInput, Failed, Stopped int
}

// kindWords is how a ticket's kind is said beside it: the kind of an issue or a
// multi-task, "task of <parent>" for a task under one, and nothing for a task
// of none.
func (d *digestInfo) kindWords() string {
	switch {
	case d == nil:
		return ""
	case d.Kind == "task" && d.Parent != "":
		return "task of " + d.Parent
	case d.Kind == "task":
		return ""
	}
	return d.Kind
}

// waitsForAnswer says a slice of the ticket is parked on a question, as the
// roster counts them, before the ticket's status says which.
func (d *digestInfo) waitsForAnswer() bool {
	return d != nil && d.Slices != nil && d.Slices.NeedsInput > 0
}

// phase is the digest's row for the phase named name.
func (d *digestInfo) phase(name phase.Name) (digestPhase, bool) {
	for _, p := range d.Phases {
		if p.Name == string(name) {
			return p, true
		}
	}
	return digestPhase{}, false
}

// readingText is what a step says while the roster has not told where its
// ticket stands.
const readingText = "reading…"

// readingPhases are the phases of a ticket nothing has been read of: all
// pending, saying so.
func readingPhases() phase.Ticket {
	var d phase.Ticket
	for i, n := range []phase.Name{phase.Brief, phase.Build, phase.Review, phase.Publish, phase.Land} {
		d.Phases[i] = phase.Phase{Name: n, State: phase.StatePending, Text: readingText}
	}
	d.Current = phase.Brief
	return d
}

// lay puts the digest's words on d: each phase's state and text, the phase the
// ticket stands at and whether it can start. What d holds beside them (the
// sub-steps, from the status and the journal) stays. A digest that told no
// phases (a ticket that is not a task, or whose phase could not be derived)
// puts what it does say in the place of the words.
func (dg *digestInfo) lay(d *phase.Ticket) {
	if len(dg.Phases) == 0 {
		said := dg.PhaseProblem
		if said == "" {
			said = strings.TrimSpace(dg.Kind + " · " + dg.State)
		}
		for i := range d.Phases {
			d.Phases[i].Text = said
		}
		return
	}
	for i := range d.Phases {
		if p, ok := dg.phase(d.Phases[i].Name); ok {
			d.Phases[i].State, d.Phases[i].Text = p.State, p.Text
		}
	}
	d.Current, d.Ready = phase.Name(dg.Current), dg.Ready
}

// landLine is the words of the Land phase under a ticket's steps, with the pull
// requests it waits on by number and target; "" until the roster has told them.
func (m *Model) landLine(t *ticket) string {
	dg := t.digest
	if dg == nil {
		return ""
	}
	p, ok := dg.phase(phase.Land)
	if !ok || p.Text == "" || p.State == phase.StatePending {
		// Until the publish there is nothing of the landing to say.
		return ""
	}
	var prs []string
	for _, pr := range dg.PRs {
		switch {
		case pr.Number > 0 && pr.Target != "":
			prs = append(prs, "PR #"+strconv.Itoa(pr.Number)+" into "+pr.Target)
		case pr.Number > 0:
			prs = append(prs, "PR #"+strconv.Itoa(pr.Number))
		default:
			prs = append(prs, pr.URL)
		}
	}
	text := p.Text
	if len(prs) > 0 {
		text += " · " + strings.Join(prs, ", ")
	}
	state := StateQueued
	switch {
	case p.State == phase.StateDone:
		state = StateOK
	case p.State == phase.StateCurrent && strings.Contains(p.Text, "not recorded"):
		state = StateFindings
	case p.State == phase.StateCurrent:
		state = StatePublishing
	}
	return m.st.dim("land  ") + m.st.state(state, text)
}
