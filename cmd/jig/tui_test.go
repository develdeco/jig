package main

import (
	"bytes"
	"context"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/develdeco/jig/internal/journal"
)

// tuiOut is the output of a running `jig tui`, read while it runs.
type tuiOut struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (o *tuiOut) Write(p []byte) (int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.buf.Write(p)
}

func (o *tuiOut) String() string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.buf.String()
}

// untilFold waits for the last screen's fold panel titled title to be state.
func (o *tuiOut) untilFold(t *testing.T, title, state string) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if foldState(lastScreen(o.String()), title) == state {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("the fold %s never was %s; the last screen was:\n%s", title, state, lastScreen(o.String()))
}

// until waits for the output to hold want.
func (o *tuiOut) until(t *testing.T, want string) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(o.String(), want) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("the screens never showed %q; they showed:\n%s", want, o.String())
}

// showFolds waits for the project's list to be read and opens its folds, the
// ones of the groups at the end of a queue (not started, then done), so every
// ticket has a row on the screens, and leaves the cursor on the last row.
func showFolds(t *testing.T, keys io.Writer, out *tuiOut) {
	t.Helper()
	// The end of the list is below the first screen: the cursor goes there until
	// the done panel shows.
	deadline := time.Now().Add(20 * time.Second)
	for !strings.Contains(lastScreen(out.String()), "╭─ Done ") {
		if time.Now().After(deadline) {
			t.Fatalf("the screens never showed the done panel; they showed:\n%s", out.String())
		}
		keys.Write([]byte("\x1b[F"))
		time.Sleep(50 * time.Millisecond)
	}
	for _, group := range []string{"not started", "done"} {
		title := strings.ToUpper(group[:1]) + group[1:]
		if foldState(lastScreen(out.String()), title) != "closed" {
			continue
		}
		// The done group's fold row is the last row, and the other's the one
		// before it, so opening it first leaves the done group's where it is.
		keys.Write([]byte("\x1b[F"))
		if group == "not started" {
			keys.Write([]byte("\x1b[A"))
		}
		keys.Write([]byte("\r"))
		out.untilFold(t, title, "open")
	}
	// The list is longer than the screen: the last rows are read with the
	// cursor on the last one, the first ones from the screens before.
	keys.Write([]byte("\x1b[F"))
}

// startTUI runs `jig tui args...` in e in the background on a pipe, as a
// script drives it, and returns the pipe to write keys to, the output and a
// func that waits for the exit code.
func startTUI(t *testing.T, e env, args ...string) (io.WriteCloser, *tuiOut, func() int) {
	t.Helper()
	pr, pw := io.Pipe()
	out := &tuiOut{}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() { cancel(); pw.Close() })
	code := make(chan int, 1)
	go func() { code <- run(e.withContext(ctx), append([]string{"tui"}, args...), out, pr) }()
	return pw, out, func() int {
		t.Helper()
		select {
		case c := <-code:
			return c
		case <-time.After(20 * time.Second):
			t.Fatalf("jig tui did not end; it showed:\n%s", out.String())
			return -1
		}
	}
}

// TestTUIShowsTheStoreAndEndsOnQ: on a pipe the screens are printed whole as
// the stream brings the tickets in, and q ends it with exit 0.
func TestTUIShowsTheStoreAndEndsOnQ(t *testing.T) {
	t.Parallel()
	fx, st, e := workFixture(t)
	T := fx.Ticket
	if err := journal.Append(st, T, journal.Line{Event: journal.EventClaim, Slice: "a", Session: "s-1"}); err != nil {
		t.Fatal(err)
	}

	keys, out, wait := startTUI(t, e)
	out.until(t, T)
	out.until(t, "Build: 0 of 4 green") // the digest's words, from the roster
	if got := out.String(); strings.Contains(got, "\x1b") {
		t.Errorf("a pipe got an escape sequence:\n%q", got)
	}
	keys.Write([]byte("q"))
	if code := wait(); code != 0 {
		t.Fatalf("exit %d, want 0 after q\n%s", code, out.String())
	}
	if frames := strings.Count(out.String(), "\n\f\n"); frames < 2 {
		t.Errorf("printed %d screens, want one at the start and one when the tickets came\n%s", frames, out.String())
	}
}

