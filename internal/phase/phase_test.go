package phase

import (
	"reflect"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/journal"
)

const tk = "SHOP-1"

func ln(event string, mod ...func(*journal.Line)) journal.Line {
	l := journal.Line{Event: event}
	for _, m := range mod {
		m(&l)
	}
	return l
}

func onSlice(id string) func(*journal.Line) { return func(l *journal.Line) { l.Slice = id } }
func attempt(n int) func(*journal.Line)     { return func(l *journal.Line) { l.Attempt = n } }
func by(session string) func(*journal.Line) { return func(l *journal.Line) { l.Session = session } }
func commit(sha string) func(*journal.Line) { return func(l *journal.Line) { l.Commit = sha } }
func ref(r string) func(*journal.Line)      { return func(l *journal.Line) { l.Ref = r } }
func outcome(o string) func(*journal.Line)  { return func(l *journal.Line) { l.Outcome = o } }
func provenance(p string) func(*journal.Line) {
	return func(l *journal.Line) { l.Provenance = p }
}

// approved is a ticket with a brief that a person approved and three slices.
func approved() Facts {
	return Facts{
		Ticket: tk, BriefExists: true, Sections: 4, Approval: ApprovalCurrent, ApprovedBy: "Dana", ApprovedHow: "person",
		Slices: []Slice{{ID: "a", State: "queued"}, {ID: "b", State: "queued", BlockedBy: []string{"a"}}, {ID: "c", State: "queued"}},
	}
}

// allGreen is approved with every slice green.
func allGreen() Facts {
	f := approved()
	for i := range f.Slices {
		f.Slices[i].State = "green"
	}
	return f
}

// built is the journal of a build that went green: a claim and a verified line
// per slice.
func built() []journal.Line {
	var out []journal.Line
	out = append(out, ln(journal.EventApproval, provenance("person")))
	for _, id := range []string{"a", "b", "c"} {
		out = append(out, ln(journal.EventClaim, onSlice(id), by("s-build")), ln(journal.EventVerified, onSlice(id), commit("abcdef0123")))
	}
	return out
}

func subKeys(p Phase) []string {
	var out []string
	for _, s := range p.Subs {
		out = append(out, s.Key)
	}
	return out
}

func sub(t *testing.T, p Phase, key string) Sub {
	t.Helper()
	for _, s := range p.Subs {
		if s.Key == key {
			return s
		}
	}
	t.Fatalf("%s has no sub-step %q; it has %v", p.Name, key, subKeys(p))
	return Sub{}
}

