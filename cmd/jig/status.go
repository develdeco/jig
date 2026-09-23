package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/store"
	"github.com/develdeco/jig/internal/verifydeliver"
)

// cmdStatus implements `jig status [<ticket>]`.
func cmdStatus(args []string, stdout io.Writer) int {
	fs := newFlagSet("status")
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

	st, _, _, err := resolveStoreForProject(*projectFlag, *storeFlag)
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
// ticket/state lines, a slices table, a questions table (or "questions:
// none"), and a contextual help hint.
func RenderStatus(st *store.Store, ticket string) (string, error) {
	slices, err := st.ReadSlices(ticket)
	if err != nil {
		return "", err
	}
	questions, err := st.ReadQuestions(ticket)
	if err != nil {
		return "", err
	}

	var rows [][]string
	counts := map[string]int{}
	allGreen := len(slices) > 0
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
		rows = append(rows, []string{sl.ID, ss.State, strconv.Itoa(ss.Attempts), blocked, question})
	}

	overall := "building"
	switch {
	case allGreen:
		overall = "green"
	case counts["stalled"] > 0:
		overall = "stalled"
	case counts["needs-input"] > 0 || counts["env-blocked"] > 0:
		overall = "paused"
	}

	blocks := []string{
		"ticket: " + ticket,
		"state: " + overall,
		axi.Table("slices", []string{"id", "state", "attempts", "blocked_by", "question"}, rows),
	}
	if len(questions) == 0 {
		blocks = append(blocks, "questions: none")
	} else {
		var qrows [][]string
		for _, q := range questions {
			qrows = append(qrows, []string{q.ID, q.Slice, q.Status})
		}
		blocks = append(blocks, axi.Table("questions", []string{"id", "slice", "status"}, qrows))
	}

	// A gate round whose bookkeeping cannot be read is named rather than
	// passed over: status would otherwise be quietly missing a waiting
	// decision, which is exactly what this block exists to show.
	outstanding, unreadableRounds, err := verifydeliver.OutstandingAsks(st, ticket)
	if err != nil {
		return "", err
	}
	if len(unreadableRounds) > 0 {
		rounds := make([]string, 0, len(unreadableRounds))
		for _, r := range unreadableRounds {
			rounds = append(rounds, strconv.Itoa(r))
		}
		blocks = append(blocks, "unreadable_gate_rounds: "+strings.Join(rounds, ","))
	}
	if len(outstanding) > 0 {
		var askRows [][]string
		for _, f := range outstanding {
			askRows = append(askRows, []string{f.ID, f.Risk, fileLine(f), f.Title})
		}
		blocks = append(blocks, axi.Table("outstanding_asks", []string{"id", "risk", "file:line", "title"}, askRows))
	}

	// What decides an outstanding ask is `jig gate <ticket>` at a
	// terminal, named once here rather than in a per-row cell because it
	// is the same command for every ask. The frontier check refuses that
	// gate while any slice is short of green, and a round that queues fix
	// slices and leaves an ask undecided is the ordinary case, so while
	// the frontier is not green the ticket's own next step comes first and
	// the gate is named as the step after it - the order `jig gate`'s own
	// report hint prints.
	var helpLines []string
	if len(outstanding) > 0 && frontierGreen(st, ticket) {
		helpLines = []string{fmt.Sprintf("Run `jig gate %s` at a terminal to decide the outstanding asks", ticket)}
	} else {
		hint, err := nextStepHint(st, ticket)
		if err != nil {
			return "", err
		}
		helpLines = []string{hint}
		if len(outstanding) > 0 {
			helpLines = append(helpLines, fmt.Sprintf("Then run `jig gate %s` at a terminal to decide the outstanding asks", ticket))
		}
	}
	blocks = append(blocks, axi.Help(helpLines...))

	var buf bytes.Buffer
	axi.Render(&buf, blocks...)
	return buf.String(), nil
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

// nextStepHint computes the single contextual next-step hint for ticket:
// an open question takes priority; otherwise, once every slice is green,
// the hint points at gate (no rounds yet), publish (last round clean), or
// run (a fix-slice round is queued); otherwise it points at run to work
// the frontier.
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
		if q.Status == "open" {
			return fmt.Sprintf("Run `jig run %s --answer %s \"<text>\"` to answer and resume", ticket, q.ID), nil
		}
	}

	if len(slices) == 0 {
		return intakeHint(ticket), nil
	}
	allGreen := true
	for _, sl := range slices {
		ss, err := st.ReadSliceState(ticket, sl.ID)
		if err != nil {
			return "", err
		}
		if ss.State != "green" {
			allGreen = false
			break
		}
	}

	if allGreen {
		rounds, verdict, err := latestGateRound(st, ticket)
		if err != nil {
			return "", err
		}
		switch {
		case rounds == 0:
			return fmt.Sprintf("Run `jig gate %s` to open a gate round", ticket), nil
		case verdict == "clean":
			return fmt.Sprintf("Run `jig publish %s` to open the PR", ticket), nil
		default:
			return fmt.Sprintf("Run `jig run %s` to work the fix-slice round", ticket), nil
		}
	}
	return fmt.Sprintf("Run `jig run %s` to work the frontier", ticket), nil
}

var gateRoundDirRE = regexp.MustCompile(`^round-(\d+)$`)

// latestGateRound returns the highest gate round number recorded for
// ticket, and that round's verdict (from its report.yaml). rounds is 0 when
// no gate round has run yet.
func latestGateRound(st *store.Store, ticket string) (rounds int, verdict string, err error) {
	dir := filepath.Join(st.TicketDir(ticket), "gate")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, "", nil
		}
		return 0, "", err
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
		return 0, "", nil
	}

	data, err := os.ReadFile(filepath.Join(dir, fmt.Sprintf("round-%d", max), "report.yaml"))
	if err != nil {
		return max, "", err
	}
	var rep struct {
		Verdict string `yaml:"verdict"`
	}
	if err := yaml.Unmarshal(data, &rep); err != nil {
		return max, "", err
	}
	return max, rep.Verdict, nil
}
