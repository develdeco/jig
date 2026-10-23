package tui

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/develdeco/jig/internal/events"
)

// liveSource emits its history and then whatever is pushed, until its context
// ends, as events.Follow does.
type liveSource struct {
	history []events.Envelope
	live    chan events.Envelope
	err     error
}

func newLiveSource(history ...events.Envelope) *liveSource {
	return &liveSource{history: history, live: make(chan events.Envelope, 16)}
}

func (s *liveSource) Follow(ctx context.Context, emit func(events.Envelope) error) error {
	if s.err != nil {
		return s.err
	}
	for _, e := range s.history {
		if err := emit(e); err != nil {
			return err
		}
	}
	for {
		select {
		case e := <-s.live:
			if err := emit(e); err != nil {
				return err
			}
		case <-ctx.Done():
			return nil
		}
	}
}

// syncBuffer is a buffer a program writes to while a test reads it.
type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// frames are the screens a plain run printed, in order.
func (b *syncBuffer) frames() []string {
	var out []string
	for _, f := range strings.Split(b.String(), "\n\f\n") {
		if f != "" {
			out = append(out, f)
		}
	}
	return out
}

// until waits for the output to hold want.
func (b *syncBuffer) until(t *testing.T, want string) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if strings.Contains(b.String(), want) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("no screen showed %q within 20s; the last one:\n%s", want, lastFrame(b))
}

func lastFrame(b *syncBuffer) string {
	frames := b.frames()
	if len(frames) == 0 {
		return "(none)"
	}
	return frames[len(frames)-1]
}

