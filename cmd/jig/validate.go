package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/pool"
	"github.com/develdeco/jig/internal/project"
	"github.com/develdeco/jig/internal/ready"
	"github.com/develdeco/jig/internal/store"
)

// init registers this file's verb with the registry (registry.go).
func init() {
	registerVerb(verb{
		Spec: cmdSpec{"validate", "check a ticket's brief, slices and manifest against the store, and approve a valid brief", []flagSpec{
			{"store", "explicit store path", false},
			{"project", "project name, resolved via the machine mapping", false},
			{"check", validateCheckUsage, false},
		}, false},
		Order: 190,
		Run: func(e env, args []string, stdout io.Writer, stdin io.Reader) int {
			return cmdValidate(e, args, stdout, stdin)
		},
		Probe: []string{"T-1"},
	})
}

// validateCheckUsage is the --check flag's usage, in the help row and the
// FlagSet alike.
const validateCheckUsage = "check only: print the verdict and the brief's section hashes, and record no approval"

// cmdValidate implements `jig validate <ticket> [--check]`. A ticket that is
// valid has its brief approved: the approval is recorded for the brief as it
// was when the checks began (brief.md, and illustration.html when it has one),
// so a later edit of either shows as unapproved to `jig claim`. With --check
// nothing is recorded: the session drafting a brief checks its slices.yaml and
// reads the section hashes it cites before the person has confirmed the brief.
// Besides the ticket own files the checks look at the store around it
// (validate_store.go); what they only advise on is printed under the verdict.
func cmdValidate(e env, args []string, stdout io.Writer, stdin io.Reader) int {
	fs := newFlagSet(e, "validate")
	storeFlag := fs.String("store", "", "explicit store path")
	projectFlag := fs.String("project", "", "project name, resolved via the machine mapping")
	check := fs.Bool("check", false, validateCheckUsage)
	ticket, rest, err := requirePositional(args, "ticket")
	if err != nil {
		return renderErr(stdout, err)
	}
	if handled, err := parseFlags(stdout, fs, rest); handled {
		return 0
	} else if err != nil {
		return renderErr(stdout, err)
	}

	st, cfg, mp, _, err := resolveStoreForProject(e, *projectFlag, *storeFlag, stdout)
	if err != nil {
		return renderErr(stdout, err)
	}
	ticket, err = resolveTicketArg(st, ticket, stdout)
	if err != nil {
		return renderErr(stdout, err)
	}

	// The hash is taken before the checks read the brief: a brief edited
	// meanwhile is then approved as the older text and shows as changed.
	sum, sumErr := st.BriefSHA256(ticket)
	problems, err := validateTicket(st, cfg, mp, ticket)
	if err != nil {
		return renderErr(stdout, err)
	}
	if len(problems) > 0 {
		return renderErr(stdout, &axi.Error{
			Msg:  fmt.Sprintf("ticket %s failed validation", ticket),
			Code: "VALIDATION_ERROR",
			Help: problems,
		})
	}
	if sumErr != nil {
		return renderErr(stdout, sumErr)
	}
	slices, err := st.ReadSlices(ticket)
	if err != nil {
		return renderErr(stdout, err)
	}
	advisories := validateAdvisories(st, ticket, slices)
	blocks := []string{"valid: yes"}
	next := fmt.Sprintf("Run `jig next %s` to see what is ready, then `jig claim %s --slice <slice>` to take one", ticket, ticket)
	if *check {
		blocks = append(blocks, axi.KV("approval", [][2]string{{"result", approvalChecked}}))
		next = fmt.Sprintf("Run `jig validate %s` once the person has confirmed the brief, to record its approval", ticket)
	} else {
		approval, result, err := recordApproval(e, st, ticket, sum, defaultProvenance(e, stdin))
		if err != nil {
			return renderErr(stdout, err)
		}
		blocks = append(blocks, axi.KV("approval", [][2]string{
			{"result", result}, {"by", approval.By}, {"email", approval.Email}, {"at", approval.At},
			{"provenance", approval.Provenance}, {"brief_sha256", approval.BriefSHA256},
		}))
	}
	if data, rerr := os.ReadFile(filepath.Join(st.TicketDir(ticket), "brief.md")); rerr == nil {
		hashes := store.BriefSectionHashes(data)
		headings := make([]string, 0, len(hashes))
		for h := range hashes {
			headings = append(headings, h)
		}
		sort.Strings(headings)
		rows := make([][]string, 0, len(headings))
		for _, h := range headings {
			rows = append(rows, []string{h, hashes[h]})
		}
		blocks = append(blocks, axi.Table("sections", []string{"heading", "sha256"}, rows))
	}
	if table := advisoryTable(advisories); table != "" {
		blocks = append(blocks, table)
	}
	blocks = append(blocks, axi.Help(next))
	axi.Render(stdout, blocks...)
	return 0
}

