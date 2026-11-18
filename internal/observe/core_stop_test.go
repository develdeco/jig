package observe

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/ledger"
	"github.com/develdeco/jig/internal/store"
)

// unattendedClaim records a claim made with --unattended by session s1.
func (r *rig) unattendedClaim(slice string) {
	r.add(journal.Line{Event: journal.EventClaim, Slice: slice, Session: "s1", Unattended: true})
}

// TestStopDoesNotNameARunningVerification: a verification is the builder's own
// run in its own shell, so Stop has nothing to say about one that is under
// way, and an unattended session is not held back for it. A pending line
// with a live worker and a worker only the ledger knows are both passed over.
func TestStopDoesNotNameARunningVerification(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.onTicketBranch()
	r.handle(r.ev(KindSessionStart))
	r.unattendedClaim("a")
	r.recordStart()
	sha := r.commit("a", "a.txt")
	r.add(journal.Line{Event: journal.EventVerifyPending, Slice: "a", Commit: sha, Attempt: 1})
	if err := ledger.Append(r.d.JigHome, journal.Line{Ticket: r.fx.Ticket, Slice: "a", Event: journal.EventVerifySpawned, Commit: sha, PID: os.Getpid()}); err != nil {
		t.Fatal(err)
	}
	sha2 := r.commit("b", "b.txt")
	if err := ledger.Append(r.d.JigHome, journal.Line{Ticket: r.fx.Ticket, Slice: "b", Event: journal.EventVerifySpawned, Commit: sha2, PID: os.Getpid()}); err != nil {
		t.Fatal(err)
	}

	res := r.handle(r.ev(KindStop))
	for _, text := range []string{res.Message, res.Block} {
		if strings.Contains(text, "still running") || strings.Contains(text, "--slice a --wait` to wait") {
			t.Errorf("Stop = %+v, want no running verification named", res)
		}
	}
	// The commit that has no recorded result is still named, with the command
	// that clears it.
	if !strings.Contains(res.Block, "commit "+sha2[:12]+" for slice b is not verified") ||
		!strings.Contains(res.Block, "jig verify "+r.fx.Ticket+" --slice b --wait") {
		t.Errorf("Block = %q, want the commit with no result held with the command that verifies it", res.Block)
	}
}

func TestStopNamesTrackedFilesNotCommitted(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.onTicketBranch()
	r.handle(r.ev(KindSessionStart))
	if res := r.handle(r.ev(KindStop)); !res.Empty() {
		t.Fatalf("Stop on a clean tree = %+v, want nothing", res)
	}
	if err := os.WriteFile(filepath.Join(r.fx.RepoDir, "go.mod"), []byte("module changed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(r.fx.RepoDir, "untracked.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	res := r.handle(r.ev(KindStop))
	if !strings.Contains(res.Message, "1 tracked file changed and not committed: go.mod") || strings.Contains(res.Message, "untracked.txt") {
		t.Errorf("Stop message = %q, want the one tracked file", res.Message)
	}
}

func TestStopNamesAnOpenQuestionWithoutHoldingAnyoneBack(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.onTicketBranch()
	r.handle(r.ev(KindSessionStart))
	r.unattendedClaim("a")
	if err := r.st.WriteQuestion(r.fx.Ticket, store.Question{ID: "q-001", Slice: "a", Status: "open", Body: "Which port?\nMore."}); err != nil {
		t.Fatal(err)
	}
	res := r.handle(r.ev(KindStop))
	if res.Block != "" || !strings.Contains(res.Message, "question q-001 is open (slice a): Which port?") {
		t.Errorf("Stop = %+v, want the question said and no block: an unattended session waits for its answer by stopping", res)
	}
}
