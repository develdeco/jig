package tui

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/develdeco/jig/internal/events"
	"github.com/develdeco/jig/internal/journal"
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

// steps are the ticket's four steps, in order. A step is done only when every
// step before it is: a review that found fixes takes the build back, and the
// build is not done again until they are green.
func (m *Model) steps(t *ticket) []stepRow {
	brief := m.briefStep(t)
	build := m.buildStep(t, brief.Done)
	review := m.reviewStep(t, build.Done)
	publish := m.publishStep(t, review.Done)
	return []stepRow{brief, build, review, publish}
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

// briefStep is the brief: drafted, waiting for its approval, approved, or
// changed after it. A ticket whose work started with no brief on record (an
// older ticket, an adopted branch) is past this step, and says so.
func (m *Model) briefStep(t *ticket) stepRow {
	s := stepRow{Step: stepBrief, Kind: rowBrief}
	approval := t.latest("", journal.EventApproval)
	started := false
	for _, l := range t.lines {
		if st := stepOfLine(l); st > stepBrief {
			started = true
			break
		}
	}
	var b *statusBrief
	if t.status != nil {
		b = t.status.Brief
	}
	nSlices := 0
	if t.status != nil {
		nSlices = len(t.status.Slices)
	}
	counts := ""
	if b != nil {
		counts = fmt.Sprintf(" · %d %s, %d %s", len(b.Sections), plural(len(b.Sections), "section", "sections"), nSlices, plural(nSlices, "slice", "slices"))
	}
	switch {
	case b != nil && b.Approval == "current":
		s.Done, s.State = true, StateOK
		s.Text = "approved by " + firstOf(b.ApprovedBy, "someone") + counts
	case b != nil && b.Approval == "changed":
		s.State = StateFindings
		s.Text = "changed after " + firstOf(b.ApprovedBy, "someone") + " approved it: it needs approving again"
	case b != nil && started:
		s.Done, s.State = true, StateFindings
		s.Text = "never approved: the work started without it" + counts
	case b != nil && nSlices == 0:
		s.State = StateBuilding
		s.Text = "being drafted" + counts
	case b != nil:
		s.State = StateBuilding
		s.Text = "drafted" + counts + " · waiting for your approval"
	case t.status != nil && t.status.Branch != "":
		s.Done, s.State = true, StateOK
		s.Text = "none: the ticket took over branch " + t.status.Branch
	case approval >= 0:
		s.Done, s.State = true, StateOK
		s.Text = "approved by " + firstOf(personName(t.lines[approval].Summary), "someone")
	case started:
		s.Done, s.State = true, StateOK
		s.Text = "no brief on record"
	case t.status == nil:
		s.State, s.Text = StateQueued, "not read yet"
	default:
		s.State, s.Text = StateBuilding, "being drafted"
	}
	return s
}

// buildStep is the build: how many slices are green and what the rest are
// doing.
func (m *Model) buildStep(t *ticket, ready bool) stepRow {
	s := stepRow{Step: stepBuild, Kind: rowBuild}
	slices := t.slices(m.zone)
	lines := t.stepLines(stepBuild)
	if len(slices) == 0 {
		switch {
		case t.status != nil && t.status.Branch != "" && ready:
			// An adopted branch is built: it has no slices until a review
			// sends fixes back.
			s.Done, s.State = true, StateOK
			s.Text = "built on branch " + t.status.Branch + ", adopted"
		case t.status == nil && ready && len(t.stepLines(stepReview)) > 0:
			// The same, seen from the journal alone (the main screen reads
			// no status): reviewed with no slice ever built.
			s.Done, s.State = true, StateOK
			s.Text = "built on an adopted branch"
		case !ready:
			s.State, s.Text = StateQueued, "after the brief"
		default:
			s.State, s.Text = StateQueued, "no slices yet"
		}
		return s
	}
	counts := map[string]int{}
	for _, sl := range slices {
		counts[sl.State]++
	}
	n := len(slices)
	if counts[StateGreen] == n && len(lines) > 0 {
		s.Done, s.State = ready, StateOK
		s.Text = fmt.Sprintf("%d %s green, each verified by jig", n, plural(n, "slice", "slices"))
		s.Took = m.stepTook(t, lines, journal.EventVerified)
		return s
	}
	if len(lines) == 0 {
		if !ready {
			s.State, s.Text = StateQueued, "after the brief"
			return s
		}
		s.State = StateReady
		s.Text = fmt.Sprintf("%d %s to build, starting with %s", n, plural(n, "slice", "slices"), strings.Join(readySlices(slices), " and "))
		return s
	}

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
	s.Text = fmt.Sprintf("%d of %d green", counts[StateGreen], n)
	if focus := m.buildFocus(t, slices); focus != "" {
		s.Text += " · " + focus
	}
	return s
}

// readySlices are the slices nothing blocks, by id.
func readySlices(slices []sliceRow) []string {
	var out []string
	for _, s := range slices {
		if s.BlockedBy == "" && s.State != StateGreen {
			out = append(out, "slice "+s.ID)
		}
	}
	if len(out) == 0 {
		out = []string{"slice " + slices[0].ID}
	}
	return out
}

// buildFocus is what matters most in a build under way: a question for the
// person, a stall, a verification running, the slice a builder holds.
func (m *Model) buildFocus(t *ticket, slices []sliceRow) string {
	for _, want := range []string{StateNeedsInput, StateStalled, StateEnvBlocked, StateVerifying, StateBuilding} {
		for _, sl := range slices {
			if sl.State != want {
				continue
			}
			switch want {
			case StateNeedsInput:
				return "slice " + sl.ID + " needs your answer"
			case StateStalled:
				return "slice " + sl.ID + " stalled"
			case StateEnvBlocked:
				return "slice " + sl.ID + " waits for its environment"
			case StateVerifying:
				p := t.latest(sl.ID, journal.EventVerifyPending)
				if p >= 0 {
					return "verifying slice " + sl.ID + " at " + commitWords(t, t.lines[p].Commit)
				}
				return "verifying slice " + sl.ID
			case StateBuilding:
				if holder, ok := claimHolder(t, sl.ID); ok && holder != "" {
					return m.who(t, holder) + " is on slice " + sl.ID
				}
				return "slice " + sl.ID + " is being built"
			}
		}
	}
	return ""
}

// reviewStep is the review: no round yet, a round open, findings to decide,
// or a clean round after the last of the build.
func (m *Model) reviewStep(t *ticket, ready bool) stepRow {
	s := stepRow{Step: stepReview, Kind: rowReview}
	rounds := t.rounds(m.zone)
	if len(rounds) == 0 {
		if !ready {
			s.State, s.Text = StateQueued, "after the build"
			return s
		}
		s.State, s.Text = StateReady, "ready for a fresh session to review the change (g opens a round)"
		return s
	}
	r := rounds[len(rounds)-1]
	last := t.latest("", journal.EventGateOpen, journal.EventGateRound, journal.EventGateClean)
	builtSince := t.latest("", buildEventNames...) > last
	switch r.State {
	case StateReviewing:
		s.State = StateReviewing
		s.Text = fmt.Sprintf("round %d open · ", r.N)
		if open := t.latest("", journal.EventGateOpen); open >= 0 && t.lines[open].Session != "" {
			s.Text += m.who(t, t.lines[open].Session) + " is reading the change"
		} else {
			s.Text += "waiting for a reviewer"
		}
	case StateReady:
		if builtSince || !ready {
			s.State, s.Text = StateQueued, fmt.Sprintf("round %d was clean, but an earlier step changed since", r.N)
			return s
		}
		s.Done, s.State = true, StateOK
		s.Text = fmt.Sprintf("round %d clean", r.N)
		if last >= 0 && t.lines[last].Session != "" {
			s.Text += " · by " + m.whoWith(t, t.lines[last].Session)
		}
		s.Took = m.stepTook(t, t.stepLines(stepReview), journal.EventGateClean)
	default:
		s.State = StateFindings
		asks := t.asks()
		undecided := 0
		for _, a := range asks {
			if a.Decision == "" {
				undecided++
			}
		}
		switch {
		case undecided > 0:
			s.Text = fmt.Sprintf("round %d found %d to decide · %d %s your decision", r.N, len(asks), undecided, plural(undecided, "waits for", "wait for"))
		case builtSince && !ready:
			s.Text = fmt.Sprintf("round %d sent fixes back · the build is on them", r.N)
		case builtSince:
			s.Text = fmt.Sprintf("round %d's fixes are green · g runs round %d", r.N, r.N+1)
		case len(asks) > 0:
			s.Text = fmt.Sprintf("round %d's asks are decided · g runs round %d", r.N, r.N+1)
		default:
			s.Text = fmt.Sprintf("round %d found things to fix", r.N)
		}
	}
	return s
}

// publishStep is the publish: the body, the person's approval, the pull
// request.
func (m *Model) publishStep(t *ticket, ready bool) stepRow {
	s := stepRow{Step: stepPublish, Kind: rowPublish}
	pub := t.publish()
	lines := t.stepLines(stepPublish)
	switch {
	case (pub.Done || pub.PR != "") && ready:
		s.Done, s.State = true, StatePublished
		s.Text = strings.TrimSpace("pull request " + firstOf(pub.PROutcome, "opened") + " · " + pub.PR)
		s.Took = m.stepTook(t, lines, journal.EventPr)
	case pub.Done || pub.PR != "":
		s.State = StatePublished
		s.Text = strings.TrimSpace("published " + pub.PR + ", but an earlier step changed since")
	case !ready:
		s.State, s.Text = StateQueued, "after a clean review"
	case pub.ApprovedFor != "":
		s.State, s.Text = StatePublishing, "approved by "+firstOf(m.byWhom(t, pub.ApprovedBy, ""), "someone")+" · pushing and opening the pull request"
	case pub.BodyFor != "":
		s.State, s.Text = StatePublishing, "body written · waiting for your approval (p)"
	default:
		s.State, s.Text = StateReady, "ready: p approves it, pushes the branch and opens the pull request"
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
		word = map[string]string{StateBuilding: "being drafted", StateFindings: "needs approving"}[cur.State]
	}
	return cur.State, strings.ToLower(stepNames[cur.Step]) + " " + firstOf(word, cur.State)
}
