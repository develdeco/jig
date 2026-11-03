package spend

import (
	"fmt"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/ledger"
	"github.com/develdeco/jig/internal/observe"
	"github.com/develdeco/jig/internal/project"
)

// Notes is the note source for spend (observe.NoteSource). `jig observe`
// calls it at every tool, prompt, stop and session-end event of a session on a
// ticket:
//
//   - at a stop and a session end it reads the session's transcript
//     (Event.Transcript) and, when the session's total differs from its
//     latest spend line, appends a new one. The total leaves out the
//     transcripts of the subagents that have a line of their own (see
//     ownAgents). The first time it records spend for a ticket that has no
//     budget record, it records the project's default budget (the jig.yaml
//     marker's), when there is one, as the ticket's, so status and every
//     later event read one budget.
//   - at a subagent's stop it does the same for the agent's own transcript,
//     on the ticket the agent is placed at, and the line it appends has the
//     agent's id set (AgentLine). An agent placed at no ticket, or with no
//     transcript, writes none.
//   - at every event but the session end it holds the ticket's spend, the sum
//     of its sessions' and agents' latest lines, against its budget record,
//     and returns a note when the ticket is near or over it.
//
// The note is never Enforce: spend is reported, and nothing is stopped. A
// ticket with no budget record, and a session whose transcript cannot be
// read, produce no note and no error, the first because there is nothing to
// hold spend against and the second because an absent transcript is a
// harness that has none (a path that is there but cannot be read is the
// one error).
func Notes(c observe.NoteCtx) ([]observe.Note, error) {
	switch c.Event.Kind {
	case observe.KindTool, observe.KindPrompt, observe.KindStop, observe.KindSessionEnd:
	default:
		return nil, nil
	}
	if c.Store == nil || c.Where.Ticket == "" {
		return nil, nil
	}
	lines, err := c.Journal()
	if err != nil {
		return nil, err
	}
	switch {
	case c.Event.Kind == observe.KindStop && c.Event.IsSubagent():
		lines, err = recordAgent(c, lines)
	case c.Event.Kind == observe.KindStop || c.Event.Kind == observe.KindSessionEnd:
		lines, err = record(c, lines)
	}
	if err != nil {
		return nil, err
	}
	if c.Event.Kind == observe.KindSessionEnd {
		return nil, nil
	}
	return alert(c, lines)
}

// record appends the spend line of the event session when its total changed,
// and returns lines with it added (the journal as read before it is stale).
// The line is written to the clone and not committed: a Stop runs in every
// turn, the line is cumulative and written again whenever the total moved, so
// a commit and a push here would be a store commit and a round trip per turn.
// The next checkpoint's commit, or `jig trackers sync`, carries it to origin
// (ADR 0030, "Amendment: a checkpoint lands what its sync wrote").
func record(c observe.NoteCtx, lines []journal.Line) ([]journal.Line, error) {
	transcript, session := transcriptOf(c.Event)
	if transcript == "" || session == "" {
		return lines, nil
	}
	// A prices file that does not parse adds no prices: the models it would
	// have priced show as unpriced, which status and the notes say.
	prices, _ := LoadPrices(c.JigHome)
	own, err := ownAgents(c, transcript, session)
	if err != nil {
		return lines, err
	}
	t, err := FromSession(transcript, session, own, prices)
	if err != nil {
		return lines, err
	}
	if t.Messages == 0 {
		return lines, nil
	}
	return write(c, lines, Line(ledger.Stamp(c.Now), session, c.Event.Harness, c.Where.Slice, t))
}

// recordAgent appends the spend line of the subagent the event is, on the
// ticket it is placed at, when its total changed. The total is that agent's
// own transcript, which the session's line leaves out once the agent has a
// line (ownAgents). A subagent's event names the session it belongs to and
// the session's main transcript; the agent's transcript is beside it.
func recordAgent(c observe.NoteCtx, lines []journal.Line) ([]journal.Line, error) {
	transcript, session := transcriptOf(c.Event)
	if transcript == "" || session == "" {
		return lines, nil
	}
	prices, _ := LoadPrices(c.JigHome)
	t, err := FromAgent(transcript, session, c.Event.Agent, prices)
	if err != nil {
		return lines, err
	}
	if t.Messages == 0 {
		return lines, nil
	}
	return write(c, lines, AgentLine(ledger.Stamp(c.Now), session, c.Event.Agent, c.Event.Harness, c.Where.Slice, t))
}

// transcriptOf is the transcript the event names and the session it belongs
// to, which is the name of the transcript when the event names none.
func transcriptOf(e observe.Event) (transcript, session string) {
	transcript, session = e.Transcript, e.Session
	if session == "" && transcript != "" {
		session = strings.TrimSuffix(filepath.Base(transcript), ".jsonl")
	}
	return transcript, session
}