// untilRan waits for the runner to have run the command line.
func (f *fakeRunner) untilRan(t *testing.T, line string) {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		for _, l := range f.lines() {
			if l == line {
				return
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("%q was never run; ran %q", line, f.lines())
}

// session is a plain run in the background: keys are written to it, screens
// read from it.
type session struct {
	t    *testing.T
	in   *io.PipeWriter
	out  *syncBuffer
	done chan error
}

func startRun(t *testing.T, cfg Config) *session {
	t.Helper()
	pr, pw := io.Pipe()
	s := &session{t: t, in: pw, out: &syncBuffer{}, done: make(chan error, 1)}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() { cancel(); pw.Close() })
	go func() { s.done <- Run(ctx, cfg, pr, s.out, false) }()
	return s
}

// keys writes keys one at a time, as a person types them.
func (s *session) keys(keys ...string) {
	s.t.Helper()
	for _, k := range keys {
		if _, err := s.in.Write([]byte(k)); err != nil {
			s.t.Fatal(err)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// ended waits for the run to return.
func (s *session) ended() error {
	s.t.Helper()
	select {
	case err := <-s.done:
		return err
	case <-time.After(20 * time.Second):
		s.t.Fatalf("the run did not end; the last screen:\n%s", lastFrame(s.out))
		return nil
	}
}

func plainConfig(src Source, r *fakeRunner) Config {
	return Config{Source: src, Run: r.run, Scope: []string{"--store", "/s"}, Project: "demo", Zone: time.UTC}
}

func TestRunPrintsEachScreenWholeAndTakesKeysFromTheInput(t *testing.T) {
	t.Parallel()
	r := newRunner()
	src := newLiveSource(append(jig1(), jig2()...)...)
	s := startRun(t, plainConfig(src, r))

	s.out.until(t, "published")
	s.keys("\r") // open JIG-1
	s.out.until(t, "needs a person")
	s.keys("g")
	r.untilRan(t, "gate JIG-1 --store /s")
	s.keys("q")
	if err := s.ended(); err != nil {
		t.Fatalf("Run = %v, want nil after q", err)
	}

	frames := s.out.frames()
	if len(frames) < 3 {
		t.Fatalf("printed %d screens, want one for each change", len(frames))
	}
	if first := frames[0]; !strings.Contains(first, "no tickets yet") {
		t.Errorf("the first screen is not the empty project before the stream:\n%s", first)
	}
	for i, f := range frames {
		if !strings.Contains(f, "jig  demo") || !strings.Contains(f, "quit") {
			t.Errorf("screen %d is not a whole screen (no header or no keys):\n%s", i, f)
		}
		if strings.Contains(f, "\x1b") {
			t.Errorf("screen %d has an escape sequence; a pipe gets plain text:\n%q", i, f)
		}
		if i > 0 && f == frames[i-1] {
			t.Errorf("screen %d repeats screen %d", i, i-1)
		}
	}
}

func TestRunShowsWhatIsWrittenAfterItStarted(t *testing.T) {
	t.Parallel()
	r := newRunner()
	src := newLiveSource(jig1()...)
	s := startRun(t, plainConfig(src, r))
	s.out.until(t, "needs input")

	src.live <- jl(at(9, 0), "JIG-7", "a", "claim")
	s.out.until(t, "JIG-7")
	s.keys("q")
	if err := s.ended(); err != nil {
		t.Fatal(err)
	}
}

func TestRunTakesKeysThatArriveTogether(t *testing.T) {
	t.Parallel()
	r := newRunner()
	cfg := plainConfig(newLiveSource(jig1()...), r)
	cfg.Ticket = "JIG-1"
	s := startRun(t, cfg)
	s.out.until(t, "needs a person")

	// The answer prompt, its text and its end, in one write.
	s.keys("aOK\r")
	r.untilRan(t, "answer --as person --store /s JIG-1 q-001 -- OK")
	s.keys("q")
	if err := s.ended(); err != nil {
		t.Fatal(err)
	}
}

func TestRunEndsWithItsContext(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	pr, pw := io.Pipe()
	defer pw.Close()
	out := &syncBuffer{}
	go func() {
		done <- Run(ctx, plainConfig(newLiveSource(jig1()...), newRunner()), pr, out, false)
	}()
	out.until(t, "JIG-1")
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Run = %v after its context ended, want nil", err)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("Run did not end with its context")
	}
}

func TestRunNamesAStreamThatFails(t *testing.T) {
	t.Parallel()
	src := newLiveSource()
	src.err = errors.New("the journal cannot be read")
	s := startRun(t, plainConfig(src, newRunner()))
	s.out.until(t, "the event stream ended: the journal cannot be read")
	s.keys("q")
	if err := s.ended(); err != nil {
		t.Fatal(err)
	}
}

func TestRunWithNoSourceAndNoRunnerIsAnEmptyProject(t *testing.T) {
	t.Parallel()
	pr, pw := io.Pipe()
	defer pw.Close()
	out := &syncBuffer{}
	done := make(chan error, 1)
	go func() { done <- Run(context.Background(), Config{Zone: time.UTC}, pr, out, false) }()
	out.until(t, "no tickets yet")
	pw.Write([]byte("q"))
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("did not end")
	}
}

func TestKeysThatArriveTogetherArePressedOneByOne(t *testing.T) {
	t.Parallel()
	g := newRig(t, "JIG-1")
	g.feed(jig1()...)
	g.send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("aHi")})
	if g.m.input == nil || string(g.m.input.value) != "Hi" {
		t.Fatalf("aHi: input %+v, want the prompt open with Hi typed", g.m.input)
	}
	// A paste is text, whatever it holds.
	g.send(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(" qa"), Paste: true})
	if got := string(g.m.input.value); got != "Hi qa" {
		t.Errorf("after the paste the text is %q, want %q", got, "Hi qa")
	}
}

// TestRunOnAScreenDrawsWithTheRendererAndEndsOnQ: with screen set the
// bubbletea renderer draws (the alternate screen is entered and left) and keys
// are taken from the input; here the output is a pipe, which the renderer
// draws to as well as a terminal.
func TestRunOnAScreenDrawsWithTheRendererAndEndsOnQ(t *testing.T) {
	t.Parallel()
	r := newRunner()
	cfg := plainConfig(newLiveSource(jig1()...), r)
	cfg.Ticket = "JIG-1"
	pr, pw := io.Pipe()
	defer pw.Close()
	out := &syncBuffer{}
	done := make(chan error, 1)
	go func() { done <- Run(context.Background(), cfg, pr, out, true) }()

	out.until(t, "needs a person")
	pw.Write([]byte("g"))
	r.untilRan(t, "gate JIG-1 --store /s")
	pw.Write([]byte("q"))
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run = %v, want nil after q", err)
		}
	case <-time.After(20 * time.Second):
		t.Fatalf("did not end on q; it drew:\n%q", out.String())
	}
	got := out.String()
	for _, seq := range []string{"\x1b[?1049h", "\x1b[?1049l"} {
		if !strings.Contains(got, seq) {
			t.Errorf("the output lacks %q: the alternate screen was not entered and left", seq)
		}
	}
	if strings.Contains(got, "\f") {
		t.Errorf("a screen run printed plain frames")
	}
}
