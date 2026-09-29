package frontier

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/pool"
	"github.com/develdeco/jig/internal/store"
)

// authorEnv pins the identity of the commits these tests make outside jig.
var authorEnv = []string{
	"GIT_AUTHOR_NAME=author",
	"GIT_AUTHOR_EMAIL=author@example.invalid",
	"GIT_COMMITTER_NAME=author",
	"GIT_COMMITTER_EMAIL=author@example.invalid",
	"GIT_AUTHOR_DATE=2026-01-02T00:00:00Z",
	"GIT_COMMITTER_DATE=2026-01-02T00:00:00Z",
}

// authorCommit plays the author of a branch built outside jig: a fresh clone
// of the fixture's remote, branch checked out (started from main when origin
// has none yet), one commit adding file, pushed. It returns the new tip.
func authorCommit(t *testing.T, fx *fixture.Fixture, branch, file string) string {
	t.Helper()
	dir := t.TempDir()
	runGitT(t, dir, "clone", fx.RepoRemote, ".")
	if runGitT(t, dir, "ls-remote", "--heads", "origin", branch) != "" {
		runGitT(t, dir, "checkout", branch)
	} else {
		runGitT(t, dir, "checkout", "-b", branch)
	}
	if err := os.WriteFile(filepath.Join(dir, file), []byte("the author's work\n"), 0o644); err != nil {
		t.Fatalf("write %s: %v", file, err)
	}
	runGitT(t, dir, "add", "-A")
	if _, err := gitx.RunEnv(dir, authorEnv, "commit", "-m", "author: "+file); err != nil {
		t.Fatalf("commit %s: %v", file, err)
	}
	runGitT(t, dir, "push", "origin", branch)
	return runGitT(t, dir, "rev-parse", "HEAD")
}

// moveMain moves the fixture's target branch on, as other work landing on it
// would.
func moveMain(t *testing.T, fx *fixture.Fixture) string {
	t.Helper()
	dir := t.TempDir()
	runGitT(t, dir, "clone", fx.RepoRemote, ".")
	if err := os.WriteFile(filepath.Join(dir, "MOVED.md"), []byte("main moved on\n"), 0o644); err != nil {
		t.Fatalf("write MOVED.md: %v", err)
	}
	runGitT(t, dir, "add", "-A")
	if _, err := gitx.RunEnv(dir, authorEnv, "commit", "-m", "main moves on"); err != nil {
		t.Fatalf("commit: %v", err)
	}
	runGitT(t, dir, "push", "origin", "main")
	return runGitT(t, dir, "rev-parse", "HEAD")
}

// adopt records what `jig gate --branch` records at adoption: the branch, and
// its tip as the ticket's start sha.
func adopt(t *testing.T, st *store.Store, ticket, branch, tip string) {
	t.Helper()
	if err := st.WriteStartSHA(ticket, "fixture-repo", tip); err != nil {
		t.Fatalf("WriteStartSHA: %v", err)
	}
	if err := st.WriteTicketBranch(ticket, branch); err != nil {
		t.Fatalf("WriteTicketBranch: %v", err)
	}
}

// buildLeaseOf is the build lease directory of fx's ticket.
func buildLeaseOf(t *testing.T, fx *fixture.Fixture) string {
	t.Helper()
	dir, err := pool.Dir(fx.Home, "fixture-repo", fx.Ticket, pool.Build)
	if err != nil {
		t.Fatalf("pool.Dir: %v", err)
	}
	return dir
}

// TestRunBuildsOnAnAdoptedBranchAfterMainMoves: the builds of an adopted
// ticket land on the author's branch, on top of the author's commit, and
// verify against the tip recorded at adoption - which main moving past the
// branch's fork point since does not change. The commits sit on top of the
// tip, so verifyGreen accepts them only because the start sha is the tip, not
// the moved main.
func TestRunBuildsOnAnAdoptedBranchAfterMainMoves(t *testing.T) {
	const branch = "add-retry"
	fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
	d, st := newDeps(t, fx)
	tip := authorCommit(t, fx, branch, "retry.txt")
	moved := moveMain(t, fx)
	adopt(t, st, fx.Ticket, branch, tip)

	report, err := Run(d, RunOpts{Ticket: fx.Ticket})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	for _, want := range []string{"a", "b", "d"} {
		if !containsID(report.Green, want) {
			t.Fatalf("Green = %v, want %s built and verified on the adopted branch (start sha %s, main moved to %s)", report.Green, want, tip, moved)
		}
	}

	lease := buildLeaseOf(t, fx)
	if got := runGitT(t, lease, "symbolic-ref", "--short", "HEAD"); got != branch {
		t.Fatalf("the build lease is on %q, want the adopted %q", got, branch)
	}
	built := strings.Fields(runGitT(t, lease, "rev-list", "--reverse", tip+"..HEAD"))
	if len(built) == 0 {
		t.Fatal("jig built nothing on top of the author's tip")
	}
	if parent := runGitT(t, lease, "rev-parse", built[0]+"^"); parent != tip {
		t.Fatalf("jig's first commit sits on %s, want the author's tip %s", parent, tip)
	}
	if got := runGitT(t, fx.RepoRemote, "rev-parse", "refs/heads/"+branch); got != tip {
		t.Fatalf("origin's %s = %s, want the author's tip %s: a run pushes nothing", branch, got, tip)
	}
}

