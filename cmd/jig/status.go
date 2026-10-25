package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/project"
	"github.com/develdeco/jig/internal/store"
	"github.com/develdeco/jig/internal/verifydeliver"
)

// init registers this file's verb with the registry (registry.go).
func init() {
	registerVerb(verb{
		Spec: cmdSpec{"status", "print a ticket's slice and question state", []flagSpec{
			{"store", "explicit store path", false},
			{"project", "project name, resolved via the machine mapping", false},
		}, false},
		Order: 180,
		Run: func(e env, args []string, stdout io.Writer, _ io.Reader) int {
			return cmdStatus(e, args, stdout)
		},
		Probe: []string{"T-1"},
	})
}

// cmdStatus implements `jig status [<ticket>]`.
func cmdStatus(e env, args []string, stdout io.Writer) int {
	fs := newFlagSet(e, "status")
	storeFlag := fs.String("store", "", "explicit store path")
	projectFlag := fs.String("project", "", "project name, resolved via the machine mapping")
	ticket, rest, err := requirePositional(args, "ticket")
	if err != nil {
		return renderErr(stdout, err)
	}
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

	out, err := RenderStatus(st, ticket)
	if err != nil {
		return renderErr(stdout, err)
	}
	fmt.Fprint(stdout, out)
	return 0
}

// RenderStatus renders the exact `jig status` golden format for ticket:
// ticket/state lines, a slices table (a slice's id, state, attempts,
// blocked_by, question and goal, then the workspace and oracle it names as
// slices.yaml has them and, for a fix slice, the round it came from), a
// questions table (or "questions:
// none"), a parked table (present only while a slice is needs-input) and a
// stalled table (present only while a slice is stalled), the blocks of every
// registered status section (registerStatusSection), and a contextual help
// hint. A ticket that adopted a branch names it on a branch line under the
// ticket's. The parked and stalled tables are the custody surface: they make
// "awaiting a human" (parked) and "stuck" (stalled) unmistakable and distinct
// from each other and from ordinary in-progress work.
func RenderStatus(st *store.Store, ticket string) (string, error) {
	return renderStatus(st, ticket, statusSections())
}

