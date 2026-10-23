package observe

import (
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/store"
)

func TestPromptDeliversAVerificationOutcomeOnce(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.onTicketBranch()
	r.handle(r.ev(KindSessionStart))
	sha := r.commit("a", "a.txt")
	r.add(journal.Line{Event: journal.EventVerifyPending, Slice: "a", Commit: sha, Attempt: 1})
	r.add(journal.Line{Event: journal.EventOracle, Slice: "a", Commit: sha, Attempt: 1, Outcome: "pass", Seconds: 7})
	r.add(journal.Line{Event: journal.EventVerified, Slice: "a", Commit: sha, Attempt: 1})

	first := r.handle(r.ev(KindPrompt))
	if !strings.Contains(first.Context, "verified "+sha[:12]+" for slice a (attempt 1, 7s)") {
		t.Fatalf("prompt context = %q, want the pass", first.Context)
	}
	if again := r.handle(r.ev(KindPrompt)); again.Context != "" {
		t.Errorf("the same prompt event again gave %q, want nothing: it was told", again.Context)
	}
	if tool := r.handle(r.ev(KindTool, func(e *Event) { e.Tool, e.Command = "Bash", "ls" })); tool.Context != "" {
		t.Errorf("a tool call after gave %q, want nothing", tool.Context)
	}
	delivered := r.ledgerOf(journal.EventNoteDelivered)
	if len(delivered) != 1 || delivered[0].Session != "s1" || delivered[0].Outcome != "prompt" || !strings.HasPrefix(delivered[0].Ref, "verified:") {
		t.Errorf("note-delivered lines = %+v, want one from the prompt", delivered)
	}
	// Another session on the ticket has not heard it, but it began after the
	// outcome, so it is not its own either.
	other := r.ev(KindPrompt, func(e *Event) { e.Session = "s2" })
	if res := r.handle(other); res.Context != "" {
		t.Errorf("a session that began later was told %q", res.Context)
	}
}

func TestAToolCallDeliversAFailureAsContext(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.onTicketBranch()
	r.handle(r.ev(KindSessionStart))
	sha := r.commit("b", "b.txt")
	r.add(journal.Line{Event: journal.EventOracle, Slice: "b", Commit: sha, Attempt: 1, Outcome: "fail", Summary: "FAIL beta TestX"})

	res := r.handle(r.ev(KindTool, func(e *Event) { e.Tool, e.Command = "Bash", "ls" }))
	if !strings.Contains(res.Context, "verification FAILED for slice b at "+sha[:12]+" (attempt 1): FAIL beta TestX") {
		t.Errorf("tool context = %q, want the failure", res.Context)
	}
	if got := r.ledgerOf(journal.EventNoteDelivered); len(got) != 1 || got[0].Outcome != "tool" {
		t.Errorf("note-delivered lines = %+v, want one from the tool call", got)
	}
}

func TestAFailureThatAnotherCommitFixedIsHistory(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.onTicketBranch()
	r.handle(r.ev(KindSessionStart))
	r.add(journal.Line{Event: journal.EventOracle, Slice: "b", Commit: "1111111111111111", Attempt: 1, Outcome: "fail", Summary: "bad"})
	r.add(journal.Line{Event: journal.EventVerified, Slice: "b", Commit: "2222222222222222", Attempt: 2})
	res := r.handle(r.ev(KindPrompt))
	if strings.Contains(res.Context, "FAILED") || !strings.Contains(res.Context, "verified 222222222222") {
		t.Errorf("prompt context = %q, want only the pass", res.Context)
	}
}

func TestAnAnswerReachesTheSessionThatAsked(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.onTicketBranch()
	r.handle(r.ev(KindSessionStart))
	if err := r.st.WriteQuestion(r.fx.Ticket, store.Question{ID: "q-001", Slice: "a", Status: "answered", Body: "Which port?", Answer: "8080"}); err != nil {
		t.Fatal(err)
	}
	r.add(journal.Line{Event: journal.EventQuestion, Slice: "a", Ref: "q-001", Session: "s1"})
	r.add(journal.Line{Event: journal.EventAnswer, Slice: "a", Ref: "q-001", Provenance: "person"})

	if res := r.handle(r.ev(KindPrompt, func(e *Event) { e.Session = "s2" })); res.Context != "" {
		t.Errorf("a session that did not ask was told %q", res.Context)
	}
	res := r.handle(r.ev(KindPrompt))
	if !strings.Contains(res.Context, "q-001 was answered by the person: 8080") {
		t.Errorf("prompt context = %q, want the answer", res.Context)
	}
	if again := r.handle(r.ev(KindPrompt)); again.Context != "" {
		t.Errorf("answered twice: %q", again.Context)
	}
}