// TestRunRecordsTheAdoptedBranchTipAsItsStartSHA: a ticket that records a
// branch origin has, with no start sha yet (a record adopted by hand, or one
// whose adoption never wrote it), starts where the lease's branch started,
// pool.Acquire's own rule - origin's copy of the branch - not at the target,
// which a branch built before main moved is not a descendant of.
func TestRunRecordsTheAdoptedBranchTipAsItsStartSHA(t *testing.T) {
	const branch = "add-retry"
	fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
	d, st := newDeps(t, fx)
	tip := authorCommit(t, fx, branch, "retry.txt")
	moved := moveMain(t, fx)
	if err := st.WriteTicketBranch(fx.Ticket, branch); err != nil {
		t.Fatalf("WriteTicketBranch: %v", err)
	}

	report, err := Run(d, RunOpts{Ticket: fx.Ticket})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !containsID(report.Green, "a") {
		t.Fatalf("Green = %v, want slice a green: a start sha off main would fail its verification", report.Green)
	}
	got, err := os.ReadFile(st.StartSHAPath(fx.Ticket, "fixture-repo"))
	if err != nil || strings.TrimSpace(string(got)) != tip {
		t.Fatalf("start sha = %q (err %v), want the branch's tip %s, not main's %s", got, err, tip, moved)
	}
}

// TestRunFastForwardsAnAdoptedBranchTheAuthorPushedTo: the author pushes to the
// branch after jig acquired its lease but before jig built anything on it. The
// next build fast-forwards the lease and builds on the author's new tip.
func TestRunFastForwardsAnAdoptedBranchTheAuthorPushedTo(t *testing.T) {
	const branch = "add-retry"
	fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
	d, st := newDeps(t, fx)
	tip1 := authorCommit(t, fx, branch, "retry.txt")
	adopt(t, st, fx.Ticket, branch, tip1)
	if _, err := pool.Acquire(fx.Home, "fixture-repo", fx.RepoRemote, "main", branch, fx.Ticket, pool.Build); err != nil {
		t.Fatalf("acquire the build lease: %v", err)
	}
	tip2 := authorCommit(t, fx, branch, "backoff.txt")

	report, err := Run(d, RunOpts{Ticket: fx.Ticket})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !containsID(report.Green, "a") {
		t.Fatalf("Green = %v, want slice a built after the author's push", report.Green)
	}
	lease := buildLeaseOf(t, fx)
	if _, err := gitx.Run(lease, "merge-base", "--is-ancestor", tip2, "HEAD"); err != nil {
		t.Fatalf("the author's new tip %s is not under jig's commits: the lease was not fast-forwarded (%v)", tip2, err)
	}
	built := strings.Fields(runGitT(t, lease, "rev-list", "--reverse", tip2+"..HEAD"))
	if len(built) == 0 || runGitT(t, lease, "rev-parse", built[0]+"^") != tip2 {
		t.Fatalf("jig's first commit does not sit on the author's new tip %s (built: %v)", tip2, built)
	}
	// Until jig built, the branch was the author's, so the start sha followed
	// the push: it is the tip jig started from, not the adopted one.
	if got, err := os.ReadFile(st.StartSHAPath(fx.Ticket, "fixture-repo")); err != nil || strings.TrimSpace(string(got)) != tip2 {
		t.Fatalf("start sha = %q (err %v), want the tip jig started from, %s (adopted at %s)", got, err, tip2, tip1)
	}
}

