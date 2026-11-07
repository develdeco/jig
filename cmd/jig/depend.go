package main

import (
	"fmt"
	"io"
	"strings"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/store"
)

// The usage lines of `jig depend`'s flags: the help row and the FlagSet carry
// the same text, which the help parity test compares.
const (
	dependOnUsage      = "the ticket T waits for"
	dependKindUsage    = "merged (the default: wait for the parent's merge) or stacked (build on the parent's branch)"
	dependClearUsage   = "remove the edge to --on, or every edge of the ticket when --on is absent"
	dependStoreUsage   = "explicit store path"
	dependProjectUsage = "project name, resolved via the machine mapping"
)

// init registers this file's verb with the registry (registry.go).
func init() {
	registerVerb(verb{
		Spec: cmdSpec{"depend", "make a ticket wait for another: jig depend <ticket> --on <ticket> [--kind merged|stacked], or --clear", []flagSpec{
			{"on", dependOnUsage, false},
			{"kind", dependKindUsage, false},
			{"clear", dependClearUsage, false},
			{"store", dependStoreUsage, false},
			{"project", dependProjectUsage, false},
		}, false},
		Order: 112,
		Run: func(e env, args []string, stdout io.Writer, _ io.Reader) int {
			return cmdDepend(e, args, stdout)
		},
		Probe: []string{"T-1", "--on", "T-2"},
	})
}

// cmdDepend implements `jig depend <T> --on <P> [--kind merged|stacked]` and
// `jig depend <T> [--on <P>] --clear`: it writes T's blocked_by entry for P
// into its ticket.yaml, the shape `jig graduate` writes, and commits that
// record alone through Store.Claim so the edge reaches every clone of the
// store. A stacked edge is advice for a branch that already exists: nothing
// in a branch, the store or a clone is touched.
func cmdDepend(e env, args []string, stdout io.Writer) int {
	fs := newFlagSet(e, "depend")
	onFlag := fs.String("on", "", dependOnUsage)
	kindFlag := fs.String("kind", "", dependKindUsage)
	clearFlag := fs.Bool("clear", false, dependClearUsage)
	storeFlag := fs.String("store", "", dependStoreUsage)
	projectFlag := fs.String("project", "", dependProjectUsage)
	positionals, handled, err := parseWithPositionals(stdout, fs, args)
	if handled {
		return 0
	} else if err != nil {
		return renderErr(stdout, err)
	}
	if err := checkDependArgs(positionals, *onFlag, *kindFlag, *clearFlag); err != nil {
		return renderErr(stdout, err)
	}
	kind := *kindFlag
	if kind == "" {
		kind = "merged"
	}

	st, _, _, _, err := resolveStoreForProject(e, *projectFlag, *storeFlag, stdout)
	if err != nil {
		return renderErr(stdout, err)
	}
	child, err := resolveTicketArg(st, positionals[0], stdout)
	if err == nil {
		err = requireTicketID(st, child)
	}
	if err != nil {
		return renderErr(stdout, err)
	}
	parent := ""
	if *onFlag != "" {
		if parent, err = resolveTicketArg(st, *onFlag, stdout); err == nil {
			err = requireTicketID(st, parent)
		}
		if err != nil {
			return renderErr(stdout, err)
		}
		if parent == child {
			return renderErr(stdout, dependUsage("a ticket cannot wait for itself"))
		}
	}
	if err := st.Sync(); err != nil {
		return renderErr(stdout, err)
	}
	if err := requireOpen(st, child); err != nil {
		return renderErr(stdout, err)
	}
	rec, err := st.ReadTicket(child)
	if err != nil {
		return renderErr(stdout, err)
	}

	var (
		write  func() error
		msg    string
		result string
		after  []store.TicketBlockedBy
	)
	held := dependOf(rec.BlockedBy, parent)
	switch {
	case *clearFlag && parent == "":
		if len(rec.BlockedBy) == 0 {
			result = "unchanged"
			break
		}
		result, msg, after = "cleared", child+": clear dependencies", nil
		write = func() error { return st.ClearTicketDeps(child) }
	case *clearFlag:
		if held == nil {
			result, after = "unchanged", rec.BlockedBy
			break
		}
		result, msg = "cleared", fmt.Sprintf("%s: clear dependency on %s", child, parent)
		for _, b := range rec.BlockedBy {
			if b.Ticket != parent {
				after = append(after, b)
			}
		}
		write = func() error { return st.RemoveTicketDep(child, parent) }
	case held != nil && held.Kind == kind:
		result, after = "unchanged", rec.BlockedBy
	default:
		edge := store.TicketBlockedBy{Ticket: parent, Kind: kind}
		if cyc := ticketDepsCycle(st, child, &edge); cyc != "" {
			return renderErr(stdout, dependUsage("this edge would close a cycle: "+strings.ReplaceAll(cyc, "->", " -> ")))
		}
		result = "recorded"
		if held != nil {
			result = "changed"
		}
		msg = fmt.Sprintf("%s: depend on %s (%s)", child, parent, kind)
		after = append([]store.TicketBlockedBy{}, rec.BlockedBy...)
		if held != nil {
			for i := range after {
				if after[i].Ticket == parent {
					after[i].Kind = kind
				}
			}
		} else {
			after = append(after, edge)
		}
		write = func() error { return st.SetTicketDep(child, parent, kind) }
	}

	if write != nil {
		if _, err := st.Claim(
			func() (string, []string, error) {
				if err := write(); err != nil {
					return "", nil, err
				}
				return child, []string{st.TicketFileRelPath(child)}, nil
			},
			func(string) string { return msg },
		); err != nil {
			return renderErr(stdout, err)
		}
		// Claim never runs the checkpoint hook itself, so this is the
		// checkpoint of the edge just landed.
		st.RunCheckpointHook()
	}

	hints := []string{fmt.Sprintf("Run `jig next %s` to see what it waits for", child)}
	if result != "unchanged" && result != "cleared" && kind == "stacked" {
		hints = append(hints, fmt.Sprintf("If %s already has a branch, `jig next %s` says whether it is behind %s's branch and the `git merge` that brings it up; nothing was rewritten", child, child, parent))
	}
	axi.Render(stdout, dependBlock(child, after, result), axi.Help(hints...))
	return 0
}

