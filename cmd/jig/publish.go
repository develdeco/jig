package main

import (
	"fmt"
	"io"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/verifydeliver"
)

// The usage lines of `jig publish`'s flags: the help row and the FlagSet carry
// the same text, which the help parity test compares.
const (
	publishYesUsage     = "skip the interactive confirm"
	publishBodyUsage    = "render the pull request body and review notes for the reviewed head and stop: nothing is pushed, no lease or branch changes"
	publishPRUsage      = "record the pull request at this URL, which you opened from the ticket's branch, as the ticket's and finish the publish"
	publishApproveUsage = "record that you approve publishing the reviewed head, so `jig publish` pushes it without asking"
	publishRepoUsage    = "the repo to publish, by its name in the project (default: every repo the ticket builds in)"
	publishPicksUsage   = "a picks file: the recordings to show in the pull request's demo, in the pick result's JSON shape"
	publishStoreUsage   = "explicit store path"
	publishProjectUsage = "project name, resolved via the machine mapping"
)

// init registers this file's verb with the registry (registry.go).
func init() {
	registerVerb(verb{
		Spec: cmdSpec{"publish", "render the PR body, push the reviewed head and open or update the PR, or record one you opened", []flagSpec{
			{"yes", publishYesUsage, false},
			{"body", publishBodyUsage, false},
			{"pr", publishPRUsage, false},
			{"approve", publishApproveUsage, false},
			{"repo", publishRepoUsage, false},
			{"picks", publishPicksUsage, false},
			{"store", publishStoreUsage, false},
			{"project", publishProjectUsage, false},
		}, false},
		Order: 170,
		Run: func(e env, args []string, stdout io.Writer, stdin io.Reader) int {
			return cmdPublish(e, args, stdout, stdin)
		},
		Probe: []string{"T-1"},
	})
}

