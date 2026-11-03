package tui

import (
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/develdeco/jig/internal/events"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/phase"
)

// The chart screen's Detail panel is the ticket under the cursor, in a few
// rows: each slice being built with its stage, the session that holds its claim
// and the agent whose call made it. It reads nothing the ticket screens do not:
// the stream and `jig status`, so moving the cursor re-draws it and runs no
// verb.

// detailMax is the most rows the Detail panel has, its frame not counted, and
// holdsMax the most holds it names.
const (
	detailMax = 4
	holdsMax  = 2
)

// claimSlack is how long before a claim line a tool line of the claim's call
// may be stamped and still be read: the lines are stamped before the ledger's
// append lock is taken, so two writers can land a little out of order. It is
// the ledger's own agentSlack.
const claimSlack = time.Minute

// chartDetailRows are the rows of the Detail panel for the ticket of r: a row
// for each slice being built (its id, its stage, the session that holds it and
// the agent that claimed it), then the holds that are current, the newest two
// oldest first, and, when there is neither, the one row that says where the
// ticket stands. A hold weighs 0 so a short screen gives up slice rows before
// it gives up a hold.
func (m *Model) chartDetailRows(r chartRow) []runRow {
	if r.t == nil {
		return []runRow{{"state", m.st.dim("not read yet"), 0}}
	}
	var rows []runRow
	subs := r.d.Of(phase.Build).Subs
	for _, sub := range subs {
		id, ok := strings.CutPrefix(sub.Key, "slice:")
		if !ok || (sub.State != phase.SubActive && sub.State != phase.SubNeedsYou && sub.State != phase.SubFailed) {
			continue
		}
		label := ""
		if len(rows) == 0 {
			label = "slice"
		}
		rows = append(rows, runRow{label, m.sliceDetail(r.t, sub, subs, id), 1})
	}
	for i, l := range r.t.currentHolds() {
		label := ""
		if i == 0 {
			label = "hold"
		}
		rows = append(rows, runRow{label, m.moveText(r.t, l), 0})
	}
	if len(rows) == 0 {
		rows = append(rows, runRow{"state", m.st.dim(chartStateWord(r)), 0})
	}
	return rows
}

// chartStateWord is where the ticket of r stands, in the words of a header: its
// current step in its state, from the row's own steps, which follow the verb
// (followVerb), and not from the screens' derivation alone as stepLabel's do. A
// ticket that is behind owes a merge and a publish, and is not said to be
// published.
func chartStateWord(r chartRow) string {
	if r.behind {
		return behindText
	}
	cur := r.steps[currentStep(r.steps)]
	if cur.Done {
		return "published"
	}
	return stepWords(cur.Step, cur.State)
}

// sliceDetail is a slice being built in the words of a Detail row: its id, its
// stage, who holds it and which agent claimed it, the last two when the slice
// has a live claim.
func (m *Model) sliceDetail(t *ticket, sub phase.Sub, subs []phase.Sub, id string) string {
	word := sub.Detail
	if sub.State == phase.SubActive {
		stages, reached := t.stagesOf(stepBuild, sub, subs)
		word = stages[max(reached, 1)-1]
	}
	parts := []string{id, m.st.state(subState(stepBuild, sub), word)}
	if claim, ok := t.liveClaim(id); ok {
		parts = append(parts, m.whoAs(t, claim.Session, roleBuilder))
		if agent := t.claimAgent(claim); agent != "" {
			parts = append(parts, agentName(agent))
		}
	}
	return strings.Join(parts, " · ")
}

// liveClaim is the claim that holds slice: its latest claim line, unless the
// slice was released after it.
func (t *ticket) liveClaim(slice string) (events.Envelope, bool) {
	i := t.latest(slice, journal.EventClaim)
	if i < 0 || t.latest(slice, journal.EventRelease) > i {
		return events.Envelope{}, false
	}
	return t.lines[i], true
}

