package main

import (
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/store"
)

// TestDecideRecordsACallWithoutMovingTheSlice: a call is written already
// answered, as kind call with provenance agent, with a call journal line; the
// slice keeps its state, no question line is written and the phase does not
// move.
func TestDecideRecordsACallWithoutMovingTheSlice(t *testing.T) {
	t.Parallel()
	fx, st, e := workFixture(t)
	T := fx.Ticket
	before, _ := st.ReadSliceState(T, "c")
	phaseBefore, _ := runMain(t, e, "", "next", T)

	out, code := runMain(t, e, "", "decide", T, "--slice", "c", "Formal", "or", "casual?", "--chose", "casual, the brief's examples are")
	if code != 0 || !strings.Contains(out, "question: q-001\n") || !strings.Contains(out, "slice: c\n") || !strings.Contains(out, "kind: call\n") {
		t.Fatalf("decide: exit %d\n%s", code, out)
	}
	q, err := st.ReadQuestion(T, "q-001")
	if err != nil {
		t.Fatal(err)
	}
	if q.Kind != store.KindCall || q.Status != "answered" || q.Provenance != "agent" || q.Slice != "c" ||
		q.Body != "Formal or casual?" || q.Answer != "casual, the brief's examples are" {
		t.Fatalf("question = %+v", q)
	}
	calls := linesOf(t, st, T, journal.EventCall)
	if len(calls) != 1 || calls[0].Ref != "q-001" || calls[0].Slice != "c" || calls[0].Provenance != "agent" ||
		!strings.Contains(calls[0].Summary, "Formal or casual?") || !strings.Contains(calls[0].Summary, "casual, the brief's") {
		t.Fatalf("call lines = %+v", calls)
	}
	if n := len(linesOf(t, st, T, journal.EventQuestion)); n != 0 {
		t.Errorf("%d question lines, want none: a call is not an ask", n)
	}
	if after, _ := st.ReadSliceState(T, "c"); after != before {
		t.Errorf("slice state %+v became %+v", before, after)
	}
	if phaseAfter, _ := runMain(t, e, "", "next", T); phaseAfter != phaseBefore {
		t.Errorf("next changed:\n%s\nbecame\n%s", phaseBefore, phaseAfter)
	}
	if ctx, _ := runMain(t, e, "", "context", T, "--slice", "c"); !strings.Contains(ctx, "q-001,answered,Formal or casual?") || !strings.Contains(ctx, ",agent,call") {
		t.Errorf("context does not list the call as one:\n%s", ctx)
	}
}

// TestDecideNeedsBothParts: a missing question or a blank --chose is refused
// with the usage, and nothing is written.
func TestDecideNeedsBothParts(t *testing.T) {
	t.Parallel()
	fx, st, e := workFixture(t)
	T := fx.Ticket
	for _, args := range [][]string{
		{"decide", T, "--slice", "c", "Formal or casual?"},
		{"decide", T, "--slice", "c", "Formal or casual?", "--chose", "  "},
		{"decide", T, "--slice", "c", "--chose", "casual"},
	} {
		out, code := runMain(t, e, "", args...)
		if code == 0 || !strings.Contains(out, "VALIDATION_ERROR") || !strings.Contains(out, "jig decide") {
			t.Errorf("%v: exit %d\n%s", args, code, out)
		}
	}
	if qs, _ := st.ReadQuestions(T); len(qs) != 0 {
		t.Errorf("questions = %+v, want none", qs)
	}
}

// TestDecideOnTheWholeTicketAndAPublishedOne: without a slice the call is on
// the whole ticket, and a published ticket takes it.
func TestDecideOnTheWholeTicketAndAPublishedOne(t *testing.T) {
	t.Parallel()
	fx, st, e := workFixture(t)
	T := fx.Ticket
	if err := journal.Append(st, T, journal.Line{Event: journal.EventPublishDone}); err != nil {
		t.Fatal(err)
	}
	out, code := runMain(t, e, "", "decide", T, "Which tone?", "--chose", "casual")
	if code != 0 || !strings.Contains(out, "slice: -\n") {
		t.Fatalf("decide: exit %d\n%s", code, out)
	}
	if q, err := st.ReadQuestion(T, "q-001"); err != nil || q.Slice != "" || q.Kind != store.KindCall {
		t.Fatalf("question = %+v, %v", q, err)
	}
	if out, code := runMain(t, e, "", "decide", T, "--slice", "nope", "Which tone?", "--chose", "casual"); code == 0 || !strings.Contains(out, "SLICE_NOT_FOUND") {
		t.Errorf("an unknown slice: exit %d\n%s", code, out)
	}
}
