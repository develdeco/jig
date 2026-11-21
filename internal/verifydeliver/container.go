package verifydeliver

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/project"
	"github.com/develdeco/jig/internal/repohost"
	"github.com/develdeco/jig/internal/store"
)

// PublishNotReady is the code of a container's publish refused because a child
// has not landed (or the container has no child).
const PublishNotReady = "PUBLISH_NOT_READY"

// The states a ContainerStanding is in, as land.ReadinessOf names them.
const (
	ContainerWaiting   = "waiting"
	ContainerReady     = "ready"
	ContainerPublished = "published"
	ContainerLanded    = "landed"
)

// ContainerWait is a child of a container that has not landed, with where it
// stands.
type ContainerWait struct{ Ticket, Standing string }

// ContainerUnmet is a precondition of a container's publish that is not met:
// its code, why, and what to ask to meet it.
type ContainerUnmet struct{ Code, Why, Prompt string }

// ContainerStanding is where a multi-task or an issue stands before its own
// pull request (land.ReadinessOf, which this package cannot import: the command
// hands it in as Deps.Container).
type ContainerStanding struct {
	State string
	Waits []ContainerWait
	Unmet []ContainerUnmet
}

// childRow is one child of a container as its pull request body names it: the
// ticket, its title, the pull requests it landed (or opened) into the
// container, and, for a multi-task of an issue, its own tasks.
type childRow struct {
	ID, Title string
	URLs      []string
	Tasks     []childRow
}

// containerNotReady is the refusal of a publish whose container is not ready: a
// child not landed is PUBLISH_NOT_READY naming each with its standing, an unmet
// precondition its own code, and a container already landed has nothing to
// open.
func containerNotReady(ticket string, s ContainerStanding, hasChildren bool) error {
	switch {
	case s.State == ContainerLanded:
		return &axi.Error{
			Msg:  fmt.Sprintf("%s has landed: its pull request is merged", ticket),
			Code: PublishNotReady,
			Help: []string{"Nothing is left to publish"},
		}
	case len(s.Waits) > 0:
		rows := make([]string, len(s.Waits))
		for i, w := range s.Waits {
			rows[i] = w.Ticket + " (" + w.Standing + ")"
		}
		return &axi.Error{
			Msg:  fmt.Sprintf("%s is not ready to publish: %d of its children have not landed: %s", ticket, len(s.Waits), strings.Join(rows, ", ")),
			Code: PublishNotReady,
			Help: []string{fmt.Sprintf("Land them (`jig next %s` lists them), then publish %s again", ticket, ticket)},
		}
	case len(s.Unmet) > 0:
		u := s.Unmet[0]
		help := []string{"Meet it, then publish " + ticket + " again"}
		if u.Prompt != "" {
			help = []string{fmt.Sprintf("Ask `%s`, then publish %s again", u.Prompt, ticket)}
		}
		return &axi.Error{Msg: fmt.Sprintf("%s is not ready to publish: %s", ticket, u.Why), Code: u.Code, Help: help}
	case !hasChildren || s.State == ContainerWaiting:
		return &axi.Error{
			Msg:  fmt.Sprintf("%s is not ready to publish: it has no children yet", ticket),
			Code: PublishNotReady,
			Help: []string{"Graduate its chart, build and land its tasks, then publish it"},
		}
	}
	return nil
}

// containerChildren are the children of a multi-task or an issue, in chart
// order: the tasks under a multi-task, the top-level entries of an issue's
// chart with a multi-task's tasks nested under it. Each names the pull requests
// it landed into the container, else the ones it opened.
func containerChildren(d Deps, container string, rec store.Ticket, repo string, qualified bool) ([]childRow, error) {
	st := d.Store
	var order []string
	nested := map[string][]string{}
	issue := container
	if rec.KindOrDefault() == store.KindMultiTask {
		ids, err := st.IssuesOf(container)
		if err != nil {
			return nil, err
		}
		issue = ""
		if len(ids) > 0 {
			issue = ids[0]
		}
	}
	if issue != "" {
		chart, err := st.ChartOfIssue(issue)
		if err != nil {
			return nil, err
		}
		if chart != "" {
			entries, err := st.ReadChart(chart)
			if err != nil {
				return nil, err
			}
			for _, e := range entries {
				if e.ID == "" {
					continue
				}
				r, err := st.ReadTicket(e.ID)
				if err != nil {
					continue
				}
				switch {
				case r.Parent == "" && rec.KindOrDefault() == store.KindIssue:
					order = append(order, e.ID)
				case r.Parent != "":
					nested[r.Parent] = append(nested[r.Parent], e.ID)
				}
			}
		}
	}
	if rec.KindOrDefault() == store.KindMultiTask {
		order = nested[container]
		if len(order) == 0 {
			var err error
			if order, err = st.TasksUnder(container, rec); err != nil {
				return nil, err
			}
		}
	}
	var out []childRow
	for _, id := range order {
		row, err := childOf(d, id, container, repo, qualified)
		if err != nil {
			return nil, err
		}
		if rec.KindOrDefault() == store.KindIssue {
			for _, sub := range nested[id] {
				t, err := childOf(d, sub, id, repo, qualified)
				if err != nil {
					return nil, err
				}
				row.Tasks = append(row.Tasks, t)
			}
		}
		out = append(out, row)
	}
	return out, nil
}

