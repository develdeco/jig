package main

import (
	"errors"
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

	st, cfg, _, _, err := resolveStoreForProject(*projectFlag, *storeFlag)
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
	if err := tracker.CheckMinted(adapter, id); err != nil {
		return renderErr(stdout, err)
	}

	// Record the title on every tracker, not only the local one (whose Mint
	// already creates the ticket's store folder itself): creating the record
	// also creates the ticket's store folder, which requireTicket needs and
	// a github or command tracker's Mint does not make. The record is
	// created, never merged into: an id that already has a ticket.yaml means
	// some earlier, unrelated write claimed it in the store, and no Mint
	// (local, github, command) makes that file itself. The check is on the
	// record, not on the folder: a folder with no ticket.yaml has no claim to
	// protect (the intake skill writes one for a ticket that exists only in
	// its tracker, and a ticket minted before jig recorded titles has one
	// too), so the record is created in it.
	//
	// The tracker-side ticket exists by now (the same unavoidable ordering as
	// the reserved-id refusal above), so a failure here says so and tells the
	// operator not to mint again for it.
	if err := st.CreateTicketRecord(id, store.Ticket{Title: *title}); err != nil {
		doNotMint := "do not run `jig ticket new` again for it"
		if errors.Is(err, store.ErrTicketRecordExists) {
			return renderErr(stdout, mintedIDCollision(adapter.Name(), id, st.TicketFilePath(id), doNotMint))
		}
		return renderErr(stdout, &axi.Error{
			Msg:  fmt.Sprintf("the %s tracker minted %s, but recording its title in the store failed: %v", adapter.Name(), id, err),
			Code: "VALIDATION_ERROR",
			Help: []string{
				fmt.Sprintf("%s now exists in the %s tracker; %s", id, adapter.Name(), doNotMint),
				fmt.Sprintf("Resolve the store error above, then write its title into %s by hand", st.TicketFilePath(id)),
			},
		})
	}

	axi.Render(stdout,
		axi.KV("ticket", [][2]string{{"id", id}, {"title", *title}}),
		axi.Help(getWorkHints(id)...),
	)
	return 0
}

// mintedIDCollision is the refusal for a minted id whose store folder already
// holds a ticket.yaml: some earlier, unrelated write claimed the id, so the
// record is that ticket's own, and it is neither merged into nor deleted. The
// tracker-side ticket exists by then (a github or command tracker's id is
// known only once it has created the ticket), so the help says so first;
// again is what the operator must not do about it, which differs by command
// (mint again with jig ticket new, re-run jig graduate). Both commands that
// mint into the store refuse this collision with this one message and one
// recovery story.
func mintedIDCollision(trackerName, id, recordPath, again string) *axi.Error {
	return &axi.Error{
		Msg:  fmt.Sprintf("the %s tracker minted %s, but the store already has a ticket.yaml for %s", trackerName, id, id),
		Code: "VALIDATION_ERROR",
		Help: []string{
			fmt.Sprintf("%s now exists in the %s tracker; %s", id, trackerName, again),
			fmt.Sprintf("Inspect %s to see which ticket already claims this id: it is that ticket's record, so do not delete it - resolve the collision by hand", recordPath),
		},
	}
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
// A ticket that adopted a branch has none until a gate round queues its
// fixes, and the refusal says so instead of pointing at intake.
func requireSlices(st *store.Store, ticket string) error {
	return requireWorkable(st, ticket, false)
}

// requireWork is requireSlices for the commands that also work a ticket which
// adopted a branch, whose gate round queues the slices the rest of the loop
// builds: gate, run, solve and publish. A ticket with slices, or with an
// adopted branch, passes; one with neither is told both ways to get work.
func requireWork(st *store.Store, ticket string) error {
	return requireWorkable(st, ticket, true)
}

// requireWorkable is the check behind requireSlices and requireWork:
// adoptedOK says whether a ticket with no slices but an adopted branch
// passes.
func requireWorkable(st *store.Store, ticket string, adoptedOK bool) error {
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
	rec, err := st.ReadTicket(ticket)
	if err != nil {
		return err
	}
	if rec.Adopted() && adoptedOK {
		return nil
	}
	refusal := &axi.Error{
		Msg:  fmt.Sprintf("ticket %s has no slices yet", ticket),
		Code: "VALIDATION_ERROR",
	}
	switch {
	case rec.Adopted():
		refusal.Help = []string{fmt.Sprintf("It adopted branch %s: `jig gate %s` reviews it and queues what it finds as slices", rec.Branch, ticket)}
	case adoptedOK:
		refusal.Help = getWorkHints(ticket)
	default:
		refusal.Help = []string{intakeHint(ticket)}
	}
	return refusal
}

// adoptHint is the other way for a ticket with no slices to get work: gate a
// branch built outside jig, which adopts it.
func adoptHint(ticket string) string {
	return fmt.Sprintf("Or review a branch built outside jig, which the ticket then adopts: `jig gate %s --branch <name>`", ticket)
}

// getWorkHints is both ways for a ticket with no slices and no adopted branch
// to get work, in the order `jig ticket new`, `jig status` and the refusal of a
// command that can work an adopted ticket (requireWork) all say them. A command
// with nothing to adopt (requireSlices) names intake alone.
func getWorkHints(ticket string) []string {
	return []string{intakeHint(ticket), adoptHint(ticket)}
}
