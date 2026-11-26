package e2e

// End-to-end test of `jig tui`: a ticket built by hand goes from its first
// commit to a published pull request while the screens watch it, and the
// person acts only through their keys. The harness stands for the agents (a
// builder committing in the clone, a reviewer in a session of its own); the
// TUI is the real binary on a pipe, driven by scripted keys, printing each
// screen whole.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/store"
)

// tuiWait is how long a test gives the screens to show what the journal
// already holds: the stream polls four times a second.
const tuiWait = 60 * time.Second

// tuiScreens reads the screens a `jig tui` on a pipe prints: each is followed
// by a line holding a form feed.
type tuiScreens struct {
	t *testing.T
	p *jigProc
}

// until reads screens, oldest first, until one holds all of wants, and
// returns it. A screen read here is not read again, so wants name what the
// state being waited for adds to the screen, not what every screen after it
// holds, and what must be seen together is asked for together.
func (s tuiScreens) until(wants ...string) string {
	s.t.Helper()
	return s.untilWithin(tuiWait, wants...)
}

// untilWithin is until with a wait of its own: a screen that can only change
// once a detached verification has run waits as long as the suite gives one
// (verifyWait), since the run queues behind the others for a verify slot.
func (s tuiScreens) untilWithin(wait time.Duration, wants ...string) string {
	s.t.Helper()
	deadline := time.Now().Add(wait)
	var frame []string
	last := "(no screen yet)"
	for {
		left := time.Until(deadline)
		if left <= 0 {
			s.t.Fatalf("no screen showed %q within %s; the last one:\n%s", wants, wait, last)
		}
		line, ok := s.p.Line(left)
		if !ok {
			s.t.Fatalf("jig tui printed no screen with %q before it stopped or went quiet; the last one:\n%s", wants, last)
		}
		if line != "\f" {
			frame = append(frame, line)
			continue
		}
		last = strings.Join(frame, "\n")
		frame = nil
		all := true
		for _, want := range wants {
			all = all && strings.Contains(last, want)
		}
		if all {
			return last
		}
	}
}

// press types keys at the screens, one write.
func (s tuiScreens) press(keys string) {
	s.t.Helper()
	s.p.Write([]byte(keys))
}

// reviewResult writes the result.json of a reviewer's report on round n of
// the ticket: the findings given, and every path the round says must be read.
// ask, when set, is reported as one finding that only a person can decide.
func reviewResult(t *testing.T, st *store.Store, ticket string, n int, ask bool) string {
	t.Helper()
	data := readFileOrFatal(t, filepath.Join(st.TicketDir(ticket), "work", fmt.Sprintf("gate.round-%d.review.json", n)))
	var req struct {
		MustReview []string `json:"must_review"`
	}
	if err := json.Unmarshal(data, &req); err != nil || len(req.MustReview) == 0 {
		t.Fatalf("round %d review.json: %v, must_review %v\n%s", n, err, req.MustReview, data)
	}
	findings := []map[string]any{}
	if ask {
		findings = append(findings, map[string]any{
			"file": req.MustReview[0], "line": 1, "title": "Which greeting does the product want?",
			"detail": "The code says hello; the brief does not say whether that is formal.", "action": "ask",
			"risk": "medium", "risk_rationale": "a user-visible string", "oracle": "test",
		})
	}
	body, err := json.Marshal(map[string]any{
		"findings": findings, "still_present": []any{}, "reviewed_paths": req.MustReview, "summary": "read the change",
	})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), fmt.Sprintf("round-%d.json", n))
	writeFileOrFatal(t, path, body)
	return path
}

