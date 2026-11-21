package main

import (
	"fmt"
	"io"
	"strings"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/store"
)

// The refusal codes of an `after` record: a suggestion to land one issue after
// another, never a dependency. They check the record itself, never landing.
const (
	codeAfterNotIssue = "AFTER_NOT_ISSUE"
	codeAfterSelf     = "AFTER_SELF"
	codeAfterCycle    = "AFTER_CYCLE"
)

func afterUsage(msg string) error {
	return &axi.Error{Msg: msg, Code: "VALIDATION_ERROR", Help: []string{"Usage: jig depend <issue> --after <issue> [--clear]"}}
}

// afterEdgeError is why issue ticket cannot be recorded after other, nil when
// the edge can be true: both are issues of the store, they differ, and the edge
// closes no loop of `after`.
func afterEdgeError(st *store.Store, ticket, other string) *axi.Error {
	for _, id := range []string{ticket, other} {
		if k, ok := issueKind(st, id); !ok {
			return &axi.Error{
				Msg:  fmt.Sprintf("%s is not an issue of the store (%s): only an issue lands after another issue", id, k),
				Code: codeAfterNotIssue,
				Help: []string{"An issue is the ticket `jig chart new --issue` binds a chart to; a task waits with `jig depend --on`."},
			}
		}
	}
	if ticket == other {
		return &axi.Error{Msg: ticket + " cannot be suggested to land after itself", Code: codeAfterSelf}
	}
	if loop := afterLoop(st, ticket, other); loop != "" {
		return &axi.Error{
			Msg:  "this edge would close a loop of after: " + loop,
			Code: codeAfterCycle,
			Help: []string{fmt.Sprintf("Run `jig depend %s --after %s --clear` to remove the edge that closes it.", other, ticket)},
		}
	}
	return nil
}

// issueKind says whether id is a ticket of kind issue, and else what it is.
func issueKind(st *store.Store, id string) (string, bool) {
	if store.CheckTicketID(id) != nil || !st.TicketExists(id) {
		return "not in the store", false
	}
	rec, err := st.ReadTicket(id)
	if err != nil {
		return "unreadable", false
	}
	if k := rec.KindOrDefault(); k != store.KindIssue {
		return "a " + k, false
	}
	return "issue", true
}

// afterLoop is the loop ticket -> other -> ... -> ticket the edge ticket after
// other would close, "" when it closes none.
func afterLoop(st *store.Store, ticket, other string) string {
	seen := map[string]bool{}
	var walk func(n string, path []string) []string
	walk = func(n string, path []string) []string {
		path = append(path, n)
		if n == ticket {
			return path
		}
		if seen[n] {
			return nil
		}
		seen[n] = true
		if store.CheckTicketID(n) != nil {
			return nil
		}
		rec, err := st.ReadTicket(n)
		if err != nil {
			return nil
		}
		for _, a := range rec.After {
			if got := walk(a, path); got != nil {
				return got
			}
		}
		return nil
	}
	if p := walk(other, []string{ticket}); p != nil {
		return strings.Join(p, " -> ")
	}
	return ""
}

// afterLoopOf is a loop of after the record of ticket sits on, "" when none.
func afterLoopOf(st *store.Store, ticket string) string {
	rec, err := st.ReadTicket(ticket)
	if err != nil {
		return ""
	}
	for _, a := range rec.After {
		if loop := afterLoop(st, ticket, a); loop != "" {
			return loop
		}
	}
	return ""
}

// validateTicketAfter is the problems of a ticket's after list: the same three
// refusals `jig depend --after` makes, found in a hand-edited record, and an
// after on a ticket that is not an issue.
func validateTicketAfter(st *store.Store, ticket string) []string {
	rec, err := st.ReadTicket(ticket)
	if err != nil || len(rec.After) == 0 {
		return nil
	}
	var problems []string
	if k := rec.KindOrDefault(); k != store.KindIssue {
		problems = append(problems, fmt.Sprintf("%s: ticket.yaml: after on a ticket of kind %s; only an issue has one", codeAfterNotIssue, k))
	}
	for _, a := range rec.After {
		if a == ticket {
			problems = append(problems, fmt.Sprintf("%s: ticket.yaml: after names the ticket itself", codeAfterSelf))
			continue
		}
		if k, ok := issueKind(st, a); !ok {
			problems = append(problems, fmt.Sprintf("%s: ticket.yaml: after %q is not an issue of the store (%s)", codeAfterNotIssue, a, k))
		}
	}
	if loop := afterLoopOf(st, ticket); loop != "" {
		problems = append(problems, fmt.Sprintf("%s: ticket.yaml: after closes a loop: %s", codeAfterCycle, loop))
	}
	return problems
}

// cmdDependAfter implements `jig depend <issue> --after <issue> [--clear]`: it
// adds or removes one id of the issue's after list and commits the record
// alone through Claim. It refuses, writing nothing, an edge that cannot be true.
func cmdDependAfter(e env, positionals []string, after string, clear bool, storeFlag, projectFlag string, stdout io.Writer) int {
	st, _, _, _, err := resolveStoreForProject(e, projectFlag, storeFlag, stdout)
	if err != nil {
		return renderErr(stdout, err)
	}
	child, err := resolveTicketArg(st, positionals[0], stdout)
	if err == nil {
		err = requireTicket(st, child)
	}
	if err != nil {
		return renderErr(stdout, err)
	}
	other := after
	if resolved, rerr := st.ResolveTicket(after); rerr == nil {
		other = resolved
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
	held := false
	for _, a := range rec.After {
		held = held || a == other
	}

	var (
		write  func() error
		msg    string
		result string
		list   []string
	)
	switch {
	case clear && !held:
		result, list = "unchanged", rec.After
	case clear:
		result, msg = "cleared", fmt.Sprintf("%s: clear land-after %s", child, other)
		for _, a := range rec.After {
			if a != other {
				list = append(list, a)
			}
		}
		write = func() error { return st.RemoveTicketAfter(child, other) }
	case held:
		result, list = "unchanged", rec.After
	default:
		if ae := afterEdgeError(st, child, other); ae != nil {
			return renderErr(stdout, ae)
		}
		result, msg = "recorded", fmt.Sprintf("%s: land after %s", child, other)
		list = append(append([]string{}, rec.After...), other)
		write = func() error { return st.SetTicketAfter(child, other) }
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
		st.RunCheckpointHook()
	}
	shown := "none"
	if len(list) > 0 {
		shown = strings.Join(list, ",")
	}
	axi.Render(stdout,
		axi.KV("depend", [][2]string{{"ticket", child}, {"after", shown}, {"result", result}}),
		axi.Help(fmt.Sprintf("Run `jig next %s` to see what it is suggested to land after; nothing waits", child)),
	)
	return 0
}
