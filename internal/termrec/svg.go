package termrec

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// SVGOptions shapes a rendering. The zero value takes the defaults.
type SVGOptions struct {
	// IdleLimit caps a pause between two writes, so a recording that waited
	// on a slow step shows a short still instead of the whole wait.
	// Default 2s.
	IdleLimit time.Duration
	// Hold is how long the last frame stays before the animation starts
	// over. Default 3s.
	Hold time.Duration
}

const (
	defaultIdleLimit = 2 * time.Second
	defaultHold      = 3 * time.Second
	// minFrameInterval merges writes closer together than this into one
	// frame: an eye cannot tell them apart.
	minFrameInterval = 50 * time.Millisecond
	// maxFrames bounds a rendering's size: a longer recording's frames are
	// spaced further apart, so it keeps at most this many (plus the blank
	// first frame).
	maxFrames = 600

	cellWidth  = 8.4 // a monospace cell at 14px: 0.6em
	lineHeight = 18
	baseline   = 14 // the text baseline's offset within a row
	padding    = 10

	themeBG color = 0x1e1e1e
	themeFG color = 0xd4d4d4
)

// frame is the screen from a moment on, until the next frame.
type frame struct {
	at   time.Duration
	body string
}

// SVG renders c as an animated SVG: the terminal's screen as it changed,
// pauses capped at IdleLimit, looping after Hold. A recording whose screen
// never changes renders as a still image.
func (c Cast) SVG(o SVGOptions) ([]byte, error) {
	if err := c.Validate(); err != nil {
		return nil, err
	}
	if o.IdleLimit <= 0 {
		o.IdleLimit = defaultIdleLimit
	}
	if o.Hold <= 0 {
		o.Hold = defaultHold
	}
	frames := c.frames(o.IdleLimit)

	innerW := float64(c.Width) * cellWidth
	innerH := c.Height * lineHeight
	var b strings.Builder
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" width="%s" height="%d" viewBox="0 0 %s %d" role="img">`,
		px(innerW+2*padding), innerH+2*padding, px(innerW+2*padding), innerH+2*padding)
	b.WriteString(`<style>text{font-family:ui-monospace,SFMono-Regular,Menlo,Consolas,"Liberation Mono",monospace;font-size:14px;white-space:pre;fill:` + hex(themeFG) + `}`)
	b.WriteString(`.b{font-weight:700}.i{font-style:italic}.u{text-decoration:underline}.d{opacity:.6}`)
	if len(frames) > 1 {
		total := frames[len(frames)-1].at + o.Hold
		b.WriteString(`@keyframes play{`)
		for i, f := range frames {
			fmt.Fprintf(&b, `%s%%{transform:translateY(-%dpx)}`, strconv.FormatFloat(float64(f.at)/float64(total)*100, 'f', 4, 64), i*innerH)
		}
		fmt.Fprintf(&b, `100%%{transform:translateY(-%dpx)}}`, (len(frames)-1)*innerH)
		fmt.Fprintf(&b, `.play{animation:play %ss steps(1,end) infinite}`, strconv.FormatFloat(total.Seconds(), 'f', 3, 64))
	}
	b.WriteString(`</style>`)
	fmt.Fprintf(&b, `<rect width="100%%" height="100%%" rx="6" fill="%s"/>`, hex(themeBG))
	fmt.Fprintf(&b, `<svg x="%d" y="%d" width="%s" height="%d" overflow="hidden"><g class="play">`, padding, padding, px(innerW), innerH)
	for i, f := range frames {
		fmt.Fprintf(&b, `<g transform="translate(0 %d)">%s</g>`, i*innerH, f.body)
	}
	b.WriteString(`</g></svg></svg>`)
	b.WriteByte('\n')
	return []byte(b.String()), nil
}

// frames plays c's events on a screen and returns what it showed over time:
// a blank first frame, then one frame per change, with pauses capped at
// idle. Writes in the same interval (at least minFrameInterval, wider for
// a long recording so it keeps at most maxFrames) make one frame, timed at
// the interval's start.
func (c Cast) frames(idle time.Duration) []frame {
	times := make([]time.Duration, len(c.Events))
	var at, prev time.Duration
	for i, e := range c.Events {
		at += min(e.Time-prev, idle)
		prev = e.Time
		times[i] = at
	}
	interval := minFrameInterval
	if len(times) > 0 {
		interval = max(interval, times[len(times)-1]/maxFrames+1)
	}

	s := newScreen(c.Width, c.Height)
	frames := []frame{{at: 0, body: s.svg()}}
	for i := 0; i < len(c.Events); {
		slot := times[i] / interval
		for ; i < len(c.Events) && times[i]/interval == slot; i++ {
			s.feed(c.Events[i].Data)
		}
		body := s.svg()
		last := &frames[len(frames)-1]
		switch {
		case body == last.body:
		case last.at == slot*interval:
			last.body = body
		default:
			frames = append(frames, frame{at: slot * interval, body: body})
		}
	}
	return frames
}

