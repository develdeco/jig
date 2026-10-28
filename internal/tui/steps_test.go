package tui

import (
	"strings"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/events"
	"github.com/develdeco/jig/internal/journal"
)

// stepsOf feeds lines for ticket with status, and returns its steps.
func stepsOf(t *testing.T, ticket, status string, lines ...events.Envelope) []stepRow {
	t.Helper()
	g := newRig(t, ticket)
	g.r.answer("status "+ticket+" --store /s", status, 0)
	g.feed(lines...)
	return g.m.steps(g.m.w.tickets[ticket])
}

func TestAnAdoptedBranchIsBuiltAndGoesOnToReview(t *testing.T) {
	t.Parallel()
	status := strings.Join([]string{"ticket: JIG-5", "branch: feature/x", "state: green",
		axi.Table("slices", []string{"id", "state", "attempts", "blocked_by", "question", "goal"}, nil), "questions: none"}, "\n") + "\n"
	steps := stepsOf(t, "JIG-5", status,
		jl(at(20, 0), "JIG-5", "", journal.EventGateOpen),
		jl(at(20, 30), "JIG-5", "", journal.EventGateClean, func(l *journal.Line) { l.Attempt, l.Session = 1, "s-reviewer" }),
	)
	for i := stepBrief; i <= stepReview; i++ {
		if !steps[i].Done {
			t.Errorf("%s is not done: %+v", stepNames[i], steps[i])
		}
	}
	if cur := currentStep(steps); cur != stepPublish || steps[stepPublish].State != StateReady {
		t.Errorf("current step = %s %+v, want the publish, ready", stepNames[cur], steps[cur])
	}
}

func TestFixesGreenAfterARoundAskForTheNextRound(t *testing.T) {
	t.Parallel()
	status := strings.Join([]string{"ticket: JIG-6", "state: green",
		axi.Table("slices", []string{"id", "state", "attempts", "blocked_by", "question", "goal"}, [][]string{
			{"a", "green", "1", "-", "-", "Do a"}, {"fix-1", "green", "1", "-", "-", "Fix it"},
		}), "questions: none"}, "\n") + "\n"
	steps := stepsOf(t, "JIG-6", status,
		jl(at(30, 0), "JIG-6", "a", journal.EventClaim, with("s-builder", "", "", "", "", "")),
		jl(at(30, 10), "JIG-6", "a", journal.EventVerified, with("", sha('a'), "", "", "", "")),
		jl(at(31, 0), "JIG-6", "", journal.EventGateOpen),
		jl(at(31, 30), "JIG-6", "", journal.EventGateRound, func(l *journal.Line) { l.Attempt, l.Session = 1, "s-reviewer" }),
		jl(at(31, 31), "JIG-6", "fix-1", journal.EventFixSlice),
		jl(at(32, 0), "JIG-6", "fix-1", journal.EventClaim, with("s-builder", "", "", "", "", "")),
		jl(at(32, 10), "JIG-6", "fix-1", journal.EventVerified, with("", sha('b'), "", "", "", "")),
	)
	r := steps[stepReview]
	if r.Done || currentStep(steps) != stepReview || r.Text != "round 1's fixes are green · round 2 is next" {
		t.Errorf("review = %+v, want the current step asking for round 2", r)
	}
}

func TestAStepIsNotDoneBeforeTheStepsAheadOfIt(t *testing.T) {
	t.Parallel()
	status := strings.Join([]string{"ticket: JIG-7", "state: green",
		axi.Table("slices", []string{"id", "state", "attempts", "blocked_by", "question", "goal"}, [][]string{{"a", "green", "1", "-", "-", "Do a"}}),
		"questions: none",
		axi.KV("brief", [][2]string{{"approval", "changed"}, {"approved_by", "Ada"}, {"approved_at", "2026-10-23T09:00:00Z"}}),
	}, "\n") + "\n"
	steps := stepsOf(t, "JIG-7", status,
		jl(at(40, 0), "JIG-7", "a", journal.EventClaim, with("s-builder", "", "", "", "", "")),
		jl(at(40, 10), "JIG-7", "a", journal.EventVerified, with("", sha('a'), "", "", "", "")),
		jl(at(41, 0), "JIG-7", "", journal.EventGateOpen),
		jl(at(41, 30), "JIG-7", "", journal.EventGateClean, func(l *journal.Line) { l.Attempt, l.Session = 1, "s-reviewer" }),
	)
	if cur := currentStep(steps); cur != stepBrief {
		t.Fatalf("current step = %s, want the brief: it changed after its approval", stepNames[cur])
	}
	for i := stepBuild; i <= stepPublish; i++ {
		if steps[i].Done {
			t.Errorf("%s is done ahead of the brief: %+v", stepNames[i], steps[i])
		}
	}
}

