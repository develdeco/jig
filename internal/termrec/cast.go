// Package termrec turns what a program wrote to a terminal into a recording
// a pull request can show: a Cast (the output, each write stamped with when
// it happened) and its rendering as an animated SVG (ADR 0029).
package termrec

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"strings"
	"time"
)

// Size bounds a Cast's terminal, so a rendering stays a size a pull request
// can carry.
const (
	MaxWidth  = 400
	MaxHeight = 200
)

// Cast is a terminal recording: the terminal's size and the output written to
// it, in order. It is asciicast v2's model, so a recording asciinema made
// reads as one (ReadAsciicast).
type Cast struct {
	Width, Height int
	Events        []Event
}

// Event is one write to the terminal: its text, exactly as the terminal
// received it (escape sequences included, a line ended by "\r\n"), and when
// it happened, measured from the recording's start.
type Event struct {
	Time time.Duration
	Data string
}

// Validate reports why c cannot be rendered: a size outside 1..MaxWidth by
// 1..MaxHeight, or an event earlier than the one before it.
func (c Cast) Validate() error {
	if c.Width < 1 || c.Width > MaxWidth || c.Height < 1 || c.Height > MaxHeight {
		return fmt.Errorf("termrec: terminal size %dx%d, want 1..%d columns by 1..%d rows", c.Width, c.Height, MaxWidth, MaxHeight)
	}
	var last time.Duration
	for i, e := range c.Events {
		if e.Time < last {
			return fmt.Errorf("termrec: event %d at %v comes before the one at %v", i+1, e.Time, last)
		}
		last = e.Time
	}
	return nil
}

// asciicastHeader is the first line of an asciicast v2 file. A recorder's
// other header fields (timestamp, env, theme) are read past.
type asciicastHeader struct {
	Version int `json:"version"`
	Width   int `json:"width"`
	Height  int `json:"height"`
}

// WriteAsciicast writes c as an asciicast v2 file: the header line, then one
// `[seconds, "o", text]` line per event.
func (c Cast) WriteAsciicast(w io.Writer) error {
	if err := c.Validate(); err != nil {
		return err
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(asciicastHeader{Version: 2, Width: c.Width, Height: c.Height}); err != nil {
		return err
	}
	for _, e := range c.Events {
		if err := enc.Encode([]any{json.Number(seconds(e.Time)), "o", e.Data}); err != nil {
			return err
		}
	}
	_, err := w.Write(buf.Bytes())
	return err
}

// seconds spells d as asciicast's seconds, to the microsecond.
func seconds(d time.Duration) string {
	return fmt.Sprintf("%d.%06d", d/time.Second, (d%time.Second)/time.Microsecond)
}

// ReadAsciicast reads an asciicast v2 file. Output events ("o") become the
// Cast's events; input, marker and resize events are read past, since they
// change nothing a rendering shows at the recorded size.
func ReadAsciicast(r io.Reader) (Cast, error) {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, 64*1024), 16*1024*1024)
	if !sc.Scan() {
		if err := sc.Err(); err != nil {
			return Cast{}, fmt.Errorf("termrec: read asciicast header: %w", err)
		}
		return Cast{}, fmt.Errorf("termrec: read asciicast: empty file, want a header line")
	}
	var h asciicastHeader
	if err := json.Unmarshal(sc.Bytes(), &h); err != nil {
		return Cast{}, fmt.Errorf("termrec: read asciicast header: %w", err)
	}
	if h.Version != 2 {
		return Cast{}, fmt.Errorf("termrec: read asciicast: version %d, want 2", h.Version)
	}
	c := Cast{Width: h.Width, Height: h.Height}
	for line := 2; sc.Scan(); line++ {
		text := strings.TrimSpace(sc.Text())
		if text == "" {
			continue
		}
		var ev []json.RawMessage
		if err := json.Unmarshal([]byte(text), &ev); err != nil || len(ev) != 3 {
			return Cast{}, fmt.Errorf("termrec: read asciicast line %d: want [seconds, code, data]", line)
		}
		var at float64
		var code, data string
		if json.Unmarshal(ev[0], &at) != nil || json.Unmarshal(ev[1], &code) != nil || json.Unmarshal(ev[2], &data) != nil ||
			at < 0 || math.IsInf(at, 0) || at > float64(math.MaxInt64/int64(time.Second)) {
			return Cast{}, fmt.Errorf("termrec: read asciicast line %d: want [seconds, code, data]", line)
		}
		if code != "o" {
			continue
		}
		c.Events = append(c.Events, Event{Time: time.Duration(math.Round(at * float64(time.Second))), Data: data})
	}
	if err := sc.Err(); err != nil {
		return Cast{}, fmt.Errorf("termrec: read asciicast: %w", err)
	}
	if err := c.Validate(); err != nil {
		return Cast{}, err
	}
	return c, nil
}

// FinalText is the screen as the recording leaves it: one line per terminal
// row, trailing blanks and trailing empty rows left out. It is what a person
// sees on the last frame, as text.
func (c Cast) FinalText() (string, error) {
	if err := c.Validate(); err != nil {
		return "", err
	}
	s := newScreen(c.Width, c.Height)
	for _, e := range c.Events {
		s.feed(e.Data)
	}
	return s.text(), nil
}