// TestTUIHandBuiltToPublish: from a ticket's first commit to its pull request,
// the person never leaves the screens. The builder commits slice a, asks a
// question and builds the rest; the screens show each commit verified, the
// question, the person answers it with a key (a line with the person's
// provenance), the gate opens from a key, a reviewer in another session finds
// an ask, the person dismisses it with a key (a triage line), the next round
// is clean, and one key approves and publishes (publish-approved, pr,
// publish-done). Everything the keys did is a verb: the journal holds what
// the same commands would have written.
//
// This is the one chain of the screens. The first round's reviewer is a
// subagent of the builder's session (the hook payloads carry its id, the
// ledger names it, and the round is independent). With the round's ask
// waiting, `jig status` lists it with no decision and the phase waits on it,
// and the detail screens (the round, the build, the brief) show what the verbs
// print for them; once the person dismisses it, status lists the decision and
// who made it, and the next round's request carries it. The rules behind those
// lines are the seams': internal/tui's screens and records tests, cmd/jig's
// status and triage tests, internal/verifydeliver's TestOpenRowsCarryAPendingDecision
// and TestGateFinishFindsTheReviewerWhenNobodyNamedIt; that the screens and
// `jig next` stand at the same place is cmd/jig's TestTheScreensStandWhereJigNextDoes.
func TestTUIHandBuiltToPublish(t *testing.T) {
	t.Parallel()
	fx, env := newFixture(t, fixture.Opts{})
	dropEnvFromSlices(t, fx)
	h := newHarness(t, fx, env, "s-builder")
	branch := fx.Branch
	h.Branch(branch)
	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatal(err)
	}
	// The project's repo is a GitHub repo as far as jig can tell, and a fake
	// gh is on PATH for the screens' publish to open the pull request with.
	env, ghLog := githubClone(t, builtTicket{whole: true, fx: fx, env: env, builder: h, branch: branch, st: st})
	h.env = env

	tui := startJig(t, env, h.Clone, "tui", fx.Ticket)
	screens := tuiScreens{t: t, p: tui}
	screens.until("› " + fx.Ticket)

	// The first commit, verified by the builder's own `jig verify`. Like every
	// verification of the suite it holds one of the slots that bound how many
	// `go test` runs the parallel tests have going at once.
	func() {
		verifySlots <- struct{}{}
		defer func() { <-verifySlots }()
		h.CommitScenario("a", 1)
		if res := h.Verify(fx.Ticket, "a"); res.Code != 0 || !strings.Contains(res.Stdout, "state: pass") {
			t.Fatalf("jig verify a exit = %d, want 0 and a pass\nstdout:\n%s\nstderr:\n%s", res.Code, res.Stdout, res.Stderr)
		}
		screens.untilWithin(verifyWait, "1 of 4 green")
	}()

	// The builder asks, the question reaches the screens, and the person
	// answers it from them.
	if res := h.Jig("ask", fx.Ticket, "--slice", "c", "Formal or casual?"); res.Code != 0 {
		t.Fatalf("jig ask exit = %d\nstdout:\n%s\nstderr:\n%s", res.Code, res.Stdout, res.Stderr)
	}
	screens.until("Formal or casual?", "a answer") // the key is listed once the roster has read the ticket
	screens.press("a")
	screens.until("answer q-001")
	screens.press("Casual, please.")
	screens.press("\r")
	screens.until("jig answer --as person " + fx.Ticket + " q-001 -- Casual, please.")
	answer := waitJournal(t, fx, fx.Ticket, tuiWait, func(l journal.Line) bool { return l.Event == journal.EventAnswer })
	if answer.Ref != "q-001" || answer.Slice != "c" || answer.Provenance != "person" || answer.Summary != "Casual, please." {
		t.Errorf("answer line = %+v, want the person's answer to q-001 on slice c", answer)
	}

	// The rest of the ticket is built and verified. Slice b's first attempt
	// fails its test and is never verified; the second is the slice's commit.
	h.CommitScenario("b", 1)
	var last string
	verifySlots <- struct{}{}
	for _, s := range []struct {
		slice   string
		attempt int
	}{{"b", 2}, {"c", 2}, {"d", 1}} {
		last = h.CommitScenario(s.slice, s.attempt)
		if res := h.Verify(fx.Ticket, s.slice); res.Code != 0 || !strings.Contains(res.Stdout, "state: pass") {
			t.Fatalf("jig verify %s exit = %d, want 0 and a pass\nstdout:\n%s\nstderr:\n%s", s.slice, res.Code, res.Stdout, res.Stderr)
		}
	}
	<-verifySlots
	screens.until("4 slices green, each verified by jig")

	// The ticket screen is a set of panes. The cursor in Steps goes up to the
	// build, tab reaches the pane of its sub-steps and the hint line names it,
	// and enter on a slice opens the slice; back, the cursor is where it was
	// left.
	screens.press("\x1b[A")
	screens.until("╭─ Build")
	screens.press("\t")
	screens.until("↑↓ move in Build")
	screens.press("\x1b[H")
	screens.press("\r")
	screens.until("slice a (1 of 4)")
	screens.press("\x7f")
	screens.until("▸ ✓ slice a", "↑↓ move in Build")
	// tab reaches Activity, whose newest line opens the slice it is about; z
	// shows the lines the screen had cut, and z again the screen.
	screens.press("\t")
	activity := screens.until("↑↓ move in Activity")
	if !strings.Contains(activity, "latest ") {
		t.Errorf("the ticket's Activity pane cuts none of its lines at 100x30:\n%s", activity)
	}
	// A line the pane cut to its first word ends in a mark: space on it lays
	// the line out whole where it stands, and space again folds it back.
	if !strings.Contains(activity, "●…") {
		t.Fatalf("the Activity pane has no cut line at 100x30:\n%s", activity)
	}
	screens.press("\x1b[B")
	screens.until("↑↓ move in Activity", "space expand")
	screens.press(" ")
	whole := screens.until("space fold")
	if strings.Contains(whole, "●…") {
		t.Errorf("space on the cut line did not show it whole:\n%s", whole)
	}
	screens.press(" ")
	if folded := screens.until("space expand"); !strings.Contains(folded, "●…") {
		t.Errorf("space again did not fold the line back:\n%s", folded)
	}
	screens.press("\x1b[A")
	screens.until("↑↓ move in Activity")
	screens.press("\r")
	screens.until("slice ")
	screens.press("\x7f")
	screens.until("↑↓ move in Activity")
	screens.press("z")
	zoomed := screens.until("Activity, zoomed", "esc unzoom")
	if strings.Contains(zoomed, "╭─ Steps") || !strings.Contains(zoomed, "you answered the question") {
		t.Errorf("the zoomed Activity pane draws another pane or lacks the lines the screen cut:\n%s", zoomed)
	}
	screens.press("z")
	screens.until("╭─ Steps", "↑↓ move in Activity")
	// Back to Steps, on the step it began on.
	screens.press("\x1b[Z")
	screens.press("\x1b[Z")
	screens.until("↑↓ move in Steps")
	screens.press("\x1b[B")

	// The gate opens from the screens. A subagent of the builder's session
	// reads the round and reports one ask: the harness's hook payloads carry the
	// subagent's id, the ledger's tool lines name it, and the round is
	// independent of the builders although the session is the builder's.
	screens.press("g")
	screens.until("round 1 open", "✓ jig gate "+fx.Ticket)
	b := builtTicket{whole: true, fx: fx, env: env, builder: h, branch: branch, st: st}
	sub := newHarness(t, fx, env, "s-builder")
	sub.Agent = "agent-reviewer"
	if res := jigHooked(sub, "review", "context", fx.Ticket); res.Code != 0 {
		t.Fatalf("jig review context exit = %d\nstdout:\n%s\nstderr:\n%s", res.Code, res.Stdout, res.Stderr)
	}
	done := jigHooked(sub, "review", "done", fx.Ticket, "--result", reviewResult(t, st, fx.Ticket, 1, true))
	if !strings.Contains(done.Stdout, "round: 1") || !strings.Contains(done.Stdout, "independent: true") {
		t.Fatalf("jig review done (round 1) exit = %d, want round 1 recorded as independent\nstdout:\n%s\nstderr:\n%s", done.Code, done.Stdout, done.Stderr)
	}
	if rep := b.report(t, "1"); rep["independent"] != true {
		t.Errorf("report.yaml of round 1 = %v, want independent: true", rep)
	}
	if reviewer, _ := b.report(t, "1")["reviewer"].(map[string]any); reviewer["session"] != "s-builder" || reviewer["agent"] != "agent-reviewer" {
		t.Errorf("report.yaml reviewer = %v, want the subagent agent-reviewer of session s-builder", reviewer)
	}
	if screen := screens.until("round 1: 1 ask waits for you"); !strings.Contains(screen, "Which greeting does the product want?") {
		t.Errorf("the screen with the round's findings does not show the ask:\n%s", screen)
	}

	// The ask waits for a decision: `jig status` lists it with none, the help
	// counts it, and the ticket's phase waits on it.
	out := runJig(t, env, h.Clone, "status", fx.Ticket).Stdout
	if !strings.Contains(out, "outstanding_asks[1]{id,risk,file:line,title,why,decision,decided_by}:") {
		t.Fatalf("status lacks the asks table with its decision columns:\n%s", out)
	}
	if row := lineStarting(out, "  r1-f1,"); !strings.HasSuffix(row, ",,,") {
		t.Errorf("the undecided ask's row = %q, want no why, no decision and no decider", row)
	}
	if !strings.Contains(out, "to decide the 1 undecided ask, then `jig gate "+fx.Ticket+"` to apply the decisions") {
		t.Errorf("status help does not count the undecided ask:\n%s", out)
	}
	if p := nextJSON[phaseOut](t, env, h.Clone, fx.Ticket).Phase; p.Ready || p.Code != "PHASE_ASKS_UNDECIDED" {
		t.Errorf("before the decision: phase ready=%v code=%q, want PHASE_ASKS_UNDECIDED", p.Ready, p.Code)
	}

	// The detail screens read what the verbs print. From the review step, which
	// is the current one, up through the build to the brief, each screen shows
	// what `jig status` and `jig brief show --text` printed for it.
	// The round: who reviewed it, whether they stood apart from the builders,
	// what they read, what they raised, and the ask with the reviewer's reason.
	// The steps are one key each: 3 is the review, 2 the build, 1 the brief.
	// The cursor opens on the ask, whose detail is its finding whole with the
	// reviewer's reason; up from it is the round, whose detail says who reviewed
	// it and what they read.
	screens.press("3")
	screens.until("fix budget 0 of 3 rounds used", "Which greeting does the product want?", "undecided", "a user-visible string")
	screens.press("\x1b[Z") // shift+tab: the Rounds pane
	screens.until("round 1  findings", "1 ask", "independent", "5 files")
	// ? lists the keys of the screen, with the ones that cannot run and why: the
	// gate waits for the ask to be decided, and no pull request is open yet.
	screens.press("?")
	screens.until("Keys on this screen", "not now: the review is not ready", "not now: "+fx.Ticket+" has no pull request")
	screens.press("?")
	// The build: each slice's workspace and oracle from slices.yaml, and the
	// commit that was verified, with its subject.
	// The detail of the slice under the cursor says them, so the cursor goes
	// from the first slice down to the third, whose workspace is the other.
	screens.press("2")
	screens.until("workspace alpha", "oracle    test", "“JIG-1 a: ", "fix budget 0 of 3 rounds used")
	screens.press("\x1b[B")
	screens.press("\x1b[B")
	screens.until("workspace beta", "oracle    test")
	screens.press("\x1b[A")
	screens.press("\x1b[A")
	screens.until("╭─ Detail · a")
	// A slice, and the one after it by ]: the header names the place.
	screens.press("\r")
	screens.until("slice a (1 of 4)")
	screens.press("]")
	screens.until("slice b (2 of 4)")
	// The brief: its sections with their first lines, read by the screen from
	// the verb, and the slices that cite them.
	screens.press("1")
	screens.until("Slice A - failing clamp")
	// The detail of a section has its text: down to the section of slice A.
	screens.press("\x1b[B")
	screens.until("╭─ Detail · Slice A - failing clamp", "`alpha.Clamp` is committed broken")
	// The slices are the parts after the sections and before the approval: the end
	// of the list is the approval, and three up is slice b, which waits for a.
	screens.press("\x1b[F")
	screens.press("\x1b[A")
	screens.press("\x1b[A")
	screens.press("\x1b[A")
	screens.until("╭─ Detail · b", "workspace alpha", "oracle    test", "waits for a")
	// h goes to the project's list, and its row opens the ticket again.
	screens.press("h")
	screens.until("╭─ Running ", fx.Ticket)
	screens.press("\r")
	screens.until("› "+fx.Ticket, "Steps")

	// The person dismisses the ask from the screens, and opens the next round.
	screens.press("d")
	screens.until("asks are decided", "✓ jig triage "+fx.Ticket+" r1-f1 dismiss")
	triage := waitJournal(t, fx, fx.Ticket, tuiWait, func(l journal.Line) bool { return l.Event == journal.EventTriage })
	if triage.Ref != "r1-f1" || triage.Outcome != "dismiss" || triage.Provenance != "person" {
		t.Errorf("triage line = %+v, want r1-f1 dismissed by a person", triage)
	}
	// The decision is pending: the ask stays listed with it, by whom, and the
	// ticket's phase no longer waits.
	out = runJig(t, env, h.Clone, "status", fx.Ticket).Stdout
	if row := lineStarting(out, "  r1-f1,"); !strings.HasSuffix(row, ",,dismiss,person") {
		t.Errorf("the decided ask's row = %q, want it to end in the dismissal and its decider\n%s", row, out)
	}
	if want := "Run `jig gate " + fx.Ticket + "` to apply the decisions: the 1 decided ask is applied when the next round closes"; !strings.Contains(out, want) {
		t.Errorf("status help does not count the ask as decided (want %q):\n%s", want, out)
	}
	if p := nextJSON[phaseOut](t, env, h.Clone, fx.Ticket).Phase; !p.Ready || p.Code != "" {
		t.Errorf("after the decision: phase ready=%v code=%q, want ready", p.Ready, p.Code)
	}
	screens.press("g")
	screens.until("round 2 open", "✓ jig gate "+fx.Ticket)
	if row := lineStarting(runJig(t, env, h.Clone, "status", fx.Ticket).Stdout, "  r1-f1,"); !strings.HasSuffix(row, ",,dismiss,person") {
		t.Errorf("with round 2 open, the ask's row = %q, want the decision still shown", row)
	}

	// A reviewer in a session of its own reads round 2. Its request holds the
	// ask still, with the decision, so it does not raise it again; the dismissed
	// list stays empty, since only the round that closes moves the ask.
	reviewer := newHarness(t, fx, env, "s-reviewer")
	ctx := reviewer.Jig("review", "context", fx.Ticket)
	if ctx.Code != 0 {
		t.Fatalf("jig review context (round 2) exit = %d\nstdout:\n%s\nstderr:\n%s", ctx.Code, ctx.Stdout, ctx.Stderr)
	}
	// Round 2 is a delta round: the ask is in its answers table.
	const answersHeader = "answers[1]{id,file:line,action,risk,title,decision}:\n"
	i := strings.Index(ctx.Stdout, answersHeader)
	if i < 0 {
		t.Fatalf("the round's answers lack the ask:\n%s", ctx.Stdout)
	}
	if row := strings.SplitN(ctx.Stdout[i+len(answersHeader):], "\n", 2)[0]; !strings.HasSuffix(row, `,"decided: dismiss, by a person"`) {
		t.Errorf("the ask's open row = %q, want it to end in the decision and who made it", row)
	}
	if !strings.Contains(ctx.Stdout, "dismissed[0]") {
		t.Errorf("the decided ask was moved to the dismissed list before the round closed:\n%s", ctx.Stdout)
	}
	var req struct {
		Open []struct {
			ID        string `json:"id"`
			Decision  string `json:"decision"`
			DecidedBy string `json:"decided_by"`
		} `json:"open"`
		Dismissed []struct {
			ID string `json:"id"`
		} `json:"dismissed"`
	}
	asJSON := reviewer.Jig("review", "context", fx.Ticket, "--json")
	if err := json.Unmarshal([]byte(asJSON.Stdout), &req); err != nil {
		t.Fatalf("parse review context --json: %v\n%s", err, asJSON.Stdout)
	}
	if len(req.Open) != 1 || req.Open[0].ID != "r1-f1" || req.Open[0].Decision != "dismiss" || req.Open[0].DecidedBy != "person" || len(req.Dismissed) != 0 {
		t.Errorf("request open = %+v, dismissed = %+v, want r1-f1 dismissed by a person, and none dismissed yet", req.Open, req.Dismissed)
	}
	if stored := string(readFileOrFatal(t, b.workFile("gate.round-2.review.json"))); !strings.Contains(stored, `"decision": "dismiss"`) {
		t.Errorf("the stored review.json does not carry the decision:\n%s", stored)
	}
	if res := reviewer.Jig("review", "done", fx.Ticket, "--result", reviewResult(t, st, fx.Ticket, 2, false)); res.Code != 0 || !strings.Contains(res.Stdout, "verdict: clean") {
		t.Fatalf("jig review done (round 2) exit = %d, want 0 and a clean verdict\nstdout:\n%s\nstderr:\n%s", res.Code, res.Stdout, res.Stderr)
	}
	screens.until("round 2 clean")
	// The review screen lists the rounds newest first: round 2 above round 1.
	screens.press("\x1b[H")
	screens.press("\x1b[B")
	screens.press("\x1b[B")
	screens.press("\r")
	rounds := screens.until("round 2  clean", "round 1  findings")
	if strings.Index(rounds, "round 2  clean") > strings.Index(rounds, "round 1  findings") {
		t.Errorf("the review screen lists round 1 above round 2:\n%s", rounds)
	}
	// What each round read is the detail of its row: down from round 2 is round 1.
	screens.press("\x1b[H")
	screens.press("\x1b[B")
	screens.until("╭─ Detail · round 1", "5 files read")
	screens.press("\x7f")
	// The round that closed applied the dismissal: the ask is gone, and the finding
	// stays in the findings table as the round that raised it recorded it.
	if out = runJig(t, env, h.Clone, "status", fx.Ticket).Stdout; strings.Contains(out, "outstanding_asks") {
		t.Errorf("status still lists the ask after the round applied the decision:\n%s", out)
	} else if !strings.Contains(out, "r1-f1,1,ask,") {
		t.Errorf("status lost the finding the first round raised:\n%s", out)
	}

	// With no What written the key stops: the frame says where the bullets go and
	// how to give them, and nothing is asked or pushed.
	screens.press("p")
	screens.until("✓ jig publish "+fx.Ticket+" --body", "Write the pull request's What", "--what")
	if got := originRefOrEmpty(t, fx.RepoRemote, "refs/heads/"+branch); got != "" {
		t.Errorf("origin's %s = %s after a p that stopped on the What, want nothing pushed", branch, got)
	}
	for _, l := range readJournal(t, fx, fx.Ticket) {
		if l.Event == journal.EventPublishApproved || l.Event == journal.EventPublishDone {
			t.Errorf("the journal has a %s line after a p that stopped on the What", l.Event)
		}
	}

	// The What is given with the verb the frame named, and one key, and a yes to
	// the question, approves and publishes.
	what := filepath.Join(t.TempDir(), "what.md")
	if err := os.WriteFile(what, []byte("- Greet the person by the name they gave, so the page no longer says hello to nobody\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if res := h.Jig("publish", fx.Ticket, "--body", "--what", what); res.Code != 0 || !strings.Contains(res.Stdout, "what: given") {
		t.Fatalf("jig publish --body --what exit = %d, want 0 and the What given\nstdout:\n%s\nstderr:\n%s", res.Code, res.Stdout, res.Stderr)
	}
	screens.press("p")
	screens.until("approve the reviewed head")
	screens.press("y")
	screens.until("✓ jig publish "+fx.Ticket+" --yes", "◇ waits to land", "published")
	// o names the pull request by number and target; a pipe has no opener, so
	// the notice carries its address.
	screens.press("o")
	screens.until("PR #", " into ", "/pull/")
	// The publish screen's activity reads newest first: what was finished above
	// the changelog it wrote just before.
	screens.press("\x1b[H")
	for i := 0; i < 3; i++ {
		screens.press("\x1b[B")
	}
	screens.press("\r")
	// The body's detail is long and the activity is under it: the cursor goes to
	// the last check, whose detail is short, so both are on the screen.
	screens.until("╭─ Detail · body")
	screens.press("\x1b[F")
	publish := screens.until("╭─ Detail · published", "Publish activity", "finished", "changelog    written")
	activity = publish[strings.Index(publish, "Publish activity"):]
	if finished, written := strings.Index(activity, "finished"), strings.Index(activity, "changelog    written"); finished < 0 || finished > written {
		t.Errorf("the publish screen's activity does not read newest first:\n%s", publish)
	}
	screens.press("q")
	if res := tui.Stop(); res.Code != 0 {
		t.Fatalf("jig tui exit = %d, want 0 after q\nstderr:\n%s", res.Code, res.Stderr)
	} else if res.Stderr != "" {
		// The screens own the terminal: what a command they ran warned (the
		// publish's "no demo" here) belongs in its output on the screen.
		t.Errorf("jig tui wrote to stderr under its screens:\n%s", res.Stderr)
	}

	// What the keys left in the journal is what the same commands write.
	var events []string
	var approved, pr journal.Line
	for _, l := range readJournal(t, fx, fx.Ticket) {
		switch l.Event {
		case journal.EventPublishApproved:
			approved = l
		case journal.EventPr:
			pr = l
		case journal.EventAnswer, journal.EventTriage, journal.EventGateOpen, journal.EventGateRound, journal.EventGateClean, journal.EventPublishDone:
			// A round is opened by the gate and again by the reviewer's
			// `review context`: one open, however many lines say so.
			if len(events) == 0 || events[len(events)-1] != l.Event {
				events = append(events, l.Event)
			}
		}
	}
	want := []string{
		journal.EventAnswer, journal.EventGateOpen, journal.EventGateRound, journal.EventTriage,
		journal.EventGateOpen, journal.EventGateClean, journal.EventPublishDone,
	}
	if strings.Join(events, ",") != strings.Join(want, ",") {
		t.Errorf("journal events = %v, want %v", events, want)
	}
	if approved.Provenance != "person" || approved.Commit != last || approved.Branch != branch {
		t.Errorf("publish-approved line = %+v, want a person's approval of %s on %s", approved, last, branch)
	}
	if pr.Outcome != "opened" || pr.Commit != last || pr.Branch != branch || !strings.Contains(pr.URL, "/pull/") {
		t.Errorf("pr line = %+v, want an opened pull request for %s from %s", pr, last, branch)
	}
	if got := originRefOrEmpty(t, fx.RepoRemote, "refs/heads/"+branch); got != last {
		t.Errorf("origin's %s = %s, want the reviewed head %s", branch, got, last)
	}
	created := false
	for _, c := range ghCalls(t, ghLog) {
		created = created || strings.HasPrefix(c, "pr create")
	}
	if !created {
		t.Errorf("the fake gh was never asked to open a pull request: %v", ghCalls(t, ghLog))
	}
}
