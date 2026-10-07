package termrec

import (
	"bytes"
	"fmt"
	"io"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"
)

// clock is a test clock that returns the times it is given, in order, then
// the last one.
func clock(times ...time.Duration) func() time.Time {
	base := time.Date(2026, 10, 6, 0, 0, 0, 0, time.UTC)
	i := 0
	return func() time.Time {
		t := base.Add(times[min(i, len(times)-1)])
		i++
		return t
	}
}

func mustRecorder(t *testing.T, width, height int, now func() time.Time) *Recorder {
	t.Helper()
	rec, err := newRecorder(width, height, now)
	if err != nil {
		t.Fatalf("newRecorder: %v", err)
	}
	return rec
}

func eventData(c Cast) []string {
	var out []string
	for _, e := range c.Events {
		out = append(out, e.Data)
	}
	return out
}

// TestRecorderStampsEachWriteAndKeepsWhatTheTeeSees: a stream teed beside
// the buffer a test already reads records each write with its time, while
// the buffer gets the bytes unchanged.
func TestRecorderStampsEachWriteAndKeepsWhatTheTeeSees(t *testing.T) {
	t.Parallel()
	// The first reading is the recording's start.
	rec := mustRecorder(t, 80, 24, clock(0, 100*time.Millisecond, 250*time.Millisecond))
	var stdout bytes.Buffer
	w := io.MultiWriter(&stdout, rec.Stream())
	for _, s := range []string{"$ jig run T-1\n", "slice a: green\n", ""} {
		if _, err := io.WriteString(w, s); err != nil {
			t.Fatal(err)
		}
	}
	if got := stdout.String(); got != "$ jig run T-1\nslice a: green\n" {
		t.Errorf("the teed buffer got %q, want the bytes as written", got)
	}
	want := Cast{Width: 80, Height: 24, Events: []Event{
		{Time: 100 * time.Millisecond, Data: "$ jig run T-1\r\n"},
		{Time: 250 * time.Millisecond, Data: "slice a: green\r\n"},
	}}
	if got := rec.Cast(); !reflect.DeepEqual(got, want) {
		t.Errorf("Cast = %+v, want %+v (an empty write records nothing)", got, want)
	}
	if text, _ := rec.Cast().FinalText(); text != "$ jig run T-1\nslice a: green" {
		t.Errorf("FinalText = %q: lines must start at the left margin", text)
	}
}

// TestRecorderPassesLineFeedsAsATerminalDoes: every "\n" is passed on as
// "\r\n", as a terminal's line discipline does (a "\r\n" already written
// draws the same as "\r\r\n").
func TestRecorderPassesLineFeedsAsATerminalDoes(t *testing.T) {
	t.Parallel()
	rec := mustRecorder(t, 12, 6, clock(0))
	out := rec.Stream()
	for _, s := range []string{"a\nb\r\nc", "\nprogress 1\rprogress 2\n"} {
		io.WriteString(out, s)
	}
	want := []string{"a\r\nb\r\r\nc", "\r\nprogress 1\rprogress 2\r\n"}
	if got := eventData(rec.Cast()); !reflect.DeepEqual(got, want) {
		t.Errorf("events = %q, want %q", got, want)
	}
	if text, _ := rec.Cast().FinalText(); text != "a\nb\nc\nprogress 2" {
		t.Errorf("FinalText = %q", text)
	}
}

// TestRecorderHoldsBackASequenceAWriteCutShort: a pipe read can end in the
// middle of a rune or an escape sequence; the stream holds that part back
// until its next write finishes it, so another stream's write cannot land
// inside it.
func TestRecorderHoldsBackASequenceAWriteCutShort(t *testing.T) {
	t.Parallel()
	rec := mustRecorder(t, 20, 2, clock(0))
	out, errw := rec.Stream(), rec.Stream()
	io.WriteString(out, "ok \xe6\x97") // a rune cut short
	io.WriteString(errw, "E")
	io.WriteString(out, "\xa5 \x1b[3") // the rune's end, then a CSI cut short
	io.WriteString(errw, "F")
	io.WriteString(out, "1mred\x1b[0m") // the CSI's end, then a whole CSI: nothing held
	io.WriteString(errw, "G")
	io.WriteString(out, "\x1b]0;ti") // a title cut short
	io.WriteString(errw, "H")
	io.WriteString(out, "tle\x07!")
	want := []string{"ok ", "E", "日 ", "F", "\x1b[31mred\x1b[0m", "G", "H", "\x1b]0;title\x07!"}
	if got := eventData(rec.Cast()); !reflect.DeepEqual(got, want) {
		t.Errorf("events = %q, want %q", got, want)
	}
	if text, _ := rec.Cast().FinalText(); text != "ok E日 FredGH!" {
		t.Errorf("FinalText = %q", text)
	}
}

// TestRecorderPassesOnAControlStringLeftOpen: a stream holds back at most
// maxHeld bytes, so an escape sequence that never ends cannot hold the rest
// of the output back.
func TestRecorderPassesOnAControlStringLeftOpen(t *testing.T) {
	t.Parallel()
	rec := mustRecorder(t, 20, 2, clock(0))
	out := rec.Stream()
	io.WriteString(out, "\x1b]0;"+strings.Repeat("x", maxHeld-100))
	if n := len(rec.Cast().Events); n != 0 {
		t.Errorf("events = %d, want the open string held while it is shorter than maxHeld", n)
	}
	io.WriteString(out, strings.Repeat("x", 200))
	if n := len(rec.Cast().Events); n != 1 {
		t.Errorf("events = %d, want the open string passed on once it is longer than maxHeld", n)
	}
}

// TestRecorderTakesStdoutAndStderrAtOnce: two streams written concurrently
// lose no write, and each stream's lines arrive whole and in order.
func TestRecorderTakesStdoutAndStderrAtOnce(t *testing.T) {
	t.Parallel()
	rec, err := NewRecorder(20, 5)
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for _, name := range []string{"out", "err"} {
		w := rec.Stream()
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 500; i++ {
				fmt.Fprintf(w, "%s %d\n", name, i)
			}
		}()
	}
	wg.Wait()
	c := rec.Cast()
	if err := c.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	next := map[string]int{}
	for _, e := range c.Events {
		var name string
		var i int
		if _, err := fmt.Sscanf(e.Data, "%s %d\r\n", &name, &i); err != nil || e.Data != fmt.Sprintf("%s %d\r\n", name, i) {
			t.Fatalf("event %q is not one whole line", e.Data)
		}
		if i != next[name] {
			t.Fatalf("%s line %d came where line %d was due", name, i, next[name])
		}
		next[name]++
	}
	if next["out"] != 500 || next["err"] != 500 {
		t.Errorf("lines = %v, want 500 of each", next)
	}
}

// TestNewRecorderRefusesASizeItCannotRender: the size is checked when the
// recording starts, not when it is rendered.
func TestNewRecorderRefusesASizeItCannotRender(t *testing.T) {
	t.Parallel()
	for _, size := range [][2]int{{0, 24}, {80, 0}, {MaxWidth + 1, 24}, {80, MaxHeight + 1}} {
		if _, err := NewRecorder(size[0], size[1]); err == nil {
			t.Errorf("NewRecorder(%d, %d) succeeded", size[0], size[1])
		}
	}
}
