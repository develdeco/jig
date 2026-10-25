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
func TestTUIHandBuiltToPublish(t *testing.T) {
	t.Parallel()
	fx, env := newFixture(t, fixture.Opts{})
	dropEnvFromSlices(t, fx)
	h := newHarness(t, fx, env, "s-builder")
	branch := "jig/" + fx.Ticket
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

	// The first commit, verified by a worker the builder starts. Like every
	// verification of the suite it holds one of the slots that bound how many
	// `go test` runs the parallel tests have going at once: under a loaded
	// machine the screens would otherwise wait out their minute on a worker
	// that is queued behind the rest.
	func() {
		verifySlots <- struct{}{}
		defer func() { <-verifySlots }()
		sha := h.CommitScenario("a", 1)
		if res := h.Jig("verify", fx.Ticket, sha, "--slice", "a"); res.Code != 0 || !strings.Contains(res.Stdout, "state: pending") {
			t.Fatalf("jig verify a exit = %d, want 0 and a pending worker\nstdout:\n%s\nstderr:\n%s", res.Code, res.Stdout, res.Stderr)
		}
		screens.untilWithin(verifyWait, "1 of 4 green")
	}()

	// The builder asks, the question reaches the screens, and the person
	// answers it from them.
	if res := h.Jig("ask", fx.Ticket, "--slice", "c", "Formal or casual?"); res.Code != 0 {
		t.Fatalf("jig ask exit = %d\nstdout:\n%s\nstderr:\n%s", res.Code, res.Stdout, res.Stderr)
	}
	screens.until("Formal or casual?")
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
	rest := []struct{ slice, sha string }{{"b", h.CommitScenario("b", 2)}, {"c", h.CommitScenario("c", 2)}, {"d", h.CommitScenario("d", 1)}}
	last := rest[len(rest)-1].sha
	verifySlots <- struct{}{}
	for _, s := range rest {
		if res := h.Jig("verify", fx.Ticket, s.sha, "--slice", s.slice); res.Code != 0 || !strings.Contains(res.Stdout, "state: pending") {
			t.Fatalf("jig verify %s exit = %d, want 0 and a pending worker\nstdout:\n%s\nstderr:\n%s", s.slice, res.Code, res.Stdout, res.Stderr)
		}
	}
	for _, s := range rest {
		l := waitJournal(t, fx, fx.Ticket, verifyWait, func(l journal.Line) bool {
			return l.Slice == s.slice && ((l.Event == journal.EventVerified && l.Commit == s.sha) || (l.Event == journal.EventOracle && l.Outcome == "fail" && l.Commit == s.sha))
		})
		if l.Event != journal.EventVerified {
			t.Fatalf("slice %s failed its oracle at %s: %s", s.slice, s.sha, l.Summary)
		}
	}
	<-verifySlots
	h.WaitVerifiers()
	screens.until("4 slices green, each verified by jig")

	// The gate opens from the screens. A reviewer in a session of its own
	// reads the round and reports one ask.
	screens.press("g")
	screens.until("round 1 open", "✓ jig gate "+fx.Ticket)
	reviewer := newHarness(t, fx, env, "s-reviewer")
	if res := reviewer.Jig("review", "context", fx.Ticket); res.Code != 0 {
		t.Fatalf("jig review context exit = %d\nstdout:\n%s\nstderr:\n%s", res.Code, res.Stdout, res.Stderr)
	}
	reviewer.Bash("git log --oneline -3")
	if res := reviewer.Jig("review", "done", fx.Ticket, "--result", reviewResult(t, st, fx.Ticket, 1, true)); !strings.Contains(res.Stdout, "round: 1") {
		t.Fatalf("jig review done (round 1) exit = %d\nstdout:\n%s\nstderr:\n%s", res.Code, res.Stdout, res.Stderr)
	}
	if screen := screens.until("round 1 left 1 ask"); !strings.Contains(screen, "Which greeting does the product want?") {
		t.Errorf("the screen with the round's findings does not show the ask:\n%s", screen)
	}

	// The person dismisses the ask from the screens, and opens the next round.
	screens.press("d")
	screens.until("dismissed by you", "✓ jig triage "+fx.Ticket+" r1-f1 dismiss")
	triage := waitJournal(t, fx, fx.Ticket, tuiWait, func(l journal.Line) bool { return l.Event == journal.EventTriage })
	if triage.Ref != "r1-f1" || triage.Outcome != "dismiss" || triage.Provenance != "person" {
		t.Errorf("triage line = %+v, want r1-f1 dismissed by a person", triage)
	}
	screens.press("g")
	screens.until("round 2 open", "✓ jig gate "+fx.Ticket)
	if res := reviewer.Jig("review", "context", fx.Ticket); res.Code != 0 {
		t.Fatalf("jig review context (round 2) exit = %d\nstdout:\n%s\nstderr:\n%s", res.Code, res.Stdout, res.Stderr)
	}
	if res := reviewer.Jig("review", "done", fx.Ticket, "--result", reviewResult(t, st, fx.Ticket, 2, false)); res.Code != 0 || !strings.Contains(res.Stdout, "verdict: clean") {
		t.Fatalf("jig review done (round 2) exit = %d, want 0 and a clean verdict\nstdout:\n%s\nstderr:\n%s", res.Code, res.Stdout, res.Stderr)
	}
	screens.until("round 2 clean")

	// One key, and a yes to the question, approves and publishes.
	screens.press("p")
	screens.until("approve the reviewed head")
	screens.press("y")
	screens.until("✓ jig publish "+fx.Ticket+" --approve", "✓ jig publish "+fx.Ticket+" --yes", "✓ published")
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
