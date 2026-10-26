package e2e

// End-to-end tests of who reviews a ticket (decision 39): `jig gate` and `jig
// next` tell the session that asks whether it built the ticket, and so whether
// it may read the round itself or has a subagent do it; the subagent's round is
// independent where the main session's would not be; and an unattended session
// that stops with the build done is told to open the gate.

import (
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
)

// jigHooked runs jig as the agent h speaks for and fires the PostToolUse hook
// for the command, as Claude Code does after its Bash tool ran it: the ledger
// then has the tool line, with the agent's id when h is a subagent.
func jigHooked(h *fakeHarness, args ...string) jigResult {
	h.t.Helper()
	res := h.Jig(args...)
	h.Hook("PostToolUse", map[string]any{
		"tool_name": "Bash",
		"command":   "jig " + strings.Join(args, " "),
		"tool_response": map[string]any{
			"stdout": res.Stdout, "stderr": res.Stderr, "interrupted": false,
		},
	})
	return res
}

// freshnessOut is the part of `jig next <ticket> --json` that says who reviews.
type freshnessOut struct {
	Fresh  string   `json:"fresh"`
	Advice []string `json:"advice"`
}

// TestReviewFromBuildSessionIsAdvisedToSpawn: a session that approved the
// brief, claimed a slice and had its commits verified is told, by `jig next`
// and by `jig gate`, to spawn a subagent to review, and why; the advice names
// the Agent tool, a prompt to pass, and that the main session must not run
// `jig review context` itself. A session that touched nothing of the ticket is
// told to review it in its own context, one jig has no record of that jig cannot
// tell and the same advice as the builder, and no session at all is told
// nothing about itself.
func TestReviewFromBuildSessionIsAdvisedToSpawn(t *testing.T) {
	t.Parallel()
	fx, env := newFixture(t, fixture.Opts{})
	T := fx.Ticket
	builderEnv := env.with("JIG_SESSION=s-builder")
	if r := runJig(t, builderEnv, fx.RepoDir, "validate", T); r.Code != 0 {
		t.Fatalf("jig validate exit = %d\nstdout:\n%s\nstderr:\n%s", r.Code, r.Stdout, r.Stderr)
	}
	if r := runJig(t, builderEnv, fx.RepoDir, "claim", T, "--slice", "a"); r.Code != 0 {
		t.Fatalf("jig claim exit = %d\nstdout:\n%s\nstderr:\n%s", r.Code, r.Stdout, r.Stderr)
	}
	b := buildTicketOn(t, fx, env, true)

	fresh := newHarness(t, fx, env, "s-fresh")
	fresh.Hook("SessionStart", map[string]any{"source": "startup", "model": "claude-test"})
	unseen := newHarness(t, fx, env, "s-unseen")

	// The advice the builder gets, from `jig next` (review is the phase that can
	// start) in text and in JSON.
	spawn := []string{
		"spawn a subagent to review", "claimed slice a", "approved the brief",
		"Agent tool", "subagent_type: general-purpose", "model: opus", "no worktree isolation",
		"TaskOutput (`block: true`", "an unattended session ends with its turn", "Never call another tool to stall",
		"You did not build " + T, "jig skill review", "jig review context " + T, "Reply with the verdict line only",
		"must not run `jig review context` itself",
	}
	mustSay := func(who, text string, wants ...string) {
		t.Helper()
		for _, want := range wants {
			if !strings.Contains(text, want) {
				t.Errorf("%s: output lacks %q:\n%s", who, want, text)
			}
		}
	}
	mustNotSay := func(who, text string, nots ...string) {
		t.Helper()
		for _, not := range nots {
			if strings.Contains(text, not) {
				t.Errorf("%s: output has %q, want it left out:\n%s", who, not, text)
			}
		}
	}
	next := b.builder.Jig("next", T)
	if next.Code != 0 {
		t.Fatalf("jig next exit = %d\nstdout:\n%s\nstderr:\n%s", next.Code, next.Stdout, next.Stderr)
	}
	mustSay("builder's next", next.Stdout, append(spawn, "phase: review", "ready: yes", "fresh: no\n")...)
	mustNotSay("builder's next", next.Stdout, "review it in this session")
	j := nextJSON[freshnessOut](t, b.env.with("JIG_SESSION=s-builder"), fx.RepoDir, T)
	if j.Fresh != "no" || len(j.Advice) < 2 || !strings.Contains(j.Advice[0], "claimed slice a") {
		t.Errorf("builder's next --json: fresh=%q advice=%q, want no, and advice that starts with why", j.Fresh, j.Advice)
	}

	// A session that touched nothing is told to review it itself.
	got := fresh.Jig("next", T)
	mustSay("fresh session's next", got.Stdout, "review it in this session", "jig skill review", "fresh: yes\n")
	mustNotSay("fresh session's next", got.Stdout, "spawn a subagent")
	if j := nextJSON[freshnessOut](t, env.with("JIG_SESSION=s-fresh"), fx.RepoDir, T); j.Fresh != "yes" || len(j.Advice) != 1 {
		t.Errorf("fresh session's next --json: fresh=%q advice=%q, want yes and one line", j.Fresh, j.Advice)
	}

	// A session jig has no record of may or may not have built it.
	got = unseen.Jig("next", T)
	mustSay("unseen session's next", got.Stdout, "cannot tell whether this session built "+T, "spawn a subagent", "Agent tool", "fresh: unknown\n")

	// No session: the TUI's `g` has none. Nothing is said of it.
	got = runJig(t, env, fx.RepoDir, "next", T)
	mustSay("sessionless next", got.Stdout, "phase: review", "ready: yes")
	mustNotSay("sessionless next", got.Stdout, "fresh:", "spawn a subagent", "review it in this session")

	// `jig gate` says the same, as it opens the round: the builder is told to
	// spawn, a session that touched nothing to review, and no session neither.
	gate := b.builder.Jig("gate", T)
	if gate.Code != 0 {
		t.Fatalf("jig gate exit = %d\nstdout:\n%s\nstderr:\n%s", gate.Code, gate.Stdout, gate.Stderr)
	}
	mustSay("builder's gate", gate.Stdout, append(spawn, "round: 1")...)
	mustNotSay("builder's gate", gate.Stdout, "review it in this session")

	got = fresh.Jig("gate", T)
	mustSay("fresh session's gate", got.Stdout, "review it in this session", "jig skill review")
	mustNotSay("fresh session's gate", got.Stdout, "spawn a subagent")

	got = unseen.Jig("gate", T)
	mustSay("unseen session's gate", got.Stdout, "cannot tell whether this session built "+T, "spawn a subagent")

	got = runJig(t, env, fx.RepoDir, "gate", T)
	mustSay("sessionless gate", got.Stdout, "round: 1", "`jig review context "+T+"`")
	mustNotSay("sessionless gate", got.Stdout, "this session", "spawn a subagent", "must not run `jig review context` itself")

	// With the round open its reviewer is at work: `jig next` has no advice.
	got = b.builder.Jig("next", T)
	mustNotSay("next with the round open", got.Stdout, "fresh:", "spawn a subagent")
}

