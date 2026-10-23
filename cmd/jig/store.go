package main

import (
	"fmt"
	"io"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/migrate"
)

// init registers this file's verb with the registry (registry.go).
func init() {
	registerVerb(verb{
		Spec: cmdSpec{"store", "jig store migrate: move a store up one schema_version (v1 to v2 with a rename map, v2 to v3 without)", []flagSpec{
			{"map", "the rename map file (old id to new key); the v1 to v2 step only", false},
			{"dry-run", "print what the next step would move, delete, write or rewrite, changing nothing", false},
			{"to", "the schema_version to migrate to: only the next one is applied (default: the next one)", false},
			{"store", "explicit store path", false},
			{"project", "project name, resolved via the machine mapping", false},
		}, false},
		Order: 230,
		Run: func(e env, args []string, stdout io.Writer, _ io.Reader) int {
			return cmdStore(e, args, stdout)
		},
		FlagSet: "store migrate",
		Probe:   []string{"migrate"},
	})
}

// cmdStore implements `jig store <subcommand>`.
func cmdStore(e env, args []string, stdout io.Writer) int {
	sub, rest, err := requirePositional(args, "subcommand (migrate)")
	if err != nil {
		return renderErr(stdout, err)
	}
	if sub == "" {
		// requirePositional passed a leading -h/--help through.
		sub = "migrate"
	}
	if sub != "migrate" {
		return renderErr(stdout, &axi.Error{
			Msg:  fmt.Sprintf("unknown store subcommand %q; only \"migrate\" is supported", sub),
			Code: "VALIDATION_ERROR",
		})
	}
	return cmdStoreMigrate(e, rest, stdout)
}

// cmdStoreMigrate implements `jig store migrate [--map <file>] [--dry-run]
// [--to <n>]`: it applies exactly the next migration step (migrate.Steps) to
// the store, so each step stays one reviewable store pull request. It is the one
// command that may open a store older than the current schema
// (resolveStoreForProjectAllowingOldSchema, unlike every other command's
// resolveStore, lets an older schema_version through), since moving one up is
// its whole job. A v1 store takes the rename map (brief.md#The rename map,
// brief.md#The migration); a v2 store takes none.
func cmdStoreMigrate(e env, args []string, stdout io.Writer) int {
	fs := newFlagSet(e, "store migrate")
	mapFlag := fs.String("map", "", "the rename map file (old id to new key); the v1 to v2 step only")
	dryRun := fs.Bool("dry-run", false, "print what the next step would move, delete, write or rewrite, changing nothing")
	toFlag := fs.Int("to", 0, "the schema_version to migrate to: only the next one is applied (default: the next one)")
	storeFlag := fs.String("store", "", "explicit store path")
	projectFlag := fs.String("project", "", "project name, resolved via the machine mapping")
	if handled, err := parseFlags(stdout, fs, args); handled {
		return 0
	} else if err != nil {
		return renderErr(stdout, err)
	}

	st, cfg, _, jigHome, err := resolveStoreForProjectAllowingOldSchema(e, *projectFlag, *storeFlag, stdout)
	if err != nil {
		return renderErr(stdout, err)
	}
	step, err := migrate.NextStep(cfg, *toFlag)
	if err != nil {
		return renderErr(stdout, err)
	}
	in := migrate.Input{Store: st, Cfg: cfg, JigHome: jigHome}
	if *mapFlag != "" {
		if !step.UsesMap {
			return renderErr(stdout, &axi.Error{
				Msg:  fmt.Sprintf("--map applies to the step from schema_version 1; this store is at %d, and its next step takes no rename map", cfg.SchemaVersion),
				Code: "VALIDATION_ERROR",
				Help: []string{"Run `jig store migrate` without --map"},
			})
		}
		if in.MapPath, err = e.abs(*mapFlag); err != nil {
			return renderErr(stdout, err)
		}
	}

	plan, err := step.Plan(in)
	if err != nil {
		return renderErr(stdout, err)
	}

	if *dryRun {
		renderMigratePlan(stdout, plan)
		axi.Render(stdout, axi.Help("Nothing was changed: rerun without --dry-run to carry this out"))
		return 0
	}

	if err := migrate.CheckClean(st); err != nil {
		return renderErr(stdout, err)
	}
	if err := migrate.CheckLevelWithOrigin(st); err != nil {
		return renderErr(stdout, err)
	}
	warnings, err := step.Check(in, plan)
	if err != nil {
		return renderErr(stdout, err)
	}
	for _, w := range warnings {
		axi.Render(stdout, axi.KV("warning", [][2]string{{"message", w}}))
	}

	renderMigratePlan(stdout, plan)
	if err := step.Apply(in, plan); err != nil {
		return renderErr(stdout, err)
	}

	axi.Render(stdout, axi.Help(step.Help(in, plan)...))
	return 0
}

// renderMigratePlan prints plan's rename map (when it renames anything) and
// file operations, the same table shapes for both the dry run and the apply.
func renderMigratePlan(stdout io.Writer, plan migrate.Plan) {
	if len(plan.Renames) > 0 {
		renameRows := make([][]string, len(plan.Renames))
		for i, r := range plan.Renames {
			renameRows[i] = []string{r.OldID, r.NewID, r.Title}
		}
		axi.Render(stdout, axi.Table("rename", []string{"old", "new", "title"}, renameRows))
	}

	fileRows := make([][]string, len(plan.Files))
	for i, f := range plan.Files {
		fileRows[i] = []string{f.Action, f.Path}
	}
	axi.Render(stdout, axi.Table("files", []string{"action", "path"}, fileRows))
}
