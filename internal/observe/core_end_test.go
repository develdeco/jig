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

func TestASessionThatStartedOnNoTicketIsTouchedOnItsFirst(t *testing.T) {
	t.Parallel()
	r := newRig(t)
	// The session starts on the default branch: its start line names no
	// ticket, so a reader of the ticket's stream would never see it.
	r.handle(r.ev(KindSessionStart, func(e *Event) { e.Model = "claude-sonnet-5" }))
	if starts := r.ledgerOf(journal.EventSessionStart); len(starts) != 1 || starts[0].Ticket != "" {
		t.Fatalf("session-start lines = %+v, want one naming no ticket", starts)
	}
	// It moves onto the ticket's branch and runs a command there.
	r.onTicketBranch()
	r.handle(r.ev(KindTool, func(e *Event) { e.Tool, e.Command = "Bash", "jig claim JIG-1 --slice a" }))
	touches := r.ledgerOf(journal.EventSessionTouch)
	if len(touches) != 1 || touches[0].Ticket != r.fx.Ticket || touches[0].Model != "claude-sonnet-5" || touches[0].Harness != "claude" {
		t.Fatalf("session-touch lines = %+v, want one moving the session onto %s with its harness and model", touches, r.fx.Ticket)
	}
	// Further calls on the same ticket add none.
	r.handle(r.ev(KindTool, func(e *Event) { e.Tool, e.Command = "Bash", "go test ./..." }))
	if got := r.ledgerOf(journal.EventSessionTouch); len(got) != 1 {
		t.Errorf("session-touch lines = %d after a second call on the same ticket, want 1", len(got))
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
	claude, _ := AdapterFor("claude")
	payload := []byte(`{"session_id":"s1","cwd":` + jsonString(r.fx.RepoDir) + `,"hook_event_name":"SessionStart"}`)
	const asked = "jig knows this session as s1"
	withEnvFile := func(path string) Deps {
		d := r.d
		d.Getenv = func(k string) string {
			if k == "CLAUDE_ENV_FILE" {
				return path
			}
			return ""
		}
		return d
	}

	// Claude Code names a file of the session's, in a directory named after
	// its id, that it sources before each Bash command: the id goes there,
	// and the context does not ask for it.
	sessionDir := filepath.Join(t.TempDir(), "s1")
	if err := os.Mkdir(sessionDir, 0o700); err != nil {
		t.Fatal(err)
	}
	envFile := filepath.Join(sessionDir, "sessionstart-hook-0.sh")
	out, err := Run(withEnvFile(envFile), claude, payload)
	if err != nil || strings.Contains(string(out), asked) || !strings.Contains(string(out), "ticket: "+r.fx.Ticket) {
		t.Errorf("Run with the session's env file = %s, %v, want the context without the request", out, err)
	}
	if got, _ := os.ReadFile(envFile); string(got) != "export JIG_SESSION='s1'\n" {
		t.Errorf("env file = %q, want the export of JIG_SESSION", got)
	}

	// A CLAUDE_ENV_FILE that is not the session's is a script of the
	// person's own: it is never written, and the context asks.
	own := filepath.Join(t.TempDir(), "my-env.sh")
	if err := os.WriteFile(own, []byte("export A=1\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if out, err := Run(withEnvFile(own), claude, payload); err != nil || !strings.Contains(string(out), asked) {
		t.Errorf("Run with the person's own env file = %s, %v, want the context to ask", out, err)
	}
	if got, _ := os.ReadFile(own); string(got) != "export A=1\n" {
		t.Errorf("the person's env file became %q", got)
	}

	// With no such file (a hook run by hand), the context asks; so it does
	// for Codex, which shares the shape but not the file, and for a harness
	// with no way at all.
	if out, err := Run(r.d, claude, payload); err != nil || !strings.Contains(string(out), asked) {
		t.Errorf("Run with no env file = %s, %v, want the context to ask", out, err)
	}
	codex, _ := AdapterFor("codex")
	codexFile := filepath.Join(sessionDir, "codex.sh")
	if out, _ := Run(withEnvFile(codexFile), codex, payload); !strings.Contains(string(out), asked) {
		t.Errorf("Run for codex = %s, want the context to ask", out)
	}
	if _, err := os.Stat(codexFile); !os.IsNotExist(err) {
		t.Errorf("codex wrote an env file (stat: %v)", err)
	}
	gemini, _ := AdapterFor("gemini")
	if out, _ := Run(r.d, gemini, payload); !strings.Contains(string(out), asked) {
		t.Errorf("Run for gemini = %s, want the context to ask", out)
	}

	// A file of the session's that cannot be written is an error, and the
	// context still asks.
	missing := filepath.Join(t.TempDir(), "gone", "s1", "sessionstart-hook-0.sh")
	if out, err := Run(withEnvFile(missing), claude, payload); err == nil || !strings.Contains(string(out), asked) {
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
