package main

import (
	"fmt"
	"io"
	"os"

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
	if err := fs.Parse(args); err != nil {
		return renderErr(stdout, &axi.Error{Msg: err.Error(), Code: "VALIDATION_ERROR"})
	}

	if *standalone {
		cwd, err := os.Getwd()
		if err != nil {
			return renderErr(stdout, err)
		}
		storeDir, err := project.InitStandalone(cwd)
		if err != nil {
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
