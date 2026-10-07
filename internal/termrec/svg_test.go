package termrec

import (
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"
)

// wellFormed reports whether svg parses as XML with an svg root.
func wellFormed(svg []byte) error {
	dec := xml.NewDecoder(strings.NewReader(string(svg)))
	root := ""
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		if se, ok := tok.(xml.StartElement); ok && root == "" {
			root = se.Name.Local
		}
	}
	if root != "svg" {
		return fmt.Errorf("root element %q, want svg", root)
	}
	return nil
}

// svgOf renders events on a width x height terminal with default options and
// checks the result is well-formed.
func svgOf(t *testing.T, width, height int, events ...Event) string {
	t.Helper()
	out, err := Cast{Width: width, Height: height, Events: events}.SVG(SVGOptions{})
	if err != nil {
		t.Fatalf("SVG: %v", err)
	}
	if err := wellFormed(out); err != nil {
		t.Fatalf("the SVG is not well-formed: %v\n%s", err, out)
	}
	return string(out)
}

// frameCount counts the frames an SVG stacks for its animation.
func frameCount(svg string) int { return strings.Count(svg, `<g transform="translate(0 `) }

// TestSVGAnimatesTheScreenOverTime: one frame per change, writes in the same
// slot merged, a long pause capped at the idle limit, the last frame held
// before the loop starts over.
func TestSVGAnimatesTheScreenOverTime(t *testing.T) {
	t.Parallel()
	svg := svgOf(t, 10, 2,
		Event{Time: 0, Data: "a"},
		Event{Time: time.Second, Data: "b"},
		Event{Time: time.Second + 10*time.Millisecond, Data: "c"}, // the same slot as "b"
		Event{Time: 10 * time.Second, Data: "d"},                  // after an 8.99 s wait, capped at 2 s
	)
	if n := frameCount(svg); n != 3 {
		t.Errorf("frames = %d, want 3 (a, abc, abcd)", n)
	}
	// Frames at 0 s, 1 s and 3 s, held 3 s: a 6 s loop.
	for _, want := range []string{
		`.play{animation:play 6.000s steps(1,end) infinite}`,
		`0.0000%{transform:translateY(-0px)}`,
		`16.6667%{transform:translateY(-36px)}`,
		`50.0000%{transform:translateY(-72px)}`,
		`100%{transform:translateY(-72px)}`,
		`>abc</tspan>`,
		`>abcd</tspan>`,
	} {
		if !strings.Contains(svg, want) {
			t.Errorf("the SVG lacks %s:\n%s", want, svg)
		}
	}
	if strings.Contains(svg, `>ab</tspan>`) {
		t.Errorf("writes 10 ms apart became two frames:\n%s", svg)
	}
}

// TestSVGStartsBlankUntilTheFirstWrite: output that starts after a pause
// shows the empty terminal first.
func TestSVGStartsBlankUntilTheFirstWrite(t *testing.T) {
	t.Parallel()
	svg := svgOf(t, 10, 2, Event{Time: time.Second, Data: "late"})
	if n := frameCount(svg); n != 2 {
		t.Errorf("frames = %d, want 2 (blank, late)", n)
	}
	if !strings.Contains(svg, `<g transform="translate(0 0)"></g>`) {
		t.Errorf("the first frame is not blank:\n%s", svg)
	}
}

// TestSVGOfAnUnchangingScreenIsStill: a recording whose screen never changes
// (or that wrote nothing) renders as one frame with no animation.
func TestSVGOfAnUnchangingScreenIsStill(t *testing.T) {
	t.Parallel()
	for name, events := range map[string][]Event{
		"nothing written":        nil,
		"only mode switches":     {{Data: "\x1b[?25l"}, {Time: time.Second, Data: "\x1b[?25h"}},
		"one write at the start": {{Data: "done"}},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			svg := svgOf(t, 10, 2, events...)
			if n := frameCount(svg); n != 1 || strings.Contains(svg, "@keyframes") {
				t.Errorf("frames = %d (animated: %v), want one still frame:\n%s", n, strings.Contains(svg, "@keyframes"), svg)
			}
		})
	}
}

// TestSVGKeepsALongRecordingBounded: a recording with thousands of changes
// keeps at most maxFrames frames, and still ends on the last write.
func TestSVGKeepsALongRecordingBounded(t *testing.T) {
	t.Parallel()
	var events []Event
	for i := 0; i < 5000; i++ {
		events = append(events, Event{Time: time.Duration(i) * 100 * time.Millisecond, Data: fmt.Sprintf("\r%d", i)})
	}
	svg := svgOf(t, 10, 2, events...)
	if n := frameCount(svg); n > maxFrames || n < maxFrames/2 {
		t.Errorf("frames = %d, want between %d and %d", n, maxFrames/2, maxFrames)
	}
	if !strings.Contains(svg, `>4999</tspan>`) {
		t.Errorf("the last frame does not show the last write")
	}
}