// TestDerive is the table of where a ticket stands: for each state of the
// store and the journal, the current phase, whether it is ready, the code and
// the prompt.
func TestDerive(t *testing.T) {
	t.Parallel()
	blocked := approved()
	blocked.Blockers = []Blocker{{Ticket: "SHOP-0", Kind: "merged", Reason: "SHOP-1 waits for SHOP-0, which has not published", Prompt: "Build SHOP-0"}}
	drafted := approved()
	drafted.Approval = ApprovalNone
	changed := approved()
	changed.Approval = ApprovalChanged
	noSlices := Facts{Ticket: tk, BriefExists: true, Sections: 2}
	fixQueued := allGreen()
	fixQueued.Slices = append(fixQueued.Slices, Slice{ID: "r1-fix-1", State: "queued", FromGate: 1})
	fixGreen := allGreen()
	fixGreen.Slices = append(fixGreen.Slices, Slice{ID: "r1-fix-1", State: "green", FromGate: 1})
	asked := fixGreen
	asked.Asks = []Ask{{ID: "r1-f2"}}
	open := allGreen()
	open.Open = &OpenRound{Round: 1}
	published := allGreen()
	published.Published = true
	adopted := Facts{Ticket: tk, Adopted: true, Branch: "feature/x"}
	started := approved()
	started.Approval = ApprovalNone
	started.Slices[0].State = "building"
	oldTicket := Facts{Ticket: tk, Slices: []Slice{{ID: "a", State: "queued"}}}

	round1Findings := append(built(), ln(journal.EventGateOpen, attempt(1)), ln(journal.EventGateRound, attempt(1), by("s-rev")), ln(journal.EventFixSlice, onSlice("r1-fix-1"), attempt(1)))
	round1Verified := append(append([]journal.Line{}, round1Findings...), ln(journal.EventVerified, onSlice("r1-fix-1"), commit("1234567abc")))
	clean := append(built(), ln(journal.EventGateOpen, attempt(1)), ln(journal.EventGateClean, attempt(1), by("s-rev")))
	moved := append(append([]journal.Line{}, clean...), ln(journal.EventClaim, onSlice("c")), ln(journal.EventVerified, onSlice("c"), commit("fedcba9876")))

	for _, tc := range []struct {
		name    string
		facts   Facts
		lines   []journal.Line
		current Name
		ready   bool
		code    string
		prompt  string
	}{
		{"a new ticket", Facts{Ticket: tk}, nil, Brief, true, "", "Plan SHOP-1"},
		{"a brief with no slices", noSlices, nil, Brief, true, "", "Plan SHOP-1"},
		{"a drafted brief waits for approval", drafted, nil, Brief, true, "", "Plan SHOP-1"},
		{"a brief changed after its approval", changed, []journal.Line{ln(journal.EventApproval)}, Brief, true, "", "Plan SHOP-1"},
		{"approved, nothing started", approved(), []journal.Line{ln(journal.EventApproval, provenance("person"))}, Build, true, "", "Build SHOP-1"},
		{"approved but blocked", blocked, nil, Build, false, CodeBlocked, "Build SHOP-0"},
		{"work started without an approval", started, []journal.Line{ln(journal.EventClaim, onSlice("a"))}, Build, true, "", "Build SHOP-1"},
		{"an old ticket with no brief", oldTicket, nil, Build, true, "", "Build SHOP-1"},
		{"building", withState(approved(), "a", "green"), built()[:3], Build, true, "", "Build SHOP-1"},
		{"all green, no round", allGreen(), built(), Review, true, "", "Review SHOP-1"},
		{"a round open", open, append(built(), ln(journal.EventGateOpen, attempt(1))), Review, false, CodeRoundOpen, "Review SHOP-1"},
		{"a fix round: the fix slices are queued", fixQueued, round1Findings, Build, true, "", "Build SHOP-1"},
		{"a fix round: the fixes are green", fixGreen, round1Verified, Review, true, "", "Review SHOP-1"},
		{"asks nobody decided", asked, round1Verified, Review, false, CodeAsksUndecided, "Review SHOP-1"},
		{"asks decided, applied by the next round", asked, append(append([]journal.Line{}, round1Verified...), ln(journal.EventTriage, ref("r1-f2"), outcome("keep"), provenance("person"))), Review, true, "", "Review SHOP-1"},
		{"a decision spent by a later round", asked, append([]journal.Line{ln(journal.EventTriage, ref("r1-f2"), outcome("keep"))}, round1Verified...), Review, false, CodeAsksUndecided, "Review SHOP-1"},
		{"a clean round on this head", allGreen(), clean, Publish, true, "", "Publish SHOP-1"},
		{"a clean round on a moved head", allGreen(), moved, Review, true, "", "Review SHOP-1"},
		{"a verification in flight after green", allGreen(), append(built(), ln(journal.EventVerifyPending, onSlice("a"), commit("0badf00d11"))), Build, true, "", "Build SHOP-1"},
		{"published", published, append(clean, ln(journal.EventPublishDone)), Publish, false, CodeDone, ""},
		{"an adopted branch", adopted, nil, Review, true, "", "Review SHOP-1"},
	} {
		got := Derive(tc.facts, tc.lines)
		if got.Current != tc.current || got.Ready != tc.ready || got.Code != tc.code || got.Prompt != tc.prompt {
			t.Errorf("%s: at %s ready=%v code=%q prompt=%q, want %s ready=%v code=%q prompt=%q (reason %q)",
				tc.name, got.Current, got.Ready, got.Code, got.Prompt, tc.current, tc.ready, tc.code, tc.prompt, got.Reason)
		}
		if !got.Ready && got.Reason == "" {
			t.Errorf("%s: not ready, and no reason says why", tc.name)
		}
		states := ""
		for _, p := range got.Phases {
			states += p.State[:1]
		}
		if want := stateWord(tc.current, got); states != want {
			t.Errorf("%s: phase states %q, want %q", tc.name, states, want)
		}
	}
}

// stateWord is the phase states a ticket at cur should have: done before it,
// current at it, pending after, and all done for a published ticket.
func stateWord(cur Name, got Ticket) string {
	if got.Code == CodeDone {
		return "dddd"
	}
	out := ""
	for _, n := range Names {
		switch {
		case n.index() < cur.index():
			out += "d"
		case n == cur:
			out += "c"
		default:
			out += "p"
		}
	}
	return out
}

func withState(f Facts, id, state string) Facts {
	f.Slices = append([]Slice(nil), f.Slices...)
	for i := range f.Slices {
		if f.Slices[i].ID == id {
			f.Slices[i].State = state
		}
	}
	return f
}

