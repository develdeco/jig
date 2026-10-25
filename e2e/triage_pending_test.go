package e2e

// End-to-end tests of a triage decision between two gate rounds: a reviewer in
// a session of its own closes round 1 with an ask, `jig triage` decides it, and
// until the next round closes and applies the decision, `jig status` and the
// next round's request show the ask as decided, not as plainly open.

import (
	"encoding/json"
	"strings"
	"testing"
)

// askedRound is a built ticket whose round 1 a reviewer in a session of its own
// closed with one ask, r1-f1, that nobody has decided. It returns the ticket and
// the reviewer's harness.
func askedRound(t *testing.T) (builtTicket, *fakeHarness) {
	t.Helper()
	b := newBuiltTicket(t, true)
	b.openRound(t)
	reviewer := newHarness(t, b.fx, b.env, "s-reviewer")
	if res := reviewer.Jig("review", "context", b.fx.Ticket); res.Code != 0 {
		t.Fatalf("jig review context exit = %d\nstdout:\n%s\nstderr:\n%s", res.Code, res.Stdout, res.Stderr)
	}
	reviewer.Bash("git log --oneline -3")
	done := reviewer.Jig("review", "done", b.fx.Ticket, "--result", reviewResult(t, b.st, b.fx.Ticket, 1, true))
	if !strings.Contains(done.Stdout, "round: 1") || !strings.Contains(done.Stdout, "r1-f1") {
		t.Fatalf("jig review done (round 1) exit = %d, want the round recorded with the ask r1-f1\nstdout:\n%s\nstderr:\n%s", done.Code, done.Stdout, done.Stderr)
	}
	return b, reviewer
}

// lineStarting is the first line of out that starts with prefix, "" when none
// does.
func lineStarting(out, prefix string) string {
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, prefix) {
			return line
		}
	}
	return ""
}

// TestStatusShowsPendingTriage: after `jig triage T r1-f1 dismiss`, `jig status`
// lists the ask as dismissed, by whom, and says the gate applies it; `jig next`
// no longer waits on it. The ask stays listed through the round that opens next
// and goes when that round closes.
func TestStatusShowsPendingTriage(t *testing.T) {
	t.Parallel()
	b, reviewer := askedRound(t)
	T := b.fx.Ticket
	status := func() string {
		t.Helper()
		res := b.builder.Jig("status", T)
		if res.Code != 0 {
			t.Fatalf("jig status exit = %d\nstdout:\n%s\nstderr:\n%s", res.Code, res.Stdout, res.Stderr)
		}
		return res.Stdout
	}

	// Nobody has decided the ask: the row says nothing, the help counts it, and
	// the ticket's phase waits on it.
	out := status()
	if !strings.Contains(out, "outstanding_asks[1]{id,risk,file:line,title,why,decision,decided_by}:") {
		t.Fatalf("status lacks the asks table with its decision columns:\n%s", out)
	}
	if row := lineStarting(out, "  r1-f1,"); !strings.HasSuffix(row, ",,,") {
		t.Errorf("the undecided ask's row = %q, want no why, no decision and no decider", row)
	}
	if !strings.Contains(out, "to decide the 1 undecided ask, then `jig gate "+T+"` to apply the decisions") {
		t.Errorf("status help does not count the undecided ask:\n%s", out)
	}
	if p := nextJSON[phaseOut](t, b.env, b.fx.RepoDir, T).Phase; p.Ready || p.Code != "PHASE_ASKS_UNDECIDED" {
		t.Errorf("before the decision: phase ready=%v code=%q, want PHASE_ASKS_UNDECIDED", p.Ready, p.Code)
	}

	// The decision is a journal line, made here by an agent (a stdin that is no
	// terminal): the ask is still outstanding, now with its decision.
	if res := b.builder.Jig("triage", T, "r1-f1", "dismiss", "--reason", "the greeting is meant to be casual"); res.Code != 0 {
		t.Fatalf("jig triage exit = %d\nstdout:\n%s\nstderr:\n%s", res.Code, res.Stdout, res.Stderr)
	}
	out = status()
	if row := lineStarting(out, "  r1-f1,"); !strings.HasSuffix(row, ",,dismiss,agent") {
		t.Errorf("the decided ask's row = %q, want it to end in the dismissal and its decider\n%s", row, out)
	}
	wantHelp := "Run `jig gate " + T + "` to apply the decisions: the 1 decided ask is applied when the next round closes"
	if !strings.Contains(out, wantHelp) {
		t.Errorf("status help does not count the ask as decided (want %q):\n%s", wantHelp, out)
	}
	if p := nextJSON[phaseOut](t, b.env, b.fx.RepoDir, T).Phase; !p.Ready || p.Code != "" {
		t.Errorf("after the decision: phase ready=%v code=%q, want ready", p.Ready, p.Code)
	}

	// The next round opens: the decision is still pending until it closes.
	b.openRound(t)
	if row := lineStarting(status(), "  r1-f1,"); !strings.HasSuffix(row, ",,dismiss,agent") {
		t.Errorf("with round 2 open, the ask's row = %q, want the decision still shown", row)
	}

	// The reviewer reports nothing new; the round closes, applies the dismissal,
	// and the ask is gone.
	if res := reviewer.Jig("review", "context", T); res.Code != 0 {
		t.Fatalf("jig review context (round 2) exit = %d\nstdout:\n%s\nstderr:\n%s", res.Code, res.Stdout, res.Stderr)
	}
	done := reviewer.Jig("review", "done", T, "--result", reviewResult(t, b.st, T, 2, false))
	if done.Code != 0 || !strings.Contains(done.Stdout, "verdict: clean") {
		t.Fatalf("jig review done (round 2) exit = %d, want 0 and a clean verdict\nstdout:\n%s\nstderr:\n%s", done.Code, done.Stdout, done.Stderr)
	}
	if out = status(); strings.Contains(out, "outstanding_asks") || strings.Contains(out, "r1-f1") {
		t.Errorf("status still lists the ask after the round applied the decision:\n%s", out)
	}
}