// write appends line to the ticket journal unless its key (the session, or the
// session and the agent) has said the same already, and returns lines with it
// added.
func write(c observe.NoteCtx, lines []journal.Line, line journal.Line) ([]journal.Line, error) {
	if prev, ok := Latest(lines)[Key(line.Session, line.Agent)]; ok && Same(prev, line) {
		return lines, nil
	}
	if err := seedBudget(c); err != nil {
		return lines, err
	}
	if err := journal.Append(c.Store, c.Where.Ticket, line); err != nil {
		return lines, err
	}
	line.Ticket = c.Where.Ticket
	return append(slices.Clone(lines), line), nil
}

// ledgerWindow bounds how far back in the ledger the agents of a session are
// looked for, as the core bounds its memory of a session.
const ledgerWindow = 16 << 20

// ownAgents are the subagents of session, among those that have a transcript
// beside the main one, that have a spend line of their own: their last stop
// was placed at a ticket. The machine ledger says it, with a hook line of the
// session, of that agent, at a stop, that names a ticket. The session's line
// leaves their transcripts out; an agent with no such line (still running,
// crashed, placed at no ticket, or of a harness that sends no subagent stops)
// stays in it, so its tokens are not lost.
//
// The walk goes back from the end of the ledger and ends when every agent on
// disk is found. It does not end at the session's start: a session that is
// resumed or compacted starts again under the same id, and the lines its
// agents wrote to the store outlive that. It reads nothing for a session
// without subagent transcripts.
func ownAgents(c observe.NoteCtx, transcript, session string) (map[string]bool, error) {
	onDisk := AgentsOnDisk(transcript)
	if len(onDisk) == 0 {
		return nil, nil
	}
	want := map[string]bool{}
	for _, id := range onDisk {
		want[id] = true
	}
	own := map[string]bool{}
	err := ledger.ReadBackward(c.JigHome, ledgerWindow, func(l journal.Line) bool {
		if l.Session == session && l.Event == journal.EventHook && l.Outcome == observe.KindStop &&
			l.Ticket != "" && want[l.Agent] {
			own[l.Agent] = true
		}
		return len(own) < len(want)
	})
	return own, err
}

// seedBudget records the project's default budget as the ticket's when the
// ticket has no budget record and the project has a default.
func seedBudget(c observe.NoteCtx) error {
	if _, recorded, err := ReadBudget(c.Store, c.Where.Ticket); err != nil || recorded {
		return err
	}
	b, ok := ProjectDefault(c.Where.Cwd, c.Where.RepoDir, c.Where.Root)
	if !ok {
		return nil
	}
	return WriteBudget(c.Store, c.Where.Ticket, b)
}

// alert is the note for a ticket near or over its budget, or none.
func alert(c observe.NoteCtx, lines []journal.Line) ([]observe.Note, error) {
	ticket := c.Where.Ticket
	b, recorded, err := ReadBudget(c.Store, ticket)
	if err != nil {
		return nil, err
	}
	if !recorded || !Limited(b) {
		return nil, nil
	}
	t := FromLines(lines)
	v := Check(t, b)
	if v.State != StateNear && v.State != StateOver {
		return nil, nil
	}
	// The limits are in the ref: a ticket whose budget was raised and then
	// passed again is news to a session that heard of the first time.
	ref := fmt.Sprintf("spend:%s:%s:%d:%s", ticket, v.State, b.Tokens, strconv.FormatFloat(b.USD, 'f', 2, 64))
	return []observe.Note{{Ref: ref, Text: alertText(ticket, t, b, v)}}, nil
}

// alertText is the one line a near or over budget note says.
func alertText(ticket string, t Totals, b project.Budget, v Verdict) string {
	var parts []string
	if b.Tokens > 0 {
		parts = append(parts, fmt.Sprintf("%s of %s tokens", Compact(t.Tokens()), Compact(b.Tokens)))
	}
	if b.USD > 0 {
		parts = append(parts, fmt.Sprintf("%s of %s", Dollars(t.USD), Dollars(b.USD)))
	}
	var s string
	if v.State == StateOver {
		s = fmt.Sprintf("%s is over its spend budget: %s", ticket, strings.Join(parts, ", "))
	} else {
		s = fmt.Sprintf("%s has used %d%% of its spend budget: %s", ticket, int(v.Ratio*100), strings.Join(parts, ", "))
	}
	s += fmt.Sprintf(" (%d %s", t.Sessions, plural(t.Sessions, "session", "sessions"))
	if t.Agents > 0 {
		s += fmt.Sprintf(", %d %s", t.Agents, plural(t.Agents, "subagent", "subagents"))
	}
	s += ")."
	if b.USD > 0 && len(t.Unpriced) > 0 {
		s += " No price for " + strings.Join(t.Unpriced, ", ") + ": its dollars are not counted."
	}
	return s + " Jig only reports spend; it stops nothing."
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}
