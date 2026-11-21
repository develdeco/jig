package main

import (
	"fmt"
	"io"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/land"
	"github.com/develdeco/jig/internal/ledger"
	"github.com/develdeco/jig/internal/project"
	"github.com/develdeco/jig/internal/repohost"
	"github.com/develdeco/jig/internal/store"
)

const (
	landIssueUsage  = "the issue a task two issues share is landed into (a ticket with at most one issue takes none)"
	landMethodUsage = "the merge method to name: squash, merge or rebase (default: squash where the repo allows it, else merge, else rebase)"
	// landNamedWindow bounds how far back the ledger is read for the line that
	// says this machine named the merge.
	landNamedWindow = 4 << 20
)

// init registers this file's verb with the registry (registry.go).
func init() {
	registerVerb(verb{
		Spec: cmdSpec{"land", "name the merge of a published ticket's pull requests, and record it once the host says merged: never merges", []flagSpec{
			{"issue", landIssueUsage, false},
			{"method", landMethodUsage, false},
			{"store", "explicit store path", false},
			{"project", "project name, resolved via the machine mapping", false},
		}, false},
		Order: 231,
		Run: func(e env, args []string, stdout io.Writer, stdin io.Reader) int {
			return cmdLand(e, args, stdout, stdin)
		},
		Probe: []string{"T-1"},
		Note:  "Run it only at the person's word (\"Land T-1\"): it names the merge, you make it, then run it again to record it. See `jig skill land`.",
	})
}

// cmdLand implements `jig land <ticket> [--issue <ID>] [--method m]`. It never
// merges. It refuses a landing that is out of order (nothing written), names the
// merge to make in each repo not yet merged, and, once the host says every pull
// request of the ticket is merged, writes one landed line per repo (ADR 0061).
func cmdLand(e env, args []string, stdout io.Writer, stdin io.Reader) int {
	ticket, rest, err := requirePositional(args, "ticket")
	if err != nil {
		return renderErr(stdout, err)
	}
	fs := newFlagSet(e, "land")
	issue := fs.String("issue", "", landIssueUsage)
	method := fs.String("method", "", landMethodUsage)
	storeFlag := fs.String("store", "", "explicit store path")
	projectFlag := fs.String("project", "", "project name, resolved via the machine mapping")
	if handled, err := parseFlags(stdout, fs, rest); handled {
		return 0
	} else if err != nil {
		return renderErr(stdout, err)
	}
	switch *method {
	case "", "squash", "merge", "rebase":
	default:
		return renderErr(stdout, &axi.Error{Msg: fmt.Sprintf("--method %q is not squash, merge or rebase", *method), Code: "VALIDATION_ERROR"})
	}

	st, cfg, mp, jigHome, err := resolveStoreForProject(e, *projectFlag, *storeFlag, stdout)
	if err != nil {
		return renderErr(stdout, err)
	}
	if ticket, err = resolveTicketArg(st, ticket, stdout); err != nil {
		return renderErr(stdout, err)
	}
	if *issue != "" {
		if id, err := st.ResolveTicket(*issue); err == nil {
			*issue = id
		}
	}
	if err := requireTicket(st, ticket); err != nil {
		return renderErr(stdout, err)
	}

	deps := landDeps(e, st, cfg, mp, jigHome)
	v, err := land.Judge(deps, ticket, land.Options{Issue: *issue, Method: *method})
	if err != nil {
		return renderErr(stdout, err)
	}
	if v.Refusal != nil {
		return renderErr(stdout, v.Refusal.AxiError())
	}
	var notes []string
	for _, n := range v.Notes {
		notes = append(notes, "Note: "+n)
	}
	switch {
	case len(v.Merge) > 0:
		return renderMerge(e, stdout, jigHome, ticket, v, notes)
	case len(v.Record) > 0:
		return recordLanded(e, stdin, stdout, st, jigHome, ticket, v, notes)
	}
	// Every repo has its line: already landed, and what is next.
	rows := landedRows(v.Landed)
	axi.Render(stdout,
		axi.KV("already_landed", [][2]string{{"ticket", ticket}}),
		axi.Table("landed", []string{"repo", "pr", "base", "into", "commit", "provenance"}, rows),
		axi.Help(append(notes, nextLanding(st, ticket))...),
	)
	return 0
}

// landDeps is what land reads: the merge facts and the host through this
// command's gh.
func landDeps(e env, st *store.Store, cfg project.Config, mp project.MachineProject, jigHome string) land.Deps {
	gh, _ := e.lookPath("gh")
	return land.Deps{
		Store: st, Cfg: cfg, Clones: mp.Clones,
		Merges:  newMergeReader(e, st, cfg, mp, jigHome, 0),
		Status:  func(url string) (repohost.Status, bool) { return repohost.PRStatus(gh, nil, url) },
		Methods: func(url string) (repohost.MergeMethods, bool) { return repohost.AllowedMergeMethods(gh, nil, url) },
	}
}

