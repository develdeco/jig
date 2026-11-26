package tui

import (
	"strconv"
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
	p := m.publishPageLess(id, 0)
	if over := len(p.lines) - m.bodyRows(); over > 0 && !m.paneExpanded("Checklist") {
		// A check that wrapped took rows the sizes did not count: the texts give
		// them up, so the page ends where the window does.
		p = m.publishPageLess(id, over)
	}
	return p
}

// prReading is a pull request named by its address as the digest numbers it, in
// prWords' reading ("PR #7 open into main"); an address the digest does not
// number is shown whole.
func (m *Model) prReading(t *ticket, url string) string {
	for _, pr := range t.digest.prs() {
		if pr.URL == url && pr.Number > 0 {
			return prWords([]digestPR{pr}, true)
		}
	}
	return url
}

// checkColumns are the columns of the publish screen's checklist and
// checkColumnNames name them.
var (
	checkColumns     = []column{{}, {free: true, gap: 1}, {gap: 2, keep: true}}
	checkColumnNames = []string{"", "step", "at"}
)

// publishCheck is a row of the checklist: whether it is done, what it says, when
// it happened, and the lines of its text a person may read under it.
type publishCheck struct {
	done    bool
	text    string
	when    string
	excerpt []string
	// cutValue is the whole text of an excerpt that was clipped, "" when it was
	// not: its last line ends in the cut mark.
	cutValue string
}

// publishPageLess is publishPage with the two texts held to less rows than the
// room the page's sizes give them.
func (m *Model) publishPageLess(id string, less int) page {
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
	// checklist with its edges and names, and the activity's floor of three lines
	// with its own.
	checks := 3 + 6 + max(0, len(pub.Repos)-1)
	activity := 3 + min(3, max(1, len(t.stepLines(stepPublish))))
	bodyN, changelogN := shareRows(len(body), len(changelog), m.bodyRows()-m.taken(&p)-checks-activity-less)
	if m.paneExpanded("Checklist") {
		// An expanded checklist holds the texts whole; the page is read by lines.
		bodyN, changelogN = len(body), len(changelog)
	}

	var list []publishCheck
	check := func(done bool, text, when string) {
		list = append(list, publishCheck{done: done, text: text, when: when})
	}
	record := func(lines []string, n int) {
		var ex []string
		for _, l := range clip(lines, n) {
			ex = append(ex, "    "+m.st.dim(l))
		}
		list[len(list)-1].excerpt = ex
		if n > 0 && len(lines) > n {
			list[len(list)-1].cutValue = strings.Join(lines, "\n")
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
				check(true, strings.TrimSpace("pull request "+r.Name+" · "+firstOf(r.PROutcome, "recorded")+" · "+m.prReading(t, r.PR)), atLine(r.PRAt))
			} else {
				check(false, "pull request "+r.Name+" opened", "")
			}
		}
	case pub.PR != "":
		check(true, strings.TrimSpace("pull request "+firstOf(pub.PROutcome, "recorded")+" · "+m.prReading(t, pub.PR)), at(journal.EventPr))
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

	done := 0
	cells := make([][]string, len(list))
	for i, c := range list {
		state, text := StateQueued, m.st.dim(c.text)
		if c.done {
			state, text = StateOK, c.text
			done++
		}
		cells[i] = []string{m.st.icon(state), text, m.st.dim(c.when)}
	}
	p.boxTop("Checklist", strconv.Itoa(done)+" of "+strconv.Itoa(len(list))+" done")
	laid := m.laidList(&p, checkColumns, dimmed(m.st, checkColumnNames), cells, 0)
	for i, c := range list {
		p.boxLaid(nil, laid[i])
		for i, l := range c.excerpt {
			p.boxUnder(l)
			if c.cutValue != "" && i == len(c.excerpt)-1 {
				// The clip's own ellipsis is the mark: it is the line's last cell.
				p.lines[len(p.lines)-1].cuts = []cutMark{{x: width(l) - 1, value: c.cutValue}}
			}
		}
	}
	p.boxEnd()
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