// cmdPublish implements `jig publish <ticket> [--body | --pr <url> | --approve]
// [--repo <name>] [--picks <file>] [--yes]`. Bare, it pushes the head the last
// clean gate round reviewed, as it is, and opens or updates the pull request.
// The three flags are the steps of the same hand-off when an agent does the
// git and gh work: --body renders the pull request body and stops, --pr
// records the pull request the agent opened, --approve records a person's
// approval of the head. Publish reconciles nothing, squashes nothing and
// commits nothing on the branch. A ticket that builds in several repos has a
// pull request for each: every form covers them all, or the one --repo names,
// and --pr takes the repo of the pull request it records.
func cmdPublish(e env, args []string, stdout io.Writer, stdin io.Reader) int {
	ticket, rest, err := requirePositional(args, "ticket")
	if err != nil {
		return renderErr(stdout, err)
	}

	fs := newFlagSet(e, "publish")
	yes := fs.Bool("yes", false, publishYesUsage)
	body := fs.Bool("body", false, publishBodyUsage)
	prURL := fs.String("pr", "", publishPRUsage)
	approve := fs.Bool("approve", false, publishApproveUsage)
	repo := fs.String("repo", "", publishRepoUsage)
	picks := fs.String("picks", "", publishPicksUsage)
	storeFlag := fs.String("store", "", publishStoreUsage)
	projectFlag := fs.String("project", "", publishProjectUsage)
	if handled, err := parseFlags(stdout, fs, rest); handled {
		return 0
	} else if err != nil {
		return renderErr(stdout, err)
	}
	forms := 0
	for _, set := range []bool{*body, *prURL != "", *approve} {
		if set {
			forms++
		}
	}
	if forms > 1 {
		return renderErr(stdout, &axi.Error{
			Msg:  "jig publish takes one of --body, --pr and --approve",
			Code: "VALIDATION_ERROR",
		})
	}
	if *picks, err = e.abs(*picks); err != nil {
		return renderErr(stdout, err)
	}

	st, cfg, mp, jigHome, err := resolveStoreForProject(e, *projectFlag, *storeFlag, stdout)
	if err != nil {
		return renderErr(stdout, err)
	}
	ticket, err = resolveTicketArg(st, ticket, stdout)
	if err != nil {
		return renderErr(stdout, err)
	}
	if err := requireWork(st, ticket); err != nil {
		return renderErr(stdout, err)
	}

	deps := verifydeliverDeps(e, st, cfg, mp, jigHome)
	opts := verifydeliver.PublishOpts{Ticket: ticket, Repo: *repo, Yes: *yes, Picks: *picks}

	switch {
	case *body:
		report, err := verifydeliver.PublishBody(deps, opts)
		if err != nil {
			return renderErr(stdout, err)
		}
		renderBodies(stdout, st.Root, ticket, report)
		return 0
	case *prURL != "":
		report, err := verifydeliver.PublishRecordPR(deps, opts, *prURL, nil)
		if err != nil {
			return renderErr(stdout, err)
		}
		verified := "yes"
		if !report.Verified {
			verified = "no (the repo has no pull-request host to ask)"
		}
		help := "Run `jig status " + ticket + "` to confirm the ticket is fully green"
		if len(report.Pending) > 0 {
			help = fmt.Sprintf("%s still needs its pull request: `jig publish %s --pr <url> --repo %s`", joinWords(report.Pending), ticket, report.Pending[0])
		}
		axi.Render(stdout,
			axi.KV("publish_pr", [][2]string{
				{"ticket", ticket}, {"repo", report.Repo}, {"url", report.URL}, {"branch", report.Branch},
				{"commit", report.Commit}, {"verified", verified},
			}),
			axi.Help(help),
		)
		return 0
	case *approve:
		provenance := "agent"
		if e.stdinIsTerminal(stdin) {
			provenance = "person"
		}
		report, err := verifydeliver.PublishApprove(deps, opts, provenance)
		if err != nil {
			return renderErr(stdout, err)
		}
		heads := make([]string, len(report.Repos))
		for i, r := range report.Repos {
			heads[i] = shortSHA(r.Head)
		}
		hint := fmt.Sprintf("`jig publish %s` now pushes %s without asking", ticket, strings.Join(heads, ", "))
		if provenance != "person" {
			hint = "Only a person at a terminal stands in for the confirmation: this approval was recorded as an agent's, and `jig publish` still asks (or takes --yes)"
		}
		blocks := []string{axi.KV("publish_approved", approvedRows(ticket, report))}
		if len(report.Repos) > 1 {
			rows := make([][]string, len(report.Repos))
			for i, r := range report.Repos {
				rows[i] = []string{r.Repo, r.Head}
			}
			blocks = append(blocks, axi.Table("approved", []string{"repo", "head"}, rows))
		}
		axi.Render(stdout, append(blocks, axi.Help(hint))...)
		return 0
	}

	report, err := verifydeliver.Publish(deps, opts)
	if err != nil {
		return renderErr(stdout, err)
	}
	var prRows [][]string
	for repo, path := range report.PRBody {
		prRows = append(prRows, []string{repo, path})
	}
	head := [][2]string{{"ticket", ticket}, {"tier", report.Tier}}
	help := "Run `jig status " + ticket + "` to confirm the ticket is fully green"
	if len(report.Pending) > 0 {
		head = append(head, [2]string{"pending", strings.Join(report.Pending, ", ")})
		help = fmt.Sprintf("%s still needs its pull request: `jig publish %s --repo %s`", joinWords(report.Pending), ticket, report.Pending[0])
	}
	axi.Render(stdout,
		axi.KV("publish", append(head, picksRows(report.Picks)...)),
		pushedTable(report),
		axi.Table("pr_body", []string{"repo", "path"}, prRows),
		prURLTable(report),
		axi.Help(help),
	)
	return 0
}

// renderBodies prints what `jig publish --body` rendered. A body for one repo
// is a block of its facts; bodies for several are a table with a row for each
// repo, since the branch is the same in all.
func renderBodies(stdout io.Writer, root, ticket string, report verifydeliver.BodyReport) {
	path := func(rel string) string { return filepath.Join(root, filepath.FromSlash(rel)) }
	if len(report.Repos) == 1 {
		r := report.Repos[0]
		kv := [][2]string{
			{"ticket", ticket}, {"repo", r.Repo}, {"branch", report.Branch}, {"base", r.Target},
			{"head", r.Head}, {"title", r.Title}, {"pr_body", path(r.PRBody)}, {"review_notes", path(r.ReviewNotes)},
		}
		kv = append(kv, picksRows(report.Picks)...)
		record, bare := "", fmt.Sprintf("`jig publish %s`", ticket)
		if len(report.Covered) > 1 {
			record, bare = " --repo "+r.Repo, fmt.Sprintf("`jig publish %s --repo %s`", ticket, r.Repo)
		}
		axi.Render(stdout,
			axi.KV("publish_body", kv),
			axi.Help(
				fmt.Sprintf("Push %s to origin, open the pull request with the title and body above (gh pr create --head %s --base %s --title <title> --body-file <pr_body>), then record it: `jig publish %s --pr <url>%s`", report.Branch, report.Branch, r.Target, ticket, record),
				"Or have jig push and open it: "+bare,
			),
		)
		return
	}
	rows := make([][]string, len(report.Repos))
	for i, r := range report.Repos {
		rows[i] = []string{r.Repo, r.Target, r.Head, r.Title, path(r.PRBody), path(r.ReviewNotes)}
	}
	axi.Render(stdout,
		axi.KV("publish_body", append([][2]string{{"ticket", ticket}, {"branch", report.Branch}}, picksRows(report.Picks)...)),
		axi.Table("bodies", []string{"repo", "base", "head", "title", "pr_body", "review_notes"}, rows),
		axi.Help(
			fmt.Sprintf("Push %s to origin in each repo, open a pull request in each with its title and body above (gh pr create --head %s --base <base> --title <title> --body-file <pr_body>), then record each: `jig publish %s --pr <url> --repo <repo>`", report.Branch, report.Branch, ticket),
			fmt.Sprintf("Each body names the others' pull requests, \"pending\" until they are recorded: once all are, render the bodies again and update the pull requests with them. Or have jig push and open them all: `jig publish %s`", ticket),
		),
	)
}

