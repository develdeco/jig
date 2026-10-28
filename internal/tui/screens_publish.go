package tui

import (
	"strings"

	"github.com/develdeco/jig/internal/journal"
)

// publishPage is the publish: the pull request body jig writes with its first
// lines, the person's approval of the reviewed head, the branch, the pull
// request, the changelog with its first lines, when each of them happened, and
// the publish's activity.
//
// The body and the changelog are the lines the status printed of their files.
// They take the rows the steps and the activity leave, so the screen fits the
// window it has; past that they end in an ellipsis, and the files hold the rest.
func (m *Model) publishPage(id string) page {
	var p page
	t := m.w.tickets[id]
	if t == nil {
		p.add(m.st.dim("no ticket " + id))
		return p
	}
	step := m.steps(t)[stepPublish]
	p.add(m.st.icon(step.State) + " " + m.st.state(step.State, step.Text))
	p.add("")

	pub := t.publish()
	var body, changelog []string
	if t.status != nil && t.status.Publish != nil {
		body, changelog = textLines(t.status.Publish.Body), textLines(t.status.Publish.Changelog)
	}
	// The rows the page has for the two texts: what the window holds less the
	// checks, the rule before the activity and its floor of three lines.
	checks := 6 + max(0, len(pub.Repos)-1)
	activity := 2 + min(3, max(1, len(t.stepLines(stepPublish))))
	bodyN, changelogN := shareRows(len(body), len(changelog), m.bodyRows()-len(p.lines)-checks-1-activity)

	check := func(done bool, text, when string) {
		state := StateQueued
		if done {
			state = StateOK
		} else {
			text = m.st.dim(text)
		}
		if when != "" {
			text += m.st.dim("  " + when)
		}
		p.add(m.st.icon(state) + " " + text)
	}
	record := func(lines []string, n int) {
		for _, l := range clip(lines, n) {
			p.add("    " + m.st.dim(fit(l, m.width-8)))
		}
	}
	at := func(event string) string {
		if i := t.latest("", event); i >= 0 {
			return clock(t.lines[i], m.zone)
		}
		return ""
	}
	atLine := func(i int) string {
		if i < 0 {
			return ""
		}
		return clock(t.lines[i], m.zone)
	}
	if pub.BodyFor != "" {
		check(true, "body written for "+repoHeads(pub, func(r publishRepo) string { return r.BodyFor }, commitWords(t, pub.BodyFor)), at(journal.EventPrBody))
		record(body, bodyN)
	} else {
		check(false, "body: jig writes it from the brief, the slices and the review", "")
	}
	if pub.ApprovedFor != "" && allApproved(pub) {
		check(true, "approved by "+firstOf(m.byWhom(t, pub.ApprovedBy, ""), "someone")+" for "+repoHeads(pub, func(r publishRepo) string { return r.ApprovedFor }, short(pub.ApprovedFor)), at(journal.EventPublishApproved))
	} else {
		check(false, "approval: yours, of the reviewed head (p)", "")
	}
	if pub.Branch != "" {
		check(pub.PR != "", "branch "+pub.Branch+" pushed", "")
	} else {
		check(false, "branch pushed", "")
	}
	switch {
	case len(pub.Repos) > 0:
		// A pull request for each repo of the publish.
		for _, r := range pub.Repos {
			if r.PR != "" {
				check(true, strings.TrimSpace("pull request "+r.Name+"  "+firstOf(r.PROutcome, "recorded")+"  "+r.PR), atLine(r.PRAt))
			} else {
				check(false, "pull request "+r.Name+" opened", "")
			}
		}
	case pub.PR != "":
		check(true, strings.TrimSpace("pull request "+firstOf(pub.PROutcome, "recorded")+"  "+pub.PR), at(journal.EventPr))
	default:
		check(false, "pull request opened", "")
	}
	if when := at(journal.EventChangelog); when != "" || len(changelog) > 0 {
		check(true, "changelog written", when)
		record(changelog, changelogN)
	} else {
		check(false, "changelog: written when the publish finishes", "")
	}
	if pub.Done {
		check(true, "published", at(journal.EventPublishDone))
	} else {
		check(false, "published", "")
	}

	p.add("")
	m.stepActivityPanel(&p, t, stepPublish)
	return p
}

// textLines are the lines of a text the status printed, none for no text.
func textLines(text string) []string {
	if text == "" {
		return nil
	}
	return strings.Split(text, "\n")
}

// shareRows divides room rows between two texts of a and b lines: each gets
// its whole text when both fit, else the first gets three parts of five and the
// second two, whichever is shorter than its share leaving the rest to the other.
// A text that exists gets a row when there is any room at all.
func shareRows(a, b, room int) (int, int) {
	room = max(0, room)
	if a+b <= room {
		return a, b
	}
	second := room * 2 / 5
	first := room - second
	if b < second {
		second, first = b, room-b
	}
	if a < first {
		first, second = a, min(b, room-a)
	}
	if a > 0 && first == 0 && room > 0 {
		first, second = 1, max(0, min(second, room-1))
	}
	return first, second
}

// clip is the first n of lines, the last of them ending in an ellipsis when
// some were left out.
func clip(lines []string, n int) []string {
	if n <= 0 {
		return nil
	}
	if len(lines) <= n {
		return lines
	}
	out := append([]string(nil), lines[:n]...)
	out[n-1] += " …"
	return out
}

// allApproved is whether a person's approval stands for every repo of the
// publish: the one head of a publish of one repo, the head of each repo of a
// publish of several.
func allApproved(pub publishInfo) bool {
	for _, r := range pub.Repos {
		if r.ApprovedFor == "" {
			return false
		}
	}
	return true
}

// repoHeads words the heads a check is about: the one head of a publish of one
// repo, as the caller words it (one), or each repo's head, of the repos that
// have one, for a publish of several ("api ccccccc, web ddddddd"). The commit
// shows with its subject in a body's check and as its short sha in an
// approval's, as each did before, so a publish of one repo is worded by its caller.
func repoHeads(pub publishInfo, head func(publishRepo) string, one string) string {
	if len(pub.Repos) == 0 {
		return one
	}
	var parts []string
	for _, r := range pub.Repos {
		if sha := head(r); sha != "" {
			parts = append(parts, r.Name+" "+short(sha))
		}
	}
	return strings.Join(parts, ", ")
}