// TestRunRefusesAnAdoptedBranchTheAuthorPushedToOverJigsCommits: the author
// pushes to the branch after jig committed on it, so each side has commits
// the other lacks. The run stops with BRANCH_DIVERGED before it dispatches
// anything else, and the lease keeps jig's commits: nothing is merged, rebased
// or reset on the human's behalf.
func TestRunRefusesAnAdoptedBranchTheAuthorPushedToOverJigsCommits(t *testing.T) {
	const branch = "add-retry"
	fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
	d, st := newDeps(t, fx)
	tip1 := authorCommit(t, fx, branch, "retry.txt")
	adopt(t, st, fx.Ticket, branch, tip1)

	// The first run builds a and b, and parks c on its question.
	if _, err := Run(d, RunOpts{Ticket: fx.Ticket}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	lease := buildLeaseOf(t, fx)
	jigHead := runGitT(t, lease, "rev-parse", "HEAD")
	if jigHead == tip1 {
		t.Fatal("test setup: jig built nothing")
	}
	tip2 := authorCommit(t, fx, branch, "backoff.txt")
	journalBefore, err := journal.Read(st, fx.Ticket)
	if err != nil {
		t.Fatalf("journal.Read: %v", err)
	}

	_, err = Run(d, RunOpts{Ticket: fx.Ticket, AnswerQID: "q-001", AnswerText: "Casual."})
	var ae *axi.Error
	if !errors.As(err, &ae) || ae.Code != "BRANCH_DIVERGED" {
		t.Fatalf("Run over a diverged branch: err = %v, want an *axi.Error BRANCH_DIVERGED", err)
	}
	if got := runGitT(t, lease, "rev-parse", "HEAD"); got != jigHead {
		t.Fatalf("the lease is at %s after the refusal, want jig's own %s untouched", got, jigHead)
	}
	if got := runGitT(t, fx.RepoRemote, "rev-parse", "refs/heads/"+branch); got != tip2 {
		t.Fatalf("origin's %s = %s, want the author's %s untouched", branch, got, tip2)
	}
	journalAfter, err := journal.Read(st, fx.Ticket)
	if err != nil {
		t.Fatalf("journal.Read: %v", err)
	}
	for _, l := range journalAfter[len(journalBefore):] {
		if l.Event == "dispatch" {
			t.Fatalf("a slice was dispatched after the refusal: %+v", l)
		}
	}
}

// TestRunAcceptsAnAdoptedTicketWithNoSlices: an adopted ticket has no slices
// until a gate round queues its fixes, and a run over it has nothing to
// dispatch: it reports nothing green, stalled or parked, and acquires no
// lease.
func TestRunAcceptsAnAdoptedTicketWithNoSlices(t *testing.T) {
	fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
	d, st := newDeps(t, fx)
	const ticket = "JIG-2"
	if err := st.CreateTicketRecord(ticket, store.Ticket{Title: "Add retry"}); err != nil {
		t.Fatalf("CreateTicketRecord: %v", err)
	}
	adopt(t, st, ticket, "add-retry", authorCommit(t, fx, "add-retry", "retry.txt"))

	report, err := Run(d, RunOpts{Ticket: ticket})
	if err != nil {
		t.Fatalf("Run over an adopted ticket with no slices: %v", err)
	}
	if len(report.Green)+len(report.Stalled)+len(report.NeedsInput)+len(report.EnvBlocked) != 0 || report.Stopped || report.PendingQuestion != "" {
		t.Fatalf("report = %+v, want nothing to report", report)
	}
	lease, err := pool.Dir(fx.Home, "fixture-repo", ticket, pool.Build)
	if err != nil {
		t.Fatalf("pool.Dir: %v", err)
	}
	if _, err := os.Stat(lease); !os.IsNotExist(err) {
		t.Fatalf("a run with nothing to build acquired a lease at %s (stat err %v)", lease, err)
	}
}

// authorRewrite is the author rewriting branch: it is reset to main and given
// one commit adding file, force-pushed. The old history is gone from origin's
// branch. It returns the new tip.
func authorRewrite(t *testing.T, fx *fixture.Fixture, branch, file string) string {
	t.Helper()
	dir := t.TempDir()
	runGitT(t, dir, "clone", fx.RepoRemote, ".")
	runGitT(t, dir, "checkout", "-B", branch, "origin/main")
	if err := os.WriteFile(filepath.Join(dir, file), []byte("the rewritten work\n"), 0o644); err != nil {
		t.Fatalf("write %s: %v", file, err)
	}
	runGitT(t, dir, "add", "-A")
	if _, err := gitx.RunEnv(dir, authorEnv, "commit", "-m", "author: "+file); err != nil {
		t.Fatalf("commit %s: %v", file, err)
	}
	runGitT(t, dir, "push", "--force", "origin", branch)
	return runGitT(t, dir, "rev-parse", "HEAD")
}

// dispatched reports whether the journal records any slice dispatch.
func dispatched(t *testing.T, st *store.Store, ticket string) bool {
	t.Helper()
	lines, err := journal.Read(st, ticket)
	if err != nil {
		t.Fatalf("journal.Read: %v", err)
	}
	for _, l := range lines {
		if l.Event == "dispatch" {
			return true
		}
	}
	return false
}

// TestRunRefusesAnAdoptedBranchOriginLost: an adopted branch is on origin by
// definition. When it is gone - deleted since the adoption - the run refuses
// with BRANCH_NOT_FOUND before it builds anything, instead of pool.Acquire
// cutting the branch from the target and building fixes on a branch that lacks
// the author's code.
func TestRunRefusesAnAdoptedBranchOriginLost(t *testing.T) {
	const branch = "add-retry"
	fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
	d, st := newDeps(t, fx)
	tip := authorCommit(t, fx, branch, "retry.txt")
	adopt(t, st, fx.Ticket, branch, tip)
	runGitT(t, fx.RepoRemote, "branch", "-D", branch)

	_, err := Run(d, RunOpts{Ticket: fx.Ticket})
	var ae *axi.Error
	if !errors.As(err, &ae) || ae.Code != "BRANCH_NOT_FOUND" {
		t.Fatalf("Run with the adopted branch gone from origin: err = %v, want an *axi.Error BRANCH_NOT_FOUND", err)
	}
	if dispatched(t, st, fx.Ticket) {
		t.Fatal("a slice was dispatched over a branch origin lost")
	}
	if _, err := gitx.Run(buildLeaseOf(t, fx), "rev-parse", "--verify", "--quiet", "refs/heads/"+branch); err == nil {
		t.Fatalf("the refused run still cut %s in the build lease from the target", branch)
	}
}

// TestRunFollowsAnAdoptedBranchTheAuthorRewroteBeforeJigBuilt: the branch is
// the author's until jig builds on it, so one the author rewrote after the
// adoption is built on as it is when jig starts, and the start sha follows it.
// A start sha fixed at adoption would sit on the old history: every commit a
// build makes must descend from it, so each attempt failed verification and the
// slices stalled, with nothing saying why.
func TestRunFollowsAnAdoptedBranchTheAuthorRewroteBeforeJigBuilt(t *testing.T) {
	const branch = "add-retry"
	fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
	d, st := newDeps(t, fx)
	tip1 := authorCommit(t, fx, branch, "retry.txt")
	adopt(t, st, fx.Ticket, branch, tip1)
	tip2 := authorRewrite(t, fx, branch, "rewritten.txt")

	report, err := Run(d, RunOpts{Ticket: fx.Ticket})
	if err != nil {
		t.Fatalf("Run over a branch rewritten before jig built: %v", err)
	}
	if !containsID(report.Green, "a") || len(report.Stalled) != 0 {
		t.Fatalf("green = %v, stalled = %v, want slice a built on the rewritten branch", report.Green, report.Stalled)
	}
	if got, err := os.ReadFile(st.StartSHAPath(fx.Ticket, "fixture-repo")); err != nil || strings.TrimSpace(string(got)) != tip2 {
		t.Fatalf("start sha = %q (err %v), want the rewritten tip %s, not the adopted %s", got, err, tip2, tip1)
	}
	lease := buildLeaseOf(t, fx)
	if _, err := gitx.Run(lease, "merge-base", "--is-ancestor", tip2, "HEAD"); err != nil {
		t.Fatalf("jig's commits are not on the rewritten tip %s (%v)", tip2, err)
	}
	if _, err := gitx.Run(lease, "merge-base", "--is-ancestor", tip1, "HEAD"); err == nil {
		t.Fatalf("jig built on the old history: %s is under its commits", tip1)
	}
}

// TestRunKeepsTheStartSHAOnceJigBuilt: once the journal records commits jig
// built, the start sha is theirs to descend from, and stays where it was even
// when the branch moves under it - here jig's own commits pushed to origin, as
// the publish refusal says to.
func TestRunKeepsTheStartSHAOnceJigBuilt(t *testing.T) {
	const branch = "add-retry"
	fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
	d, st := newDeps(t, fx)
	tip1 := authorCommit(t, fx, branch, "retry.txt")
	adopt(t, st, fx.Ticket, branch, tip1)

	// The first run builds a and b, and parks c on its question.
	if _, err := Run(d, RunOpts{Ticket: fx.Ticket}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	lease := buildLeaseOf(t, fx)
	runGitT(t, lease, "push", "origin", branch)
	if got := runGitT(t, fx.RepoRemote, "rev-parse", "refs/heads/"+branch); got == tip1 {
		t.Fatal("test setup: pushing jig's commits did not move origin's branch")
	}

	report, err := Run(d, RunOpts{Ticket: fx.Ticket, AnswerQID: "q-001", AnswerText: "Casual."})
	if err != nil {
		t.Fatalf("Run after the answer: %v", err)
	}
	if !containsID(report.Green, "c") {
		t.Fatalf("Green = %v, want slice c built after the answer", report.Green)
	}
	if got, err := os.ReadFile(st.StartSHAPath(fx.Ticket, "fixture-repo")); err != nil || strings.TrimSpace(string(got)) != tip1 {
		t.Fatalf("start sha = %q (err %v), want it kept at %s once jig built", got, err, tip1)
	}
}

// TestRunKeepsAnOrdinaryTicketsStartSHAWhenTheTargetMoves: the start sha follows
// origin only for an adopted branch jig has built nothing on. An ordinary
// ticket's stays where its first dispatch recorded it, however the target moves
// between runs: its later slices' commits sit on jig/<ticket>, cut from the
// target as it was, and descend from the start sha only while it is that
// commit. Here slice c waits on its question while main moves on; if the start
// sha followed origin, c's commit would not descend from it and the slice would
// stall on a green that did not verify.
func TestRunKeepsAnOrdinaryTicketsStartSHAWhenTheTargetMoves(t *testing.T) {
	fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
	d, st := newDeps(t, fx)

	if _, err := Run(d, RunOpts{Ticket: fx.Ticket}); err != nil {
		t.Fatalf("first Run: %v", err)
	}
	before, err := os.ReadFile(st.StartSHAPath(fx.Ticket, "fixture-repo"))
	if err != nil {
		t.Fatalf("read the start sha: %v", err)
	}
	moved := moveMain(t, fx)
	if strings.TrimSpace(string(before)) == moved {
		t.Fatal("test setup: main did not move")
	}

	report, err := Run(d, RunOpts{Ticket: fx.Ticket, AnswerQID: "q-001", AnswerText: "Casual."})
	if err != nil {
		t.Fatalf("Run after the answer: %v", err)
	}
	if len(report.Green) != 4 || len(report.Stalled) != 0 {
		t.Fatalf("Green = %v, Stalled = %v after main moved, want all four slices green: c's commit descends from the start sha", report.Green, report.Stalled)
	}
	after, err := os.ReadFile(st.StartSHAPath(fx.Ticket, "fixture-repo"))
	if err != nil || string(after) != string(before) {
		t.Fatalf("start sha = %q (err %v), want it kept at %q: main moved to %s", after, err, before, moved)
	}
}

// builtOnAnotherMachine plays a first machine that built a and b on the adopted
// branch and parked c on its question: it runs the ticket against its own jig
// home. The commits stay in its build lease, whose directory it returns beside
// the Deps that machine used.
func builtOnAnotherMachine(t *testing.T, fx *fixture.Fixture, d Deps, branch string) (machineA Deps, leaseA string) {
	t.Helper()
	machineA = d
	machineA.Home = t.TempDir()
	if _, err := Run(machineA, RunOpts{Ticket: fx.Ticket}); err != nil {
		t.Fatalf("Run on the machine that builds: %v", err)
	}
	leaseA, err := pool.Dir(machineA.Home, "fixture-repo", fx.Ticket, pool.Build)
	if err != nil {
		t.Fatalf("pool.Dir: %v", err)
	}
	if head := runGitT(t, leaseA, "rev-parse", "HEAD"); head == runGitT(t, fx.RepoRemote, "rev-parse", "refs/heads/"+branch) {
		t.Fatal("test setup: the machine that built has nothing origin lacks")
	}
	return machineA, leaseA
}

// wantAxiCode fails unless err is an *axi.Error with the given code, and
// returns it.
func wantAxiCode(t *testing.T, err error, code string) *axi.Error {
	t.Helper()
	var ae *axi.Error
	if !errors.As(err, &ae) || ae.Code != code {
		t.Fatalf("err = %v, want an *axi.Error %s", err, code)
	}
	return ae
}

// TestRunRefusesABuildLeaseThatLacksWhatJigBuilt: the journal records commits
// jig built on the adopted branch, on another machine, and they are still in
// that machine's build lease. This machine's build lease is cut afresh from
// origin, which lacks them, so a build here would put the next commits on a
// branch that leaves the earlier ones out. The run refuses with
// BUILD_LEASE_MISSING before it dispatches anything, and no attempt is spent.
// The way forward the help gives - push them from the machine that built them -
// works: this machine's lease then follows origin's copy, which holds them.
func TestRunRefusesABuildLeaseThatLacksWhatJigBuilt(t *testing.T) {
	const branch = "add-retry"
	fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
	d, st := newDeps(t, fx)
	tip := authorCommit(t, fx, branch, "retry.txt")
	adopt(t, st, fx.Ticket, branch, tip)
	_, leaseA := builtOnAnotherMachine(t, fx, d, branch)
	built := strings.Fields(runGitT(t, leaseA, "rev-list", "--reverse", tip+"..HEAD"))
	before, err := journal.Read(st, fx.Ticket)
	if err != nil {
		t.Fatalf("journal.Read: %v", err)
	}

	_, err = Run(d, RunOpts{Ticket: fx.Ticket, AnswerQID: "q-001", AnswerText: "Casual."})
	ae := wantAxiCode(t, err, "BUILD_LEASE_MISSING")
	if len(ae.Help) == 0 || !strings.Contains(ae.Msg, branch) || !strings.Contains(ae.Msg, built[0][:7]) {
		t.Fatalf("BUILD_LEASE_MISSING = %+v, want it to name the branch and the missing commits, and carry help", ae)
	}
	after, err := journal.Read(st, fx.Ticket)
	if err != nil {
		t.Fatalf("journal.Read: %v", err)
	}
	for _, l := range after[len(before):] {
		if l.Event == "dispatch" {
			t.Fatalf("a slice was dispatched after the refusal: %+v", l)
		}
	}
	if state, err := st.ReadSliceState(fx.Ticket, "c"); err != nil || state.Attempts != 1 {
		t.Fatalf("slice c = %+v (err %v), want the one attempt the first machine spent and none more", state, err)
	}

	// The help: push them from the machine that built them.
	runGitT(t, leaseA, "push", "origin", branch)
	report, err := Run(d, RunOpts{Ticket: fx.Ticket})
	if err != nil {
		t.Fatalf("Run after the commits were pushed: %v", err)
	}
	if !containsID(report.Green, "c") {
		t.Fatalf("Green = %v, want slice c built once this machine's lease follows origin", report.Green)
	}
	lease := buildLeaseOf(t, fx)
	for _, commit := range built {
		if _, err := gitx.Run(lease, "merge-base", "--is-ancestor", commit, "HEAD"); err != nil {
			t.Fatalf("the build lease does not hold jig's commit %s: %v", commit, err)
		}
	}
}

// TestRunFollowsTheHelpForABranchRewrittenUnderJigsCommits: the author rewrote
// the branch after jig built on it. Each machine is refused, and following the
// help gets the run going again - none of it asks for anything that cannot
// work:
//
//   - on the machine that built, whose lease holds jig's commits, the two
//     histories have diverged: BRANCH_DIVERGED, and merging origin's branch in
//     the lease, as the help says, keeps the old tip reachable, so jig's
//     commits and the start sha they descend from stay valid;
//   - on another machine, whose lease was cut from the rewritten origin, jig's
//     commits are missing: BUILD_LEASE_MISSING, until the first machine has
//     merged and pushed.
//
// A rebase of jig's commits onto the rewritten branch is not among the
// remedies: it changes the commits the journal recorded and leaves the start
// sha where it was.
func TestRunFollowsTheHelpForABranchRewrittenUnderJigsCommits(t *testing.T) {
	const branch = "add-retry"
	setup := func(t *testing.T) (fx *fixture.Fixture, d Deps, st *store.Store, machineA Deps, leaseA, tip1, tip2 string) {
		fx = fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
		d, st = newDeps(t, fx)
		tip1 = authorCommit(t, fx, branch, "retry.txt")
		adopt(t, st, fx.Ticket, branch, tip1)
		machineA, leaseA = builtOnAnotherMachine(t, fx, d, branch)
		tip2 = authorRewrite(t, fx, branch, "rewritten.txt")
		return
	}
	integrate := func(t *testing.T, leaseA string) {
		t.Helper()
		runGitT(t, leaseA, "fetch", "origin")
		if _, err := gitx.RunEnv(leaseA, authorEnv, "merge", "--no-edit", "origin/"+branch); err != nil {
			t.Fatalf("merge origin/%s in the lease that built: %v", branch, err)
		}
	}

	t.Run("on the machine that built", func(t *testing.T) {
		fx, _, st, machineA, leaseA, tip1, tip2 := setup(t)
		_, err := Run(machineA, RunOpts{Ticket: fx.Ticket, AnswerQID: "q-001", AnswerText: "Casual."})
		ae := wantAxiCode(t, err, "BRANCH_DIVERGED")
		if help := strings.Join(ae.Help, "\n"); !strings.Contains(help, "fetch origin && git -C "+leaseA+" merge origin/"+branch) {
			t.Fatalf("BRANCH_DIVERGED help %q does not give the fetch and merge", help)
		}

		integrate(t, leaseA)
		report, err := Run(machineA, RunOpts{Ticket: fx.Ticket})
		if err != nil {
			t.Fatalf("Run after integrating in the lease: %v", err)
		}
		if !containsID(report.Green, "c") {
			t.Fatalf("Green = %v, want slice c built after the merge", report.Green)
		}
		if _, err := gitx.Run(leaseA, "merge-base", "--is-ancestor", tip2, "HEAD"); err != nil {
			t.Fatalf("the lease does not hold the rewritten tip %s: %v", tip2, err)
		}
		if got, err := os.ReadFile(st.StartSHAPath(fx.Ticket, "fixture-repo")); err != nil || strings.TrimSpace(string(got)) != tip1 {
			t.Fatalf("start sha = %q (err %v), want it kept at %s: jig's commits descend from it", got, err, tip1)
		}
	})

	t.Run("on another machine", func(t *testing.T) {
		fx, d, _, _, leaseA, _, tip2 := setup(t)
		_, err := Run(d, RunOpts{Ticket: fx.Ticket, AnswerQID: "q-001", AnswerText: "Casual."})
		ae := wantAxiCode(t, err, "BUILD_LEASE_MISSING")
		if len(ae.Help) == 0 {
			t.Fatalf("BUILD_LEASE_MISSING carries no help")
		}

		// The first machine integrates and pushes, as the help says.
		integrate(t, leaseA)
		runGitT(t, leaseA, "push", "origin", branch)
		report, err := Run(d, RunOpts{Ticket: fx.Ticket})
		if err != nil {
			t.Fatalf("Run after the first machine merged and pushed: %v", err)
		}
		if !containsID(report.Green, "c") {
			t.Fatalf("Green = %v, want slice c built once the lease follows origin", report.Green)
		}
		if _, err := gitx.Run(buildLeaseOf(t, fx), "merge-base", "--is-ancestor", tip2, "HEAD"); err != nil {
			t.Fatalf("this machine's lease does not hold the rewritten tip %s: %v", tip2, err)
		}
	})
}

// leaseCommitT commits one file in a build lease, as an attempt that left
// work behind would, and returns the commit. The journal does not record it.
func leaseCommitT(t *testing.T, dir, file string) string {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, file), []byte("an attempt's leftovers\n"), 0o644); err != nil {
		t.Fatalf("write %s: %v", file, err)
	}
	runGitT(t, dir, "add", "-A")
	if _, err := gitx.RunEnv(dir, authorEnv, "commit", "-m", "attempt: "+file); err != nil {
		t.Fatalf("commit %s: %v", file, err)
	}
	return runGitT(t, dir, "rev-parse", "HEAD")
}

