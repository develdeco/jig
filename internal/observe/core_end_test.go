package observe

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/ledger"
	"github.com/develdeco/jig/internal/locate"
	"github.com/develdeco/jig/internal/store"
)

func TestSessionEndClosesTheSessionAndNotesAnOpenQuestionOnce(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.onTicketBranch()
	r.handle(r.ev(KindSessionStart))
	if err := r.st.WriteQuestion(r.fx.Ticket, store.Question{ID: "q-001", Slice: "a", Status: "open", Body: "Which port?\nDetails."}); err != nil {
		t.Fatal(err)
	}
	if err := r.st.WriteQuestion(r.fx.Ticket, store.Question{ID: "q-002", Slice: "b", Status: "answered", Body: "Done?", Answer: "yes"}); err != nil {
		t.Fatal(err)
	}

	if res := r.handle(r.ev(KindSessionEnd)); !res.Empty() {
		t.Errorf("SessionEnd = %+v, want nothing to say", res)
	}
	got, err := ledger.Sessions(r.fx.Home)
	if err != nil || got["s1"].Live() {
		t.Errorf("Sessions = %+v, %v, want s1 ended", got["s1"], err)
	}
	notes := []journal.Line{}
	for _, l := range r.journal() {
		if l.Event == journal.EventNote {
			notes = append(notes, l)
		}
	}
	if len(notes) != 1 || notes[0].Ref != "q-001" || notes[0].Session != "s1" || notes[0].Slice != "a" || notes[0].Provenance != "jig" ||
		!strings.Contains(notes[0].Summary, "question q-001 still open: Which port?") {
		t.Fatalf("note lines = %+v, want one for q-001 only", notes)
	}
	// The same session ending again (a resumed one) does not note it twice.
	r.handle(r.ev(KindSessionEnd))
	count := 0
	for _, l := range r.journal() {
		if l.Event == journal.EventNote {
			count++
		}
	}
	if count != 1 {
		t.Errorf("note lines after a second end = %d, want 1", count)
	}
	if hooks := r.hooksOf(KindSessionEnd); len(hooks) != 2 {
		t.Errorf("hook lines = %+v, want one per session-end", hooks)
	}
}

func TestSessionEndCallsTheNoteSourcesButDeliversNothing(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.onTicketBranch()
	calls := 0
	r.d.Sources = []NamedSource{{"spend", func(c NoteCtx) ([]Note, error) {
		if c.Event.Kind == KindSessionEnd {
			calls++
		}
		return []Note{{Ref: "spend:1", Text: "12k tokens"}}, nil
	}}}
	r.handle(r.ev(KindSessionStart))
	if res := r.handle(r.ev(KindSessionEnd)); !res.Empty() || calls != 1 {
		t.Errorf("SessionEnd = %+v after %d source calls, want nothing delivered and one call", res, calls)
	}
	if got := r.ledgerOf(journal.EventNoteDelivered); len(got) != 0 {
		t.Errorf("note-delivered lines = %+v, want none", got)
	}
}

func TestAPromptOnAnotherTicketTouchesTheSession(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.onTicketBranch()
	r.handle(r.ev(KindSessionStart))
	r.handle(r.ev(KindPrompt))
	if got := r.ledgerOf(journal.EventSessionTouch); len(got) != 0 {
		t.Fatalf("session-touch lines = %+v after a prompt on the same ticket, want none", got)
	}
	// The slice the session works on changes (a claim made since).
	r.add(journal.Line{Event: journal.EventClaim, Slice: "b", Session: "s1"})
	r.handle(r.ev(KindPrompt))
	touches := r.ledgerOf(journal.EventSessionTouch)
	if len(touches) != 1 || touches[0].Slice != "b" || touches[0].Ticket != r.fx.Ticket {
		t.Errorf("session-touch lines = %+v, want one that moves the session to slice b", touches)
	}
}

func TestHandleNeverPanics(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.onTicketBranch()
	r.d.Here = func(locate.Where, *store.Store) (string, error) { panic("here broke") }
	res, err := Handle(r.d, r.ev(KindSessionStart))
	if err == nil || !strings.Contains(err.Error(), "here broke") || !res.Empty() {
		t.Errorf("Handle = %+v, %v, want an error that names the panic", res, err)
	}
	if _, err := Handle(r.d, Event{Kind: "nonsense"}); err == nil {
		t.Error("Handle accepted a kind that is none")
	}
	if _, err := Handle(r.d, Event{Kind: KindTool}); err == nil {
		t.Error("Handle accepted an event with no directory")
	}
}

