package termrec

import (
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
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
	// minFrameInterval is the shortest slot of time one frame stands for:
	// writes in the same slot make one frame, as an eye cannot tell them
	// apart.
	minFrameInterval = 50 * time.Millisecond
	// maxFrames bounds a rendering's frames: a longer recording gets wider
	// slots, so it keeps at most this many frames in all.
	maxFrames = 600
	// MaxSVGBytes bounds a rendering's size, under the 10 MB GitHub takes for
	// an attached image: a rendering past it gets wider slots, so fewer
	// frames, until it fits.
	MaxSVGBytes = 8 << 20

	cellWidth  = 8.4 // a monospace cell at 14px: 0.6em
	lineHeight = 18
	baseline   = 14 // the text baseline's offset within a row
	padding    = 10

	themeBG color = 0x1e1e1e
	themeFG color = 0xd4d4d4
)

// frame is the screen from a moment on, until the next frame: the rows it
// shows, each by its id among the rendering's distinct rows.
type frame struct {
	at   time.Duration
	rows []rowUse
}

// rowUse is one distinct row drawn at a row position.
type rowUse struct{ id, row int }

func (f frame) same(g frame) bool {
	if len(f.rows) != len(g.rows) {
		return false
	}
	for i := range f.rows {
		if f.rows[i] != g.rows[i] {
			return false
		}
	}
	return true
}

// SVG renders c as an animated SVG: the terminal's screen as it changed,
// pauses capped at IdleLimit, looping after Hold. A recording whose screen
// never changes renders as a still image. Each distinct row is drawn once
// and placed in every frame that shows it, and every run of text is pinned
// to its cells, so the columns line up in any monospace font.
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
	times := make([]time.Duration, len(c.Events))
	var at, prev time.Duration
	for i, e := range c.Events {
		at += min(e.Time-prev, o.IdleLimit)
		prev = e.Time
		times[i] = at
	}
	interval := minFrameInterval
	if len(times) > 0 {
		interval = max(interval, times[len(times)-1]/maxFrames+1)
	}
	for {
		frames, rows := c.frames(times, interval)
		out := render(c.Width, c.Height, frames, rows, o.Hold)
		if len(out) <= MaxSVGBytes {
			return out, nil
		}
		if len(frames) == 1 {
			return nil, fmt.Errorf("termrec: the last screen alone renders to %d bytes, over the %d-byte budget", len(out), MaxSVGBytes)
		}
		interval *= 2
	}
}

// frames plays c's events on a screen and returns what it showed over time
// (a blank first frame, then one frame per visible change) and the distinct
// rows those frames draw. The events in one slot of interval make one frame,
// timed at the slot's start; times are the events' times, pauses capped.
func (c Cast) frames(times []time.Duration, interval time.Duration) ([]frame, []string) {
	ids := map[string]int{}
	var rows []string
	snapshot := func(s *screen, at time.Duration) frame {
		f := frame{at: at}
		for r, line := range s.rows {
			body := rowSVG(line)
			if body == "" {
				continue
			}
			id, ok := ids[body]
			if !ok {
				id = len(rows)
				ids[body] = id
				rows = append(rows, body)
			}
			f.rows = append(f.rows, rowUse{id: id, row: r})
		}
		return f
	}

	s := newScreen(c.Width, c.Height)
	frames := []frame{snapshot(s, 0)}
	for i := 0; i < len(c.Events); {
		slot := times[i] / interval
		for ; i < len(c.Events) && times[i]/interval == slot; i++ {
			s.feed(c.Events[i].Data)
		}
		f := snapshot(s, slot*interval)
		last := &frames[len(frames)-1]
		switch {
		case f.same(*last):
		case last.at == f.at:
			*last = f
		default:
			frames = append(frames, f)
		}
	}
	return frames, rows
}