// childOf is the row of ticket id, which went into container: the pull
// requests of its landed lines for that container, or, when none is recorded,
// of its pr lines. A project of several repos names only the repo's own.
func childOf(d Deps, id, container, repo string, qualified bool) (childRow, error) {
	rec, err := d.Store.ReadTicket(id)
	if err != nil {
		return childRow{}, err
	}
	row := childRow{ID: id, Title: d.Store.TicketName(id, rec)}
	lines, err := journal.ReadFor(d.Store, id, container)
	if err != nil {
		return childRow{}, err
	}
	pick := func(event string) []string {
		latest := map[string]string{}
		var order []string
		for _, l := range lines {
			if l.Event != event || l.URL == "" || (qualified && l.Repo != "" && l.Repo != repo) {
				continue
			}
			if _, ok := latest[l.Repo]; !ok {
				order = append(order, l.Repo)
			}
			latest[l.Repo] = l.URL
		}
		var urls []string
		for _, r := range order {
			urls = append(urls, latest[r])
		}
		return urls
	}
	if row.URLs = pick(journal.EventLanded); len(row.URLs) == 0 {
		row.URLs = pick(journal.EventPr)
	}
	return row, nil
}

// renderContainerBody is the pull request body of a multi-task or an issue: its
// title, for an issue its need, the other repos' pull requests, one line per
// child with its landed pull request, and the project's trailer. No round, no
// demo and no verification: every line was reviewed in its task.
func renderContainerBody(ticket, title, need string, children []childRow, links []prLink, trailer string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "# %s\n\n", title)
	if need = strings.TrimSpace(need); need != "" {
		b.WriteString(need + "\n\n")
	}
	b.WriteString(renderPartOfSection(ticket, links))
	b.WriteString("## Children\n\n")
	var line func(c childRow, depth int)
	line = func(c childRow, depth int) {
		fmt.Fprintf(&b, "%s- %s (%s)", strings.Repeat("  ", depth), c.Title, c.ID)
		if len(c.URLs) > 0 {
			b.WriteString(": " + strings.Join(c.URLs, ", "))
		}
		b.WriteString("\n")
		for _, t := range c.Tasks {
			line(t, depth+1)
		}
	}
	for _, c := range children {
		line(c, 0)
	}
	b.WriteString(renderTrailer(trailer))
	return b.String()
}

// containerShipment is one repo's pull request of a container while it is
// under way.
type containerShipment struct {
	pt       *publishTarget
	host     repohost.Host
	open     repohost.OpenPR
	children []childRow
	pub      publication
	linked   []prLink
}