// approvedRows are the facts of an approval: the one repo's head, or for
// several repos the branch alone, the heads being a table.
func approvedRows(ticket string, report verifydeliver.ApproveReport) [][2]string {
	if len(report.Repos) == 1 {
		r := report.Repos[0]
		return [][2]string{{"ticket", ticket}, {"repo", r.Repo}, {"branch", report.Branch}, {"head", r.Head}, {"provenance", report.Provenance}}
	}
	return [][2]string{{"ticket", ticket}, {"branch", report.Branch}, {"provenance", report.Provenance}}
}

// joinWords lists words in prose: "a", "a and b", "a, b and c".
func joinWords(words []string) string {
	if len(words) < 2 {
		return strings.Join(words, "")
	}
	return strings.Join(words[:len(words)-1], ", ") + " and " + words[len(words)-1]
}

// picksRows are the publish report's rows for the recordings it picked: none
// for a build that recorded nothing.
func picksRows(p verifydeliver.PicksReport) [][2]string {
	if p.Status == "" && len(p.Dropped) == 0 {
		return nil
	}
	var rows [][2]string
	if p.Status != "" {
		rows = append(rows, [2]string{"picks", p.Status})
	}
	switch p.Status {
	case verifydeliver.PicksPicked, verifydeliver.PicksReused:
		rows = append(rows, [2]string{"picks_flows", strconv.Itoa(p.Flows)}, [2]string{"picks_files", strconv.Itoa(p.Files)})
	case verifydeliver.PicksRefused:
		rows = append(rows, [2]string{"picks_reason", p.Reason})
	case verifydeliver.PicksPending:
		rows = append(rows, [2]string{"picks_candidates", strconv.Itoa(p.Candidates)}, [2]string{"picks_request", p.Request})
	}
	if len(p.Dropped) > 0 {
		rows = append(rows, [2]string{"picks_dropped", strings.Join(p.Dropped, "; ")})
	}
	return rows
}

// pushedTable is the publish report's account of what each repo's push left on
// origin: the head, which is the head the gate reviewed, unchanged.
func pushedTable(report verifydeliver.PublishReport) string {
	repos := make([]string, 0, len(report.Head))
	for repo := range report.Head {
		repos = append(repos, repo)
	}
	sort.Strings(repos)
	rows := make([][]string, 0, len(repos))
	for _, repo := range repos {
		rows = append(rows, []string{repo, report.Head[repo]})
	}
	return axi.Table("pushed", []string{"repo", "head"}, rows)
}

// prURLTable is the publish report's account of the pull request each repo's
// publish left: its URL, and whether publish opened it or updated the one the
// branch already had open. A repo with no pull request still gets a row,
// naming why in the action column (report.PRNote) rather than being dropped
// silently: the operator cannot otherwise tell "this remote has no
// pull-request host" from "the host returned no URL".
func prURLTable(report verifydeliver.PublishReport) string {
	repos := make([]string, 0, len(report.PRURL))
	for repo := range report.PRURL {
		repos = append(repos, repo)
	}
	sort.Strings(repos)
	rows := make([][]string, 0, len(repos))
	for _, repo := range repos {
		url := report.PRURL[repo]
		action := "opened"
		switch {
		case url != "" && report.PRUpdated[repo]:
			action = "updated"
		case url == "":
			action = report.PRNote[repo]
			if action == "" {
				action = "none"
			}
		}
		rows = append(rows, []string{repo, url, action})
	}
	return axi.Table("pr_url", []string{"repo", "url", "action"}, rows)
}