// TestTUIWithATicketOpensItsScreen: the ticket screen is first, the project's
// list one step back; the ticket may be named by an alias or by the id.
func TestTUIWithATicketOpensItsScreen(t *testing.T) {
	t.Parallel()
	fx, _, e := workFixture(t)
	keys, out, wait := startTUI(t, e, fx.Ticket)
	out.until(t, "› "+fx.Ticket)
	out.until(t, "waiting for your approval") // the status was read
	keys.Write([]byte("\x1b"))                // esc: back to the project
	out.until(t, "1 need you")                // the project's list, grouped by what the person does next
	keys.Write([]byte("q"))
	if code := wait(); code != 0 {
		t.Fatalf("exit %d\n%s", code, out.String())
	}
}

// TestTUIRefusesWhatItCannotOpen: an id that is no ticket's, a key the project
// does not declare, two tickets, an unknown flag and a directory with no store
// are refusals before any screen. (A well-formed id under a declared key is
// waited for: TestTUIWaitsForATicketToBeCreated.)
func TestTUIRefusesWhatItCannotOpen(t *testing.T) {
	t.Parallel()
	fx, _, e := workFixture(t)
	for _, tc := range []struct {
		name string
		e    env
		args []string
		code int
		want string
	}{
		{"a malformed ticket", e, []string{"jig-99"}, 2, "jig-99"},
		{"a ticket no store would mint", e, []string{"JIG-0"}, 2, "JIG-0"},
		{"a ticket under a key the project does not declare", e, []string{"NOPE-1"}, 2, `does not declare key "NOPE"`},
		{"two tickets", e, []string{fx.Ticket, "JIG-2"}, 2, "at most one ticket"},
		{"unknown flag", e, []string{"--nope"}, 2, "--nope"},
		{"a negative merge interval", e, []string{"--merge-every", "-1s"}, 2, "--merge-every must not be negative"},
		{"unknown project", e, []string{"--project", "nope"}, 2, `no project "nope"`},
		{"no store", testEnv(t.TempDir()).inDir(t.TempDir()), nil, 2, "jig store"},
	} {
		out, code := runMain(t, tc.e, "q", append([]string{"tui"}, tc.args...)...)
		if code != tc.code || !strings.Contains(out, tc.want) || strings.Contains(out, "\f") {
			t.Errorf("%s: jig tui %v exit %d, want %d with %q and no screen\n%s", tc.name, tc.args, code, tc.code, tc.want, out)
		}
	}
}

func TestTUIHelpListsItsFlags(t *testing.T) {
	t.Parallel()
	out, code := runMain(t, testEnv(t.TempDir()), "", "tui", "-h")
	if code != 0 || !strings.Contains(out, "--store") || !strings.Contains(out, "--project") || !strings.Contains(out, "--merge-every") {
		t.Errorf("jig tui -h: exit %d\n%s", code, out)
	}
	if help, _ := runMain(t, testEnv(t.TempDir()), "", "-h"); !strings.Contains(help, "tui,") {
		t.Errorf("jig -h does not list tui:\n%s", help)
	}
}

// TestTUIMergeEveryTakesADuration: --merge-every sets how often a screen that
// waits on a merge is read again. A duration opens the screens as usual, and
// text that is no duration is refused before any screen, naming the flag.
func TestTUIMergeEveryTakesADuration(t *testing.T) {
	t.Parallel()
	_, _, e := workFixture(t)
	out, code := runMain(t, e, "q", "tui", "--merge-every", "1h")
	if code != 0 || !strings.Contains(out, "\f") {
		t.Errorf("jig tui --merge-every 1h: exit %d, want 0 with a screen\n%s", code, out)
	}
	out, code = runMain(t, e, "q", "tui", "--merge-every", "soon")
	if code != 2 || !strings.Contains(out, "merge-every") || strings.Contains(out, "\f") {
		t.Errorf("jig tui --merge-every soon: exit %d, want 2 naming the flag and no screen\n%s", code, out)
	}
}

