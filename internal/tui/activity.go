package tui

import (
	"strconv"
	"strings"
	"time"

	"github.com/develdeco/jig/internal/events"
	"github.com/develdeco/jig/internal/journal"
)

// A ticket goes through four steps, in order: its brief is agreed, its slices
// are built and verified, the change is reviewed, and it is published. Every
// journal line belongs to one of them, and a screen says a line in words: who
// did it by role, the slice by its goal, the commit by its subject. A line also
// advances a sub-step of its step (a slice, a round, the body of the pull
// request), and its activity line names it in the words of the sub-steps block.

// The steps, in the order a ticket goes through them.
const (
	stepBrief = iota
	stepBuild
	stepReview
	stepPublish
	stepCount
)

// stepNames are the steps as the screens title them.
var stepNames = [stepCount]string{"Brief", "Build", "Review", "Publish"}

// stepOfLine is the step a journal line belongs to, or -1 for a line about
// the ticket as a whole (what it has cost).
func stepOfLine(l events.Envelope) int {
	switch l.Event {
	case journal.EventApproval, journal.EventRevalidate:
		return stepBrief
	case journal.EventGateOpen, journal.EventGateReviewer, journal.EventGateRound, journal.EventGateClean, journal.EventTriage,
		journal.EventFixSlice, journal.EventAdopt, journal.EventRoute:
		return stepReview
	case journal.EventPrBody, journal.EventPublishApproved, journal.EventPr, journal.EventPublishDone,
		journal.EventSquash, journal.EventMemorize, journal.EventChangelog, journal.EventPublishPicks:
		return stepPublish
	case journal.EventSpend:
		return -1
	}
	return stepBuild
}

// stepLines are the ticket's lines of step, oldest first.
func (t *ticket) stepLines(step int) []events.Envelope {
	var out []events.Envelope
	for _, l := range t.lines {
		if stepOfLine(l) == step {
			out = append(out, l)
		}
	}
	return out
}

// subjects maps each commit the ticket verified to its subject, which jig
// verify writes on its verify-pending line.
func (t *ticket) subjects() map[string]string {
	out := map[string]string{}
	for _, l := range t.lines {
		if l.Event == journal.EventVerifyPending && l.Commit != "" && l.Summary != "" {
			out[l.Commit] = l.Summary
		}
	}
	return out
}

// goals maps each slice to its goal in one line (goalLine), once a status
// has been read.
func (t *ticket) goals() map[string]string {
	out := map[string]string{}
	if t.status != nil {
		for _, s := range t.status.Slices {
			out[s.ID] = goalLine(s.Goal)
		}
	}
	return out
}

// commitWords is a commit as a line says it: its short id and, when known,
// its subject in quotes.
func commitWords(t *ticket, sha string) string {
	if sha == "" {
		return ""
	}
	if subj := t.subjects()[sha]; subj != "" {
		return short(sha) + " “" + subj + "”"
	}
	return short(sha)
}

// subLabels are the labels of a ticket's sub-steps by key, as internal/phase
// words them for the sub-steps block. An activity line names the sub-step it
// advances with the same label.
type subLabels map[string]string

// subLabelsOf are the labels of the sub-steps the ticket has now.
func (m *Model) subLabelsOf(t *ticket) subLabels {
	out := subLabels{}
	d := m.phaseOf(t)
	for _, p := range d.Phases {
		for _, s := range p.Subs {
			out[s.Key] = s.Label
		}
	}
	return out
}

// subKey is the key of the sub-step a journal line advances (the keys of
// internal/phase's sub-steps), "" for a line that advances none.
func (t *ticket) subKey(l events.Envelope) string {
	switch l.Event {
	case journal.EventApproval:
		return "approved"
	case journal.EventRevalidate:
		return "slices"
	case journal.EventGateOpen, journal.EventGateReviewer, journal.EventGateRound, journal.EventGateClean:
		return "round:" + strconv.Itoa(t.roundOf(l))
	case journal.EventTriage:
		return "asks"
	case journal.EventFixSlice:
		return "fixes"
	case journal.EventPrBody, journal.EventPublishPicks:
		return "body"
	case journal.EventPublishApproved:
		return "approval"
	case journal.EventPr:
		// A publish of several repos has a pull request sub-step for each.
		if l.Repo != "" && len(t.publish().Repos) > 0 {
			return "pr:" + l.Repo
		}
		return "pr"
	case journal.EventSquash:
		return "pr"
	case journal.EventChangelog, journal.EventMemorize:
		return "changelog"
	case journal.EventPublishDone:
		return "done"
	}
	if l.Slice != "" && stepOfLine(l) == stepBuild {
		return "slice:" + l.Slice
	}
	return ""
}

// roundOf is the round a gate line is about: its attempt, else the round after
// the ones that had closed before it.
func (t *ticket) roundOf(l events.Envelope) int {
	if l.Attempt > 0 {
		return l.Attempt
	}
	closed := 0
	for _, x := range t.lines {
		if x.TS == l.TS && x.Event == l.Event && x.Session == l.Session {
			break
		}
		if x.Event == journal.EventGateRound || x.Event == journal.EventGateClean {
			if x.Attempt > 0 {
				closed = x.Attempt
			} else {
				closed++
			}
		}
	}
	return closed + 1
}

