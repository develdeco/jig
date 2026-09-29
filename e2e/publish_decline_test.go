package e2e

import (
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/gitx"
)

// TestPublishDeclinedPromptCommitsAndPushesStore is the decisive end-to-end
// test for Publish's own deferred push: a real jig subprocess runs
// `jig publish <ticket>` with no --yes and no scripted stdin. runJig never
// sets cmd.Stdin, so the child reads EOF at the real
// bufio.NewReader(os.Stdin).ReadString('\n') inside confirm - the same
// empty answer an operator declining at the real prompt would give - and
// Publish returns PUBLISH_DECLINED. Before Publish grew its own deferred
// push, everything it had already journaled and written to the store (the
// reconcile outcome, the changelogs, the ledger entry, the contract-index
// entry, evidence, the PR body) stayed uncommitted on that decline, landing
// only once some later command's own Store.Sync swept it into an
// anonymous, unattributed commit. This test asserts the store is clean,
// pushed, and carries a commit subject naming the decline right after the
// declined attempt itself - not merely after a following command's Sync.
func TestPublishDeclinedPromptCommitsAndPushesStore(t *testing.T) {
	fx, _ := newFixture(t, fixture.Opts{})
	ticket := fx.Ticket

	// --- drive the ticket to a clean gate: same shape as
	// TestEndToEndTwice's steps 1-4, minus the scripted divergent store
	// commit, which exercises the store's rebase path rather than anything
	// relevant here.
	r1 := runJig(t, fx.StoreDir, "run", ticket, "--backend", "fake", "--scenario", fx.ScenarioDir)
	if r1.Code != 2 {
		t.Fatalf("run 1 exit = %d, want 2 (paused at q-001)\nstdout:\n%s\nstderr:\n%s", r1.Code, r1.Stdout, r1.Stderr)
	}
	r2 := runJig(t, fx.StoreDir, "run", ticket, "--answer", "q-001", "Casual.", "--backend", "fake", "--scenario", fx.ScenarioDir)
	if r2.Code != 0 {
		t.Fatalf("run 2 (answer) exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", r2.Code, r2.Stdout, r2.Stderr)
	}
	r3 := runJig(t, fx.StoreDir, "gate", ticket, "--scenario", fx.ScenarioDir)
	if r3.Code != 0 {
		t.Fatalf("gate (round 1) exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", r3.Code, r3.Stdout, r3.Stderr)
	}
	r4 := runJig(t, fx.StoreDir, "run", ticket, "--backend", "fake", "--scenario", fx.ScenarioDir)
	if r4.Code != 0 {
		t.Fatalf("run (fix-1) exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", r4.Code, r4.Stdout, r4.Stderr)
	}
	r5 := runJig(t, fx.StoreDir, "gate", ticket, "--scenario", fx.ScenarioDir)
	if r5.Code != 0 {
		t.Fatalf("gate (round 2, clean) exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", r5.Code, r5.Stdout, r5.Stderr)
	}

	// --- decline: no --yes, no scripted stdin.
	r6 := runJig(t, fx.StoreDir, "publish", ticket)
	if r6.Code != 1 {
		t.Fatalf("publish (declined) exit = %d, want 1\nstdout:\n%s\nstderr:\n%s", r6.Code, r6.Stdout, r6.Stderr)
	}
	if !strings.Contains(r6.Stdout, "code: PUBLISH_DECLINED") {
		t.Fatalf("publish (declined) stdout missing code: PUBLISH_DECLINED:\n%s", r6.Stdout)
	}

	// The store's working copy must already be clean: no leftover tracked
	// change from the journal lines or documents Publish wrote before the
	// decline.
	status, err := gitx.Run(fx.StoreDir, "status", "--porcelain")
	if err != nil {
		t.Fatalf("store status: %v", err)
	}
	if status != "" {
		t.Fatalf("store working copy is dirty after the declined publish:\n%s", status)
	}

	// And already pushed: local HEAD must match the store remote's tip,
	// not merely be committed locally.
	localHead := gitLog(t, fx.StoreDir, "rev-parse", "HEAD")
	remoteHead := gitLog(t, fx.StoreRemote, "rev-parse", "main")
	if localHead != remoteHead {
		t.Fatalf("store local HEAD %s != store remote main %s: the declined publish's failure commit was not pushed", localHead, remoteHead)
	}

	// The failure commit's subject names the ticket and the decline, never
	// a round (Publish is not round-scoped the way Gate is).
	gotSubject := gitLog(t, fx.StoreDir, "log", "-1", "--format=%s")
	wantSubject := ticket + ": publish failed: PUBLISH_DECLINED"
	if gotSubject != wantSubject {
		t.Fatalf("store failure commit subject = %q, want %q", gotSubject, wantSubject)
	}

	// The ticket branch itself is never pushed on a decline: only the
	// store's own record of the attempt is.
	if _, err := gitx.Run(fx.RepoRemote, "rev-parse", "--verify", "refs/heads/jig/"+ticket); err == nil {
		t.Fatalf("published branch jig/%s must not exist on the repo remote after a declined publish", ticket)
	}
}
