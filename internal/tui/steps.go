package tui

import (
	"strconv"
	"strings"
	"time"

	"github.com/develdeco/jig/internal/phase"
)

// stepRow is one step of a ticket as its screen shows it: whether it is done,
// the state it is in, a few words on where it stands now, and how long it has
// taken so far.
type stepRow struct {
	Step  int
	Kind  string // the row kind that opens the step's screen
	State string
	Text  string
	Took  string
	Done  bool
	took  time.Duration
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
// row is drawn in, and how long the step has taken (stepTimes).
func (m *Model) stepRows(t *ticket, d phase.Ticket) []stepRow {
	rows := []stepRow{m.briefRow(t, d), m.buildRow(t, d), m.reviewRow(t, d), m.publishRow(t, d)}
	if t.digest == nil && t.status == nil {
		// Nothing was read of the ticket: its steps say so, with no state and
		// no time.
		for i := range rows {
			rows[i].State, rows[i].Text, rows[i].Done = StateQueued, readingText, false
		}
		return rows
	}
	for i, took := range m.stepTimes(t, d) {
		if took > 0 {
			rows[i].Took, rows[i].took = ago(took), took
		}
	}
	return rows
}

// totalTook is how long the steps have taken in all: their times added up, so
// the time between them (a person reading before the next prompt) is not in it.
func totalTook(steps []stepRow) time.Duration {
	var d time.Duration
	for _, s := range steps {
		d += s.took
	}
	return d
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
	case t.status == nil && t.digest != nil && t.digest.PhaseProblem != "" && len(t.digest.Phases) == 0:
		s.State = StateStalled
	default:
		s.State = StateBuilding
	}
	return s
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
	counts, n := t.sliceCounts(m.zone)
	switch {
	case s.Done:
		s.State = StateOK
	case n == 0:
		s.State = StateQueued
	case counts[StateGreen] == n:
		// Every slice is green, but an earlier step is not done.
		s.State = StateOK
	case len(t.stepLines(stepBuild)) == 0 && counts[StateQueued] == n:
		s.State = StateReady
		if p.State == phase.StatePending || cannotStart(d, p) {
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
		case counts[StateFail] > 0:
			s.State = StateFail
		}
	}
	return s
}

// reviewRow is the review: no round yet, a round open, findings to decide,
// or a clean round after the last of the build.
func (m *Model) reviewRow(t *ticket, d phase.Ticket) stepRow {
	p := d.Of(phase.Review)
	s := rowOf(stepReview, rowReview, p)
	rounds := t.rounds(m.zone)
	pending := p.State == phase.StatePending || cannotStart(d, p)
	switch {
	case s.Done:
		s.State = StateOK
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
			clean := "clean"
			if last.Carried > 0 {
				clean += " (carried over from round " + strconv.Itoa(last.Carried) + ")"
			}
			s.Text = "round " + strconv.Itoa(last.N) + " was " + clean + ", but an earlier step changed since"
		case last.State == StateReady:
			// Clean, but the build went on after it: the head is not reviewed.
			s.State = StateReady
		default:
			s.State = StateFindings
		}
	}
	return s
}

// publishRow is the publish: the body, the pull request (with the person's
// approval first when jig pushes), the changelog.
func (m *Model) publishRow(t *ticket, d phase.Ticket) stepRow {
	p := d.Of(phase.Publish)
	s := rowOf(stepPublish, rowPublish, p)
	switch {
	case s.Done:
		s.State = StatePublished
	case p.State == phase.StatePending:
		s.State = StateQueued
	case t.digest != nil && t.digest.Behind:
		// Published, and owed a merge before the publish that follows it.
		s.State = StateReady
	case hasSub(p, phase.SubDone):
		s.State = StatePublishing
	case cannotStart(d, p):
		s.State = StateQueued
	default:
		s.State = StateReady
	}
	return s
}

// cannotStart says p is the phase the ticket stands at and the digest says it
// cannot start now: it waits for a blocker, not for the person.
func cannotStart(d phase.Ticket, p phase.Phase) bool {
	return p.Name == d.Current && p.State == phase.StateCurrent && !d.Ready
}

// stepTimes are how long each step has taken: the gaps between consecutive
// lines of the log that both belong to the step (placed), so a build that a
// review sent a fix back to counts the fix and not the review between, and the
// time from one step to the next (a person reading the outcome before prompting
// the next phase) counts for neither. A line of no step (a spend) neither
// starts nor ends a gap. The step under way also counts the time since its
// latest line, while that line is the latest of the log: the brief waiting on
// the person's answer, a slice being built.
func (m *Model) stepTimes(t *ticket, d phase.Ticket) [stepCount]time.Duration {
	var took [stepCount]time.Duration
	var prev time.Time
	prevStep := -1
	for _, p := range m.placed(t, d) {
		at, ok := p.l.At()
		if p.step < 0 || !ok {
			continue
		}
		if p.step == prevStep && at.After(prev) {
			took[p.step] += at.Sub(prev)
		}
		prev, prevStep = at, p.step
	}
	if cur := currentOf(d); prevStep == cur && d.Phases[cur].State != phase.StateDone && m.now().After(prev) {
		took[cur] += m.now().Sub(prev)
	}
	return took
}

// stepLabel is where a ticket stands, for a header: the state of its current
// step and the words that say it.
func (m *Model) stepLabel(t *ticket) (state, label string) {
	steps := m.steps(t)
	cur := steps[currentStep(steps)]
	if cur.Done {
		return cur.State, "published"
	}
	return cur.State, stepWords(cur.Step, cur.State)
}

// stepState is a step in a state, the key of the header's words.
type stepState struct {
	step  int
	state string
}

// headerWords are what the header says of a step in a state, whole, since a
// step's name and its state's word do not always read as one phrase: the
// publish that is under way is "publishing", not "publish publishing". Every
// pair a step's row can be in is here; stepWords falls back to the step's name
// and the state for one that is not.
var headerWords = map[stepState]string{
	{stepBrief, StateQueued}:       "brief being read",
	{stepBrief, StateBuilding}:     "brief being drafted",
	{stepBrief, StateFindings}:     "brief needs approving",
	{stepBrief, StateStalled}:      "brief cannot be read",
	{stepBuild, StateQueued}:       "waiting to build",
	{stepBuild, StateReady}:        "ready to build",
	{stepBuild, StateBuilding}:     "build in progress",
	{stepBuild, StateNeedsInput}:   "build needs you",
	{stepBuild, StateStalled}:      "build stalled",
	{stepBuild, StateFail}:         "build failing",
	{stepBuild, StateEnvBlocked}:   "build blocked by its environment",
	{stepReview, StateQueued}:      "waiting to review",
	{stepReview, StateReady}:       "ready to review",
	{stepReview, StateReviewing}:   "review round open",
	{stepReview, StateFindings}:    "review findings",
	{stepPublish, StateQueued}:     "waiting to publish",
	{stepPublish, StateReady}:      "ready to publish",
	{stepPublish, StatePublishing}: "publishing",
	{stepPublish, StatePublished}:  "published",
}

// stepWords is what the header says of step in state.
func stepWords(step int, state string) string {
	if w, ok := headerWords[stepState{step, state}]; ok {
		return w
	}
	return strings.ToLower(stepNames[step]) + " " + state
}