// fallbackLabels are the words of a sub-step the ticket does not list now (the
// asks of a round that closed clean, a slice the status does not name): the
// same words internal/phase gives it while it does.
var fallbackLabels = map[string]string{
	"approved": "brief approved", "slices": "slices written", "asks": "asks", "fixes": "fixes",
	"body": "body", "approval": "approval", "pr": "pull request", "changelog": "changelog", "done": "published",
}

// labelOf is the sub-step a line advances, in the words of the sub-steps
// block; a line that advances none names its step.
func (t *ticket) labelOf(ls subLabels, l events.Envelope) string {
	key := t.subKey(l)
	if key == "" {
		if step := stepOfLine(l); step >= 0 {
			return strings.ToLower(stepNames[step])
		}
		return "ticket"
	}
	if label, ok := ls[key]; ok {
		return label
	}
	if label, ok := fallbackLabels[key]; ok {
		return label
	}
	kind, id, _ := strings.Cut(key, ":")
	if kind == "pr" {
		return "pull request " + id
	}
	return kind + " " + id
}

// activityLine is one line of a step's activity: its time, the state it is
// drawn in, the sub-step it advances and what happened.
func (m *Model) activityLine(t *ticket, ls subLabels, l events.Envelope) string {
	return m.st.dim(clock(l, m.zone)) + " " + m.st.icon(eventState(l)) + " " + t.labelOf(ls, l) + m.st.dim(" · ") + m.say(t, l)
}

// timelineLine is activityLine without the sub-step, for a screen that is
// about one of them.
func (m *Model) timelineLine(t *ticket, l events.Envelope) string {
	return m.st.dim(clock(l, m.zone)) + " " + m.st.icon(eventState(l)) + " " + m.say(t, l)
}

// say is what a journal line records, in words that follow the sub-step it
// names: "builder took it", "verified aaaaaaa", "reviewer took it".
func (m *Model) say(t *ticket, l events.Envelope) string {
	who := m.whoAs(t, l.Session, roleOf(l.Event))
	if who == "" {
		who = "a session"
	}
	switch l.Event {
	case journal.EventClaim:
		return who + " took it" + goalTail(t, l.Slice, m)
	case journal.EventRelease:
		return who + " let go of it"
	case journal.EventDispatch:
		return "jig started a builder"
	case journal.EventVerifyPending:
		return "verifying " + commitWords(t, l.Commit)
	case journal.EventOracle:
		cmd := "its oracle"
		if l.Command != "" {
			cmd = "`" + l.Command + "`"
		}
		if l.Outcome == "pass" {
			return cmd + " passed" + took(l.Seconds)
		}
		return strings.TrimSpace(cmd + " failed" + took(l.Seconds) + ": " + firstOf(l.Summary, l.Signature))
	case journal.EventVerified:
		return "verified " + short(l.Commit)
	case journal.EventVerifyError:
		return strings.TrimSpace("could not be verified: " + l.Summary)
	case journal.EventQuestion:
		return strings.TrimSpace(who + " asks: " + firstLine(l.Summary))
	case journal.EventAnswer:
		return strings.TrimSpace(firstOf(m.byWhom(t, l.Provenance, l.Session), "someone") + " answered the question: " + firstLine(l.Summary))
	case journal.EventStall:
		return strings.TrimSpace("stalled: " + firstOf(l.Summary, l.Outcome))
	case journal.EventRequeue:
		return "back in the queue"
	case journal.EventResult:
		return strings.TrimSpace(who + "'s attempt ended " + firstOf(l.Outcome, "without an outcome") + attemptWords(l))
	case journal.EventRecorded:
		return "recorded " + strconv.Itoa(len(l.Recordings)) + " " + plural(len(l.Recordings), "file", "files") + " of the build's scenarios at " + short(l.Commit)
	case journal.EventEnvUp:
		return "its environment came up"
	case journal.EventEnvDown:
		return "its environment went down"
	case journal.EventEnvUnavailable:
		return strings.TrimSpace("its environment is unavailable: " + l.Summary)
	case journal.EventDriver:
		return strings.TrimSpace("jig's driver: " + firstOf(l.Summary, l.Outcome))
	case journal.EventReconcile:
		return strings.TrimSpace("reconciled with the store: " + firstOf(l.Summary, l.Outcome))
	case journal.EventLeaseRecovered:
		return "the lease was recovered"
	case journal.EventNote, journal.EventCorrection:
		return "note: " + l.Summary
	case journal.EventApproval:
		text := "by " + firstOf(personName(l.Summary), "someone")
		if l.Provenance == "agent" && l.Session != "" {
			text += ", recorded by " + who
		}
		return text
	case journal.EventRevalidate:
		return "the brief was checked again"
	case journal.EventAdopt:
		return strings.TrimSpace("the ticket took over " + firstOf(l.Branch, "a branch") + " " + short(l.Commit))
	case journal.EventGateOpen:
		return "opened"
	case journal.EventGateReviewer:
		return firstOf(m.whoWith(t, l.Session), "a reviewer") + " took it"
	case journal.EventGateRound:
		return firstOf(m.whoWith(t, l.Session), "the reviewer") + " finished with findings"
	case journal.EventGateClean:
		return firstOf(m.whoWith(t, l.Session), "the reviewer") + " finished: clean"
	case journal.EventRoute:
		return strings.TrimSpace("the round's findings were routed: " + firstOf(l.Summary, l.Outcome))
	case journal.EventTriage:
		verb := "kept"
		if l.Outcome == "dismiss" {
			verb = "dismissed"
		}
		text := firstOf(m.byWhom(t, l.Provenance, l.Session), "someone") + " " + verb + " " + askName(t, l.Ref)
		if l.Summary != "" {
			text += ": " + l.Summary
		}
		return text
	case journal.EventFixSlice:
		text := "sent back " + l.Slice
		if f := t.goals()[l.Slice]; f != "" {
			text += ": " + f
		}
		return text
	case journal.EventPrBody:
		return "written for " + repoWords(l) + commitWords(t, l.Commit)
	case journal.EventPublishPicks:
		return strings.TrimSpace("demo " + firstOf(l.Outcome, "picked") + ": " + firstOf(pickSummary(l), l.Summary))
	case journal.EventPublishApproved:
		return firstOf(m.byWhom(t, l.Provenance, l.Session), "someone") + " approved " + repoWords(l) + short(l.Commit)
	case journal.EventPr:
		return strings.TrimSpace(firstOf(l.Outcome, "recorded") + " " + l.URL)
	case journal.EventSquash:
		return strings.TrimSpace("the branch was squashed " + short(l.Commit))
	case journal.EventChangelog:
		return "written"
	case journal.EventMemorize:
		return strings.TrimSpace("what the ticket taught was memorized: " + firstOf(l.Summary, l.Outcome))
	case journal.EventPublishDone:
		return "finished"
	}
	return strings.TrimSpace(l.Event + " " + firstOf(l.Summary, l.Outcome))
}

