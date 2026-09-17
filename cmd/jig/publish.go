package main

import (
	"io"

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
	if err := fs.Parse(rest); err != nil {
		return renderErr(stdout, &axi.Error{Msg: err.Error(), Code: "VALIDATION_ERROR"})
	}

	st, cfg, mp, err := resolveStoreForProject(*projectFlag, *storeFlag)
	if err != nil {
		return renderErr(stdout, err)
	}

	deps := verifydeliverDeps(st, cfg, mp)

	// Check identity before acquiring a lease.
	if err := verifydeliver.CheckIdentity(deps); err != nil {
		return renderErr(stdout, err)
	}

	report, err := verifydeliver.Publish(deps, verifydeliver.PublishOpts{Ticket: ticket, Yes: *yes})
	if err != nil {
		return renderErr(stdout, err)
	}

	var squashRows, prRows, prURLRows [][]string
	for repo, sha := range report.Squashed {
		squashRows = append(squashRows, []string{repo, sha})
	}
	for repo, path := range report.PRBody {
		prRows = append(prRows, []string{repo, path})
	}
	for repo, url := range report.PRURL {
		if url != "" {
			prURLRows = append(prURLRows, []string{repo, url})
		}
	}
	axi.Render(stdout,
		axi.KV("publish", [][2]string{{"ticket", ticket}, {"tier", report.Tier}}),
		axi.Table("squashed", []string{"repo", "sha"}, squashRows),
		axi.Table("pr_body", []string{"repo", "path"}, prRows),
		axi.Table("pr_url", []string{"repo", "url"}, prURLRows),
		axi.Help("Run `jig status "+ticket+"` to confirm the ticket is fully green"),
	)
	return 0
}
