package tui

import (
	"strconv"
	"strings"

	"github.com/develdeco/jig/internal/events"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/phase"
)

// stepRow is one step of a ticket as its screen shows it: whether it is done,
// the state it is in, a few words on where it stands now, and for a done step
// how long it took.
type stepRow struct {
	Step  int
	Kind  string // the row kind that opens the step's screen
	State string
	Text  string
	Took  string
	Done  bool
}

// steps are the ticket's four steps, in order, as internal/phase derives them
// (standing.go): a step is done only when every step before it is, so a review
// that found fixes takes the build back, and the build is not done again until
// they are green.
func (m *Model) steps(t *ticket) []stepRow {
	return m.stepRows(t, m.phaseOf(t))
}

// stepRows are the four rows of a ticket that stands as d says. The phase gives
// each row whether it is done and what it says; the screens add the state the
// row is drawn in, and how long a done step took.
func (m *Model) stepRows(t *ticket, d phase.Ticket) []stepRow {
	return []stepRow{m.briefRow(t, d), m.buildRow(t, d), m.reviewRow(t, d), m.publishRow(t, d)}
}

// rowOf is the row the phase p makes: its text, and whether it is done.
func rowOf(step int, kind string, p phase.Phase) stepRow {
	return stepRow{Step: step, Kind: kind, Text: p.Text, Done: p.State == phase.StateDone}
}

// currentStep is the first step not done, the last when all are.
func currentStep(steps []stepRow) int {
	for i, s := range steps {
		if !s.Done {
			return i
		}
	}
	return len(steps) - 1
}

// briefRow is the brief: drafted, waiting for its approval, approved, or
// changed after it. A ticket whose work started with no brief on record (an
// older ticket, an adopted branch) is past this step, and says so.
func (m *Model) briefRow(t *ticket, d phase.Ticket) stepRow {
	p := d.Of(phase.Brief)
	s := rowOf(stepBrief, rowBrief, p)
	changed := t.status != nil && t.status.Brief != nil && t.status.Brief.Approval == phase.ApprovalChanged
	switch {
	case s.Done && hasSub(p, phase.SubNeedsYou):
		// The work started without an approval.
		s.State = StateFindings
	case s.Done:
		s.State = StateOK
	case changed:
		s.State = StateFindings
	case t.status == nil:
		s.State, s.Text = m.unreadBrief(t, s.Text)
	default:
		s.State = StateBuilding
	}
	return s
}

// unreadBrief is the brief of a ticket whose status has not been read and
// whose journal shows no approval or work: all the screens know of it is what
// `jig next` listed it as.
func (m *Model) unreadBrief(t *ticket, text string) (state, said string) {
	switch t.rosterState {
	case "new":
		return StateBuilding, text
	case "ready", "blocked":
		// It has slices that nobody has started, and the journal holds no
		// approval (or has not been delivered yet, which the next line
		// mends).
		return StateBuilding, "drafted · waiting for your approval"
	case "invalid":
		return StateStalled, "its slices.yaml cannot be read"
	}
	// Started or done (its lines are on their way), or not listed yet.
	return StateQueued, "reading"
}

// hasSub is whether any sub-step of p is in state.
func hasSub(p phase.Phase, state string) bool {
	for _, sub := range p.Subs {
		if sub.State == state {
			return true
		}
	}
	return false
}

// buildRow is the build: how many slices are green and what the rest are
// doing.
func (m *Model) buildRow(t *ticket, d phase.Ticket) stepRow {
	p := d.Of(phase.Build)
	s := rowOf(stepBuild, rowBuild, p)
	slices := t.slices(m.zone)
	counts := map[string]int{}
	for _, sl := range slices {
		counts[sl.State]++
	}
	n := len(slices)
	switch {
	case s.Done:
		s.State = StateOK
		if n > 0 {
			s.Took = m.stepTook(t, t.stepLines(stepBuild), journal.EventVerified)
		}
	case n == 0:
		s.State = StateQueued
	case counts[StateGreen] == n:
		// Every slice is green, but an earlier step is not done.
		s.State = StateOK
	case len(t.stepLines(stepBuild)) == 0:
		s.State = StateReady
		if p.State == phase.StatePending {
			s.State = StateQueued
		}
	default:
		s.State = StateBuilding
		switch {
		case counts[StateStalled] > 0:
			s.State = StateStalled
		case counts[StateEnvBlocked] > 0:
			s.State = StateEnvBlocked
		case counts[StateNeedsInput] > 0:
			s.State = StateNeedsInput
		case counts[StateVerifying] > 0:
			s.State = StateVerifying
		}
	}
	return s
}