// repoWords is the repo a publish line names with a space after it, none for a
// line that names no repo.
func repoWords(l events.Envelope) string {
	if l.Repo == "" {
		return ""
	}
	return l.Repo + " "
}

// attemptWords is " (attempt n)" for a line of a numbered attempt.
func attemptWords(l events.Envelope) string {
	if l.Attempt > 0 {
		return " (attempt " + strconv.Itoa(l.Attempt) + ")"
	}
	return ""
}

// pickSummary is a publish-picks line's summary of the demo it picked.
func pickSummary(l events.Envelope) string {
	if l.Pick == nil {
		return ""
	}
	return firstLine(l.Pick.Summary)
}

// goalLine is a goal in one line, as a row shows it. A fix slice's goal runs
// to several lines under a heading that ends in a colon ("Fix these gate
// findings:", "Gate finding r1-f1, kept by the human:"): its line is the
// first finding after the heading. Any other goal's is its first line.
func goalLine(goal string) string {
	first := ""
	for _, line := range strings.Split(goal, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		if first == "" {
			first = line
		}
		if !strings.HasSuffix(line, ":") {
			return line
		}
	}
	return first
}

// goalTail is " · <goal>" for a slice whose goal is known.
func goalTail(t *ticket, slice string, m *Model) string {
	if g := t.goals()[slice]; g != "" {
		return m.st.dim(" · " + g)
	}
	return ""
}

// took is " in <n>s" for a run that took a time.
func took(seconds int) string {
	if seconds <= 0 {
		return ""
	}
	return " in " + span(time.Duration(seconds)*time.Second)
}

// askName is an ask by its title when the status lists it, else its id.
func askName(t *ticket, id string) string {
	if t.status != nil {
		for _, a := range t.status.Asks {
			if a.ID == id && a.Title != "" {
				return "“" + a.Title + "”"
			}
		}
	}
	return id
}

// span is a duration as people read it: 45s, 3m 05s, 2h 10m.
func span(d time.Duration) string {
	switch {
	case d < time.Second:
		return "under a second"
	case d < time.Minute:
		return strconv.Itoa(int(d/time.Second)) + "s"
	case d < time.Hour:
		return strconv.Itoa(int(d/time.Minute)) + "m " + twoDigits(int(d%time.Minute/time.Second)) + "s"
	}
	return strconv.Itoa(int(d/time.Hour)) + "h " + twoDigits(int(d%time.Hour/time.Minute)) + "m"
}

func twoDigits(n int) string {
	if n < 10 {
		return "0" + strconv.Itoa(n)
	}
	return strconv.Itoa(n)
}

// between is the time from one line to another, "" when either has no time.
func between(a, b events.Envelope) string {
	ta, okA := a.At()
	tb, okB := b.At()
	if !okA || !okB || tb.Before(ta) {
		return ""
	}
	return span(tb.Sub(ta))
}
