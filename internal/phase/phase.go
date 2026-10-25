// Package phase derives where a ticket stands: which of its four phases
// (brief, build, review and publish) is current, whether that phase can start
// now, why not, the prompt that starts it, and the sub-steps of each phase.
// Every command that advises the next step reads this one derivation, so a
// session, a person and the TUI get the same answer.
//
// Derive is pure. It takes the facts the store holds (the brief and its
// approval, the slices and their states, the outstanding asks, the round open
// now, the blockers that have not published) and the ticket's journal lines,
// and returns an answer; the caller decides how to read the store. The package
// imports only the journal's line type, never the store's readers, so the TUI,
// which reads no store, can use it.
//
// A phase has no start or end line of its own. The lines jig already writes
// mark them: the approval line, the claim and verification lines, the gate
// lines, and the pr-body, publish-approved, pr, changelog and publish-done
// lines.
package phase

import (
	"fmt"
	"sort"
	"strings"

	"github.com/develdeco/jig/internal/journal"
)

// Name is one of the four phases of a ticket.
type Name string

// The phases, in the order a ticket goes through them.
const (
	Brief   Name = "brief"
	Build   Name = "build"
	Review  Name = "review"
	Publish Name = "publish"
)

// Names are the phases in order.
var Names = [4]Name{Brief, Build, Review, Publish}

// Parse is the phase a word names, and false for a word that names none.
func Parse(s string) (Name, bool) {
	for _, n := range Names {
		if string(n) == strings.ToLower(strings.TrimSpace(s)) {
			return n, true
		}
	}
	return "", false
}

// index is the position of n among Names, -1 for a name that is none of them.
func (n Name) index() int {
	for i, have := range Names {
		if have == n {
			return i
		}
	}
	return -1
}

// Prompt is what to ask an agent to run phase p of ticket: the words a person
// types, and the words a refusal names when it says what makes a ticket ready.
func Prompt(p Name, ticket string) string {
	switch p {
	case Brief:
		return "Plan " + ticket
	case Build:
		return "Build " + ticket
	case Review:
		return "Review " + ticket
	case Publish:
		return "Publish " + ticket
	}
	return ""
}

// The states of a phase.
const (
	// StateDone: the phase is finished, and so is every phase before it.
	StateDone = "done"
	// StateCurrent: the first phase that is not done.
	StateCurrent = "current"
	// StatePending: a phase after the current one.
	StatePending = "pending"
)

// The states of a sub-step.
const (
	SubDone     = "done"
	SubActive   = "active"
	SubWaiting  = "waiting"
	SubNeedsYou = "needs-you"
	SubFailed   = "failed"
	SubPending  = "pending"
)

// The readiness codes: why the phase asked for cannot start now. A code is the
// same wherever it is read: `jig next`, the claim and gate refusals, the TUI.
const (
	// CodeBriefNotApproved: the brief is not approved (or changed after it
	// was), so the build waits for it.
	CodeBriefNotApproved = "PHASE_BRIEF_NOT_APPROVED"
	// CodeBlocked: a blocker of the ticket has not published (or, stacked,
	// has no clean gate round).
	CodeBlocked = "PHASE_BLOCKED"
	// CodeNotBuilt: a slice is short of green, or a verification is in
	// flight, so the review waits for the build.
	CodeNotBuilt = "PHASE_NOT_BUILT"
	// CodeRoundOpen: a gate round is open, so the review is in progress.
	CodeRoundOpen = "PHASE_ROUND_OPEN"
	// CodeAsksUndecided: a round left asks that no person has decided, so the
	// next round waits for them.
	CodeAsksUndecided = "PHASE_ASKS_UNDECIDED"
	// CodeNotReviewed: no clean round on the current head, so the publish
	// waits for the review.
	CodeNotReviewed = "PHASE_NOT_REVIEWED"
	// CodeDone: the ticket is published.
	CodeDone = "PHASE_DONE"
	// CodeClosed: the ticket was closed without a pull request (`jig close`),
	// so no phase of it can run until `jig reopen`.
	CodeClosed = "PHASE_CLOSED"
)

// The approval of the brief, as the store holds it.
const (
	ApprovalNone    = "none"
	ApprovalCurrent = "current"
	ApprovalChanged = "changed"
)

// The states of a slice that Derive reads. They are the store's.
const (
	sliceQueued     = "queued"
	sliceBuilding   = "building"
	sliceGreen      = "green"
	sliceNeedsInput = "needs-input"
	sliceEnvBlocked = "env-blocked"
	sliceStalled    = "stalled"
)

// Sub is one sub-step of a phase.
type Sub struct {
	// Key is stable: "drafted", "slices", "approved", "slice:<id>",
	// "round:<n>", "asks", "fixes", "body", "approval", "pr", "changelog",
	// "done".
	Key string
	// Label is the sub-step in words: "brief drafted", "slice a", "round 1".
	Label string
	// State is one of the Sub states: done, active, waiting, needs-you,
	// failed or pending.
	State string
	// Detail is a few words on where it stands: "5 sections", "verified
	// d7101d0", "1 fix, 1 ask".
	Detail string
}

// Phase is one phase of a ticket.
type Phase struct {
	Name Name
	// State is done, current or pending.
	State string
	// Text is where the phase stands, in a few words.
	Text string
	Subs []Sub
}

