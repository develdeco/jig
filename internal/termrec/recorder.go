package termrec

import (
	"io"
	"sync"
	"time"
	"unicode/utf8"
)

// Recorder records a Cast from the writes a program makes to the pipes it
// already writes to, for a program whose output does not change on a
// terminal (ADR 0029). The caller tees each stream into a writer of its own,
// for example io.MultiWriter(&stdout, rec.Stream()), and the streams share
// one recording. Each write becomes an event stamped with when it arrived,
// with its line feeds passed on as a terminal's line discipline passes them
// (onlcr: "\n" as "\r\n"), so lines start at the left margin as they do on a
// terminal.
type Recorder struct {
	mu            sync.Mutex
	width, height int
	start         time.Time
	now           func() time.Time
	events        []Event
}

// maxHeld bounds what a stream holds back waiting for the rest of a
// sequence: it looks back only this far for an unfinished one, so a control
// string left open longer than this is passed on as it is.
const maxHeld = 4096

// NewRecorder starts a recording of a width x height terminal; time is
// measured from now. The size must be one Cast.Validate accepts.
func NewRecorder(width, height int) (*Recorder, error) {
	return newRecorder(width, height, time.Now)
}

func newRecorder(width, height int, now func() time.Time) (*Recorder, error) {
	if err := (Cast{Width: width, Height: height}).Validate(); err != nil {
		return nil, err
	}
	return &Recorder{width: width, height: height, start: now(), now: now}, nil
}

// Stream returns a writer for one of the program's streams, such as its
// stdout. Writes to different streams may come concurrently. A stream holds
// back a write's trailing part that ends in the middle of a rune or an
// escape sequence until its next write completes it, so another stream's
// write never lands inside one; what is still held back when the recording
// is read draws nothing on a terminal either.
func (r *Recorder) Stream() io.Writer { return &stream{r: r} }

type stream struct {
	r    *Recorder
	held []byte
}

// Write records p. It never fails.
func (s *stream) Write(p []byte) (int, error) {
	s.r.mu.Lock()
	defer s.r.mu.Unlock()
	data := append(s.held, p...)
	whole := wholeUpTo(data)
	s.held = append([]byte(nil), data[whole:]...)
	s.r.record(data[:whole])
	return len(p), nil
}

// record appends data as one event; the caller holds r.mu.
func (r *Recorder) record(data []byte) {
	if len(data) == 0 {
		return
	}
	out := make([]byte, 0, len(data)+8)
	for _, b := range data {
		if b == '\n' {
			out = append(out, '\r')
		}
		out = append(out, b)
	}
	r.events = append(r.events, Event{Time: r.now().Sub(r.start), Data: string(out)})
}

// Cast is the recording so far.
func (r *Recorder) Cast() Cast {
	r.mu.Lock()
	defer r.mu.Unlock()
	return Cast{Width: r.width, Height: r.height, Events: append([]Event(nil), r.events...)}
}

// wholeUpTo is how much of data ends on a whole rune and outside an escape
// sequence: what follows is the start of a rune or a sequence the next
// write finishes.
func wholeUpTo(data []byte) int {
	whole := len(data)
	// A rune cut short.
	for i := len(data) - 1; i >= 0 && i >= len(data)-utf8.UTFMax; i-- {
		if utf8.RuneStart(data[i]) {
			if !utf8.FullRune(data[i:]) {
				whole = i
			}
			break
		}
	}
	// An escape sequence cut short: the last ESC, if what follows it does
	// not end the sequence it starts.
	for i := len(data) - 1; i >= 0 && i >= len(data)-maxHeld; i-- {
		if data[i] != 0x1b {
			continue
		}
		if !sequenceEnds(data[i+1:]) {
			whole = min(whole, i)
		}
		break
	}
	return whole
}

// sequenceEnds reports whether rest, the bytes after an ESC, hold the end of
// the sequence that ESC starts.
func sequenceEnds(rest []byte) bool {
	if len(rest) == 0 {
		return false
	}
	switch rest[0] {
	case '[': // CSI: parameters, then a final byte
		for _, b := range rest[1:] {
			if b >= 0x40 && b <= 0x7e {
				return true
			}
		}
		return false
	case ']', 'P', 'X', '^', '_': // a control string, ended by BEL or ST (ESC \)
		for _, b := range rest[1:] {
			if b == 0x07 {
				return true
			}
		}
		return false // an ST's ESC is the last ESC, and is read on its own
	case '(', ')', '*', '+', '-', '.', '/', '#', '%', ' ': // one more byte
		return len(rest) >= 2
	}
	return true
}
