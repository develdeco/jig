package main

import (
	"io"
	"sort"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/verifydeliver"
)

// cmdPublish implements `jig publish <ticket> [--yes]`.
func cmdPublish(args []string, stdout io.Writer) int {
	ticket, rest, err := requirePositional(args, "ticket")
	if err != nil {
		return renderErr(stdout, err)
	}

	fs := newFlagSet("publish")
	yes := fs.Bool("yes", false, "skip the interactive confirm")
	storeFlag := fs.String("store", "", "explicit store path")
	projectFlag := fs.String("project", "", "project name, resolved via the machine mapping")
	if handled, err := parseFlags(stdout, fs, rest); handled {
		return 0
	} else if err != nil {
		return renderErr(stdout, err)
	}

	st, cfg, mp, jigHome, err := resolveStoreForProject(*projectFlag, *storeFlag)
	if err != nil {
		return renderErr(stdout, err)
	}
	if err := requireWork(st, ticket); err != nil {
		return renderErr(stdout, err)
	}

	deps := verifydeliverDeps(st, cfg, mp, jigHome)

	// Check identity before acquiring a lease.
	if err := verifydeliver.CheckIdentity(deps); err != nil {
		return renderErr(stdout, err)
	}

	report, err := verifydeliver.Publish(deps, verifydeliver.PublishOpts{Ticket: ticket, Yes: *yes})
	if err != nil {
		return renderErr(stdout, err)
	}

	var prRows [][]string
	for repo, path := range report.PRBody {
		prRows = append(prRows, []string{repo, path})
	}
	axi.Render(stdout,
		axi.KV("publish", [][2]string{{"ticket", ticket}, {"tier", report.Tier}}),
		pushedTable(report),
		axi.Table("pr_body", []string{"repo", "path"}, prRows),
		prURLTable(report),
		axi.Help("Run `jig status "+ticket+"` to confirm the ticket is fully green"),
	)
	return 0
}

// pushedTable is the publish report's account of what each repo's push left on
// origin: the head, and whether publish squashed the branch or pushed it as it
// was because it was already on origin (verifydeliver.NotSquashed).
func pushedTable(report verifydeliver.PublishReport) string {
	repos := make([]string, 0, len(report.Head))
	for repo := range report.Head {
		repos = append(repos, repo)
	}
	sort.Strings(repos)
	rows := make([][]string, 0, len(repos))
	for _, repo := range repos {
		rows = append(rows, []string{repo, report.Head[repo], report.Squash(repo)})
	}
	return axi.Table("pushed", []string{"repo", "head", "squash"}, rows)
}

// prURLTable is the publish report's account of the pull request each repo's
// publish left: its URL, and whether publish opened it or updated the one the
// branch already had open. A repo whose tracker opens no pull requests has no
// row.
func prURLTable(report verifydeliver.PublishReport) string {
	repos := make([]string, 0, len(report.PRURL))
	for repo, url := range report.PRURL {
		if url != "" {
			repos = append(repos, repo)
		}
	}
	sort.Strings(repos)
	rows := make([][]string, 0, len(repos))
	for _, repo := range repos {
		action := "opened"
		if report.PRUpdated[repo] {
			action = "updated"
		}
		rows = append(rows, []string{repo, report.PRURL[repo], action})
	}
	return axi.Table("pr_url", []string{"repo", "url", "action"}, rows)
}
