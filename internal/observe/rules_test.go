package observe

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/locate"
	"github.com/develdeco/jig/internal/phase"
	"github.com/develdeco/jig/internal/store"
)

// stubPhaseHandler is a handler whose phase derivation says what the test
// sets and whose event is an edit of files, in cwd, on the ticket: the note
// rules need no clone, no store and no session.
func stubPhaseHandler(cwd, ticket string, t phase.Ticket, files ...string) *handler {
	return &handler{
		d:  Deps{Derive: func(*store.Store, string) (phase.Ticket, error) { return t, nil }},
		ev: Event{Kind: KindTool, Tool: "Edit", Files: files},
		w:  locate.Where{Cwd: cwd, Ticket: ticket},
	}
}

func TestRuleAtClaimedSlice(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name  string
		state string
		extra string
		want  bool
	}{
		{"building", phase.SubActive, "", true},
		{"parked for an answer", phase.SubNeedsYou, "", true},
		{"stalled", phase.SubFailed, "", true},
		{"parked on its environment", phase.SubWaiting, phase.WaitsForEnvironment, true},
		{"waiting on blockers", phase.SubWaiting, "waits for b", false},
		{"ready", phase.SubPending, "", false},
		{"done", phase.SubDone, "", false},
	} {
		tk := buildPhase(map[string]string{"a": c.state, "b": phase.SubPending}, "a", "b")
		tk.Phases[1].Subs[0].Detail = c.extra
		if got := claimedSlice(tk); got != c.want {
			t.Errorf("%s: claimedSlice = %v, want %v", c.name, got, c.want)
		}
	}
	// A claim is held at the build phase's slices even while the brief is current.
	tk := briefPhase()
	tk.Phases[1] = phase.Phase{Name: phase.Build, State: "pending", Subs: []phase.Sub{{Key: "slice:a", State: phase.SubActive}}}
	if !claimedSlice(tk) {
		t.Error("a claimed slice under an unapproved brief is not claimed")
	}
	// A sub that is not a slice never counts.
	tk = buildPhase(nil)
	tk.Phases[1].Subs = []phase.Sub{{Key: "approved", State: phase.SubActive}}
	if claimedSlice(tk) {
		t.Error("a sub that is no slice counted as a claim")
	}
}

func TestRuleAtPathKey(t *testing.T) {
	t.Parallel()
	for in, want := range map[string]string{
		`a\b\c`:    "a/b/c",
		"a/b/../c": "a/c",
		"a//b/./c": "a/b/c",
		`a\b/c\d`:  "a/b/c/d",
	} {
		if got := pathKey(in); got != want {
			t.Errorf("pathKey(%q) = %q, want %q", in, got, want)
		}
	}
	want := "A/B"
	if runtime.GOOS == "windows" {
		want = "a/b"
	}
	if got := pathKey("A/B"); got != want {
		t.Errorf("pathKey(A/B) = %q, want %q on %s", got, want, runtime.GOOS)
	}
}