// TestForNamesWhatAPhaseWaitsFor: a phase asked for before its time says what
// it waits for, in the code the refusals use; a phase already done is warned,
// not refused, with the prompt that moves the ticket on.
func TestForNamesWhatAPhaseWaitsFor(t *testing.T) {
	t.Parallel()
	drafted := approved()
	drafted.Approval = ApprovalNone
	changed := approved()
	changed.Approval = ApprovalChanged
	noBrief := Facts{Ticket: tk}
	open := allGreen()
	open.Open = &OpenRound{Round: 2, Reviewer: "s-reviewer-long-id"}
	moved := append(append([]journal.Line{}, built()...), ln(journal.EventGateClean, attempt(1)), ln(journal.EventClaim, onSlice("c")), ln(journal.EventVerified, onSlice("c"), commit("fedcba9876")))
	findings := append(built(), ln(journal.EventGateRound, attempt(1)))
	fixed := allGreen()
	fixed.Slices = append(fixed.Slices, Slice{ID: "r1-fix-1", State: "queued", FromGate: 1})

	for _, tc := range []struct {
		name   string
		facts  Facts
		lines  []journal.Line
		asked  Name
		ready  bool
		code   string
		reason string
	}{
		{"build before the brief is approved", drafted, nil, Build, false, CodeBriefNotApproved, "the brief of SHOP-1 is not approved"},
		{"build after the brief changed", changed, nil, Build, false, CodeBriefNotApproved, "changed after it was approved"},
		{"build with no brief", noBrief, nil, Build, false, CodeBriefNotApproved, "SHOP-1 has no brief yet"},
		{"review before the build is done", approved(), nil, Review, false, CodeNotBuilt, "0 of 3 slices are green; a, b, c are not"},
		{"publish before the build is done", withState(approved(), "a", "green"), built()[:3], Publish, false, CodeNotBuilt, "1 of 3 slices are green; b, c are not"},
		{"review while a verification runs", allGreen(), append(built(), ln(journal.EventVerifyPending, onSlice("a"), commit("0badf00d11"))), Review, false, CodeNotBuilt, "slice a is being verified at 0badf00"},
		{"publish with no round", allGreen(), built(), Publish, false, CodeNotReviewed, "no gate round has reviewed SHOP-1 yet"},
		{"publish while a round is open", open, built(), Publish, false, CodeRoundOpen, "round 2 of SHOP-1 is open: s-review is reading the change"},
		{"publish after a round of findings", allGreen(), findings, Publish, false, CodeNotReviewed, "round 1 of SHOP-1 found things to fix"},
		{"publish on a head that moved", allGreen(), moved, Publish, false, CodeNotReviewed, "the build of SHOP-1 changed after clean round 1"},
		{"review in a fix round", fixed, append(findings, ln(journal.EventFixSlice, onSlice("r1-fix-1"), attempt(1))), Review, false, CodeNotBuilt, "r1-fix-1"},
		{"the current phase is ready", allGreen(), built(), Review, true, "", ""},
	} {
		got := Derive(tc.facts, tc.lines).For(tc.asked)
		if got.Ready != tc.ready || got.Code != tc.code || !strings.Contains(got.Reason, tc.reason) {
			t.Errorf("%s: ready=%v code=%q reason=%q, want ready=%v code=%q reason containing %q", tc.name, got.Ready, got.Code, got.Reason, tc.ready, tc.code, tc.reason)
		}
		if got.Warning != "" {
			t.Errorf("%s: a phase not yet done was warned: %q", tc.name, got.Warning)
		}
	}

	done := Derive(allGreen(), built()).For(Build)
	if !done.Ready || done.Code != "" || done.Warning != "build is done (3 slices green, each verified by jig); prompt: Review SHOP-1" {
		t.Errorf("a finished phase: %+v, want ready with the warning and the prompt that moves on", done)
	}
	if got := Derive(allGreen(), built()).For(Name("deploy")); got.Ready || got.Reason == "" {
		t.Errorf("a name that is no phase: %+v, want not ready, with a reason", got)
	}
}

// TestBriefSubSteps: the brief is drafted, its slices written, then approved;
// the approval says who and how.
func TestBriefSubSteps(t *testing.T) {
	t.Parallel()
	drafted := approved()
	drafted.Approval = ApprovalNone
	got := Derive(drafted, nil).Of(Brief)
	want := []Sub{
		{"drafted", "brief drafted", SubDone, "4 sections"},
		{"slices", "slices written", SubDone, "3 slices"},
		{"approved", "brief approved", SubNeedsYou, "waits for your approval"},
	}
	if !reflect.DeepEqual(got.Subs, want) || got.State != StateCurrent {
		t.Errorf("drafted brief: %+v, want %+v, current", got, want)
	}
	if got.Text != "drafted · 4 sections, 3 slices · waiting for your approval" {
		t.Errorf("drafted brief text = %q", got.Text)
	}

	got = Derive(approved(), nil).Of(Brief)
	if s := sub(t, got, "approved"); s.State != SubDone || s.Detail != "Dana (person)" {
		t.Errorf("approved sub = %+v, want done by Dana (person)", s)
	}
	if got.Text != "approved by Dana (person) · 4 sections, 3 slices" || got.State != StateDone {
		t.Errorf("approved brief = %+v", got)
	}

	// The approval line says who and how when the store's record does not.
	bare := approved()
	bare.ApprovedBy, bare.ApprovedHow = "", ""
	line := ln(journal.EventApproval, provenance("agent"))
	line.Summary = "Dana Q <dana@example.invalid>"
	if s := sub(t, Derive(bare, []journal.Line{line}).Of(Brief), "approved"); s.Detail != "Dana Q (agent)" {
		t.Errorf("approved by the line: %+v, want Dana Q (agent)", s)
	}

	changed := approved()
	changed.Approval = ApprovalChanged
	if s := sub(t, Derive(changed, nil).Of(Brief), "approved"); s.State != SubNeedsYou || !strings.Contains(s.Detail, "changed after Dana (person) approved it") {
		t.Errorf("changed approval sub = %+v", s)
	}

	never := started()
	got = Derive(never, []journal.Line{ln(journal.EventClaim, onSlice("a"))}).Of(Brief)
	if s := sub(t, got, "approved"); s.State != SubNeedsYou || s.Detail != "never approved" || got.State != StateDone {
		t.Errorf("work without an approval: %+v, want the brief done with the approval still owed", got)
	}

	fresh := Derive(Facts{Ticket: tk}, nil).Of(Brief)
	if s := sub(t, fresh, "drafted"); s.State != SubActive || fresh.Text != "being drafted" {
		t.Errorf("a new ticket: %+v, want the draft under way", fresh)
	}
	noSlices := Derive(Facts{Ticket: tk, BriefExists: true, Sections: 2}, nil).Of(Brief)
	if s := sub(t, noSlices, "slices"); s.State != SubActive {
		t.Errorf("a brief with no slices: %+v, want the slices under way", noSlices)
	}
}