// renderStatus is RenderStatus over the given sections: each one's blocks
// follow the status blocks and come before the help line.
func renderStatus(st *store.Store, ticket string, sections []statusSection) (string, error) {
	slices, err := st.ReadSlices(ticket)
	if err != nil {
		return "", err
	}
	questions, err := st.ReadQuestions(ticket)
	if err != nil {
		return "", err
	}
	rec, err := st.ReadTicket(ticket)
	if err != nil {
		return "", err
	}
	adopted := rec.Adopted()

	cfg, err := project.LoadStoreConfig(st.Root)
	if err != nil {
		return "", err
	}
	fixRounds := *cfg.ResolvedGateConfig().FixRounds
	gateRounds, _, _, err := latestGateRound(st, ticket)
	if err != nil {
		return "", err
	}
	budgetUsed, _ := verifydeliver.UsedFixBudget(st, ticket, gateRounds+1)

	var rows, parkedRows, stalledRows [][]string
	counts := map[string]int{}
	// A ticket that adopted a branch has no slices until a gate round queues
	// its fixes, and nothing built is not something building: every slice
	// it has is green, however few.
	allGreen := len(slices) > 0 || adopted
	for _, sl := range slices {
		ss, err := st.ReadSliceState(ticket, sl.ID)
		if err != nil {
			return "", err
		}
		counts[ss.State]++
		if ss.State != "green" {
			allGreen = false
		}
		blocked := "-"
		if len(sl.BlockedBy) > 0 {
			blocked = joinPlus(sl.BlockedBy)
		}
		question := "-"
		if ss.Question != "" {
			question = ss.Question
		}
		goal := sl.Goal
		if goal == "" {
			goal = "-"
		}
		fromGate := "-"
		if sl.FromGate > 0 {
			fromGate = strconv.Itoa(sl.FromGate)
		}
		rows = append(rows, []string{sl.ID, ss.State, strconv.Itoa(ss.Attempts), blocked, question, goal, dashed(sl.Workspace), dashed(sl.Oracle), fromGate})

		if ss.State == "needs-input" {
			parkedRows = append(parkedRows, []string{sl.ID, ss.Question, resumeCell(ticket, sl, ss)})
		}
		if ss.State == "stalled" {
			summary := ss.StallSummary
			if summary == "" {
				summary = "-"
			}
			stalledRows = append(stalledRows, []string{sl.ID, ss.Reason, summary})
		}
	}

	// Precedence: a stalled slice is worse than one merely parked awaiting a
	// human, which is worse than one blocked on its env coming up, which
	// beats "green" only once every slice is; "building" is the fallback.
	overall := "building"
	switch {
	case rec.Closed != nil:
		overall = "closed"
	case counts["stalled"] > 0:
		overall = "stalled"
	case counts["needs-input"] > 0:
		overall = "parked"
	case counts["env-blocked"] > 0:
		overall = "env-blocked"
	case allGreen:
		overall = "green"
	}

	blocks := []string{"ticket: " + ticket}
	if adopted {
		blocks = append(blocks, "branch: "+rec.Branch)
	}
	blocks = append(blocks, "state: "+overall)
	if c := rec.Closed; c != nil {
		pairs := [][2]string{{"reason", c.Reason}}
		if c.By != "" {
			pairs = append(pairs, [2]string{"by", c.By})
		}
		if c.Note != "" {
			pairs = append(pairs, [2]string{"note", c.Note})
		}
		if c.At != "" {
			pairs = append(pairs, [2]string{"at", c.At})
		}
		blocks = append(blocks, axi.KV("closed", pairs))
	}
	// fix_budget: once the ticket has a gate round, how much of its fix
	// budget (gate.fix_rounds) is used - the count of its earlier rounds
	// that appended at least one fix slice - ending in ", reached" exactly
	// when the next round would park rather than queue.
	if gateRounds > 0 {
		budgetLine := fmt.Sprintf("fix_budget: %d of %d rounds used", budgetUsed, fixRounds)
		if budgetUsed >= fixRounds {
			budgetLine += ", reached"
		}
		blocks = append(blocks, budgetLine)
	}
	blocks = append(blocks, axi.Table("slices", []string{"id", "state", "attempts", "blocked_by", "question", "goal", "workspace", "oracle", "from_gate"}, rows))
	if len(questions) == 0 {
		blocks = append(blocks, "questions: none")
	} else {
		var qrows [][]string
		for _, q := range questions {
			qrows = append(qrows, []string{q.ID, q.Slice, q.Status})
		}
		blocks = append(blocks, axi.Table("questions", []string{"id", "slice", "status"}, qrows))
	}
	if len(parkedRows) > 0 {
		blocks = append(blocks, axi.Table("parked", []string{"slice", "question", "resume"}, parkedRows))
	}
	if len(stalledRows) > 0 {
		blocks = append(blocks, axi.Table("stalled", []string{"slice", "reason", "summary"}, stalledRows))
	}

	// A gate round whose bookkeeping cannot be read is named, along with
	// what that costs: status would otherwise be quietly missing a waiting
	// decision, which is exactly what the outstanding-asks block exists to
	// show, and when the unreadable round is the only one there is nothing
	// left in that block to hint at the gap. Hence the note line beside
	// the table below.
	outstanding, unreadableRounds, err := verifydeliver.OutstandingAsks(st, ticket)
	if err != nil {
		return "", err
	}
	// The findings files are the ones a gate round itself must read, so an
	// unreadable one is also what stops the next round; keep that list
	// apart from the report file below, which only status reads, so the
	// help can name the repair without claiming a refusal that would not
	// happen.
	blockedRounds := append([]int(nil), unreadableRounds...)
	// A round's report.yaml is the other file status reads per round, and
	// it fails the same way, so it joins the same table.
	if n, _, reportUnreadable, lerr := latestGateRound(st, ticket); lerr == nil && reportUnreadable && n > 0 {
		found := false
		for _, r := range unreadableRounds {
			if r == n {
				found = true
			}
		}
		if !found {
			unreadableRounds = append(unreadableRounds, n)
			sort.Ints(unreadableRounds)
		}
	}
	if len(unreadableRounds) > 0 {
		rows := make([][]string, 0, len(unreadableRounds))
		for _, r := range unreadableRounds {
			rows = append(rows, []string{strconv.Itoa(r)})
		}
		// A table, like every other block, so the number before the colon
		// is a count and the round numbers are rows - a bare
		// "unreadable_gate_rounds: 2" reads as two rounds, not round two.
		blocks = append(blocks, "note: asks recorded by an unreadable round are not listed")
		blocks = append(blocks, axi.Table("unreadable_gate_rounds", []string{"round"}, rows))
	}
	var asks askCounts
	if len(outstanding) > 0 {
		// A decision `jig triage` journaled is pending until the next round
		// closes and applies it: the ask stays listed, with the decision.
		decisions, err := verifydeliver.ReadTriageDecisions(st, ticket)
		if err != nil {
			return "", err
		}
		var askRows [][]string
		for _, f := range outstanding {
			d := decisions[f.ID]
			decision, decidedBy := d.Label(f), ""
			if decision != "" {
				decidedBy = d.Provenance
			}
			askRows = append(askRows, []string{f.ID, f.Risk, fileLine(f), f.Title, f.RoutedWhy, decision, decidedBy})
			asks.add(f, d)
		}
		blocks = append(blocks, axi.Table("outstanding_asks", []string{"id", "risk", "file:line", "title", "why", "decision", "decided_by"}, askRows))
	}

	// The help names one way forward, in this order of precedence: a
	// round jig cannot read blocks everything, so the repair comes first;
	// otherwise what decides an outstanding ask is `jig triage <ticket>
	// <id>`, and the next `jig gate <ticket>` applies it, named once here
	// rather than in a per-row cell because it is the same command for
	// every ask. The frontier check refuses that
	// gate while any slice is short of green, and a round that queues fix
	// slices and leaves an ask undecided is the ordinary case, so while
	// the frontier is not green the ticket's own next step comes first and
	// the gate is named as the step after it - the order `jig gate`'s own
	// report hint prints.
	var helpLines []string
	if rec.Closed != nil {
		// A closed ticket is not worked: the way forward is to take it back.
		helpLines = []string{fmt.Sprintf("%s is closed (%s): run `jig reopen %s` to work it again", ticket, rec.Closed.Phrase(), ticket)}
	} else if len(blockedRounds) > 0 {
		// A gate round folds every earlier round's findings file, so it
		// refuses outright while one cannot be read. Naming the next
		// command here would name one that cannot run; the step is the
		// repair, and the file is in the store beside this ticket.
		rounds := make([]string, 0, len(blockedRounds))
		for _, r := range blockedRounds {
			rounds = append(rounds, "round-"+strconv.Itoa(r))
		}
		helpLines = []string{fmt.Sprintf(
			"Repair or remove %s/gate/{%s}/findings.yaml in the store: `jig gate` cannot open a round until it reads them",
			ticket, strings.Join(rounds, ","))}
	} else if len(outstanding) > 0 && frontierGreen(st, ticket) {
		helpLines = []string{"Run " + outstandingAsksHelpLine(ticket, asks, budgetUsed, fixRounds)}
	} else if len(slices) == 0 && !adopted {
		// A ticket with nothing to work has the two ways to get work that
		// `jig ticket new` and the refusal of `jig claim` give.
		helpLines = getWorkHints(ticket)
	} else {
		hint, err := nextStepHint(st, ticket)
		if err != nil {
			return "", err
		}
		helpLines = []string{hint}
		if len(outstanding) > 0 {
			helpLines = append(helpLines, "Then run "+outstandingAsksHelpLine(ticket, asks, budgetUsed, fixRounds))
		}
	}
	for _, sec := range sections {
		extra, err := sec.Blocks(st, ticket)
		if err != nil {
			return "", err
		}
		blocks = append(blocks, extra...)
	}
	blocks = append(blocks, axi.Help(helpLines...))

	var buf bytes.Buffer
	axi.Render(&buf, blocks...)
	return buf.String(), nil
}

