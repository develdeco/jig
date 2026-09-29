package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/pool"
	"github.com/develdeco/jig/internal/store"
	"github.com/develdeco/jig/internal/verifydeliver"
)

// writeRoundReport records one gate round's report.yaml with verdict, the file
// `jig status` reads to say what the ticket's next step is.
func writeRoundReport(t *testing.T, storeDir, ticket string, round int, verdict string) {
	t.Helper()
	dir := filepath.Join(storeDir, ticket, "gate", "round-"+string(rune('0'+round)))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir round dir: %v", err)
	}
	body := "round: " + string(rune('0'+round)) + "\nverdict: " + verdict + "\n"
	if err := os.WriteFile(filepath.Join(dir, "report.yaml"), []byte(body), 0o644); err != nil {
		t.Fatalf("write report.yaml: %v", err)
	}
}

// TestAdoptedTicketIsWorkedWithoutSlices: a ticket with no slices is refused
// by every command that needs work, with both ways to get some (a brief and
// slices, or a branch adopted through the gate); one that adopted a branch
// passes for gate, run, solve and publish - its gate round queues the slices
// the rest of the loop builds - and status and the hints stop pointing it at
// intake. Publish still refuses it, by name, instead of the "no slices"
// message that would send its owner to write a brief.
func TestAdoptedTicketIsWorkedWithoutSlices(t *testing.T) {
	t.Setenv("JIG_HOME", t.TempDir())
	fx := fixture.Generate(t, fixture.Opts{})
	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	const ticket = "JIG-2"
	if err := st.CreateTicketRecord(ticket, store.Ticket{Title: "Add retry"}); err != nil {
		t.Fatalf("CreateTicketRecord: %v", err)
	}
	withStore := func(args ...string) []string { return append(args, "--store", fx.StoreDir) }

	// Before adoption.
	for _, args := range [][]string{
		{"run", ticket, "--backend", "fake", "--scenario", fx.ScenarioDir},
		{"gate", ticket, "--scenario", fx.ScenarioDir},
		{"solve", ticket, "--yes", "--backend", "fake", "--scenario", fx.ScenarioDir},
		{"publish", ticket, "--yes"},
	} {
		out, code := runMain(t, "", withStore(args...)...)
		for _, want := range []string{"ticket JIG-2 has no slices yet", "intake skill", "`jig gate JIG-2 --branch <name>`"} {
			if code == 0 || !strings.Contains(out, want) {
				t.Errorf("jig %s: exit %d, want a refusal saying %q:\n%s", strings.Join(args, " "), code, want, out)
			}
		}
	}
	out, code := runMain(t, "", withStore("requeue", ticket, "--from-brief-diff")...)
	if code == 0 || !strings.Contains(out, "intake skill") || strings.Contains(out, "--branch") {
		t.Errorf("jig requeue before adoption: exit %d, want the intake refusal alone (requeue has nothing to adopt):\n%s", code, out)
	}
	out, code = runMain(t, "", withStore("status", ticket)...)
	if code != 0 || !strings.Contains(out, "intake skill") || strings.Contains(out, "branch:") {
		t.Errorf("jig status before adoption: exit %d, want the intake hint and no branch line:\n%s", code, out)
	}

	if err := st.WriteTicketBranch(ticket, "add-retry"); err != nil {
		t.Fatalf("WriteTicketBranch: %v", err)
	}

	// run has nothing to dispatch and says what the next step is.
	out, code = runMain(t, "", withStore("run", ticket, "--backend", "fake", "--scenario", fx.ScenarioDir)...)
	if code != 0 || !strings.Contains(out, "Run `jig gate JIG-2` to open a gate round") {
		t.Errorf("jig run on an adopted ticket: exit %d, want it to accept the ticket and point at the gate:\n%s", code, out)
	}
	// gate and solve get past the precondition into the gate itself, which
	// here fails on the branch not being on origin.
	for _, args := range [][]string{
		{"gate", ticket, "--scenario", fx.ScenarioDir},
		{"solve", ticket, "--yes", "--backend", "fake", "--scenario", fx.ScenarioDir},
	} {
		out, code := runMain(t, "", withStore(args...)...)
		if code == 0 || strings.Contains(out, "no slices yet") || !strings.Contains(out, "code: BRANCH_NOT_FOUND") {
			t.Errorf("jig %s: exit %d, want the gate's own BRANCH_NOT_FOUND, not a no-slices refusal:\n%s", strings.Join(args, " "), code, out)
		}
	}
	out, code = runMain(t, "", withStore("publish", ticket, "--yes")...)
	if code == 0 || !strings.Contains(out, "code: PUBLISH_ADOPTED_BRANCH") || strings.Contains(out, "no slices yet") {
		t.Errorf("jig publish on an adopted ticket: exit %d, want PUBLISH_ADOPTED_BRANCH:\n%s", code, out)
	}
	out, code = runMain(t, "", withStore("requeue", ticket, "--from-brief-diff")...)
	if code == 0 || !strings.Contains(out, "adopted branch add-retry") || !strings.Contains(out, "`jig gate JIG-2`") || strings.Contains(out, "intake") {
		t.Errorf("jig requeue on an adopted ticket: exit %d, want it to say the ticket adopted a branch and point at the gate:\n%s", code, out)
	}
}

