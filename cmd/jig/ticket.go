package main

import (
	"fmt"
	"io"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/tracker"
)

// cmdTicket implements `jig ticket new --title <t> [--body <b>]`.
func cmdTicket(args []string, stdout io.Writer) int {
	sub, rest, err := requirePositional(args, "subcommand (new)")
	if err != nil {
		return renderErr(stdout, err)
	}
	if sub != "new" {
		return renderErr(stdout, &axi.Error{
			Msg:  fmt.Sprintf("unknown ticket subcommand %q; only \"new\" is supported", sub),
			Code: "VALIDATION_ERROR",
		})
	}

	fs := newFlagSet("ticket new")
	title := fs.String("title", "", "ticket title (required)")
	body := fs.String("body", "", "ticket body")
	storeFlag := fs.String("store", "", "explicit store path")
	projectFlag := fs.String("project", "", "project name, resolved via the machine mapping")
	if err := fs.Parse(rest); err != nil {
		return renderErr(stdout, &axi.Error{Msg: err.Error(), Code: "VALIDATION_ERROR"})
	}
	if *title == "" {
		return renderErr(stdout, &axi.Error{Msg: "jig ticket new requires --title", Code: "VALIDATION_ERROR"})
	}

	st, cfg, _, err := resolveStoreForProject(*projectFlag, *storeFlag)
	if err != nil {
		return renderErr(stdout, err)
	}

	adapter, err := tracker.New(cfg, st)
	if err != nil {
		return renderErr(stdout, err)
	}
	id, err := adapter.Mint(tracker.Draft{Title: *title, Body: *body})
	if err != nil {
		return renderErr(stdout, err)
	}

	axi.Render(stdout,
		axi.KV("ticket", [][2]string{{"id", id}, {"title", *title}}),
		axi.Help(fmt.Sprintf("Run `jig validate %s` once its brief and slices are written", id)),
	)
	return 0
}