// askCounts is how a ticket's outstanding asks stand against the decisions
// `jig triage` has journaled for the next round to apply.
type askCounts struct {
	// decided are the asks with a pending decision the next round applies.
	decided int
	// undecided are the others: no pending decision, or a keep with no build
	// target, which the round leaves asked.
	undecided int
	// noTarget are the undecided asks that have a keep with no build target.
	noTarget int
	// parked are the undecided asks the fix budget parked.
	parked int
}

// add counts one outstanding ask f, and the decision d journaled for it (the
// zero decision when none was).
func (c *askCounts) add(f verifydeliver.Finding, d verifydeliver.TriageDecision) {
	switch {
	case !d.Decided():
		c.undecided++
	case d.KeepLacksTarget(f):
		c.undecided++
		c.noTarget++
	default:
		c.decided++
		return
	}
	if f.RoutedWhy == verifydeliver.RoutedWhyBudget {
		c.parked++
	}
}

// asksOf counts n asks of one kind: "1 undecided ask", "3 decided asks".
func asksOf(n int, kind string) string {
	if n == 1 {
		return "1 " + kind + " ask"
	}
	return fmt.Sprintf("%d %s asks", n, kind)
}

// decidedClause says when the decided asks take effect: a decision changes
// nothing until a round closes.
func decidedClause(n int) string {
	verb := "are"
	if n == 1 {
		verb = "is"
	}
	return fmt.Sprintf("the %s %s applied when the next round closes", asksOf(n, "decided"), verb)
}