// roundReviewer is the session that took round n to review it: the latest
// gate-reviewer line since the round last opened, or "". A round reopened at
// a moved head starts with no reviewer, and one reopened at the same head
// names its reviewer again.
func (t *ticket) roundReviewer(n int) string {
	for i := len(t.lines) - 1; i >= 0; i-- {
		switch l := t.lines[i]; {
		case l.Event == journal.EventGateOpen:
			return ""
		case l.Event == journal.EventGateReviewer && l.Attempt == n:
			return l.Session
		}
	}
	return ""
}

// reviewRow is the review: no round yet, a round open, findings to decide,
// or a clean round after the last of the build.
func (m *Model) reviewRow(t *ticket, d phase.Ticket) stepRow {
	p := d.Of(phase.Review)
	s := rowOf(stepReview, rowReview, p)
	rounds := t.rounds(m.zone)
	pending := p.State == phase.StatePending
	switch {
	case s.Done:
		s.State = StateOK
		s.Took = m.stepTook(t, t.stepLines(stepReview), journal.EventGateClean)
	case len(rounds) == 0:
		s.State = StateReady
		if pending {
			s.State = StateQueued
		}
	default:
		switch last := rounds[len(rounds)-1]; {
		case last.State == StateReviewing:
			s.State = StateReviewing
		case last.State == StateReady && pending:
			// Clean, but a step before it is not done any more.
			s.State = StateQueued
			s.Text = "round " + strconv.Itoa(last.N) + " was clean, but an earlier step changed since"
		case last.State == StateReady:
			// Clean, but the build went on after it: the head is not reviewed.
			s.State = StateReady
		default:
			s.State = StateFindings
		}
	}
	return s
}

// publishRow is the publish: the body, the person's approval, the pull
// request.
func (m *Model) publishRow(t *ticket, d phase.Ticket) stepRow {
	p := d.Of(phase.Publish)
	s := rowOf(stepPublish, rowPublish, p)
	switch {
	case s.Done:
		s.State = StatePublished
		s.Took = m.stepTook(t, t.stepLines(stepPublish), journal.EventPr)
	case p.State == phase.StatePending:
		s.State = StateQueued
	case hasSub(p, phase.SubDone):
		s.State = StatePublishing
	default:
		s.State = StateReady
	}
	return s
}

// stepTook is how long a done step took: from its first line to the last
// line of event.
func (m *Model) stepTook(t *ticket, lines []events.Envelope, event string) string {
	if len(lines) == 0 {
		return ""
	}
	end := -1
	for i := len(lines) - 1; i >= 0; i-- {
		if lines[i].Event == event {
			end = i
			break
		}
	}
	if end < 0 {
		return ""
	}
	return between(lines[0], lines[end])
}

// stepTitle is a step as its panel titles it: "2 Build".
func stepTitle(step int) string {
	return strconv.Itoa(step+1) + " " + stepNames[step]
}

// stepLabel is where a ticket stands, for a header: the state of its current
// step and the step's name with a word for that state.
func (m *Model) stepLabel(t *ticket) (state, label string) {
	steps := m.steps(t)
	cur := steps[currentStep(steps)]
	if cur.Done {
		return cur.State, "published"
	}
	word := map[string]string{
		StateQueued: "waiting", StateReady: "ready", StateBuilding: "in progress", StateVerifying: "verifying",
		StateNeedsInput: "needs you", StateStalled: "stalled", StateEnvBlocked: "env blocked",
		StateReviewing: "round open", StateFindings: "findings", StatePublishing: "publishing",
	}[cur.State]
	if cur.Step == stepBrief {
		// The brief's states are its own: drafted, or changed after it was
		// approved.
		if w := map[string]string{StateBuilding: "being drafted", StateFindings: "needs approving"}[cur.State]; w != "" {
			word = w
		}
	}
	return cur.State, strings.ToLower(stepNames[cur.Step]) + " " + firstOf(word, cur.State)
}
