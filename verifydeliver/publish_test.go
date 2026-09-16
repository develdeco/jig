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
	assertJournal("reconcile", "local-rebase")
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

func mustPoolDir(t *testing.T) string {
	t.Helper()
	dir, err := home.PoolDir()
	if err != nil {
		t.Fatalf("resolve pool dir: %v", err)
	}
	return dir
}