// Slice is a slice as Derive reads it.
type Slice struct {
	ID string
	// State is the store's: queued, building, green, needs-input,
	// env-blocked or stalled.
	State string
	Goal  string
	// BlockedBy are the slices this one waits for.
	BlockedBy []string
	// FromGate is the round that queued a fix slice, 0 for a planned slice.
	FromGate int
	// OpenQuestion is whether a question of the slice has no answer yet.
	OpenQuestion bool
}

// Ask is a finding a round left for a person. Decision is "" when nobody has
// decided it, else keep or dismiss. Derive reads it from the triage lines when
// the caller leaves it empty: a decision is pending (applied by the next round)
// when its line comes after the last gate-round or gate-clean line, and an
// older one is spent.
type Ask struct {
	ID        string
	Decision  string
	DecidedBy string
	Pending   bool
}

// Blocker is a blocker of the ticket that stops its claim: Reason says why in
// a sentence, and Prompt is what to ask to meet it ("" when nothing a session
// can do meets it).
type Blocker struct {
	Ticket string
	Kind   string
	Reason string
	Prompt string
}

// VerdictClean is the verdict of a round that found nothing: the store's word
// (report.yaml), the one publish checks. Any other verdict is a round that did
// not close the review.
const VerdictClean = "clean"

// Round is a gate round the store records as closed.
type Round struct {
	N       int
	Verdict string
	// Reviewer is the session that reviewed it, "" when the report names none.
	Reviewer string
}

// OpenRound is the round open now: opened, and not yet closed by `review
// done`.
type OpenRound struct {
	Round int
	// Reviewer is the session that took the round, "" until one did.
	Reviewer string
}

// Facts are what the store knows of a ticket, gathered by the caller.
type Facts struct {
	Ticket string
	// Adopted: the ticket took over a branch, which Branch names. With no
	// brief on record there is nothing to approve.
	Adopted bool
	Branch  string

	BriefExists bool
	Sections    int
	// Approval is ApprovalNone, ApprovalCurrent or ApprovalChanged.
	Approval   string
	ApprovedBy string
	// ApprovedHow is the approval's provenance: "person" or "agent".
	ApprovedHow string

	Slices []Slice
	// Rounds are the closed gate rounds the store records. They are the word on
	// a round's verdict where the journal's gate-round and gate-clean lines are
	// missing or disagree: publish reads the store's, so the advice does.
	Rounds   []Round
	Asks     []Ask
	Blockers []Blocker
	// Open is the round open now, nil when none is. The journal's gate-open
	// line is not enough: a gate that failed on its oracles wrote it and
	// opened nothing. OpenRoundIn reads the journal for a caller that has no
	// store to ask.
	Open      *OpenRound
	Published bool
	// Closed says how the ticket was closed without a pull request (`jig
	// close`), such as "covered by develdeco/jig#82" or "not planned"; "" while
	// it is open. A closed ticket is done whatever its journal says, and no
	// phase of it can run.
	Closed string

	// SessionName words a session id wherever the answer names a session (who
	// took a round, who holds a slice). Nil is the first eight characters of the
	// id; a caller that knows its sessions by role (the TUI) passes its own words.
	SessionName func(id string) string
}

// Ticket is where a ticket stands.
type Ticket struct {
	Phases [4]Phase
	// Current is the first phase not done, the last when all are.
	Current Name
	// Ready is whether Current can start now.
	Ready bool
	// Code is why not, when it cannot: one of the Code constants.
	Code string
	// Reason is the Code in a sentence for a person.
	Reason string
	// Prompt is what to ask an agent to run the current phase, "" when the
	// ticket is done.
	Prompt string

	d *digest
}

// Of is the phase named n.
func (t Ticket) Of(n Name) Phase {
	if i := n.index(); i >= 0 {
		return t.Phases[i]
	}
	return Phase{}
}

// Asked is the answer to "can phase p run now?" for a caller that wants p
// whatever the ticket is at (a skill, a person's prompt). Re-running a phase
// that is done is warned, never refused.
type Asked struct {
	Phase Name
	// Ready is whether p can run now. A done phase is ready, with a Warning.
	Ready bool
	// Code and Reason say why p cannot run now.
	Code   string
	Reason string
	// Warning says p is done already, and what to ask instead.
	Warning string
	// Prompt is the prompt of the phase the ticket is at, the one that moves
	// it on.
	Prompt string
}

// For answers whether phase p can run now. For the current phase that is
// Ready; for a phase already done it is a warning with the prompt that moves
// the ticket on; for a phase after the current one it is the code that says
// what that phase waits for. For a closed ticket no phase can run: every one is
// answered with CodeClosed.
func (t Ticket) For(p Name) Asked {
	a := Asked{Phase: p, Prompt: t.Prompt}
	i := p.index()
	switch {
	case i < 0:
		a.Reason = fmt.Sprintf("%q is none of the phases (brief, build, review, publish)", string(p))
	case t.d != nil && t.d.f.Closed != "":
		a.Code, a.Reason = CodeClosed, t.d.closedReason()
	case t.Phases[i].State == StateDone:
		a.Ready = true
		a.Warning = fmt.Sprintf("%s is done (%s)", p, t.Phases[i].Text)
		if t.Prompt != "" {
			a.Warning += "; prompt: " + t.Prompt
		}
	case p == t.Current:
		a.Ready, a.Code, a.Reason = t.Ready, t.Code, t.Reason
	default:
		a.Code, a.Reason = t.d.waitFor(t.Current)
	}
	return a
}

