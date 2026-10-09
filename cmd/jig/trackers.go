package main

import (
	"fmt"
	"io"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/mirror"
)

// cmdTrackers implements `jig trackers sync [--dry-run] [--store <path>]
// [--project <name>]`.
func cmdTrackers(e env, args []string, stdout io.Writer) int {
	sub, rest, err := requirePositional(args, "subcommand (sync)")
	if err != nil {
		return renderErr(stdout, err)
	}
	if sub == "" {
		// requirePositional passed a leading -h/--help through.
		sub = "sync"
	}
	if sub != "sync" {
		return renderErr(stdout, &axi.Error{
			Msg:  fmt.Sprintf("unknown trackers subcommand %q; only \"sync\" is supported", sub),
			Code: "VALIDATION_ERROR",
		})
	}

	fs := newFlagSet(e, "trackers sync")
	dryRun := fs.Bool("dry-run", false, "read everything and report what would change, writing nothing")
	storeFlag := fs.String("store", "", "explicit store path")
	projectFlag := fs.String("project", "", "project name, resolved via the machine mapping")
	if handled, err := parseFlags(stdout, fs, rest); handled {
		return 0
	} else if err != nil {
		return renderErr(stdout, err)
	}

	st, cfg, _, jigHome, err := resolveStoreForProject(e, *projectFlag, *storeFlag, stdout)
	if err != nil {
		return renderErr(stdout, err)
	}

	if !*dryRun {
		if err := st.Sync(); err != nil {
			return renderErr(stdout, err)
		}
	}

	// jig trackers sync runs on demand rather than from inside another
	// command, so unlike the checkpoint hook it has no reason to share the
	// 2-minute bound a first sync of a sizeable store cannot finish inside
	// (gate finding r2-f5): it runs until it is done.
	report, err := mirror.Sync(mirror.Deps{Store: st, Cfg: cfg, Home: jigHome, Client: e.mirrorClient}, mirror.SyncOpts{DryRun: *dryRun, Unbounded: true})
	if err != nil {
		return renderErr(stdout, err)
	}
	if report.NoTracker {
		axi.Render(stdout, axi.KV("trackers", [][2]string{{"status", "no github entry configured"}}))
		return 0
	}

	if renderSyncReport(stdout, report, *dryRun) {
		return 1
	}
	return 0
}
