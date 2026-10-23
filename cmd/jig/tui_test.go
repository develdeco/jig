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
	out.until(t, "building")
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
	out.until(t, "slices green") // the status was read
	keys.Write([]byte("\x1b"))   // esc: back to the project
	out.until(t, "1 ticket")
	keys.Write([]byte("q"))
	if code := wait(); code != 0 {
		t.Fatalf("exit %d\n%s", code, out.String())
	}
}

// TestTUIRefusesWhatItCannotOpen: a ticket the store has not, two tickets, an
// unknown flag and a directory with no store are refusals before any screen.
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
		{"unknown ticket", e, []string{"JIG-99"}, 2, "JIG-99"},
		{"two tickets", e, []string{fx.Ticket, "JIG-2"}, 2, "at most one ticket"},
		{"unknown flag", e, []string{"--nope"}, 2, "--nope"},
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
	if code != 0 || !strings.Contains(out, "--store") || !strings.Contains(out, "--project") {
		t.Errorf("jig tui -h: exit %d\n%s", code, out)
	}
	if help, _ := runMain(t, testEnv(t.TempDir()), "", "-h"); !strings.Contains(help, "tui,") {
		t.Errorf("jig -h does not list tui:\n%s", help)
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
