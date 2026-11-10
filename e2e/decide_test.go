package e2e

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/store"
)

// TestDecideShowsInRecallAndNeverParksTheSlice: a builder claims a slice and
// records a judgment call with the real binary. The slice stays building (a
// call asks no one), and on a ticket that is already in the ledger the entry
// is rewritten in place so `jig recall` lists the call marked as one, beside a
// person's answer in its own form.
func TestDecideShowsInRecallAndNeverParksTheSlice(t *testing.T) {
	t.Parallel()
	fx, env := newFixture(t, fixture.Opts{})
	env = env.without("JIG_SLICE", "JIG_SESSION").with("JIG_SESSION=s-builder")
	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatal(err)
	}
	if r := runJig(t, env, fx.RepoDir, "claim", fx.Ticket, "--slice", "c"); r.Code != 0 {
		t.Fatalf("jig claim: exit %d\n%s%s", r.Code, r.Stdout, r.Stderr)
	}
	// A person's answer first, then the ticket is in the ledger.
	if r := runJig(t, env, fx.RepoDir, "ask", fx.Ticket, "Formal", "or", "casual?"); r.Code != 0 {
		t.Fatalf("jig ask: exit %d\n%s%s", r.Code, r.Stdout, r.Stderr)
	}
	if r := runJig(t, env.with("JIG_SESSION=s-person"), fx.StoreDir, "answer", fx.Ticket, "q-001", "Casual."); r.Code != 0 {
		t.Fatalf("jig answer: exit %d\n%s%s", r.Code, r.Stdout, r.Stderr)
	}
	writeFileOrFatal(t, filepath.Join(fx.StoreDir, "ledger.md"), []byte(
		"# Ledger\n\n## "+fx.Ticket+" - Greeting tone\n\nDelivered 1 slice(s).\n\n**Answers:**\nnone\n"))

	// The held slice is the default: no --slice needed.
	r := runJig(t, env, fx.RepoDir, "decide", fx.Ticket, "Which", "locale?", "--chose", "English first, the brief's examples are")
	if r.Code != 0 {
		t.Fatalf("jig decide: exit %d\nstdout:\n%s\nstderr:\n%s", r.Code, r.Stdout, r.Stderr)
	}
	for _, want := range []string{"decided:\n", "  question: q-002\n", "  slice: c\n", "  kind: call\n"} {
		if !strings.Contains(r.Stdout, want) {
			t.Errorf("jig decide output lacks %q:\n%s", want, r.Stdout)
		}
	}
	if ss, _ := st.ReadSliceState(fx.Ticket, "c"); ss.State != "building" || ss.Question != "" {
		t.Errorf("after the call slice c is %+v, want building with no question", ss)
	}

	rep := recallJSON(t, env, fx.StoreDir)
	if len(rep.Answers) != 1 || len(rep.Answers[0].Answers) != 2 {
		t.Fatalf("recall answers = %+v, want the ticket's entry with two answers", rep.Answers)
	}
	person, call := rep.Answers[0].Answers[0], rep.Answers[0].Answers[1]
	if person.ID != "q-001" || person.By != "" || call.ID != "q-002" || call.By != "call" || !strings.Contains(call.Text, "English first") {
		t.Errorf("answers = %+v and %+v, want the person's q-001 and the call q-002", person, call)
	}
	if out := runJig(t, env, fx.StoreDir, "recall"); !strings.Contains(out.Stdout, "answers[2]{ticket,title,id,by,text}:") || !strings.Contains(out.Stdout, ",q-002,call,") {
		t.Errorf("jig recall does not mark the call:\n%s", out.Stdout)
	}
}
