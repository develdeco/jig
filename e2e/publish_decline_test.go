package e2e

import (
	"strings"
	"testing"

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
// changelogs, the ledger entry, the contract-index entry, evidence, the PR
// body) stayed uncommitted on that decline, landing only once some later
// command's own Store.Sync swept it into an anonymous, unattributed commit.
// This test asserts the store is clean, pushed, and carries a commit subject
// naming the decline right after the declined attempt itself - not merely
// after a following command's Sync.
func TestPublishDeclinedPromptCommitsAndPushesStore(t *testing.T) {
	t.Parallel()
	// --- the ticket built by hand and reviewed clean by a session of its own.
	b := newBuiltTicket(t, true)
	ticket := b.fx.Ticket
	b.openRound(t)
	b.reviewedClean(t)

	// --- decline: no --yes, no scripted stdin.
	r6 := runJig(t, b.env, b.fx.StoreDir, "publish", ticket)
	if r6.Code != 1 {
		t.Fatalf("publish (declined) exit = %d, want 1\nstdout:\n%s\nstderr:\n%s", r6.Code, r6.Stdout, r6.Stderr)
	}
	if !strings.Contains(r6.Stdout, "code: PUBLISH_DECLINED") {
		t.Fatalf("publish (declined) stdout missing code: PUBLISH_DECLINED:\n%s", r6.Stdout)
	}

	// The store's working copy must already be clean: no leftover tracked
	// change from the journal lines or documents Publish wrote before the
	// decline.
	status, err := gitx.Run(b.fx.StoreDir, "status", "--porcelain")
	if err != nil {
		t.Fatalf("store status: %v", err)
	}
	if status != "" {
		t.Fatalf("store working copy is dirty after the declined publish:\n%s", status)
	}

	// And already pushed: local HEAD must match the store remote's tip,
	// not merely be committed locally.
	localHead := gitLog(t, b.fx.StoreDir, "rev-parse", "HEAD")
	remoteHead := gitLog(t, b.fx.StoreRemote, "rev-parse", "main")
	if localHead != remoteHead {
		t.Fatalf("store local HEAD %s != store remote main %s: the declined publish's failure commit was not pushed", localHead, remoteHead)
	}

	// The failure commit's subject names the ticket and the decline, never
	// a round (Publish is not round-scoped the way Gate is).
	gotSubject := gitLog(t, b.fx.StoreDir, "log", "-1", "--format=%s")
	wantSubject := ticket + ": publish failed: PUBLISH_DECLINED"
	if gotSubject != wantSubject {
		t.Fatalf("store failure commit subject = %q, want %q", gotSubject, wantSubject)
	}

	// The ticket branch itself is never pushed on a decline: only the
	// store's own record of the attempt is.
	if got := originRefOrEmpty(t, b.fx.RepoRemote, "refs/heads/"+b.branch); got != "" {
		t.Fatalf("published branch %s must not exist on the repo remote after a declined publish (at %s)", b.branch, got)
	}
}
