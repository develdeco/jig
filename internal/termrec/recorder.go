package termrec

import (
	"sync"
	"time"
)

// Recorder records a Cast from the writes a program makes to the pipes it
// already writes to, for a program whose output does not change on a
// terminal (ADR 0029): the caller tees each stream into it, for example
// io.MultiWriter(&stdout, rec). Each write becomes an event stamped with
// when it arrived. A line feed is passed on as a terminal's line discipline
// passes it (onlcr): "\n" becomes "\r\n", so lines start at the left margin
// as they do on a terminal. It is safe for concurrent writes, so one
// Recorder can take both stdout and stderr.
type Recorder struct {
	mu            sync.Mutex
	width, height int
	start         time.Time
	now           func() time.Time
	events        []Event
	lastCR        bool // the last byte recorded was "\r"
}

// NewRecorder starts a recording of a width x height terminal; time is
// measured from now.
func NewRecorder(width, height int) *Recorder {
	return newRecorder(width, height, time.Now)
}

func newRecorder(width, height int, now func() time.Time) *Recorder {
	return &Recorder{width: width, height: height, start: now(), now: now}
}

// Write records p as one write. It never fails.
func (r *Recorder) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	data := make([]byte, 0, len(p)+8)
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, b := range p {
		if b == '\n' && !r.lastCR {
			data = append(data, '\r')
		}
		data = append(data, b)
		r.lastCR = b == '\r'
	}
	at := r.now().Sub(r.start)
	if n := len(r.events); n > 0 && at < r.events[n-1].Time {
		at = r.events[n-1].Time
	}
	r.events = append(r.events, Event{Time: at, Data: string(data)})
	return len(p), nil
}

// Cast is the recording so far.
func (r *Recorder) Cast() Cast {
	r.mu.Lock()
	defer r.mu.Unlock()
	return Cast{Width: r.width, Height: r.height, Events: append([]Event(nil), r.events...)}
}
