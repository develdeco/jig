package termrec

import (
	"encoding/xml"
	"fmt"
	"io"
	"strings"
	"testing"
	"time"
)

// svgOf renders events on a width x height terminal with default options and
// checks the result is well-formed XML with an svg root.
func svgOf(t *testing.T, width, height int, events ...Event) string {
	t.Helper()
	out, err := Cast{Width: width, Height: height, Events: events}.SVG(SVGOptions{})
	if err != nil {
		t.Fatalf("SVG: %v", err)
	}
	dec := xml.NewDecoder(strings.NewReader(string(out)))
	root := ""
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatalf("the SVG is not well-formed XML: %v\n%s", err, out)
		}
		if se, ok := tok.(xml.StartElement); ok && root == "" {
			root = se.Name.Local
		}
	}
	if root != "svg" {
		t.Fatalf("root element = %q, want svg", root)
	}
	return string(out)
}

// frameCount counts the frames an SVG stacks for its animation.
func frameCount(svg string) int { return strings.Count(svg, `<g transform="translate(0 `) }

// TestSVGAnimatesTheScreenOverTime: one frame per change, writes in the same
// instant merged, a long pause capped at the idle limit, the last frame held
// before the loop starts over.
func TestSVGAnimatesTheScreenOverTime(t *testing.T) {
	t.Parallel()
	svg := svgOf(t, 10, 2,
		Event{Time: 0, Data: "a"},
		Event{Time: time.Second, Data: "b"},
		Event{Time: time.Second + 10*time.Millisecond, Data: "c"}, // the same instant as "b"
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
// keeps at most maxFrames frames (plus the blank first one).
func TestSVGKeepsALongRecordingBounded(t *testing.T) {
	t.Parallel()
	var events []Event
	for i := 0; i < 5000; i++ {
		events = append(events, Event{Time: time.Duration(i) * 100 * time.Millisecond, Data: fmt.Sprintf("\r%d", i)})
	}
	svg := svgOf(t, 10, 2, events...)
	if n := frameCount(svg); n > maxFrames+1 || n < maxFrames/2 {
		t.Errorf("frames = %d, want between %d and %d", n, maxFrames/2, maxFrames+1)
	}
	if !strings.Contains(svg, `>4999</tspan>`) {
		t.Errorf("the last frame does not show the last write")
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

// TestSVGRefusesAnUnrenderableCast: a size out of bounds is an error, not a
// huge file.
func TestSVGRefusesAnUnrenderableCast(t *testing.T) {
	t.Parallel()
	if _, err := (Cast{Width: MaxWidth + 1, Height: 24}).SVG(SVGOptions{}); err == nil {
		t.Error("SVG of a terminal wider than MaxWidth succeeded")
	}
}