// TestSubagentReviewIsIndependent: the builder session S builds the ticket and
// opens the round; a subagent of S (hook payloads that carry an agent id) reads
// it, reports a finding and closes it, and the round is independent: the
// ledger's tool line for `jig review context` names the subagent, and the
// subagent is none of the builders.
func TestSubagentReviewIsIndependent(t *testing.T) {
	t.Parallel()
	b := newBuiltTicket(t, true)
	T := b.fx.Ticket
	b.openRound(t)

	sub := newHarness(t, b.fx, b.env, "s-builder")
	sub.Agent = "agent-reviewer"
	if ctx := jigHooked(sub, "review", "context", T); ctx.Code != 0 {
		t.Fatalf("subagent: jig review context exit = %d\nstdout:\n%s\nstderr:\n%s", ctx.Code, ctx.Stdout, ctx.Stderr)
	}
	must := b.mustReview(t)
	if f := jigHooked(sub, "finding", T, "--file", must[0], "--line", "1", "--title", "A name worth a look", "--detail", "The name does not say what it holds.",
		"--action", "note", "--risk", "low", "--risk-rationale", "a name only"); f.Code != 0 {
		t.Fatalf("subagent: jig finding exit = %d\nstdout:\n%s\nstderr:\n%s", f.Code, f.Stdout, f.Stderr)
	}
	done := jigHooked(sub, "review", "done", T, "--reviewed", strings.Join(must, ","), "--summary", "read the change")
	if done.Code != 0 || !strings.Contains(done.Stdout, "independent: true") {
		t.Fatalf("subagent: jig review done exit = %d, want 0 and an independent round\nstdout:\n%s\nstderr:\n%s", done.Code, done.Stdout, done.Stderr)
	}
	if rep := b.report(t, "1"); rep["independent"] != true {
		t.Errorf("report.yaml = %v, want independent: true", rep)
	}
	reviewer, _ := b.report(t, "1")["reviewer"].(map[string]any)
	if reviewer["session"] != "s-builder" || reviewer["agent"] != "agent-reviewer" {
		t.Errorf("report.yaml reviewer = %v, want the subagent agent-reviewer of session s-builder", reviewer)
	}
}

