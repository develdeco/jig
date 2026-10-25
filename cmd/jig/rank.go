package main

import (
	"fmt"
	"io"
	"strconv"

	"github.com/develdeco/jig/internal/axi"
)

// The usage lines of `jig rank`'s flags: the help row and the FlagSet carry
// the same text, which the help parity test compares.
const (
	rankClearUsage   = "remove the ticket's rank instead of setting one"
	rankStoreUsage   = "explicit store path"
	rankProjectUsage = "project name, resolved via the machine mapping"
)

// init registers this file's verb with the registry (registry.go).
func init() {
	registerVerb(verb{
		Spec: cmdSpec{"rank", "put a ticket in the owner's priority order: jig rank <ticket> <n> (lowest first), or --clear", []flagSpec{
			{"clear", rankClearUsage, false},
			{"store", rankStoreUsage, false},
			{"project", rankProjectUsage, false},
		}, false},
		Order: 111,
		Run: func(e env, args []string, stdout io.Writer, _ io.Reader) int {
			return cmdRank(e, args, stdout)
		},
		Probe: []string{"T-1", "1"},
	})
}

// cmdRank implements `jig rank <ticket> <n>` and `jig rank <ticket> --clear`:
// it writes the ticket's rank into its ticket.yaml and commits that record
// alone through Store.Claim, as `jig ticket new` commits a new one, so the
// order reaches every clone of the store. `jig next` lists the ready tickets
// by rank, lowest first, and the unranked last. A rank is a whole number from
// 1; tickets may share one, and `jig next` says so.
func cmdRank(e env, args []string, stdout io.Writer) int {
	fs := newFlagSet(e, "rank")
	clearFlag := fs.Bool("clear", false, rankClearUsage)
	storeFlag := fs.String("store", "", rankStoreUsage)
	projectFlag := fs.String("project", "", rankProjectUsage)
	positionals, handled, err := parseWithPositionals(stdout, fs, args)
	if handled {
		return 0
	} else if err != nil {
		return renderErr(stdout, err)
	}
	ticket, rank, err := parseRankArgs(positionals, *clearFlag)
	if err != nil {
		return renderErr(stdout, err)
	}

	st, _, _, _, err := resolveStoreForProject(e, *projectFlag, *storeFlag, stdout)
	if err != nil {
		return renderErr(stdout, err)
	}
	id, err := resolveTicketArg(st, ticket, stdout)
	if err == nil {
		err = requireTicket(st, id)
	}
	if err != nil {
		return renderErr(stdout, err)
	}
	if err := st.Sync(); err != nil {
		return renderErr(stdout, err)
	}

	rec, err := st.ReadTicket(id)
	if err != nil {
		return renderErr(stdout, err)
	}
	if sameRank(rec.Rank, rank) {
		axi.Render(stdout, rankBlock(id, rank, "unchanged"), axi.Help(rankHints()...))
		return 0
	}
	msg := id + ": clear rank"
	if rank != nil {
		msg = fmt.Sprintf("%s: rank %d", id, *rank)
	}
	if _, err := st.Claim(
		func() (string, []string, error) {
			if err := st.SetTicketRank(id, rank); err != nil {
				return "", nil, err
			}
			return id, []string{st.TicketFileRelPath(id)}, nil
		},
		func(string) string { return msg },
	); err != nil {
		return renderErr(stdout, err)
	}
	// Claim never runs the checkpoint hook itself, so this is the checkpoint
	// of the rank just landed, as it is for a new ticket.
	st.RunCheckpointHook()

	axi.Render(stdout, rankBlock(id, rank, "recorded"), axi.Help(rankHints()...))
	return 0
}

// parseRankArgs reads the ticket and, unless cleared is set, the rank out of the
// positionals of `jig rank`; the rank is nil for --clear.
func parseRankArgs(positionals []string, cleared bool) (ticket string, rank *int, err error) {
	usage := func(msg string) error {
		return &axi.Error{Msg: msg, Code: "VALIDATION_ERROR", Help: []string{"Usage: jig rank <ticket> <n>, or jig rank <ticket> --clear"}}
	}
	switch {
	case len(positionals) == 0:
		return "", nil, usage("missing required ticket")
	case cleared && len(positionals) > 1:
		return "", nil, usage("--clear takes the ticket alone, not a rank")
	case cleared:
		return positionals[0], nil, nil
	case len(positionals) == 1:
		return "", nil, usage("missing required rank (or --clear to remove one)")
	case len(positionals) > 2:
		return "", nil, usage(fmt.Sprintf("jig rank takes a ticket and a rank, got %d arguments", len(positionals)))
	}
	n, convErr := strconv.Atoi(positionals[1])
	if convErr != nil || n < 1 {
		return "", nil, usage(fmt.Sprintf("rank %q is not a whole number from 1", positionals[1]))
	}
	return positionals[0], &n, nil
}

// sameRank reports whether two ranks are the same: both absent, or equal.
func sameRank(a, b *int) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

// rankBlock says which rank the ticket holds now ("none" once cleared) and
// whether this run changed it.
func rankBlock(ticket string, rank *int, result string) string {
	held := "none"
	if rank != nil {
		held = strconv.Itoa(*rank)
	}
	return axi.KV("rank", [][2]string{{"ticket", ticket}, {"rank", held}, {"result", result}})
}

func rankHints() []string {
	return []string{"Run `jig next` to see the ready tickets in priority order"}
}