// checkDependArgs is the usage of `jig depend`, judged before anything is
// read: one ticket, an --on unless everything is cleared, a kind that is one,
// and no kind with --clear.
func checkDependArgs(positionals []string, on, kind string, clear bool) error {
	switch {
	case len(positionals) == 0:
		return dependUsage("missing required ticket")
	case len(positionals) > 1:
		return dependUsage(fmt.Sprintf("jig depend takes one ticket, got %d arguments", len(positionals)))
	case clear && kind != "":
		return dependUsage("--clear takes no --kind")
	case on == "" && !clear:
		return dependUsage("missing required --on (the ticket to wait for)")
	case kind != "" && kind != "merged" && kind != "stacked":
		return dependUsage(fmt.Sprintf("kind %q is neither merged nor stacked", kind))
	}
	return nil
}

func dependUsage(msg string) error {
	return &axi.Error{Msg: msg, Code: "VALIDATION_ERROR", Help: []string{"Usage: jig depend <ticket> --on <ticket> [--kind merged|stacked], or jig depend <ticket> [--on <ticket>] --clear"}}
}

// dependOf is the entry of deps for parent, nil when there is none.
func dependOf(deps []store.TicketBlockedBy, parent string) *store.TicketBlockedBy {
	for i := range deps {
		if parent != "" && deps[i].Ticket == parent {
			return &deps[i]
		}
	}
	return nil
}

// dependBlock says the ticket's edges after the call ("none" when it has no
// blocker) and what this run did.
func dependBlock(ticket string, deps []store.TicketBlockedBy, result string) string {
	held := "none"
	if len(deps) > 0 {
		var parts []string
		for _, b := range deps {
			parts = append(parts, b.Ticket+":"+b.Kind)
		}
		held = strings.Join(parts, ",")
	}
	return axi.KV("depend", [][2]string{{"ticket", ticket}, {"blocked_by", held}, {"result", result}})
}
