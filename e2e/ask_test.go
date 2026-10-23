package e2e

import (
	"os"
	"strings"
	"testing"
	"time"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/store"
)

// TestAskAnswerAcrossProcesses: a session asks and waits in one process, the
// person answers from another, in another directory and as another session,
// and the waiting ask prints the answer with who it speaks for and ends. The
// slice is parked while the question is open and goes back to work (queued,
// nobody holds it) when it is answered; the question and answer lines carry
// the sessions and the provenance; and the ledger notes the answer was handed
// over, so no hook gives it again.
func TestAskAnswerAcrossProcesses(t *testing.T) {
	t.Parallel()
	fx, env := newFixture(t, fixture.Opts{})
	env = env.without("JIG_SLICE", "JIG_SESSION")
	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatal(err)
	}

	asker := startJig(t, env.with("JIG_SESSION=s-asker"), fx.RepoDir,
		"ask", fx.Ticket, "--slice", "c", "--wait", "--timeout", "2m", "Formal", "or", "casual?")
	// It pushes before it waits: the store remote has the question, and the
	// slice parked on it, as soon as the process is waiting.
	waitFor(t, 30*time.Second, "q-001 on the store remote", func() bool {
		got, err := gitx.Run(fx.StoreRemote, "show", "main:tickets/"+fx.Ticket+"/questions/q-001.md")
		return err == nil && strings.Contains(got, "Formal or casual?")
	})
	if q, err := st.ReadQuestion(fx.Ticket, "q-001"); err != nil || q.Body != "Formal or casual?" || q.Status != "open" {
		t.Errorf("question = %+v, %v, want q-001 open with the words joined", q, err)
	}
	if ss, _ := st.ReadSliceState(fx.Ticket, "c"); ss.State != "needs-input" || ss.Question != "q-001" {
		t.Errorf("while it waits slice c is %+v, want needs-input on q-001", ss)
	}

	// The person answers from the store directory, as another session.
	r := runJig(t, env.with("JIG_SESSION=s-person"), fx.StoreDir, "answer", fx.Ticket, "q-001", "Casual,", "please.")
	if r.Code != 0 {
		t.Fatalf("jig answer: exit %d\nstdout:\n%s\nstderr:\n%s", r.Code, r.Stdout, r.Stderr)
	}
	for _, want := range []string{"answered:\n", "  question: q-001\n", "  slice: c\n", "  provenance: agent\n", "  state: queued\n"} {
		if !strings.Contains(r.Stdout, want) {
			t.Errorf("jig answer output lacks %q:\n%s", want, r.Stdout)
		}
	}

	var out []string
	for {
		line, ok := asker.Line(30 * time.Second)
		if !ok {
			break
		}
		out = append(out, line)
	}
	res := asker.Stop()
	if res.Code != 0 {
		t.Fatalf("jig ask --wait: exit %d, want 0 after the answer\nstdout:\n%s\nstderr:\n%s", res.Code, res.Stdout, res.Stderr)
	}
	printed := strings.Join(out, "\n")
	for _, want := range []string{"answer:\n", "  question: q-001\n", "  slice: c\n", "  provenance: agent\n", "text:\n  Casual, please.\n"} {
		if !strings.Contains(printed+"\n", want) {
			t.Errorf("the waiting ask printed no %q:\n%s", want, printed)
		}
	}

	if ss, _ := st.ReadSliceState(fx.Ticket, "c"); ss.State != "queued" || ss.Question != "" {
		t.Errorf("after the answer slice c is %+v, want queued with no question", ss)
	}
	q, err := st.ReadQuestion(fx.Ticket, "q-001")
	if err != nil || q.Status != "answered" || q.Answer != "Casual, please." || q.Provenance != "agent" {
		t.Errorf("question = %+v, %v, want answered by an agent with the text", q, err)
	}
	var asked, answered []journal.Line
	for _, l := range readJournal(t, fx, fx.Ticket) {
		switch l.Event {
		case journal.EventQuestion:
			asked = append(asked, l)
		case journal.EventAnswer:
			answered = append(answered, l)
		}
	}
	if len(asked) != 1 || asked[0].Session != "s-asker" || asked[0].Ref != "q-001" || asked[0].Slice != "c" || asked[0].Summary != "Formal or casual?" {
		t.Errorf("question lines = %+v, want one from s-asker about slice c", asked)
	}
	if len(answered) != 1 || answered[0].Session != "s-person" || answered[0].Ref != "q-001" || answered[0].Provenance != "agent" {
		t.Errorf("answer lines = %+v, want one from s-person with provenance agent", answered)
	}
	delivered := 0
	for _, l := range readLedger(t, env) {
		if l.Event == journal.EventNoteDelivered && l.Session == "s-asker" && l.Ref == "q-001" {
			delivered++
		}
	}
	if delivered != 1 {
		t.Errorf("the ledger has %d note-delivered lines for s-asker and q-001, want 1", delivered)
	}

	// A second answer is refused, and says why.
	r = runJig(t, env, fx.StoreDir, "answer", fx.Ticket, "q-001", "Formal.")
	if r.Code != 1 || !strings.Contains(r.Stdout, "code: QUESTION_NOT_OPEN") {
		t.Errorf("answering twice: exit %d, want 1 with QUESTION_NOT_OPEN\n%s", r.Code, r.Stdout)
	}
	if _, err := os.Stat(st.TicketDir(fx.Ticket)); err != nil {
		t.Fatal(err)
	}
}
