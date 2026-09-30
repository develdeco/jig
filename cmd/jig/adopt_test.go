package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/gitx"
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
// intake. Publish gets past the precondition to its own checks (it has no
// gate round to publish yet), instead of the "no slices" message that would
// send its owner to write a brief.
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
	if code == 0 || strings.Contains(out, "no slices yet") || !strings.Contains(out, "no gate rounds recorded for "+ticket) {
		t.Errorf("jig publish on an adopted ticket: exit %d, want publish's own refusal (no gate round yet), not a no-slices one:\n%s", code, out)
	}
	out, code = runMain(t, "", withStore("requeue", ticket, "--from-brief-diff")...)
	if code == 0 || !strings.Contains(out, "adopted branch add-retry") || !strings.Contains(out, "`jig gate JIG-2`") || strings.Contains(out, "intake") {
		t.Errorf("jig requeue on an adopted ticket: exit %d, want it to say the ticket adopted a branch and point at the gate:\n%s", code, out)
	}
}

// TestStatusOfAnAdoptedTicket: an adopted ticket names its branch, is not
// "building" for having no slices, and its hint follows its gate rounds - the
// gate first, the next round after an unclean one, `jig publish` after a clean
// one, as for any ticket. A ticket that adopted nothing keeps the intake hint.
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

	// After a clean round the ticket is published like any other: publish
	// ships an adopted branch, so the hint names it.
	writeRoundReport(t, fx.StoreDir, ticket, 2, "clean")
	if got := status(ticket); !strings.HasSuffix(got, "help[1]:\n  Run `jig publish JIG-2` to open or update the PR\n") {
		t.Errorf("status after a clean round does not point at `jig publish`:\n%s", got)
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

// TestSolveOfAnAdoptedTicketPublishesIt: `jig solve` on a ticket that adopted a
// branch runs the whole loop - the fix the first round queued is built on the
// branch, the next round is clean - and publishes it: the branch on origin is
// fast-forwarded, the author's commit keeping its sha and jig's fix and the
// memorize commit on top, and the report says the branch was pushed as it was.
func TestSolveOfAnAdoptedTicketPublishesIt(t *testing.T) {
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
		t.Fatalf("jig solve on an adopted ticket: exit %d, want 0 - it built the fix, reached a clean round and published\n%s", code, out)
	}
	head, err := gitx.Run(fx.RepoRemote, "rev-parse", "refs/heads/"+branch)
	if err != nil || head == tip {
		t.Fatalf("origin's %s = %q (err %v), want it advanced past the author's %s by the publish", branch, head, err, tip)
	}
	want := "pushed[1]{repo,head,squash}:\n  fixture-repo," + head + "," + verifydeliver.NotSquashed + "\n"
	if !strings.Contains(out, want) {
		t.Errorf("jig solve's output lacks the pushed table %q:\n%s", want, out)
	}

	// The author's commit is under jig's fix, and the memorize commit is on
	// top: fast-forwarded, nothing rewritten.
	if _, err := gitx.Run(fx.RepoRemote, "merge-base", "--is-ancestor", tip, head); err != nil {
		t.Fatalf("the author's %s is not under origin's %s: %v", tip, head, err)
	}
	subjects, err := gitx.Run(fx.RepoRemote, "log", "--format=%s", tip+".."+head)
	if err != nil {
		t.Fatalf("git log: %v", err)
	}
	lines := strings.Split(subjects, "\n")
	if len(lines) != 2 || !strings.HasPrefix(lines[1], ticket+" fix-1: ") || lines[0] != "docs: memorize "+ticket {
		t.Errorf("publish added %q on top of the author's commit, want the memorize commit over jig's fix-1", lines)
	}
	if got, err := gitx.Run(fx.RepoRemote, "for-each-ref", "--format=%(refname)", "refs/heads/jig/"); err != nil || got != "" {
		t.Errorf("origin has jig/ branches %q (err %v), want none: an adopted ticket ships its own branch", got, err)
	}
}