// outstandingAsksHelpLine phrases the command that decides a ticket's
// outstanding asks, lead-verb-free ("run ...", for a caller to prepend
// "Run " or "Then "): it counts the asks nobody has decided, and says the
// decided ones are applied when the next round closes, since a decision
// changes nothing before then. While any undecided ask is budget-parked it
// says the fix budget is reached and names keeping or dismissing them, rather
// than the generic "decide the undecided asks" - the same `jig triage` decides
// both kinds, and the next `jig gate <ticket>` round applies the decisions, so
// this is one line, not two. With every ask decided, only the gate is left.
func outstandingAsksHelpLine(ticket string, c askCounts, budgetUsed, budgetLimit int) string {
	apply := fmt.Sprintf("`jig gate %s` to apply the decisions", ticket)
	if c.undecided == 0 {
		return apply + ": " + decidedClause(c.decided)
	}
	triage := fmt.Sprintf("`jig triage %s <id> keep|dismiss`", ticket)
	var line string
	if c.parked > 0 {
		line = fmt.Sprintf("%s to keep or dismiss the parked finding(s), then %s: the fix budget is reached (%d of %d)", triage, apply, budgetUsed, budgetLimit)
	} else {
		line = fmt.Sprintf("%s to decide the %s, then %s", triage, asksOf(c.undecided, "undecided"), apply)
	}
	if c.noTarget > 0 {
		line += "; a keep with no build target needs --workspace or --oracle, or the round leaves it asked"
	}
	if c.decided > 0 {
		line += "; " + decidedClause(c.decided)
	}
	return line
}

// resumeCommand returns the exact command that clears a parked
// (needs-input) slice's custody. Amending the brief and requeuing is the
// remedy only when the slice was parked for a flawed brief AND has brief
// sections for --from-brief-diff to notice (ready.Requeue keys off
// FromBrief hashes): a gate fix slice carries the same "flawed-brief"
// reason but no FromBrief, and a plain question on a slice that does have
// brief sections is not a brief problem at all. Every other parked slice,
// which is the common case, is resumed by answering its open question.
func resumeCommand(ticket string, sl store.Slice, ss store.SliceState) string {
	if ss.Reason == "flawed-brief" && len(sl.FromBrief) > 0 {
		return fmt.Sprintf("jig requeue %s --from-brief-diff", ticket)
	}
	return fmt.Sprintf("jig answer %s %s '<text>'", ticket, ss.Question)
}