// round is one gate round the journal records as closed.
type round struct {
	N       int
	Clean   bool
	Session string
}

// buildEvents are the lines that show a ticket is being built, and that move
// the head a clean round reviewed.
var buildEvents = map[string]bool{
	journal.EventClaim: true, journal.EventRelease: true, journal.EventDispatch: true, journal.EventVerifyPending: true,
	journal.EventOracle: true, journal.EventVerified: true, journal.EventVerifyError: true, journal.EventQuestion: true,
	journal.EventAnswer: true, journal.EventStall: true, journal.EventRequeue: true, journal.EventResult: true,
	journal.EventFixSlice: true,
}

// startedEvents are the lines that show work began, whatever phase it was in.
var startedEvents = map[string]bool{
	journal.EventGateOpen: true, journal.EventGateReviewer: true, journal.EventGateRound: true, journal.EventGateClean: true,
	journal.EventPrBody: true, journal.EventPublishApproved: true, journal.EventPr: true, journal.EventChangelog: true,
	journal.EventPublishDone: true,
}

// sliceView is a slice with what the journal says of it.
type sliceView struct {
	Slice
	verifying      bool
	pendingCommit  string
	verifiedCommit string
	claimed        bool
	claimedBy      string
}

// publishView is what the journal says of the publish since the last round
// closed: a publish of an earlier head does not count for this one.
type publishView struct {
	body           bool
	bodyFor        string
	approvedPerson bool
	approvedAgent  bool
	pr, prOutcome  string
	changelog      bool
}

// digest is the journal read once, with the facts laid over it.
type digest struct {
	f          Facts
	lines      []journal.Line
	started    bool
	rounds     []round
	lastClosed int
	builtSince bool
	closedAt   map[int]int
	approval   int
	asks       []Ask
	slices     []sliceView
	pub        publishView
}

// OpenRoundIn is the round the journal says is open: the latest gate-open line
// with no gate-round or gate-clean line after it, its number, and the session
// of the latest gate-reviewer line for it ("" when none). A caller that can
// read the store asks it instead (verifydeliver.ReadStatus), since a gate that
// failed on its oracles leaves a gate-open line and no open round.
func OpenRoundIn(lines []journal.Line) *OpenRound {
	open, closed, closedN := -1, -1, 0
	for i, l := range lines {
		switch l.Event {
		case journal.EventGateOpen:
			open = i
		case journal.EventGateRound, journal.EventGateClean:
			closed = i
			closedN++
		}
	}
	if open < 0 || open < closed {
		return nil
	}
	n := lines[open].Attempt
	if n == 0 {
		n = closedN + 1
	}
	o := &OpenRound{Round: n}
	for _, l := range lines[open:] {
		if l.Event == journal.EventGateReviewer && l.Attempt == n {
			o.Reviewer = l.Session
		}
	}
	return o
}

// takenEvents are the lines that show a slice was taken up: a claim (or the
// dispatch of the era before claims), or a verification of what it built.
var takenEvents = map[string]bool{
	journal.EventClaim: true, journal.EventDispatch: true, journal.EventVerifyPending: true,
	journal.EventOracle: true, journal.EventVerified: true, journal.EventVerifyError: true,
}

// EverClaimed is whether the journal shows any slice was ever claimed or
// verified. A release does not undo it: the slice was taken up once, so the
// build has begun whatever the slices' states say now. It tells a ticket whose
// slices nobody has started from one with a build under way; the lines that
// only follow from a claim (a question, a stall, a requeue) add nothing to it.
func EverClaimed(lines []journal.Line) bool {
	for _, l := range lines {
		if takenEvents[l.Event] {
			return true
		}
	}
	return false
}

// Derive works out where a ticket stands from the facts the store holds and the
// ticket's journal lines, in order. A phase is done only when every phase
// before it is: a review that found fixes takes the build back, and the build
// is not done again until they are green. A published ticket is done whatever
// it says of the earlier phases, and so is a closed one, with no prompt.
func Derive(f Facts, lines []journal.Line) Ticket {
	d := newDigest(f, lines)
	pub := f.Published || f.Closed != ""

	briefP, own := d.brief()
	briefDone := pub || own
	buildP, own := d.build(briefDone)
	buildDone := pub || own && briefDone
	reviewP, own := d.review(buildDone)
	reviewDone := pub || own && buildDone
	publishP, own := d.publish(reviewDone)
	publishDone := pub || own && reviewDone

	t := Ticket{Phases: [4]Phase{briefP, buildP, reviewP, publishP}, d: d}
	done := [4]bool{briefDone, buildDone, reviewDone, publishDone}
	cur := len(done) - 1
	for i, dn := range done {
		if !dn {
			cur = i
			break
		}
	}
	for i := range t.Phases {
		switch {
		case done[i]:
			t.Phases[i].State = StateDone
		case i == cur:
			t.Phases[i].State = StateCurrent
		default:
			t.Phases[i].State = StatePending
		}
	}
	t.Current = Names[cur]
	d.ready(&t)
	if f.Closed != "" {
		t.stand()
	}
	return t
}