func TestRuleAtUnclaimedEditNote(t *testing.T) {
	t.Parallel()
	cwd := t.TempDir()
	sub := filepath.Join(cwd, "pkg")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	inside := filepath.Join(cwd, "main.go")
	ready := buildPhase(map[string]string{"a": phase.SubPending, "b": phase.SubWaiting, "c": phase.SubDone}, "a", "b", "c")
	claimed := buildPhase(map[string]string{"a": phase.SubActive, "b": phase.SubPending}, "a", "b")
	envParked := buildPhase(map[string]string{"a": phase.SubWaiting}, "a")
	envParked.Phases[1].Subs[0].Detail = phase.WaitsForEnvironment
	blocked := buildPhase(map[string]string{"a": phase.SubWaiting}, "a")
	blocked.Phases[1].Subs[0].Detail = "waits for b"
	done, closed := ready, ready
	done.Code, closed.Code = phase.CodeDone, phase.CodeClosed

	for _, c := range []struct {
		name  string
		tk    phase.Ticket
		files []string
		want  string // "" none, else a substring of the one note
	}{
		{"claim note names the ready slices only", ready, []string{inside}, "ready: a)"},
		{"no ready slice", blocked, []string{inside}, "--slice <slice>`)."},
		{"brief without approval", briefPhase(), []string{inside}, "has no approved brief yet"},
		{"a slice claimed", claimed, []string{inside}, ""},
		{"a slice parked on its environment", envParked, []string{inside}, ""},
		{"a done ticket", done, []string{inside}, ""},
		{"a closed ticket", closed, []string{inside}, ""},
		{"a file below a subdirectory", ready, []string{filepath.Join(sub, "deep", "x.go")}, "jig claim"},
		{"a relative file is relative to the cwd", ready, []string{"x.go"}, "jig claim"},
		{"either slash forms a path", ready, []string{filepath.ToSlash(inside)}, "jig claim"},
		{"a sibling of the checkout", ready, []string{cwd + "-tickets" + string(filepath.Separator) + "x.md"}, ""},
		{"outside the checkout", ready, []string{filepath.Join(t.TempDir(), "x.txt")}, ""},
		{"no files", ready, nil, ""},
		{"an empty file name", ready, []string{""}, ""},
	} {
		h := stubPhaseHandler(cwd, "ticket-a", c.tk, c.files...)
		notes := h.unclaimedEditNotes(sessionMemory{delivered: map[string]bool{}})
		if c.want == "" {
			if len(notes) != 0 {
				t.Errorf("%s: notes = %+v, want none", c.name, notes)
			}
			continue
		}
		if len(notes) != 1 || !strings.Contains(notes[0].Text, c.want) {
			t.Errorf("%s: notes = %+v, want one with %q", c.name, notes, c.want)
		}
	}

	// The refs name the ticket and which of the two notes, and a note heard is not repeated.
	h := stubPhaseHandler(cwd, "ticket-a", ready, inside)
	n := h.unclaimedEditNotes(sessionMemory{delivered: map[string]bool{}})
	if len(n) != 1 || n[0].Ref != "unclaimed-edit:ticket-a:claim" || n[0].Source != "unclaimed-edit" {
		t.Errorf("claim note = %+v", n)
	}
	h = stubPhaseHandler(cwd, "ticket-a", briefPhase(), inside)
	n = h.unclaimedEditNotes(sessionMemory{delivered: map[string]bool{}})
	if len(n) != 1 || n[0].Ref != "unclaimed-edit:ticket-a:brief" {
		t.Errorf("brief note = %+v", n)
	}
	both := sessionMemory{delivered: map[string]bool{"unclaimed-edit:ticket-a:brief": true, "unclaimed-edit:ticket-a:claim": true}}
	if n := h.unclaimedEditNotes(both); n != nil {
		t.Errorf("notes after both were heard = %+v", n)
	}

	// Anything that is not an edit of a ticket's checkout says nothing.
	for name, mod := range map[string]func(*handler){
		"a Bash call":   func(h *handler) { h.ev.Files = nil },
		"a prompt":      func(h *handler) { h.ev.Kind = KindPrompt },
		"no ticket":     func(h *handler) { h.w.Ticket = "" },
		"no derivation": func(h *handler) { h.d.Derive = nil },
		"no cwd":        func(h *handler) { h.w.Cwd = "" },
	} {
		h := stubPhaseHandler(cwd, "ticket-a", ready, inside)
		mod(h)
		if n := h.unclaimedEditNotes(sessionMemory{delivered: map[string]bool{}}); n != nil {
			t.Errorf("%s: notes = %+v, want none", name, n)
		}
	}

	// A phase that cannot be derived costs the note and is reported.
	h = stubPhaseHandler(cwd, "ticket-a", ready, inside)
	h.d.Derive = func(*store.Store, string) (phase.Ticket, error) { return phase.Ticket{}, errors.New("boom") }
	if n := h.unclaimedEditNotes(sessionMemory{delivered: map[string]bool{}}); n != nil || len(h.errs) != 1 {
		t.Errorf("a failed derivation: notes = %+v, errs = %v", n, h.errs)
	}
}