// TestSVGKeepsAHeavyRecordingUnderTheByteBudget: a full-screen, every-cell-
// colored redraw ten times a second would take tens of megabytes at
// maxFrames; it keeps fewer frames instead and stays under MaxSVGBytes.
func TestSVGKeepsAHeavyRecordingUnderTheByteBudget(t *testing.T) {
	t.Parallel()
	const w, h = 80, 24
	var events []Event
	seed := uint32(1)
	next := func(n uint32) uint32 { // a fixed pseudo-random sequence, so no two rows repeat
		seed ^= seed << 13
		seed ^= seed >> 17
		seed ^= seed << 5
		return seed % n
	}
	for i := 0; i < 600; i++ {
		var b strings.Builder
		b.WriteString("\x1b[H")
		for r := 0; r < h; r++ {
			for c := 0; c < w; c++ {
				fmt.Fprintf(&b, "\x1b[3%dm%c", 1+next(7), 'A'+next(26))
			}
		}
		events = append(events, Event{Time: time.Duration(i) * 100 * time.Millisecond, Data: b.String()})
	}
	out, err := Cast{Width: w, Height: h, Events: events}.SVG(SVGOptions{})
	if err != nil {
		t.Fatalf("SVG: %v", err)
	}
	if len(out) > MaxSVGBytes {
		t.Errorf("SVG = %d bytes, over the %d-byte budget", len(out), MaxSVGBytes)
	}
	if n := frameCount(string(out)); n < 2 || n >= maxFrames {
		t.Errorf("frames = %d, want fewer than %d but still an animation", n, maxFrames)
	}
}

// TestSVGDrawsEachDistinctRowOnce: a line that scrolls up the screen is one
// definition, placed wherever and whenever it shows.
func TestSVGDrawsEachDistinctRowOnce(t *testing.T) {
	t.Parallel()
	var events []Event
	for i := 0; i < 10; i++ {
		events = append(events, Event{Time: time.Duration(i) * 100 * time.Millisecond, Data: "line\r\n"})
	}
	svg := svgOf(t, 10, 3, events...)
	if n := strings.Count(svg, `<g id="r`); n != 1 {
		t.Errorf("row definitions = %d, want 1:\n%s", n, svg)
	}
	if n := strings.Count(svg, `<use href="#r0"`); n < 3 {
		t.Errorf("the row is placed %d times, want once per frame and row it shows on:\n%s", n, svg)
	}
}

// TestSVGPinsTextToTheCellGrid: a run of text spans exactly its cells, and a
// wide rune or a rune with marks stands at its own cell, so columns line up
// whatever the font's advance.
func TestSVGPinsTextToTheCellGrid(t *testing.T) {
	t.Parallel()
	svg := svgOf(t, 20, 2, Event{Data: "abc\u65e5e\u0301f"})
	for _, want := range []string{
		`<tspan x="0" textLength="25.2">abc</tspan>`,
		"<tspan x=\"25.2\">\u65e5</tspan>", // cells 3-4
		"<tspan x=\"42\">e\u0301</tspan>",  // cell 5
		`<tspan x="50.4">f</tspan>`,        // cell 6, one cell: no textLength
	} {
		if !strings.Contains(svg, want) {
			t.Errorf("the SVG lacks %s:\n%s", want, svg)
		}
	}
}

// TestSVGDrawsStylesAndEscapesText: colors as fills and background rects,
// fonts as classes, inverse video swapped, and text escaped for XML.
func TestSVGDrawsStylesAndEscapesText(t *testing.T) {
	t.Parallel()
	svg := svgOf(t, 40, 2, Event{Data: "<b>&\x1b[31mR\x1b[42mG\x1b[0m\x1b[1mB\x1b[0m\x1b[38;5;196mX\x1b[38;2;1;2;3mY\x1b[0m\x1b[7mI"})
	for _, want := range []string{
		`>&lt;b&gt;&amp;</tspan>`,      // escaped, in the theme's color (no fill)
		`fill="#cd3131">RG</tspan>`,    // SGR 31, kept over the green background
		`fill="#0dbc79"/>`,             // SGR 42, a background rect
		`class="b">B</tspan>`,          // bold
		`fill="#ff0000">X</tspan>`,     // 256-color 196
		`fill="#010203">Y</tspan>`,     // RGB
		`fill="#1e1e1e">I</tspan>`,     // inverse: the theme's background as text
		`height="18" fill="#d4d4d4"/>`, // inverse: the theme's text color as background
		`<svg xmlns="http://www.w3.org/2000/svg" width="356" height="56" viewBox="0 0 356 56"`, // 40 x 8.4 + 20, 2 x 18 + 20
	} {
		if !strings.Contains(svg, want) {
			t.Errorf("the SVG lacks %s:\n%s", want, svg)
		}
	}
}

