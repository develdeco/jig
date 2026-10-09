package main

import (
	"fmt"
	"io"
	"os"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/pool"
	"github.com/develdeco/jig/internal/store"
)

// resolveTicketArg resolves ticket - as typed on the command line, an id or
// one of a ticket's aliases - to its current id through st.ResolveTicket,
// printing one line naming it when it differs from what was typed: every
// command that takes a ticket id (run, gate, publish, solve, requeue, status
// and validate) calls this before anything else touches the ticket, so a
// command reached through an alias always works on the ticket's folder,
// leases and branch under its current id.
func resolveTicketArg(st *store.Store, ticket string, stdout io.Writer) (string, error) {
	resolved, err := st.ResolveTicket(ticket)
	if err != nil {
		return "", err
	}
	if resolved != ticket {
		fmt.Fprintf(stdout, "%s is now %s\n", ticket, resolved)
	}
	return resolved, nil
}

// cmdTicket implements `jig ticket new --title <t>`.
func cmdTicket(e env, args []string, stdout io.Writer) int {
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

	fs := newFlagSet(e, "ticket new")
	title := fs.String("title", "", "ticket title (required)")
	body := fs.String("body", "", "ticket body/description")
	keyFlag := fs.String("key", "", "key to mint under (required when project.yaml declares more than one)")
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

	st, cfg, _, _, err := resolveStoreForProject(e, *projectFlag, *storeFlag, stdout)
	if err != nil {
		return renderErr(stdout, err)
	}

	// An undeclared --key is refused before anything is minted.
	key, err := cfg.ResolveKey(*keyFlag)
	if err != nil {
		return renderErr(stdout, err)
	}

	if err := st.Sync(); err != nil {
		return renderErr(stdout, err)
	}

	// jig mints every id itself, through the store package's own per-key
	// counter, whatever project.yaml says about trackers: Mint computes the
	// next id, refuses one jig cannot use before writing anything, and
	// creates the ticket's folder and ticket.yaml together. Claim then
	// commits that folder alone and, on a store with an origin, pushes it
	// alone, re-minting after a rejected push so two clones minting at once
	// never collide on the same id (internal/store/claim.go).
	id, err := st.Claim(
		func() (string, []string, error) {
			mintedID, err := st.Mint(key, store.Ticket{Title: *title, Body: *body})
			if err != nil {
				return "", nil, err
			}
			return mintedID, []string{mintedID}, nil
		},
		func(id string) string { return fmt.Sprintf("%s: new ticket", id) },
	)
	if err != nil {
		return renderErr(stdout, err)
	}

	// Claim never runs the checkpoint hook itself (internal/mirror's own
	// claims reach the store through Claim too, and hooking Claim would
	// re-enter the mirror), so this is the checkpoint: a newly minted
	// ticket's own sync, run explicitly once its claim has landed.
	st.RunCheckpointHook()

	axi.Render(stdout,
		axi.KV("ticket", [][2]string{{"id", id}, {"title", *title}}),
		axi.Help(getWorkHints(id)...),
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
		Help: []string{"Run `jig ticket new --title \"...\"` to mint a ticket"},
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