// resumeCell returns the parked table's "resume" column text. For the
// common case, that is resumeCommand's raw command: running it is the whole
// remedy. For the amend-brief-then-requeue case, the raw command alone is
// not the whole remedy - it requeues nothing, and the next `jig status` is
// byte-identical, until the brief has actually been amended - so the cell
// says so, the same way the stalled hint already does ("amend the brief,
// then run `jig requeue <ticket> --from-brief-diff`").
func resumeCell(ticket string, sl store.Slice, ss store.SliceState) string {
	cmd := resumeCommand(ticket, sl, ss)
	if ss.Reason == "flawed-brief" && len(sl.FromBrief) > 0 {
		return fmt.Sprintf("amend the brief, then run %s", cmd)
	}
	return cmd
}

// frontierGreen reports whether every slice of ticket is green: the exact
// condition verifydeliver's own frontier check applies before it will open
// a gate round, with a ticket that has no slices yet nothing to block.
// Both `jig status` and `jig gate`'s report hint decide from this rather
// than from a proxy, so neither ever prints a `jig gate <ticket>` the
// check would refuse. A state jig cannot read counts as not green: naming
// the frontier first is sound advice in every case, naming the gate is
// not.
func frontierGreen(st *store.Store, ticket string) bool {
	slices, err := st.ReadSlices(ticket)
	if err != nil {
		return false
	}
	for _, sl := range slices {
		ss, err := st.ReadSliceState(ticket, sl.ID)
		if err != nil || ss.State != "green" {
			return false
		}
	}
	return true
}

// joinPlus joins ids with "+", the wire format for a slice's blocked_by
// column.
func joinPlus(ids []string) string {
	out := ids[0]
	for _, id := range ids[1:] {
		out += "+" + id
	}
	return out
}

// sliceByID returns the slice with the given id from slices, and whether it
// was found.
func sliceByID(slices []store.Slice, id string) (store.Slice, bool) {
	for _, sl := range slices {
		if sl.ID == id {
			return sl, true
		}
	}
	return store.Slice{}, false
}