func TestRuleAtPhaseNotes(t *testing.T) {
	t.Parallel()
	phases := map[string]phase.Ticket{
		"A-1": {Current: phase.Build, Ready: true},
		"A-2": {Current: phase.Review, Ready: false, Reason: "a round is open"},
		"A-3": {Current: phase.Publish, Ready: true},
		"A-4": {Current: phase.Brief, Ready: true},
		"A-5": {Current: phase.Publish, Code: phase.CodeDone},
		"A-6": {Current: phase.Build, Code: phase.CodeClosed},
	}
	derive := func(_ *store.Store, id string) (phase.Ticket, error) {
		tk, ok := phases[id]
		if !ok {
			return phase.Ticket{}, errors.New("no phase for " + id)
		}
		return tk, nil
	}
	notes := func(mem sessionMemory, ids ...string) ([]Note, *handler) {
		h := &handler{d: Deps{Derive: derive}}
		return h.phaseNotes(mem, ids), h
	}
	fresh := sessionMemory{delivered: map[string]bool{}}

	got, _ := notes(fresh, "A-1", "A-2", "A-3", "A-4")
	if len(got) != maxNamedTickets {
		t.Fatalf("notes = %d, want the cap of %d", len(got), maxNamedTickets)
	}
	for i, want := range []string{
		"A-1 is at its build phase, ready: use the build skill (`jig skill build`); `jig next A-1` says what the phase needs.",
		"A-2 is at its review phase, not ready (a round is open): use the review skill (`jig skill review`); `jig next A-2` says what the phase needs.",
		"A-3 is at its publish phase, ready: use the publish skill (`jig skill publish`); `jig next A-3` says what the phase needs.",
	} {
		if got[i].Text != want {
			t.Errorf("note %d = %q, want %q", i, got[i].Text, want)
		}
	}
	if got[0].Ref != "phase:A-1:build" || got[1].Ref != "phase:A-2:review" {
		t.Errorf("refs = %q, %q", got[0].Ref, got[1].Ref)
	}

	// A done or closed ticket, or one already heard at this phase, takes no place.
	heard := sessionMemory{delivered: map[string]bool{"phase:A-1:build": true}}
	got, _ = notes(heard, "A-5", "A-6", "A-1", "A-2", "A-3", "A-4")
	if len(got) != 3 || !strings.HasPrefix(got[0].Text, "A-2 ") || !strings.HasPrefix(got[2].Text, "A-4 ") || !strings.Contains(got[2].Text, "`jig skill intake`") {
		t.Errorf("notes = %+v, want A-2, A-3, A-4", got)
	}

	// A ticket whose phase cannot be derived costs its own note, and is reported.
	got, h := notes(fresh, "A-9", "A-1")
	if len(got) != 1 || !strings.HasPrefix(got[0].Text, "A-1 ") || len(h.errs) != 1 {
		t.Errorf("notes = %+v, errs = %v", got, h.errs)
	}

	// No derivation, no notes.
	if got := (&handler{}).phaseNotes(fresh, []string{"A-1"}); got != nil {
		t.Errorf("notes without a derivation = %+v", got)
	}
}

// ruleCtx is a note context over a store that holds nothing (a directory
// with a project.yaml) and a journal the test hands over.
func ruleCtx(t *testing.T, session string, lines []journal.Line, since time.Time) NoteCtx {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "project.yaml"), []byte("schema_version: 2\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	return NoteCtx{
		Event:   Event{Kind: KindTool, Session: session},
		Where:   locate.Where{Ticket: "ticket-a"},
		Store:   st,
		JigHome: t.TempDir(),
		Since:   since,
		Journal: func() ([]journal.Line, error) { return lines, nil },
	}
}

func TestRuleAtVerificationNotes(t *testing.T) {
	t.Parallel()
	start := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	at := func(m int) string { return start.Add(time.Duration(m) * time.Minute).Format(time.RFC3339Nano) }
	const sha = "0123456789abcdef0123"
	fail := journal.Line{Event: journal.EventOracle, Outcome: "fail", Commit: sha, Slice: "a", Attempt: 1, TS: at(1), Summary: "red"}
	pass := journal.Line{Event: journal.EventVerified, Commit: sha, Slice: "a", Attempt: 2, TS: at(2)}

	run := func(since time.Time, lines ...journal.Line) []Note {
		c := ruleCtx(t, "s1", lines, since)
		n, err := verificationNotes(c)
		if err != nil {
			t.Fatal(err)
		}
		return n
	}

	n := run(start, fail)
	if len(n) != 1 || !n[0].Enforce || n[0].Text != "verification FAILED for slice a at 0123456789ab (attempt 1): red" || n[0].Ref != "oracle:0123456789ab:a:1" {
		t.Errorf("a failure = %+v", n)
	}
	e := journal.Line{Event: journal.EventVerifyError, Commit: sha, Slice: "a", Attempt: 1, TS: at(1)}
	if n := run(start, e); len(n) != 1 || !strings.Contains(n[0].Text, "could not run") || !n[0].Enforce {
		t.Errorf("a run that could not run = %+v", n)
	}
	if n := run(start, fail, pass); len(n) != 1 || n[0].Enforce || !strings.HasPrefix(n[0].Text, "verified 0123456789ab for slice a (attempt 2") {
		t.Errorf("a later pass: notes = %+v, want the pass alone", n)
	}
	stopped := fail
	stopped.Cause = journal.CauseStopped
	uncovered := fail
	uncovered.Uncovered = "x.go"
	for name, l := range map[string]journal.Line{"a stopped run": stopped, "a run that covers nothing": uncovered} {
		if n := run(start, l); len(n) != 0 {
			t.Errorf("%s: notes = %+v, want none", name, n)
		}
	}
	// Only outcomes since the session began count, and with no start nothing is the session's.
	if n := run(start.Add(5*time.Minute), fail, pass); len(n) != 0 {
		t.Errorf("outcomes before the session: %+v", n)
	}
	if n := run(time.Time{}, fail); len(n) != 0 {
		t.Errorf("a session with no start heard %+v", n)
	}
	// Other events say nothing, and so does a context with no ticket.
	c := ruleCtx(t, "s1", []journal.Line{fail}, start)
	c.Event.Kind = KindSessionStart
	if n, _ := verificationNotes(c); n != nil {
		t.Errorf("a session start heard %+v", n)
	}
	c = ruleCtx(t, "s1", []journal.Line{fail}, start)
	c.Where.Ticket = ""
	if n, _ := verificationNotes(c); n != nil {
		t.Errorf("no ticket heard %+v", n)
	}
	// A journal that cannot be read is an error, not a silence.
	c = ruleCtx(t, "s1", nil, start)
	c.Journal = func() ([]journal.Line, error) { return nil, errors.New("boom") }
	if _, err := verificationNotes(c); err == nil {
		t.Error("an unreadable journal is no error")
	}
}

