package main

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/phase"
	"github.com/develdeco/jig/internal/project"
	"github.com/develdeco/jig/internal/ready"
	"github.com/develdeco/jig/internal/stack"
	"github.com/develdeco/jig/internal/store"
	"github.com/develdeco/jig/internal/verifydeliver"
)

// init registers this file's verb with the registry (registry.go).
func init() {
	registerVerb(verb{
		Spec: cmdSpec{"next", "name what is ready to build: a ticket's ready slices by repo, or the tickets of the store", []flagSpec{
			{"store", "explicit store path", false},
			{"project", "project name, resolved via the machine mapping", false},
			{"topology", "also print every slice of the ticket with its repo, wave and blockers", false},
			{"for", "the phase you are about to run (brief, build, review or publish): says whether it can run now, what it waits for, or that it is done already", false},
			{"json", "print one JSON object instead of blocks", false},
		}, false},
		Order: 301,
		Run: func(e env, args []string, stdout io.Writer, _ io.Reader) int {
			return cmdNext(e, args, stdout)
		},
	})
}

// cmdNext implements `jig next [<ticket>]`. With a ticket it prints the
// slices that are ready to build, grouped by the repo they build in, the
// branch to create and how the repos can be shared out; without one, every
// ticket of the store in order with what blocks it and which are ready, the
// ready tickets in the owner's priority order (jig rank), and a warning for
// ready tickets that build in the same workspace and for local branches of
// different tickets that change the same files.
// With a ticket it also says which phase the ticket is at, whether that phase
// can start now, why not, and the prompt that starts it (internal/phase); --for
// asks about one phase, whatever the ticket is at. A ticket stacked on another
// gets, for each repo it builds in, where its branch starts and where its pull
// request goes (internal/stack): the parent's branch in a repo they share, the
// target elsewhere and once the parent merged.
// It reads the store and the clones and writes nothing.
func cmdNext(e env, args []string, stdout io.Writer) int {
	fs := newFlagSet(e, "next")
	storeFlag := fs.String("store", "", "explicit store path")
	projectFlag := fs.String("project", "", "project name, resolved via the machine mapping")
	topology := fs.Bool("topology", false, "also print every slice of the ticket with its repo, wave and blockers")
	forFlag := fs.String("for", "", "the phase you are about to run (brief, build, review or publish): says whether it can run now, what it waits for, or that it is done already")
	jsonOut := fs.Bool("json", false, "print one JSON object instead of blocks")
	positionals, handled, err := parseWithPositionals(stdout, fs, args)
	if handled {
		return 0
	} else if err != nil {
		return renderErr(stdout, err)
	}
	ticket, err := atMostOne(positionals, "jig next", "ticket")
	if err != nil {
		return renderErr(stdout, err)
	}
	asked, err := parseAskedPhase(*forFlag, ticket)
	if err != nil {
		return renderErr(stdout, err)
	}

	st, cfg, mp, jigHome, err := resolveStoreForProject(e, *projectFlag, *storeFlag, stdout)
	if err != nil {
		return renderErr(stdout, err)
	}
	distribution := ""
	if cwd, err := e.getwd(); err == nil {
		if _, m, ok, err := project.FindMarker(cwd); err == nil && ok {
			distribution = m.Distribution
		}
	}

	var out any
	var blocks []string
	if ticket != "" {
		id, err := st.ResolveTicket(ticket)
		if err == nil {
			err = requireTicket(st, id)
		}
		if err != nil {
			return renderErr(stdout, err)
		}
		// A ticket is read for its own sake: the blockers it is held on are asked
		// of the host now, not taken from what this machine remembers.
		n, err := nextForTicket(st, cfg, mp, id, distribution, *topology, asked, newMergeReader(e, st, cfg, mp, jigHome, 0))
		if err != nil {
			return renderErr(stdout, err)
		}
		n.setReviewer(e, verifydeliver.Deps{Store: st, Home: jigHome})
		out, blocks = n, n.blocks(*topology)
	} else {
		n, err := nextForStore(st, cfg, mp, newMergeReader(e, st, cfg, mp, jigHome, listingMergeAge))
		if err != nil {
			return renderErr(stdout, err)
		}
		out, blocks = n, n.blocks()
	}
	if *jsonOut {
		data, err := json.Marshal(out)
		if err != nil {
			return renderErr(stdout, err)
		}
		fmt.Fprintf(stdout, "%s\n", data)
		return 0
	}
	axi.Render(stdout, blocks...)
	return 0
}