func started() Facts {
	f := approved()
	f.Approval = ApprovalNone
	f.Slices[0].State = "building"
	return f
}

// TestAdoptedAndOldTicketsHaveNoBrief: a ticket that took over a branch, or has
// slices and no brief, is past the brief with nothing to show for it.
func TestAdoptedAndOldTicketsHaveNoBrief(t *testing.T) {
	t.Parallel()
	got := Derive(Facts{Ticket: tk, Adopted: true, Branch: "feature/x"}, nil)
	if b := got.Of(Brief); b.State != StateDone || len(b.Subs) != 0 || b.Text != "none: the ticket took over branch feature/x" {
		t.Errorf("adopted brief = %+v", b)
	}
	if b := got.Of(Build); b.State != StateDone || b.Text != "built on branch feature/x, adopted" {
		t.Errorf("adopted build = %+v", b)
	}
	old := Derive(Facts{Ticket: tk, Slices: []Slice{{ID: "a", State: "queued"}}}, nil).Of(Brief)
	if old.State != StateDone || len(old.Subs) != 0 || old.Text != "no brief on record" {
		t.Errorf("a ticket with no brief = %+v", old)
	}
}

// TestBuildSubStepsFollowTheSlices: one sub-step per slice in slices.yaml order,
// planned and fix slices alike, each with what the journal says of it.
func TestBuildSubStepsFollowTheSlices(t *testing.T) {
	t.Parallel()
	f := approved()
	f.Slices = []Slice{
		{ID: "a", State: "green"},
		{ID: "b", State: "building"},
		{ID: "c", State: "queued", BlockedBy: []string{"b"}},
		{ID: "d", State: "queued"},
		{ID: "e", State: "needs-input", OpenQuestion: true},
		{ID: "f", State: "stalled"},
		{ID: "g", State: "env-blocked"},
		{ID: "r1-fix-1", State: "building", FromGate: 1},
	}
	lines := []journal.Line{
		ln(journal.EventClaim, onSlice("a"), by("s-first")), ln(journal.EventVerified, onSlice("a"), commit("d7101d0aaaa")),
		ln(journal.EventClaim, onSlice("b"), by("0123456789abcdef")), ln(journal.EventVerifyPending, onSlice("b"), commit("9999999bbb")),
	}
	b := Derive(f, lines).Of(Build)
	want := []Sub{
		{"slice:a", "slice a", SubDone, "verified d7101d0"},
		{"slice:b", "slice b", SubActive, "verifying 9999999"},
		{"slice:c", "slice c", SubWaiting, "waits for b"},
		{"slice:d", "slice d", SubPending, "ready to build"},
		{"slice:e", "slice e", SubNeedsYou, "waits for your answer"},
		{"slice:f", "slice f", SubFailed, "stalled"},
		{"slice:g", "slice g", SubWaiting, "waits for its environment"},
		{"slice:r1-fix-1", "fix slice r1-fix-1 (round 1)", SubActive, "claimed"},
	}
	if !reflect.DeepEqual(b.Subs, want) {
		t.Errorf("build sub-steps:\n got %+v\nwant %+v", b.Subs, want)
	}
	if b.Text != "1 of 8 green · slice e needs your answer" {
		t.Errorf("build text = %q", b.Text)
	}

	// A claim without a verification names the holder, until the slice is released.
	f = approved()
	f.Slices = []Slice{{ID: "a", State: "building"}}
	held := []journal.Line{ln(journal.EventClaim, onSlice("a"), by("0123456789abcdef"))}
	if s := sub(t, Derive(f, held).Of(Build), "slice:a"); s.Detail != "claimed by 01234567" {
		t.Errorf("a held slice: %+v", s)
	}
	freed := append(held, ln(journal.EventRelease, onSlice("a")))
	if s := sub(t, Derive(f, freed).Of(Build), "slice:a"); s.Detail != "claimed" {
		t.Errorf("a released slice: %+v", s)
	}
}

