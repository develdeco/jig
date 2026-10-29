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
		Spec: cmdSpec{"store", "jig store migrate: move a store up one schema_version (v1 to v2 with a rename map, v2 to v3 without), or with --writers to writers mode", []flagSpec{
			{"map", "the rename map file (old id to new key); the v1 to v2 step only", false},
			{"dry-run", "print what the next step would move, delete, write or rewrite, changing nothing", false},
			{"to", "the schema_version to migrate to: only the next one is applied (default: the next one)", false},
			{"writers", "move the store to writers mode: one branch per writer, a claims branch for ids and drivers; the schema stays as it is", false},
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
	writersFlag := fs.Bool("writers", false, "move the store to writers mode: one branch per writer, a claims branch for ids and drivers; the schema stays as it is")
	storeFlag := fs.String("store", "", "explicit store path")
	projectFlag := fs.String("project", "", "project name, resolved via the machine mapping")
	if handled, err := parseFlags(stdout, fs, args); handled {
		return 0
	} else if err != nil {
		return renderErr(stdout, err)
	}
	if *writersFlag {
		if *mapFlag != "" || *toFlag != 0 {
			return renderErr(stdout, &axi.Error{
				Msg:  "--writers moves the store to writers mode and does not change its schema_version: it takes neither --map nor --to",
				Code: "VALIDATION_ERROR",
				Help: []string{"Run `jig store migrate --writers` on its own, and `jig store migrate` separately for a schema step"},
			})
		}
		return cmdStoreWriters(e, stdout, *projectFlag, *storeFlag, *dryRun)
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

// cmdStoreWriters implements `jig store migrate --writers [--dry-run]`: it
// moves the store to writers mode (store.MigrateToWriters), or joins this
// clone to a store another clone already moved. The schema stays as it is, so
// it works on a store of any schema the other verbs open. It refuses a store
// with uncommitted changes, and one that is not level with its origin unless
// it is in writers mode already.
func cmdStoreWriters(e env, stdout io.Writer, projectFlag, storeFlag string, dryRun bool) int {
	st, _, _, _, err := resolveStoreForProject(e, projectFlag, storeFlag, stdout)
	if err != nil {
		return renderErr(stdout, err)
	}
	plan, err := st.PlanWriters()
	if err != nil {
		return renderErr(stdout, err)
	}
	if dryRun {
		axi.Render(stdout, writersBlock(plan.Writer, plan.Branch, claimsState(plan.HasClaims, false), plan.IDs, plan.Carried))
		axi.Render(stdout, axi.Help("Nothing was changed: rerun without --dry-run to carry this out"))
		return 0
	}
	if err := migrate.CheckClean(st); err != nil {
		return renderErr(stdout, err)
	}
	if !plan.HasClaims {
		if err := migrate.CheckLevelWithOrigin(st); err != nil {
			return renderErr(stdout, err)
		}
	}
	res, err := st.MigrateToWriters()
	if err != nil {
		return renderErr(stdout, err)
	}
	axi.Render(stdout,
		writersBlock(res.Writer, res.Branch, claimsState(res.Joined, true), res.IDs, res.Carried),
		axi.Help(
			fmt.Sprintf("This clone now writes to %s and merges the other writers in; main is never pushed again and stays on the origin as history", res.Branch),
			"A clone made from here on joins by itself; one made before runs `jig store migrate --writers` to join, until then it keeps pushing main, which every writer still reads",
			"Two clones must not share a writer name: set `git config jig.writer <name>` in each",
		),
	)
	return 0
}

// claimsState words what the migration did to the claims branch.
func claimsState(joined, done bool) string {
	switch {
	case joined && done:
		return "joined"
	case joined:
		return "exists"
	case done:
		return "created"
	}
	return "to create"
}

// writersBlock is the writers block both the plan and the result print.
func writersBlock(writer, branch, claims string, ids, carried int) string {
	pairs := [][2]string{{"writer", writer}, {"branch", branch}, {"claims", claims}}
	if claims == "created" || claims == "to create" {
		pairs = append(pairs, [2]string{"ids", fmt.Sprint(ids)})
		if carried > 0 {
			pairs = append(pairs, [2]string{"carried", fmt.Sprint(carried)})
		}
	}
	return axi.KV("writers", pairs)
}

// renderMigratePlan prints plan's rename map (when it renames anything), file
// operations and the duplicate ledger and contract index entries it drops
// (when it drops any), the same table shapes for both the dry run and the
// apply.
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

	if len(plan.Collapsed) > 0 {
		collapsedRows := make([][]string, len(plan.Collapsed))
		for i, c := range plan.Collapsed {
			collapsedRows[i] = []string{c.Path, fmt.Sprint(c.Entries)}
		}
		axi.Render(stdout, axi.Table("collapsed", []string{"path", "entries"}, collapsedRows))
	}
}
