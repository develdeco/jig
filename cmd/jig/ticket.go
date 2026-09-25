package main

import (
	"fmt"
	"io"
	"os"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/pool"
	"github.com/develdeco/jig/internal/store"
	"github.com/develdeco/jig/internal/tracker"
)

// cmdTicket implements `jig ticket new --title <t> [--body <b>]`.
func cmdTicket(args []string, stdout io.Writer) int {
	sub, rest, err := requirePositional(args, "subcommand (new)")
	if err != nil {
		return renderErr(stdout, err)
	}
	if sub == "" {
		// requirePositional passed a leading -h/--help through.
		sub = "new"
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
	if handled, err := parseFlags(stdout, fs, rest); handled {
		return 0
	} else if err != nil {
		return renderErr(stdout, err)
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
	// The local tracker refuses an unusable id before writing anything; any
	// other tracker's id is known only once it has created the ticket, so it
	// is refused here, before anyone writes a brief under it.
	if err := pool.CheckTicket(id); err != nil {
		return renderErr(stdout, &axi.Error{
			Msg:  fmt.Sprintf("the %s tracker minted %s, which jig cannot use: %v", adapter.Name(), id, err),
			Code: "VALIDATION_ERROR",
			Help: []string{
				fmt.Sprintf("%s now exists in the %s tracker; close it there", id, adapter.Name()),
				"Change the tracker's id scheme so new ids are usable, then mint again",
			},
		})
	}

	axi.Render(stdout,
		axi.KV("ticket", [][2]string{{"id", id}, {"title", *title}}),
		axi.Help(fmt.Sprintf("Run `jig validate %s` once its brief and slices are written", id)),
	)
	return 0
}

// intakeHint is the next step for a ticket that has no slices yet.
func intakeHint(ticket string) string {
	return fmt.Sprintf("Write its brief.md and slices.yaml (the intake skill drafts both), then run `jig validate %s`", ticket)
}

// checkTicketID fails when ticket cannot name a ticket's pool leases (see
// pool.CheckTicket): every command that works a ticket refuses such an id
// before it reaches the store or the pool.
func checkTicketID(ticket string) error {
	if err := pool.CheckTicket(ticket); err != nil {
		return &axi.Error{Msg: err.Error(), Code: "VALIDATION_ERROR"}
	}
	return nil
}

// requireTicket fails when ticket is not a usable id or the store has no
// folder for it.
func requireTicket(st *store.Store, ticket string) error {
	if err := checkTicketID(ticket); err != nil {
		return err
	}
	if fi, err := os.Stat(st.TicketDir(ticket)); err == nil && fi.IsDir() {
		return nil
	}
	return &axi.Error{
		Msg:  fmt.Sprintf("ticket %s not found in the store at %s", ticket, st.Root),
		Code: "VALIDATION_ERROR",
		Help: []string{
			"Run `jig ticket new --title \"...\"` to mint a ticket",
			"For a ticket that exists only in the tracker, write its brief.md and slices.yaml (the intake skill drafts both)",
		},
	}
}

// requireSlices fails when ticket is not a usable id or has no slices to
// work, which is also the case for a ticket with no folder in the store yet.
func requireSlices(st *store.Store, ticket string) error {
	if err := checkTicketID(ticket); err != nil {
		return err
	}
	slices, err := st.ReadSlices(ticket)
	if err != nil {
		return err
	}
	if len(slices) > 0 {
		return nil
	}
	return &axi.Error{
		Msg:  fmt.Sprintf("ticket %s has no slices yet", ticket),
		Code: "VALIDATION_ERROR",
		Help: []string{intakeHint(ticket)},
	}
}
