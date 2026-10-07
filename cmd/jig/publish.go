package main

import (
	"io"
	"sort"
	"strconv"
	"strings"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/session"
	"github.com/develdeco/jig/internal/verifydeliver"
)

// cmdPublish implements `jig publish <ticket> [--yes] [--backend
// fake|headless|herdr] [--scenario <dir>]`.
func cmdPublish(args []string, stdout io.Writer) int {
	ticket, rest, err := requirePositional(args, "ticket")
	if err != nil {
		return renderErr(stdout, err)
	}

	fs := newFlagSet("publish")
	yes := fs.Bool("yes", false, "skip the interactive confirm")
	backendFlag := fs.String("backend", "", "session backend that picks the recordings the pull request shows: fake, headless, or herdr")
	scenario := fs.String("scenario", "", "scenario dir for the fake backend")
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
	backend, err := publishBackend(*backendFlag, *scenario)
	if err != nil {
		return renderErr(stdout, err)
	}

	deps := verifydeliverDeps(st, cfg, mp, jigHome)

	// Check identity before acquiring a lease.
	if err := verifydeliver.CheckIdentity(deps); err != nil {
		return renderErr(stdout, err)
	}

	report, err := verifydeliver.Publish(deps, verifydeliver.PublishOpts{Ticket: ticket, Yes: *yes, Backend: backend})
	if err != nil {
		return renderErr(stdout, err)
	}

	var prRows [][]string
	for repo, path := range report.PRBody {
		prRows = append(prRows, []string{repo, path})
	}
	axi.Render(stdout,
		axi.KV("publish", append([][2]string{{"ticket", ticket}, {"tier", report.Tier}}, picksRows(report.Picks)...)),
		pushedTable(report),
		axi.Table("pr_body", []string{"repo", "path"}, prRows),
		prURLTable(report),
		axi.Help("Run `jig status "+ticket+"` to confirm the ticket is fully green"),
	)
	return 0
}

// publishBackend is the session backend publish picks the build's recordings
// with, built the way the other commands build theirs. Publish needs a session
// only for a ticket whose build recorded something, so a backend that is not
// installed does not stop a publish: it is handed over as one that fails when
// it is used, and the pick is then refused with that failure and the gate's
// demo stands in. A backend name jig does not have is an error at once.
func publishBackend(backendFlag, scenario string) (session.Backend, error) {
	kind := backendName(backendFlag, scenario)
	backend, err := session.New(kind, session.Options{ScenarioDir: scenario})
	if err != nil {
		return nil, err
	}
	if err := session.Available(kind); err != nil {
		return unavailableBackend{err}, nil
	}
	return backend, nil
}

// unavailableBackend is a backend that is not there: every dispatch fails with
// the reason it is not.
type unavailableBackend struct{ err error }

func (b unavailableBackend) Run(session.Dispatch) error { return b.err }

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
	}
	if len(p.Dropped) > 0 {
		rows = append(rows, [2]string{"picks_dropped", strings.Join(p.Dropped, "; ")})
	}
	return rows
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