// validateTicket checks that ticket's brief, slices.yaml, and the manifests of
// the repos its slices build in agree, returning every problem found (nil
// means valid). Two ids around a ticket are reserved by jig's own machinery
// and reported as problems on their own: the ticket id, which names the
// ticket's pool leases (see pool.CheckTicket for the suffixes it refuses), and
// the slice id "gate", which the gate reviewer's dispatch uses as its own
// session slice name (checked below, alongside every other slice id).
func validateTicket(st *store.Store, cfg project.Config, mp project.MachineProject, ticket string) ([]string, error) {
	// An id that cannot name its leases is the only problem worth reporting
	// on its own: every path below is derived from it.
	if err := pool.CheckTicket(ticket); err != nil {
		return []string{err.Error()}, nil
	}

	var problems []string

	briefData, briefErr := os.ReadFile(filepath.Join(st.TicketDir(ticket), "brief.md"))
	if briefErr != nil {
		problems = append(problems, fmt.Sprintf("brief.md not found: %v", briefErr))
	} else if hashes := store.BriefSectionHashes(briefData); len(hashes) == 0 {
		problems = append(problems, `brief.md has no "## " sections`)
	}

	aliasProblems, err := st.CheckAliases()
	if err != nil {
		return nil, err
	}
	problems = append(problems, aliasProblems...)

	problems = append(problems, validateTicketDeps(st, ticket)...)
	problems = append(problems, validateTicketClosed(st, ticket)...)
	problems = append(problems, validateUpstreamEdges(st, ticket)...)
	problems = append(problems, validateTicketBranch(st, cfg, ticket)...)

	slices, err := st.ReadSlices(ticket)
	if err != nil {
		problems = append(problems, fmt.Sprintf("slices.yaml did not parse: %v", err))
		return problems, nil
	}

	var hashes map[string]string
	if briefErr == nil {
		hashes = store.BriefSectionHashes(briefData)
	}
	hashExists := func(h string) bool {
		for _, cur := range hashes {
			if cur == h {
				return true
			}
		}
		return false
	}

	ids := map[string]bool{}
	for _, sl := range slices {
		ids[sl.ID] = true
	}
	for _, sl := range slices {
		if sl.ID == "gate" {
			problems = append(problems, `slice id "gate" is reserved for the gate reviewer's own dispatch, which uses it as the session's slice name`)
		}
	}

	problems = append(problems, validateWorkspaces(ready.ProjectRepos(cfg, mp.Clones), slices)...)

	for _, sl := range slices {
		for _, h := range sl.FromBrief {
			if !hashExists(h) {
				problems = append(problems, fmt.Sprintf("slice %s: from_brief hash %s does not match any current brief section", sl.ID, h))
			}
		}
		for _, b := range sl.BlockedBy {
			if !ids[b] {
				problems = append(problems, fmt.Sprintf("slice %s: blocked_by %q does not exist", sl.ID, b))
			}
		}
		if strings.TrimSpace(sl.Oracle) == "" {
			problems = append(problems, fmt.Sprintf("slice %s: oracle is empty", sl.ID))
		}
	}

	if cyc := findBlockedByCycle(slices); cyc != "" {
		problems = append(problems, "blocked_by cycle: "+cyc)
	}

	return problems, nil
}

// findBlockedByCycle runs a DFS over slices' blocked_by edges and returns a
// description of the first cycle found, or "" when the graph is acyclic.
func findBlockedByCycle(slices []store.Slice) string {
	edges := map[string][]string{}
	for _, sl := range slices {
		edges[sl.ID] = sl.BlockedBy
	}

	const (
		white = 0
		gray  = 1
		black = 2
	)
	color := map[string]int{}
	var path []string
	var found string

	var dfs func(n string)
	dfs = func(n string) {
		if found != "" {
			return
		}
		color[n] = gray
		path = append(path, n)
		for _, next := range edges[n] {
			if found != "" {
				return
			}
			switch color[next] {
			case gray:
				idx := -1
				for i, p := range path {
					if p == next {
						idx = i
						break
					}
				}
				if idx == -1 {
					found = strings.Join(append(append([]string{}, path...), next), "->")
				} else {
					found = strings.Join(append(append([]string{}, path[idx:]...), next), "->")
				}
				return
			case white:
				dfs(next)
			}
		}
		path = path[:len(path)-1]
		color[n] = black
	}

	for _, sl := range slices {
		if found != "" {
			break
		}
		if color[sl.ID] == white {
			dfs(sl.ID)
		}
	}
	return found
}