func TestAnAdoptedBranchSeenWithoutItsStatusIsBuilt(t *testing.T) {
	t.Parallel()
	g := newRig(t, "") // the main screen reads no ticket's status
	g.feed(
		jl(at(20, 0), "JIG-5", "", journal.EventGateOpen),
		jl(at(20, 30), "JIG-5", "", journal.EventGateClean, func(l *journal.Line) { l.Attempt, l.Session = 1, "s-reviewer" }),
	)
	steps := g.m.steps(g.m.w.tickets["JIG-5"])
	if !steps[stepBuild].Done || !steps[stepReview].Done || currentStep(steps) != stepPublish {
		t.Errorf("steps = %+v, want the build and the review done and the publish current", steps)
	}
}

func TestTheLastRowShowsThePanelBelowIt(t *testing.T) {
	t.Parallel()
	g := newRig(t, "JIG-1")
	g.send(tea.WindowSizeMsg{Width: 80, Height: 18})
	g.feed(jig1()...).press("enter", "end") // the build's screen, its last slice
	v := g.view()
	if !strings.Contains(v, "▸ ○ d") || !strings.Contains(v, "╰") || !strings.Contains(v, "slice c · builder asks: Formal or casual?") {
		t.Errorf("the last slice, the newest activity line and the panel's edge are not all in view:\n%s", v)
	}
}

func TestTheReviewScreenNamesWhatARoundSentBack(t *testing.T) {
	t.Parallel()
	status := strings.Join([]string{"ticket: JIG-6", "state: building",
		axi.Table("slices", []string{"id", "state", "attempts", "blocked_by", "question", "goal"}, [][]string{
			{"a", "green", "1", "-", "-", "Do a"},
			{"fix-1", "queued", "0", "-", "-", "Fix these gate findings:\n\nalpha/a.go:3 Unchecked error\nThe error is dropped."},
		}), "questions: none"}, "\n") + "\n"
	g := newRig(t, "JIG-6")
	g.r.answer("status JIG-6 --store /s", status, 0)
	g.feed(
		jl(at(30, 0), "JIG-6", "a", journal.EventClaim, with("s-builder", "", "", "", "", "")),
		jl(at(30, 10), "JIG-6", "a", journal.EventVerified, with("", sha('a'), "", "", "", "")),
		jl(at(31, 0), "JIG-6", "", journal.EventGateOpen),
		jl(at(31, 30), "JIG-6", "", journal.EventGateRound, func(l *journal.Line) { l.Attempt, l.Session = 1, "s-reviewer" }),
		jl(at(31, 31), "JIG-6", "fix-1", journal.EventFixSlice, func(l *journal.Line) { l.Attempt = 1 }),
	)
	g.press("down", "enter") // from the build, the current step, to the review
	v := g.view()
	for _, want := range []string{"sent back to the build", "fix-1  round 1  alpha/a.go:3 Unchecked error", "fixes · sent back fix-1: alpha/a.go:3 Unchecked error"} {
		if !strings.Contains(v, want) {
			t.Errorf("the review screen lacks %q:\n%s", want, v)
		}
	}
	// The goal's other lines never reach a row: every screen keeps its height.
	for _, keys := range [][]string{{"esc"}, {"up", "enter"}} {
		g.press(keys...)
		v := g.view()
		if strings.Contains(v, "The error is dropped.") || strings.Count(v, "\n")+1 != 24 {
			t.Errorf("after %v a fix slice's goal spills over its row:\n%s", keys, v)
		}
	}
}

