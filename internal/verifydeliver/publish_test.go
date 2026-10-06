package verifydeliver

import (
	"errors"
	"os"
	"path/filepath"
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

func TestPublishFullChain(t *testing.T) {
	t.Parallel()

	fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
	d := newDeps(t, fx)
	gateToClean(t, fx, d)
	advanceTarget(t, fx)

	report, err := Publish(d, PublishOpts{Ticket: fx.Ticket, Yes: true})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if report.Tier != "oracles-only" {
		t.Fatalf("Tier = %q, want oracles-only", report.Tier)
	}
	sha, ok := report.Squashed["fixture-repo"]
	if !ok || sha == "" {
		t.Fatalf("Squashed[fixture-repo] missing, got %v", report.Squashed)
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
	assertJournal := func(event, outcome string) {
		for _, l := range lines {
			if l.Event == event && (outcome == "" || l.Outcome == outcome) {
				return
			}
		}
		t.Fatalf("journal missing event=%q outcome=%q", event, outcome)
	}
	assertJournalPrefix := func(event, outcomePrefix string) {
		for _, l := range lines {
			if l.Event == event && strings.HasPrefix(l.Outcome, outcomePrefix) {
				return
			}
		}
		t.Fatalf("journal missing event=%q outcome prefix=%q", event, outcomePrefix)
	}
	// The reconcile outcome now carries the divergence file count too
	// ("<policy>:<n>-files"), so only the policy prefix is pinned here.
	assertJournalPrefix("reconcile", "local-rebase:")
	assertJournal("revalidate", "oracles-only:target-moved")
	assertJournal("memorize", "")
	assertJournal("squash", "")
	assertJournal("pr", "")
	assertJournal("publish-done", "")

	// Memorize landed before the squash: the squash commit's tree
	// contains the retrieval notes file.
	show, err := gitx.Run(publishLeaseDir(t, fx), "show", sha+":.claude/retrieval/"+fx.Ticket+".md")
	if err != nil {
		t.Fatalf("show retrieval notes in squash commit: %v", err)
	}
	if !strings.Contains(show, "# "+fx.Ticket+" - retrieval notes") {
		t.Fatalf("retrieval notes content = %q, missing heading", show)
	}

	msg, err := gitx.Run(publishLeaseDir(t, fx), "log", "-1", "--format=%s", sha)
	if err != nil {
		t.Fatalf("read squash commit message: %v", err)
	}
	if !strings.HasPrefix(msg, fx.Ticket+": ") {
		t.Fatalf("squash message = %q, want prefix %q", msg, fx.Ticket+": ")
	}

	// The squash landed on origin's jig/<ticket> branch.
	remoteHead, err := gitx.Run(fx.RepoRemote, "rev-parse", "refs/heads/jig/"+fx.Ticket)
	if err != nil {
		t.Fatalf("resolve origin jig branch: %v", err)
	}
	if remoteHead != sha {
		t.Fatalf("origin jig/%s = %s, want squash sha %s", fx.Ticket, remoteHead, sha)
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

// TestPublishTitlesTheSquashWithTheRecordedTitle covers the title fallback
// through publish itself (consolidatedTitle is only unit-tested with the title
// handed to it): when no slice has a goal, the squash commit's subject carries
// the title recorded in ticket.yaml, not the bare ticket id.
func TestPublishTitlesTheSquashWithTheRecordedTitle(t *testing.T) {
	t.Parallel()

	fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
	d := newDeps(t, fx)
	gateToClean(t, fx, d)

	slices, err := d.Store.ReadSlices(fx.Ticket)
	if err != nil {
		t.Fatalf("ReadSlices: %v", err)
	}
	for i := range slices {
		slices[i].Goal = ""
	}
	data, err := yaml.Marshal(store.SliceFile{Slices: slices})
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

	report, err := Publish(d, PublishOpts{Ticket: fx.Ticket, Yes: true})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	sha := report.Squashed["fixture-repo"]
	if sha == "" {
		t.Fatalf("Squashed[fixture-repo] missing, got %v", report.Squashed)
	}
	msg, err := gitx.Run(publishLeaseDir(t, fx), "log", "-1", "--format=%s", sha)
	if err != nil {
		t.Fatalf("read squash commit message: %v", err)
	}
	if want := fx.Ticket + ": Recorded title"; msg != want {
		t.Fatalf("squash message = %q, want %q", msg, want)
	}
}

func TestPublishTierNone(t *testing.T) {
	t.Parallel()

	fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
	d := newDeps(t, fx)
	gateToClean(t, fx, d)
	// No target move.

	report, err := Publish(d, PublishOpts{Ticket: fx.Ticket, Yes: true})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if report.Tier != "none" {
		t.Fatalf("Tier = %q, want none", report.Tier)
	}

	lines, err := journal.Read(d.Store, fx.Ticket)
	if err != nil {
		t.Fatalf("journal.Read: %v", err)
	}
	found := false
	for _, l := range lines {
		if l.Event == "revalidate" && l.Outcome == "none:target-unmoved" {
			found = true
		}
	}
	if !found {
		t.Fatal("journal missing revalidate outcome none:target-unmoved")
	}
}

// TestSquashRefusesPushedRange: a never-pushed branch squashes as it always
// has, and the squash still refuses a range whose commits already reached a
// remote under another name - commits someone may be building on, which the
// squash would leave behind in a second copy. The branch is not on origin
// under its own name, so it is the squash's rule that stops it, not the
// branch-on-origin rule that lets publish push a branch as it is
// (TestPublishPushesABranchAlreadyOnOriginAsItIs).
func TestSquashRefusesPushedRange(t *testing.T) {
	t.Parallel()

	fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
	d := newDeps(t, fx)
	gateToClean(t, fx, d)

	// Pre-push the build branch once, under another name: a commit mid-range
	// becomes reachable from a remote-tracking ref.
	buildDir := buildLeaseDir(t, fx)
	if _, err := gitx.Run(buildDir, "push", "origin", "jig/"+fx.Ticket+":scratch/"+fx.Ticket); err != nil {
		t.Fatalf("pre-push build branch: %v", err)
	}

	_, err := Publish(d, PublishOpts{Ticket: fx.Ticket, Yes: true})
	var ae *axi.Error
	if !errors.As(err, &ae) || ae.Code != "PUSHED_RANGE" {
		t.Fatalf("err = %v, want *axi.Error PUSHED_RANGE", err)
	}

	// Nothing was pushed under the ticket's own name.
	if got := originRef(t, fx.RepoRemote, "refs/heads/jig/"+fx.Ticket); got != "" {
		t.Fatalf("origin has jig/%s = %s after the refused publish, want no such branch", fx.Ticket, got)
	}

	// PUSHED_RANGE fires inside squash, well after recordAndCheckDivergence
	// set journaled - the store must still end up clean, pushed, and
	// carrying this failure's own subject rather than left for a later
	// Sync to sweep anonymously.
	wantFailureCommit(t, d, "main", fx.Ticket+": publish failed: PUSHED_RANGE")
}

// TestPublishNoDivergenceStillPushesStore checks that PUBLISH_NO_DIVERGENCE -
// raised by recordAndCheckDivergence itself, immediately after it appends
// Publish's own earliest store write and before revalidate, memorize,
// squash, confirm or any push ever run - still leaves the store committed
// and pushed under that failure's own subject: journaled must already be
// true by the time this, the very first post-journal failure Publish can
// hit, happens - not only once Publish reaches its later steps.
func TestPublishNoDivergenceStillPushesStore(t *testing.T) {
	t.Parallel()

	fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
	d := newDeps(t, fx)
	gateToClean(t, fx, d)

	// Fast-forward origin's target branch to exactly the build lease's
	// ticket-branch tip: reconcile then has nothing to rebase (the branch
	// is already based on the new origin/main), so its diff against the
	// target comes out empty - the same integration failure
	// TestRecordAndCheckDivergenceRefusesEmptyDiff drives directly against
	// recordAndCheckDivergence, reached here through the whole Publish
	// pipeline instead, so it also exercises wherever journaled is set.
	run(t, buildLeaseDir(t, fx), "push", "origin", "jig/"+fx.Ticket+":main")

	_, err := Publish(d, PublishOpts{Ticket: fx.Ticket, Yes: true})
	var ae *axi.Error
	if !errors.As(err, &ae) || ae.Code != "PUBLISH_NO_DIVERGENCE" {
		t.Fatalf("err = %v, want *axi.Error PUBLISH_NO_DIVERGENCE", err)
	}

	wantFailureCommit(t, d, "main", fx.Ticket+": publish failed: PUBLISH_NO_DIVERGENCE")
}

// TestRecordAndCheckDivergenceRefusesEmptyDiff checks that a reconcile whose
// merge left the branch with no diff at all against the (moved) target -
// because the target already carries the identical change - is refused,
// and that the journalled reconcile outcome still records the policy and
// file count before the refusal.
func TestRecordAndCheckDivergenceRefusesEmptyDiff(t *testing.T) {
	t.Parallel()

	_, remote := tinyRepo(t)

	clone := cloneFrom(t, remote)
	run(t, clone, "checkout", "-b", "jig/T-1")
	writeAndCommit(t, clone, "g.txt", "same content", "branch work")
	run(t, clone, "push", "origin", "jig/T-1")

	// Advance main with the identical change, as if it landed upstream
	// through another path: the ticket branch's diff against the new
	// target is empty even though the branch itself has a commit.
	advance := cloneFrom(t, remote)
	writeAndCommit(t, advance, "g.txt", "same content", "identical change landed on main")
	run(t, advance, "push", "origin", "main")

	run(t, clone, "fetch", "origin")
	policy, err := reconcile(clone, ticketBranch("T-1"), "main", nil)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}
	if policy != policyMerge {
		t.Fatalf("policy = %q, want %q", policy, policyMerge)
	}

	storeDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(storeDir, "project.yaml"), []byte("schema_version: 1\n"), 0o644); err != nil {
		t.Fatalf("write project.yaml: %v", err)
	}
	st, err := store.Open(storeDir)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	d := Deps{Store: st}

	files, err := diffFiles(clone, "main")
	if err != nil {
		t.Fatalf("diffFiles: %v", err)
	}
	err = recordAndCheckDivergence(d, "T-1", policy, files)
	var ae *axi.Error
	if !errors.As(err, &ae) || ae.Code != "PUBLISH_NO_DIVERGENCE" {
		t.Fatalf("err = %v, want *axi.Error PUBLISH_NO_DIVERGENCE", err)
	}

	lines, err := journal.Read(st, "T-1")
	if err != nil {
		t.Fatalf("journal.Read: %v", err)
	}
	found := false
	for _, l := range lines {
		if l.Event == "reconcile" && l.Outcome == policy+":0-files" {
			found = true
		}
	}
	if !found {
		t.Fatalf("journal missing reconcile outcome %s:0-files, got %+v", policy, lines)
	}
}

// TestRecordAndCheckDivergenceAllowsRealChange checks the non-empty-diff
// path stays unblocked: a reconcile that actually integrates new content
// against the target must not trip PUBLISH_NO_DIVERGENCE.
func TestRecordAndCheckDivergenceAllowsRealChange(t *testing.T) {
	t.Parallel()

	_, remote := tinyRepo(t)

	clone := cloneFrom(t, remote)
	run(t, clone, "checkout", "-b", "jig/T-1")
	writeAndCommit(t, clone, "g.txt", "branch work", "branch work")

	advance := cloneFrom(t, remote)
	writeAndCommit(t, advance, "h.txt", "target moved", "target moved")
	run(t, advance, "push", "origin", "main")

	run(t, clone, "fetch", "origin")
	policy, err := reconcile(clone, ticketBranch("T-1"), "main", nil)
	if err != nil {
		t.Fatalf("reconcile: %v", err)
	}

	storeDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(storeDir, "project.yaml"), []byte("schema_version: 1\n"), 0o644); err != nil {
		t.Fatalf("write project.yaml: %v", err)
	}
	st, err := store.Open(storeDir)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	d := Deps{Store: st}

	files, err := diffFiles(clone, "main")
	if err != nil {
		t.Fatalf("diffFiles: %v", err)
	}
	if err := recordAndCheckDivergence(d, "T-1", policy, files); err != nil {
		t.Fatalf("recordAndCheckDivergence: %v", err)
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

	err := checkNonEmptyRange(clone, "main")
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

	if err := checkNonEmptyRange(clone, "main"); err != nil {
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

// TestBuildAcquireAfterPublishIsRefusedAsDiverged documents where a published
// ticket stands. Publish rebases and squashes jig/<ticket> in its own lease
// and pushes the squash, while the build lease keeps the unsquashed originals,
// so the ticket's branch is on origin and the build lease's copy has diverged
// from it: the next build acquire (a `jig run` after a post-publish gate round
// or requeue) stops with BRANCH_DIVERGED rather than merge a squash with its
// own originals. The sync rule is the branch's, the ordinary jig/<ticket>
// included once it is on origin. Merging origin's branch into the build lease,
// which the refusal says to do, is the way on: a publish from there is a
// fast-forward (TestPublishRepublishesAPublishedTicket).
func TestBuildAcquireAfterPublishIsRefusedAsDiverged(t *testing.T) {
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

	_, err := pool.Acquire(fx.Home, "fixture-repo", fx.RepoRemote, "main", ticketBranch(fx.Ticket), fx.Ticket, pool.Build)
	var ae *axi.Error
	if !errors.As(err, &ae) || ae.Code != "BRANCH_DIVERGED" {
		t.Fatalf("build Acquire after a publish: err = %v, want an *axi.Error BRANCH_DIVERGED", err)
	}
	if !strings.Contains(ae.Msg, "has 1 the lease lacks") {
		t.Fatalf("BRANCH_DIVERGED = %q, want origin's one squash commit named as what the lease lacks", ae.Msg)
	}
}
