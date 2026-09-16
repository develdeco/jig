package verifydeliver

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/axi"
	"github.com/develdeco/jig/fixture"
	"github.com/develdeco/jig/gitx"
	"github.com/develdeco/jig/home"
	"github.com/develdeco/jig/journal"
	"github.com/develdeco/jig/store"
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
	if _, err := runGitEnv(clone, buildGitEnv, "commit", "-m", "upstream: unrelated change"); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if _, err := gitx.Run(clone, "push", "origin", "main"); err != nil {
		t.Fatalf("push: %v", err)
	}
}

func TestPublishFullChain(t *testing.T) {
	t.Setenv("JIG_HOME", t.TempDir())
	fx := fixture.Generate(t, fixture.Opts{})
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
	assertJournal("route", "")
	assertJournal("publish-done", "")

	// Memorize landed before the squash: the squash commit's tree
	// contains the retrieval notes file.
	show, err := gitx.Run(filepath.Join(mustPoolDir(t), "fixture-repo", fx.Ticket+"-publish"), "show", sha+":.claude/retrieval/"+fx.Ticket+".md")
	if err != nil {
		t.Fatalf("show retrieval notes in squash commit: %v", err)
	}
	if !strings.Contains(show, "# "+fx.Ticket+" — retrieval notes") {
		t.Fatalf("retrieval notes content = %q, missing heading", show)
	}

	msg, err := gitx.Run(filepath.Join(mustPoolDir(t), "fixture-repo", fx.Ticket+"-publish"), "log", "-1", "--format=%s", sha)
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
		filepath.Join("tracker", "ticket.md"),
		filepath.Join("tracker", "subtasks.yaml"),
		filepath.Join("tracker", "comments", "001.md"),
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

func TestPublishTierNone(t *testing.T) {
	t.Setenv("JIG_HOME", t.TempDir())
	fx := fixture.Generate(t, fixture.Opts{})
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

func TestSquashRefusesPushedRange(t *testing.T) {
	t.Setenv("JIG_HOME", t.TempDir())
	fx := fixture.Generate(t, fixture.Opts{})
	d := newDeps(t, fx)
	gateToClean(t, fx, d)

	// Pre-push the build branch once: a commit mid-range becomes
	// reachable from a remote-tracking ref.
	buildDir := buildLeaseDir(t, fx)
	if _, err := gitx.Run(buildDir, "push", "origin", "jig/"+fx.Ticket); err != nil {
		t.Fatalf("pre-push build branch: %v", err)
	}

	_, err := Publish(d, PublishOpts{Ticket: fx.Ticket, Yes: true})
	var ae *axi.Error
	if !errors.As(err, &ae) || ae.Code != "PUSHED_RANGE" {
		t.Fatalf("err = %v, want *axi.Error PUSHED_RANGE", err)
	}

	// Nothing was force-pushed: the remote branch still matches exactly
	// what was pre-pushed (no diverging squash landed on it).
	buildHead, err := gitx.Run(buildDir, "rev-parse", "HEAD")
	if err != nil {
		t.Fatalf("resolve build HEAD: %v", err)
	}
	remoteHead, err := gitx.Run(fx.RepoRemote, "rev-parse", "refs/heads/jig/"+fx.Ticket)
	if err != nil {
		t.Fatalf("resolve remote jig branch: %v", err)
	}
	if remoteHead != buildHead {
		t.Fatalf("remote jig/%s = %s, want unchanged pre-push head %s", fx.Ticket, remoteHead, buildHead)
	}
}

// TestRecordAndCheckDivergenceRefusesEmptyDiff checks that a reconcile whose
// merge left the branch with no diff at all against the (moved) target —
// because the target already carries the identical change — is refused,
// and that the journalled reconcile outcome still records the policy and
// file count before the refusal.
func TestRecordAndCheckDivergenceRefusesEmptyDiff(t *testing.T) {
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
	policy, err := reconcile(clone, "T-1", "main")
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

	err = recordAndCheckDivergence(d, "T-1", clone, "main", policy)
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
	_, remote := tinyRepo(t)

	clone := cloneFrom(t, remote)
	run(t, clone, "checkout", "-b", "jig/T-1")
	writeAndCommit(t, clone, "g.txt", "branch work", "branch work")

	advance := cloneFrom(t, remote)
	writeAndCommit(t, advance, "h.txt", "target moved", "target moved")
	run(t, advance, "push", "origin", "main")

	run(t, clone, "fetch", "origin")
	policy, err := reconcile(clone, "T-1", "main")
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

	if err := recordAndCheckDivergence(d, "T-1", clone, "main", policy); err != nil {
		t.Fatalf("recordAndCheckDivergence: %v", err)
	}
}

// TestPublishConfirmWiring checks that Publish threads an honest confirm
// value into guardedPush: a declined interactive prompt must stop before
// any push is attempted, and both --yes and an accepted prompt must pass
// confirmed=true — never a hardcoded literal, and never proceeding past a
// decline.
func TestPublishConfirmWiring(t *testing.T) {
	origPush := guardedPush
	origConfirm := stdinConfirm
	defer func() { guardedPush = origPush; stdinConfirm = origConfirm }()

	newPublishableFixture := func(t *testing.T) (*fixture.Fixture, Deps) {
		t.Helper()
		t.Setenv("JIG_HOME", t.TempDir())
		fx := fixture.Generate(t, fixture.Opts{})
		d := newDeps(t, fx)
		gateToClean(t, fx, d)
		return fx, d
	}

	t.Run("declined_prompt_never_pushes", func(t *testing.T) {
		fx, d := newPublishableFixture(t)
		called := false
		guardedPush = func(string, string, string, bool) error {
			called = true
			return nil
		}
		stdinConfirm = func() (string, error) { return "n", nil }

		_, err := Publish(d, PublishOpts{Ticket: fx.Ticket, Yes: false})
		var ae *axi.Error
		if !errors.As(err, &ae) || ae.Code != "PUBLISH_DECLINED" {
			t.Fatalf("err = %v, want *axi.Error PUBLISH_DECLINED", err)
		}
		if called {
			t.Fatal("guardedPush was called despite a declined confirm")
		}
	})

	t.Run("yes_flag_threads_confirmed_true", func(t *testing.T) {
		fx, d := newPublishableFixture(t)
		var gotConfirmed bool
		guardedPush = func(_, _, _ string, confirmed bool) error {
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
		fx, d := newPublishableFixture(t)
		var gotConfirmed bool
		guardedPush = func(_, _, _ string, confirmed bool) error {
			gotConfirmed = confirmed
			return nil
		}
		stdinConfirm = func() (string, error) { return "yes", nil }

		if _, err := Publish(d, PublishOpts{Ticket: fx.Ticket, Yes: false}); err != nil {
			t.Fatalf("Publish: %v", err)
		}
		if !gotConfirmed {
			t.Fatal("guardedPush confirmed = false, want true after an accepted prompt")
		}
	})
}

// TestPublishRefusesWhenLatestGateNotClean checks Publish's precondition:
// even when every slice (including a round's fix slice) is green, an
// unrebutted fix-slices verdict from the latest gate round must refuse
// publish rather than ship work whose review was never re-confirmed clean.
func TestPublishRefusesWhenLatestGateNotClean(t *testing.T) {
	t.Setenv("JIG_HOME", t.TempDir())
	fx := fixture.Generate(t, fixture.Opts{})
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
	_, remote := tinyRepo(t)
	clone := cloneFrom(t, remote)
	run(t, clone, "checkout", "-b", "jig/T-1")
	writeAndCommit(t, clone, "g.txt", "branch work", "branch work")
	run(t, clone, "fetch", "origin")

	if err := checkNonEmptyRange(clone, "main"); err != nil {
		t.Fatalf("checkNonEmptyRange: %v", err)
	}
}

// TestRouteCustomRoutesExcludeDiffChangelogs checks that a project.yaml
// routes: map actually drives which files feed the tracker projection's
// comments: excluding "gate/round-*/diff-changelog.md" from pr.comments
// must leave only the consolidated changelog behind, instead of the
// hardcoded default (consolidated plus every gate round's diff changelog).
func TestRouteCustomRoutesExcludeDiffChangelogs(t *testing.T) {
	t.Setenv("JIG_HOME", t.TempDir())
	fx := fixture.Generate(t, fixture.Opts{})
	d := newDeps(t, fx)
	gateToClean(t, fx, d) // two gate rounds, so the default would pick up two diff-changelog.md files

	d.Cfg.Routes = map[string][]string{
		"pr.comments": {"changelog/consolidated.md"},
	}

	if _, err := Publish(d, PublishOpts{Ticket: fx.Ticket, Yes: true}); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	commentsDir := filepath.Join(d.Store.TicketDir(fx.Ticket), "tracker", "comments")
	entries, err := os.ReadDir(commentsDir)
	if err != nil {
		t.Fatalf("ReadDir comments: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("comments count = %d, want 1 (only the consolidated changelog; diff-changelogs excluded by routes)", len(entries))
	}
	got, err := os.ReadFile(filepath.Join(commentsDir, entries[0].Name()))
	if err != nil {
		t.Fatalf("read %s: %v", entries[0].Name(), err)
	}
	want, err := os.ReadFile(filepath.Join(d.Store.TicketDir(fx.Ticket), "changelog", "consolidated.md"))
	if err != nil {
		t.Fatalf("read consolidated.md: %v", err)
	}
	if string(got) != string(want) {
		t.Fatalf("comment content = %q, want the consolidated changelog %q", got, want)
	}
}

func mustPoolDir(t *testing.T) string {
	t.Helper()
	dir, err := home.PoolDir()
	if err != nil {
		t.Fatalf("resolve pool dir: %v", err)
	}
	return dir
}
