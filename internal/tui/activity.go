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
// did it by role, the slice by its goal, the commit by its subject.

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
	case journal.EventGateOpen, journal.EventGateRound, journal.EventGateClean, journal.EventTriage,
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

// activityLine is one line of a step's activity: its time, the state it is
// drawn in, and what happened.
func (m *Model) activityLine(t *ticket, l events.Envelope) string {
	return m.st.dim(clock(l, m.zone)) + " " + m.st.icon(eventState(l)) + " " + m.say(t, l)
}

// say is what a journal line records, in words.
func (m *Model) say(t *ticket, l events.Envelope) string {
	slice := "slice " + l.Slice
	who := m.whoAs(t, l.Session, roleOf(l.Event))
	if who == "" {
		who = "a session"
	}
	switch l.Event {
	case journal.EventClaim:
		return who + " took " + slice + goalTail(t, l.Slice, m)
	case journal.EventRelease:
		return who + " let go of " + slice
	case journal.EventDispatch:
		return "jig started a builder on " + slice
	case journal.EventVerifyPending:
		return "verifying " + slice + " at " + commitWords(t, l.Commit)
	case journal.EventOracle:
		cmd := "its oracle"
		if l.Command != "" {
			cmd = "`" + l.Command + "`"
		}
		if l.Outcome == "pass" {
			return slice + ": " + cmd + " passed" + took(l.Seconds)
		}
		return strings.TrimSpace(slice + ": " + cmd + " failed" + took(l.Seconds) + ": " + firstOf(l.Summary, l.Signature))
	case journal.EventVerified:
		return slice + " is green: jig verified " + short(l.Commit)
	case journal.EventVerifyError:
		return strings.TrimSpace(slice + " could not be verified: " + l.Summary)
	case journal.EventQuestion:
		return strings.TrimSpace(who + " asks about " + slice + ": " + firstLine(l.Summary))
	case journal.EventAnswer:
		return strings.TrimSpace(firstOf(m.byWhom(t, l.Provenance, l.Session), "someone") + " answered the question on " + slice + ": " + firstLine(l.Summary))
	case journal.EventStall:
		return strings.TrimSpace(slice + " stalled: " + firstOf(l.Summary, l.Outcome))
	case journal.EventRequeue:
		return slice + " is back in the queue"
	case journal.EventNote, journal.EventCorrection:
		if l.Slice != "" {
			return "note on " + slice + ": " + l.Summary
		}
		return "note: " + l.Summary
	case journal.EventApproval:
		text := firstOf(personName(l.Summary), "someone") + " approved the brief"
		if l.Provenance == "agent" && l.Session != "" {
			text += ", as recorded by " + who
		}
		return text
	case journal.EventRevalidate:
		return "the brief was checked again"
	case journal.EventGateOpen:
		return "a review round opened"
	case journal.EventGateRound:
		return firstOf(m.whoWith(t, l.Session), "the reviewer") + " finished " + roundName(l) + " with findings"
	case journal.EventGateClean:
		return firstOf(m.whoWith(t, l.Session), "the reviewer") + " finished " + roundName(l) + ": clean"
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
		text := "the review sent back " + l.Slice
		if f := t.goals()[l.Slice]; f != "" {
			text += ": " + f
		}
		return text
	case journal.EventPrBody:
		return "the pull request body was written for " + commitWords(t, l.Commit)
	case journal.EventPublishApproved:
		return firstOf(m.byWhom(t, l.Provenance, l.Session), "someone") + " approved publishing " + short(l.Commit)
	case journal.EventPr:
		return strings.TrimSpace("pull request " + firstOf(l.Outcome, "recorded") + " " + l.URL)
	case journal.EventPublishDone:
		return "published"
	}
	return strings.TrimSpace(l.Event + " " + firstOf(l.Summary, l.Outcome))
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

// roundName is "round N" for a line of a numbered round.
func roundName(l events.Envelope) string {
	if l.Attempt > 0 {
		return "round " + strconv.Itoa(l.Attempt)
	}
	return "the round"
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
