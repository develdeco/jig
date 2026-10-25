package tui

import (
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/develdeco/jig/internal/events"
	"github.com/develdeco/jig/internal/journal"
)

// What the screens know is the event stream, kept per ticket, and what the
// verbs said about a ticket's slices and asks. Nothing here reads a store: a
// line arrives as an envelope and a status arrives as a verb's output.

const (
	// feedCap and ledgerCap bound the lines kept for the tail. The journal of
	// a ticket is kept whole, because the screens are derived from it; the
	// rest is only ever shown newest-last.
	feedCap   = 400
	ledgerCap = 400
)

// world is the screens' picture of one store.
type world struct {
	// tickets by id, and the order they are listed in: the roster `jig next`
	// gave, then the tickets the stream showed that it did not.
	tickets map[string]*ticket
	order   []string
	// feed is the latest lines of the whole stream, newest last.
	feed []events.Envelope
	// sessions binds a session to the ticket its lines last named, so a
	// ledger line that carries a session and no ticket (a tool call) can be
	// shown with the ticket's.
	sessions map[string]string
	// sessionInfo is what each session's session-start line in the ledger
	// said of it (people.go), whatever ticket the line named.
	sessionInfo map[string]sessionInfo
}

func newWorld() *world {
	return &world{tickets: map[string]*ticket{}, sessions: map[string]string{}, sessionInfo: map[string]sessionInfo{}}
}

// ticket is what is known of one ticket.
type ticket struct {
	id    string
	title string
	// rosterState is the state `jig next` gave the ticket (ready, blocked, ...).
	rosterState string
	// lines are the journal lines, by instant.
	lines []events.Envelope
	// ledger are the machine ledger lines about the ticket, newest last.
	ledger []events.Envelope
	// status is the last `jig status` read, nil before the first.
	status *statusInfo
	// loading is set while a status is being read; dirty, when the ticket
	// changed since the one being read was asked for.
	loading, dirty bool
	// failures counts the reads of the status that failed in a row.
	failures int
	// awaiting is set for a ticket the person asked to watch before the store
	// had a folder for it: it clears when the ticket has a line or a row in the
	// ticket list.
	awaiting bool
	// brief is the brief's text as `jig brief show --text` printed it, nil
	// until the brief screen has read it. briefStale is set when a line about
	// the brief arrived since (it is read again when the screen needs it), and
	// briefLoading while a read is out.
	brief                    *briefText
	briefStale, briefLoading bool
}

// ticketFor returns the ticket id, making it known when it is not.
func (w *world) ticketFor(id string) (*ticket, bool) {
	if t, ok := w.tickets[id]; ok {
		return t, false
	}
	t := &ticket{id: id}
	w.tickets[id] = t
	w.order = append(w.order, id)
	return t, true
}

// apply takes a batch of the stream in. It reports the tickets the batch
// touched, and whether any of them was new to the screens.
func (w *world) apply(envs []events.Envelope) (touched map[string]bool, fresh bool) {
	touched = map[string]bool{}
	for _, e := range envs {
		w.feed = append(w.feed, e)
		if len(w.feed) > feedCap {
			w.feed = w.feed[len(w.feed)-feedCap:]
		}
		if e.Src == events.SrcLedger && e.Session != "" && (e.Event == journal.EventSessionStart || e.Event == journal.EventSessionTouch) {
			info := w.sessionInfo[e.Session]
			info.Harness, info.Model = firstOf(e.Harness, info.Harness), firstOf(e.Model, info.Model)
			w.sessionInfo[e.Session] = info
		}
		id := e.Ticket
		if e.Session != "" && id != "" {
			w.sessions[e.Session] = id
		}
		if id == "" && e.Src == events.SrcLedger {
			id = w.sessions[e.Session]
		}
		if id == "" {
			continue
		}
		t, isNew := w.ticketFor(id)
		fresh = fresh || isNew
		t.awaiting = false
		touched[id] = true
		if e.Src == events.SrcJournal {
			t.lines = insertSorted(t.lines, e)
			continue
		}
		t.ledger = append(t.ledger, e)
		if len(t.ledger) > ledgerCap {
			t.ledger = t.ledger[len(t.ledger)-ledgerCap:]
		}
	}
	return touched, fresh
}

