package observe

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/store"
)

// unattendedClaim records a claim made with --unattended by session s1.
func (r *rig) unattendedClaim(slice string) {
	r.add(journal.Line{Event: journal.EventClaim, Slice: slice, Session: "s1", Unattended: true})
}

func TestStopInteractiveSaysWhatIsRunningAndNeverBlocks(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.onTicketBranch()
	r.handle(r.ev(KindSessionStart))
	sha := r.commit("a", "a.txt")
	r.add(journal.Line{Event: journal.EventVerifyPending, Slice: "a", Commit: sha, Attempt: 1})

	res := r.handle(r.ev(KindStop))
	if res.Block != "" || !strings.Contains(res.Message, "verification of "+sha[:12]+" for slice a is still running") {
		t.Fatalf("Result = %+v, want a message naming the running verification and no block", res)
	}
	// An interactive Stop is the same every time: it is the state, not an event.
	if again := r.handle(r.ev(KindStop, func(e *Event) { e.StopActive = true })); again.Message != res.Message || again.Block != "" {
		t.Errorf("second Stop = %+v, want the same message", again)
	}
	hooks := r.hooksOf(KindStop)
	if len(hooks) != 2 || hooks[0].Harness != "claude" || !strings.HasPrefix(hooks[0].Summary, "shown: ") {
		t.Errorf("hook lines = %+v, want two stop lines that say they showed it", hooks)
	}
}

func TestStopSeesAWorkerTheLedgerStartedBeforeItWroteTheJournal(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.onTicketBranch()
	r.handle(r.ev(KindSessionStart))
	sha := r.commit("a", "a.txt")
	r.handle(Event{Harness: "git", Kind: KindPostCommit, Cwd: r.fx.RepoDir})

	res := r.handle(r.ev(KindStop))
	if !strings.Contains(res.Message, "verification of "+sha[:12]+" for slice a is still running") {
		t.Errorf("Stop message = %q, want the commit whose worker only the ledger knows", res.Message)
	}
	// Once the journal has its outcome the commit is not running any more.
	r.add(journal.Line{Event: journal.EventVerified, Slice: "a", Commit: sha, Attempt: 1})
	if res := r.handle(r.ev(KindStop)); strings.Contains(res.Message, "still running") {
		t.Errorf("Stop message = %q after the verification finished", res.Message)
	}
	// A worker the ledger started long ago and the journal never heard of is
	// a dead one.
	sha2 := r.commit("b", "b.txt")
	r.handle(Event{Harness: "git", Kind: KindPostCommit, Cwd: r.fx.RepoDir})
	r.clock.advance(11 * time.Minute)
	if res := r.handle(r.ev(KindStop)); strings.Contains(res.Message, sha2[:12]) {
		t.Errorf("Stop message = %q, want a worker started 11 minutes ago passed over", res.Message)
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
