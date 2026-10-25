package main

import (
	"fmt"
	"io"
	"strings"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/store"
)

// closeReasonUsage is the --reason flag usage, which the help table repeats.
const closeReasonUsage = "why the ticket is closed: covered (another change did the work), superseded (another ticket replaces it) or not-planned (required)"

// closeByUsage is the --by flag usage, which the help table repeats.
const closeByUsage = "what stands for the ticket: a reference such as a pull request or a commit for covered, the replacing ticket for superseded"

// init registers this file with the registry (registry.go).
func init() {
	registerVerb(verb{
		Spec: cmdSpec{"close", "close a ticket without a pull request: covered, superseded or not planned", []flagSpec{
			{"reason", closeReasonUsage, false},
			{"by", closeByUsage, false},
			{"note", "free text saying more", false},
			{"store", "explicit store path", false},
			{"project", "project name, resolved via the machine mapping", false},
		}, false},
		Order: 420,
		Run: func(e env, args []string, stdout io.Writer, _ io.Reader) int {
			return cmdClose(e, args, stdout)
		},
		Probe: []string{"T-1"},
	})
	registerVerb(verb{
		Spec: cmdSpec{"reopen", "reopen a closed ticket", []flagSpec{
			{"store", "explicit store path", false},
			{"project", "project name, resolved via the machine mapping", false},
		}, false},
		Order: 421,
		Run: func(e env, args []string, stdout io.Writer, _ io.Reader) int {
			return cmdReopen(e, args, stdout)
		},
		Probe: []string{"T-1"},
	})
}

// cmdClose implements `jig close <ticket> --reason covered|superseded|not-planned
// [--by <ref>] [--note <text>]`: it writes the closed entry of the record of the
// ticket, commits that record alone and pushes it through Store.Claim, as
// `jig ticket new` lands an id. A closed ticket cannot be built, and its issue
// closes with the next sync of the mirror.
func cmdClose(e env, args []string, stdout io.Writer) int {
	ticket, rest, err := requirePositional(args, "ticket")
	if err != nil {
		return renderErr(stdout, err)
	}
	fs := newFlagSet(e, "close")
	reason := fs.String("reason", "", closeReasonUsage)
	by := fs.String("by", "", closeByUsage)
	note := fs.String("note", "", "free text saying more")
	storeFlag := fs.String("store", "", "explicit store path")
	projectFlag := fs.String("project", "", "project name, resolved via the machine mapping")
	if handled, err := parseFlags(stdout, fs, rest); handled {
		return 0
	} else if err != nil {
		return renderErr(stdout, err)
	}
	if *reason == "" {
		return renderErr(stdout, &axi.Error{
			Msg:  "jig close requires --reason",
			Code: "VALIDATION_ERROR",
			Help: []string{"--reason covered (another change did the work), superseded (another ticket replaces it) or not-planned"},
		})
	}

	st, _, _, _, err := resolveStoreForProject(e, *projectFlag, *storeFlag, stdout)
	if err != nil {
		return renderErr(stdout, err)
	}
	ticket, err = resolveTicketArg(st, ticket, stdout)
	if err != nil {
		return renderErr(stdout, err)
	}
	if err := requireTicket(st, ticket); err != nil {
		return renderErr(stdout, err)
	}
	closure, err := checkClosure(st, ticket, store.NewClosed(strings.TrimSpace(*reason), strings.TrimSpace(*by), strings.TrimSpace(*note), e.now()))
	if err != nil {
		return renderErr(stdout, err)
	}

	if err := st.Sync(); err != nil {
		return renderErr(stdout, err)
	}
	if _, err := st.Claim(
		func() (string, []string, error) {
			if err := st.CloseTicket(ticket, closure); err != nil {
				return "", nil, err
			}
			return ticket, []string{st.TicketRecordRelPath(ticket)}, nil
		},
		func(id string) string { return fmt.Sprintf("%s: closed, %s", id, closure.Phrase()) },
	); err != nil {
		return renderErr(stdout, err)
	}
	// Claim never runs the checkpoint hook itself, so this is the checkpoint:
	// the mirror closes the issue of the ticket now.
	st.RunCheckpointHook()

	pairs := [][2]string{{"ticket", ticket}, {"reason", closure.Reason}}
	if closure.By != "" {
		pairs = append(pairs, [2]string{"by", closure.By})
	}
	if closure.Note != "" {
		pairs = append(pairs, [2]string{"note", closure.Note})
	}
	pairs = append(pairs, [2]string{"at", closure.At})
	axi.Render(stdout,
		axi.KV("closed", pairs),
		axi.Help(
			fmt.Sprintf("%s cannot be built while it is closed, and the tickets that wait for it %s", ticket, releasesSentence(closure.Reason)),
			fmt.Sprintf("Take it back with `jig reopen %s`", ticket),
		),
	)
	return 0
}