// nextTicket is what `jig next <ticket>` says. Closed is how the ticket was
// closed (`jig close`), such as "covered by develdeco/jig#82" or "not planned",
// when it was.
type nextTicket struct {
	Ticket       string        `json:"ticket"`
	Title        string        `json:"title,omitempty"`
	State        string        `json:"state"`
	Closed       string        `json:"closed,omitempty"`
	Branch       string        `json:"branch"`
	Distribution string        `json:"distribution,omitempty"`
	BlockedBy    []nextBlocker `json:"blocked_by,omitempty"`
	// Stack is, for a ticket stacked on another, where its branch starts and
	// where its pull request goes in each repo it builds in (internal/stack).
	Stack     []nextStack `json:"stack,omitempty"`
	Ready     []nextBatch `json:"ready"`
	Unplaced  []string    `json:"unplaced_workspaces,omitempty"`
	Ambiguous []string    `json:"ambiguous_workspaces,omitempty"`
	Topology  []nextSlice `json:"topology,omitempty"`
	// Advice is how to share the ready slices out, and, when review is the
	// phase that can start, who reviews (Fresh says whether the calling
	// session may itself).
	Advice []string `json:"advice,omitempty"`
	// Fresh is "yes", "no" or "unknown": whether the calling session may
	// review the ticket itself, named only when review is ready and the
	// environment names a session.
	Fresh string `json:"fresh,omitempty"`
	// Phase is where the ticket stands (internal/phase), nil when it could
	// not be told (PhaseProblem says why). It is a block of its own in the
	// JSON because `ready` above is already the slices ready to build.
	Phase        *nextPhase `json:"phase,omitempty"`
	PhaseProblem string     `json:"phase_problem,omitempty"`

	// derived is the same derivation, for the help.
	derived *phase.Ticket
}

// nextPhase is the phase block of `jig next <ticket>`: the phase the ticket is
// at, whether it can start now (Code and Reason say why not), the prompt that
// starts it, each phase's state, and what --for asked.
type nextPhase struct {
	Name   string          `json:"name"`
	Ready  bool            `json:"ready"`
	Code   string          `json:"code,omitempty"`
	Reason string          `json:"reason,omitempty"`
	Prompt string          `json:"prompt,omitempty"`
	Phases []nextPhaseRow  `json:"phases"`
	For    *nextPhaseAsked `json:"for,omitempty"`
}

// nextPhaseRow is one phase's state and where it stands, in words.
type nextPhaseRow struct {
	Name  string `json:"name"`
	State string `json:"state"`
	Text  string `json:"text"`
}

// nextPhaseAsked is the answer to --for: whether that phase can run now, what
// it waits for if not, and the warning when it is done already.
type nextPhaseAsked struct {
	Phase   string `json:"phase"`
	Ready   bool   `json:"ready"`
	Code    string `json:"code,omitempty"`
	Reason  string `json:"reason,omitempty"`
	Warning string `json:"warning,omitempty"`
}

// parseAskedPhase is the phase --for names: "" for none, a VALIDATION_ERROR for
// a word that is no phase or for a flag with no ticket to ask about.
func parseAskedPhase(word, ticket string) (phase.Name, error) {
	if word == "" {
		return "", nil
	}
	p, ok := phase.Parse(word)
	switch {
	case !ok:
		return "", &axi.Error{Msg: fmt.Sprintf("--for %q is none of the phases", word), Code: "VALIDATION_ERROR", Help: []string{"The phases are brief, build, review and publish"}}
	case ticket == "":
		return "", &axi.Error{Msg: "--for asks about a ticket: name one", Code: "VALIDATION_ERROR"}
	}
	return p, nil
}

// setPhase reads where the ticket stands and lays it on n, with the answer to
// asked when --for named a phase. It is best-effort: a ticket whose phase
// cannot be derived (a gate round it cannot read, say) still gets the rest of
// `jig next`, and says why the phase is missing.
func (n *nextTicket) setPhase(st *store.Store, id string, asked phase.Name, merges mergeSource) {
	t, err := derivePhaseWith(st, id, merges)
	if err != nil {
		n.PhaseProblem = err.Error()
		return
	}
	n.derived = &t
	p := &nextPhase{Name: string(t.Current), Ready: t.Ready, Code: t.Code, Reason: t.Reason, Prompt: t.Prompt}
	for _, ph := range t.Phases {
		p.Phases = append(p.Phases, nextPhaseRow{Name: string(ph.Name), State: ph.State, Text: ph.Text})
	}
	if asked != "" {
		a := t.For(asked)
		p.For = &nextPhaseAsked{Phase: string(asked), Ready: a.Ready, Code: a.Code, Reason: a.Reason, Warning: a.Warning}
	}
	n.Phase = p
}

// setReviewer says who reviews the ticket when review is the phase that can
// start now: whether the calling session (envSession) may review it itself, and
// the advice for it (decision 39). It adds nothing when review is not ready, as
// while a round is open, whose reviewer is at work already, or when the
// environment names no session.
func (n *nextTicket) setReviewer(e env, d verifydeliver.Deps) {
	if n.derived == nil || n.derived.Current != phase.Review || !n.derived.Ready {
		return
	}
	if a, ok := sessionAdvice(e, d, n.Ticket); ok {
		n.Fresh, n.Advice = a.State, a.Lines
	}
}