// stand puts the sub-steps of a closed ticket at rest: nothing of it is being
// worked, waits for anyone or can start, so a step that was active, waiting or
// owed to the person is pending, with no word on where it stood.
func (t *Ticket) stand() {
	for i := range t.Phases {
		for j, s := range t.Phases[i].Subs {
			switch s.State {
			case SubActive, SubWaiting, SubNeedsYou:
				t.Phases[i].Subs[j].State, t.Phases[i].Subs[j].Detail = SubPending, ""
			}
		}
	}
}

func newDigest(f Facts, lines []journal.Line) *digest {
	d := &digest{f: f, lines: lines, lastClosed: -1, approval: -1}
	d.readRounds()
	for i, l := range lines {
		switch {
		case l.Event == journal.EventApproval:
			d.approval = i
		case buildEvents[l.Event] || startedEvents[l.Event]:
			d.started = true
		}
	}
	// The head moved when the build went on after the last round closed.
	if last, ok := d.lastRound(); ok {
		if at, ok := d.closedAt[last.N]; ok {
			for _, l := range lines[at+1:] {
				if buildEvents[l.Event] {
					d.builtSince = true
				}
			}
		}
	}
	// The store shows work began where the journal does not (a slice past
	// queued, a round on record, a fix slice a round queued).
	for _, s := range f.Slices {
		if s.State != sliceQueued || s.FromGate > 0 {
			d.started = true
		}
	}
	if len(f.Rounds) > 0 {
		d.started = true
	}
	d.readAsks()
	d.readSlices()
	d.readPublish()
	return d
}

// readRounds reads the closed rounds by number: the journal's lines, then the
// store's reports over them, since the store is what publish reads. closedAt
// holds the line each journaled round closed at, and lastClosed the last such
// line.
func (d *digest) readRounds() {
	d.closedAt = map[int]int{}
	byN := map[int]round{}
	for i, l := range d.lines {
		if l.Event != journal.EventGateRound && l.Event != journal.EventGateClean {
			continue
		}
		n := l.Attempt
		if n == 0 {
			n = len(byN) + 1
		}
		byN[n] = round{N: n, Clean: l.Event == journal.EventGateClean, Session: l.Session}
		d.closedAt[n] = i
		d.lastClosed = i
	}
	for _, r := range d.f.Rounds {
		have := byN[r.N]
		byN[r.N] = round{N: r.N, Clean: r.Verdict == VerdictClean, Session: firstOf(have.Session, r.Reviewer)}
	}
	for _, r := range byN {
		d.rounds = append(d.rounds, r)
	}
	sort.Slice(d.rounds, func(i, j int) bool { return d.rounds[i].N < d.rounds[j].N })
}

// readAsks lays each ask's latest pending decision over it. A decision the
// caller gave stands; otherwise the latest triage line for the ask after the
// last round closed is its decision, and an older one is spent.
func (d *digest) readAsks() {
	for _, a := range d.f.Asks {
		if a.Decision == "" {
			for i := len(d.lines) - 1; i > d.lastClosed; i-- {
				l := d.lines[i]
				if l.Event == journal.EventTriage && l.Ref == a.ID && (l.Outcome == "keep" || l.Outcome == "dismiss") {
					a.Decision, a.DecidedBy, a.Pending = l.Outcome, l.Provenance, true
					break
				}
			}
		}
		d.asks = append(d.asks, a)
	}
}

// readSlices lays the journal over each slice: whether a worker is verifying
// it, the commit that verified it, who holds it.
func (d *digest) readSlices() {
	for _, s := range d.f.Slices {
		v := sliceView{Slice: s}
		pending, resolved, claim, release := -1, -1, -1, -1
		for i, l := range d.lines {
			if l.Slice != s.ID {
				continue
			}
			switch l.Event {
			case journal.EventVerifyPending:
				pending, v.pendingCommit = i, l.Commit
			case journal.EventVerified:
				resolved, v.verifiedCommit = i, l.Commit
			case journal.EventOracle, journal.EventVerifyError:
				resolved = i
			case journal.EventClaim:
				claim, v.claimedBy = i, l.Session
			case journal.EventRelease:
				release = i
			}
		}
		v.verifying = pending >= 0 && pending > resolved
		v.claimed = claim >= 0 && claim > release
		d.slices = append(d.slices, v)
	}
}

// readPublish reads the publish lines since the last round closed, all of them
// once the ticket is published.
func (d *digest) readPublish() {
	from := d.lastClosed + 1
	if d.f.Published {
		from = 0
	}
	p := &d.pub
	for _, l := range d.lines[from:] {
		switch l.Event {
		case journal.EventPrBody:
			p.body, p.bodyFor = true, l.Commit
		case journal.EventPublishApproved:
			if l.Provenance == "person" {
				p.approvedPerson = true
			} else {
				p.approvedAgent = true
			}
		case journal.EventPr:
			if l.URL != "" {
				p.pr, p.prOutcome = l.URL, l.Outcome
			}
		case journal.EventChangelog:
			p.changelog = true
		}
	}
}

