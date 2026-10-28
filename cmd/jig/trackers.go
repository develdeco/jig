package main

import (
	"fmt"
	"io"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/mirror"
)

// init registers this file's verb with the registry (registry.go).
func init() {
	registerVerb(verb{
		Spec: cmdSpec{"trackers", "jig trackers sync: sync the store onto GitHub issues", []flagSpec{
			{"dry-run", "read everything and report what would change, writing nothing", false},
			{"store", "explicit store path", false},
			{"project", "project name, resolved via the machine mapping", false},
		}, false},
		Order: 220,
		Run: func(e env, args []string, stdout io.Writer, _ io.Reader) int {
			return cmdTrackers(e, args, stdout)
		},
		FlagSet: "trackers sync",
		Probe:   []string{"sync"},
	})
}

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
	// What the sync wrote goes out even when it failed part-way: the records
	// it did write are true. Together with the commit Sync made of the
	// leftovers, a run that is not a dry run, on a store with an origin, leaves
	// none of what it wrote or committed unpushed.
	var landErr error
	if !*dryRun {
		landErr = st.LandTrackerWrites()
	}
	if err != nil {
		if landErr != nil {
			st.Warn("the tracker records this sync wrote were not landed on the store's origin: %v", landErr)
		}
		return renderErr(stdout, err)
	}
	if report.NoTracker {
		axi.Render(stdout, axi.KV("trackers", [][2]string{{"status", "no github entry configured"}}))
		if landErr != nil {
			return renderErr(stdout, landErr)
		}
		return 0
	}

	skipped := renderSyncReport(stdout, report, *dryRun)
	if landErr != nil {
		return renderErr(stdout, landErr)
	}
	if skipped {
		return 1
	}
	return 0
}
