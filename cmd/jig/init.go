package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/project"
)

// cmdInit implements `jig init [--standalone] [--store <path>] [--clone
// <name>=<path>]...`.
func cmdInit(args []string, stdout io.Writer) int {
	fs := newFlagSet("init")
	standalone := fs.Bool("standalone", false, "create a sibling tickets store next to the current repo")
	storeFlag := fs.String("store", "", "store path to initialize (used with --clone)")
	var clones cloneFlag
	fs.Var(&clones, "clone", "name=path clone mapping; repeatable")
	if handled, err := parseFlags(stdout, fs, args); handled {
		return 0
	} else if err != nil {
		return renderErr(stdout, err)
	}

	if *standalone {
		cwd, err := os.Getwd()
		if err != nil {
			return renderErr(stdout, err)
		}
		existing, err := project.StandaloneStoreDir(cwd)
		if err != nil {
			return renderErr(stdout, err)
		}
		if _, err := os.Stat(filepath.Join(existing, "project.yaml")); err == nil {
			return renderErr(stdout, &axi.Error{
				Msg:  fmt.Sprintf("store already initialized at %s; init would reset its project.yaml and ledger.md", existing),
				Code: "VALIDATION_ERROR",
				Help: []string{"Run `jig ticket new --title \"...\"` to use the existing store"},
			})
		}
		storeDir, err := project.InitStandalone(cwd)
		if err != nil {
			return renderErr(stdout, err)
		}
		// Record the machine mapping (store path, and this repo's clone
		// mapped to cwd) the same way the --store/--clone form does, so a
		// later command in this repo (validate, run, gate, ...) resolves
		// its manifest without a separate `jig init --store --clone` step.
		if _, err := project.InitProject(storeDir, map[string]string{filepath.Base(cwd): cwd}); err != nil {
			return renderErr(stdout, err)
		}
		axi.Render(stdout,
			axi.KV("init", [][2]string{{"mode", "standalone"}, {"store", storeDir}}),
			axi.Help("Run `jig ticket new --title \"...\"` to mint the first ticket"),
		)
		return 0
	}

	if *storeFlag == "" {
		return renderErr(stdout, &axi.Error{
			Msg:  "jig init requires --standalone or --store",
			Code: "VALIDATION_ERROR",
		})
	}
	cfg, err := project.InitProject(*storeFlag, clones.m)
	if err != nil {
		return renderErr(stdout, err)
	}
	axi.Render(stdout,
		axi.KV("init", [][2]string{{"mode", "project"}, {"name", cfg.Name}, {"store", *storeFlag}}),
		axi.Help(fmt.Sprintf("Run `jig ticket new --title \"...\"` to mint a ticket in %s", cfg.Name)),
	)
	return 0
}