// TestTUIRunnerSpeaksAsAPersonAtTheKeys: the commands the screens run record
// what they did as a person's, because one pressed the key; the same command
// run with a script's stdin records an agent's.
func TestTUIRunnerSpeaksAsAPersonAtTheKeys(t *testing.T) {
	t.Parallel()
	fx, st, e := workFixture(t)
	T := fx.Ticket
	runMain(t, e, "", "ask", T, "--slice", "c", "--session", "s-1", "first?")
	runMain(t, e, "", "ask", T, "--slice", "c", "--session", "s-1", "second?")

	out, code := tuiRunner(e)([]string{"answer", T, "q-001", "Casual."})
	if code != 0 || !strings.Contains(out, "provenance: person\n") {
		t.Fatalf("answer through the screens' runner: exit %d\n%s", code, out)
	}
	out, code = runMain(t, e, "", "answer", T, "q-002", "Formal.")
	if code != 0 || !strings.Contains(out, "provenance: agent\n") {
		t.Fatalf("answer from a script: exit %d\n%s", code, out)
	}
	answers := linesOf(t, st, T, journal.EventAnswer)
	if len(answers) != 2 || answers[0].Provenance != "person" || answers[1].Provenance != "agent" {
		t.Errorf("answer lines = %+v, want a person's and then an agent's", answers)
	}

	// What the runner returns is what the verb printed and how it exited.
	out, code = tuiRunner(e)([]string{"answer", T, "q-001", "Again."})
	if code != 1 || !strings.Contains(out, "QUESTION_NOT_OPEN") {
		t.Errorf("a refused verb: exit %d, want 1 and its code\n%s", code, out)
	}
	if _, code = tuiRunner(e)([]string{"nonsense"}); code != 2 {
		t.Errorf("an unknown verb: exit %d, want 2", code)
	}
}

// TestTUIReadsAndActsOnTheStoreItWasOpenedOn: every command the screens run is
// pinned to the store the verb resolved, so a directory change does not move
// them.
func TestTUIReadsAndActsOnTheStoreItWasOpenedOn(t *testing.T) {
	t.Parallel()
	fx, st, e := workFixture(t)
	runMain(t, e, "", "ask", fx.Ticket, "--slice", "c", "--session", "s-1", "which?")

	// Started from outside every project, with the store named.
	out := t.TempDir()
	keys, screen, wait := startTUI(t, testEnv(fx.Home).inDir(out), "--store", fx.StoreDir, fx.Ticket)
	screen.until(t, "q-001")
	screen.until(t, "a answer") // the key is listed once the roster has read the ticket
	keys.Write([]byte("a"))
	screen.until(t, "answer q-001")
	keys.Write([]byte("Casual\r"))
	screen.until(t, "jig answer --as person "+fx.Ticket+" q-001 -- Casual")
	keys.Write([]byte("q"))
	if code := wait(); code != 0 {
		t.Fatalf("exit %d\n%s", code, screen.String())
	}
	answers := linesOf(t, st, fx.Ticket, journal.EventAnswer)
	if len(answers) != 1 || answers[0].Provenance != "person" || answers[0].Summary != "Casual" || answers[0].Ref != "q-001" {
		t.Errorf("answer lines = %+v, want the person's answer to q-001", answers)
	}
}

// TestTUIWaitsForATicketToBeCreated: `jig tui JIG-2` before the session has
// planned JIG-2 opens its screen saying it waits, with the store untouched, and
// the screen fills in once the ticket is minted, with no key pressed.
func TestTUIWaitsForATicketToBeCreated(t *testing.T) {
	t.Parallel()
	_, _, e := workFixture(t)
	keys, out, wait := startTUI(t, e, "JIG-2")
	out.until(t, "waiting for JIG-2 to be created")
	if got := out.String(); strings.Contains(got, "╭─ Steps") {
		t.Errorf("a ticket that does not exist has steps:\n%s", got)
	}

	// The session plans it: the ticket exists, with no line in its journal yet.
	minted, code := runMain(t, e, "", "ticket", "new", "--title", "Second ticket")
	if code != 0 || !strings.Contains(minted, "id: JIG-2") {
		t.Fatalf("jig ticket new: exit %d, want JIG-2\n%s", code, minted)
	}
	out.until(t, "Second ticket")
	out.until(t, "╭─ Steps")
	out.until(t, "being drafted")
	keys.Write([]byte("q"))
	if code := wait(); code != 0 {
		t.Fatalf("exit %d\n%s", code, out.String())
	}
}

// withSessionEnv returns e whose environment names a harness session by the
// given variables, as a terminal inside a harness session does.
func withSessionEnv(e env, vars map[string]string) env {
	inner := e.getenv
	e.getenv = func(k string) string {
		if v, ok := vars[k]; ok {
			return v
		}
		return inner(k)
	}
	return e
}