// TestStatusOfAnAdoptedTicket: an adopted ticket names its branch, is not
// "building" for having no slices, and its hint follows its gate rounds - the
// gate first, the next round after an unclean one - and never names `jig
// publish` after a clean one, which refuses an adopted branch. A ticket that
// adopted nothing keeps the intake hint.
func TestStatusOfAnAdoptedTicket(t *testing.T) {
	t.Setenv("JIG_HOME", t.TempDir())
	fx := fixture.Generate(t, fixture.Opts{})
	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	const ticket = "JIG-2"
	if err := st.CreateTicketRecord(ticket, store.Ticket{Title: "Add retry"}); err != nil {
		t.Fatalf("CreateTicketRecord: %v", err)
	}
	if err := st.CreateTicketRecord("JIG-3", store.Ticket{Title: "Nothing adopted"}); err != nil {
		t.Fatalf("CreateTicketRecord: %v", err)
	}
	if err := st.WriteTicketBranch(ticket, "add-retry"); err != nil {
		t.Fatalf("WriteTicketBranch: %v", err)
	}
	status := func(ticket string) string {
		t.Helper()
		out, err := RenderStatus(st, ticket)
		if err != nil {
			t.Fatalf("RenderStatus(%s): %v", ticket, err)
		}
		return out
	}

	got := status(ticket)
	if !strings.HasPrefix(got, "ticket: JIG-2\nbranch: add-retry\nstate: green\n") {
		t.Errorf("status of an adopted ticket does not name its branch under the ticket, in a green state:\n%s", got)
	}
	if !strings.HasSuffix(got, "help[1]:\n  Run `jig gate JIG-2` to open a gate round\n") {
		t.Errorf("status of an adopted ticket with no rounds does not point at the gate:\n%s", got)
	}

	writeRoundReport(t, fx.StoreDir, ticket, 1, "fix-slices")
	if got := status(ticket); !strings.HasSuffix(got, "help[1]:\n  Run `jig gate JIG-2` to open the next gate round\n") {
		t.Errorf("status after an unclean round does not point at the next round:\n%s", got)
	}

	// After a clean round jig built nothing on the branch: the pull request is
	// the human's to open, and there is nothing of jig's to push first.
	writeRoundReport(t, fx.StoreDir, ticket, 2, "clean")
	got = status(ticket)
	if strings.Contains(got, "jig publish JIG-2") {
		t.Errorf("status names `jig publish` for an adopted ticket, which publish refuses:\n%s", got)
	}
	if want := "  Round 2 is clean; jig publish does not ship an adopted branch yet: open the pull request for add-retry yourself\n"; !strings.HasSuffix(got, want) {
		t.Errorf("status after a clean round does not say publishing an adopted branch is not built:\n%s", got)
	}

	// A builder's claim that did not verify put no commit on the branch: it
	// leaves the hint as it was.
	const commit = "1111111111111111111111111111111111111111"
	if err := journal.Append(st, ticket, journal.Line{Slice: "fix-1", Event: "result", Outcome: "green", Commit: commit, Attempt: 1}); err != nil {
		t.Fatalf("journal.Append: %v", err)
	}
	if got := status(ticket); !strings.HasSuffix(got, "yet: open the pull request for add-retry yourself\n") {
		t.Errorf("status after a claim that never verified does not keep the plain hint:\n%s", got)
	}

	// Once jig built commits on the branch they wait in the build lease, so
	// origin's copy lacks what the clean round reviewed: the hint says to push
	// them first, in the words the publish refusal uses.
	if err := journal.Append(st, ticket, journal.Line{Slice: "fix-1", Event: "verified", Commit: commit, Attempt: 1}); err != nil {
		t.Fatalf("journal.Append: %v", err)
	}
	got = status(ticket)
	steps := verifydeliver.PublishByHand("add-retry", true, "")
	if want := "  Round 2 is clean; jig publish does not ship an adopted branch yet: " + steps + "\n"; !strings.HasSuffix(got, want) {
		t.Errorf("status after a clean round, with jig's commits built, does not say to push them first (%q):\n%s", steps, got)
	}
	if !strings.Contains(steps, "build lease") {
		t.Errorf("PublishByHand for built commits = %q, want it to name the build lease they wait in", steps)
	}

	// The fix for a branch that moved meanwhile is in the sentence itself: a
	// push over a branch that moved is not a fast-forward. The row must fit
	// the 160-column terminal the branch demo records in
	// (demo/adopt-branch.tape), with the demo's branch name.
	if !strings.Contains(steps, "if it moved") {
		t.Errorf("PublishByHand for built commits = %q, want it to say to merge the branch in when it moved", steps)
	}
	row := "  Round 2 is clean; jig publish does not ship an adopted branch yet: " + verifydeliver.PublishByHand("add-percent", true, "")
	if len(row) > 160 {
		t.Errorf("the hint row is %d columns wide, want at most the demo's 160:\n%s", len(row), row)
	}

	// A ticket that adopted nothing has both ways to get work, as `jig ticket
	// new` and the refusal of `jig run` name them.
	got = status("JIG-3")
	wantHelp := "help[2]:\n  " + intakeHint("JIG-3") + "\n  " + adoptHint("JIG-3") + "\n"
	if strings.Contains(got, "branch:") || !strings.HasSuffix(got, wantHelp) {
		t.Errorf("status of a ticket that adopted nothing does not end in the intake hint and the way to adopt a branch, or gained a branch line:\n%s", got)
	}
}

