package main

import (
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/store"
)

// workFixture is the fixture with an env that runs in its clone, on no
// ticket branch, with no slice or session in the environment.
func workFixture(t *testing.T) (*fixture.Fixture, *store.Store, env) {
	t.Helper()
	fx := newFixture(t, fixture.Opts{})
	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatal(err)
	}
	return fx, st, testEnv(fx.Home).inDir(fx.RepoDir)
}

func linesOf(t *testing.T, st *store.Store, ticket, event string) []journal.Line {
	t.Helper()
	all, err := journal.Read(st, ticket)
	if err != nil {
		t.Fatal(err)
	}
	var out []journal.Line
	for _, l := range all {
		if l.Event == event {
			out = append(out, l)
		}
	}
	return out
}

// TestAskThenAnswerMovesTheSlice: asking parks the slice and records the
// question line; answering as a person sends it back to queued when nobody
// holds it, to building when a session does, and a second open question keeps
// it parked.
func TestAskThenAnswerMovesTheSlice(t *testing.T) {
	t.Parallel()
	fx, st, e := workFixture(t)
	T := fx.Ticket

	out, code := runMain(t, e, "", "ask", T, "--slice", "c", "--session", "s-1", "Formal", "or", "casual?")
	if code != 0 || !strings.Contains(out, "question: q-001\n") || !strings.Contains(out, "state: needs-input\n") {
		t.Fatalf("ask: exit %d\n%s", code, out)
	}
	if ss, _ := st.ReadSliceState(T, "c"); ss.State != "needs-input" || ss.Question != "q-001" {
		t.Fatalf("after ask slice c = %+v", ss)
	}
	if asked := linesOf(t, st, T, journal.EventQuestion); len(asked) != 1 || asked[0].Session != "s-1" || asked[0].Ref != "q-001" {
		t.Fatalf("question lines = %+v", asked)
	}
	if out, _ := runMain(t, e, "", "status", T); !strings.Contains(out, "c,q-001,jig answer "+T+" q-001") {
		t.Errorf("status does not name jig answer:\n%s", out)
	}

	// A person at a terminal answers; nobody holds slice c: queued.
	out, code = runMain(t, e.atTerminal(), "", "answer", T, "q-001", "Casual.")
	if code != 0 || !strings.Contains(out, "provenance: person\n") || !strings.Contains(out, "state: queued\n") {
		t.Fatalf("answer: exit %d\n%s", code, out)
	}
	if q, _ := st.ReadQuestion(T, "q-001"); q.Provenance != "person" || q.Answer != "Casual." || q.Status != "answered" {
		t.Errorf("question = %+v", q)
	}

	// Held by a session, with two questions open: answering one keeps it
	// parked, answering the last sends it to building.
	runMain(t, e, "", "claim", T, "--slice", "c", "--session", "s-1")
	runMain(t, e, "", "ask", T, "--slice", "c", "--session", "s-1", "one?")
	runMain(t, e, "", "ask", T, "--slice", "c", "--session", "s-1", "two?")
	if out, code := runMain(t, e, "", "answer", T, "q-002", "--as", "agent", "yes"); code != 0 || !strings.Contains(out, "state: needs-input\n") {
		t.Fatalf("answer q-002 with q-003 open: exit %d\n%s", code, out)
	}
	if ss, _ := st.ReadSliceState(T, "c"); ss.State != "needs-input" || ss.Question != "q-003" {
		t.Errorf("slice c = %+v, want parked on q-003", ss)
	}
	if out, code := runMain(t, e, "", "answer", T, "q-003", "no"); code != 0 || !strings.Contains(out, "state: building\n") || !strings.Contains(out, "provenance: agent\n") {
		t.Fatalf("answer q-003: exit %d\n%s", code, out)
	}
	if answered := linesOf(t, st, T, journal.EventAnswer); len(answered) != 3 || answered[2].Ref != "q-003" || answered[1].Provenance != "agent" {
		t.Errorf("answer lines = %+v", answered)
	}
}

// TestAnswerRefusals: a question that is not there, one already answered, a
// bad --as, and a missing text each fail without touching the store.
func TestAnswerRefusals(t *testing.T) {
	t.Parallel()
	fx, _, e := workFixture(t)
	T := fx.Ticket
	runMain(t, e, "", "ask", T, "--slice", "c", "which?")
	runMain(t, e, "", "answer", T, "q-001", "this")

	for _, tc := range []struct {
		args       []string
		code       int
		wantInBody string
	}{
		{[]string{"answer", T, "q-009", "x"}, 2, "has no question q-009"},
		{[]string{"answer", T, "q-001", "again"}, 1, "QUESTION_NOT_OPEN"},
		{[]string{"answer", T, "q-001", "--as", "robot", "x"}, 2, "person or agent"},
		{[]string{"answer", T, "q-001"}, 2, "missing the answer text"},
		{[]string{"answer", T}, 2, "question id"},
		{[]string{"answer", "JIG-77", "q-001", "x"}, 2, "JIG-77"},
	} {
		out, code := runMain(t, e, "", tc.args...)
		if code != tc.code || !strings.Contains(out, tc.wantInBody) {
			t.Errorf("jig %s: exit %d, want %d naming %q\n%s", strings.Join(tc.args, " "), code, tc.code, tc.wantInBody, out)
		}
	}
}
