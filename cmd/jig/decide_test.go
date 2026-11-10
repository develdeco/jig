package main

import (
	"os"
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

// TestDecideRewritesOnlyThePublishedTicketsLedgerEntry: with three tickets in
// the ledger, a call on the second rewrites its entry in place, with the call
// listed, and leaves the others as they were; a ticket with no entry leaves
// the ledger untouched.
func TestDecideRewritesOnlyThePublishedTicketsLedgerEntry(t *testing.T) {
	t.Parallel()
	fx, st, e := workFixture(t)
	T := fx.Ticket
	first := "## AAA-1 - First\n\nDelivered 1 slice(s).\n\n**Answers:**\nnone\n"
	third := "## ZZZ-3 - Third\n\nDelivered 1 slice(s).\n\n**Answers:**\n- q-001: Which? -> This.\n"
	stale := "## " + T + " - Stale title\n\nDelivered 9 slice(s).\n\n**Answers:**\nnone\n"
	ledger := "# Ledger\n\n" + first + "\n" + stale + "\n" + third
	if err := os.WriteFile(st.LedgerPath(), []byte(ledger), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, code := runMain(t, e, "", "decide", T, "--slice", "c", "Which tone?", "--chose", "casual"); code != 0 {
		t.Fatalf("decide: exit %d\n%s", code, out)
	}
	data, err := os.ReadFile(st.LedgerPath())
	if err != nil {
		t.Fatal(err)
	}
	got := string(data)
	i1, i2, i3 := strings.Index(got, "## AAA-1"), strings.Index(got, "## "+T), strings.Index(got, "## ZZZ-3")
	if !(0 < i1 && i1 < i2 && i2 < i3) || strings.Count(got, "## "+T) != 1 {
		t.Fatalf("entries are not in place:\n%s", got)
	}
	if !strings.Contains(got, "- q-001 (call): Which tone? -> casual") || strings.Contains(got, "Stale title") {
		t.Errorf("the entry does not list the call:\n%s", got)
	}
	if !strings.Contains(got, first) || !strings.HasSuffix(got, third) {
		t.Errorf("the other entries changed:\n%s", got)
	}

	// An unpublished ticket: no entry for it, so the ledger stays as it is.
	if err := os.WriteFile(st.LedgerPath(), []byte("# Ledger\n\n"+first), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, code := runMain(t, e, "", "decide", T, "Another?", "--chose", "yes"); code != 0 {
		t.Fatalf("decide: exit %d\n%s", code, out)
	}
	if data, _ := os.ReadFile(st.LedgerPath()); string(data) != "# Ledger\n\n"+first {
		t.Errorf("the ledger changed for an unpublished ticket:\n%s", data)
	}
}