// nextBlocker is a blocker that is not met, with what it is missing (Fact: it
// has not published, its PR is open, no merge is on record, ...) and, when the
// host's answer behind that is not from this run, the time it was read.
type nextBlocker struct {
	Ticket string `json:"ticket"`
	Kind   string `json:"kind,omitempty"`
	Fact   string `json:"fact,omitempty"`
	AsOf   string `json:"as_of,omitempty"`
}

// nextBatch is one repo's ready slices, in the order they build, and where the
// ticket's branch starts in that repo: its target branch on the origin. A
// batch of slices no repo owns has none.
type nextBatch struct {
	Repo   string   `json:"repo"`
	Slices []string `json:"slices"`
	Start  string   `json:"start,omitempty"`
}

type nextSlice struct {
	ID        string   `json:"id"`
	Workspace string   `json:"workspace"`
	Repo      string   `json:"repo"`
	State     string   `json:"state"`
	Wave      int      `json:"wave"`
	BlockedBy []string `json:"blocked_by"`
}

// ticketData is one ticket as readiness sees it, with its title. problem is
// why the ticket could not be read (its slices.yaml does not parse, say): such
// a ticket is still listed, with no slices, so one broken ticket never hides
// the rest of the store.
type ticketData struct {
	in      ready.Ticket
	title   string
	rec     store.Ticket
	problem string
}

// stateInvalid is the state `jig next` and `jig recall` give a ticket whose
// files cannot be read.
const stateInvalid = "invalid"

// readTicketDataOrProblem is readTicketData for a listing of many tickets: a
// ticket it cannot read comes back with the problem, no slices, and what its
// record and journal still say (its title, its blockers, whether it was
// published), so the tickets around it are classed as before.
func readTicketDataOrProblem(st *store.Store, id string) ticketData {
	d, err := readTicketData(st, id)
	if err == nil {
		return d
	}
	d = ticketData{in: ready.Ticket{ID: id}, problem: err.Error()}
	if published, perr := isPublished(st, id); perr == nil {
		d.in.Published = published
	}
	if rec, rerr := st.ReadTicket(id); rerr == nil {
		d.title, d.rec = rec.Title, rec
		d.in.Blockers, d.in.Closed = ticketBlockers(st, rec), rec.Closed
	}
	return d
}

// ticketBlockers are the tickets rec's record says block it, each resolved
// through aliases when it can be.
func ticketBlockers(st *store.Store, rec store.Ticket) []ready.Blocker {
	var out []ready.Blocker
	for _, b := range rec.BlockedBy {
		blocker := b.Ticket
		if resolved, err := st.ResolveTicket(b.Ticket); err == nil {
			blocker = resolved
		}
		out = append(out, ready.Blocker{Ticket: blocker, Kind: b.Kind})
	}
	return out
}

// readTicketData reads what the readiness of ticket id depends on.
func readTicketData(st *store.Store, id string) (ticketData, error) {
	rec, err := st.ReadTicket(id)
	if err != nil {
		return ticketData{}, err
	}
	slices, err := st.ReadSlices(id)
	if err != nil {
		return ticketData{}, err
	}
	states := make(map[string]store.SliceState, len(slices))
	for _, s := range slices {
		if states[s.ID], err = st.ReadSliceState(id, s.ID); err != nil {
			return ticketData{}, err
		}
	}
	published, err := isPublished(st, id)
	if err != nil {
		return ticketData{}, err
	}
	in := ready.Ticket{ID: id, Published: published, Closed: rec.Closed, Slices: slices, States: states, Blockers: ticketBlockers(st, rec)}
	return ticketData{in: in, title: rec.Title, rec: rec}, nil
}

// blockerInput is blocker id as readiness sees it from a ticket that waits for
// it: whether it published and whether it was closed.
func blockerInput(st *store.Store, id string) (ready.Ticket, error) {
	published, err := isPublished(st, id)
	if err != nil {
		return ready.Ticket{}, err
	}
	rec, err := st.ReadTicket(id)
	if err != nil {
		return ready.Ticket{}, err
	}
	return ready.Ticket{ID: id, Published: published, Closed: rec.Closed}, nil
}

// isPublished reports whether ticket's journal has a publish-done line.
func isPublished(st *store.Store, id string) (bool, error) {
	lines, err := journal.Read(st, id)
	if err != nil {
		return false, err
	}
	for _, l := range lines {
		if l.Event == journal.EventPublishDone {
			return true, nil
		}
	}
	return false, nil
}