// TestRunFollowsAnAdoptedBranchTheAuthorRewroteAfterAParkedFirstRun: the first
// run on an adopted branch parked its slice on a question and built nothing, so
// the branch is still the author's. The author rewrites it while the question
// waits. The lease from that first run holds the old tip, which the rewritten
// origin has replaced: the two have diverged, but not one of the lease's
// commits is jig's, so the run answers, follows the rewritten branch and builds
// on it. Refusing it as diverged would send the human to merge the author's
// discarded history back into the rewritten one.
func TestRunFollowsAnAdoptedBranchTheAuthorRewroteAfterAParkedFirstRun(t *testing.T) {
	const branch = "add-retry"
	fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
	d, st := newDeps(t, fx)
	tip1 := authorCommit(t, fx, branch, "retry.txt")
	adopt(t, st, fx.Ticket, branch, tip1)
	// a, b and d are done and c is the only slice left: its question is the
	// first thing the run parks, before any commit of jig's exists.
	for _, id := range []string{"a", "b", "d"} {
		if err := st.WriteSliceState(fx.Ticket, id, store.SliceState{State: "green", Attempts: 1}); err != nil {
			t.Fatalf("WriteSliceState(%s): %v", id, err)
		}
	}
	first, err := Run(d, RunOpts{Ticket: fx.Ticket})
	if err != nil || len(first.NeedsInput) != 1 {
		t.Fatalf("first Run = %+v (err %v), want slice c parked on its question", first, err)
	}
	lines, err := journal.Read(st, fx.Ticket)
	if err != nil || len(journal.BuiltCommits(lines)) != 0 {
		t.Fatalf("journal built = %v (err %v), want nothing built yet", journal.BuiltCommits(lines), err)
	}
	tip2 := authorRewrite(t, fx, branch, "rewritten.txt")

	report, err := Run(d, RunOpts{Ticket: fx.Ticket, AnswerQID: "q-001", AnswerText: "Casual."})
	if err != nil {
		t.Fatalf("Run after the rewrite: %v", err)
	}
	if !containsID(report.Green, "c") {
		t.Fatalf("Green = %v, want slice c built on the rewritten branch", report.Green)
	}
	lease := buildLeaseOf(t, fx)
	if _, err := gitx.Run(lease, "merge-base", "--is-ancestor", tip2, "HEAD"); err != nil {
		t.Fatalf("jig's commit is not on the rewritten tip %s (%v)", tip2, err)
	}
	if _, err := gitx.Run(lease, "merge-base", "--is-ancestor", tip1, "HEAD"); err == nil {
		t.Fatalf("the lease kept the author's discarded history: %s is under jig's commit", tip1)
	}
	if got, err := os.ReadFile(st.StartSHAPath(fx.Ticket, "fixture-repo")); err != nil || strings.TrimSpace(string(got)) != tip2 {
		t.Fatalf("start sha = %q (err %v), want the rewritten tip %s", got, err, tip2)
	}
}

