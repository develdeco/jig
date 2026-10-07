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

// clock is a test clock that returns the times it is given, in order.
func clock(times ...time.Duration) func() time.Time {
	base := time.Date(2026, 10, 6, 0, 0, 0, 0, time.UTC)
	i := 0
	return func() time.Time {
		t := base.Add(times[min(i, len(times)-1)])
		i++
		return t
	}
}

// TestRecorderStampsEachWriteAndKeepsWhatTheTeeSees: a recorder teed beside
// the buffer a test already reads records each write with its time, while
// the buffer gets the bytes unchanged.
func TestRecorderStampsEachWriteAndKeepsWhatTheTeeSees(t *testing.T) {
	t.Parallel()
	// The first reading is the recording's start.
	rec := newRecorder(80, 24, clock(0, 100*time.Millisecond, 250*time.Millisecond, 250*time.Millisecond))
	var stdout bytes.Buffer
	w := io.MultiWriter(&stdout, rec)
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

// TestRecorderPassesLineFeedsAsATerminalDoes: "\n" becomes "\r\n", as a
// terminal's line discipline passes it on, and a "\r\n" already written,
// even split across two writes, stays as it is.
func TestRecorderPassesLineFeedsAsATerminalDoes(t *testing.T) {
	t.Parallel()
	rec := newRecorder(10, 4, clock(0))
	for _, s := range []string{"a\nb\r\nc", "\r", "\nprogress 1\rprogress 2\n"} {
		rec.Write([]byte(s))
	}
	var got []string
	for _, e := range rec.Cast().Events {
		got = append(got, e.Data)
	}
	want := []string{"a\r\nb\r\nc", "\r", "\nprogress 1\rprogress 2\r\n"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("events = %q, want %q", got, want)
	}
}

// TestRecorderNeverGoesBackInTime: a clock reading earlier than the last one
// (a wall clock stepped back) stamps the write at the last time instead, so
// the recording stays valid.
func TestRecorderNeverGoesBackInTime(t *testing.T) {
	t.Parallel()
	rec := newRecorder(10, 2, clock(0, time.Second, 500*time.Millisecond))
	rec.Write([]byte("a"))
	rec.Write([]byte("b"))
	c := rec.Cast()
	if err := c.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if c.Events[1].Time != time.Second {
		t.Errorf("second write at %v, want %v", c.Events[1].Time, time.Second)
	}
}

// TestRecorderTakesStdoutAndStderrAtOnce: two streams written concurrently
// into one recorder lose no write and keep their time order.
func TestRecorderTakesStdoutAndStderrAtOnce(t *testing.T) {
	t.Parallel()
	rec := NewRecorder(20, 5)
	var wg sync.WaitGroup
	for _, name := range []string{"out", "err"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 500; i++ {
				fmt.Fprintf(rec, "%s %d\n", name, i)
			}
		}()
	}
	wg.Wait()
	c := rec.Cast()
	if len(c.Events) != 1000 {
		t.Fatalf("events = %d, want 1000", len(c.Events))
	}
	if err := c.Validate(); err != nil {
		t.Fatalf("Validate: %v", err)
	}
	all := ""
	for _, e := range c.Events {
		all += e.Data
	}
	for _, want := range []string{"out 499\r\n", "err 499\r\n", "out 0\r\n", "err 0\r\n"} {
		if !strings.Contains(all, want) {
			t.Errorf("the recording lacks %q", want)
		}
	}
}