// nextForTicket works out what is ready to build on ticket id, and which phase
// it is at; asked is the phase --for named, "" for none.
func nextForTicket(st *store.Store, cfg project.Config, mp project.MachineProject, id, distribution string, topology bool, asked phase.Name, merges mergeSource) (nextTicket, error) {
	data, err := readTicketData(st, id)
	if err != nil {
		return nextTicket{}, err
	}
	place := ready.Place(ready.ProjectRepos(cfg, mp.Clones), data.in.Slices)
	// The branch is cut in every repo, so it must not be the target of any.
	var branch string
	for _, target := range projectTargets(cfg) {
		if branch, err = st.ResolveTicketBranch(id, data.rec, target); err != nil {
			return nextTicket{}, err
		}
	}

	// Its blockers' own journals say whether they published, and merges whether
	// their work has merged.
	inputs := []ready.Ticket{data.in}
	for _, b := range data.in.Blockers {
		in, err := blockerInput(st, b.Ticket)
		if err != nil {
			return nextTicket{}, err
		}
		inputs = append(inputs, in)
	}
	readMerges(merges, inputs)
	status := ready.Tickets(inputs)[0]
	// A stacked ticket starts from its parent's branch where they share a
	// repo, and its pull request goes there, until the parent merges.
	plan, err := stack.Read(stackInput(st, cfg, mp, merges), id)
	if err != nil {
		return nextTicket{}, err
	}

	n := nextTicket{
		Ticket: id, Title: data.title, State: status.State, Branch: branch, Distribution: distribution,
		Ready: []nextBatch{}, Unplaced: place.Unplaced, Ambiguous: place.Ambiguous,
	}
	if data.rec.Closed != nil {
		n.Closed = data.rec.Closed.Phrase()
	}
	for _, b := range status.Blocked {
		n.BlockedBy = append(n.BlockedBy, blockerFact(b, status.Why[b.Ticket], inputs))
	}
	isReady := map[string]bool{}
	for _, id := range status.Ready {
		isReady[id] = true
	}
	var readySlices []store.Slice
	for _, s := range data.in.Slices {
		if isReady[s.ID] {
			readySlices = append(readySlices, s)
		}
	}
	n.Stack = stackRows(plan)
	for _, b := range ready.Batches(readySlices, place.Repo) {
		n.Ready = append(n.Ready, nextBatch{Repo: b.Repo, Slices: b.Slices, Start: startPoint(cfg, plan, b.Repo)})
	}
	n.Advice = distributionAdvice(n.Ready)
	n.setPhase(st, id, asked, merges)
	if topology {
		waves := ready.Waves(data.in.Slices)
		for _, s := range data.in.Slices {
			blockedBy := s.BlockedBy
			if blockedBy == nil {
				blockedBy = []string{}
			}
			n.Topology = append(n.Topology, nextSlice{
				ID: s.ID, Workspace: s.Workspace, Repo: place.Repo[s.Workspace],
				State: data.in.States[s.ID].State, Wave: waves[s.ID], BlockedBy: blockedBy,
			})
		}
	}
	return n, nil
}

// startPoint is where the ticket's branch starts in repo: the branch of the
// parent it is stacked on there (plan), else the target branch of the repo on
// its origin. It is "" for a repo the project does not have (the name slices
// no repo owns are grouped under).
func startPoint(cfg project.Config, plan stack.Plan, repo string) string {
	if r, ok := plan.In(repo); ok && r.StartFrom != "" {
		return r.StartFrom
	}
	for _, r := range cfg.Repos {
		if r.Name() == repo {
			return "origin/" + r.TargetBranch()
		}
	}
	return ""
}

// distributionAdvice says how the repos with ready slices can be shared out:
// a repo's slices build one after the other, but repos are independent. Slices
// that no repo owns yet (ready.Unplaced) are no repo's to share out.
func distributionAdvice(all []nextBatch) []string {
	var batches []nextBatch
	for _, b := range all {
		if b.Repo != ready.Unplaced {
			batches = append(batches, b)
		}
	}
	switch len(batches) {
	case 0:
		return nil
	case 1:
		b := batches[0]
		return []string{fmt.Sprintf("All ready slices build in %s: one session takes them in order (%s)", b.Repo, strings.Join(b.Slices, ", "))}
	}
	repos := make([]string, len(batches))
	for i, b := range batches {
		repos[i] = b.Repo
	}
	return []string{
		fmt.Sprintf("%d repos have ready slices (%s): one session per repo can build them in parallel", len(batches), strings.Join(repos, ", ")),
		"Within a repo the slices build one after the other, in the order listed",
	}
}