// renderMerge names the merges to make, one row per repo, and leaves a line in
// the ledger saying so, so that the landing that follows is the session's own.
// Nothing is merged and nothing is written to the store.
func renderMerge(e env, stdout io.Writer, jigHome, ticket string, v land.Verdict, notes []string) int {
	if jigHome != "" {
		_ = ledger.Append(jigHome, journal.Line{TS: stamp(e), Ticket: ticket, Event: journal.EventLandNamed, Into: v.Into})
	}
	rows := make([][]string, len(v.Merge))
	help := append([]string{}, notes...)
	for i, r := range v.Merge {
		rows[i] = []string{r.Repo, r.PR, r.Base, r.Method, r.Command}
		if r.Conflicting {
			help = append(help, "Conflict: "+r.Resolve)
		}
	}
	help = append(help,
		"jig does not merge: make each merge with its command, at the person's word, then run `jig land "+ticket+"` again to record it",
	)
	axi.Render(stdout,
		axi.KV("land", [][2]string{{"ticket", ticket}, {"into", orDash(v.Into)}, {"merges", fmt.Sprint(len(v.Merge))}}),
		axi.Table("merge", []string{"repo", "pr", "base", "method", "command"}, rows),
		axi.Help(help...),
	)
	return 0
}

// recordLanded writes the landed lines of a ticket whose pull requests are all
// merged on the host. Their provenance is the host's unless this machine named
// the merge first (then it is the agent's, or a person's at a terminal).
func recordLanded(e env, stdin io.Reader, stdout io.Writer, st *store.Store, jigHome, ticket string, v land.Verdict, notes []string) int {
	provenance := journal.ProvenanceHost
	if named(jigHome, ticket) {
		provenance = journal.ProvenanceAgent
		if e.stdinIsTerminal(stdin) {
			provenance = journal.ProvenancePerson
		}
	}
	if err := st.Sync(); err != nil {
		return renderErr(stdout, err)
	}
	rows := make([]land.Landing, len(v.Record))
	for i, l := range v.Record {
		l.Provenance = provenance
		rows[i] = l
		line := l.Line(ticket)
		line.TS = stamp(e)
		if err := journal.Append(st, ticket, line); err != nil {
			return renderErr(stdout, err)
		}
	}
	if err := st.Push(fmt.Sprintf("%s: landed", ticket)); err != nil {
		return renderErr(stdout, err)
	}
	axi.Render(stdout,
		axi.KV("landed", [][2]string{{"ticket", ticket}, {"provenance", provenance}}),
		axi.Table("landed", []string{"repo", "pr", "base", "into", "commit", "provenance"}, landedRows(rows)),
		axi.Help(append(notes, nextLanding(st, ticket))...),
	)
	return 0
}

// named says this machine named a merge for ticket: a land-named line in the
// ledger.
func named(jigHome, ticket string) bool {
	if jigHome == "" {
		return false
	}
	found := false
	err := ledger.ReadBackward(jigHome, landNamedWindow, func(l journal.Line) bool {
		if l.Event == journal.EventLandNamed && l.Ticket == ticket {
			found = true
			return false
		}
		return true
	})
	return err == nil && found
}

func landedRows(ls []land.Landing) [][]string {
	rows := make([][]string, len(ls))
	for i, l := range ls {
		rows[i] = []string{l.Repo, l.PR, l.Base, orDash(l.Into), orDash(l.Commit), l.Provenance}
	}
	return rows
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// nextLanding is what the next landing is: a published ticket that waits for
// this one, else the container this one lands into, else that nothing waits.
func nextLanding(st *store.Store, ticket string) string {
	all, err := st.Tickets()
	if err != nil {
		return "Nothing waits on a landing here"
	}
	for _, id := range all {
		rec, err := st.ReadTicket(id)
		if err != nil || rec.Closed != nil {
			continue
		}
		for _, b := range rec.BlockedBy {
			if resolved, err := st.ResolveTicket(b.Ticket); err == nil && resolved == ticket {
				if got, _ := land.Landed(st, id, ""); len(got) == 0 && published(st, id) {
					return fmt.Sprintf("The next landing is `jig land %s`, which waited for it", id)
				}
			}
		}
	}
	if rec, err := st.ReadTicket(ticket); err == nil {
		if cs, err := st.ContainersOf(ticket, rec); err == nil && len(cs) == 1 && published(st, cs[0]) {
			return fmt.Sprintf("When its other tasks have landed, the next landing is `jig land %s`", cs[0])
		}
	}
	return "Nothing waits on a landing here"
}

// published says ticket has a publish-done line.
func published(st *store.Store, ticket string) bool {
	lines, err := journal.Read(st, ticket)
	if err != nil {
		return false
	}
	for _, l := range lines {
		if l.Event == journal.EventPublishDone {
			return true
		}
	}
	return false
}