// nextStepHint computes the single contextual next-step hint for ticket:
// an open question takes priority; next, a stalled or env-blocked slice
// points at the command that can actually reset it; otherwise the hint is
// the advice of the phase the ticket is at (phaseAdvice, from
// internal/phase): the command that moves it on, and the prompt that asks an
// agent for it. `jig next`, `jig here` and the claim and gate refusals name
// the same prompt.
func nextStepHint(st *store.Store, ticket string) (string, error) {
	slices, err := st.ReadSlices(ticket)
	if err != nil {
		return "", err
	}
	questions, err := st.ReadQuestions(ticket)
	if err != nil {
		return "", err
	}
	for _, q := range questions {
		if q.Status != "open" {
			continue
		}
		ss, err := st.ReadSliceState(ticket, q.Slice)
		if err != nil {
			return "", err
		}
		sl, ok := sliceByID(slices, q.Slice)
		if !ok {
			return "", fmt.Errorf("nextStepHint: question %s names unknown slice %s", q.ID, q.Slice)
		}
		// Same resume command as the parked table's "resume" column
		// (resumeCommand): the hint and the table must never disagree
		// about how to get unstuck.
		cmd := resumeCommand(ticket, sl, ss)
		if ss.Reason == "flawed-brief" && len(sl.FromBrief) > 0 {
			// The brief must be amended before this command does anything:
			// run as printed with no amendment, it requeues nothing and the
			// next status is byte-identical. Say so in that order, the way
			// the stalled hint below already does.
			return fmt.Sprintf("Amend the brief, then run `%s` to resume", cmd), nil
		}
		return fmt.Sprintf("Run `%s` to answer and resume", cmd), nil
	}

	states := make(map[string]store.SliceState, len(slices))
	for _, sl := range slices {
		ss, err := st.ReadSliceState(ticket, sl.ID)
		if err != nil {
			return "", err
		}
		states[sl.ID] = ss
	}

	// A stalled slice outranks every hint below except an open question
	// (checked above), and env-blocked in turn outranks everything but
	// stalled (matching the state: line's own precedence in RenderStatus):
	// both are stuck, not merely unfinished, so the worse of the two is
	// reported the moment it is found rather than waited out - checked in
	// two full passes, not one combined pass, so a stalled slice later in
	// slices.yaml still outranks an env-blocked one earlier in it. The
	// --from-brief-diff remedy only ever works for a stalled slice with a
	// brief section to amend (FromBrief non-empty); a fix slice from a gate
	// round has none, and it can never touch it, so that slice's only way
	// out is `jig requeue --slice`, the same command an env-blocked slice
	// resumes with once its env is back up.
	for _, sl := range slices {
		ss := states[sl.ID]
		if ss.State != "stalled" {
			continue
		}
		if len(sl.FromBrief) > 0 {
			return fmt.Sprintf(
				"Slice %s is stalled (%s): amend the brief, then run `jig requeue %s --from-brief-diff`",
				sl.ID, ss.Reason, ticket,
			), nil
		}
		return fmt.Sprintf(
			"Slice %s is stalled (%s): run `jig requeue %s --slice %s`",
			sl.ID, ss.Reason, ticket, sl.ID,
		), nil
	}
	for _, sl := range slices {
		ss := states[sl.ID]
		if ss.State != "env-blocked" {
			continue
		}
		return fmt.Sprintf(
			"Slice %s is env-blocked (%s): bring the env up, then run `jig requeue %s --slice %s`",
			sl.ID, ss.Reason, ticket, sl.ID,
		), nil
	}

	t, err := derivePhase(st, ticket)
	if err != nil {
		return "", err
	}
	return phaseAdvice(t, ticket), nil
}

var gateRoundDirRE = regexp.MustCompile(`^round-(\d+)$`)

// latestGateRound returns the highest gate round number recorded for
// ticket, and that round's verdict (from its report.yaml). rounds is 0 when
// no gate round has run yet.
//
// A report.yaml that cannot be read or parsed yields an empty verdict
// rather than an error, for the same reason the outstanding-ask fold skips
// a round it cannot read: this feeds `jig status`, the command a person
// runs when something is already wrong, and an unreadable round is not a
// reason to refuse to show the ticket at all. An empty verdict reads as
// "not clean", so the hint points at the frontier rather than at publish -
// the safe direction when jig cannot tell.
func latestGateRound(st *store.Store, ticket string) (rounds int, verdict string, unreadable bool, err error) {
	dir := filepath.Join(st.TicketDir(ticket), "gate")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, "", false, nil
		}
		return 0, "", false, err
	}

	max := 0
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		m := gateRoundDirRE.FindStringSubmatch(e.Name())
		if m == nil {
			continue
		}
		if n, cerr := strconv.Atoi(m[1]); cerr == nil && n > max {
			max = n
		}
	}
	if max == 0 {
		return 0, "", false, nil
	}

	data, err := os.ReadFile(filepath.Join(dir, fmt.Sprintf("round-%d", max), "report.yaml"))
	if err != nil {
		// A round with no report.yaml at all has not finished writing
		// one; that is a round in progress, not a corrupt one, so it is
		// not reported as unreadable. Anything else is.
		return max, "", !os.IsNotExist(err), nil
	}
	var rep struct {
		Verdict string `yaml:"verdict"`
	}
	if err := yaml.Unmarshal(data, &rep); err != nil {
		return max, "", true, nil
	}
	return max, rep.Verdict, false, nil
}