// setRoster takes the tickets `jig next` listed: their titles and states, and
// the order they are listed in.
func (w *world) setRoster(rows []rosterRow) {
	listed := map[string]bool{}
	order := make([]string, 0, len(rows)+len(w.order))
	for _, r := range rows {
		t, _ := w.ticketFor(r.ID)
		t.title, t.rosterState = r.Title, r.State
		t.awaiting = false
		listed[r.ID] = true
		order = append(order, r.ID)
	}
	for _, id := range w.order {
		if !listed[id] {
			order = append(order, id)
		}
	}
	w.order = order
}

// insertSorted puts e among lines by instant, after any line of the same
// instant (arrival order breaks the tie). A line with no readable time sorts
// first.
func insertSorted(lines []events.Envelope, e events.Envelope) []events.Envelope {
	at, _ := e.At()
	i := sort.Search(len(lines), func(i int) bool {
		t, _ := lines[i].At()
		return t.After(at)
	})
	lines = append(lines, events.Envelope{})
	copy(lines[i+1:], lines[i:])
	lines[i] = e
	return lines
}

// clock is the time of a line as the screens show it.
func clock(e events.Envelope, zone *time.Location) string {
	at, ok := e.At()
	if !ok {
		return "--:--:--"
	}
	return at.In(zone).Format("15:04:05")
}

