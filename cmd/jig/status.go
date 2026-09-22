package main

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"

	"gopkg.in/yaml.v3"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/store"
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

	out, err := RenderStatus(st, ticket, *storeFlag, *projectFlag)
	if err != nil {
		return renderErr(stdout, err)
	}
	fmt.Fprint(stdout, out)
	return 0
}

// resumeFlags returns the "--store <path>"/"--project <name>" suffix (with
// a leading space) that reproduces this invocation's store selection on a
// command printed for the person to run later, so a resume command works
// as printed even when the store cannot be resolved the same way from
// wherever they run it next. Empty when neither flag was given. --project
// wins when both are, matching resolveStoreForProject's own precedence.
func resumeFlags(storeFlag, projectFlag string) string {
	switch {
	case projectFlag != "":
		return fmt.Sprintf(" --project %s", projectFlag)
	case storeFlag != "":
		return fmt.Sprintf(" --store %s", storeFlag)
	default:
		return ""
	}
}

// RenderStatus renders the exact `jig status` golden format for ticket:
// ticket/state lines, a slices table, a questions table (or "questions:
// none"), a parked table (present only while a slice is needs-input) and a
// stalled table (present only while a slice is stalled), and a contextual
// help hint. The parked and stalled tables are the custody surface: they
// make "awaiting a human" (parked) and "stuck" (stalled) unmistakable and
// distinct from each other and from ordinary in-progress work. storeFlag
// and projectFlag are this invocation's own --store/--project (each may be
// empty); every command this renders carries them, so it still works as
// printed wherever the person runs it next.
func RenderStatus(st *store.Store, ticket, storeFlag, projectFlag string) (string, error) {
	flags := resumeFlags(storeFlag, projectFlag)
	slices, err := st.ReadSlices(ticket)
	if err != nil {
		return "", err
	}
	questions, err := st.ReadQuestions(ticket)
	if err != nil {
		return "", err
	}

	var rows, parkedRows, stalledRows [][]string
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

		if ss.State == "needs-input" {
			parkedRows = append(parkedRows, []string{sl.ID, ss.Question, resumeCommand(ticket, sl, ss, flags)})
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
	case counts["stalled"] > 0:
		overall = "stalled"
	case counts["needs-input"] > 0:
		overall = "parked"
	case counts["env-blocked"] > 0:
		overall = "env-blocked"
	case allGreen:
		overall = "green"
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
	if len(parkedRows) > 0 {
		blocks = append(blocks, axi.Table("parked", []string{"slice", "question", "resume"}, parkedRows))
	}
	if len(stalledRows) > 0 {
		blocks = append(blocks, axi.Table("stalled", []string{"slice", "reason", "summary"}, stalledRows))
	}

	hint, err := nextStepHint(st, ticket, storeFlag, projectFlag)
	if err != nil {
		return "", err
	}
	blocks = append(blocks, axi.Help(hint))

	var buf bytes.Buffer
	axi.Render(&buf, blocks...)
	return buf.String(), nil
}

// resumeCommand returns the exact command that clears a parked (needs-input)
// slice's custody, chosen from the slice's own structure rather than the
// state's Reason alone: a slice with brief sections to amend (FromBrief
// non-empty) is remediated by amending the brief and requeuing - the only
// command that can ever touch it, since frontier.Requeue's --from-brief-diff
// keys off FromBrief hashes. A slice with none (for example a gate fix
// slice, which routeQuestion can still mark Reason "flawed-brief" the same
// way) has no brief section for --from-brief-diff to ever notice, so it is
// remediated by answering the open question directly - the only command
// that works for it. flags is resumeFlags' output, appended so the printed
// command carries the store selection the caller resolved this ticket with.
func resumeCommand(ticket string, sl store.Slice, ss store.SliceState, flags string) string {
	if len(sl.FromBrief) > 0 {
		return fmt.Sprintf("jig requeue %s --from-brief-diff%s", ticket, flags)
	}
	return fmt.Sprintf("jig run %s --answer %s '<text>'%s", ticket, ss.Question, flags)
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
// an open question takes priority; next, a stalled slice points at
// requeuing with an amended brief; otherwise, once every slice is green,
// the hint points at gate (no rounds yet), publish (last round clean), or
// run (a fix-slice round is queued); otherwise it points at run to work
// the frontier. storeFlag and projectFlag are RenderStatus's own (see its
// doc comment); every command this prints carries them via resumeFlags.
func nextStepHint(st *store.Store, ticket, storeFlag, projectFlag string) (string, error) {
	flags := resumeFlags(storeFlag, projectFlag)
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
		if len(sl.FromBrief) > 0 {
			// Same resume command as the parked table's "resume" column
			// (resumeCommand): the hint and the table must never disagree
			// about how to get unstuck.
			return fmt.Sprintf("Run `%s` to amend the brief and resume", resumeCommand(ticket, sl, ss, flags)), nil
		}
		return fmt.Sprintf("Run `%s` to answer and resume", resumeCommand(ticket, sl, ss, flags)), nil
	}

	if len(slices) == 0 {
		return intakeHint(ticket), nil
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
				"Slice %s is stalled (%s): amend the brief, then run `jig requeue %s --from-brief-diff%s`",
				sl.ID, ss.Reason, ticket, flags,
			), nil
		}
		return fmt.Sprintf(
			"Slice %s is stalled (%s): run `jig requeue %s --slice %s%s`",
			sl.ID, ss.Reason, ticket, sl.ID, flags,
		), nil
	}
	for _, sl := range slices {
		ss := states[sl.ID]
		if ss.State != "env-blocked" {
			continue
		}
		return fmt.Sprintf(
			"Slice %s is env-blocked (%s): bring the env up, then run `jig requeue %s --slice %s%s`",
			sl.ID, ss.Reason, ticket, sl.ID, flags,
		), nil
	}

	allGreen := true
	for _, sl := range slices {
		if states[sl.ID].State != "green" {
			allGreen = false
		}
	}

	if allGreen {
		rounds, verdict, err := latestGateRound(st, ticket)
		if err != nil {
			return "", err
		}
		switch {
		case rounds == 0:
			return fmt.Sprintf("Run `jig gate %s%s` to open a gate round", ticket, flags), nil
		case verdict == "clean":
			return fmt.Sprintf("Run `jig publish %s%s` to open the PR", ticket, flags), nil
		default:
			return fmt.Sprintf("Run `jig run %s%s` to work the fix-slice round", ticket, flags), nil
		}
	}
	return fmt.Sprintf("Run `jig run %s%s` to work the frontier", ticket, flags), nil
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
