package main

import (
	"bytes"
	"context"
	"io"
	"slices"

	"github.com/develdeco/jig/internal/events"
	"github.com/develdeco/jig/internal/store"
	"github.com/develdeco/jig/internal/tui"
)

// The usage lines of `jig tui`'s flags: the help row and the FlagSet carry the
// same text, which the help parity test compares.
const (
	tuiStoreUsage   = "explicit store path"
	tuiProjectUsage = "project name, resolved via the machine mapping"
)

// init registers this file's verb with the registry (registry.go).
func init() {
	registerVerb(verb{
		Spec: cmdSpec{"tui", "browse a store's tickets and charts in the terminal and drive them: answer, decide asks, gate, publish", []flagSpec{
			{"store", tuiStoreUsage, false},
			{"project", tuiProjectUsage, false},
		}, false},
		Order: 360,
		Run: func(e env, args []string, stdout io.Writer, stdin io.Reader) int {
			return cmdTUI(e, args, stdout, stdin)
		},
	})
}

// cmdTUI implements `jig tui [T|C] [--store P] [--project N]`: eight screens
// (the project, a ticket, its brief, build, review and publish, a slice, a
// chart) over the event stream `jig events` prints, acting only by running
// verbs (internal/tui). With a ticket the screens open on it, with a chart (a
// word that is no ticket of the store) on the chart's screen. It reads and
// writes nothing itself: what a key does is a command, run in process as a
// person at a terminal would run it.
//
// A ticket the store has no folder for yet is waited for when the store could
// mint it (a well-formed id under a declared key): the ticket screen says so
// and fills in once the ticket exists. A malformed id, or one under a key the
// project does not declare, is refused as before.
//
// On a terminal the screens take over the screen. Where stdout is not one,
// each screen is printed whole as it changes, and keys are read from stdin
// as they arrive, so a script can drive it.
func cmdTUI(e env, args []string, stdout io.Writer, stdin io.Reader) int {
	fs := newFlagSet(e, "tui")
	storeFlag := fs.String("store", "", tuiStoreUsage)
	projectFlag := fs.String("project", "", tuiProjectUsage)
	positionals, handled, err := parseWithPositionals(stdout, fs, args)
	if handled {
		return 0
	} else if err != nil {
		return renderErr(stdout, err)
	}
	ticket, err := atMostOne(positionals, "jig tui", "ticket or chart")
	if err != nil {
		return renderErr(stdout, err)
	}
	if *storeFlag, err = e.abs(*storeFlag); err != nil {
		return renderErr(stdout, err)
	}

	// The checkpoint output has nowhere to go: the screens own the terminal.
	st, cfg, _, jigHome, err := resolveStoreForProject(e, *projectFlag, *storeFlag, io.Discard)
	if err != nil {
		return renderErr(stdout, err)
	}
	chart := ""
	if chartNamed(st, ticket) {
		chart, ticket = ticket, ""
	}
	awaiting := false
	if ticket != "" {
		id, err := st.ResolveTicket(ticket)
		if err == nil {
			if err = requireTicket(st, id); err != nil {
				if key, ok := mintableKey(id); ok {
					if _, declared := cfg.Keys[key]; declared {
						err, awaiting = nil, true
					} else {
						_, err = cfg.ResolveKey(key)
					}
				}
			}
		}
		if err != nil {
			return renderErr(stdout, err)
		}
		ticket = id
	}

	scope := events.Scope{
		Ledger:   jigHome,
		Journals: func() ([]events.Journal, error) { return storeJournals(st, cfg.Name) },
	}
	err = tui.Run(e.ctx, tui.Config{
		Source:   eventsSource{scope},
		Run:      tuiRunner(e),
		Ticket:   ticket,
		Chart:    chart,
		Awaiting: awaiting,
		Scope:    []string{"--store", st.Root},
		Project:  cfg.Name,
	}, stdin, stdout, e.stdoutIsTerminal(stdout))
	if err != nil {
		return renderErr(stdout, err)
	}
	return 0
}

// chartNamed is whether word names a chart of the store and no ticket: a ticket
// by that id or alias wins, as it does for `jig next`.
func chartNamed(st *store.Store, word string) bool {
	if word == "" {
		return false
	}
	if id, err := st.ResolveTicket(word); err != nil || st.TicketExists(id) {
		return false
	}
	names, err := st.ChartNames()
	return err == nil && slices.Contains(names, word)
}

// mintableKey is the key of id when id is shaped like an id the store mints (a
// key, a dash and a number) and safe to name a folder. Whether the project
// declares the key decides whether a ticket by that id may be created at any
// moment, by the session that plans it.
func mintableKey(id string) (string, bool) {
	if checkTicketID(id) != nil {
		return "", false
	}
	key, _, ok := store.ParseTicketID(id)
	return key, ok
}

// eventsSource is the screens' event stream: internal/events following a scope.
type eventsSource struct{ scope events.Scope }

func (s eventsSource) Follow(ctx context.Context, emit func(events.Envelope) error) error {
	return events.Follow(ctx, s.scope, events.FollowOptions{}, emit)
}

// personAtTheKeys is the stdin of a command the screens run. The person who
// pressed the key is at a terminal, whatever the screens' own stdin is, so
// the commands record what they did as the person's: an approval, a triage.
type personAtTheKeys struct{}

func (personAtTheKeys) Read([]byte) (int, error) { return 0, io.EOF }

// tuiRunner runs a verb in process for the screens, with the person at the
// keys as its terminal, and returns what it printed and its exit code. What
// the verb would warn on stderr is part of that output: the screens own the
// terminal, and a line written to it under them shifts every screen after.
func tuiRunner(e env) tui.Runner {
	pe := e
	pe.stdinIsTerminal = func(r io.Reader) bool {
		_, ok := r.(personAtTheKeys)
		return ok
	}
	return func(args []string) (string, int) {
		var out bytes.Buffer
		ce := pe
		ce.stderr = &out
		code := run(ce, args, &out, personAtTheKeys{})
		return out.String(), code
	}
}
