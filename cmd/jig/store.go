package main

import (
	"fmt"
	"io"

	"github.com/develdeco/jig/internal/axi"
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

// cmdStoreMigrate implements `jig store migrate --map <file> [--dry-run]`:
// the one command that may open a v1 store (resolveStoreForMigrate, unlike
// every other command's resolveStore, lets its older schema_version through),
// since rewriting one onto the current schema is its whole job
// (brief.md#The migration). The rewrite itself is not built yet; this stub
// carries the flags and the schema exemption so a v1 store is never refused
// before reaching a command that could act on it, and names the real work
// left to do.
func cmdStoreMigrate(e env, args []string, stdout io.Writer) int {
	fs := newFlagSet(e, "store migrate")
	_ = fs.String("map", "", "the rename map file (old id to new key)")
	_ = fs.Bool("dry-run", false, "print the rename map and every file the migration would move, delete or rewrite, changing nothing")
	storeFlag := fs.String("store", "", "explicit store path")
	projectFlag := fs.String("project", "", "project name, resolved via the machine mapping")
	if handled, err := parseFlags(stdout, fs, args); handled {
		return 0
	} else if err != nil {
		return renderErr(stdout, err)
	}

	if _, _, _, _, err := resolveStoreForProjectAllowingOldSchema(e, *projectFlag, *storeFlag, stdout); err != nil {
		return renderErr(stdout, err)
	}

	return renderErr(stdout, &axi.Error{
		Msg:  "jig store migrate does not move ticket folders or rewrite project.yaml yet",
		Code: "NOT_IMPLEMENTED",
	})
}