// TestReviewSubSteps: a round is opened, taken by a reviewer, then closed with a
// summary; asks and open fixes get sub-steps while they last; the next round
// follows a round that was not clean.
func TestReviewSubSteps(t *testing.T) {
	t.Parallel()
	pending := func(f Facts, lines []journal.Line) Phase { return Derive(f, lines).Of(Review) }

	// Nothing reviewed: a round 1 waits.
	got := pending(allGreen(), built())
	if !reflect.DeepEqual(got.Subs, []Sub{{"round:1", "round 1", SubPending, "ready to open"}}) {
		t.Errorf("before a round: %+v", got.Subs)
	}
	if got.Text != "ready for a fresh session to review the change" {
		t.Errorf("review text before a round = %q", got.Text)
	}
	// ... but not before the build is done.
	if got := pending(approved(), nil); !reflect.DeepEqual(got.Subs, []Sub{{"round:1", "round 1", SubPending, ""}}) || got.Text != "after the build" {
		t.Errorf("review before the build: %+v", got)
	}

	// Round 1 opened, then taken.
	open := allGreen()
	open.Open = &OpenRound{Round: 1}
	got = pending(open, built())
	if s := sub(t, got, "round:1"); s.State != SubActive || s.Detail != "opened, waiting for a reviewer" || got.Text != "round 1 open · waiting for a reviewer" {
		t.Errorf("an open round: %+v / %q", s, got.Text)
	}
	open.Open = &OpenRound{Round: 1, Reviewer: "reviewer-session-1"}
	got = pending(open, built())
	if s := sub(t, got, "round:1"); s.Detail != "taken by reviewer" || got.Text != "round 1 open · reviewer is reading the change" {
		t.Errorf("a round a reviewer took: %+v / %q", s, got.Text)
	}

	// Round 1 found a fix and an ask; the fix is still being built.
	f := allGreen()
	f.Slices = append(f.Slices, Slice{ID: "r1-fix-1", State: "queued", FromGate: 1})
	f.Asks = []Ask{{ID: "r1-f2"}}
	lines := append(built(), ln(journal.EventGateOpen, attempt(1)), ln(journal.EventGateRound, attempt(1)), ln(journal.EventFixSlice, onSlice("r1-fix-1"), attempt(1)))
	got = pending(f, lines)
	wantKeys := []string{"round:1", "asks", "fixes", "round:2"}
	if !reflect.DeepEqual(subKeys(got), wantKeys) {
		t.Fatalf("after a round of findings: %v, want %v", subKeys(got), wantKeys)
	}
	if s := sub(t, got, "round:1"); s.State != SubDone || s.Detail != "1 fix, 1 ask" {
		t.Errorf("round 1 = %+v, want done with 1 fix, 1 ask", s)
	}
	if s := sub(t, got, "asks"); s.State != SubNeedsYou || s.Detail != "1 to decide" {
		t.Errorf("asks = %+v", s)
	}
	if s := sub(t, got, "fixes"); s.State != SubActive || s.Detail != "0 of 1 green" {
		t.Errorf("fixes = %+v", s)
	}
	if s := sub(t, got, "round:2"); s.State != SubPending || s.Detail != "" {
		// The fix is not built, so the next round cannot open yet.
		t.Errorf("round 2 = %+v, want pending with nothing to say", s)
	}

	// The ask is decided; the fix is green: the next round applies the decision.
	f.Slices[len(f.Slices)-1].State = "green"
	decided := append(append([]journal.Line{}, lines...), ln(journal.EventTriage, ref("r1-f2"), outcome("dismiss"), provenance("person")))
	got = pending(f, decided)
	if s := sub(t, got, "asks"); s.State != SubWaiting || s.Detail != "1 decided, applied at the next round" {
		t.Errorf("a decided ask = %+v", s)
	}
	if len(got.Subs) != 3 {
		t.Errorf("with the fix green there is no fixes sub-step: %v", subKeys(got))
	}

	// A clean round 2 ends it: no sub-step for a round 3.
	clean := append(append([]journal.Line{}, decided...), ln(journal.EventGateOpen, attempt(2)), ln(journal.EventGateClean, attempt(2), by("0123456789abcdef")))
	f.Asks = nil
	got = pending(f, clean)
	if s := sub(t, got, "round:2"); s.State != SubDone || s.Detail != "clean" {
		t.Errorf("round 2 = %+v", s)
	}
	for _, s := range got.Subs {
		if s.Key == "round:3" {
			t.Errorf("a clean round still lists round 3: %v", subKeys(got))
		}
	}
	if got.Text != "round 2 clean · by 01234567" || got.State != StateDone {
		t.Errorf("review after the clean round = %+v", got)
	}
}

