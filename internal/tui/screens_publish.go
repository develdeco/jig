package tui

import (
	"strconv"
	"strings"

	"github.com/develdeco/jig/internal/journal"
)

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
// it happened, and the fields of its detail: what it holds when it is done, what
// it waits for when it is not.
type publishCheck struct {
	key, name string
	done      bool
	text      string
	when      string
	rows      []detailRow
}

// publishPage is the publish: a checklist of what the publish does, a row for
// each (the body jig writes, the person's approval of the reviewed head, the
// branch, a pull request for each repo, the changelog, the end), when each of
// them happened, the detail of the check the cursor is on, and the publish's
// activity.
//
// The detail holds what the status and the journal carry of the check, whole: the
// body's and the changelog's lines with the file that holds each, who approved
// which head, the branch, a pull request's number, state, target and address.
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

	list := m.publishChecks(t)
	done := 0
	rows := make([]listRow, len(list))
	for i, c := range list {
		state, text := StateQueued, m.st.dim(c.text)
		if c.done {
			state, text = StateOK, c.text
			done++
		}
		rows[i] = listRow{ref: &rowRef{kind: rowCheck, id: c.key, ticket: id}, cells: []string{m.st.icon(state), text, m.st.dim(c.when)}}
	}
	c := list[m.cursorAmong(len(list))]
	summary := "not done"
	if c.done {
		summary = strings.TrimSpace("done " + c.when)
	}
	detail := detailSpec{title: c.name, summary: summary, rows: c.rows}
	m.listAndDetail(&p, &detail,
		listSpec{title: "Checklist", summary: strconv.Itoa(done) + " of " + strconv.Itoa(len(list)) + " done", cols: checkColumns, names: checkColumnNames, rows: rows})
	m.stepActivityPanel(&p, t, stepPublish)
	return p
}

// publishChecks are the checks of the publish, each with its detail.
func (m *Model) publishChecks(t *ticket) []publishCheck {
	pub := t.publish()
	var st statusPublish
	if t.status != nil && t.status.Publish != nil {
		st = *t.status.Publish
	}
	var list []publishCheck
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
	// check adds a check with the rows of what is known of it; one that is not done
	// ends in what it waits for, in the sentence its row says.
	check := func(key, name string, done bool, text, when string, rows ...detailRow) {
		if !done {
			rows = append(rows, detailRow{label: "waits for", text: text})
		}
		list = append(list, publishCheck{key: key, name: name, done: done, text: text, when: when, rows: rows})
	}

	if pub.BodyFor != "" {
		rows := []detailRow{{label: "written for", text: repoHeads(pub, func(r publishRepo) string { return r.BodyFor }, commitWords(t, pub.BodyFor))}}
		switch {
		case len(st.Repos) > 0:
			label := "files"
			for _, r := range pub.Repos {
				if file := st.Repos[r.Name]; file != "" {
					rows = append(rows, detailRow{label: label, text: r.Name + " " + file})
					label = ""
				}
			}
		case st.BodyFile != "":
			rows = append(rows, detailRow{label: "file", text: st.BodyFile})
		}
		if st.Body != "" {
			rows = append(rows, detailRow{label: "body", text: st.Body})
		}
		check("body", "body", true, "body written for "+repoHeads(pub, func(r publishRepo) string { return r.BodyFor }, commitWords(t, pub.BodyFor)), at(journal.EventPrBody), rows...)
	} else {
		check("body", "body", false, "body: jig writes it from the brief, the slices and the review", "")
	}

	if pub.ApprovedFor != "" && allApproved(pub) {
		who := firstOf(m.byWhom(t, pub.ApprovedBy, ""), "someone")
		heads := repoHeads(pub, func(r publishRepo) string { return r.ApprovedFor }, short(pub.ApprovedFor))
		when := at(journal.EventPublishApproved)
		rows := []detailRow{{label: "approved by", text: who}, {label: "for", text: heads}}
		if when != "" {
			rows = append(rows, detailRow{label: "at", text: when})
		}
		check("approval", "approval", true, "approved by "+who+" for "+heads, when, rows...)
	} else {
		check("approval", "approval", false, "approval: yours, of the reviewed head (p)", "")
	}

	if pub.Branch != "" {
		rows := []detailRow{{label: "branch", text: pub.Branch}}
		if pub.PR != "" {
			rows = append(rows, detailRow{label: "pushed", text: "yes"})
		}
		check("branch", "branch", pub.PR != "", "branch "+pub.Branch+" pushed", "", rows...)
	} else {
		check("branch", "branch", false, "branch pushed", "")
	}

	pr := func(key, name, repo, url, outcome, when string) {
		rows := []detailRow{{label: "pull request", text: m.prReading(t, url)}, {label: "address", text: url}}
		if repo != "" {
			rows = append([]detailRow{{label: "repo", text: repo}}, rows...)
		}
		if outcome != "" {
			rows = append(rows, detailRow{label: "outcome", text: outcome})
		}
		check(key, name, true, strings.TrimSpace("pull request "+repo+" · "+firstOf(outcome, "recorded")+" · "+m.prReading(t, url)), when, rows...)
	}
	switch {
	case len(pub.Repos) > 0:
		// A pull request for each repo of the publish.
		for _, r := range pub.Repos {
			if r.PR != "" {
				pr("pr:"+r.Name, "pull request "+r.Name, r.Name, r.PR, r.PROutcome, atLine(r.PRAt))
			} else {
				check("pr:"+r.Name, "pull request "+r.Name, false, "pull request "+r.Name+" opened", "")
			}
		}
	case pub.PR != "":
		pr("pr", "pull request", "", pub.PR, pub.PROutcome, at(journal.EventPr))
	default:
		check("pr", "pull request", false, "pull request opened", "")
	}

	if when := at(journal.EventChangelog); when != "" || st.Changelog != "" {
		var rows []detailRow
		if st.ChangelogFile != "" {
			rows = append(rows, detailRow{label: "file", text: st.ChangelogFile})
		}
		if st.Changelog != "" {
			rows = append(rows, detailRow{label: "changelog", text: st.Changelog})
		}
		check("changelog", "changelog", true, "changelog written", when, rows...)
	} else {
		check("changelog", "changelog", false, "changelog: written when the publish finishes", "")
	}

	if pub.Done {
		check("published", "published", true, "published", at(journal.EventPublishDone), detailRow{label: "published", text: "yes"})
	} else {
		check("published", "published", false, "published", "")
	}
	return list
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