// TestReviewContextShowsDecision: the next round's request holds the ask it
// still carries with the decision `jig triage` recorded for it, so the reviewer
// does not raise it again; the ask stays in the open list, the dismissed list is
// still empty, since only the round that closes moves it.
func TestReviewContextShowsDecision(t *testing.T) {
	t.Parallel()
	b, reviewer := askedRound(t)
	T := b.fx.Ticket
	const reason = "the greeting is meant to be casual"
	if res := b.builder.Jig("triage", T, "r1-f1", "dismiss", "--reason", reason); res.Code != 0 {
		t.Fatalf("jig triage exit = %d\nstdout:\n%s\nstderr:\n%s", res.Code, res.Stdout, res.Stderr)
	}
	b.openRound(t)

	ctx := reviewer.Jig("review", "context", T)
	if ctx.Code != 0 {
		t.Fatalf("jig review context exit = %d\nstdout:\n%s\nstderr:\n%s", ctx.Code, ctx.Stdout, ctx.Stderr)
	}
	if !strings.Contains(ctx.Stdout, "open[1]{id,file:line,action,risk,title,decision}:") {
		t.Fatalf("the round's open list lacks the ask:\n%s", ctx.Stdout)
	}
	if row := lineStarting(ctx.Stdout, "  r1-f1,"); !strings.HasSuffix(row, `,"decided: dismiss, by an agent: `+reason+`"`) {
		t.Errorf("the ask's open row = %q, want it to end in the decision, who made it and why", row)
	}
	if !strings.Contains(ctx.Stdout, "dismissed[0]") {
		t.Errorf("the decided ask was moved to the dismissed list before the round closed:\n%s", ctx.Stdout)
	}

	// The same in the request itself, which the reviewer may read as JSON.
	var req struct {
		Open []struct {
			ID             string `json:"id"`
			Decision       string `json:"decision"`
			DecidedBy      string `json:"decided_by"`
			DecisionReason string `json:"decision_reason"`
		} `json:"open"`
		Dismissed []struct {
			ID string `json:"id"`
		} `json:"dismissed"`
	}
	asJSON := reviewer.Jig("review", "context", T, "--json")
	if err := json.Unmarshal([]byte(asJSON.Stdout), &req); err != nil {
		t.Fatalf("parse review context --json: %v\n%s", err, asJSON.Stdout)
	}
	if len(req.Open) != 1 || req.Open[0].ID != "r1-f1" || req.Open[0].Decision != "dismiss" || req.Open[0].DecidedBy != "agent" || req.Open[0].DecisionReason != reason {
		t.Errorf("request open = %+v, want r1-f1 dismissed by an agent with the reason", req.Open)
	}
	if len(req.Dismissed) != 0 {
		t.Errorf("request dismissed = %+v, want none until the round closes", req.Dismissed)
	}
	if stored := string(readFileOrFatal(t, b.workFile("gate.round-2.review.json"))); !strings.Contains(stored, `"decision": "dismiss"`) {
		t.Errorf("the stored review.json does not carry the decision:\n%s", stored)
	}
}