// validateTicketDeps checks ticket's optional ticket.yaml, returning every
// problem found (nil means valid or absent). A ticket without the file
// validates as before. A ticket.yaml that cannot be read is one problem, the
// failure itself, followed by the next steps the refusal carries when it has
// any (an unknown key: upgrade jig or fix the key; a newer schema: upgrade
// jig), since the message alone names the problem but not what to do about it.
func validateTicketDeps(st *store.Store, ticket string) []string {
	deps, err := st.ReadTicketDeps(ticket)
	if err != nil {
		problems := []string{fmt.Sprintf("ticket.yaml could not be read: %v", err)}
		var ae *axi.Error
		if errors.As(err, &ae) {
			problems = append(problems, ae.Help...)
		}
		return problems
	}

	var problems []string
	for i, d := range deps {
		if d.Ticket == "" {
			problems = append(problems, fmt.Sprintf("ticket.yaml: entry %d has no ticket", i+1))
			continue
		}
		resolved, rerr := st.ResolveTicket(d.Ticket)
		if rerr != nil {
			problems = append(problems, fmt.Sprintf("ticket.yaml: blocked_by %q: %v", d.Ticket, rerr))
		} else if err := requireTicketID(st, resolved); err != nil {
			problems = append(problems, fmt.Sprintf("ticket.yaml: blocked_by %q: %v", d.Ticket, err))
		}
		if d.Kind != "merged" && d.Kind != "stacked" {
			problems = append(problems, fmt.Sprintf("ticket.yaml: blocked_by %q has kind %q; must be \"merged\" or \"stacked\"", d.Ticket, d.Kind))
		}
	}

	if cyc := ticketDepsCycle(st, ticket); cyc != "" {
		problems = append(problems, "ticket.yaml blocked_by cycle: "+cyc)
	}
	return problems
}

// validateTicketClosed checks the closed entry of ticket.yaml when it has one,
// returning every problem found (nil means open or valid): a reason that is
// none of covered, superseded and not-planned, a missing by where the reason
// needs one (or one where it has none), a date that is no date, and, for
// superseded, a by that is not another ticket of the store. A ticket.yaml that
// cannot be read is validateTicketDeps to report.
func validateTicketClosed(st *store.Store, ticket string) []string {
	rec, err := st.ReadTicket(ticket)
	if err != nil || rec.Closed == nil {
		return nil
	}
	var problems []string
	for _, p := range rec.Closed.Check() {
		problems = append(problems, "ticket.yaml: "+p)
	}
	if rec.Closed.Reason == store.CloseSuperseded && rec.Closed.By != "" {
		by, rerr := st.ResolveTicket(rec.Closed.By)
		if rerr == nil {
			rerr = requireTicketID(st, by)
		}
		switch {
		case rerr != nil:
			problems = append(problems, fmt.Sprintf("ticket.yaml: closed: by %q is not a ticket of the store", rec.Closed.By))
		case by == ticket:
			problems = append(problems, "ticket.yaml: closed: a ticket cannot be superseded by itself")
		}
	}
	return problems
}

// validateTicketBranch checks ticket's optional recorded branch through the
// same Store.TicketBranch validation every build lease, gate round and
// publish runs, so a bad hand-edited branch (equal to the target of one of the
// project's repos, or not a name git itself would accept) is reported here,
// where it is cheap to fix, before one of those fails on it. The branch has
// the same name in every repo, so it is checked against each target. A record
// that cannot be read at all is left to validateTicketDeps, which already
// reports it: reporting it again here would duplicate the same "ticket.yaml
// could not be read" problem.
func validateTicketBranch(st *store.Store, cfg project.Config, ticket string) []string {
	if _, err := st.ReadTicket(ticket); err != nil {
		return nil
	}
	var problems []string
	seen := map[string]bool{}
	for _, target := range projectTargets(cfg) {
		if _, err := st.TicketBranch(ticket, target); err != nil && !seen[err.Error()] {
			seen[err.Error()] = true
			problems = append(problems, err.Error())
		}
	}
	return problems
}

// ticketDepsCycle runs a DFS over blocked_by edges rooted at ticket, reading
// each visited ticket's own ticket.yaml as it goes, and returns a description
// of the first cycle found, or "" when none exists. A ticket id that cannot
// name a store folder, or whose ticket.yaml does not parse, contributes no
// further edges: that ticket's own validate reports the problem.
func ticketDepsCycle(st *store.Store, ticket string) string {
	cache := map[string][]store.TicketBlockedBy{}
	deps := func(id string) []store.TicketBlockedBy {
		if d, ok := cache[id]; ok {
			return d
		}
		var d []store.TicketBlockedBy
		if pool.CheckTicket(id) == nil {
			d, _ = st.ReadTicketDeps(id)
		}
		cache[id] = d
		return d
	}

	const (
		white = 0
		gray  = 1
		black = 2
	)
	color := map[string]int{}
	var path []string
	var found string

	var dfs func(n string)
	dfs = func(n string) {
		if found != "" {
			return
		}
		color[n] = gray
		path = append(path, n)
		for _, b := range deps(n) {
			if found != "" {
				return
			}
			if b.Ticket == "" {
				continue
			}
			next := b.Ticket
			if resolved, err := st.ResolveTicket(b.Ticket); err == nil {
				next = resolved
			}
			switch color[next] {
			case gray:
				idx := 0
				for i, p := range path {
					if p == next {
						idx = i
						break
					}
				}
				found = strings.Join(append(append([]string{}, path[idx:]...), next), "->")
				return
			case white:
				dfs(next)
			}
		}
		path = path[:len(path)-1]
		color[n] = black
	}

	dfs(ticket)
	return found
}
