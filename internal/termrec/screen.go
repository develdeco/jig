package termrec

import (
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// The terminal model: enough of a VT100/xterm to show what command-line
// programs write (text, line control, colors, erasing, cursor moves, a
// progress line redrawn in place). What it does not model, such as the
// alternate screen, scroll regions, mouse and mode switches, or control
// strings (OSC, DCS, SOS, PM, APC), it reads past without drawing anything.

// color is a cell color: defaultColor, or an RGB value 0xRRGGBB.
type color int32

const defaultColor color = -1

// style is how a cell's text is drawn.
type style struct {
	fg, bg                               color
	bold, dim, italic, underline, invert bool
}

var plain = style{fg: defaultColor, bg: defaultColor}

// cell is one terminal cell. A wide rune takes two cells: the first holds it,
// the second is its continuation (cont) and draws nothing.
type cell struct {
	r    rune   // 0 for a blank cell
	mark string // combining marks drawn with r
	st   style
	wide bool // r takes this cell and the next
	cont bool // the right half of the wide rune before it
}

// maxParams bounds a CSI sequence's parameter bytes; a longer one is read
// to its end and drawn as if cut there.
const maxParams = 64

// maxMarks bounds the combining marks a cell keeps, as xterm keeps a few:
// the rest are dropped.
const maxMarks = 8

// parser states.
const (
	ground = iota
	escape
	escapeSkip // an escape sequence that takes one more byte, such as ESC ( B
	csi
	str       // a control string (OSC, DCS, SOS, PM, APC), read past to its end
	strEscape // ESC inside a control string, the start of its ST terminator
)

type screen struct {
	w, h        int
	rows        [][]cell
	row, col    int
	pendingWrap bool // the last column was written; the next rune wraps first
	st          style
	savedRow    int
	savedCol    int
	savedStyle  style

	state  int
	params []byte // a CSI sequence's parameter and intermediate bytes
	utf    []byte // an incomplete UTF-8 sequence, carried to the next write
}

func newScreen(w, h int) *screen {
	s := &screen{w: w, h: h, st: plain, savedStyle: plain}
	s.rows = make([][]cell, h)
	for i := range s.rows {
		s.rows[i] = s.blankRow()
	}
	return s
}

func (s *screen) blankRow() []cell {
	r := make([]cell, s.w)
	for i := range r {
		r[i] = cell{st: plain}
	}
	return r
}

// blank is an erased cell: erasing keeps the current background, as xterm does.
func (s *screen) blank() cell {
	return cell{st: style{fg: defaultColor, bg: s.st.bg}}
}

// feed writes data to the screen. A sequence or rune split across two writes
// is carried over whole.
func (s *screen) feed(data string) {
	for i := 0; i < len(data); i++ {
		b := data[i]
		switch s.state {
		case ground:
			s.ground(b)
		case escape:
			s.escape(b)
		case escapeSkip:
			s.state = ground
		case csi:
			switch {
			case b == 0x1b:
				s.state = escape
			case b == 0x18 || b == 0x1a: // CAN, SUB: the sequence is cancelled
				s.state = ground
			case b < 0x20:
				s.control(b)
			case b >= 0x40 && b <= 0x7e:
				s.dispatchCSI(b)
				s.state = ground
			case len(s.params) < maxParams:
				s.params = append(s.params, b)
			}
		case str:
			switch b {
			case 0x07, 0x18, 0x1a: // BEL ends an OSC; CAN and SUB cancel any
				s.state = ground
			case 0x1b:
				s.state = strEscape
			}
		case strEscape:
			if b == '\\' {
				s.state = ground
			} else {
				s.state = str
			}
		}
	}
}

func (s *screen) ground(b byte) {
	if len(s.utf) > 0 || b >= 0x80 {
		s.utf = append(s.utf, b)
		if !utf8.FullRune(s.utf) {
			return
		}
		r, size := utf8.DecodeRune(s.utf)
		rest := append([]byte(nil), s.utf[size:]...)
		s.utf = s.utf[:0]
		s.put(r)
		// A byte that ended an invalid sequence starts over on its own.
		for _, c := range rest {
			s.ground(c)
		}
		return
	}
	switch {
	case b == 0x1b:
		s.state = escape
	case b < 0x20 || b == 0x7f:
		s.control(b)
	default:
		s.put(rune(b))
	}
}

func (s *screen) control(b byte) {
	switch b {
	case '\r':
		s.col, s.pendingWrap = 0, false
	case '\n', 0x0b, 0x0c:
		s.lineFeed()
	case '\b':
		if s.col > 0 {
			s.col--
		}
		s.pendingWrap = false
	case '\t':
		s.col = min(s.w-1, (s.col/8+1)*8)
		s.pendingWrap = false
	}
}

func (s *screen) escape(b byte) {
	switch {
	case b == 0x1b: // a new sequence starts over
		return
	case b == 0x18 || b == 0x1a:
		s.state = ground
		return
	case b < 0x20: // a control inside the sequence runs, and the sequence goes on
		s.control(b)
		return
	}
	s.state = ground
	switch b {
	case '[':
		s.state, s.params = csi, s.params[:0]
	case ']', 'P', 'X', '^', '_': // OSC, DCS, SOS, PM, APC: strings to ST
		s.state = str
	case '(', ')', '*', '+', '-', '.', '/', '#', '%', ' ':
		s.state = escapeSkip
	case '7':
		s.savedRow, s.savedCol, s.savedStyle = s.row, s.col, s.st
	case '8':
		s.row, s.col, s.st, s.pendingWrap = s.savedRow, s.savedCol, s.savedStyle, false
	case 'D':
		s.lineFeed()
	case 'E':
		s.col = 0
		s.lineFeed()
	case 'M':
		s.pendingWrap = false
		if s.row == 0 {
			s.scrollDown(1)
		} else {
			s.row--
		}
	case 'c':
		*s = *newScreen(s.w, s.h)
	}
}

func (s *screen) lineFeed() {
	s.pendingWrap = false
	if s.row == s.h-1 {
		s.scrollUp(1)
		return
	}
	s.row++
}

func (s *screen) scrollUp(n int) {
	n = min(n, s.h)
	copy(s.rows, s.rows[n:])
	for i := s.h - n; i < s.h; i++ {
		s.rows[i] = s.blankRow()
	}
}

func (s *screen) scrollDown(n int) {
	n = min(n, s.h)
	copy(s.rows[n:], s.rows[:s.h-n])
	for i := 0; i < n; i++ {
		s.rows[i] = s.blankRow()
	}
}

// put draws r at the cursor and moves it on, wrapping at the right edge.
func (s *screen) put(r rune) {
	if r < 0x20 || (r >= 0x7f && r < 0xa0) {
		return
	}
	if isZeroWidth(r) {
		s.mark(r)
		return
	}
	width := 1
	if isWide(r) {
		width = 2
	}
	if s.pendingWrap || s.col+width > s.w {
		if width > s.w {
			return
		}
		s.col = 0
		s.lineFeed()
	}
	s.set(s.row, s.col, cell{r: r, st: s.st, wide: width == 2})
	if width == 2 {
		s.set(s.row, s.col+1, cell{st: s.st, cont: true})
	}
	if s.col+width >= s.w {
		s.col, s.pendingWrap = s.w-1, true
		return
	}
	s.col += width
}

// mark adds a combining mark to the rune it follows.
func (s *screen) mark(r rune) {
	row, col := s.row, s.col-1
	if s.pendingWrap {
		col = s.col
	}
	if col < 0 {
		return
	}
	if s.rows[row][col].cont && col > 0 {
		col--
	}
	if c := &s.rows[row][col]; c.r != 0 && utf8.RuneCountInString(c.mark) < maxMarks {
		c.mark += string(r)
	}
}

// set writes c at (row, col), clearing the other half of a wide rune it
// overwrites.
func (s *screen) set(row, col int, c cell) {
	line := s.rows[row]
	old := line[col]
	if old.wide && col+1 < s.w {
		line[col+1] = cell{st: old.st}
	}
	if old.cont && col > 0 {
		line[col-1] = cell{st: line[col-1].st}
	}
	line[col] = c
}

// erase blanks the cells [from, to) of a row.
func (s *screen) erase(row, from, to int) {
	from, to = max(from, 0), min(to, s.w)
	for c := from; c < to; c++ {
		s.set(row, c, s.blank())
	}
}

// csiParams reads a CSI sequence's numeric parameters, separated by ';'; a
// missing or unreadable one is -1.
func csiParams(raw string) []int {
	if raw == "" {
		return nil
	}
	return numbers(strings.Split(raw, ";"))
}

func numbers(fields []string) []int {
	out := make([]int, len(fields))
	for i, f := range fields {
		out[i] = number(f)
	}
	return out
}

// number is a parameter's value, capped, or -1 when missing or unreadable.
func number(f string) int {
	n, err := strconv.Atoi(f)
	if err != nil || n < 0 {
		return -1
	}
	return min(n, 1<<16)
}

// arg is parameter i, or def when missing or zero (for counts and positions).
func arg(p []int, i, def int) int {
	if i < len(p) && p[i] > 0 {
		return p[i]
	}
	return def
}

func (s *screen) dispatchCSI(final byte) {
	// A private sequence (ESC [ ? ...) switches modes jig does not model.
	if len(s.params) > 0 && (s.params[0] == '?' || s.params[0] == '>' || s.params[0] == '<' || s.params[0] == '=') {
		return
	}
	for _, b := range s.params {
		if b >= 0x20 && b <= 0x2f { // an intermediate byte: a sequence jig does not model
			return
		}
	}
	if final == 'm' {
		s.sgr(string(s.params))
		return
	}
	p := csiParams(string(s.params))
	s.pendingWrap = false
	switch final {
	case 'A':
		s.row = max(0, s.row-arg(p, 0, 1))
	case 'B', 'e':
		s.row = min(s.h-1, s.row+arg(p, 0, 1))
	case 'C', 'a':
		s.col = min(s.w-1, s.col+arg(p, 0, 1))
	case 'D':
		s.col = max(0, s.col-arg(p, 0, 1))
	case 'E':
		s.row, s.col = min(s.h-1, s.row+arg(p, 0, 1)), 0
	case 'F':
		s.row, s.col = max(0, s.row-arg(p, 0, 1)), 0
	case 'G', '`':
		s.col = min(s.w-1, arg(p, 0, 1)-1)
	case 'd':
		s.row = min(s.h-1, arg(p, 0, 1)-1)
	case 'H', 'f':
		s.row, s.col = min(s.h-1, arg(p, 0, 1)-1), min(s.w-1, arg(p, 1, 1)-1)
	case 'K':
		switch arg(p, 0, 0) {
		case 0:
			s.erase(s.row, s.col, s.w)
		case 1:
			s.erase(s.row, 0, s.col+1)
		case 2:
			s.erase(s.row, 0, s.w)
		}
	case 'J':
		switch arg(p, 0, 0) {
		case 0:
			s.erase(s.row, s.col, s.w)
			for r := s.row + 1; r < s.h; r++ {
				s.erase(r, 0, s.w)
			}
		case 1:
			for r := 0; r < s.row; r++ {
				s.erase(r, 0, s.w)
			}
			s.erase(s.row, 0, s.col+1)
		case 2: // 3 erases only the scrollback, which the model has none of
			for r := 0; r < s.h; r++ {
				s.erase(r, 0, s.w)
			}
		}
	case 'X':
		s.erase(s.row, s.col, s.col+arg(p, 0, 1))
	case 'P':
		n := min(arg(p, 0, 1), s.w-s.col)
		line := s.rows[s.row]
		copy(line[s.col:], line[s.col+n:])
		s.erase(s.row, s.w-n, s.w)
	case '@':
		n := min(arg(p, 0, 1), s.w-s.col)
		line := s.rows[s.row]
		copy(line[s.col+n:], line[s.col:s.w-n])
		s.erase(s.row, s.col, s.col+n)
	case 'L', 'M':
		n := min(arg(p, 0, 1), s.h-s.row)
		region := s.rows[s.row:]
		if final == 'L' {
			copy(region[n:], region[:len(region)-n])
			for i := 0; i < n; i++ {
				region[i] = s.blankRow()
			}
		} else {
			copy(region, region[n:])
			for i := len(region) - n; i < len(region); i++ {
				region[i] = s.blankRow()
			}
		}
		s.col = 0
	case 'S':
		s.scrollUp(arg(p, 0, 1))
	case 'T':
		s.scrollDown(arg(p, 0, 1))
	case 's':
		s.savedRow, s.savedCol = s.row, s.col
	case 'u':
		s.row, s.col = s.savedRow, s.savedCol
	}
}

// sgr applies Select Graphic Rendition parameters to the current style. A
// parameter written with ':' sub-parameters (38:2::r:g:b, 4:3) is one
// group; a color written with ';' (38;5;n, 38;2;r;g;b) takes the groups
// after it.
func (s *screen) sgr(raw string) {
	groups := strings.Split(raw, ";")
	for i := 0; i < len(groups); i++ {
		if strings.Contains(groups[i], ":") {
			sub := numbers(strings.Split(groups[i], ":"))
			switch sub[0] {
			case 38, 48:
				c := defaultColor
				switch {
				case len(sub) >= 3 && sub[1] == 5:
					c, _ = extendedColor(sub[1:])
				case len(sub) >= 6 && sub[1] == 2: // 38:2:<colorspace>:r:g:b
					c, _ = extendedColor([]int{2, sub[3], sub[4], sub[5]})
				case len(sub) == 5 && sub[1] == 2: // 38:2:r:g:b
					c, _ = extendedColor(sub[1:])
				}
				s.setColor(sub[0], c)
			case 4:
				s.st.underline = len(sub) < 2 || sub[1] != 0
			}
			continue
		}
		switch n := number(groups[i]); {
		case n <= 0:
			s.st = plain
		case n == 1:
			s.st.bold = true
		case n == 2:
			s.st.dim = true
		case n == 3:
			s.st.italic = true
		case n == 4, n == 21:
			s.st.underline = true
		case n == 7:
			s.st.invert = true
		case n == 22:
			s.st.bold, s.st.dim = false, false
		case n == 23:
			s.st.italic = false
		case n == 24:
			s.st.underline = false
		case n == 27:
			s.st.invert = false
		case n >= 30 && n <= 37:
			s.st.fg = palette(n - 30)
		case n == 39:
			s.st.fg = defaultColor
		case n >= 40 && n <= 47:
			s.st.bg = palette(n - 40)
		case n == 49:
			s.st.bg = defaultColor
		case n >= 90 && n <= 97:
			s.st.fg = palette(n - 90 + 8)
		case n >= 100 && n <= 107:
			s.st.bg = palette(n - 100 + 8)
		case n == 38 || n == 48 || n == 58: // 58, the underline color, is not drawn
			c, used := extendedColor(numbers(groups[i+1:]))
			i += used
			s.setColor(n, c)
		}
	}
}

// setColor sets the foreground (38) or background (48) to c; a malformed
// color (defaultColor) leaves it as it was, and any other parameter sets
// nothing.
func (s *screen) setColor(which int, c color) {
	switch {
	case c == defaultColor:
	case which == 38:
		s.st.fg = c
	case which == 48:
		s.st.bg = c
	}
}

// extendedColor reads the parameters after 38 or 48: "5;n" for the 256-color
// palette or "2;r;g;b" for RGB. It returns the color (defaultColor when
// malformed) and how many parameters it read.
func extendedColor(p []int) (color, int) {
	if len(p) == 0 {
		return defaultColor, 0
	}
	switch p[0] {
	case 5:
		if len(p) < 2 || p[1] < 0 || p[1] > 255 {
			return defaultColor, min(len(p), 2)
		}
		return palette(p[1]), 2
	case 2:
		if len(p) < 4 {
			return defaultColor, len(p)
		}
		r, g, b := p[1], p[2], p[3]
		if r < 0 || r > 255 || g < 0 || g > 255 || b < 0 || b > 255 {
			return defaultColor, 4
		}
		return color(r<<16 | g<<8 | b), 4
	}
	return defaultColor, 1
}

// ansi16 is the 16-color palette the rendering uses (a dark theme's).
var ansi16 = [16]color{
	0x000000, 0xcd3131, 0x0dbc79, 0xe5e510, 0x2472c8, 0xbc3fbc, 0x11a8cd, 0xe5e5e5,
	0x666666, 0xf14c4c, 0x23d18b, 0xf5f543, 0x3b8eea, 0xd670d6, 0x29b8db, 0xffffff,
}

// palette is xterm's 256-color palette: the 16 colors, a 6x6x6 cube, then 24
// grays.
func palette(n int) color {
	switch {
	case n < 16:
		return ansi16[n]
	case n < 232:
		n -= 16
		level := func(v int) int {
			if v == 0 {
				return 0
			}
			return 55 + v*40
		}
		return color(level(n/36)<<16 | level(n/6%6)<<8 | level(n%6))
	default:
		g := 8 + (n-232)*10
		return color(g<<16 | g<<8 | g)
	}
}

// isZeroWidth reports a rune drawn with the one before it: combining marks,
// variation selectors and the zero-width joiner.
func isZeroWidth(r rune) bool {
	return unicode.Is(unicode.Mn, r) || unicode.Is(unicode.Me, r) ||
		r == 0x200b || r == 0x200c || r == 0x200d || (r >= 0xfe00 && r <= 0xfe0f)
}

// isWide reports a rune a terminal draws two cells wide: East Asian wide and
// fullwidth ranges and emoji.
func isWide(r rune) bool {
	for _, rg := range wideRanges {
		if r >= rg[0] && r <= rg[1] {
			return true
		}
	}
	return false
}

var wideRanges = [][2]rune{
	{0x1100, 0x115f}, {0x231a, 0x231b}, {0x2329, 0x232a}, {0x23e9, 0x23ec}, {0x23f0, 0x23f0}, {0x23f3, 0x23f3},
	{0x25fd, 0x25fe}, {0x2614, 0x2615}, {0x2648, 0x2653}, {0x267f, 0x267f}, {0x2693, 0x2693}, {0x26a1, 0x26a1},
	{0x26aa, 0x26ab}, {0x26bd, 0x26be}, {0x26c4, 0x26c5}, {0x26ce, 0x26ce}, {0x26d4, 0x26d4}, {0x26ea, 0x26ea},
	{0x26f2, 0x26f3}, {0x26f5, 0x26f5}, {0x26fa, 0x26fa}, {0x26fd, 0x26fd}, {0x2705, 0x2705}, {0x270a, 0x270b},
	{0x2728, 0x2728}, {0x274c, 0x274c}, {0x274e, 0x274e}, {0x2753, 0x2755}, {0x2757, 0x2757}, {0x2795, 0x2797},
	{0x27b0, 0x27b0}, {0x27bf, 0x27bf}, {0x2b1b, 0x2b1c}, {0x2b50, 0x2b50}, {0x2b55, 0x2b55},
	{0x2e80, 0x303e}, {0x3041, 0x33ff}, {0x3400, 0x4dbf}, {0x4e00, 0x9fff}, {0xa000, 0xa4cf}, {0xa960, 0xa97f},
	{0xac00, 0xd7a3}, {0xf900, 0xfaff}, {0xfe10, 0xfe19}, {0xfe30, 0xfe6f}, {0xff00, 0xff60}, {0xffe0, 0xffe6},
	{0x16fe0, 0x16fe4}, {0x17000, 0x18cff}, {0x1b000, 0x1b2ff}, {0x1f004, 0x1f004}, {0x1f0cf, 0x1f0cf},
	{0x1f18e, 0x1f18e}, {0x1f191, 0x1f19a}, {0x1f200, 0x1f251}, {0x1f300, 0x1f64f}, {0x1f680, 0x1f6ff},
	{0x1f7e0, 0x1f7eb}, {0x1f90c, 0x1f9ff}, {0x1fa70, 0x1faff}, {0x20000, 0x3fffd},
}

// text is the screen as plain text: trailing blanks of each row and trailing
// empty rows left out.
func (s *screen) text() string {
	lines := make([]string, s.h)
	for i, row := range s.rows {
		var b strings.Builder
		for _, c := range row {
			switch {
			case c.cont:
			case c.r == 0:
				b.WriteByte(' ')
			default:
				b.WriteRune(c.r)
				b.WriteString(c.mark)
			}
		}
		lines[i] = strings.TrimRight(b.String(), " ")
	}
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return strings.Join(lines, "\n")
}