// PublishContainer opens the pull request of a multi-task (from its branch into
// its issue's) or of an issue (into the repo's target), in each repo its tasks
// build in, once every child has landed (Deps.Container says so). No round and
// no slice precedes it: every line was reviewed in its task. It pushes nothing:
// the container's branch is the base its first task's pull request went into,
// and one the host lacks is the host's refusal. It writes, as a task's publish
// does, the pr-body, publish-approved, pr and publish-done lines, and the body
// under <ticket>/pr/<repo>.md, and nothing at all when it refuses.
func PublishContainer(d Deps, o PublishOpts) (report PublishReport, err error) {
	ticket := o.Ticket
	fn := d.publishFuncs()
	rec, err := d.Store.ReadTicket(ticket)
	if err != nil {
		return PublishReport{}, err
	}
	if rec.KindOrDefault() == store.KindTask || d.Container == nil || d.Stack == nil {
		return PublishReport{}, fmt.Errorf("verifydeliver: publish: %s is not a multi-task or an issue", ticket)
	}

	var journaled bool
	defer func() {
		if err != nil && journaled {
			_ = d.Store.Push(fmt.Sprintf("%s: publish failed: %s", ticket, failureCode(err)))
		}
	}()

	plan, err := d.Stack(ticket)
	if err != nil {
		return PublishReport{}, err
	}
	qualified := len(d.Cfg.Repos) > 1
	standing, err := d.Container(ticket)
	if err != nil {
		return PublishReport{}, err
	}
	if err := containerNotReady(ticket, standing, len(plan.Repos) > 0); err != nil {
		return PublishReport{}, err
	}
	into, _, err := d.Store.ContainerOf(ticket, rec)
	if err != nil {
		return PublishReport{}, err
	}
	title := publishTitle(d.Store.TicketName(ticket, rec), ticket)
	var covered []string
	for _, r := range plan.Repos {
		covered = append(covered, r.Repo)
	}

	// Everything that can refuse runs before the first write.
	var ships []*containerShipment
	branch := ""
	for _, r := range plan.Repos {
		b, err := d.Store.ResolveTicketBranch(ticket, rec, r.Target)
		if err != nil {
			return PublishReport{}, inRepo(qualified, r.Repo, err)
		}
		if branch != "" && b != branch {
			return PublishReport{}, fmt.Errorf("verifydeliver: publish: the branch of %s is %s in one repo and %s in another", ticket, branch, b)
		}
		branch = b
		repo := repoNamed(d.Cfg, r.Repo)
		pt := &publishTarget{
			ticket: ticket, repo: repo, repoName: r.Repo, target: r.Target, rec: rec, branch: b, title: title,
			covered: covered, qualified: qualified, stack: r, into: into,
		}
		host, err := d.publishHost(repo.Remote)
		if err != nil {
			return PublishReport{}, inRepo(qualified, r.Repo, fmt.Errorf("verifydeliver: publish: build repo host: %w", err))
		}
		var open repohost.OpenPR
		if host != nil {
			if open, err = host.FindOpenPRInto(b, pt.base()); err != nil {
				return PublishReport{}, inRepo(qualified, r.Repo, fmt.Errorf("verifydeliver: publish: look for an open pull request from %s: %w", b, err))
			}
		}
		children, err := containerChildren(d, ticket, rec, r.Repo, qualified)
		if err != nil {
			return PublishReport{}, err
		}
		ships = append(ships, &containerShipment{pt: pt, host: host, open: open, children: children})
	}

	answeredYes := false
	if !o.Yes {
		openPR, hasHost := "", false
		for _, sh := range ships {
			hasHost = hasHost || sh.host != nil
			if openPR == "" {
				openPR = sh.open.URL
			}
		}
		if !fn.confirm(branch, ticket, openPR, hasHost) {
			return PublishReport{}, &axi.Error{Msg: "publish declined at confirmation", Code: "PUBLISH_DECLINED"}
		}
		answeredYes = true
	}

	journaled = true
	jlines, err := journal.ReadFor(d.Store, ticket, into)
	if err != nil {
		return PublishReport{}, fmt.Errorf("verifydeliver: publish: read journal: %w", err)
	}
	known := recordedPRs(jlines)
	for _, sh := range ships {
		if sh.open.URL != "" {
			known[sh.pt.repoName] = sh.open.URL
		}
	}
	provenance := "agent"
	if answeredYes && d.StdinIsTerminal != nil && d.StdinIsTerminal() {
		provenance = "person"
	}
	for _, sh := range ships {
		pt := sh.pt
		sh.linked = linksFor(pt, known)
		sh.pub, err = writeContainerBody(d, pt, ticket, title, rec.Body, sh.children, sh.linked)
		if err != nil {
			return PublishReport{}, inRepo(qualified, pt.repoName, err)
		}
		if err := pt.appendLine(d, journal.Line{Event: journal.EventPrBody, Branch: pt.branch, Ref: sh.pub.prPath, Repo: pt.lineRepo()}); err != nil {
			return PublishReport{}, fmt.Errorf("verifydeliver: publish: journal pr-body: %w", err)
		}
		line := journal.Line{Event: journal.EventPublishApproved, Branch: pt.branch, Provenance: provenance, Repo: pt.lineRepo(), Summary: approverIdentity(d.Store.Root)}
		if err := pt.appendLine(d, line); err != nil {
			return PublishReport{}, fmt.Errorf("verifydeliver: publish: journal publish-approved: %w", err)
		}
	}

	report = PublishReport{
		Tier: "none", Head: map[string]string{}, PRBody: map[string]string{}, What: map[string]WhatReport{}, FinalRun: map[string]FinalRunReport{}, PRURL: map[string]string{},
		PRUpdated: map[string]bool{}, PRRetargeted: map[string]string{}, PRNote: map[string]string{},
		ReviewNotes: map[string]ReviewNotesReport{},
	}
	for _, sh := range ships {
		pt := sh.pt
		if links := linksFor(pt, known); !sameLinks(links, sh.linked) {
			if sh.pub, err = writeContainerBody(d, pt, ticket, title, rec.Body, sh.children, links); err != nil {
				return PublishReport{}, inRepo(qualified, pt.repoName, err)
			}
			sh.linked = links
		}
		url, outcome, retargeted, err := openOrUpdatePR(d, sh.host, pt, sh.pub, sh.open, fn.warn)
		if err != nil {
			return PublishReport{}, inRepo(qualified, pt.repoName, err)
		}
		if err := pt.appendLine(d, journal.Line{Event: journal.EventPr, Outcome: outcome, URL: url, Branch: pt.branch, Repo: pt.lineRepo()}); err != nil {
			return PublishReport{}, fmt.Errorf("verifydeliver: publish: journal pr: %w", err)
		}
		known[pt.repoName] = url
		report.PRBody[pt.repoName] = sh.pub.prPath
		report.PRURL[pt.repoName] = url
		report.PRUpdated[pt.repoName] = outcome == "updated"
		if retargeted != "" {
			report.PRRetargeted[pt.repoName] = retargeted
		}
		if sh.host == nil {
			report.PRNote[pt.repoName] = "no pull-request host"
		}
	}
	// The pull requests opened first named the others' as not yet opened.
	for _, sh := range ships {
		url := report.PRURL[sh.pt.repoName]
		links := linksFor(sh.pt, known)
		if url == "" || sameLinks(links, sh.linked) {
			continue
		}
		if sh.pub, err = writeContainerBody(d, sh.pt, ticket, title, rec.Body, sh.children, links); err != nil {
			return PublishReport{}, inRepo(qualified, sh.pt.repoName, err)
		}
		sh.linked = links
		if _, _, _, err := openOrUpdatePR(d, sh.host, sh.pt, sh.pub, repohost.OpenPR{URL: url, Base: sh.pt.base()}, fn.warn); err != nil {
			return PublishReport{}, inRepo(qualified, sh.pt.repoName, err)
		}
	}

	lines, err := journal.ReadFor(d.Store, ticket, into)
	if err != nil {
		return PublishReport{}, fmt.Errorf("verifydeliver: publish: read journal: %w", err)
	}
	report.Pending = pendingRepos(lines, covered)
	if len(report.Pending) == 0 {
		if err := ships[0].pt.appendLine(d, journal.Line{Event: journal.EventPublishDone}); err != nil {
			return PublishReport{}, fmt.Errorf("verifydeliver: publish: journal publish-done: %w", err)
		}
	}
	if err := d.Store.Push(fmt.Sprintf("%s: publish", ticket)); err != nil {
		return PublishReport{}, fmt.Errorf("verifydeliver: publish: push store: %w", err)
	}
	return report, nil
}

// repoNamed is the project's repo called name, the zero repo when there is
// none (a project that names no repo has one that is not named).
func repoNamed(cfg project.Config, name string) project.Repo {
	for _, r := range cfg.Repos {
		if r.Name() == name {
			return r
		}
	}
	return project.Repo{}
}

// writeContainerBody writes the body of the container's pull request in the
// repo of pt to <ticket>/pr/<repo>.md.
func writeContainerBody(d Deps, pt *publishTarget, ticket, title, need string, children []childRow, links []prLink) (publication, error) {
	rel := filepath.ToSlash(filepath.Join(d.Store.TicketRelDir(ticket), "pr", pt.repoName+".md"))
	full := filepath.Join(d.Store.Root, filepath.FromSlash(rel))
	body := renderContainerBody(ticket, title, need, children, links, d.Cfg.PublishTrailer)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return publication{}, fmt.Errorf("verifydeliver: pr body: create dir: %w", err)
	}
	if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
		return publication{}, fmt.Errorf("verifydeliver: pr body: write: %w", err)
	}
	return publication{prPath: rel, title: title}, nil
}
