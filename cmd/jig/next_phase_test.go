package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/events"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/ledger"
	"github.com/develdeco/jig/internal/store"
	"github.com/develdeco/jig/internal/tui"
	"github.com/develdeco/jig/internal/verifydeliver"
)

// The tests here walk a ticket through the points it passes from a drafted
// brief to a published pull request, writing in a store what each verb leaves,
// and ask the readers of a ticket's phase what they say at each point: `jig
// next <ticket>` and the screens. The end-to-end chain (e2e's TestDigesterFlow)
// asks `jig next` at the same points, with every state made by the real verbs;
// the rules behind each answer are internal/phase's (Derive, For), so this is
// the seam where the words `jig next` prints, and the agreement of the screens
// with it, are pinned. The store is made of files alone: no test here starts git.

// phaseRig is a store with one ticket, JIG-1, of a brief of two sections and
// four slices, and the env that reads it.
type phaseRig struct {
	st     *store.Store
	root   string
	home   string
	ticket string
}

func newPhaseRig(t *testing.T) phaseRig {
	t.Helper()
	root := t.TempDir()
	put := func(name, content string) {
		path := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	put("project.yaml", "schema_version: 2\nname: demo\nkeys:\n  JIG: everything in demo\ntrackers: []\n")
	put("tickets/JIG-1/ticket.yaml", "schema_version: 1\ntitle: Fixture ticket\n")
	put("tickets/JIG-1/brief.md", "# Fixture ticket\n\n## Goal\n\nDo it.\n\n## Slices\n\nFour of them.\n")
	slice := func(id, workspace, blockedBy string) string {
		return fmt.Sprintf("  - id: %s\n    workspace: %s\n    goal: Build %s.\n    oracle: test\n    blocked_by: %s\n    from_brief: []\n", id, workspace, id, blockedBy)
	}
	put("tickets/JIG-1/slices.yaml", "slices:\n"+slice("a", "alpha", "[]")+slice("b", "alpha", "[a]")+slice("c", "beta", "[]")+slice("d", "beta", "[]"))
	st, err := store.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	return phaseRig{st: st, root: root, home: t.TempDir(), ticket: "JIG-1"}
}

// env is the env of a session (none when empty) that reads the rig's store.
func (r phaseRig) env(session string) env {
	vars := map[string]string{"JIG_HOME": r.home, "HOME": r.home, "USERPROFILE": r.home}
	if session != "" {
		vars["JIG_SESSION"] = session
	}
	return envFrom(vars)
}

func (r phaseRig) line(t *testing.T, l journal.Line) {
	t.Helper()
	if err := journal.Append(r.st, r.ticket, l); err != nil {
		t.Fatal(err)
	}
}

// openRound leaves what `jig gate` leaves of round n: its open record and the
// journal line that says the gate was opened.
func (r phaseRig) openRound(t *testing.T, n int) {
	t.Helper()
	path := filepath.Join(r.st.TicketDir(r.ticket), "work", fmt.Sprintf("gate.round-%d.open.yaml", n))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	body := fmt.Sprintf("round: %d\nticket: %s\nbranch: JIG-1-fixture-ticket\nhead: %s\ncopy: the clone's\n", n, r.ticket, sha40("e"))
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	r.line(t, journal.Line{Event: journal.EventGateOpen, Attempt: n})
}

// green marks every slice green and verified, as the builder's commits leave them.
func (r phaseRig) green(t *testing.T, session string) {
	t.Helper()
	for _, id := range []string{"a", "b", "c", "d"} {
		if err := r.st.WriteSliceState(r.ticket, id, store.SliceState{State: "green", Attempts: 1}); err != nil {
			t.Fatal(err)
		}
		r.line(t, journal.Line{Event: journal.EventVerified, Slice: id, Session: session, Commit: sha40(id)})
	}
}

// phaseStage is one point of the walk, and what a reader of the phase must say
// at it: the phase the ticket is at, whether it can start, the code that says
// why not, the prompt that starts it and the state of each phase ("dcpp" is the
// brief done, the build current, the rest pending). asked is what --for must
// answer there.
type phaseStage struct {
	name   string
	phase  string
	ready  bool
	code   string
	prompt string
	states string
	reason string // a word the reason holds, when the phase cannot start
	asked  []phaseAsk
}

// phaseAsk is the answer --for must give at a stage: a refusal with its code
// and a word of its reason, or a warning that begins and ends as given.
type phaseAsk struct {
	phase, code, reason, warnFrom, warnTo string
	ready                                 bool
}

// walk takes the ticket through its stages and calls check at each, after the
// store holds what the stage means.
func (r phaseRig) walk(t *testing.T, check func(s phaseStage)) {
	t.Helper()
	T := r.ticket
	prompt := func(verb string) string { return verb + " " + T }
	const reviewed = "round: %d\nverdict: %s\nindependent: true\nreviewer:\n  session: s-rev\n  how: env\n"

	check(phaseStage{name: "a drafted brief", phase: "brief", ready: true, prompt: prompt("Plan"), states: "cppp",
		asked: []phaseAsk{{phase: "build", code: "PHASE_BRIEF_NOT_APPROVED", reason: "is not approved"}}})

	approveBrief(t, r.st, T)
	check(phaseStage{name: "an approved brief", phase: "build", ready: true, prompt: prompt("Build"), states: "dcpp",
		asked: []phaseAsk{{phase: "brief", ready: true, warnFrom: "brief is done (approved by ", warnTo: "; prompt: " + prompt("Build")}}})

	r.line(t, journal.Line{Event: journal.EventClaim, Slice: "a", Session: "s-claim"})
	if err := r.st.WriteSliceState(T, "a", store.SliceState{State: "building", Attempts: 1}); err != nil {
		t.Fatal(err)
	}
	check(phaseStage{name: "a build under way", phase: "build", ready: true, prompt: prompt("Build"), states: "dcpp",
		asked: []phaseAsk{{phase: "review", code: "PHASE_NOT_BUILT"}}})

	r.green(t, "s-claim")
	check(phaseStage{name: "a build done", phase: "review", ready: true, prompt: prompt("Review"), states: "ddcp",
		asked: []phaseAsk{
			{phase: "build", ready: true, warnFrom: "build is done (", warnTo: "; prompt: " + prompt("Review")},
			{phase: "publish", code: "PHASE_NOT_REVIEWED"},
		}})

	r.openRound(t, 1)
	check(phaseStage{name: "a round open", phase: "review", code: "PHASE_ROUND_OPEN", prompt: prompt("Review"), states: "ddcp", reason: "round 1"})

	writeRound(t, r.root, T, 1, roundFiles{report: fmt.Sprintf(reviewed, 1, "findings")})
	writeAsks(t, r.root, T, askSpec{id: "r1-f1"})
	r.line(t, journal.Line{Event: journal.EventGateRound, Attempt: 1, Session: "s-rev"})
	check(phaseStage{name: "a round with an ask to decide", phase: "review", code: "PHASE_ASKS_UNDECIDED", prompt: prompt("Review"), states: "ddcp", reason: "1 ask of "})

	// The decision is the line `jig triage` journals (the status_triage tests
	// run the verb).
	r.line(t, journal.Line{Event: journal.EventTriage, Ref: "r1-f1", Outcome: "dismiss", Provenance: "person"})
	check(phaseStage{name: "an ask decided", phase: "review", ready: true, prompt: prompt("Review"), states: "ddcp"})

	r.openRound(t, 2)
	check(phaseStage{name: "round 2 open", phase: "review", code: "PHASE_ROUND_OPEN", prompt: prompt("Review"), states: "ddcp", reason: "round 2"})

	writeRound(t, r.root, T, 2, roundFiles{report: fmt.Sprintf(reviewed, 2, "clean")})
	r.line(t, journal.Line{Event: journal.EventGateClean, Attempt: 2, Session: "s-rev"})
	check(phaseStage{name: "a clean round", phase: "publish", ready: true, prompt: prompt("Publish"), states: "dddc"})

	r.line(t, journal.Line{Event: journal.EventPublishDone})
	check(phaseStage{name: "a published ticket", phase: "publish", code: "PHASE_DONE", states: "dddd"})
}

// nextPhaseOut is `jig next <ticket> --json` as these tests read it: the phase
// block, and the freshness `jig next` names beside it.
type nextPhaseOut struct {
	Fresh  string   `json:"fresh"`
	Advice []string `json:"advice"`
	Phase  struct {
		Name   string `json:"name"`
		Ready  bool   `json:"ready"`
		Code   string `json:"code"`
		Reason string `json:"reason"`
		Prompt string `json:"prompt"`
		Phases []struct {
			Name  string `json:"name"`
			State string `json:"state"`
		} `json:"phases"`
		For *struct {
			Phase   string `json:"phase"`
			Ready   bool   `json:"ready"`
			Code    string `json:"code"`
			Reason  string `json:"reason"`
			Warning string `json:"warning"`
		} `json:"for"`
	} `json:"phase"`
}

// next runs `jig next <ticket> args...` as e, in JSON and in text.
func (r phaseRig) next(t *testing.T, e env, args ...string) (nextPhaseOut, string) {
	t.Helper()
	asJSON, code := runMain(t, e, "", append([]string{"next", "--store", r.root, "--json", r.ticket}, args...)...)
	var out nextPhaseOut
	if err := json.Unmarshal([]byte(asJSON), &out); code != 0 || err != nil {
		t.Fatalf("jig next --json %s %v: exit %d, %v\n%s", r.ticket, args, code, err, asJSON)
	}
	text, code := runMain(t, e, "", append([]string{"next", "--store", r.root, r.ticket}, args...)...)
	if code != 0 {
		t.Fatalf("jig next %s %v: exit %d\n%s", r.ticket, args, code, text)
	}
	return out, text
}

func yesNo2(b bool) string { return map[bool]string{true: "yes", false: "no"}[b] }

// TestNextNamesThePhaseAndPrompt: at each point a ticket passes through, `jig
// next <ticket>` names the phase it is at, whether that phase can start, the
// prompt that starts it and, when it cannot, the code and the reason, in text
// and in JSON, with the state of each of the four phases; and --for answers for
// a phase asked out of its turn: a refusal with its code, or a warning that
// moves on with the prompt of the phase the ticket is at.
func TestNextNamesThePhaseAndPrompt(t *testing.T) {
	t.Parallel()
	r := newPhaseRig(t)
	e := r.env("")
	r.walk(t, func(s phaseStage) {
		out, text := r.next(t, e)
		p := out.Phase
		states := ""
		for _, ph := range p.Phases {
			states += ph.State[:1]
		}
		if p.Name != s.phase || p.Ready != s.ready || p.Code != s.code || p.Prompt != s.prompt || states != s.states {
			t.Errorf("%s: phase=%s ready=%v code=%q prompt=%q states=%s, want %s %v %q %q %s (reason %q)",
				s.name, p.Name, p.Ready, p.Code, p.Prompt, states, s.phase, s.ready, s.code, s.prompt, s.states, p.Reason)
		}
		if !s.ready && (p.Reason == "" || !strings.Contains(p.Reason, s.reason)) {
			t.Errorf("%s: not ready, and the reason is %q, want one that says %q", s.name, p.Reason, s.reason)
		}
		wants := []string{"phase: " + s.phase + "\n", "ready: " + yesNo2(s.ready) + "\n"}
		if s.prompt != "" {
			wants = append(wants, "prompt: "+s.prompt+"\n")
		}
		if !s.ready {
			wants = append(wants, "code: "+s.code+"\n", "reason: ")
		}
		for _, want := range wants {
			if !strings.Contains(text, want) {
				t.Errorf("%s: jig next lacks %q:\n%s", s.name, want, text)
			}
		}
		if s.ready && (strings.Contains(text, "\ncode: ") || strings.Contains(text, "\nreason: ")) {
			t.Errorf("%s: a phase that can start prints a code or a reason:\n%s", s.name, text)
		}

		for _, a := range s.asked {
			asked, askedText := r.next(t, e, "--for", a.phase)
			f := asked.Phase.For
			if f == nil || f.Phase != a.phase || f.Ready != a.ready || f.Code != a.code {
				t.Errorf("%s: --for %s: %+v, want ready=%v code=%q", s.name, a.phase, f, a.ready, a.code)
				continue
			}
			if a.reason != "" && !strings.Contains(f.Reason, a.reason) {
				t.Errorf("%s: --for %s: reason %q, want it to say %q", s.name, a.phase, f.Reason, a.reason)
			}
			if a.warnFrom != "" && (!strings.HasPrefix(f.Warning, a.warnFrom) || !strings.HasSuffix(f.Warning, a.warnTo)) {
				t.Errorf("%s: --for %s: warning %q, want one from %q to %q", s.name, a.phase, f.Warning, a.warnFrom, a.warnTo)
			}
			wantText := []string{"for: " + a.phase + "\n", "for_ready: " + yesNo2(a.ready) + "\n"}
			if a.code != "" {
				wantText = append(wantText, "for_code: "+a.code+"\n")
			}
			if a.warnFrom != "" {
				wantText = append(wantText, `warning: "`+a.warnFrom)
			}
			for _, want := range wantText {
				if !strings.Contains(askedText, want) {
					t.Errorf("%s: jig next --for %s lacks %q:\n%s", s.name, a.phase, want, askedText)
				}
			}
		}
	})
}

// TestTheScreensStandWhereJigNextDoes: at each point a ticket passes through,
// the screens, which read `jig status` and the journal, put it where `jig next`,
// which reads the store, does: the same current phase, the same answer to
// whether it can start and the same state for each of the four phases. The
// rule is internal/phase's, and internal/tui pins that the screens derive
// nothing of their own; this is the check that what the real status prints
// and the real store holds give both the same facts.
func TestTheScreensStandWhereJigNextDoes(t *testing.T) {
	t.Parallel()
	r := newPhaseRig(t)
	e := r.env("")
	r.walk(t, func(s phaseStage) {
		T := r.ticket
		status, err := RenderStatus(r.st, T)
		if err != nil {
			t.Fatalf("%s: RenderStatus: %v", s.name, err)
		}
		lines, err := journal.Read(r.st, T)
		if err != nil {
			t.Fatal(err)
		}
		var envs []events.Envelope
		for _, l := range lines {
			envs = append(envs, events.Envelope{Src: events.SrcJournal, Store: "demo", Line: l})
		}
		got := tui.StandingOf(T, status, envs)
		next, _ := r.next(t, e)
		p := next.Phase
		if got.Current != s.phase || got.Ready != s.ready {
			t.Errorf("%s: the screens stand at %s (ready %v), want %s (ready %v)", s.name, got.Current, got.Ready, s.phase, s.ready)
		}
		if got.Current != p.Name || got.Ready != p.Ready {
			t.Errorf("%s: the screens stand at %s (ready %v), jig next at %s (ready %v: %s)", s.name, got.Current, got.Ready, p.Name, p.Ready, p.Reason)
		}
		for i, ph := range p.Phases {
			if got.States[i] != ph.State {
				t.Errorf("%s: the screens have %s %s, jig next %s", s.name, ph.Name, got.States[i], ph.State)
			}
		}
	})
}

// TestNextAndGateAdviseWhoReviews: when review is the phase that can start,
// `jig next` and `jig gate` tell the session that asks whether it built the
// ticket, and so whether it may read the round itself or has a subagent do it.
// A session that claimed a slice and had it verified is told to spawn a
// subagent, and why; a session that touched nothing of the ticket is told to
// review it in its own context; a session jig has no record of is told to
// spawn one, as the builder is, saying jig cannot tell; and no session, as when
// the TUI's g opens the round, is told nothing about itself. With a round open
// its reviewer is at work and `jig next` advises nothing.
func TestNextAndGateAdviseWhoReviews(t *testing.T) {
	t.Parallel()
	r := newPhaseRig(t)
	T := r.ticket
	approveBrief(t, r.st, T)
	r.line(t, journal.Line{Event: journal.EventApproval, Session: "s-builder", Provenance: "person"})
	r.line(t, journal.Line{Event: journal.EventClaim, Slice: "a", Session: "s-builder"})
	r.green(t, "s-builder")
	if err := ledger.Append(r.home, journal.Line{Event: journal.EventSessionStart, Session: "s-fresh"}); err != nil {
		t.Fatal(err)
	}
	deps := verifydeliver.Deps{Store: r.st, Home: r.home}

	spawn := []string{
		"spawn a subagent to review", "Agent tool", "subagent_type: general-purpose", "model: opus", "no worktree isolation",
		"TaskOutput (`block: true`", "Do not end the turn or schedule a wakeup while it runs", "never call another tool to stall",
		"You did not build " + T, "jig skill review", "jig review context " + T, "Reply with the verdict line only",
		"must not run `jig review context` itself",
	}
	for _, tc := range []struct {
		name, session, fresh string
		wants, nots          []string
	}{
		{"the builder", "s-builder", "no", append([]string{"claimed slice a", "approved the brief"}, spawn...), []string{"review it in this session"}},
		{"a session that touched nothing", "s-fresh", "yes", []string{"review it in this session", "jig skill review"}, []string{"spawn a subagent"}},
		{"a session jig has no record of", "s-unseen", "unknown", append([]string{"cannot tell whether this session built " + T}, spawn...), []string{"review it in this session"}},
	} {
		e := r.env(tc.session)
		out, text := r.next(t, e)
		if out.Phase.Name != "review" || !out.Phase.Ready || out.Fresh != tc.fresh {
			t.Errorf("%s: jig next: phase %s ready=%v fresh=%q, want review, ready, %q", tc.name, out.Phase.Name, out.Phase.Ready, out.Fresh, tc.fresh)
		}
		if !strings.Contains(text, "fresh: "+tc.fresh+"\n") {
			t.Errorf("%s: jig next lacks %q:\n%s", tc.name, "fresh: "+tc.fresh, text)
		}
		gate := strings.Join(gateAdvice(e, deps, T), "\n")
		for _, where := range []struct{ what, text string }{{"jig next", text}, {"jig gate", gate}, {"the JSON advice", strings.Join(out.Advice, "\n")}} {
			for _, want := range tc.wants {
				if !strings.Contains(where.text, want) {
					t.Errorf("%s: %s lacks %q:\n%s", tc.name, where.what, want, where.text)
				}
			}
			for _, not := range tc.nots {
				if strings.Contains(where.text, not) {
					t.Errorf("%s: %s has %q, want it left out:\n%s", tc.name, where.what, not, where.text)
				}
			}
		}
		if tc.fresh == "yes" && len(out.Advice) != 1 {
			t.Errorf("%s: advice = %q, want one line", tc.name, out.Advice)
		}
	}

	// No session: nothing is said of it, and the gate says only what a reviewer
	// does and who must not be one.
	out, text := r.next(t, r.env(""))
	if out.Phase.Name != "review" || !out.Phase.Ready || out.Fresh != "" || len(out.Advice) != 0 || strings.Contains(text, "fresh:") || strings.Contains(text, "spawn a subagent") {
		t.Errorf("jig next with no session: %+v\n%s", out, text)
	}
	gate := strings.Join(gateAdvice(r.env(""), deps, T), "\n")
	if !strings.Contains(gate, "`jig review context "+T+"`") || !strings.Contains(gate, "REVIEW_NOT_INDEPENDENT") || strings.Contains(gate, "this session") || strings.Contains(gate, "spawn a subagent") {
		t.Errorf("jig gate advice with no session:\n%s", gate)
	}

	// With the round open its reviewer is at work: jig next has no advice.
	r.openRound(t, 1)
	out, text = r.next(t, r.env("s-builder"))
	if out.Phase.Code != "PHASE_ROUND_OPEN" || out.Fresh != "" || len(out.Advice) != 0 || strings.Contains(text, "fresh:") || strings.Contains(text, "spawn a subagent") {
		t.Errorf("jig next with the round open still advises who reviews: %+v\n%s", out, text)
	}
}

// TestATicketFolderOfLocksIsNoTicket: a ticket folder holding nothing but
// jig's lock files (what a store reset or checkout leaves of a ticket it
// removed, since the store ignores them) is not a ticket at the brief phase.
// `jig next` refuses it and points at `jig ticket new`, as for a ticket with no
// folder, so a session asked to plan it mints the ticket instead of drafting a
// brief into a folder that has no record. Nor does the store's listing show it.
func TestATicketFolderOfLocksIsNoTicket(t *testing.T) {
	t.Parallel()
	r := newPhaseRig(t)
	dir := filepath.Join(r.root, "tickets", "JIG-404")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"ticket.yaml.lock", "journal.ndjson.lock"} {
		if err := os.WriteFile(filepath.Join(dir, name), nil, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	out, code := runMain(t, r.env(""), "", "next", "--store", r.root, "JIG-404")
	if code != 2 || !strings.Contains(out, "not found in the store") || !strings.Contains(out, "jig ticket new") {
		t.Errorf("jig next on a folder of locks: exit %d, want 2 saying it is not found and to mint it\n%s", code, out)
	}
	if out, code := runMain(t, r.env(""), "", "next", "--store", r.root); code != 0 || strings.Contains(out, "JIG-404") || !strings.Contains(out, r.ticket) {
		t.Errorf("jig next lists a folder of locks, or not the store's ticket: exit %d\n%s", code, out)
	}
}