// planned is how many slices the brief planned, as against the fix slices a
// round queued.
func (d *digest) planned() int {
	n := 0
	for _, s := range d.f.Slices {
		if s.FromGate == 0 {
			n++
		}
	}
	return n
}

// who words who approved the brief: the name and, when known, how.
func (d *digest) who() string {
	by, how := d.f.ApprovedBy, d.f.ApprovedHow
	if d.approval >= 0 {
		l := d.lines[d.approval]
		if how == "" {
			how = l.Provenance
		}
		if by == "" {
			by = personName(l.Summary)
		}
	}
	if by == "" {
		by = "someone"
	}
	if how != "" {
		return by + " (" + how + ")"
	}
	return by
}

// personName is the name in an approval line's "Name <email>" summary.
func personName(summary string) string {
	if i := strings.Index(summary, " <"); i > 0 {
		return summary[:i]
	}
	return summary
}

// brief is the brief phase and whether its own work is done: approved, or past
// the point of asking (the work started without it, or there is nothing to
// approve).
func (d *digest) brief() (Phase, bool) {
	f := d.f
	p := Phase{Name: Brief}
	planned := d.planned()
	counts := ""
	if f.BriefExists {
		counts = fmt.Sprintf(" · %s, %s", count(f.Sections, "section"), count(planned, "slice"))
	}
	switch {
	case f.BriefExists && f.Approval == ApprovalCurrent:
		p.Text = "approved by " + d.who() + counts
		p.Subs = d.briefSubs(planned, "")
		return p, true
	case f.BriefExists && f.Approval == ApprovalChanged:
		p.Text = "changed after " + d.who() + " approved it: it needs approving again"
		p.Subs = d.briefSubs(planned, "changed")
		return p, false
	case f.BriefExists && d.started:
		p.Text = "never approved: the work started without it" + counts
		p.Subs = d.briefSubs(planned, "never")
		return p, true
	case f.BriefExists && planned == 0:
		p.Text = "being drafted" + counts
		p.Subs = d.briefSubs(planned, "")
		return p, false
	case f.BriefExists:
		p.Text = "drafted" + counts + " · waiting for your approval"
		p.Subs = d.briefSubs(planned, "")
		return p, false
	case f.Adopted:
		p.Text = "none: the ticket took over a branch"
		if f.Branch != "" {
			p.Text = "none: the ticket took over branch " + f.Branch
		}
		return p, true
	case d.started || len(f.Slices) > 0:
		p.Text = "no brief on record"
		return p, true
	}
	p.Text = "being drafted"
	p.Subs = d.briefSubs(planned, "")
	return p, false
}

// briefSubs are the sub-steps of a brief that exists or is being drafted:
// drafted, slices written, approved. odd says what is wrong with the approval:
// "changed" or "never".
func (d *digest) briefSubs(planned int, odd string) []Sub {
	f := d.f
	subs := []Sub{
		{Key: "drafted", Label: "brief drafted", State: SubDone},
		{Key: "slices", Label: "slices written", State: SubDone, Detail: count(planned, "slice")},
		{Key: "approved", Label: "brief approved", State: SubPending},
	}
	if !f.BriefExists {
		subs[0].State = SubActive
		subs[1].State, subs[1].Detail = SubPending, ""
		return subs
	}
	if f.Sections > 0 {
		subs[0].Detail = count(f.Sections, "section")
	}
	if planned == 0 {
		subs[1].State, subs[1].Detail = SubActive, ""
		return subs
	}
	switch {
	case odd == "changed":
		subs[2].State, subs[2].Detail = SubNeedsYou, "changed after "+d.who()+" approved it"
	case odd == "never":
		subs[2].State, subs[2].Detail = SubNeedsYou, "never approved"
	case f.Approval == ApprovalCurrent:
		subs[2].State, subs[2].Detail = SubDone, d.who()
	default:
		subs[2].State, subs[2].Detail = SubNeedsYou, "waits for your approval"
	}
	return subs
}

// build is the build phase and whether all its slices are green, none of them
// being verified.
func (d *digest) build(prevDone bool) (Phase, bool) {
	f := d.f
	p := Phase{Name: Build}
	if len(d.slices) == 0 {
		switch {
		case f.Adopted && prevDone:
			// An adopted branch is built: it has no slices until a review
			// sends fixes back.
			p.Text = "built on a branch the ticket adopted"
			if f.Branch != "" {
				p.Text = "built on branch " + f.Branch + ", adopted"
			}
			return p, true
		case !prevDone:
			p.Text = "after the brief"
		default:
			p.Text = "no slices yet"
		}
		return p, false
	}
	green := 0
	for _, s := range d.slices {
		state, detail := d.sliceState(s)
		p.Subs = append(p.Subs, Sub{Key: "slice:" + s.ID, Label: sliceLabel(s.Slice), State: state, Detail: detail})
		if s.State == sliceGreen && !s.verifying {
			green++
		}
	}
	n := len(d.slices)
	switch {
	case green == n:
		p.Text = fmt.Sprintf("%s green, each verified by jig", count(n, "slice"))
	case !prevDone && d.untouched():
		p.Text = "after the brief"
	default:
		p.Text = fmt.Sprintf("%d of %d green", green, n)
		if focus := d.buildFocus(); focus != "" {
			p.Text += " · " + focus
		}
	}
	return p, green == n
}