func (n nextTicket) blocks(topology bool) []string {
	pairs := [][2]string{{"ticket", n.Ticket}}
	if n.Title != "" {
		pairs = append(pairs, [2]string{"title", n.Title})
	}
	pairs = append(pairs, [2]string{"state", n.State})
	if n.Closed != "" {
		pairs = append(pairs, [2]string{"closed", n.Closed})
	}
	pairs = append(pairs, [2]string{"branch", n.Branch})
	if n.Distribution != "" {
		pairs = append(pairs, [2]string{"distribution", n.Distribution})
	}
	pairs = append(pairs, n.phasePairs()...)
	if n.Fresh != "" {
		pairs = append(pairs, [2]string{"fresh", n.Fresh})
	}
	blocks := []string{axi.KV("next", pairs)}
	if len(n.BlockedBy) > 0 {
		var rows [][]string
		for _, b := range n.BlockedBy {
			rows = append(rows, []string{b.Ticket, b.Kind, b.Fact})
		}
		blocks = append(blocks, axi.Table("blocked_by", []string{"ticket", "kind", "fact"}, rows))
	}
	blocks = append(blocks, stackBlocks(n.Ticket, n.Stack)...)
	if len(n.Ready) == 0 {
		blocks = append(blocks, "ready: none")
	} else {
		var rows [][]string
		for _, b := range n.Ready {
			start := b.Start
			if start == "" {
				start = "-"
			}
			rows = append(rows, []string{b.Repo, joinPlus(b.Slices), start})
		}
		blocks = append(blocks, axi.Table("ready", []string{"repo", "slices", "start"}, rows))
	}
	if len(n.Unplaced) > 0 {
		blocks = append(blocks, "note: no repo declares workspace(s) "+strings.Join(n.Unplaced, ", ")+": their slices are grouped under ?")
	}
	if len(n.Ambiguous) > 0 {
		blocks = append(blocks, "note: more than one repo declares workspace(s) "+strings.Join(n.Ambiguous, ", ")+": name the repo in the slice, as <repo>/<workspace>; their slices are grouped under ?")
	}
	if n.PhaseProblem != "" {
		blocks = append(blocks, "note: the phase of "+n.Ticket+" could not be told: "+n.PhaseProblem)
	}
	if topology {
		var rows [][]string
		for _, s := range n.Topology {
			blocked := "-"
			if len(s.BlockedBy) > 0 {
				blocked = joinPlus(s.BlockedBy)
			}
			rows = append(rows, []string{s.ID, s.Workspace, s.Repo, s.State, fmt.Sprint(s.Wave), blocked})
		}
		blocks = append(blocks, axi.Table("topology", []string{"slice", "workspace", "repo", "state", "wave", "blocked_by"}, rows))
	}
	return append(blocks, axi.Help(n.help()...))
}