// short is the first seven characters of a commit.
func short(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

// rosterRow is one ticket of `jig next --json`.
type rosterRow struct {
	ID, Title, State string
}

// latest is the position of the last line of the ticket whose event is one
// of names (and, when slice is not "", that is about the slice), or -1.
func (t *ticket) latest(slice string, names ...string) int {
	for i := len(t.lines) - 1; i >= 0; i-- {
		l := t.lines[i]
		if slice != "" && l.Slice != slice {
			continue
		}
		for _, n := range names {
			if l.Event == n {
				return i
			}
		}
	}
	return -1
}

// buildEventNames are the lines that show a ticket is being built: the work
// a session does between the claim and a slice going green.
var buildEventNames = []string{
	journal.EventClaim, journal.EventRelease, journal.EventDispatch, journal.EventVerifyPending,
	journal.EventOracle, journal.EventVerified, journal.EventVerifyError, journal.EventQuestion,
	journal.EventAnswer, journal.EventStall, journal.EventRequeue, journal.EventResult,
	journal.EventFixSlice,
}

// verifying lists the slices whose latest verify-pending has had no result
// since: a worker is running (or died; the stall shows that).
func (t *ticket) verifying() map[string]bool {
	out := map[string]bool{}
	for _, slice := range t.sliceIDsFromLines() {
		p := t.latest(slice, journal.EventVerifyPending)
		if p < 0 {
			continue
		}
		if t.latest(slice, journal.EventOracle, journal.EventVerified, journal.EventVerifyError) < p {
			out[slice] = true
		}
	}
	return out
}

// sliceIDsFromLines are the slices the journal names, in order of first
// appearance.
func (t *ticket) sliceIDsFromLines() []string {
	seen := map[string]bool{}
	var out []string
	for _, l := range t.lines {
		if l.Slice != "" && !seen[l.Slice] {
			seen[l.Slice] = true
			out = append(out, l.Slice)
		}
	}
	return out
}

// questionRow is a question the builder asked and, once there is one, its
// answer.
type questionRow struct {
	ID, Slice, Text, Session, At string
	Open                         bool
	Answer, Provenance           string
}

// questions are the ticket's questions in the order they were asked. The text
// is the question line's; whether one is open is the store's word (status)
// when it has been read, else the journal's (no answer line yet).
func (t *ticket) questions(zone *time.Location) []questionRow {
	byID := map[string]*questionRow{}
	var order []string
	for _, l := range t.lines {
		switch l.Event {
		case journal.EventQuestion:
			if l.Ref == "" {
				continue
			}
			if _, dup := byID[l.Ref]; !dup {
				order = append(order, l.Ref)
			}
			byID[l.Ref] = &questionRow{ID: l.Ref, Slice: l.Slice, Text: l.Summary, Session: l.Session, At: clock(l, zone), Open: true}
		case journal.EventAnswer:
			if q := byID[l.Ref]; q != nil {
				q.Open, q.Answer, q.Provenance = false, l.Summary, l.Provenance
			}
		}
	}
	if t.status != nil {
		for _, sq := range t.status.Questions {
			q := byID[sq.ID]
			if q == nil {
				q = &questionRow{ID: sq.ID, Slice: sq.Slice}
				byID[sq.ID] = q
				order = append(order, sq.ID)
			}
			q.Open = sq.Status == "open"
		}
	}
	out := make([]questionRow, 0, len(order))
	for _, id := range order {
		out = append(out, *byID[id])
	}
	return out
}

// openQuestions are the questions still waiting for an answer, of one slice
// when slice is not "".
func (t *ticket) openQuestions(zone *time.Location, slice string) []questionRow {
	var out []questionRow
	for _, q := range t.questions(zone) {
		if q.Open && (slice == "" || q.Slice == slice) {
			out = append(out, q)
		}
	}
	return out
}

// roundRow is one gate round.
type roundRow struct {
	N        int
	State    string // StateReviewing, StateFindings or StateReady
	At       string
	Reviewer string
}

// rounds are the gate rounds the journal records, closed ones by their
// number, and the round open now (opened and not yet closed) last.
func (t *ticket) rounds(zone *time.Location) []roundRow {
	closed := map[int]roundRow{}
	var order []int
	lastClosed := -1
	for i, l := range t.lines {
		if l.Event != journal.EventGateRound && l.Event != journal.EventGateClean {
			continue
		}
		state := StateFindings
		if l.Event == journal.EventGateClean {
			state = StateReady
		}
		n := l.Attempt
		if n == 0 {
			n = len(order) + 1
		}
		if _, dup := closed[n]; !dup {
			order = append(order, n)
		}
		closed[n] = roundRow{N: n, State: state, At: clock(l, zone), Reviewer: l.Session}
		lastClosed = i
	}
	sort.Ints(order)
	out := make([]roundRow, 0, len(order)+1)
	for _, n := range order {
		out = append(out, closed[n])
	}
	if open := t.latest("", journal.EventGateOpen); open > lastClosed {
		n := 1
		if len(order) > 0 {
			n = order[len(order)-1] + 1
		}
		out = append(out, roundRow{N: n, State: StateReviewing, At: clock(t.lines[open], zone)})
	}
	return out
}

// askRow is an ask a gate round left for a person, and what the person has
// decided about it.
type askRow struct {
	ID, Risk, Where, Title, Why string
	Decision, DecidedBy         string // "", keep or dismiss; person or agent
}

// asks are the asks status lists, each with the latest triage line for it
// that a round has not closed since: a round applies the decisions made before
// it closed, and an ask it left again is undecided (the rule internal/phase
// reads). They are known only once a status has been read.
func (t *ticket) asks() []askRow {
	if t.status == nil {
		return nil
	}
	decisions := map[string]events.Envelope{}
	for _, l := range t.lines {
		switch {
		case l.Event == journal.EventGateRound || l.Event == journal.EventGateClean:
			decisions = map[string]events.Envelope{}
		case l.Event == journal.EventTriage && l.Ref != "":
			decisions[l.Ref] = l
		}
	}
	out := make([]askRow, 0, len(t.status.Asks))
	for _, a := range t.status.Asks {
		row := askRow{ID: a.ID, Risk: a.Risk, Where: a.Where, Title: a.Title, Why: a.Why}
		if d, ok := decisions[a.ID]; ok {
			row.Decision, row.DecidedBy = d.Outcome, d.Provenance
		}
		out = append(out, row)
	}
	return out
}

// publishInfo is what the journal says of the publish.
type publishInfo struct {
	BodyFor, Branch         string // the head the body was rendered for, the branch
	ApprovedFor, ApprovedBy string // the head a person approved, and who
	PR, PROutcome           string
	Done                    bool
}

func (t *ticket) publish() publishInfo {
	var p publishInfo
	for _, l := range t.lines {
		switch l.Event {
		case journal.EventPrBody:
			p.BodyFor, p.Branch = l.Commit, firstOf(l.Branch, p.Branch)
		case journal.EventPublishApproved:
			p.ApprovedFor, p.ApprovedBy, p.Branch = l.Commit, l.Provenance, firstOf(l.Branch, p.Branch)
		case journal.EventPr:
			if l.URL != "" {
				p.PR, p.PROutcome = l.URL, l.Outcome
			}
			p.Branch = firstOf(l.Branch, p.Branch)
		case journal.EventPublishDone:
			p.Done = true
		}
	}
	return p
}

func firstOf(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
}

// sliceRow is a slice as the build screen lists it.
type sliceRow struct {
	ID, State, BlockedBy, Question string
	// Workspace and Oracle are what slices.yaml names for the slice, FromGate the
	// round a fix slice came from; all three are empty once only the journal is
	// known.
	Workspace, Oracle string
	FromGate          int
	Attempts          int
	Last              string // what the latest line about it says
	LastAt            string
	LastState         string // the state that line is drawn in
}

// relevantSliceEvents are the lines worth showing as a slice's latest.
var relevantSliceEvents = []string{
	journal.EventClaim, journal.EventRelease, journal.EventVerifyPending, journal.EventOracle,
	journal.EventVerified, journal.EventVerifyError, journal.EventQuestion, journal.EventAnswer,
	journal.EventStall, journal.EventRequeue, journal.EventNote,
}

// slices are the ticket's slices: the store's list when a status has been
// read (its states are the store's, with verifying laid over a slice a worker
// is checking), else the slices the journal names.
func (t *ticket) slices(zone *time.Location) []sliceRow {
	verifying := t.verifying()
	var out []sliceRow
	if t.status != nil && len(t.status.Slices) > 0 {
		for _, s := range t.status.Slices {
			out = append(out, sliceRow{
				ID: s.ID, State: s.State, BlockedBy: s.BlockedBy, Question: s.Question, Attempts: s.Attempts,
				Workspace: s.Workspace, Oracle: s.Oracle, FromGate: s.FromGate,
			})
		}
	} else {
		for _, id := range t.sliceIDsFromLines() {
			out = append(out, sliceRow{ID: id, State: t.guessSliceState(id)})
		}
	}
	for i := range out {
		r := &out[i]
		if verifying[r.ID] && (r.State == StateQueued || r.State == StateBuilding) {
			r.State = StateVerifying
		}
		if p := t.latest(r.ID, relevantSliceEvents...); p >= 0 {
			l := t.lines[p]
			r.Last, r.LastAt, r.LastState = describe(l), clock(l, zone), eventState(l)
		}
	}
	return out
}

// guessSliceState is the state of a slice from its lines alone, for a ticket
// whose status has not been read: verified means green, a question nobody
// answered means needs-input, a stall since the last claim means stalled, a
// claim means building.
func (t *ticket) guessSliceState(slice string) string {
	v := t.latest(slice, journal.EventVerified)
	q := t.latest(slice, journal.EventQuestion)
	a := t.latest(slice, journal.EventAnswer)
	stall := t.latest(slice, journal.EventStall)
	claim := t.latest(slice, journal.EventClaim, journal.EventDispatch, journal.EventRequeue)
	switch {
	case q >= 0 && q > a && q > v:
		return StateNeedsInput
	case stall >= 0 && stall > claim && stall > v:
		return StateStalled
	case v >= 0 && v > claim:
		return StateGreen
	case claim >= 0 || t.latest(slice, journal.EventVerifyPending, journal.EventOracle) >= 0:
		return StateBuilding
	}
	return StateQueued
}

// describe is one line's account of itself.
func describe(l events.Envelope) string {
	who := ""
	if l.Provenance != "" {
		who = " (" + l.Provenance + ")"
	}
	switch l.Event {
	case journal.EventClaim:
		return "claimed" + by(l.Session)
	case journal.EventRelease:
		return "released" + by(l.Session)
	case journal.EventVerifyPending:
		return "verify pending " + short(l.Commit)
	case journal.EventOracle:
		if l.Outcome == "pass" {
			return "oracle pass " + short(l.Commit)
		}
		return strings.TrimSpace("oracle " + l.Outcome + " " + firstOf(l.Summary, l.Signature))
	case journal.EventVerified:
		return "verified " + short(l.Commit)
	case journal.EventVerifyError:
		return strings.TrimSpace("verify error " + l.Summary)
	case journal.EventQuestion:
		return strings.TrimSpace(l.Ref + " " + l.Summary)
	case journal.EventAnswer:
		return strings.TrimSpace(l.Ref + " answered" + who + " " + l.Summary)
	case journal.EventNote:
		return "note " + l.Summary
	case journal.EventStall:
		return strings.TrimSpace("stalled " + firstOf(l.Summary, l.Outcome))
	case journal.EventGateOpen:
		return "gate round opened"
	case journal.EventGateRound:
		return roundWord(l, "findings")
	case journal.EventGateClean:
		return roundWord(l, "clean")
	case journal.EventTriage:
		return strings.TrimSpace(l.Ref + " " + l.Outcome + who + " " + l.Summary)
	case journal.EventPrBody:
		return "pr body rendered " + short(l.Commit)
	case journal.EventPublishApproved:
		return "publish approved " + short(l.Commit) + who
	case journal.EventPr:
		return strings.TrimSpace("pr " + l.Outcome + " " + l.URL)
	case journal.EventPublishDone:
		return "published"
	}
	return strings.TrimSpace(l.Event + " " + firstOf(l.Outcome, l.Summary))
}

func by(session string) string {
	if session == "" {
		return ""
	}
	return " by " + session
}

func roundWord(l events.Envelope, what string) string {
	if l.Attempt > 0 {
		return "round " + strconv.Itoa(l.Attempt) + ": " + what
	}
	return "round: " + what
}

// eventState is the state a line is drawn in.
func eventState(l events.Envelope) string {
	switch l.Event {
	case journal.EventClaim, journal.EventDispatch, journal.EventRequeue, journal.EventFixSlice:
		return StateBuilding
	case journal.EventVerifyPending:
		return StateVerifying
	case journal.EventOracle:
		if l.Outcome == "pass" {
			return StateGreen
		}
		return StateStalled
	case journal.EventVerified, journal.EventGateClean, journal.EventPublishDone:
		return StateGreen
	case journal.EventVerifyError, journal.EventStall:
		return StateStalled
	case journal.EventQuestion:
		return StateNeedsInput
	case journal.EventAnswer, journal.EventApproval, journal.EventRevalidate:
		return StateOK
	case journal.EventGateOpen, journal.EventGateReviewer:
		return StateReviewing
	case journal.EventGateRound:
		return StateFindings
	case journal.EventTriage:
		if l.Outcome == "keep" {
			return StateKept
		}
		return StateDismissed
	case journal.EventPrBody, journal.EventPublishApproved, journal.EventPr:
		return StatePublishing
	}
	return ""
}

// phase is where the ticket stands, in one state and a few words: derived
// from the journal, and from the store's word on its slices once a status
// has been read.
func (t *ticket) phase(zone *time.Location) (state, label string) {
	pub := t.publish()
	lastRound := t.latest("", journal.EventGateOpen, journal.EventGateRound, journal.EventGateClean)
	build := t.latest("", buildEventNames...)

	switch {
	case pub.Done:
		return StatePublished, "published"
	case lastRound >= 0 && t.lines[lastRound].Event == journal.EventGateClean:
		if t.latest("", journal.EventPrBody, journal.EventPublishApproved, journal.EventPr) > lastRound {
			return StatePublishing, "publishing"
		}
		return StateReady, "ready to publish"
	case lastRound >= 0 && t.lines[lastRound].Event == journal.EventGateOpen:
		return StateReviewing, "in review"
	case lastRound >= 0 && t.lines[lastRound].Event == journal.EventGateRound && build < lastRound:
		return StateFindings, "review found asks"
	}

	if len(t.openQuestions(zone, "")) > 0 {
		return StateNeedsInput, "needs input"
	}
	if t.status != nil && t.status.State != "" {
		switch t.status.State {
		case "stalled":
			return StateStalled, "stalled"
		case "parked":
			return StateNeedsInput, "needs input"
		case "env-blocked":
			return StateEnvBlocked, "env blocked"
		case "green":
			return StateGreen, "built"
		}
		if len(t.verifying()) > 0 {
			return StateVerifying, "verifying"
		}
		return StateBuilding, "building"
	}
	for _, id := range t.sliceIDsFromLines() {
		if t.guessSliceState(id) == StateStalled {
			return StateStalled, "stalled"
		}
	}
	if len(t.verifying()) > 0 {
		return StateVerifying, "verifying"
	}
	if build >= 0 {
		return StateBuilding, "building"
	}
	return StateQueued, firstOf(t.rosterState, "queued")
}