// releasesSentence says what a closure does to the tickets that wait for the
// ticket: covered and superseded let them go on, not planned holds them.
func releasesSentence(reason string) string {
	if store.Releases(reason) {
		return "go on"
	}
	return "stay held: they need planning again"
}

// checkClosure is closure checked as a record and against the store: a reason
// that is none of the three, a missing or misplaced --by, and, for superseded,
// a --by that is not another ticket of the store. The closure it returns has
// the current id of the superseding ticket in By.
func checkClosure(st *store.Store, ticket string, closure store.Closed) (store.Closed, error) {
	if problems := closure.Check(); len(problems) > 0 {
		return closure, &axi.Error{
			Msg:  fmt.Sprintf("%s cannot be closed as asked", ticket),
			Code: "VALIDATION_ERROR",
			Help: flagWords(problems),
		}
	}
	if closure.Reason != store.CloseSuperseded {
		return closure, nil
	}
	by, err := st.ResolveTicket(closure.By)
	if err == nil {
		err = requireTicket(st, by)
	}
	switch {
	case err != nil:
		return closure, &axi.Error{
			Msg:  fmt.Sprintf("--by %q is not a ticket of the store", closure.By),
			Code: "VALIDATION_ERROR",
			Help: []string{"A superseded ticket names the ticket that replaces it; `jig next` lists the tickets"},
		}
	case by == ticket:
		return closure, &axi.Error{
			Msg:  fmt.Sprintf("%s cannot be superseded by itself", ticket),
			Code: "VALIDATION_ERROR",
		}
	}
	closure.By = by
	return closure, nil
}

// flagWords rewrites the wording of a problem of the record (closed: needs by:
// ...) as the flags a person typed.
func flagWords(problems []string) []string {
	out := make([]string, len(problems))
	for i, p := range problems {
		p = strings.TrimPrefix(p, "closed: ")
		out[i] = strings.ReplaceAll(strings.ReplaceAll(p, "needs by:", "needs --by:"), "reason ", "--reason ")
	}
	return out
}

// cmdReopen implements `jig reopen <ticket>`: it removes the closed entry of
// the record of the ticket the same way `jig close` wrote it. The issue of the
// ticket reopens with the next sync, with the Status its state derives.
func cmdReopen(e env, args []string, stdout io.Writer) int {
	ticket, rest, err := requirePositional(args, "ticket")
	if err != nil {
		return renderErr(stdout, err)
	}
	fs := newFlagSet(e, "reopen")
	storeFlag := fs.String("store", "", "explicit store path")
	projectFlag := fs.String("project", "", "project name, resolved via the machine mapping")
	if handled, err := parseFlags(stdout, fs, rest); handled {
		return 0
	} else if err != nil {
		return renderErr(stdout, err)
	}

	st, _, _, _, err := resolveStoreForProject(e, *projectFlag, *storeFlag, stdout)
	if err != nil {
		return renderErr(stdout, err)
	}
	ticket, err = resolveTicketArg(st, ticket, stdout)
	if err != nil {
		return renderErr(stdout, err)
	}
	if err := requireTicket(st, ticket); err != nil {
		return renderErr(stdout, err)
	}
	if err := st.Sync(); err != nil {
		return renderErr(stdout, err)
	}
	if _, err := st.Claim(
		func() (string, []string, error) {
			if err := st.ReopenTicket(ticket); err != nil {
				return "", nil, err
			}
			return ticket, []string{st.TicketRecordRelPath(ticket)}, nil
		},
		func(id string) string { return fmt.Sprintf("%s: reopened", id) },
	); err != nil {
		return renderErr(stdout, err)
	}
	st.RunCheckpointHook()

	axi.Render(stdout,
		axi.KV("reopened", [][2]string{{"ticket", ticket}}),
		axi.Help(fmt.Sprintf("Run `jig next %s` to see where it stands", ticket)),
	)
	return 0
}

// requireOpen refuses a closed ticket with TICKET_CLOSED, naming the command
// that reopens it. The commands that build a ticket (claim, gate, publish,
// requeue and review context) ask it before they do anything else.
func requireOpen(st *store.Store, ticket string) error {
	rec, err := st.ReadTicket(ticket)
	if err != nil {
		return err
	}
	if rec.Closed == nil {
		return nil
	}
	return ticketClosedError(ticket, *rec.Closed)
}

// ticketClosedError is the refusal of a command that would work a closed
// ticket.
func ticketClosedError(ticket string, c store.Closed) error {
	return &axi.Error{
		Msg:  fmt.Sprintf("%s is closed (%s)", ticket, c.Phrase()),
		Code: "TICKET_CLOSED",
		Help: []string{fmt.Sprintf("Run `jig reopen %s` to work it again", ticket)},
	}
}