// untouched is whether no slice has been started.
func (d *digest) untouched() bool {
	for _, s := range d.slices {
		if s.State != sliceQueued || s.verifying {
			return false
		}
	}
	return true
}

// sliceLabel is how a slice is named in the sub-steps: a fix slice says which
// round queued it.
func sliceLabel(s Slice) string {
	if s.FromGate > 0 {
		return fmt.Sprintf("fix slice %s (round %d)", s.ID, s.FromGate)
	}
	return "slice " + s.ID
}

// sliceState is the sub-step state and detail of a slice, with the journal's
// word on a verification laid over the store's state.
func (d *digest) sliceState(s sliceView) (state, detail string) {
	switch {
	case s.OpenQuestion || s.State == sliceNeedsInput:
		return SubNeedsYou, "waits for your answer"
	case s.State == sliceStalled:
		return SubFailed, "stalled"
	case s.verifying:
		return SubActive, "verifying " + short(s.pendingCommit)
	case s.State == sliceGreen:
		return SubDone, strings.TrimSpace("verified " + short(s.verifiedCommit))
	case s.State == sliceEnvBlocked:
		return SubWaiting, "waits for its environment"
	case s.State == sliceBuilding:
		if s.claimed && s.claimedBy != "" {
			return SubActive, "claimed by " + d.session(s.claimedBy)
		}
		return SubActive, "claimed"
	case s.State == sliceQueued:
		if waits := d.unmet(s.Slice); len(waits) > 0 {
			return SubWaiting, "waits for " + strings.Join(waits, ", ")
		}
		return SubPending, "ready to build"
	}
	return SubPending, s.State
}

// unmet are the slices s waits for that are not green.
func (d *digest) unmet(s Slice) []string {
	var out []string
	for _, dep := range s.BlockedBy {
		for _, o := range d.f.Slices {
			if o.ID == dep && o.State != sliceGreen {
				out = append(out, dep)
			}
		}
	}
	return out
}

// buildFocus is what matters most in a build under way: a question for the
// person, a stall, a wait for the environment, a verification, a builder.
func (d *digest) buildFocus() string {
	for _, want := range []string{"needs", "stalled", "env", "verifying", "building"} {
		for _, s := range d.slices {
			switch {
			case want == "needs" && (s.OpenQuestion || s.State == sliceNeedsInput):
				return "slice " + s.ID + " needs your answer"
			case want == "stalled" && s.State == sliceStalled:
				return "slice " + s.ID + " stalled"
			case want == "env" && s.State == sliceEnvBlocked:
				return "slice " + s.ID + " waits for its environment"
			case want == "verifying" && s.verifying:
				return "verifying slice " + s.ID + " at " + short(s.pendingCommit)
			case want == "building" && s.State == sliceBuilding:
				return "slice " + s.ID + " is being built"
			}
		}
	}
	return ""
}

// undecided are the asks nobody has decided (or whose decision is spent).
func (d *digest) undecided() []Ask {
	var out []Ask
	for _, a := range d.asks {
		if a.Decision == "" {
			out = append(out, a)
		}
	}
	return out
}

// lastRound is the highest closed round, and whether there is one.
func (d *digest) lastRound() (round, bool) {
	if len(d.rounds) == 0 {
		return round{}, false
	}
	return d.rounds[len(d.rounds)-1], true
}

// review is the review phase and whether its last round is clean on a head
// nothing has changed since.
func (d *digest) review(prevDone bool) (Phase, bool) {
	p := Phase{Name: Review}
	last, hasRound := d.lastRound()
	open := d.f.Open
	undecided := d.undecided()
	fixesOpen, fixesTotal := d.fixesOf(last.N)

	for _, r := range d.rounds {
		p.Subs = append(p.Subs, d.roundSub(r))
	}
	if open != nil {
		sub := Sub{Key: fmt.Sprintf("round:%d", open.Round), Label: fmt.Sprintf("round %d", open.Round), State: SubActive, Detail: "opened, waiting for a reviewer"}
		if open.Reviewer != "" {
			sub.Detail = "taken by " + d.session(open.Reviewer)
		}
		p.Subs = append(p.Subs, sub)
	}
	if len(d.asks) > 0 {
		sub := Sub{Key: "asks", Label: "asks", State: SubNeedsYou, Detail: fmt.Sprintf("%d to decide", len(undecided))}
		if len(undecided) == 0 {
			sub.State, sub.Detail = SubWaiting, fmt.Sprintf("%d decided, applied at the next round", len(d.asks))
		}
		p.Subs = append(p.Subs, sub)
	}
	if fixesOpen > 0 {
		p.Subs = append(p.Subs, Sub{Key: "fixes", Label: "fixes", State: SubActive,
			Detail: fmt.Sprintf("%d of %d green", fixesTotal-fixesOpen, fixesTotal)})
	}
	clean := hasRound && last.Clean && !d.builtSince && open == nil
	if open == nil && !clean {
		next := last.N + 1
		sub := Sub{Key: fmt.Sprintf("round:%d", next), Label: fmt.Sprintf("round %d", next), State: SubPending}
		if prevDone {
			sub.Detail = "ready to open"
		}
		p.Subs = append(p.Subs, sub)
	}

	switch {
	case open != nil:
		p.Text = fmt.Sprintf("round %d open · ", open.Round)
		if open.Reviewer != "" {
			p.Text += d.session(open.Reviewer) + " is reading the change"
		} else {
			p.Text += "waiting for a reviewer"
		}
	case !hasRound && !prevDone:
		p.Text = "after the build"
	case !hasRound:
		p.Text = "ready for a fresh session to review the change"
	case last.Clean && d.builtSince:
		p.Text = fmt.Sprintf("round %d was clean, but the build changed since: review again", last.N)
	case last.Clean:
		p.Text = fmt.Sprintf("round %d clean", last.N)
		if last.Session != "" {
			p.Text += " · by " + d.session(last.Session)
		}
	case len(undecided) > 0:
		p.Text = fmt.Sprintf("round %d left %s · %d wait for your decision", last.N, count(len(d.asks), "ask"), len(undecided))
	case fixesOpen > 0:
		p.Text = fmt.Sprintf("round %d sent fixes back · the build is on them", last.N)
	case len(d.asks) > 0:
		p.Text = fmt.Sprintf("round %d's asks are decided · round %d applies them", last.N, last.N+1)
	default:
		p.Text = fmt.Sprintf("round %d's fixes are green · round %d is next", last.N, last.N+1)
	}
	return p, clean
}

