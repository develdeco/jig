package main

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"time"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/events"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/project"
	"github.com/develdeco/jig/internal/store"
)

// The usage lines of `jig events`'s flags: the help row and the FlagSet carry
// the same text, which the help parity test compares.
const (
	eventsFollowUsage  = "keep running and print each event as it is written, until interrupted"
	eventsSinceUsage   = "only events at or after this time: an RFC 3339 timestamp, a duration back from now (30m), or now"
	eventsAllUsage     = "every project's store on this machine and the whole machine ledger, not one store"
	eventsStoreUsage   = "explicit store path"
	eventsProjectUsage = "project name, resolved via the machine mapping"
)

// init registers this file's verb with the registry (registry.go).
func init() {
	registerVerb(verb{
		Spec: cmdSpec{"events", "stream the ticket journals and the machine ledger as one time-ordered ndjson", []flagSpec{
			{"follow", eventsFollowUsage, false},
			{"since", eventsSinceUsage, false},
			{"all", eventsAllUsage, false},
			{"store", eventsStoreUsage, false},
			{"project", eventsProjectUsage, false},
		}, false},
		Order: 350,
		Run: func(e env, args []string, stdout io.Writer, _ io.Reader) int {
			return cmdEvents(e, args, stdout)
		},
	})
}

// cmdEvents implements `jig events [T] [--follow] [--since <t>] [--all]`: the
// journals of the store's tickets (T's alone with a ticket) and the machine
// ledger lines about them, as one stream of JSON lines, oldest first, each
// tagged with its source (internal/events). With --follow it goes on printing
// what is written until it is interrupted. It reads the store and the ledger
// and writes nothing, and it never syncs: it shows what is on this machine.
//
// stdout carries the stream and nothing else, so the verb prints no help
// block, and the store is resolved with its checkpoint output thrown away
// (nothing here checkpoints).
func cmdEvents(e env, args []string, stdout io.Writer) int {
	fs := newFlagSet(e, "events")
	follow := fs.Bool("follow", false, eventsFollowUsage)
	sinceFlag := fs.String("since", "", eventsSinceUsage)
	all := fs.Bool("all", false, eventsAllUsage)
	storeFlag := fs.String("store", "", eventsStoreUsage)
	projectFlag := fs.String("project", "", eventsProjectUsage)
	positionals, handled, err := parseWithPositionals(stdout, fs, args)
	if handled {
		return 0
	} else if err != nil {
		return renderErr(stdout, err)
	}
	ticket, err := atMostOne(positionals, "jig events", "ticket")
	if err != nil {
		return renderErr(stdout, err)
	}
	if *all && (ticket != "" || *storeFlag != "" || *projectFlag != "") {
		return renderErr(stdout, &axi.Error{
			Msg:  "--all reads every project on this machine: it takes no ticket, --store or --project",
			Code: "VALIDATION_ERROR",
			Help: []string{"Drop --all to read one store, or drop the ticket and the store to read them all"},
		})
	}
	since, err := parseEventsSince(*sinceFlag, e.now())
	if err != nil {
		return renderErr(stdout, err)
	}
	*storeFlag, err = e.abs(*storeFlag)
	if err != nil {
		return renderErr(stdout, err)
	}

	scope, err := eventsScope(e, *all, ticket, *projectFlag, *storeFlag)
	if err != nil {
		return renderErr(stdout, err)
	}

	enc := json.NewEncoder(stdout)
	enc.SetEscapeHTML(false)
	emit := func(env events.Envelope) error {
		if err := enc.Encode(env); err != nil {
			return stdoutError{err}
		}
		return nil
	}

	if *follow {
		err = events.Follow(e.ctx, scope, events.FollowOptions{Since: since}, emit)
	} else {
		var envs []events.Envelope
		if envs, err = events.Read(scope, since); err == nil {
			for _, env := range envs {
				if err = emit(env); err != nil {
					break
				}
			}
		}
	}
	switch err.(type) {
	case nil:
		return 0
	case stdoutError:
		// The reader went away (a closed pipe); there is nobody to tell.
		return 1
	}
	return renderErr(stdout, err)
}