// TestRunKeepsWhatAnEarlierAttemptLeftInTheLease: a lease may hold commits an
// earlier attempt made that never verified. While the branch has not moved
// they are kept, as always: the retry builds on them and the attempt log tells
// it what was wrong. Only when the author's branch has diverged from the lease
// is the lease re-cut from origin's, since a copy holding none of jig's
// verified commits has nothing to protect and the branch is still the author's.
func TestRunKeepsWhatAnEarlierAttemptLeftInTheLease(t *testing.T) {
	const branch = "add-retry"
	for _, tc := range []struct {
		name string
		// moveOrigin changes origin's branch after the lease holds the
		// leftover, and returns the tip jig must build on.
		moveOrigin func(t *testing.T, fx *fixture.Fixture, tip string) string
		keptLeft   bool
	}{
		{"origin has not moved", func(t *testing.T, fx *fixture.Fixture, tip string) string { return tip }, true},
		{"the author pushed", func(t *testing.T, fx *fixture.Fixture, tip string) string {
			return authorCommit(t, fx, branch, "backoff.txt")
		}, false},
		{"the author rewrote", func(t *testing.T, fx *fixture.Fixture, tip string) string {
			return authorRewrite(t, fx, branch, "rewritten.txt")
		}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
			d, st := newDeps(t, fx)
			tip := authorCommit(t, fx, branch, "retry.txt")
			adopt(t, st, fx.Ticket, branch, tip)
			lease, err := pool.Acquire(fx.Home, "fixture-repo", fx.RepoRemote, "main", branch, fx.Ticket, pool.Build)
			if err != nil {
				t.Fatalf("acquire the build lease: %v", err)
			}
			leftover := leaseCommitT(t, lease.Dir, "leftover.txt")
			want := tc.moveOrigin(t, fx, tip)

			report, err := Run(d, RunOpts{Ticket: fx.Ticket})
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			if !containsID(report.Green, "a") {
				t.Fatalf("Green = %v, want slice a built", report.Green)
			}
			_, kept := gitx.Run(lease.Dir, "merge-base", "--is-ancestor", leftover, "HEAD")
			if (kept == nil) != tc.keptLeft {
				t.Fatalf("the attempt's leftover commit kept = %v, want %v", kept == nil, tc.keptLeft)
			}
			if _, err := gitx.Run(lease.Dir, "merge-base", "--is-ancestor", want, "HEAD"); err != nil {
				t.Fatalf("jig's commits are not on the branch's tip %s (%v)", want, err)
			}
		})
	}
}