func TestGoalLineIsOneLineOfAGoal(t *testing.T) {
	t.Parallel()
	for goal, want := range map[string]string{
		"Gate finding r1-f1, kept by the human:\n\nalpha/a.go:3 Unchecked error\nmore": "alpha/a.go:3 Unchecked error",
		"Two\nlines": "Two",
		"Fix these gate findings:\n\nalpha/a.go:3 Unchecked error\nmore": "alpha/a.go:3 Unchecked error",
		"Plain goal": "Plain goal",
		"":           "",
	} {
		if got := goalLine(goal); got != want {
			t.Errorf("goalLine(%q) = %q, want %q", goal, got, want)
		}
	}
}

// TestAReviewerTakingTheRoundShowsOnTheReviewStep: a round the gate opened
// waits for a reviewer until one takes it with `jig review context`, whose
// gate-reviewer line names the session; the activity says both in words.
func TestAReviewerTakingTheRoundShowsOnTheReviewStep(t *testing.T) {
	t.Parallel()
	status := strings.Join([]string{"ticket: JIG-8", "state: green",
		axi.Table("slices", []string{"id", "state", "attempts", "blocked_by", "question", "goal"}, [][]string{
			{"a", "green", "1", "-", "-", "Do a"},
		}), "questions: none"}, "\n") + "\n"
	g := newRig(t, "JIG-8")
	g.r.answer("status JIG-8 --store /s", status, 0)
	open := jl(at(40, 0), "JIG-8", "", journal.EventGateOpen, func(l *journal.Line) { l.Attempt = 1 })
	g.feed(
		jl(at(39, 0), "JIG-8", "a", journal.EventClaim, with("s-builder", "", "", "", "", "")),
		jl(at(39, 10), "JIG-8", "a", journal.EventVerified, with("", sha('a'), "", "", "", "")),
		open,
	)
	tk := g.m.w.tickets["JIG-8"]
	if r := g.m.steps(tk)[stepReview]; r.State != StateReviewing || r.Text != "round 1 open · waiting for a reviewer" {
		t.Errorf("review before a reviewer = %+v, want round 1 waiting for one", r)
	}
	took := jl(at(40, 5), "JIG-8", "", journal.EventGateReviewer, func(l *journal.Line) { l.Attempt, l.Session = 1, "s-rev" })
	g.feed(took)
	tk = g.m.w.tickets["JIG-8"]
	if r := g.m.steps(tk)[stepReview]; r.State != StateReviewing || r.Text != "round 1 open · reviewer is reading the change" {
		t.Errorf("review once taken = %+v, want the reviewer reading round 1", r)
	}
	labels := g.m.subLabelsOf(tk)
	if got := tk.labelOf(labels, open); got != "round 1" {
		t.Errorf("gate-open names %q, want round 1", got)
	}
	if got := g.m.say(tk, open); got != "opened" {
		t.Errorf("gate-open says %q", got)
	}
	if got := g.m.say(tk, took); got != "reviewer took it" {
		t.Errorf("gate-reviewer says %q", got)
	}
	if got := g.m.activityLine(tk, labels, took); !strings.HasSuffix(got, "round 1 · reviewer took it") {
		t.Errorf("gate-reviewer line = %q, want it to name the round and say who took it", got)
	}
	if stepOfLine(took) != stepReview {
		t.Errorf("a gate-reviewer line is in step %d, want the review", stepOfLine(took))
	}
	// Reopened (a moved head), the round waits for a reviewer again.
	g.feed(jl(at(41, 0), "JIG-8", "", journal.EventGateOpen, func(l *journal.Line) { l.Attempt = 1 }))
	if r := g.m.steps(g.m.w.tickets["JIG-8"])[stepReview]; r.Text != "round 1 open · waiting for a reviewer" {
		t.Errorf("review once reopened = %+v, want round 1 waiting for a reviewer again", r)
	}
}

// TestADecidedAskSaysSoBeforeItsTitle: what was decided comes before the
// title, so a long title cut at the screen's edge never hides it.
func TestADecidedAskSaysSoBeforeItsTitle(t *testing.T) {
	t.Parallel()
	g := newRig(t, "JIG-9")
	lines := g.m.askLines([]askRow{
		{ID: "r1-f2", Risk: "medium", Where: "cart/cart.go:20", Title: strings.Repeat("a long title ", 20), Decision: "dismiss", DecidedBy: "person"},
		{ID: "r1-f3", Risk: "low", Where: "cart/cart.go:9", Title: "short"},
	})
	for i, want := range []string{"dismissed by you", "undecided"} {
		d, title := strings.Index(lines[i], want), strings.Index(lines[i], []string{"a long title", "short"}[i])
		if d < 0 || title < d {
			t.Errorf("row %d = %q, want %q before the title", i, lines[i], want)
		}
	}
	if a, b := strings.Index(lines[0], "a long title"), strings.Index(lines[1], "short"); a != b {
		t.Errorf("titles start at %d and %d, want them aligned", a, b)
	}
}