func TestRuleAtAnswerNotes(t *testing.T) {
	t.Parallel()
	start := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	at := func(m int) string { return start.Add(time.Duration(m) * time.Minute).Format(time.RFC3339Nano) }
	q := journal.Line{Event: journal.EventQuestion, Ref: "Q-1", Session: "s1", TS: at(1)}
	a := journal.Line{Event: journal.EventAnswer, Ref: "Q-1", TS: at(2), Summary: "use  the\nfirst", Provenance: "owner"}

	run := func(session string, since time.Time, lines ...journal.Line) []Note {
		c := ruleCtx(t, session, lines, since)
		n, err := answerNotes(c)
		if err != nil {
			t.Fatal(err)
		}
		return n
	}
	n := run("s1", start, q, a)
	if len(n) != 1 || n[0].Ref != "answer:ticket-a:Q-1" || n[0].Text != "Q-1 was answered by the owner: use the first" || n[0].Enforce {
		t.Errorf("an answer = %+v", n)
	}
	noWho := a
	noWho.Provenance = ""
	if n := run("s1", start, q, noWho); len(n) != 1 || n[0].Text != "Q-1 was answered: use the first" {
		t.Errorf("an answer with no provenance = %+v", n)
	}
	if n := run("s2", start, q, a); len(n) != 0 {
		t.Errorf("another session heard %+v", n)
	}
	if n := run("s1", start.Add(3*time.Minute), q, a); len(n) != 0 {
		t.Errorf("an answer from before the session began: %+v", n)
	}
	if n := run("s1", start, a); len(n) != 0 {
		t.Errorf("an answer to a question nobody of the session asked: %+v", n)
	}
	if n := run("s1", start, q); len(n) != 0 {
		t.Errorf("a question still open: %+v", n)
	}
	if n := run("", start, q, a); n != nil {
		t.Errorf("an event with no session: %+v", n)
	}
	long := a
	long.Summary = strings.Repeat("x", 900)
	if n := run("s1", start, q, long); len(n) != 1 || len(n[0].Text) != len("Q-1 was answered by the owner: ")+600 {
		t.Errorf("a long answer is cut to 600 bytes: %+v", n)
	}
}

// TestRuleAtTheCheckoutIsTheTopLevelOfTheCwd: an edit from a subdirectory of a
// checkout is inside it, whichever directory of it the file sits in. The
// repository is the bare minimum discovery accepts.
func TestRuleAtTheCheckoutIsTheTopLevelOfTheCwd(t *testing.T) {
	t.Parallel()
	top := t.TempDir()
	for _, d := range []string{".git/objects", ".git/refs", "pkg"} {
		if err := os.MkdirAll(filepath.Join(top, filepath.FromSlash(d)), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(top, ".git", "HEAD"), []byte("ref: refs/heads/main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	ready := buildPhase(map[string]string{"a": phase.SubPending}, "a")
	h := stubPhaseHandler(filepath.Join(top, "pkg"), "ticket-a", ready, filepath.Join(top, "main.go"))
	if n := h.unclaimedEditNotes(sessionMemory{delivered: map[string]bool{}}); len(n) != 1 {
		t.Errorf("an edit of the top folder from a subdirectory: notes = %+v, want the claim note", n)
	}
}