func TestRunParsesHandlesAndRenders(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.onTicketBranch()
	a, _ := AdapterFor("claude")
	payload := `{"session_id":"s1","cwd":` + jsonString(r.fx.RepoDir) + `,"hook_event_name":"SessionStart"}`
	out, err := Run(r.d, a, []byte(payload))
	if err != nil || !strings.Contains(string(out), `"hookEventName":"SessionStart"`) || !strings.Contains(string(out), "ticket: "+r.fx.Ticket) {
		t.Errorf("Run = %s, %v, want the SessionStart context", out, err)
	}
	// An event jig does not act on is ignored; garbage is an error and prints
	// nothing.
	if out, err := Run(r.d, a, []byte(`{"hook_event_name":"Notification"}`)); out != nil || err != nil {
		t.Errorf("Run of an ignored event = %s, %v", out, err)
	}
	if out, err := Run(r.d, a, []byte(`garbage`)); out != nil || err == nil {
		t.Errorf("Run of garbage = %s, %v, want an error and no output", out, err)
	}
}

func TestRunHandsTheSessionIdToTheSessionsCommands(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	r.onTicketBranch()
	a, _ := AdapterFor("claude")
	payload := []byte(`{"session_id":"s1","cwd":` + jsonString(r.fx.RepoDir) + `,"hook_event_name":"SessionStart"}`)
	const asked = "Run the jig commands of this session with JIG_SESSION=s1 in their environment"

	// Claude Code names a file it sources before each Bash command: the id
	// goes there, and the context does not ask for it.
	envFile := filepath.Join(t.TempDir(), "sessionstart-hook-0.sh")
	d := r.d
	d.Getenv = func(k string) string {
		if k == "CLAUDE_ENV_FILE" {
			return envFile
		}
		return ""
	}
	out, err := Run(d, a, payload)
	if err != nil || strings.Contains(string(out), asked) || !strings.Contains(string(out), "ticket: "+r.fx.Ticket) {
		t.Errorf("Run with an env file = %s, %v, want the context without the request", out, err)
	}
	if got, _ := os.ReadFile(envFile); string(got) != "export JIG_SESSION='s1'\n" {
		t.Errorf("env file = %q, want the export of JIG_SESSION", got)
	}

	// With no such file (a hook run by hand, Codex), the context asks.
	out, err = Run(r.d, a, payload)
	if err != nil || !strings.Contains(string(out), asked) {
		t.Errorf("Run with no env file = %s, %v, want the context to ask for %s", out, err, "JIG_SESSION=s1")
	}
	// So does a harness with no way to set it at all.
	gemini, _ := AdapterFor("gemini")
	if out, _ := Run(r.d, gemini, payload); !strings.Contains(string(out), asked) {
		t.Errorf("Run for gemini = %s, want the context to ask for JIG_SESSION=s1", out)
	}
	// A file that cannot be written is an error, and the context still asks.
	d.Getenv = func(k string) string {
		if k == "CLAUDE_ENV_FILE" {
			return filepath.Join(t.TempDir(), "missing", "env.sh")
		}
		return ""
	}
	if out, err := Run(d, a, payload); err == nil || !strings.Contains(string(out), asked) {
		t.Errorf("Run with an unwritable env file = %s, %v, want an error and the context asking", out, err)
	}
}

func jsonString(s string) string { return string(jsonLine(s))[:len(jsonLine(s))-1] }

func TestRegisterNoteSourceRefusesWhatCannotWork(t *testing.T) {
	t.Parallel()
	for name, f := range map[string]func(){
		"no name":   func() { RegisterNoteSource("", func(NoteCtx) ([]Note, error) { return nil, nil }) },
		"no func":   func() { RegisterNoteSource("x-nofunc", nil) },
		"duplicate": func() { RegisterNoteSource("verification", func(NoteCtx) ([]Note, error) { return nil, nil }) },
	} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("%s: RegisterNoteSource did not panic", name)
				}
			}()
			f()
		}()
	}
	names := []string{}
	for _, s := range NoteSources() {
		names = append(names, s.Name)
	}
	if strings.Join(names, ",") != "answer,verification" {
		t.Errorf("registered sources = %v, want the built-in two by name", names)
	}
}