// stdoutError marks a failure to write to stdout, as against one of reading.
type stdoutError struct{ error }

// parseEventsSince reads --since: empty is no bound, now is the moment of the
// call, an RFC 3339 timestamp is itself, and a duration is that long before
// now.
func parseEventsSince(s string, now time.Time) (time.Time, error) {
	switch s {
	case "":
		return time.Time{}, nil
	case "now":
		return now, nil
	}
	if at, err := time.Parse(time.RFC3339Nano, s); err == nil {
		return at, nil
	}
	if d, err := time.ParseDuration(s); err == nil && d >= 0 {
		return now.Add(-d), nil
	}
	return time.Time{}, &axi.Error{
		Msg:  fmt.Sprintf("--since %q is not a time", s),
		Code: "VALIDATION_ERROR",
		Help: []string{"Give an RFC 3339 timestamp (2026-10-23T09:00:00Z), a duration back from now (30m, 2h), or now"},
	}
}

// eventsScope is what the stream covers. With --all, every store the machine
// map names that is there, and the whole ledger. Otherwise the store the
// working directory (or --store, --project) resolves to: one ticket's journal
// when a ticket is given, else every ticket's, listed again at each poll so a
// ticket made while following is followed; the ledger lines about them.
func eventsScope(e env, all bool, ticket, projectFlag, storeFlag string) (events.Scope, error) {
	if all {
		jigHome, err := e.jigHome()
		if err != nil {
			return events.Scope{}, err
		}
		return events.Scope{
			Journals:  func() ([]events.Journal, error) { return machineJournals(jigHome) },
			Ledger:    jigHome,
			AllLedger: true,
		}, nil
	}

	// The checkpoint output has nowhere to go but the stream, which it would
	// corrupt; nothing here reaches a checkpoint.
	st, cfg, _, jigHome, err := resolveStoreForProject(e, projectFlag, storeFlag, io.Discard)
	if err != nil {
		return events.Scope{}, err
	}
	scope := events.Scope{Ledger: jigHome}
	if ticket != "" {
		id, err := st.ResolveTicket(ticket)
		if err == nil {
			err = requireTicket(st, id)
		}
		if err != nil {
			return events.Scope{}, err
		}
		scope.Journals = func() ([]events.Journal, error) {
			return []events.Journal{journalOf(st, cfg.Name, id)}, nil
		}
		return scope, nil
	}
	scope.Journals = func() ([]events.Journal, error) { return storeJournals(st, cfg.Name) }
	return scope, nil
}

// journalOf names the journal of ticket in st, which belongs to the project called name.
func journalOf(st *store.Store, name, ticket string) events.Journal {
	return events.Journal{Store: name, Ticket: ticket, Path: journal.Path(st, ticket)}
}

// storeJournals names the journal of every ticket in st.
func storeJournals(st *store.Store, name string) ([]events.Journal, error) {
	ids, err := st.Tickets()
	if err != nil {
		return nil, err
	}
	out := make([]events.Journal, len(ids))
	for i, id := range ids {
		out[i] = journalOf(st, name, id)
	}
	return out, nil
}

// machineJournals names the journals of every store the machine map under
// jigHome points at, projects in name order. A project whose store is not
// there (a stale mapping) has none.
func machineJournals(jigHome string) ([]events.Journal, error) {
	machine, err := project.LoadMachine(jigHome)
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(machine))
	for name := range machine {
		names = append(names, name)
	}
	sort.Strings(names)
	var out []events.Journal
	for _, name := range names {
		if machine[name].Store == "" {
			continue
		}
		st, err := store.Open(machine[name].Store)
		if err != nil {
			continue
		}
		js, err := storeJournals(st, name)
		if err != nil {
			return nil, err
		}
		out = append(out, js...)
	}
	return out, nil
}