// phasePairs are the lines `jig next <ticket>` adds to its next block: the
// phase the ticket is at, whether it can start now, the prompt that starts it,
// why not when it cannot, and the answer to --for.
func (n nextTicket) phasePairs() [][2]string {
	p := n.Phase
	if p == nil {
		return nil
	}
	pairs := [][2]string{{"phase", p.Name}, {"ready", yesNo(p.Ready)}}
	add := func(k, v string) {
		if v != "" {
			pairs = append(pairs, [2]string{k, v})
		}
	}
	add("prompt", p.Prompt)
	if !p.Ready {
		add("code", p.Code)
		add("reason", p.Reason)
	}
	if f := p.For; f != nil {
		pairs = append(pairs, [2]string{"for", f.Phase}, [2]string{"for_ready", yesNo(f.Ready)})
		add("for_code", f.Code)
		add("for_reason", f.Reason)
		add("warning", f.Warning)
	}
	return pairs
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

// help is the next step for the ticket, after the advice on sharing it out.
// The phase the ticket is at decides what a ready or green ticket is told;
// every line that sends a person to a phase names its prompt.
func (n nextTicket) help() []string {
	t := n.derived
	prompt := ""
	if t != nil {
		prompt = promptHint(t.Prompt)
	}
	switch n.State {
	case ready.StateClosed:
		return []string{fmt.Sprintf("%s is closed (%s): run `jig reopen %s` to work it again, or `jig next` to see a ticket that is ready", n.Ticket, n.Closed, n.Ticket)}
	case ready.StateDone:
		return []string{n.Ticket + " is published: run `jig next` to see the tickets that are left"}
	case ready.StateBlocked:
		if t != nil && t.Code != phase.CodeBlocked {
			prompt = ""
		}
		return []string{fmt.Sprintf("%s waits for %s: %s; run `jig next` to see a ticket that is ready%s", n.Ticket, n.BlockedBy[0].Ticket, n.BlockedBy[0].Fact, prompt)}
	case ready.StateNew:
		return []string{fmt.Sprintf("%s has no slices yet: draft its brief and slices with the intake skill, checking them with `jig validate %s --check`, and run `jig validate %s` once the person has confirmed the brief%s",
			n.Ticket, n.Ticket, n.Ticket, promptHint(phase.Prompt(phase.Brief, n.Ticket)))}
	case ready.StateGreen:
		if t != nil {
			return append([]string{"Every slice of " + n.Ticket + " is green. " + phaseAdvice(*t, n.Ticket)}, n.Advice...)
		}
		return []string{fmt.Sprintf("Every slice of %s is green: run `jig gate %s`, then `jig publish %s`", n.Ticket, n.Ticket, n.Ticket)}
	case ready.StateActive:
		return []string{fmt.Sprintf("Nothing is ready: work is in flight or parked; run `jig status %s` for the state%s", n.Ticket, prompt)}
	}
	var help []string
	if t != nil && (t.Current == phase.Brief || t.Code == phase.CodeBlocked) {
		// The slices are ready, but the phase says what comes first.
		help = append(help, phaseAdvice(*t, n.Ticket))
		prompt = ""
	}
	help = append(help, n.Advice...)
	return append(help, fmt.Sprintf("Work on branch %s in each repo (%s where it does not exist), then run `jig here` to confirm%s", n.Branch, n.switchAdvice(), prompt))
}

// switchAdvice names the command that creates the ticket's branch in a repo,
// from the start point of the ready slices when all of them share one, else
// from the start of each repo's row in the ready table.
func (n nextTicket) switchAdvice() string {
	cmd := "git switch -c " + n.Branch
	start := ""
	for _, b := range n.Ready {
		switch {
		case b.Start == "":
		case start == "":
			start = b.Start
		case start != b.Start:
			return fmt.Sprintf("`%s <start>`, with the start of its row in `ready`,", cmd)
		}
	}
	if start != "" {
		cmd += " " + start
	}
	return "`" + cmd + "`"
}

// nextStore is what `jig next` says without a ticket.
type nextStore struct {
	// Tickets are every ticket of the store, in id order.
	Tickets []nextRow `json:"tickets"`
	// Next are the ids of the ready tickets in the order to start them: by
	// rank, lowest first, then by id, the unranked last.
	Next []string `json:"next"`
	// SharedRanks are the ranks that more than one ready ticket holds.
	SharedRanks []nextSharedRank `json:"shared_ranks,omitempty"`
	// WorkspaceOverlaps are the workspaces that two ready tickets both build
	// in, one entry for each pair and workspace.
	WorkspaceOverlaps []nextWorkspaceOverlap `json:"workspace_overlaps,omitempty"`
	Overlaps          []nextOverlap          `json:"overlaps,omitempty"`
}

// nextRow is one ticket of the listing; Closed is how a closed ticket was
// closed ("covered by develdeco/jig#82").
type nextRow struct {
	ID     string `json:"id"`
	Title  string `json:"title,omitempty"`
	State  string `json:"state"`
	Closed string `json:"closed,omitempty"`
	// Rank is the ticket's rank (ticket.yaml), absent when it has none.
	Rank      *int          `json:"rank,omitempty"`
	BlockedBy []nextBlocker `json:"blocked_by,omitempty"`
	Ready     []string      `json:"ready"`
	// Problem is why an invalid ticket could not be read.
	Problem string `json:"problem,omitempty"`
}

// nextSharedRank is a rank that more than one ready ticket holds, so which
// of them to start first is not the owner's word.
type nextSharedRank struct {
	Rank    int      `json:"rank"`
	Tickets []string `json:"tickets"`
}

// nextWorkspaceOverlap is a workspace that two ready tickets both build in:
// run together, their slices change the same files.
type nextWorkspaceOverlap struct {
	Repo      string   `json:"repo"`
	Workspace string   `json:"workspace"`
	Tickets   []string `json:"tickets"`
}

// nextOverlap is a file that local branches of different tickets both change.
type nextOverlap struct {
	Repo     string   `json:"repo"`
	File     string   `json:"file"`
	Tickets  []string `json:"tickets"`
	Branches []string `json:"branches"`
}

// nextForStore lists the tickets of the store in order with what blocks each,
// and the files local ticket branches fight over.
func nextForStore(st *store.Store, cfg project.Config, mp project.MachineProject, merges mergeSource) (nextStore, error) {
	ids, err := st.Tickets()
	if err != nil {
		return nextStore{}, err
	}
	datas := make([]ticketData, len(ids))
	inputs := make([]ready.Ticket, len(ids))
	for i, id := range ids {
		datas[i] = readTicketDataOrProblem(st, id)
		inputs[i] = datas[i].in
	}
	readMerges(merges, inputs)
	out := nextStore{Tickets: []nextRow{}, Next: []string{}}
	// The ready tickets in id order, with their ranks and slices.
	var ranked []ready.Ranked
	slicesOf := map[string][]store.Slice{}
	for i, s := range ready.Tickets(inputs) {
		row := nextRow{ID: s.ID, Title: datas[i].title, State: s.State, Rank: datas[i].rec.Rank, Ready: s.Ready}
		if c := datas[i].rec.Closed; c != nil {
			row.Closed = c.Phrase()
		}
		if row.Ready == nil {
			row.Ready = []string{}
		}
		for _, b := range s.Blocked {
			row.BlockedBy = append(row.BlockedBy, blockerFact(b, s.Why[b.Ticket], inputs))
		}
		if datas[i].problem != "" {
			row.State, row.Problem, row.Ready = stateInvalid, datas[i].problem, []string{}
			out.Tickets = append(out.Tickets, row)
			continue
		}
		out.Tickets = append(out.Tickets, row)
		if s.State == ready.StateReady {
			ranked = append(ranked, ready.Ranked{ID: s.ID, Rank: datas[i].rec.Rank})
			slicesOf[s.ID] = datas[i].in.Slices
		}
	}
	out.Next = ready.ByPriority(ranked)
	for _, s := range ready.SharedRanks(ranked) {
		out.SharedRanks = append(out.SharedRanks, nextSharedRank{Rank: s.Rank, Tickets: s.Tickets})
	}
	out.WorkspaceOverlaps = workspaceOverlaps(cfg, mp, out.Next, slicesOf)
	out.Overlaps = findOverlaps(st, cfg, mp)
	return out, nil
}

// workspaceOverlaps names, for each pair of the ready tickets, the workspaces
// both build in: the sites of all their slices, placed in the repos of the
// project as `jig next <ticket>` places them. The tickets come in the order to
// start them, so a pair names the one to start first before the other. A
// ticket's workspace that no repo owns is no site (it is not known whose it
// is), and it overlaps nothing.
func workspaceOverlaps(cfg project.Config, mp project.MachineProject, order []string, slicesOf map[string][]store.Slice) []nextWorkspaceOverlap {
	if len(order) < 2 {
		return nil
	}
	repos := ready.ProjectRepos(cfg, mp.Clones)
	footprints := make([]ready.Footprint, len(order))
	for i, id := range order {
		footprints[i] = ready.Footprint{Ticket: id, Sites: ready.Sites(slicesOf[id], ready.Place(repos, slicesOf[id]))}
	}
	var out []nextWorkspaceOverlap
	for _, o := range ready.Overlaps(footprints) {
		out = append(out, nextWorkspaceOverlap{Repo: o.Repo, Workspace: o.Workspace, Tickets: o.Tickets[:]})
	}
	return out
}

func (n nextStore) blocks() []string {
	if len(n.Tickets) == 0 {
		return []string{"tickets: none", axi.Help("Run `jig ticket new --title \"...\"` to mint the first ticket")}
	}
	var rows [][]string
	for _, t := range n.Tickets {
		blocked, readySlices := "-", "-"
		if len(t.BlockedBy) > 0 {
			bs := make([]ready.Blocker, len(t.BlockedBy))
			for i, b := range t.BlockedBy {
				bs[i] = ready.Blocker{Ticket: b.Ticket, Kind: b.Kind}
			}
			blocked = ready.JoinBlockers(bs)
		}
		if len(t.Ready) > 0 {
			readySlices = joinPlus(t.Ready)
		}
		rows = append(rows, []string{t.ID, t.Title, t.State, blocked, readySlices})
	}
	blocks := []string{axi.Table("tickets", []string{"id", "title", "state", "blocked_by", "ready"}, rows)}
	// The ready tickets in the order to start them, with the rank each holds.
	if len(n.Next) > 0 {
		byID := map[string]nextRow{}
		for _, t := range n.Tickets {
			byID[t.ID] = t
		}
		var order [][]string
		for _, id := range n.Next {
			rank := "-"
			if r := byID[id].Rank; r != nil {
				rank = strconv.Itoa(*r)
			}
			order = append(order, []string{id, rank, byID[id].Title})
		}
		blocks = append(blocks, axi.Table("next", []string{"ticket", "rank", "title"}, order))
	}
	for _, s := range n.SharedRanks {
		blocks = append(blocks, fmt.Sprintf("warning: rank %d is shared by %s: they are ordered by id", s.Rank, andList(s.Tickets)))
	}
	if len(n.WorkspaceOverlaps) > 0 {
		var wrows [][]string
		for _, o := range n.WorkspaceOverlaps {
			wrows = append(wrows, []string{o.Repo, o.Workspace, joinPlus(o.Tickets)})
		}
		blocks = append(blocks, "warning: ready tickets build in the same workspace",
			axi.Table("workspace_overlap", []string{"repo", "workspace", "tickets"}, wrows))
	}
	// What each blocker that is not met is missing, so the table above (which
	// names who waits for whom) need not.
	var waiting [][]string
	for _, t := range n.Tickets {
		for _, b := range t.BlockedBy {
			waiting = append(waiting, []string{t.ID, b.Ticket, b.Fact})
		}
	}
	if len(waiting) > 0 {
		blocks = append(blocks, axi.Table("waiting", []string{"ticket", "blocker", "fact"}, waiting))
	}
	var invalid [][]string
	for _, t := range n.Tickets {
		if t.Problem != "" {
			invalid = append(invalid, []string{t.ID, t.Problem})
		}
	}
	if len(invalid) > 0 {
		blocks = append(blocks, axi.Table("invalid", []string{"id", "problem"}, invalid))
	}
	if len(n.Overlaps) > 0 {
		var orows [][]string
		for _, o := range n.Overlaps {
			orows = append(orows, []string{o.Repo, o.File, joinPlus(o.Tickets)})
		}
		blocks = append(blocks, "warning: local branches of different tickets change the same files",
			axi.Table("overlap", []string{"repo", "file", "tickets"}, orows))
	}
	var help []string
	switch {
	case len(n.Next) > 0:
		help = append(help, fmt.Sprintf("Run `jig next %s` to see what is ready to build there", n.Next[0]))
	default:
		help = append(help, "No ticket has a slice ready: run `jig next <ticket>` for one that is in flight, or `jig status <ticket>`")
	}
	if len(n.WorkspaceOverlaps) > 0 {
		help = append(help, "Do not run tickets that share a workspace (`workspace_overlap`) at once: start the one that comes first in `next`, and the other after it")
	}
	if len(n.Overlaps) > 0 {
		help = append(help, "Build overlapping tickets one after the other, or settle who changes those files first")
	}
	if n.unrankedAmongMany() {
		help = append(help, "Order the ready tickets with `jig rank <ticket> <n>`: the lowest rank starts first")
	}
	if len(invalid) > 0 {
		help = append(help, fmt.Sprintf("Fix what the problem of %s says (its slices.yaml or a slice's state), then check it with `jig validate %s --check`", invalid[0][0], invalid[0][0]))
	}
	return append(blocks, axi.Help(help...))
}

// unrankedAmongMany reports whether there are several ready tickets and one of
// them has no rank: the order the person has not given.
func (n nextStore) unrankedAmongMany() bool {
	if len(n.Next) < 2 {
		return false
	}
	for _, t := range n.Tickets {
		if t.Rank == nil && slices.Contains(n.Next, t.ID) {
			return true
		}
	}
	return false
}

// andList joins words as prose: "a", "a and b", "a, b and c".
func andList(words []string) string {
	if len(words) < 2 {
		return strings.Join(words, "")
	}
	return strings.Join(words[:len(words)-1], ", ") + " and " + words[len(words)-1]
}

// findOverlaps looks in each repo's mapped clone for local branches that bind
// a ticket, and reports every file that branches of two different tickets
// both change since the repo's target branch (git diff --name-only
// <target>...<branch>). A repo with no clone here, or whose target is not
// there, is skipped: this is a warning, not a gate.
func findOverlaps(st *store.Store, cfg project.Config, mp project.MachineProject) []nextOverlap {
	var out []nextOverlap
	for _, r := range cfg.Repos {
		dir := mp.Clones[r.Name()]
		if dir == "" {
			continue
		}
		if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
			continue
		}
		target := r.TargetBranch()
		listing, err := gitx.Run(dir, "for-each-ref", "--format=%(refname:short)", "refs/heads")
		if err != nil || listing == "" {
			continue
		}
		type bound struct{ ticket, branch string }
		var bounds []bound
		tickets := map[string]bool{}
		for _, b := range strings.Split(listing, "\n") {
			b = strings.TrimSpace(b)
			if b == "" || b == target {
				continue
			}
			if id, _, err := st.TicketForBranch(b); err == nil && id != "" {
				bounds = append(bounds, bound{id, b})
				tickets[id] = true
			}
		}
		if len(tickets) < 2 {
			continue
		}
		base := ""
		for _, ref := range []string{target, "origin/" + target} {
			if _, found, _ := gitx.ResolveCommit(dir, ref); found {
				base = ref
				break
			}
		}
		if base == "" {
			continue
		}
		type touch struct {
			tickets, branches map[string]bool
		}
		files := map[string]*touch{}
		for _, b := range bounds {
			mb, err := gitx.MergeBase(dir, base, b.branch)
			if err != nil {
				continue
			}
			names, err := gitx.DiffNameOnly(dir, mb, b.branch, "ACDMRTUXB")
			if err != nil {
				continue
			}
			for _, f := range names {
				t := files[f]
				if t == nil {
					t = &touch{tickets: map[string]bool{}, branches: map[string]bool{}}
					files[f] = t
				}
				t.tickets[b.ticket] = true
				t.branches[b.branch] = true
			}
		}
		for _, f := range sortedStrings(mapKeys(files)) {
			t := files[f]
			if len(t.tickets) < 2 {
				continue
			}
			out = append(out, nextOverlap{Repo: r.Name(), File: f, Tickets: sortedStrings(mapKeys(t.tickets)), Branches: sortedStrings(mapKeys(t.branches))})
		}
	}
	return out
}

func mapKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

func sortedStrings(s []string) []string {
	sort.Strings(s)
	return s
}