// TestAnUnreadTicketsBriefSaysWhatTheRosterKnows: before its status is read,
// a ticket's brief step says what `jig next` told: no slices yet is a brief
// being drafted, slices nobody started with no approval in the journal wait
// for it, and a ticket whose work started or ended is only being read (its
// lines, approval among them, are on their way).
func TestAnUnreadTicketsBriefSaysWhatTheRosterKnows(t *testing.T) {
	t.Parallel()
	g := newRig(t, "")
	g.m.w.setRoster([]rosterRow{
		{ID: "JIG-10", Title: "New", State: "new"}, {ID: "JIG-11", Title: "Planned", State: "ready"},
		{ID: "JIG-12", Title: "Waits", State: "blocked"}, {ID: "JIG-13", Title: "Built", State: "green"},
		{ID: "JIG-14", Title: "Shipped", State: "done"}, {ID: "JIG-15", Title: "Going", State: "active"},
		{ID: "JIG-16", Title: "Broken", State: "invalid"},
	})
	for id, want := range map[string]string{
		"JIG-10": "being drafted", "JIG-11": "drafted · waiting for your approval", "JIG-12": "drafted · waiting for your approval",
		"JIG-13": "reading", "JIG-14": "reading", "JIG-15": "reading", "JIG-16": "its slices.yaml cannot be read",
	} {
		if s := g.m.steps(g.m.w.tickets[id])[stepBrief]; s.Text != want {
			t.Errorf("%s brief = %+v, want %q", id, s, want)
		}
	}
}

// TestTheHeaderSaysEveryStepInEveryStateInWordsThatRead: the header joins a
// step and the state it is in, and not every pair reads as a phrase when it is
// joined ("publish publishing"). The table is every pair a step's row can be in,
// each in the words a person would say it.
func TestTheHeaderSaysEveryStepInEveryStateInWordsThatRead(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		step  int
		state string
		want  string
	}{
		{stepBrief, StateQueued, "brief being read"},
		{stepBrief, StateBuilding, "brief being drafted"},
		{stepBrief, StateFindings, "brief needs approving"},
		{stepBrief, StateStalled, "brief cannot be read"},
		{stepBuild, StateQueued, "waiting to build"},
		{stepBuild, StateReady, "ready to build"},
		{stepBuild, StateBuilding, "build in progress"},
		{stepBuild, StateVerifying, "build verifying"},
		{stepBuild, StateNeedsInput, "build needs you"},
		{stepBuild, StateStalled, "build stalled"},
		{stepBuild, StateEnvBlocked, "build blocked by its environment"},
		{stepReview, StateQueued, "waiting to review"},
		{stepReview, StateReady, "ready to review"},
		{stepReview, StateReviewing, "review round open"},
		{stepReview, StateFindings, "review findings"},
		{stepPublish, StateQueued, "waiting to publish"},
		{stepPublish, StateReady, "ready to publish"},
		{stepPublish, StatePublishing, "publishing"},
		{stepPublish, StatePublished, "published"},
	} {
		got := stepWords(tc.step, tc.state)
		if got != tc.want {
			t.Errorf("%s in %s: the header says %q, want %q", stepNames[tc.step], tc.state, got, tc.want)
		}
		// No pair says a word twice in two forms: "publish publishing",
		// "build building", "review reviewing".
		words := strings.Fields(got)
		for i, a := range words {
			for _, b := range words[i+1:] {
				if len(a) >= 4 && strings.HasPrefix(b, a) {
					t.Errorf("%s in %s: %q says %q and then %q", stepNames[tc.step], tc.state, got, a, b)
				}
			}
		}
	}
	// A pair the table lacks keeps both words, for a state no row has today.
	if got := stepWords(stepBuild, "odd"); got != "build odd" {
		t.Errorf("a state with no words reads %q, want the step and the state", got)
	}
}