func TestANoteSourceIsDeliveredOnceAndSeesTheEvent(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.onTicketBranch()
	var kinds []string
	var seen NoteCtx
	r.d.Sources = []NamedSource{{"fake", func(c NoteCtx) ([]Note, error) {
		kinds = append(kinds, c.Event.Kind)
		seen = c
		return []Note{{Ref: "fake:1", Text: "mind the scope"}, {Ref: "fake:1", Text: "a duplicate"}, {Text: "no ref"}, {Ref: "fake:2"}}, nil
	}}}
	r.handle(r.ev(KindSessionStart))
	res := r.handle(r.ev(KindTool, func(e *Event) { e.Tool, e.Command = "Bash", "ls" }))
	if !strings.Contains(res.Context, "notes[1]:") || !strings.Contains(res.Context, "mind the scope") || strings.Contains(res.Context, "duplicate") {
		t.Fatalf("tool context = %q, want the one note", res.Context)
	}
	if again := r.handle(r.ev(KindPrompt)); again.Context != "" {
		t.Errorf("prompt context = %q, want nothing: the note was told", again.Context)
	}
	if seen.Where.Ticket != r.fx.Ticket || seen.Store == nil || seen.JigHome != r.fx.Home || seen.Unattended || seen.Journal == nil || seen.Since.IsZero() {
		t.Errorf("NoteCtx = %+v, want the located ticket, store, home, an interactive session and the session start", seen)
	}
	if strings.Join(kinds, ",") != "tool,prompt" {
		t.Errorf("the source was called for %v, want tool then prompt", kinds)
	}
}

func TestOneFailingNoteSourceDoesNotSilenceTheOthers(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.onTicketBranch()
	r.d.Sources = []NamedSource{
		{"broken", func(NoteCtx) ([]Note, error) { return nil, errTest }},
		{"panics", func(NoteCtx) ([]Note, error) { panic("boom") }},
		{"fine", func(NoteCtx) ([]Note, error) { return []Note{{Ref: "f", Text: "still here"}}, nil }},
	}
	r.handle(r.ev(KindSessionStart))
	res, err := Handle(r.d, r.ev(KindPrompt))
	if err == nil || !strings.Contains(err.Error(), "broken") || !strings.Contains(err.Error(), "boom") {
		t.Errorf("error = %v, want both failures named", err)
	}
	if !strings.Contains(res.Context, "still here") {
		t.Errorf("context = %q, want the note of the source that works", res.Context)
	}
}

type testError string

func (e testError) Error() string { return string(e) }

const errTest = testError("the source failed")

// TestAFailureOfAnOlderCommitJournaledAfterTheFixIsHistory: verifications
// finish in any order, so the failure of an older commit can reach the
// journal after the newer commit's pass. The commits decide, not the lines.
func TestAFailureOfAnOlderCommitJournaledAfterTheFixIsHistory(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.onTicketBranch()
	r.handle(r.ev(KindSessionStart))
	older := r.commit("b", "b.txt")
	newer := r.commit("b", "b2.txt")
	r.add(journal.Line{Event: journal.EventOracle, Slice: "b", Commit: newer, Attempt: 2, Outcome: "pass"})
	r.add(journal.Line{Event: journal.EventVerified, Slice: "b", Commit: newer, Attempt: 2})
	r.add(journal.Line{Event: journal.EventOracle, Slice: "b", Commit: older, Attempt: 1, Outcome: "fail", Summary: "bad"})
	res := r.handle(r.ev(KindPrompt))
	if strings.Contains(res.Context, "FAILED") || !strings.Contains(res.Context, "verified "+newer[:12]) {
		t.Errorf("prompt context = %q, want only the newer commit's pass", res.Context)
	}
}