// TestSVGReadsEveryColorForm: SGR colors in each spelling programs use, and
// a saved cursor's style restored with it.
func TestSVGReadsEveryColorForm(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, out, want string }{
		{"RGB foreground with ':'", "\x1b[38:2::4:5:6mC", `fill="#040506">C</tspan>`},
		{"RGB background with ':'", "\x1b[48:2::1:2:3m \x1b[0m", `fill="#010203"/>`},
		{"256-color background", "\x1b[48;5;21m \x1b[0m", `fill="#0000ff"/>`},
		{"bright foreground", "\x1b[91mC", `fill="#f14c4c">C</tspan>`},
		{"underline color takes its arguments, not bold's place", "\x1b[1;58;2;0;0;0mZ", `class="b">Z</tspan>`},
		{"ESC 8 restores the saved style", "\x1b[31m\x1b7\x1b[0m\x1b8S", `fill="#cd3131">S</tspan>`},
		{"underlined spaces stay drawn", "\x1b[4m  ", `class="u">  </tspan>`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if svg := svgOf(t, 10, 1, Event{Data: tc.out}); !strings.Contains(svg, tc.want) {
				t.Errorf("the SVG lacks %s:\n%s", tc.want, svg)
			}
		})
	}
	if svg := svgOf(t, 10, 1, Event{Data: "\x1b[41m   \x1b[0m"}); strings.Contains(svg, `"></tspan>`) {
		t.Errorf("a run of plain spaces drew an empty tspan:\n%s", svg)
	}
	if svg := svgOf(t, 10, 1, Event{Data: "\x1b[58;5;1mW"}); strings.Contains(svg, `class="b"`) {
		t.Errorf("SGR 58's arguments were read as SGR codes:\n%s", svg)
	}
}

// TestSVGRefusesAnUnrenderableCast: a size out of bounds is an error, not a
// huge file.
func TestSVGRefusesAnUnrenderableCast(t *testing.T) {
	t.Parallel()
	if _, err := (Cast{Width: MaxWidth + 1, Height: 24}).SVG(SVGOptions{}); err == nil {
		t.Error("SVG of a terminal wider than MaxWidth succeeded")
	}
}

// FuzzSVG: any output, split anywhere across writes, on any small terminal,
// renders without a panic to well-formed XML.
func FuzzSVG(f *testing.F) {
	for _, seed := range []string{
		"plain\r\ntext",
		"\x1b[1;31mred\x1b[0m\x1b[48;5;21mbg\x1b[38:2::1:2:3mrgb",
		"\x1b]0;title\x07\x1bP+q\x1b\\\x1b_G\x1b\\\x1b^pm\x1b\\",
		"\u65e5\u672c\u0301\xe6\x97\xff\x00\x7f\ufffe",
		"\x1b[999;999H\x1b[99999@\x1b[99999P\x1b[99999L\x1b[99999M\x1b[S\x1b[T\x1bM\x1b7\x1b8\x1bc",
		"\x1b[" + strings.Repeat("9;", 100) + "m\x1b\x1b[\x18\x1a",
	} {
		f.Add(seed, uint8(3), uint8(2), uint8(1), uint8(5))
	}
	f.Fuzz(func(t *testing.T, out string, w, h, cut1, cut2 uint8) {
		// Three writes, split at two points, a millisecond apart.
		a, b := int(cut1)%(len(out)+1), int(cut2)%(len(out)+1)
		if a > b {
			a, b = b, a
		}
		c := Cast{Width: 1 + int(w)%8, Height: 1 + int(h)%5, Events: []Event{
			{Data: out[:a]}, {Time: time.Millisecond, Data: out[a:b]}, {Time: 2 * time.Millisecond, Data: out[b:]},
		}}
		if _, err := c.FinalText(); err != nil {
			t.Fatalf("FinalText: %v", err)
		}
		svg, err := c.SVG(SVGOptions{})
		if err != nil {
			t.Fatalf("SVG: %v", err)
		}
		if err := wellFormed(svg); err != nil {
			t.Fatal(errors.Join(err, fmt.Errorf("%q", svg)))
		}
	})
}