// TestMainAndSubagentBothRunningContextIsNotIndependent: the main session of
// the builder and its subagent both run `jig review context`, and the subagent
// closes the round. The ledger cannot say which of them read it as the
// reviewer, so the round is taken for the builder's own and is not independent.
// This is why the advice says the main session must not run it.
func TestMainAndSubagentBothRunningContextIsNotIndependent(t *testing.T) {
	t.Parallel()
	b := newBuiltTicket(t, true)
	T := b.fx.Ticket
	b.openRound(t)

	main := newHarness(t, b.fx, b.env, "s-builder")
	sub := newHarness(t, b.fx, b.env, "s-builder")
	sub.Agent = "agent-reviewer"
	for _, h := range []*fakeHarness{main, sub} {
		if ctx := jigHooked(h, "review", "context", T); ctx.Code != 0 {
			t.Fatalf("jig review context as agent %q exit = %d\nstdout:\n%s\nstderr:\n%s", h.Agent, ctx.Code, ctx.Stdout, ctx.Stderr)
		}
	}
	done := jigHooked(sub, "review", "done", T, "--result", b.cleanResult(t))
	if done.Code != 1 || !strings.Contains(done.Stdout, "REVIEW_NOT_INDEPENDENT") || !strings.Contains(done.Stdout, "independent: false") {
		t.Fatalf("jig review done after both read the round: exit = %d, want 1, REVIEW_NOT_INDEPENDENT and independent: false\nstdout:\n%s\nstderr:\n%s", done.Code, done.Stdout, done.Stderr)
	}
	if rep := b.report(t, "1"); rep["independent"] != false || rep["verdict"] == "clean" {
		t.Errorf("report.yaml = %v, want a round that is not independent and not clean", rep)
	}
}

// TestUnattendedStopNamesReview: an unattended session that stops with every
// slice green and no round is held back once, with a note that tells it to open
// the gate and have a subagent review (it built the ticket). A session that is
// attended is never held, however the ticket stands, and once a round has been
// opened the note is gone.
func TestUnattendedStopNamesReview(t *testing.T) {
	t.Parallel()
	fx, env := newFixture(t, fixture.Opts{})
	T := fx.Ticket
	builderEnv := env.with("JIG_SESSION=s-builder")
	if r := runJig(t, builderEnv, fx.RepoDir, "validate", T); r.Code != 0 {
		t.Fatalf("jig validate exit = %d\nstdout:\n%s\nstderr:\n%s", r.Code, r.Stdout, r.Stderr)
	}
	if r := runJig(t, builderEnv, fx.RepoDir, "claim", T, "--slice", "a", "--unattended"); r.Code != 0 {
		t.Fatalf("jig claim --unattended exit = %d\nstdout:\n%s\nstderr:\n%s", r.Code, r.Stdout, r.Stderr)
	}
	b := buildTicketOn(t, fx, env, true)

	stop := decodeHook(t, b.builder.Hook("Stop", map[string]any{"stop_hook_active": false}))
	for _, want := range []string{"unattended", "open the gate and review", "`jig gate " + T + "`", "subagent", "claimed slice a", "not this session"} {
		if !strings.Contains(stop.Reason, want) {
			t.Errorf("Stop with every slice green and no round: reason lacks %q:\n%s", want, stop.Reason)
		}
	}
	if stop.Decision != "block" {
		t.Errorf("Stop with every slice green and no round: decision = %q, want block", stop.Decision)
	}
	// Told once for that fact: a session that stops again for it is let go.
	if again := b.builder.Hook("Stop", map[string]any{"stop_hook_active": true}); again.Code != 0 || again.Stdout != "" {
		t.Errorf("Stop again with stop_hook_active = exit %d, %q, want silence", again.Code, again.Stdout)
	}

	// An attended session on the same ticket is never held, and is told nothing.
	attended := newHarness(t, fx, env, "s-attended")
	if res := attended.Hook("Stop", map[string]any{"stop_hook_active": false}); res.Code != 0 || res.Stdout != "" {
		t.Errorf("attended Stop with every slice green and no round = exit %d, %q, want silence: nobody is held back", res.Code, res.Stdout)
	}

	// The round is open: there is no review left to open.
	b.openRound(t)
	if res := b.builder.Hook("Stop", map[string]any{"stop_hook_active": false}); res.Code != 0 || strings.Contains(res.Stdout, "open the gate") {
		t.Errorf("Stop with a round open = exit %d, %q, want no note to open the gate", res.Code, res.Stdout)
	}
}