// TestTheHeaderOfAPublishUnderWayReadsPublishing: the case the table is for, on
// the screen: a ticket whose body is written and whose approval waits is
// "publishing", in the header of its screen.
func TestTheHeaderOfAPublishUnderWayReadsPublishing(t *testing.T) {
	t.Parallel()
	g := newRig(t, "JIG-2")
	g.r.answer("status JIG-2 --store /s", statusJIG2Done(), 0)
	g.feed(jig2()[:9]...)
	head := strings.Split(g.view(), "\n")[0]
	if !strings.HasSuffix(head, "▲ publishing") || strings.Contains(head, "publish publishing") {
		t.Errorf("header = %q, want it to end in the icon and \"publishing\"", head)
	}
}

// TestStepTimesCountOnlyTheirOwnGaps: a step's time is the gaps between its own
// consecutive lines. A build that the review sent a fix back to counts the fix
// and not the review between; the hand-offs from one step to the next (here a
// minute each, a person reading before the next prompt) count for neither, and
// a spend line is no step's.
func TestStepTimesCountOnlyTheirOwnGaps(t *testing.T) {
	t.Parallel()
	status := strings.Join([]string{"ticket: JIG-9", "state: green",
		axi.Table("slices", []string{"id", "state", "attempts", "blocked_by", "question", "goal"}, [][]string{
			{"a", "green", "1", "-", "-", "Do a"},
			{"r1-fix-1", "green", "1", "-", "-", "Fix r1-f1"},
		}), "questions: none"}, "\n") + "\n"
	g := newRig(t, "JIG-9")
	g.r.answer("status JIG-9 --store /s", status, 0)
	round := func(n int, session string) func(*journal.Line) {
		return func(l *journal.Line) { l.Attempt, l.Session = n, session }
	}
	g.feed(
		jl(at(30, 0), "JIG-9", "a", journal.EventClaim, with("s-b", "", "", "", "", "")),
		jl(at(30, 40), "JIG-9", "a", journal.EventVerified, with("s-b", sha('a'), "", "", "", "")),
		jl(at(31, 40), "JIG-9", "", journal.EventGateOpen, round(1, "")),
		jl(at(33, 40), "JIG-9", "", journal.EventGateRound, round(1, "s-rev")),
		jl(at(33, 40), "JIG-9", "r1-fix-1", journal.EventFixSlice, round(1, "")),
		jl(at(34, 40), "JIG-9", "r1-fix-1", journal.EventClaim, with("s-b", "", "", "", "", "")),
		jl(at(35, 0), "JIG-9", "r1-fix-1", journal.EventVerified, with("s-b", sha('b'), "", "", "", "")),
		jl(at(36, 0), "JIG-9", "", journal.EventGateOpen, round(2, "")),
		jl(at(37, 30), "JIG-9", "", journal.EventGateClean, round(2, "s-rev")),
		jl(at(37, 40), "JIG-9", "r1-fix-1", journal.EventSpend, with("s-b", "", "", "", "", "")),
		jl(at(38, 40), "JIG-9", "", journal.EventPrBody, func(l *journal.Line) { l.Commit = sha('b') }),
		jl(at(39, 10), "JIG-9", "", journal.EventPr, func(l *journal.Line) {
			l.Commit, l.Outcome, l.URL = sha('b'), "recorded", "https://example.test/acme/demo/pull/9"
		}),
		jl(at(39, 10), "JIG-9", "", journal.EventPublishDone),
	)
	steps := g.m.steps(g.m.w.tickets["JIG-9"])
	for step, want := range map[int]string{stepBrief: "", stepBuild: "1m 00s", stepReview: "3m 30s", stepPublish: "30s"} {
		if got := steps[step].Took; got != want {
			t.Errorf("%s took %q, want %q", stepNames[step], got, want)
		}
	}
}