// claimAgent is the agent that made the call that wrote claim: the agent of the
// ledger tool line of the claim's session that ran `jig claim` for the claim's
// slice, the nearest in time to the claim among those stamped no more than a
// minute before it. A claim line has no agent of its own, since the CLI cannot
// know which agent it runs for, and a session's calls for different slices can
// sit close together, so a call is read by the slice it names: one that names
// the slice comes before any other, however much nearer; one that names only
// other slices made another claim and is never read; one that names none (the
// slice came from the environment) is the fallback when no call names the
// slice. Empty when no such line is kept (the harness has no hooks, or the line
// fell out of the ledger's last lines) and for a call of the session itself.
func (t *ticket) claimAgent(claim events.Envelope) string {
	at, ok := claim.At()
	if !ok {
		return ""
	}
	agent, best, found, named := "", time.Duration(0), false, false
	for _, l := range t.ledger {
		lat, ok := l.At()
		if !ok || !isMove(l) || l.Session != claim.Session || !claimCall(l.Command) || lat.Before(at.Add(-claimSlack)) {
			continue
		}
		claimed := claimedSlices(l.Command)
		if len(claimed) > 0 && !slices.Contains(claimed, claim.Slice) {
			continue
		}
		gap := lat.Sub(at)
		if gap < 0 {
			gap = -gap
		}
		names := len(claimed) > 0
		if !found || names && !named || names == named && gap < best {
			agent, best, found, named = l.Agent, gap, true, names
		}
	}
	return agent
}

// currentHolds are the holds of the ticket that no step has moved past: the
// machine ledger's lines that say jig held an agent at its stop (isHold), the
// newest two, oldest first, that are stamped after the latest journal line of a
// step (a verification landing, a gate opening, a pull request recorded). A line
// about the ticket as a whole (what it has cost) belongs to no step, so the
// main session's stop does not end a hold, and the lines the screens wrote
// themselves from a status are not the journal's. The activity log keeps the
// history of the ones that ended.
func (t *ticket) currentHolds() []events.Envelope {
	var last time.Time
	for _, l := range t.lines {
		if at, ok := l.At(); ok && stepOfLine(l) >= 0 && at.After(last) {
			last = at
		}
	}
	var holds []events.Envelope
	for _, l := range t.ledger {
		if at, ok := l.At(); ok && isHold(l) && at.After(last) {
			holds = append(holds, l)
		}
	}
	slices.SortStableFunc(holds, func(a, b events.Envelope) int {
		x, _ := a.At()
		y, _ := b.At()
		return x.Compare(y)
	})
	return holds[max(0, len(holds)-holdsMax):]
}

// claimCall is whether the shell command line cmd runs `jig claim` and does not
// release: a word whose base name is jig or jig.exe (any path, any case),
// directly followed by the word claim, and no --release anywhere. It mirrors
// ledger.RunsJig(cmd, "", "claim") with the refusal of --release that
// internal/observe makes, because the screens may not import the ledger
// (TestTheScreensReadNoStore); a test that may pins that they agree.
func claimCall(cmd string) bool {
	return !strings.Contains(cmd, "--release") && len(claimArgs(cmd)) > 0
}

// claimArgs are the words after each `jig claim` in the shell command line cmd,
// up to the next shell separator, one entry for each such claim.
func claimArgs(cmd string) [][]string {
	var calls [][]string
	words := strings.Fields(cmd)
	for i, w := range words {
		base := strings.ToLower(filepath.Base(strings.ReplaceAll(strings.Trim(w, `"'`), `\`, "/")))
		if (base != "jig" && base != "jig.exe") || i+1 >= len(words) || words[i+1] != "claim" {
			continue
		}
		args := words[i+2:]
		if end := slices.IndexFunc(args, func(a string) bool { return a == "&&" || a == "||" || a == ";" || a == "|" }); end >= 0 {
			args = args[:end]
		}
		calls = append(calls, args)
	}
	return calls
}

// claimedSlices are the slices the shell command line cmd names in its `jig
// claim`s: the value of each --slice (one dash or two, the value after a space
// or an equals sign, quotes taken off). None when a claim names no slice, as
// when the environment gives it, and a flag of another command in the line is
// not read.
func claimedSlices(cmd string) []string {
	var named []string
	for _, args := range claimArgs(cmd) {
		for i := 0; i < len(args); i++ {
			flag, value, hasValue := strings.Cut(args[i], "=")
			if flag != "--slice" && flag != "-slice" {
				continue
			}
			if !hasValue && i+1 < len(args) {
				i++
				value = args[i]
			}
			if value = strings.Trim(value, `"'`); value != "" {
				named = append(named, value)
			}
		}
	}
	return named
}

// detailPanel adds the Detail panel of ticket id to p: a row a line, drawn as
// the Run panel's are. They are never rows the cursor can stand on, which would
// change what its index means.
func (m *Model) detailPanel(p *page, id string, rows []runRow) {
	p.boxTop("Detail", id)
	for _, r := range rows {
		p.boxAdd(m.st.dim(pad(r.label, 8)) + " " + r.text)
	}
	p.boxEnd()
}
