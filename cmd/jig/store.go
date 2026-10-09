package main

import (
	"fmt"
	"io"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/migrate"
)

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

// cmdStoreMigrate implements `jig store migrate --map <file> [--dry-run]`
// (brief.md#The rename map, brief.md#The migration): the one command that
// may open a v1 store (resolveStoreForProjectAllowingOldSchema, unlike every
// other command's resolveStore, lets its older schema_version through),
// since rewriting one onto the current schema is its whole job.
func cmdStoreMigrate(e env, args []string, stdout io.Writer) int {
	fs := newFlagSet(e, "store migrate")
	mapFlag := fs.String("map", "", "the rename map file (old id to new key)")
	dryRun := fs.Bool("dry-run", false, "print the rename map and every file the migration would move, delete or rewrite, changing nothing")
	storeFlag := fs.String("store", "", "explicit store path")
	projectFlag := fs.String("project", "", "project name, resolved via the machine mapping")
	if handled, err := parseFlags(stdout, fs, args); handled {
		return 0
	} else if err != nil {
		return renderErr(stdout, err)
	}
	if *mapFlag == "" {
		return renderErr(stdout, &axi.Error{
			Msg:  "--map is required",
			Code: "VALIDATION_ERROR",
		})
	}

	st, cfg, _, jigHome, err := resolveStoreForProjectAllowingOldSchema(e, *projectFlag, *storeFlag, stdout)
	if err != nil {
		return renderErr(stdout, err)
	}
	if err := migrate.CheckSchemaVersion(cfg); err != nil {
		return renderErr(stdout, err)
	}

	mapPath, err := e.abs(*mapFlag)
	if err != nil {
		return renderErr(stdout, err)
	}
	m, err := migrate.LoadMap(mapPath)
	if err != nil {
		return renderErr(stdout, err)
	}
	plan, err := migrate.BuildPlan(st, m)
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
	repoNames := make([]string, len(cfg.Repos))
	for i, r := range cfg.Repos {
		repoNames[i] = r.Name()
	}
	oldIDs := make([]string, len(plan.Renames))
	for i, r := range plan.Renames {
		oldIDs[i] = r.OldID
	}
	if err := migrate.CheckNoLeases(jigHome, repoNames, oldIDs); err != nil {
		return renderErr(stdout, err)
	}

	for _, w := range migrate.BranchWarnings(st.Root, cfg.Repos, plan.Renames) {
		axi.Render(stdout, axi.KV("warning", [][2]string{{"message", w}}))
	}

	renderMigratePlan(stdout, plan)
	if err := migrate.Apply(st, plan); err != nil {
		return renderErr(stdout, err)
	}

	axi.Render(stdout, axi.Help(
		"Push the branch and review it before merging",
		"After the merge, the first sync updates each mirrored issue in place from its moved record",
	))
	return 0
}

// renderMigratePlan prints plan's rename map and file operations, the same
// table shapes for both the dry run and the apply.
func renderMigratePlan(stdout io.Writer, plan migrate.Plan) {
	renameRows := make([][]string, len(plan.Renames))
	for i, r := range plan.Renames {
		renameRows[i] = []string{r.OldID, r.NewID, r.Title}
	}
	axi.Render(stdout, axi.Table("rename", []string{"old", "new", "title"}, renameRows))

	fileRows := make([][]string, len(plan.Files))
	for i, f := range plan.Files {
		fileRows[i] = []string{f.Action, f.Path}
	}
	axi.Render(stdout, axi.Table("files", []string{"action", "path"}, fileRows))
}