// pushAuthorBranch builds branch outside jig, as its author would: a clone of
// the fixture's remote, one commit off main - the scenario's slice a patch,
// which fixes Clamp so the branch passes the fixture repo's oracles - pushed
// under branch. It returns the pushed tip.
func pushAuthorBranch(t *testing.T, fx *fixture.Fixture, branch string) string {
	t.Helper()
	dir := t.TempDir()
	for _, args := range [][]string{
		{"clone", fx.RepoRemote, "."},
		{"checkout", "-b", branch},
		{"apply", filepath.Join(fx.ScenarioDir, "slices", "a", "attempt-1", "patch.diff")},
		{"add", "-A"},
		{"commit", "-m", "author: fix Clamp"},
		{"push", "origin", branch},
	} {
		if _, err := gitx.Run(dir, args...); err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
	}
	tip, err := gitx.Run(dir, "rev-parse", "HEAD")
	if err != nil {
		t.Fatalf("rev-parse HEAD: %v", err)
	}
	return tip
}

// TestSolveOfAnAdoptedTicketStopsAtTheCleanRound: `jig solve` on a ticket that
// adopted a branch runs the loop - the fix the first round queued is built on
// the branch, the next round is clean - and stops there with the gate report
// and its hint, exit 0. Publishing an adopted branch is not built, and the
// loop's work is done: solve must not fall through to a publish that always
// refuses, which would end every successful solve in an error and print none of
// the round.
func TestSolveOfAnAdoptedTicketStopsAtTheCleanRound(t *testing.T) {
	jigHome := t.TempDir()
	t.Setenv("JIG_HOME", jigHome)
	fx := fixture.Generate(t, fixture.Opts{})
	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	const ticket, branch = "JIG-2", "add-retry"
	if err := st.CreateTicketRecord(ticket, store.Ticket{Title: "Add retry"}); err != nil {
		t.Fatalf("CreateTicketRecord: %v", err)
	}
	tip := pushAuthorBranch(t, fx, branch)
	withStore := func(args ...string) []string { return append(args, "--store", fx.StoreDir) }

	// Round 1 adopts the branch and queues fix-1 (the scripted round).
	out, code := runMain(t, "", withStore("gate", ticket, "--branch", branch, "--scenario", fx.ScenarioDir)...)
	if code != 0 || !strings.Contains(out, "verdict: fix-slices") {
		t.Fatalf("jig gate --branch: exit %d, want a round that queued a fix\n%s", code, out)
	}

	out, code = runMain(t, "", withStore("solve", ticket, "--yes", "--backend", "fake", "--scenario", fx.ScenarioDir)...)
	if code != 0 {
		t.Fatalf("jig solve on an adopted ticket: exit %d, want 0 - it built the fix and reached a clean round\n%s", code, out)
	}
	if strings.Contains(out, "PUBLISH_ADOPTED_BRANCH") {
		t.Errorf("jig solve fell through to publish, which refuses an adopted branch:\n%s", out)
	}
	for _, want := range []string{"branch: " + branch, "round: 2", "verdict: clean", "push the build lease to " + branch + " (merge it in if it moved)"} {
		if !strings.Contains(out, want) {
			t.Errorf("jig solve's output lacks %q:\n%s", want, out)
		}
	}

	lease, err := pool.Dir(jigHome, "fixture-repo", ticket, pool.Build)
	if err != nil {
		t.Fatalf("pool.Dir: %v", err)
	}
	if subject, err := gitx.Run(lease, "log", "-1", "--format=%s"); err != nil || !strings.HasPrefix(subject, ticket+" fix-1: ") {
		t.Fatalf("the build lease's tip is %q (err %v), want the fix-1 commit solve built", subject, err)
	}
	if got, err := gitx.Run(fx.RepoRemote, "rev-parse", "refs/heads/"+branch); err != nil || got != tip {
		t.Fatalf("origin's %s = %q (err %v), want the author's %s: solve pushes nothing", branch, got, err, tip)
	}
}
