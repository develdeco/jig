package verifydeliver

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/pool"
	"github.com/develdeco/jig/internal/store"
)

// gateToClean drives the fixture's build and gate rounds to a clean
// verdict: round 1 (fix-1 found), fix-1 played green, round 2 (clean).
func gateToClean(t *testing.T, fx *fixture.Fixture, d Deps) {
	t.Helper()
	driveBuild(t, fx, "rung-a")
	src := NewFakeGateSource(fx.ScenarioDir)
	report, err := Gate(d, src, GateOpts{Ticket: fx.Ticket})
	if err != nil {
		t.Fatalf("Gate round 1: %v", err)
	}
	if report.Verdict != "fix-slices" {
		t.Fatalf("round 1 verdict = %q, want fix-slices", report.Verdict)
	}
	driveFix1(t, fx, "rung-a")
	report, err = Gate(d, src, GateOpts{Ticket: fx.Ticket})
	if err != nil {
		t.Fatalf("Gate round 2: %v", err)
	}
	if report.Verdict != "clean" {
		t.Fatalf("round 2 verdict = %q, want clean", report.Verdict)
	}
}

// advanceTarget commits a new file directly to the fixture repo's bare
// remote main branch, through a throwaway clone, so the target moves out
// from under an already-clean gate.
func advanceTarget(t *testing.T, fx *fixture.Fixture) {
	t.Helper()
	clone := t.TempDir()
	if _, err := gitx.Run(filepath.Dir(clone), "clone", fx.RepoRemote, clone); err != nil {
		t.Fatalf("clone remote: %v", err)
	}
	if _, err := gitx.Run(clone, "config", "user.name", "jig-fixture"); err != nil {
		t.Fatalf("config user.name: %v", err)
	}
	if _, err := gitx.Run(clone, "config", "user.email", "fixture@example.invalid"); err != nil {
		t.Fatalf("config user.email: %v", err)
	}
	if err := os.WriteFile(filepath.Join(clone, "UPSTREAM.md"), []byte("upstream moved on\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	if _, err := gitx.Run(clone, "add", "-A"); err != nil {
		t.Fatalf("add: %v", err)
	}
	if _, err := gitx.RunEnv(clone, buildGitEnv, "commit", "-m", "upstream: unrelated change"); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if _, err := gitx.Run(clone, "push", "origin", "main"); err != nil {
		t.Fatalf("push: %v", err)
	}
}

// wantFailureCommit asserts that d.Store's working copy is clean, pushed to
// origin/branch (local HEAD matches the remote tip), and that its latest
// commit subject is exactly wantSubject - the shape every post-journal
// Publish failure must leave the store in, whichever step raised it. This
// must fail on the unfixed code: without Publish's deferred push (or with
// journaled set too late) the working copy stays dirty, or the pushed tip
// lags the local commit, and the porcelain or HEAD check below fails first.
func wantFailureCommit(t *testing.T, d Deps, branch, wantSubject string) {
	t.Helper()
	status, err := gitx.Run(d.Store.Root, "status", "--porcelain")
	if err != nil {
		t.Fatalf("store status: %v", err)
	}
	if status != "" {
		t.Fatalf("store working copy is dirty after the failed publish:\n%s", status)
	}
	local := run(t, d.Store.Root, "rev-parse", "HEAD")
	remote := run(t, d.Store.Root, "ls-remote", "origin", "refs/heads/"+branch)
	fields := strings.Fields(remote)
	if len(fields) == 0 {
		t.Fatalf("ls-remote origin refs/heads/%s returned nothing", branch)
	}
	if local != fields[0] {
		t.Fatalf("store local HEAD %s != pushed remote tip %s for %s", local, fields[0], branch)
	}
	subject := run(t, d.Store.Root, "log", "-1", "--format=%s")
	if subject != wantSubject {
		t.Fatalf("store failure commit subject = %q, want %q", subject, wantSubject)
	}
}

// TestPublishRefusesARecordedBranchEqualToTheTarget: a recorded branch
// Store.TicketBranch refuses (here the target itself, where a push would land
// without the PR) fails publish with the refusal's own code, before it pushes
// anything. Origin's main and the ticket's default branch, which the command
// must not fall back to, keep their shas. The ticket is gated first, on the
// branch it had then: the gate helper resolves the branch too, and would
// refuse it.
func TestPublishRefusesARecordedBranchEqualToTheTarget(t *testing.T) {
	t.Parallel()

	fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
	d := newDeps(t, fx)
	gateToClean(t, fx, d)
	recordBranch(t, d.Store, fx.Ticket, "main")
	if _, err := d.Store.TicketBranch(fx.Ticket, "main"); err == nil {
		t.Fatal("test setup: a recorded branch equal to the target must be refused")
	}
	defaultBranch := "refs/heads/" + ticketBranch(fx.Ticket)
	mainBefore := originRef(t, fx.RepoRemote, "refs/heads/main")
	defaultBefore := originRef(t, fx.RepoRemote, defaultBranch)

	_, err := Publish(d, PublishOpts{Ticket: fx.Ticket, Yes: true})
	wantTicketBranchInvalid(t, err)

	if got := originRef(t, fx.RepoRemote, "refs/heads/main"); got != mainBefore {
		t.Fatalf("origin main = %q after the refused publish, want %q unchanged", got, mainBefore)
	}
	if got := originRef(t, fx.RepoRemote, defaultBranch); got != defaultBefore {
		t.Fatalf("origin %s = %q after the refused publish, want %q unchanged", defaultBranch, got, defaultBefore)
	}
}

// TestPublishRefusesAnUnreadableTicketRecordBeforeWritingAnything covers a
// ticket.yaml publish cannot read (here one a newer jig wrote): publish reads
// the record once, up front, so it fails with the refusal's own code before
// its first store write - no memorize journal line - and pushes nothing,
// instead of publishing the ticket under the default branch and title as if
// the record were not there.
func TestPublishRefusesAnUnreadableTicketRecordBeforeWritingAnything(t *testing.T) {
	t.Parallel()

	fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
	d := newDeps(t, fx)
	gateToClean(t, fx, d)
	if err := os.WriteFile(d.Store.TicketFilePath(fx.Ticket), []byte("schema_version: 2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := d.Store.Push(fx.Ticket + ": a newer jig's record"); err != nil {
		t.Fatalf("push the unreadable record: %v", err)
	}
	if _, err := d.Store.ReadTicket(fx.Ticket); err == nil {
		t.Fatal("test setup: a newer schema_version must be unreadable")
	}
	defaultBranch := "refs/heads/" + ticketBranch(fx.Ticket)
	mainBefore := originRef(t, fx.RepoRemote, "refs/heads/main")
	defaultBefore := originRef(t, fx.RepoRemote, defaultBranch)

	_, err := Publish(d, PublishOpts{Ticket: fx.Ticket, Yes: true})
	var ae *axi.Error
	if !errors.As(err, &ae) || ae.Code != "TICKET_SCHEMA_UNSUPPORTED" {
		t.Fatalf("Publish over a newer-schema ticket.yaml: err = %v, want an *axi.Error TICKET_SCHEMA_UNSUPPORTED", err)
	}

	lines, jerr := journal.Read(d.Store, fx.Ticket)
	if jerr != nil {
		t.Fatalf("journal.Read: %v", jerr)
	}
	for _, l := range lines {
		if l.Event == "memorize" {
			t.Fatalf("journal has a %q line after the refused publish: it wrote to the store before reading the record", l.Event)
		}
	}
	if got := originRef(t, fx.RepoRemote, "refs/heads/main"); got != mainBefore {
		t.Fatalf("origin main = %q after the refused publish, want %q unchanged", got, mainBefore)
	}
	if got := originRef(t, fx.RepoRemote, defaultBranch); got != defaultBefore {
		t.Fatalf("origin %s = %q after the refused publish, want %q unchanged", defaultBranch, got, defaultBefore)
	}
}

// TestPublishConfirmWiring checks that Publish threads an honest confirm value into guardedPush: a
// declined interactive prompt must stop before any push is attempted, and
// both --yes and an accepted prompt must pass confirmed=true - never a
// hardcoded literal, and never proceeding past a decline. It also checks that
// the prompt and the push both name the ticket's own branch. Its
// declined_prompt_pushes_store_not_branch and push_error_axi_code_only
// subtests also each call wantFailureCommit, pinning the deferred
// best-effort push these reproduce: the store must end up clean, pushed,
// and carrying "<ticket>: publish failed: <code>" - the error's own axi
// code only, never its message - rather than left for a later command's
// own Sync to sweep up anonymously. failureCode's own branches (a plain
// error, an axi.Error with no code, one wrapped by fmt.Errorf) are pinned
// directly by TestFailureCode instead of paying for another full fixture
// and gate rounds here.
func TestPublishConfirmWiring(t *testing.T) {
	t.Parallel()

	// newPublishableFixture generates a gated ticket.
	newPublishableFixture := func(t *testing.T) (*fixture.Fixture, Deps) {
		t.Helper()
		fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
		d := newDeps(t, fx)
		gateToClean(t, fx, d)
		return fx, d
	}

	t.Run("declined_prompt_pushes_store_not_branch", func(t *testing.T) {
		t.Parallel()
		fx, d := newPublishableFixture(t)
		called := false
		d.GuardedPush = func(string, string, string, bool) error {
			called = true
			return nil
		}
		d.Confirm = func(string, string, string, bool) bool { return false }

		_, err := Publish(d, PublishOpts{Ticket: fx.Ticket, Yes: false})
		var ae *axi.Error
		if !errors.As(err, &ae) || ae.Code != "PUBLISH_DECLINED" {
			t.Fatalf("err = %v, want *axi.Error PUBLISH_DECLINED", err)
		}
		if called {
			t.Fatal("guardedPush was called despite a declined confirm")
		}

		// No leftover tracked change from the journal lines or documents
		// Publish wrote before the decline: the store is already clean,
		// pushed, and the commit names the decline - only the ticket
		// branch itself (guardedPush, stubbed above) is never pushed.
		wantFailureCommit(t, d, "main", fx.Ticket+": publish failed: PUBLISH_DECLINED")
	})

	t.Run("push_error_axi_code_only", func(t *testing.T) {
		t.Parallel()
		fx, d := newPublishableFixture(t)
		d.GuardedPush = func(string, string, string, bool) error {
			return &axi.Error{Msg: "push rejected: /host/secret/abs/path", Code: "PUBLISH_PUSH_REJECTED"}
		}

		if _, err := Publish(d, PublishOpts{Ticket: fx.Ticket, Yes: true}); err == nil {
			t.Fatal("Publish: want an error from the stubbed guardedPush")
		}
		wantFailureCommit(t, d, "main", fx.Ticket+": publish failed: PUBLISH_PUSH_REJECTED")
	})

	t.Run("yes_flag_threads_confirmed_true", func(t *testing.T) {
		t.Parallel()
		fx, d := newPublishableFixture(t)
		var gotConfirmed bool
		d.GuardedPush = func(_, _, _ string, confirmed bool) error {
			gotConfirmed = confirmed
			return nil
		}
		if _, err := Publish(d, PublishOpts{Ticket: fx.Ticket, Yes: true}); err != nil {
			t.Fatalf("Publish: %v", err)
		}
		if !gotConfirmed {
			t.Fatal("guardedPush confirmed = false, want true when --yes is set")
		}
	})

	t.Run("accepted_prompt_threads_confirmed_true", func(t *testing.T) {
		t.Parallel()
		fx, d := newPublishableFixture(t)
		var gotConfirmed bool
		var pushed, prompted, promptedTicket string
		d.GuardedPush = func(_, _, branch string, confirmed bool) error {
			gotConfirmed = confirmed
			pushed = branch
			return nil
		}
		d.Confirm = func(branch, ticket, _ string, _ bool) bool {
			prompted, promptedTicket = branch, ticket
			return true
		}

		if _, err := Publish(d, PublishOpts{Ticket: fx.Ticket, Yes: false}); err != nil {
			t.Fatalf("Publish: %v", err)
		}
		if !gotConfirmed {
			t.Fatal("guardedPush confirmed = false, want true after an accepted prompt")
		}
		if want := ticketBranch(fx.Ticket); prompted != want || pushed != want {
			t.Fatalf("prompt named branch %q and push named %q, want both to name the ticket's own branch %q", prompted, pushed, want)
		}
		if promptedTicket != fx.Ticket {
			t.Fatalf("prompt named ticket %q, want %q", promptedTicket, fx.Ticket)
		}
	})
}

// TestPublishRefusesWhenLatestGateNotClean checks Publish's precondition:
// even when every slice (including a round's fix slice) is green, an
// unrebutted fix-slices verdict from the latest gate round must refuse
// publish rather than ship work whose review was never re-confirmed clean.
func TestPublishRefusesWhenLatestGateNotClean(t *testing.T) {
	t.Parallel()

	fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
	d := newDeps(t, fx)
	driveBuild(t, fx, "rung-a")

	src := NewFakeGateSource(fx.ScenarioDir)
	report, err := Gate(d, src, GateOpts{Ticket: fx.Ticket})
	if err != nil {
		t.Fatalf("Gate round 1: %v", err)
	}
	if report.Verdict != "fix-slices" {
		t.Fatalf("round 1 verdict = %q, want fix-slices", report.Verdict)
	}
	// fix-1 goes green, but round 2 (which would re-review it) never runs:
	// the latest recorded gate verdict is still "fix-slices".
	driveFix1(t, fx, "rung-a")

	_, err = Publish(d, PublishOpts{Ticket: fx.Ticket, Yes: true})
	var ae *axi.Error
	if !errors.As(err, &ae) || ae.Code != "PUBLISH_NOT_CLEAN" {
		t.Fatalf("err = %v, want *axi.Error PUBLISH_NOT_CLEAN", err)
	}
}

// TestCheckNonEmptyRangeRefusesEmptyRange checks that a ticket branch with
// no commits beyond origin/target (merge-base == HEAD) is refused with
// NOTHING_TO_PUBLISH rather than reaching squash's own bare git error.
func TestCheckNonEmptyRangeRefusesEmptyRange(t *testing.T) {
	t.Parallel()

	_, remote := tinyRepo(t)
	clone := cloneFrom(t, remote)
	run(t, clone, "checkout", "-b", "jig/T-1")
	run(t, clone, "fetch", "origin")

	err := checkNonEmptyRange(clone, "main", "HEAD")
	var ae *axi.Error
	if !errors.As(err, &ae) || ae.Code != "NOTHING_TO_PUBLISH" {
		t.Fatalf("err = %v, want *axi.Error NOTHING_TO_PUBLISH", err)
	}
}

// TestCheckNonEmptyRangeAllowsRealCommits is the non-empty-range control:
// a branch with a real commit beyond the target must not be refused.
func TestCheckNonEmptyRangeAllowsRealCommits(t *testing.T) {
	t.Parallel()

	_, remote := tinyRepo(t)
	clone := cloneFrom(t, remote)
	run(t, clone, "checkout", "-b", "jig/T-1")
	writeAndCommit(t, clone, "g.txt", "branch work", "branch work")
	run(t, clone, "fetch", "origin")

	if err := checkNonEmptyRange(clone, "main", "HEAD"); err != nil {
		t.Fatalf("checkNonEmptyRange: %v", err)
	}
}

// publishLeaseDir returns the fixture ticket's publish lease directory.
func publishLeaseDir(t *testing.T, fx *fixture.Fixture) string {
	t.Helper()
	dir, err := pool.Dir(fx.Home, "fixture-repo", fx.Ticket, pool.Publish)
	if err != nil {
		t.Fatalf("resolve publish lease: %v", err)
	}
	return dir
}

// TestPublishFullChain: a clean ticket publishes its reviewed head as it is.
// The target has moved on since the gate, and publish still reconciles
// nothing, squashes nothing and commits nothing: origin's branch is exactly
// the build lease's, commit for commit, the journal holds no reconcile,
// revalidate, memorize or squash line, and no retrieval notes were added to
// the branch. What a publish leaves in the store - changelogs, the ledger
// entry, the contract index, the evidence - is written once the branch is out.
func TestPublishFullChain(t *testing.T) {
	t.Parallel()

	fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
	d := newDeps(t, fx)
	gateToClean(t, fx, d)
	buildHead := run(t, buildLeaseDir(t, fx), "rev-parse", "HEAD")
	buildLog := run(t, buildLeaseDir(t, fx), "log", "--format=%H", "jig/"+fx.Ticket)
	advanceTarget(t, fx)

	report, err := Publish(d, PublishOpts{Ticket: fx.Ticket, Yes: true})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if report.Tier != "none" {
		t.Fatalf("Tier = %q, want none: publish revalidates nothing", report.Tier)
	}
	if head := report.Head["fixture-repo"]; head != buildHead {
		t.Fatalf("Head[fixture-repo] = %q, want the reviewed head %s", head, buildHead)
	}
	prPath, ok := report.PRBody["fixture-repo"]
	if !ok || prPath == "" {
		t.Fatalf("PRBody[fixture-repo] missing, got %v", report.PRBody)
	}
	// The fixture repo's remote is a plain local path: repohost.New finds no
	// pull-request host there, so PRURL is empty and PRNote carries why.
	if url := report.PRURL["fixture-repo"]; url != "" {
		t.Fatalf("PRURL[fixture-repo] = %q, want empty: the fixture's remote has no pull-request host", url)
	}
	if note := report.PRNote["fixture-repo"]; note == "" {
		t.Fatalf("PRNote[fixture-repo] empty, want a reason the PR URL is empty")
	}

	lines, err := journal.Read(d.Store, fx.Ticket)
	if err != nil {
		t.Fatalf("journal.Read: %v", err)
	}
	seen := map[string]journal.Line{}
	for _, l := range lines {
		seen[l.Event] = l
	}
	for _, event := range []string{"pr", "changelog", "publish-done"} {
		if _, ok := seen[event]; !ok {
			t.Fatalf("journal missing event %q", event)
		}
	}
	for _, event := range []string{"reconcile", "revalidate", "memorize", "squash"} {
		if l, ok := seen[event]; ok {
			t.Fatalf("journal has %+v, but publish reconciles, revalidates, memorizes and squashes nothing", l)
		}
	}
	if pr := seen["pr"]; pr.Outcome != "none:no-host" || pr.Commit != buildHead || pr.Branch != "jig/"+fx.Ticket {
		t.Fatalf("pr line = %+v, want none:no-host for %s from jig/%s", pr, buildHead, fx.Ticket)
	}

	// The reviewed head went out as it is, on top of nothing: origin's history
	// of the branch is the build lease's, with no memorize or squash commit.
	if remoteHead := originRef(t, fx.RepoRemote, "refs/heads/jig/"+fx.Ticket); remoteHead != buildHead {
		t.Fatalf("origin jig/%s = %s, want the reviewed head %s", fx.Ticket, remoteHead, buildHead)
	}
	if got := run(t, fx.RepoRemote, "log", "--format=%H", "jig/"+fx.Ticket); got != buildLog {
		t.Fatalf("origin's history of jig/%s is\n%s\nwant the build lease's\n%s", fx.Ticket, got, buildLog)
	}
	if _, err := gitx.Run(publishLeaseDir(t, fx), "cat-file", "-e", buildHead+":.claude/retrieval/"+fx.Ticket+".md"); err == nil {
		t.Fatal("the reviewed head holds retrieval notes: publish committed on the branch")
	}

	ticketDir := d.Store.TicketDir(fx.Ticket)
	for _, f := range []string{
		filepath.Join("changelog", "alpha.md"),
		filepath.Join("changelog", "beta.md"),
		filepath.Join("changelog", "consolidated.md"),
		filepath.Join("pr", "fixture-repo.md"),
		filepath.Join("pr", "evidence.md"),
	} {
		if _, err := os.Stat(filepath.Join(ticketDir, f)); err != nil {
			t.Fatalf("missing store file %s: %v", f, err)
		}
	}

	ledger, err := os.ReadFile(filepath.Join(d.Store.Root, "ledger.md"))
	if err != nil {
		t.Fatalf("read ledger.md: %v", err)
	}
	if !strings.Contains(string(ledger), "## "+fx.Ticket) {
		t.Fatalf("ledger.md missing ## %s entry:\n%s", fx.Ticket, ledger)
	}
	if !strings.Contains(string(ledger), "**Answers:**") {
		t.Fatalf("ledger.md missing Answers tail:\n%s", ledger)
	}

	index, err := os.ReadFile(filepath.Join(d.Store.Root, "platform", "contract-index.md"))
	if err != nil {
		t.Fatalf("read contract-index.md: %v", err)
	}
	if !strings.Contains(string(index), "- "+fx.Ticket+":") {
		t.Fatalf("contract-index.md missing entry:\n%s", index)
	}

	// The store's remote advanced with the publish commits.
	storeRemoteHead, err := gitx.Run(fx.StoreRemote, "rev-parse", "main")
	if err != nil {
		t.Fatalf("resolve store remote main: %v", err)
	}
	storeHead, err := gitx.Run(fx.StoreDir, "rev-parse", "main")
	if err != nil {
		t.Fatalf("resolve store head: %v", err)
	}
	if storeRemoteHead != storeHead {
		t.Fatalf("store remote main = %s, want it to match local %s", storeRemoteHead, storeHead)
	}
}

// TestPublishTitlesThePullRequestWithTheRecordedTitle covers the title
// fallback through publish itself (consolidatedTitle is only unit-tested with
// the title handed to it): when no slice has a goal, the pull request opened
// through the host carries the title recorded in ticket.yaml, not the bare
// ticket id.
func TestPublishTitlesThePullRequestWithTheRecordedTitle(t *testing.T) {
	t.Parallel()

	fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
	d := newDeps(t, fx)
	logFile, _ := useGithubHost(t, &d, "", "")
	gateToClean(t, fx, d)

	sls, err := d.Store.ReadSlices(fx.Ticket)
	if err != nil {
		t.Fatalf("ReadSlices: %v", err)
	}
	for i := range sls {
		sls[i].Goal = ""
	}
	data, err := yaml.Marshal(store.SliceFile{Slices: sls})
	if err != nil {
		t.Fatalf("marshal slices: %v", err)
	}
	if err := os.WriteFile(filepath.Join(d.Store.TicketDir(fx.Ticket), "slices.yaml"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := d.Store.CreateTicketRecord(fx.Ticket, store.Ticket{Title: "Recorded title"}); err != nil {
		t.Fatalf("CreateTicketRecord: %v", err)
	}
	if err := d.Store.Push(fx.Ticket + ": no slice goals, a recorded title"); err != nil {
		t.Fatalf("push the recorded title: %v", err)
	}

	if _, err := Publish(d, PublishOpts{Ticket: fx.Ticket, Yes: true}); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	var create []string
	for _, argv := range loggedGh(t, logFile) {
		if len(argv) >= 2 && argv[0] == "pr" && argv[1] == "create" {
			create = argv
		}
	}
	titleAt := slices.Index(create, "--title")
	if titleAt < 0 || titleAt+1 >= len(create) || create[titleAt+1] != "Recorded title" {
		t.Fatalf("gh pr create = %v, want --title \"Recorded title\"", create)
	}
}

// TestPublishDoesNotRevalidateAfterTheTargetMoves: publish rebases and merges
// nothing, so a target that moved since the gate changes nothing it does: the
// oracles are not run again, no revalidate line is journaled, and the report's
// tier is none whether the target moved or not.
func TestPublishDoesNotRevalidateAfterTheTargetMoves(t *testing.T) {
	t.Parallel()

	fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
	d := newDeps(t, fx)
	gateToClean(t, fx, d)
	rec := &oracleRecorder{}
	d.Oracle = rec.run
	advanceTarget(t, fx)

	report, err := Publish(d, PublishOpts{Ticket: fx.Ticket, Yes: true})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if report.Tier != "none" {
		t.Fatalf("Tier = %q, want none", report.Tier)
	}
	if ran := rec.ran(); len(ran) != 0 {
		t.Fatalf("publish ran oracles %v, want none", ran)
	}
	lines, err := journal.Read(d.Store, fx.Ticket)
	if err != nil {
		t.Fatalf("journal.Read: %v", err)
	}
	for _, l := range lines {
		if l.Event == "revalidate" {
			t.Fatalf("journal has %+v", l)
		}
	}
}

// TestPublishNoDivergenceLeavesTheStoreUntouched checks that
// PUBLISH_NO_DIVERGENCE - a branch that changes nothing against the target -
// is raised before publish writes anything to the store: a refusal leaves no
// trace, so the store is clean, pushed, and its latest commit is still the
// gate's, not a failure commit.
func TestPublishNoDivergenceLeavesTheStoreUntouched(t *testing.T) {
	t.Parallel()

	fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
	d := newDeps(t, fx)
	gateToClean(t, fx, d)
	// Fast-forward origin's target branch to exactly the build lease's
	// ticket-branch tip: the branch then differs from the target by nothing.
	run(t, buildLeaseDir(t, fx), "push", "origin", "jig/"+fx.Ticket+":main")
	subjectBefore := run(t, d.Store.Root, "log", "-1", "--format=%s")

	_, err := Publish(d, PublishOpts{Ticket: fx.Ticket, Yes: true})
	var ae *axi.Error
	if !errors.As(err, &ae) || (ae.Code != "PUBLISH_NO_DIVERGENCE" && ae.Code != "NOTHING_TO_PUBLISH") {
		t.Fatalf("err = %v, want *axi.Error PUBLISH_NO_DIVERGENCE or NOTHING_TO_PUBLISH", err)
	}
	if status := run(t, d.Store.Root, "status", "--porcelain"); status != "" {
		t.Fatalf("the refused publish left the store dirty:\n%s", status)
	}
	if subject := run(t, d.Store.Root, "log", "-1", "--format=%s"); subject != subjectBefore {
		t.Fatalf("the store's latest commit is %q, was %q: a refusal before the first write must leave no commit", subject, subjectBefore)
	}
}

// TestBuildAcquireAfterPublishIsInStep documents where a published ticket
// stands. Publish pushes the reviewed head as it is, without squashing or
// adding a commit, so origin's copy of jig/<ticket> is exactly the build
// lease's: the next build acquire (a `jig run` after a post-publish gate round
// or requeue) finds the two in step and carries on, where a publish that
// rewrote the branch would have left them diverged.
func TestBuildAcquireAfterPublishIsInStep(t *testing.T) {
	t.Parallel()

	fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
	d := newDeps(t, fx)
	gateToClean(t, fx, d)
	if _, err := Publish(d, PublishOpts{Ticket: fx.Ticket, Yes: true}); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if originRef(t, fx.RepoRemote, "refs/heads/"+ticketBranch(fx.Ticket)) == "" {
		t.Fatal("test setup: publish left no branch on origin")
	}

	lease, err := pool.Acquire(fx.Home, "fixture-repo", fx.RepoRemote, "main", ticketBranch(fx.Ticket), fx.Ticket, pool.Build)
	if err != nil {
		t.Fatalf("build Acquire after a publish: %v, want the build lease in step with origin's branch", err)
	}
	standing, err := pool.Compare(lease.Dir, ticketBranch(fx.Ticket))
	if err != nil || standing != pool.InStep {
		t.Fatalf("the build lease stands %v against origin (%v), want in step", standing, err)
	}
}