// TestPublishSubStepsFollowTheFlow: body, approval, pull request, changelog,
// published, in the order publish writes them; the approval is the person's, and
// an agent's alone does not stand for it.
func TestPublishSubStepsFollowTheFlow(t *testing.T) {
	t.Parallel()
	clean := append(built(), ln(journal.EventGateOpen, attempt(1)), ln(journal.EventGateClean, attempt(1)))
	step := func(extra ...journal.Line) Phase {
		return Derive(allGreen(), append(append([]journal.Line{}, clean...), extra...)).Of(Publish)
	}
	states := func(p Phase) string {
		var out []string
		for _, s := range p.Subs {
			out = append(out, s.Key+"="+s.State)
		}
		return strings.Join(out, " ")
	}

	p := step()
	if want := "body=active approval=pending pr=pending changelog=pending done=pending"; states(p) != want {
		t.Errorf("a clean round: %s, want %s", states(p), want)
	}
	if p.Text != "ready: approve it, push the branch and open the pull request" || p.State != StateCurrent {
		t.Errorf("publish text = %q (%s)", p.Text, p.State)
	}

	p = step(ln(journal.EventPrBody, commit("abcdef0123")))
	if want := "body=done approval=needs-you pr=pending changelog=pending done=pending"; states(p) != want {
		t.Errorf("body written: %s, want %s", states(p), want)
	}
	if s := sub(t, p, "body"); s.Detail != "abcdef0" {
		t.Errorf("body detail = %q", s.Detail)
	}
	if p.Text != "body written · waiting for your approval" {
		t.Errorf("text = %q", p.Text)
	}

	p = step(ln(journal.EventPrBody, commit("abcdef0123")), ln(journal.EventPublishApproved, provenance("agent")))
	if s := sub(t, p, "approval"); s.State != SubNeedsYou || !strings.Contains(s.Detail, "an agent approved it") {
		t.Errorf("an agent's approval = %+v, want the person's still owed", s)
	}

	p = step(ln(journal.EventPrBody, commit("abcdef0123")), ln(journal.EventPublishApproved, provenance("person")))
	if want := "body=done approval=done pr=active changelog=pending done=pending"; states(p) != want {
		t.Errorf("approved: %s, want %s", states(p), want)
	}

	pr := ln(journal.EventPr, outcome("opened"))
	pr.URL = "https://example.invalid/o/r/pull/7"
	p = step(ln(journal.EventPrBody, commit("abcdef0123")), ln(journal.EventPublishApproved, provenance("person")), pr, ln(journal.EventChangelog))
	if want := "body=done approval=done pr=done changelog=done done=active"; states(p) != want {
		t.Errorf("changelog written: %s, want %s", states(p), want)
	}
	if s := sub(t, p, "pr"); s.Detail != pr.URL {
		t.Errorf("pr detail = %q", s.Detail)
	}

	f := allGreen()
	f.Published = true
	p = Derive(f, append(append(append([]journal.Line{}, clean...), pr, ln(journal.EventChangelog)), ln(journal.EventPublishDone))).Of(Publish)
	if want := "body=pending approval=pending pr=done changelog=done done=done"; states(p) != want {
		t.Errorf("published: %s, want %s", states(p), want)
	}
	if p.State != StateDone || p.Text != "pull request opened · "+pr.URL {
		t.Errorf("published phase = %+v", p)
	}

	// A body written for an earlier head does not count after a new round.
	later := append(append([]journal.Line{}, clean...), ln(journal.EventPrBody, commit("abcdef0123")), ln(journal.EventGateOpen, attempt(2)), ln(journal.EventGateClean, attempt(2)))
	if p := Derive(allGreen(), later).Of(Publish); sub(t, p, "body").State != SubActive {
		t.Errorf("after another clean round the body is stale: %s", states(p))
	}

	// Before the review is done, nothing in the publish is under way.
	if p := Derive(allGreen(), built()).Of(Publish); states(p) != "body=pending approval=pending pr=pending changelog=pending done=pending" || p.Text != "after a clean review" {
		t.Errorf("publish before the review: %s / %q", states(p), p.Text)
	}
}

// TestAnEarlierPhaseChangedTakesTheLaterOnesBack: a phase is done only while
// every phase before it is.
func TestAnEarlierPhaseChangedTakesTheLaterOnesBack(t *testing.T) {
	t.Parallel()
	changed := allGreen()
	changed.Approval = ApprovalChanged
	lines := append(built(), ln(journal.EventGateOpen, attempt(1)), ln(journal.EventGateClean, attempt(1)))
	got := Derive(changed, lines)
	if got.Current != Brief || got.Of(Build).State != StatePending || got.Of(Review).State != StatePending {
		t.Errorf("a brief changed after a clean round: current %s, build %s, review %s, want the brief current and the rest pending",
			got.Current, got.Of(Build).State, got.Of(Review).State)
	}
	if got.Prompt != "Plan SHOP-1" {
		t.Errorf("prompt = %q", got.Prompt)
	}
}