// render writes the SVG: the distinct rows the frames use as definitions,
// then the frames stacked one screen apart, stepped through by a CSS
// animation when there is more than one.
func render(width, height int, frames []frame, rows []string, hold time.Duration) []byte {
	innerW := float64(width) * cellWidth
	innerH := height * lineHeight
	var b strings.Builder
	fmt.Fprintf(&b, `<svg xmlns="http://www.w3.org/2000/svg" width="%s" height="%d" viewBox="0 0 %s %d" role="img">`,
		px(innerW+2*padding), innerH+2*padding, px(innerW+2*padding), innerH+2*padding)
	b.WriteString(`<style>text{font-family:ui-monospace,SFMono-Regular,Menlo,Consolas,"Liberation Mono",monospace;font-size:14px;white-space:pre;fill:` + hex(themeFG) + `}`)
	b.WriteString(`.b{font-weight:700}.i{font-style:italic}.u{text-decoration:underline}.d{opacity:.6}`)
	if len(frames) > 1 {
		total := frames[len(frames)-1].at + hold
		b.WriteString(`@keyframes play{`)
		for i, f := range frames {
			fmt.Fprintf(&b, `%s%%{transform:translateY(-%dpx)}`, strconv.FormatFloat(float64(f.at)/float64(total)*100, 'f', 4, 64), i*innerH)
		}
		fmt.Fprintf(&b, `100%%{transform:translateY(-%dpx)}}`, (len(frames)-1)*innerH)
		fmt.Fprintf(&b, `.play{animation:play %ss steps(1,end) infinite}`, strconv.FormatFloat(total.Seconds(), 'f', 3, 64))
	}
	b.WriteString(`</style>`)

	// Only the rows a kept frame shows: a frame merged away may have drawn
	// rows no other frame does.
	used := make([]bool, len(rows))
	for _, f := range frames {
		for _, u := range f.rows {
			used[u.id] = true
		}
	}
	b.WriteString(`<defs>`)
	for id, body := range rows {
		if used[id] {
			fmt.Fprintf(&b, `<g id="r%d">%s</g>`, id, body)
		}
	}
	b.WriteString(`</defs>`)

	fmt.Fprintf(&b, `<rect width="100%%" height="100%%" rx="6" fill="%s"/>`, hex(themeBG))
	fmt.Fprintf(&b, `<svg x="%d" y="%d" width="%s" height="%d" overflow="hidden"><g class="play">`, padding, padding, px(innerW), innerH)
	for i, f := range frames {
		fmt.Fprintf(&b, `<g transform="translate(0 %d)">`, i*innerH)
		for _, u := range f.rows {
			fmt.Fprintf(&b, `<use href="#r%d" y="%d"/>`, u.id, u.row*lineHeight)
		}
		b.WriteString(`</g>`)
	}
	b.WriteString(`</g></svg></svg>`)
	b.WriteByte('\n')
	return []byte(b.String())
}

// rowSVG draws one row at the top of its own space: a rect for each run of
// cells with a background other than the theme's, and a text element with a
// tspan for each run of text. A run of plain narrow characters in one style
// is pinned to its cells with textLength; a wide rune, a rune with combining
// marks, or one outside the Basic Multilingual Plane is a run of its own,
// placed at its cell. It is "" for a row with nothing to draw.
func rowSVG(row []cell) string {
	var b strings.Builder
	for c := 0; c < len(row); {
		bg := cellColors(row[c].st).bg
		end := c + 1
		for end < len(row) && cellColors(row[end].st).bg == bg {
			end++
		}
		if bg != themeBG {
			fmt.Fprintf(&b, `<rect x="%s" y="0" width="%s" height="%d" fill="%s"/>`,
				px(float64(c)*cellWidth), px(float64(end-c)*cellWidth), lineHeight, hex(bg))
		}
		c = end
	}

	var text strings.Builder
	for c := 0; c < len(row); {
		if row[c].r == 0 || row[c].cont {
			c++
			continue
		}
		st := textStyle(row[c].st)
		if alone(row[c]) {
			writeRun(&text, c, 0, escapeXML(string(row[c].r)+row[c].mark), st)
			c++
			continue
		}
		// A run: cells of this style, blank cells (as spaces) between them,
		// up to the next cell that must stand alone.
		// It ends at its last cell that draws something: a written space
		// draws only when underlined, a blank cell never.
		start := c
		var run []rune
		drawsTo := 0
		for ; c < len(row) && !row[c].cont && !alone(row[c]) && (row[c].r == 0 || textStyle(row[c].st) == st); c++ {
			if row[c].r == 0 {
				run = append(run, ' ')
				continue
			}
			run = append(run, row[c].r)
			if row[c].r != ' ' || st.underline {
				drawsTo = len(run)
			}
		}
		if drawsTo == 0 {
			continue
		}
		writeRun(&text, start, drawsTo, escapeXML(string(run[:drawsTo])), st)
	}
	if text.Len() > 0 {
		fmt.Fprintf(&b, `<text y="%d" xml:space="preserve">%s</text>`, baseline, text.String())
	}
	return b.String()
}

// alone reports a cell drawn as a run of its own: a glyph whose advance is
// not one cell, or that SVG may count as more than one character.
func alone(c cell) bool {
	return c.wide || c.mark != "" || c.r > 0xffff || c.r == utf8.RuneError
}

// writeRun writes one tspan at cell col; cells > 1 pins its width to that
// many cells.
func writeRun(b *strings.Builder, col, cells int, text string, st drawn) {
	fmt.Fprintf(b, `<tspan x="%s"`, px(float64(col)*cellWidth))
	if cells > 1 {
		fmt.Fprintf(b, ` textLength="%s"`, px(float64(cells)*cellWidth))
	}
	fmt.Fprintf(b, `%s>%s</tspan>`, st.attrs(), text)
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