// TestEachStepCountsItsTimeAndTheStepsTheirTotal: the brief counts from the
// call that minted the ticket to its approval, the calls in between included;
// the build under way counts on to now from its latest line; the call made
// between them (reading the build skill) is the build's, and the minute from
// the approval to it is neither's. The Steps panel's title adds them up.
func TestEachStepCountsItsTimeAndTheStepsTheirTotal(t *testing.T) {
	t.Parallel()
	status := strings.Join([]string{"ticket: SHOP-1", "state: building",
		axi.Table("slices", []string{"id", "state", "attempts", "blocked_by", "question", "goal"}, [][]string{
			{"round", "building", "1", "-", "-", "Round to the cent"},
		}), "questions: none"}, "\n") + "\n"
	g := newRig(t, "SHOP-1")
	g.m.now = func() time.Time { return time.Date(2026, 10, 23, 9, 6, 0, 0, time.UTC) }
	g.r.answer("status SHOP-1 --store /s", status, 0)
	g.send(tea.WindowSizeMsg{Width: 100, Height: 40})
	g.feed(
		ll(at(0, 0), "", journal.EventSessionStart, func(l *journal.Line) { l.Session, l.Harness, l.Model = "s-b", "claude", "claude-sonnet-5" }),
		ll(at(1, 0), "SHOP-1", journal.EventSessionTouch, func(l *journal.Line) { l.Session = "s-b" }),
		tool(at(1, 1), "s-b", "jig ticket new --title Round", said("Mint the ticket")),
		tool(at(2, 30), "s-b", "jig validate SHOP-1", said("Check the slices")),
		jl(at(4, 0), "SHOP-1", "", journal.EventApproval, with("s-b", "", "", "Dana <dana@example.invalid>", "0123456789ab", "person")),
		tool(at(5, 0), "s-b", "jig skill build", said("Read the build skill")),
		jl(at(5, 10), "SHOP-1", "round", journal.EventClaim, with("s-b", "", "", "", "", "")),
		tool(at(5, 40), "s-b", "go test ./...", said("Run the tests")),
	)
	steps := g.m.steps(g.m.w.tickets["SHOP-1"])
	for step, want := range map[int]string{stepBrief: "2m 59s", stepBuild: "1m 00s", stepReview: "", stepPublish: ""} {
		if got := steps[step].Took; got != want {
			t.Errorf("%s took %q, want %q", stepNames[step], got, want)
		}
	}
	if top := panelLines(strings.Split(g.view(), "\n"), "Steps")[0]; !strings.Contains(top, "step 2 of 4 · total 3m 59s") {
		t.Errorf("the Steps panel's title does not give the total:\n%s", top)
	}
}

// TestAStepJustStartedSaysZeroSeconds: in a step's first second its time, the
// total and the sub-step under way read "0s", not span's spelled-out words,
// which do not fit their cells.
func TestAStepJustStartedSaysZeroSeconds(t *testing.T) {
	t.Parallel()
	status := strings.Join([]string{"ticket: SHOP-1", "state: building",
		axi.Table("slices", []string{"id", "state", "attempts", "blocked_by", "question", "goal"}, [][]string{
			{"round", "building", "1", "-", "-", "Round to the cent"},
		}), "questions: none"}, "\n") + "\n"
	g := newRig(t, "SHOP-1")
	g.m.now = func() time.Time { return time.Date(2026, 10, 23, 9, 5, 10, 400_000_000, time.UTC) }
	g.r.answer("status SHOP-1 --store /s", status, 0)
	g.send(tea.WindowSizeMsg{Width: 100, Height: 40})
	g.feed(
		ll(at(0, 0), "", journal.EventSessionStart, func(l *journal.Line) { l.Session, l.Harness, l.Model = "s-b", "claude", "claude-sonnet-5" }),
		ll(at(1, 0), "SHOP-1", journal.EventSessionTouch, func(l *journal.Line) { l.Session = "s-b" }),
		jl(at(4, 0), "SHOP-1", "", journal.EventApproval, with("s-b", "", "", "Dana <dana@example.invalid>", "0123456789ab", "person")),
		jl(at(5, 10), "SHOP-1", "round", journal.EventClaim, with("s-b", "", "", "", "", "")),
	)
	if got := g.m.steps(g.m.w.tickets["SHOP-1"])[stepBuild].Took; got != "0s" {
		t.Errorf("the build's time = %q, want 0s", got)
	}
	if view := g.view(); strings.Contains(view, "under") {
		t.Errorf("a time under a second is spelled out:\n%s", view)
	}
}