// TestTUIRunnerCarriesNoHarnessSession: a person launching the screens from a
// harness session's terminal is not that session, so what the screens run
// journals no session whichever variable names it; a script's run of the same
// verb in that environment still records it, and an explicit --session is the
// person's own word and stays.
func TestTUIRunnerCarriesNoHarnessSession(t *testing.T) {
	t.Parallel()
	fx, st, e := workFixture(t)
	T := fx.Ticket
	for _, q := range []string{"one?", "two?", "three?", "four?", "five?"} {
		runMain(t, e, "", "ask", T, "--slice", "c", "--session", "s-1", q)
	}
	both := map[string]string{"JIG_SESSION": "launch-jig", "CLAUDE_CODE_SESSION_ID": "launch-harness"}

	if out, code := tuiRunner(withSessionEnv(e, both))([]string{"answer", T, "q-001", "Casual."}); code != 0 {
		t.Fatalf("answer through the runner: exit %d\n%s", code, out)
	}
	if out, code := tuiRunner(withSessionEnv(e, map[string]string{"JIG_SESSION": "launch-jig"}))([]string{"answer", T, "q-002", "Casual."}); code != 0 {
		t.Fatalf("answer through the runner: exit %d\n%s", code, out)
	}
	if out, code := tuiRunner(withSessionEnv(e, map[string]string{"CLAUDE_CODE_SESSION_ID": "launch-harness"}))([]string{"answer", T, "q-003", "Casual."}); code != 0 {
		t.Fatalf("answer through the runner: exit %d\n%s", code, out)
	}
	if out, code := tuiRunner(withSessionEnv(e, both))([]string{"ask", T, "--slice", "c", "--session", "typed", "six?"}); code != 0 {
		t.Fatalf("ask with --session through the runner: exit %d\n%s", code, out)
	}
	answers := linesOf(t, st, T, journal.EventAnswer)
	if len(answers) != 3 {
		t.Fatalf("answer lines = %+v, want 3", answers)
	}
	for _, l := range answers {
		if l.Provenance != "person" || l.Session != "" {
			t.Errorf("%s answered through the runner: provenance %q session %q, want the person's and no session", l.Ref, l.Provenance, l.Session)
		}
	}
	asks := linesOf(t, st, T, journal.EventQuestion)
	if got := asks[len(asks)-1].Session; got != "typed" {
		t.Errorf("ask with --session through the runner: session %q, want typed", got)
	}

	// The same environment through run is a script or an agent: its session.
	var buf bytes.Buffer
	if code := run(withSessionEnv(e, both), []string{"answer", T, "q-005", "Scripted."}, &buf, strings.NewReader("")); code != 0 {
		t.Fatalf("answer through run: exit %d\n%s", code, buf.String())
	}
	answers = linesOf(t, st, T, journal.EventAnswer)
	if got := answers[len(answers)-1].Session; got != "launch-jig" {
		t.Errorf("answer through run: session %q, want launch-jig", got)
	}
}

// TestTUIFramesNameNoLaunchingSession: screens opened from a harness session's
// terminal show no id of it after the person answers, and the line the answer
// wrote names none: the frames alone cannot show a session, the line can.
func TestTUIFramesNameNoLaunchingSession(t *testing.T) {
	t.Parallel()
	fx, st, e := workFixture(t)
	runMain(t, e, "", "ask", fx.Ticket, "--slice", "c", "--session", "s-1", "which?")
	e = withSessionEnv(e, map[string]string{"JIG_SESSION": "launch-jig", "CLAUDE_CODE_SESSION_ID": "launch-harness"})

	keys, screen, wait := startTUI(t, e, fx.Ticket)
	screen.until(t, "q-001")
	screen.until(t, "a answer") // the key is listed once the roster has read the ticket
	keys.Write([]byte("a"))
	screen.until(t, "answer q-001")
	keys.Write([]byte("Casual\r"))
	screen.until(t, "jig answer --as person "+fx.Ticket+" q-001 -- Casual")
	keys.Write([]byte("q"))
	if code := wait(); code != 0 {
		t.Fatalf("exit %d\n%s", code, screen.String())
	}
	for _, id := range []string{"launch-jig", "launch-harness"} {
		if strings.Contains(screen.String(), id) {
			t.Errorf("the frames name the launching session %q:\n%s", id, screen.String())
		}
	}
	answers := linesOf(t, st, fx.Ticket, journal.EventAnswer)
	if len(answers) != 1 || answers[0].Session != "" {
		t.Errorf("answer lines = %+v, want the screens' one answer with no session", answers)
	}
}