// TestPublishedIsDoneWhateverTheRest: a published ticket has four done phases
// and nothing to ask.
func TestPublishedIsDoneWhateverTheRest(t *testing.T) {
	t.Parallel()
	f := approved()
	f.Published = true
	got := Derive(f, []journal.Line{ln(journal.EventPublishDone)})
	for _, p := range got.Phases {
		if p.State != StateDone {
			t.Errorf("%s = %s, want done", p.Name, p.State)
		}
	}
	if got.Current != Publish || got.Ready || got.Code != CodeDone || got.Prompt != "" || got.Reason != "SHOP-1 is published" {
		t.Errorf("published ticket = %+v", got)
	}
	if w := got.For(Review); !w.Ready || !strings.HasPrefix(w.Warning, "review is done") {
		t.Errorf("asking for a phase of a published ticket: %+v, want a warning", w)
	}
}

// TestClosedIsDoneWithNoPrompt: a closed ticket has four done phases whatever
// its slices are, nothing to ask, and no phase that can run; its sub-steps are
// at rest, and asking for any phase is answered with the closure.
func TestClosedIsDoneWithNoPrompt(t *testing.T) {
	t.Parallel()
	f := approved() // three slices still queued, nothing built
	f.Closed = "covered by develdeco/jig#82"
	got := Derive(f, nil)
	for _, p := range got.Phases {
		if p.State != StateDone {
			t.Errorf("%s = %s, want done", p.Name, p.State)
		}
		for _, s := range p.Subs {
			switch s.State {
			case SubActive, SubWaiting, SubNeedsYou:
				t.Errorf("%s sub-step %s is %s, want a closed ticket at rest", p.Name, s.Key, s.State)
			}
		}
	}
	if got.Current != Publish || got.Ready || got.Code != CodeClosed || got.Prompt != "" {
		t.Errorf("closed ticket = %+v, want current publish, not ready, %s, no prompt", got, CodeClosed)
	}
	if want := "SHOP-1 is closed (covered by develdeco/jig#82): `jig reopen SHOP-1` takes it back"; got.Reason != want {
		t.Errorf("reason = %q, want %q", got.Reason, want)
	}
	if text := got.Of(Publish).Text; text != "closed · covered by develdeco/jig#82" {
		t.Errorf("publish text = %q", text)
	}
	for _, p := range Names {
		w := got.For(p)
		if w.Ready || w.Code != CodeClosed || w.Warning != "" || !strings.Contains(w.Reason, "jig reopen SHOP-1") {
			t.Errorf("For(%s) = %+v, want it refused as closed with no warning", p, w)
		}
	}

	// A ticket closed after it published is closed first.
	f.Published = true
	if got = Derive(f, []journal.Line{ln(journal.EventPublishDone)}); got.Code != CodeClosed {
		t.Errorf("a published ticket that was closed has code %s, want %s", got.Code, CodeClosed)
	}
}

// TestAsksTakeTheirDecisionFromTheTriageLines: a decision is pending when its
// line comes after the last round closed; the caller's own decision stands.
func TestAsksTakeTheirDecisionFromTheTriageLines(t *testing.T) {
	t.Parallel()
	f := allGreen()
	f.Asks = []Ask{{ID: "r1-f1"}, {ID: "r1-f2"}, {ID: "r1-f3", Decision: "keep", DecidedBy: "person", Pending: true}}
	lines := []journal.Line{
		ln(journal.EventTriage, ref("r1-f1"), outcome("dismiss")),
		ln(journal.EventGateRound, attempt(1)),
		ln(journal.EventTriage, ref("r1-f2"), outcome("keep"), provenance("agent")),
		ln(journal.EventTriage, ref("r1-f2"), outcome("dismiss"), provenance("person")),
		ln(journal.EventTriage, ref("r1-f3"), outcome("dismiss")),
	}
	d := newDigest(f, lines)
	got := map[string]Ask{}
	for _, a := range d.asks {
		got[a.ID] = a
	}
	want := map[string]Ask{
		"r1-f1": {ID: "r1-f1"},
		"r1-f2": {ID: "r1-f2", Decision: "dismiss", DecidedBy: "person", Pending: true},
		"r1-f3": {ID: "r1-f3", Decision: "keep", DecidedBy: "person", Pending: true},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("asks = %+v, want %+v", got, want)
	}
}

// TestOpenRoundInReadsTheJournal: the latest gate-open with no close after it,
// by its number, with the reviewer who took it.
func TestOpenRoundInReadsTheJournal(t *testing.T) {
	t.Parallel()
	if got := OpenRoundIn(nil); got != nil {
		t.Errorf("no lines: %+v", got)
	}
	lines := []journal.Line{ln(journal.EventGateOpen, attempt(1)), ln(journal.EventGateRound, attempt(1))}
	if got := OpenRoundIn(lines); got != nil {
		t.Errorf("a closed round: %+v", got)
	}
	lines = append(lines, ln(journal.EventGateOpen, attempt(2)))
	if got := OpenRoundIn(lines); got == nil || got.Round != 2 || got.Reviewer != "" {
		t.Errorf("round 2 opened: %+v", got)
	}
	lines = append(lines, ln(journal.EventGateReviewer, attempt(2), by("s-rev")))
	if got := OpenRoundIn(lines); got == nil || got.Reviewer != "s-rev" {
		t.Errorf("round 2 taken: %+v", got)
	}
	// A line with no attempt numbers the round after the closed ones.
	if got := OpenRoundIn([]journal.Line{ln(journal.EventGateRound), ln(journal.EventGateOpen)}); got == nil || got.Round != 2 {
		t.Errorf("an unnumbered round: %+v", got)
	}
}