// svg draws the screen: per row, a rect for each run of cells with a
// background other than the theme's, and a text element with a tspan for
// each run of text in one style.
func (s *screen) svg() string {
	var b strings.Builder
	for r, row := range s.rows {
		y := r * lineHeight
		// Backgrounds.
		for c := 0; c < s.w; {
			bg := cellColors(row[c].st).bg
			end := c + 1
			for end < s.w && cellColors(row[end].st).bg == bg {
				end++
			}
			if bg != themeBG {
				fmt.Fprintf(&b, `<rect x="%s" y="%d" width="%s" height="%d" fill="%s"/>`,
					px(float64(c)*cellWidth), y, px(float64(end-c)*cellWidth), lineHeight, hex(bg))
			}
			c = end
		}
		// Text, in runs of one style; blank cells inside a run are spaces.
		var text strings.Builder
		for c := 0; c < s.w; {
			if row[c].r == 0 || row[c].cont {
				c++
				continue
			}
			st := textStyle(row[c].st)
			var run strings.Builder
			start, end := c, c
			for ; c < s.w && (row[c].cont || row[c].r == 0 || textStyle(row[c].st) == st); c++ {
				switch {
				case row[c].cont:
				case row[c].r == 0:
					run.WriteByte(' ')
				default:
					run.WriteString(escapeXML(string(row[c].r) + row[c].mark))
					end = c + 1
				}
			}
			// Trailing blanks belong to no run.
			c = end
			out := strings.TrimRight(run.String(), " ")
			fmt.Fprintf(&text, `<tspan x="%s"%s>%s</tspan>`, px(float64(start)*cellWidth), st.attrs(), out)
		}
		if text.Len() > 0 {
			fmt.Fprintf(&b, `<text y="%d" xml:space="preserve">%s</text>`, y+baseline, text.String())
		}
	}
	return b.String()
}

// colors is a cell's foreground and background as drawn, the theme's in
// place of the defaults and swapped when inverted.
type colors struct{ fg, bg color }

func cellColors(st style) colors {
	c := colors{fg: st.fg, bg: st.bg}
	if c.fg == defaultColor {
		c.fg = themeFG
	}
	if c.bg == defaultColor {
		c.bg = themeBG
	}
	if st.invert {
		c.fg, c.bg = c.bg, c.fg
	}
	return c
}

// drawn is what a text run's style decides: its color and its font.
type drawn struct {
	fg                           color
	bold, italic, underline, dim bool
}

func textStyle(st style) drawn {
	return drawn{fg: cellColors(st).fg, bold: st.bold, italic: st.italic, underline: st.underline, dim: st.dim}
}

func (d drawn) attrs() string {
	var classes []string
	for _, c := range []struct {
		on   bool
		name string
	}{{d.bold, "b"}, {d.italic, "i"}, {d.underline, "u"}, {d.dim, "d"}} {
		if c.on {
			classes = append(classes, c.name)
		}
	}
	out := ""
	if d.fg != themeFG {
		out += ` fill="` + hex(d.fg) + `"`
	}
	if len(classes) > 0 {
		out += ` class="` + strings.Join(classes, " ") + `"`
	}
	return out
}

func hex(c color) string { return fmt.Sprintf("#%06x", int32(c)) }

// px spells a length to two decimals at most.
func px(v float64) string { return strconv.FormatFloat(float64(int64(v*100+0.5))/100, 'f', -1, 64) }

// escapeXML escapes text for an SVG text node; a character XML cannot carry
// becomes U+FFFD.
func escapeXML(s string) string {
	var b strings.Builder
	for _, r := range s {
		switch {
		case r == '&':
			b.WriteString("&amp;")
		case r == '<':
			b.WriteString("&lt;")
		case r == '>':
			b.WriteString("&gt;")
		case r < 0x20 || r == 0xfffe || r == 0xffff || (r >= 0xd800 && r <= 0xdfff):
			b.WriteRune(0xfffd)
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}
