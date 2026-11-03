package tui

import (
	"path/filepath"
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

// detailMax is the most rows the Detail panel has, its frame not counted.
const detailMax = 4

// claimSlack is how long before a claim line a tool line of the claim's call
// may be stamped and still be read: the lines are stamped before the ledger's
// append lock is taken, so two writers can land a little out of order. It is
// the ledger's own agentSlack.
const claimSlack = time.Minute

// chartDetailRows are the rows of the Detail panel for the ticket of r: a row
// for each slice being built (its id, its stage, the session that holds it and
// the agent that claimed it), or, when nothing is, the one row that says where
// the ticket stands.
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
	if len(rows) == 0 {
		_, label := m.stepLabel(r.t)
		rows = append(rows, runRow{"state", m.st.dim(label), 0})
	}
	return rows
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
// ledger tool line of the claim's session that ran `jig claim`, the nearest in
// time to the claim among those stamped no more than a minute before it. A
// claim line has no agent of its own, since the CLI cannot know which agent it
// runs for. Empty when no such line is kept (the harness has no hooks, or the
// line fell out of the ledger's last lines) and for a call of the session
// itself.
func (t *ticket) claimAgent(claim events.Envelope) string {
	at, ok := claim.At()
	if !ok {
		return ""
	}
	agent, best, found := "", time.Duration(0), false
	for _, l := range t.ledger {
		lat, ok := l.At()
		if !ok || !isMove(l) || l.Session != claim.Session || !claimCall(l.Command) || lat.Before(at.Add(-claimSlack)) {
			continue
		}
		gap := lat.Sub(at)
		if gap < 0 {
			gap = -gap
		}
		if !found || gap < best {
			agent, best, found = l.Agent, gap, true
		}
	}
	return agent
}

// claimCall is whether the shell command line cmd runs `jig claim` and does not
// release: a word whose base name is jig or jig.exe (any path, any case),
// directly followed by the word claim, and no --release anywhere. It mirrors
// ledger.RunsJig(cmd, "", "claim") with the refusal of --release that
// internal/observe makes, because the screens may not import the ledger
// (TestTheScreensReadNoStore); a test that may pins that they agree.
func claimCall(cmd string) bool {
	if strings.Contains(cmd, "--release") {
		return false
	}
	words := strings.Fields(cmd)
	for i, w := range words {
		base := strings.ToLower(filepath.Base(strings.ReplaceAll(strings.Trim(w, `"'`), `\`, "/")))
		if (base == "jig" || base == "jig.exe") && i+1 < len(words) && words[i+1] == "claim" {
			return true
		}
	}
	return false
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