// TestPromptsAndNames: the four prompts, and the phase a word names.
func TestPromptsAndNames(t *testing.T) {
	t.Parallel()
	for name, want := range map[Name]string{Brief: "Plan SHOP-1", Build: "Build SHOP-1", Review: "Review SHOP-1", Publish: "Publish SHOP-1"} {
		if got := Prompt(name, tk); got != want {
			t.Errorf("Prompt(%s) = %q, want %q", name, got, want)
		}
		if got, ok := Parse(" " + strings.ToUpper(string(name))); !ok || got != name {
			t.Errorf("Parse(%q) = %q, %v", name, got, ok)
		}
	}
	if _, ok := Parse("deploy"); ok {
		t.Errorf("Parse(deploy) named a phase")
	}
	if Prompt("deploy", tk) != "" {
		t.Errorf("a prompt for a name that is no phase")
	}
}

// TestTheStoresRoundsAreTheWordOnAVerdict: where the journal has no gate lines
// the store's reports decide whether a round was clean, as publish reads them;
// a clean line the store disagrees with does not stand.
func TestTheStoresRoundsAreTheWordOnAVerdict(t *testing.T) {
	t.Parallel()
	f := allGreen()
	f.Rounds = []Round{{N: 1, Verdict: "fix-slices"}}
	got := Derive(f, nil)
	if got.Current != Review || !got.Ready || got.Of(Review).Subs[0].Key != "round:1" || got.Of(Review).Subs[len(got.Of(Review).Subs)-1].Key != "round:2" {
		t.Errorf("a round of findings in the store: %+v", got)
	}

	f.Rounds = []Round{{N: 1, Verdict: "fix-slices"}, {N: 2, Verdict: VerdictClean, Reviewer: "reviewer-session"}}
	got = Derive(f, nil)
	if got.Current != Publish || got.Prompt != "Publish SHOP-1" {
		t.Errorf("a clean round in the store: %+v", got)
	}
	if want := "round 2 clean · by reviewer"; got.Of(Review).Text != want {
		t.Errorf("review text = %q, want %q", got.Of(Review).Text, want)
	}

	// The journal says clean, the report says the reviewer built the change.
	f.Rounds = []Round{{N: 1, Verdict: "not-independent"}}
	got = Derive(f, []journal.Line{ln(journal.EventGateClean, attempt(1))})
	if got.Current != Review || got.Of(Review).Subs[0].Detail != "findings" {
		t.Errorf("a round the store does not call clean: %+v", got.Of(Review))
	}

	// A fix slice or a round on record means the work began, whatever the
	// slice states say.
	fresh := Facts{Ticket: tk, BriefExists: true, Sections: 1, Slices: []Slice{{ID: "a", State: "queued"}, {ID: "r1-fix", State: "queued", FromGate: 1}}}
	if got := Derive(fresh, nil); got.Current != Build {
		t.Errorf("a fix slice with no approval: at %s, want build", got.Current)
	}
}

// TestEverClaimed: a claim, the dispatch of the era before claims, or a
// verification of any kind shows a slice was taken up, and a release does not
// undo it. Approval, notes and the lines that only follow from a claim do not.
func TestEverClaimed(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		lines []journal.Line
		want  bool
	}{
		{"an empty journal", nil, false},
		{"an approval and a note", []journal.Line{ln(journal.EventApproval), ln(journal.EventNote)}, false},
		{"a question, an answer and a stall alone", []journal.Line{ln(journal.EventQuestion, onSlice("a")), ln(journal.EventAnswer, onSlice("a")), ln(journal.EventStall, onSlice("a"))}, false},
		{"a claim", []journal.Line{ln(journal.EventClaim, onSlice("a"), by("s-1"))}, true},
		{"a claim released again", []journal.Line{ln(journal.EventClaim, onSlice("a")), ln(journal.EventRelease, onSlice("a"))}, true},
		{"a dispatch", []journal.Line{ln(journal.EventDispatch, onSlice("a"))}, true},
		{"a verification pending", []journal.Line{ln(journal.EventVerifyPending, onSlice("a"))}, true},
		{"an oracle run", []journal.Line{ln(journal.EventOracle, onSlice("a"))}, true},
		{"a verified commit", []journal.Line{ln(journal.EventVerified, onSlice("a"))}, true},
		{"a verification that errored", []journal.Line{ln(journal.EventVerifyError, onSlice("a"))}, true},
	}
	for _, tc := range tests {
		if got := EverClaimed(tc.lines); got != tc.want {
			t.Errorf("%s: EverClaimed = %v, want %v", tc.name, got, tc.want)
		}
	}
}
