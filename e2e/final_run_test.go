package e2e

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/journal"
)

// TestAGateRoundAndPublishRunNoTestAndTheFinalRunIsWitnessed: the builder
// verifies each slice in its own worktree, a gate round opens and runs nothing
// (it writes no gate-oracle line) and the review context lists the passes the
// builder's runs left; after the clean round the builder runs `jig verify
// --final` at the reviewed head, and publish's report and body name it. A
// second ticket nobody ran a final run for is published all the same, and its
// report says none.
func TestAGateRoundAndPublishRunNoTestAndTheFinalRunIsWitnessed(t *testing.T) {
	t.Parallel()
	b := newBuiltTicket(t, true)
	env, _ := githubClone(t, b)
	b.env = env
	b.builder.env = env

	gate := b.openRound(t)
	if !strings.Contains(gate.Stdout, "head: "+b.sha) {
		t.Fatalf("jig gate output lacks the reviewed head %s:\n%s", b.sha, gate.Stdout)
	}
	for _, l := range readJournal(t, b.fx, b.fx.Ticket) {
		if l.Event == "gate-oracle" {
			t.Errorf("the gate round wrote %+v: it runs no command", l)
		}
	}
	ctx := b.builder.Jig("review", "context", b.fx.Ticket)
	for _, want := range []string{"oracles_passed[4]{slice,workspace,command,commit,at_head,seconds}:"} {
		if ctx.Code != 0 || !strings.Contains(ctx.Stdout, want) {
			t.Fatalf("jig review context exit = %d, want the slices' passes (%q)\nstdout:\n%s\nstderr:\n%s", ctx.Code, want, ctx.Stdout, ctx.Stderr)
		}
	}
	b.reviewedClean(t, 1)

	// Nothing has a final run yet: publish says so, and refuses nothing.
	none := runJig(t, env, b.builder.Clone, "publish", b.fx.Ticket, "--body")
	if none.Code != 0 || !strings.Contains(none.Stdout, "none at the reviewed head "+b.sha[:12]) || !strings.Contains(none.Stdout, "jig verify "+b.fx.Ticket+" --final") {
		t.Fatalf("jig publish --body before a final run exit = %d, want 0 and a report of none with the line to run\nstdout:\n%s\nstderr:\n%s", none.Code, none.Stdout, none.Stderr)
	}

	// The builder's last run, at the reviewed head.
	verifySlots <- struct{}{}
	final := b.builder.Jig("verify", b.fx.Ticket, "--final")
	<-verifySlots
	if final.Code != 0 || !strings.Contains(final.Stdout, "outcome: pass") || !strings.Contains(final.Stdout, "covers: yes") || !strings.Contains(final.Stdout, "recorded: final-oracle") {
		t.Fatalf("jig verify --final exit = %d, want a covering pass\nstdout:\n%s\nstderr:\n%s", final.Code, final.Stdout, final.Stderr)
	}
	run, ok := journal.FinalRunAt(readJournal(t, b.fx, b.fx.Ticket), "", b.sha)
	if !ok || run.Outcome != "pass" || len(run.Slices) != 4 {
		t.Fatalf("FinalRunAt = %+v, %v, want a pass of the four slices' union at %s", run, ok, b.sha)
	}

	pub := runJig(t, env, b.builder.Clone, "publish", b.fx.Ticket, "--body")
	if pub.Code != 0 || !strings.Contains(pub.Stdout, "final_run[1]{repo,outcome,seconds,command,run}:") || !strings.Contains(pub.Stdout, "fixture-repo,pass,") {
		t.Fatalf("jig publish --body after the final run exit = %d, want it named in the report\nstdout:\n%s\nstderr:\n%s", pub.Code, pub.Stdout, pub.Stderr)
	}
	text := string(readFileOrFatal(t, filepath.Join(b.st.TicketDir(b.fx.Ticket), "pr", "fixture-repo.md")))
	if want := "Final run at the reviewed head " + b.sha[:7] + ": pass in "; !strings.Contains(text, want) || strings.Contains(text, "Oracles green") {
		t.Errorf("the pull request body lacks %q or still lists oracles:\n%s", want, text)
	}

	// A ticket nobody made a final run for is published, and the report says so.
	other := newBuiltTicket(t, true)
	otherEnv, _ := githubClone(t, other)
	other.env = otherEnv
	other.builder.env = otherEnv
	other.openRound(t)
	other.reviewedClean(t, 1)
	second := runJig(t, otherEnv, other.builder.Clone, "publish", other.fx.Ticket, "--body")
	if second.Code != 0 || !strings.Contains(second.Stdout, "none at the reviewed head") {
		t.Errorf("jig publish --body of a ticket with no final run exit = %d, want 0 and none\nstdout:\n%s\nstderr:\n%s", second.Code, second.Stdout, second.Stderr)
	}
}
