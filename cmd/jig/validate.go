package main

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
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
		Spec: cmdSpec{"validate", "check a ticket's brief, slices and manifest against the store, and approve a valid brief; or a chart's entries, minting nothing", []flagSpec{
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
	// A ticket id or alias wins over a chart name, as it does for `jig next`
	// and `jig status`: chartNamed says no for a word a ticket holds. A word
	// that is neither falls through, and is reported as a ticket with no brief.
	if chart, ok := chartNamed(st, ticket); ok {
		return validateChart(st, cfg, chart, stdout)
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
	advisories := validateAdvisories(st, ticket, slices, ready.ProjectRepos(cfg, mp.Clones))
	blocks := []string{"valid: yes"}
	nextLines := []string{fmt.Sprintf("Run `jig next %s` to see what is ready, then `jig claim %s --slice <slice>` to take one", ticket, ticket)}
	if *check {
		blocks = append(blocks, axi.KV("approval", [][2]string{{"result", approvalChecked}}))
		var brief []byte
		if data, rerr := os.ReadFile(filepath.Join(st.TicketDir(ticket), "brief.md")); rerr == nil {
			brief = data
		}
		nextLines = checkNext(ticket, defaultedDecisions(brief), e.stdinIsTerminal(stdin))
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
	blocks = append(blocks, axi.Help(nextLines...))
	axi.Render(stdout, blocks...)
	return 0
}

// validateChart implements `jig validate <chart>`: every problem of the chart's
// entries, one line each, and nothing minted. It reads the store and writes
// nothing: no sync, no commit, no mirror, and --check changes nothing because a
// chart has no approval to record. The checks are checkChart's, the ones `jig
// graduate` runs before it mints, so a chart that passes here is not refused
// there for its entries. A tickets.yaml that cannot be read and a chart with no
// entries are problems too.
func validateChart(st *store.Store, cfg project.Config, chart string, stdout io.Writer) int {
	entries, err := st.ReadChart(chart)
	var lines []string
	switch {
	case err != nil:
		lines = append(lines, fmt.Sprintf("charts/%s/tickets.yaml could not be read: %v", chart, err))
	case len(entries) == 0:
		lines = append(lines, fmt.Sprintf("charts/%s/tickets.yaml has no entries", chart))
	default:
		_, problems := checkChart(st, cfg, chart, entries)
		for _, p := range problems {
			lines = append(lines, p.Line)
		}
	}
	if len(lines) > 0 {
		return renderErr(stdout, &axi.Error{
			Msg:  fmt.Sprintf("chart %s failed validation", chart),
			Code: "VALIDATION_ERROR",
			Help: append(lines, fmt.Sprintf("Fix them in charts/%s/tickets.yaml, then run `jig validate %s` again", chart, chart)),
		})
	}

	graduated := 0
	for _, e := range entries {
		if e.ID != "" {
			graduated++
		}
	}
	ungraduated := len(entries) - graduated
	next := fmt.Sprintf("Every entry has a ticket: `jig next %s` says where the chart stands", chart)
	if ungraduated == 1 {
		next = fmt.Sprintf("Run `jig graduate %s` to create the 1 entry that has no ticket yet", chart)
	} else if ungraduated > 1 {
		next = fmt.Sprintf("Run `jig graduate %s` to create the %d entries that have no ticket yet", chart, ungraduated)
	}
	axi.Render(stdout,
		"valid: yes",
		axi.KV("chart", [][2]string{
			{"name", chart},
			{"entries", strconv.Itoa(len(entries))},
			{"graduated", strconv.Itoa(graduated)},
			{"ungraduated", strconv.Itoa(ungraduated)},
		}),
		axi.Help(next),
	)
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

// defaultedDecisions counts the decisions of brief tagged defaulted, as the
// intake skill and the store's briefs write the tag: "defaulted", then a date
// or a closing parenthesis, in a decision wherever the brief puts it (a bullet,
// a numbered item or a paragraph, under any heading, bold or not, the tag on
// the first line or a wrapped one). A block counts once however many tags it
// holds. Prose that explains the tag (it is quoted or backticked) is not one.
func defaultedDecisions(brief []byte) int {
	n := 0
	var block []string
	inFence := false
	flush := func() {
		if defaultedTag.MatchString(strings.Join(block, " ")) {
			n++
		}
		block = block[:0]
	}
	for _, line := range strings.Split(string(brief), "\n") {
		trim := strings.TrimSpace(line)
		switch {
		case strings.HasPrefix(trim, "```"):
			flush()
			inFence = !inFence
		case inFence:
		case trim == "" || strings.HasPrefix(line, "#"):
			flush()
		case listItem.MatchString(line):
			flush()
			block = append(block, trim)
		default:
			block = append(block, trim)
		}
	}
	flush()
	return n
}

// listItem matches the line that starts a bullet or a numbered item.
var listItem = regexp.MustCompile(`^\s*(?:[-*+]|\d+[.)])\s`)

// defaultedTag matches the tag: defaulted, then a date or a closing
// parenthesis (or "(defaulted;" and "(defaulted,"), not preceded by a quote or
// backtick (prose about the tag).
var defaultedTag = regexp.MustCompile(`\(defaulted[;,]|(?:^|[^"` + "`" + `A-Za-z0-9])defaulted(?:\s+\d{4}-\d{2}-\d{2}|\s*\))`)

// checkNext is the help --check prints: show the person the brief and ask
// about its decisions, then validate. defaulted is the number of decisions
// tagged defaulted (0 names none). Off a terminal the validate that follows
// records an agent's approval, which the line says; at one, the person's.
func checkNext(ticket string, defaulted int, atTerminal bool) []string {
	ask := "Show the person the brief and ask about its decisions"
	switch {
	case defaulted == 1:
		ask += " (1 is tagged defaulted)"
	case defaulted > 1:
		ask += fmt.Sprintf(" (%d are tagged defaulted)", defaulted)
	}
	lines := []string{
		ask + fmt.Sprintf(", then run `jig validate %s` once they confirm; a prompt that said to go with the recommendations confirms for them", ticket),
	}
	if atTerminal {
		return append(lines, "At this terminal the approval will be the person's")
	}
	return append(lines, fmt.Sprintf("Off a terminal `jig validate %s` will record an agent's approval, not a person's", ticket))
}
