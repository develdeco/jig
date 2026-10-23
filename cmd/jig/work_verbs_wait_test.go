package main

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/store"
)

// TestAskWaitEnds: a wait that is not answered in time exits 2 (NEEDS_INPUT)
// and leaves the question open; one interrupted exits 130; a question
// superseded under it ends it with QUESTION_SUPERSEDED; --wait with no text
// waits on the open question of the slice, and asks for one when there is none.
func TestAskWaitEnds(t *testing.T) {
	t.Parallel()
	fx, st, e := workFixture(t)
	T := fx.Ticket

	out, code := runMain(t, e, "", "ask", T, "--slice", "c", "--wait", "--timeout", "300ms", "--session", "s-1", "which?")
	if code != 2 || !strings.Contains(out, "code: NEEDS_INPUT") || !strings.Contains(out, "no answer to q-001") ||
		!strings.Contains(out, "jig ask --wait --slice c") {
		t.Fatalf("timed out wait: exit %d\n%s", code, out)
	}
	if q, _ := st.ReadQuestion(T, "q-001"); q.Status != "open" {
		t.Errorf("q-001 is %s after the wait gave up, want open", q.Status)
	}

	// Waiting again takes the open question, not a new one.
	out, code = runMain(t, e, "", "ask", T, "--slice", "c", "--wait", "--timeout", "300ms")
	if code != 2 || !strings.Contains(out, "no answer to q-001") {
		t.Fatalf("second wait: exit %d\n%s", code, out)
	}
	if qs, _ := st.ReadQuestions(T); len(qs) != 1 {
		t.Errorf("a wait with no text made %d questions, want the 1 there was", len(qs))
	}

	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(300*time.Millisecond, cancel)
	out, code = runMain(t, e.withContext(ctx), "", "ask", T, "--slice", "c", "--wait", "--timeout", "0")
	if code != exitInterrupted || !strings.Contains(out, "interrupted") {
		t.Fatalf("interrupted wait: exit %d\n%s", code, out)
	}

	// The wait itself, given q-001: the supersede lands while it polls or,
	// on a slow machine, before its first read, and either way ends it. As
	// `ask --wait`, a supersede landing before the command found q-001 would
	// leave the slice nothing to wait on.
	time.AfterFunc(300*time.Millisecond, func() {
		if err := st.Supersede(T, "q-001"); err != nil {
			t.Errorf("supersede q-001: %v", err)
		}
	})
	var buf strings.Builder
	code = waitForAnswer(e, &buf, work{St: st, Ticket: T, Session: "s-1"}, store.Slice{ID: "c"}, "q-001", 30*time.Second)
	if out = buf.String(); code != 1 || !strings.Contains(out, "code: QUESTION_SUPERSEDED") {
		t.Fatalf("superseded wait: exit %d\n%s", code, out)
	}

	// Nothing open on d, and no text: asked to ask.
	out, code = runMain(t, e, "", "ask", T, "--slice", "d", "--wait")
	if code != 2 || !strings.Contains(out, "no open question to wait on") {
		t.Errorf("wait with nothing to wait on: exit %d\n%s", code, out)
	}
	if out, code := runMain(t, e, "", "ask", T, "--slice", "d"); code != 2 || !strings.Contains(out, "missing the question") {
		t.Errorf("ask with no question: exit %d\n%s", code, out)
	}
}

// TestNoteGoesOnTheSliceOrTheTicket: a note names the slice it is on when one
// is given, the whole ticket when not; text may start with a dash after --,
// and a note too long for the journal is refused.
func TestNoteGoesOnTheSliceOrTheTicket(t *testing.T) {
	t.Parallel()
	fx, st, e := workFixture(t)
	T := fx.Ticket
	if out, code := runMain(t, e, "", "note", T, "--slice", "b", "--session", "s-1", "use", "Clamp"); code != 0 || !strings.Contains(out, "slice: b\n") {
		t.Fatalf("note on b: exit %d\n%s", code, out)
	}
	if out, code := runMain(t, e, "", "note", T, "--", "-1 means unset"); code != 0 || strings.Contains(out, "slice:") {
		t.Fatalf("ticket note: exit %d\n%s", code, out)
	}
	notes := linesOf(t, st, T, journal.EventNote)
	if len(notes) != 2 || notes[0].Slice != "b" || notes[0].Summary != "use Clamp" || notes[0].Session != "s-1" ||
		notes[0].Ref == "" || notes[1].Slice != "" || notes[1].Summary != "-1 means unset" || notes[0].Provenance != "agent" {
		t.Errorf("note lines = %+v", notes)
	}
	if out, code := runMain(t, e, "", "note", T, strings.Repeat("x", noteMax+1)); code != 2 || !strings.Contains(out, "at most") {
		t.Errorf("a long note: exit %d\n%s", code, out)
	}
	if out, code := runMain(t, e, "", "note", T); code != 2 || !strings.Contains(out, "missing the note") {
		t.Errorf("an empty note: exit %d\n%s", code, out)
	}
	if out, code := runMain(t, e, "", "note", T, "--slice", "zz", "x"); code != 1 || !strings.Contains(out, "SLICE_NOT_FOUND") {
		t.Errorf("a note on a slice that is not there: exit %d\n%s", code, out)
	}
}