// fixesOf are how many of the fix slices round n queued are not green yet, and
// how many it queued.
func (d *digest) fixesOf(n int) (open, total int) {
	if n == 0 {
		return 0, 0
	}
	for _, s := range d.slices {
		if s.FromGate != n {
			continue
		}
		total++
		if s.State != sliceGreen || s.verifying {
			open++
		}
	}
	return open, total
}

// roundSub is the sub-step of a closed round: clean, or the fixes and asks it
// left.
func (d *digest) roundSub(r round) Sub {
	sub := Sub{Key: fmt.Sprintf("round:%d", r.N), Label: fmt.Sprintf("round %d", r.N), State: SubDone, Detail: "clean"}
	if r.Clean {
		return sub
	}
	fixes := 0
	for _, s := range d.f.Slices {
		if s.FromGate == r.N {
			fixes++
		}
	}
	asks := 0
	for _, a := range d.asks {
		if roundOfFinding(a.ID) == r.N {
			asks++
		}
	}
	var parts []string
	if fixes > 0 {
		parts = append(parts, count(fixes, "fix"))
	}
	if asks > 0 {
		parts = append(parts, count(asks, "ask"))
	}
	sub.Detail = "findings"
	if len(parts) > 0 {
		sub.Detail = strings.Join(parts, ", ")
	}
	return sub
}

// roundOfFinding is the round a finding id names ("r2-f1" is round 2), 0 for an
// id that names none.
func roundOfFinding(id string) int {
	if len(id) < 2 || id[0] != 'r' {
		return 0
	}
	n := 0
	for _, c := range id[1:] {
		if c < '0' || c > '9' {
			break
		}
		n = n*10 + int(c-'0')
	}
	return n
}

// publish is the publish phase and whether the ticket is published.
func (d *digest) publish(prevDone bool) (Phase, bool) {
	p := Phase{Name: Publish}
	pub := d.pub
	steps := []struct {
		key, label, detail string
		done               bool
	}{
		{"body", "body", short(pub.bodyFor), pub.body},
		{"approval", "approval", "", pub.approvedPerson},
		{"pr", "pull request", pub.pr, pub.pr != ""},
		{"changelog", "changelog", "", pub.changelog},
		{"done", "published", "", d.f.Published},
	}
	first := true
	for _, s := range steps {
		sub := Sub{Key: s.key, Label: s.label, State: SubPending, Detail: s.detail}
		switch {
		case s.done:
			sub.State = SubDone
		case first && prevDone && !d.f.Published && s.key == "approval":
			sub.State, sub.Detail = SubNeedsYou, "waits for your approval"
			if pub.approvedAgent {
				sub.Detail = "an agent approved it; yours is still needed"
			}
		case first && prevDone && !d.f.Published:
			sub.State = SubActive
		}
		if !s.done {
			first = false
		}
		p.Subs = append(p.Subs, sub)
	}

	switch {
	case d.f.Closed != "":
		p.Text = "closed · " + d.f.Closed
		return p, true
	case d.f.Published:
		p.Text = "published"
		if pub.pr != "" {
			p.Text = strings.TrimSpace("pull request " + firstOf(pub.prOutcome, "opened") + " · " + pub.pr)
		}
		return p, true
	case !prevDone:
		p.Text = "after a clean review"
	case pub.approvedPerson:
		p.Text = "approved · pushing and opening the pull request"
	case pub.body:
		p.Text = "body written · waiting for your approval"
	default:
		p.Text = "ready: approve it, push the branch and open the pull request"
	}
	return p, false
}

// ready fills in whether the current phase can start, and why not.
func (d *digest) ready(t *Ticket) {
	f := d.f
	t.Prompt = Prompt(t.Current, f.Ticket)
	t.Ready = true
	switch {
	case f.Closed != "":
		t.Ready, t.Code, t.Reason, t.Prompt = false, CodeClosed, d.closedReason(), ""
	case f.Published:
		t.Ready, t.Code, t.Reason, t.Prompt = false, CodeDone, f.Ticket+" is published", ""
	case t.Current == Build && len(f.Blockers) > 0:
		reasons := make([]string, len(f.Blockers))
		for i, b := range f.Blockers {
			reasons[i] = b.Reason
		}
		t.Ready, t.Code, t.Reason = false, CodeBlocked, strings.Join(reasons, "; ")
		t.Prompt = f.Blockers[0].Prompt
	case t.Current == Review && f.Open != nil:
		t.Ready, t.Code, t.Reason = false, CodeRoundOpen, d.roundOpenReason()
	case t.Current == Review && len(d.undecided()) > 0:
		t.Ready, t.Code, t.Reason = false, CodeAsksUndecided, d.asksReason()
	}
}

// closedReason says the ticket is closed, and what makes it open again.
func (d *digest) closedReason() string {
	return fmt.Sprintf("%s is closed (%s): `jig reopen %s` takes it back", d.f.Ticket, d.f.Closed, d.f.Ticket)
}

// waitFor is the code and reason a phase after cur waits for while the ticket
// is at cur.
func (d *digest) waitFor(cur Name) (code, reason string) {
	switch cur {
	case Brief:
		return CodeBriefNotApproved, d.briefReason()
	case Build:
		return CodeNotBuilt, d.builtReason()
	case Review:
		switch {
		case d.f.Open != nil:
			return CodeRoundOpen, d.roundOpenReason()
		case len(d.undecided()) > 0:
			return CodeAsksUndecided, d.asksReason()
		}
		return CodeNotReviewed, d.notReviewedReason()
	}
	return "", ""
}

// briefReason says why the brief does not let the build start.
func (d *digest) briefReason() string {
	f := d.f
	switch {
	case !f.BriefExists:
		return f.Ticket + " has no brief yet"
	case f.Approval == ApprovalChanged:
		return fmt.Sprintf("the brief of %s changed after it was approved", f.Ticket)
	case d.planned() == 0:
		return f.Ticket + " has a brief but no slices yet"
	}
	return fmt.Sprintf("the brief of %s is not approved", f.Ticket)
}

// builtReason says what the build still has to finish.
func (d *digest) builtReason() string {
	f := d.f
	if len(d.slices) == 0 {
		return f.Ticket + " has no slices built yet"
	}
	for _, s := range d.slices {
		if s.verifying {
			return fmt.Sprintf("slice %s is being verified at %s", s.ID, short(s.pendingCommit))
		}
	}
	green := 0
	var behind []string
	for _, s := range d.slices {
		if s.State == sliceGreen {
			green++
		} else {
			behind = append(behind, s.ID)
		}
	}
	return fmt.Sprintf("%d of %d slices are green; %s %s not", green, len(d.slices), strings.Join(behind, ", "), plural(len(behind), "is", "are"))
}

// roundOpenReason says which round is open and who has it.
func (d *digest) roundOpenReason() string {
	o := d.f.Open
	if o.Reviewer != "" {
		return fmt.Sprintf("round %d of %s is open: %s is reading the change", o.Round, d.f.Ticket, d.session(o.Reviewer))
	}
	return fmt.Sprintf("round %d of %s is open and waits for a reviewer", o.Round, d.f.Ticket)
}

// asksReason says how many asks wait for a person.
func (d *digest) asksReason() string {
	n := len(d.undecided())
	return fmt.Sprintf("%s of %s %s for a person to decide (`jig triage %s <id> keep|dismiss`)", count(n, "ask"), d.f.Ticket, plural(n, "waits", "wait"), d.f.Ticket)
}

// notReviewedReason says why no clean round stands for the head.
func (d *digest) notReviewedReason() string {
	f := d.f
	last, ok := d.lastRound()
	switch {
	case !ok:
		return fmt.Sprintf("no gate round has reviewed %s yet", f.Ticket)
	case last.Clean && d.builtSince:
		return fmt.Sprintf("the build of %s changed after clean round %d: the head is not reviewed", f.Ticket, last.N)
	case last.Clean:
		return fmt.Sprintf("round %d of %s is clean but not on this head", last.N, f.Ticket)
	}
	return fmt.Sprintf("round %d of %s found things to fix: a clean round must follow the fixes", last.N, f.Ticket)
}

// count words n of a noun: "1 slice", "3 slices".
func count(n int, noun string) string {
	return fmt.Sprintf("%d %s", n, plural(n, noun, noun+"s"))
}

func plural(n int, one, many string) string {
	if n == 1 {
		return one
	}
	return many
}

// short is the first seven characters of a commit.
func short(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

// session words a session id: the caller's words when it gave a way to say
// them, else the first eight characters.
func (d *digest) session(id string) string {
	if d.f.SessionName != nil {
		if s := d.f.SessionName(id); s != "" {
			return s
		}
	}
	return shortSession(id)
}

// shortSession is the first eight characters of a session id.
func shortSession(id string) string {
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

func firstOf(ss ...string) string {
	for _, s := range ss {
		if s != "" {
			return s
		}
	}
	return ""
}
