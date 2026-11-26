package tui

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
)

// onScreenOf puts the rig's ticket screen's child e on top of the stack.
func onScreenOf(g *rig, e navEntry) {
	g.m.nav = append(g.m.nav[:2:2], e)
	g.view()
}

// sendKey presses a key by the name the table gives it.
func sendKey(g *rig, name string) {
	g.t.Helper()
	types := map[string]tea.KeyType{"pgup": tea.KeyPgUp, "pgdown": tea.KeyPgDown, "left": tea.KeyLeft, "right": tea.KeyRight}
	if kt, ok := types[name]; ok {
		g.send(tea.KeyMsg{Type: kt})
		return
	}
	g.press(name)
}

// hintPairs reads the hint line of a frame as the "key words" pairs it lists.
func hintPairs(frame string) []string {
	var out []string
	for _, l := range strings.Split(footerKeys(frame), "\n") {
		for _, p := range strings.Split(l, " · ") {
			if p = strings.TrimSpace(p); p != "" {
				out = append(out, p)
			}
		}
	}
	return out
}

// TestKeyTableDrivesHandlerAndHints: on each screen, a key runs the row of the
// table that names it there and no other, a row that cannot run puts its reason
// in the notice and runs nothing, and the hint line is the rows that can run,
// the quiet ones left out, in the table's own words.
func TestKeyTableDrivesHandlerAndHints(t *testing.T) {
	t.Parallel()
	places := []struct {
		name string
		nav  func(g *rig)
	}{
		{"main", func(g *rig) { g.m.nav = g.m.nav[:1]; g.view() }},
		{"ticket", func(g *rig) {}},
		{"brief", func(g *rig) { onScreenOf(g, navEntry{screen: screenBrief, ticket: "JIG-1"}) }},
		{"build", func(g *rig) { onScreenOf(g, navEntry{screen: screenBuild, ticket: "JIG-1"}) }},
		{"review", func(g *rig) { onScreenOf(g, navEntry{screen: screenReview, ticket: "JIG-1"}) }},
		{"publish", func(g *rig) { onScreenOf(g, navEntry{screen: screenPublish, ticket: "JIG-1"}) }},
		{"land", func(g *rig) { onScreenOf(g, navEntry{screen: screenLand, ticket: "JIG-1"}) }},
		{"slice", func(g *rig) { onScreenOf(g, navEntry{screen: screenSlice, ticket: "JIG-1", slice: "b"}) }},
	}
	for _, p := range places {
		g := stuckRig(t, map[string]string{"b": StateStalled})
		p.nav(g)
		g.press("down") // a key's reason is the one of the screen as it stands

		// The hint line is the table's: the rows the screen has, not quiet,
		// that can run now, each family once, in the table's order.
		var want []string
		seen := map[string]bool{}
		for _, r := range g.m.rowsOn() {
			if r.quiet || (r.hinted != 0 && !r.hinted.has(g.m.top().screen)) {
				continue
			}
			if ok, _ := r.avail(g.m, g.m.keysOn); !ok {
				continue
			}
			key, text := r.label, r.wordsFor(g.m, g.m.keysOn)
			if r.hintKey != "" {
				key, text = r.hintKey, r.hintText
			}
			if !seen[key+" "+text] {
				seen[key+" "+text] = true
				want = append(want, key+" "+text)
			}
		}
		if got := hintPairs(g.view()); strings.Join(got, "|") != strings.Join(want, "|") {
			t.Errorf("%s: the hint line is\n  %q\nthe table's runnable keys are\n  %q", p.name, got, want)
		}
		if len(want) < 3 || want[len(want)-1] == "" {
			t.Errorf("%s: the table lists too few keys: %q", p.name, want)
		}

		// A key runs its row on the screens the row names, when it can run,
		// and nowhere else.
		for i := range g.m.table {
			row := &g.m.table[i]
			for _, name := range row.names {
				var ran []string
				orig := row.run
				row.run = func(_ *Model, key string) tea.Cmd { ran = append(ran, key); return nil }
				before := len(g.r.lines())
				onScreen := g.m.listed(row)
				ok := true
				if onScreen {
					if row.needsSubject() {
						ok, _ = row.avail(g.m, g.m.subjectNow())
					}
				}
				g.m.input, g.m.ask, g.m.notice = nil, nil, ""
				sendKey(g, name)
				row.run = orig
				if want := onScreen && ok; (len(ran) == 1) != want {
					t.Errorf("%s: %s with row %q ran %v, want ran=%v (listed %v, can run %v, notice %q)", p.name, name, row.label+" "+row.words, ran, want, onScreen, ok, g.m.notice)
				}
				if len(ran) == 0 && len(g.r.lines()) != before {
					t.Errorf("%s: %s ran a command with its row's run held back: %q", p.name, name, g.r.lines()[before:])
				}
				g.m.help = nil
			}
		}
	}
}

// TestQuestionMarkListsEveryKey: ? draws, over the body of the screen it is
// pressed on, one row per key the screen has with its words and now, or not now
// and the table's reason; a key the screen does not have is not there, and the
// screen under it is still the one it was.
func TestQuestionMarkListsEveryKey(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name    string
		rig     func(t *testing.T) *rig
		nav     func(g *rig)
		has     []string
		hasNone []string
	}{
		{"main", func(t *testing.T) *rig { return can1(t, buildUnder()) }, func(g *rig) { g.m.nav = g.m.nav[:1]; g.view() },
			[]string{"Keys on this screen", "q quit now", "↑↓ move now", "g gate not now: the review is not ready: after the build", "t tail now"},
			[]string{"esc back", "k keep"}},
		{"brief", func(t *testing.T) *rig { return can1(t, briefWaiting()) }, func(g *rig) { onScreenOf(g, navEntry{screen: screenBrief, ticket: "JIG-2"}) },
			[]string{"esc back now", "v approve now", "1-5 steps now"},
			[]string{"g gate", "k keep", "p publish"}},
		{"build", func(t *testing.T) *rig { return can1(t, buildUnder()) }, func(g *rig) { onScreenOf(g, navEntry{screen: screenBuild, ticket: "JIG-2"}) },
			[]string{"u requeue not now: slice a is not stalled or env-blocked", "g gate not now: the review is not ready: after the build", "enter open now"},
			[]string{"v approve", "p publish"}},
		{"review", func(t *testing.T) *rig { return can1(t, reviewReady()) }, func(g *rig) { onScreenOf(g, navEntry{screen: screenReview, ticket: "JIG-2"}) },
			[]string{"g gate now", "k keep now", "d dismiss now"},
			[]string{"p publish", "u requeue"}},
		{"publish", func(t *testing.T) *rig { return can1(t, reviewReady()) }, func(g *rig) { onScreenOf(g, navEntry{screen: screenPublish, ticket: "JIG-2"}) },
			[]string{"↑↓ move now", "p publish not now: the publish is not ready: after a clean review"},
			[]string{"g gate"}},
		{"land", func(t *testing.T) *rig { return can1(t, landStop()) }, func(g *rig) { onScreenOf(g, navEntry{screen: screenLand, ticket: "JIG-2"}) },
			[]string{"↑↓ scroll now", "esc back now", "1-5 steps now", "h main now"},
			[]string{"g gate", "p publish", "k keep"}},
	} {
		for _, wd := range []int{80, 100} {
			g := sized(c.rig(t), wd, 24)
			c.nav(g)
			screen, depth := g.m.Screen(), len(g.m.nav)
			body := g.view()
			g.press("?")
			view := g.view()
			if g.m.Screen() != screen || len(g.m.nav) != depth {
				t.Errorf("%s: ? is a screen of its own: %s at %d, was %s at %d", c.name, g.m.Screen(), len(g.m.nav), screen, depth)
			}
			if len(strings.Split(view, "\n")) != len(strings.Split(body, "\n")) {
				t.Errorf("%s: the overlay changed the frame's height", c.name)
			}
			words := readWords(view)
			for _, h := range c.has {
				if !strings.Contains(words, h) {
					t.Errorf("%s: ? lacks %q:\n%s", c.name, h, view)
				}
			}
			for _, h := range c.hasNone {
				if strings.Contains(words, h) {
					t.Errorf("%s: ? lists %q, a key the screen does not have:\n%s", c.name, h, view)
				}
			}
			// Every row the screen has is listed once with its state.
			for _, r := range g.m.rowsOn() {
				if len(r.names) > 0 && !strings.Contains(words, r.label+" "+r.wordsFor(g.m, g.m.keysOn)) {
					t.Errorf("%s: ? does not list the key %q", c.name, r.label)
				}
			}
			g.press("?")
			if after := g.view(); after != body {
				t.Errorf("%s: ? again does not give the screen back:\n%s", c.name, after)
			}
		}
	}
}

// TestQuestionMarkEatsOtherKeys: while ? is up, up and down scroll it, ? and esc
// close it, q quits, and no other key acts: nothing runs, the screen under it
// does not move, the notice is not touched.
func TestQuestionMarkEatsOtherKeys(t *testing.T) {
	t.Parallel()
	g := sized(can1(t, reviewReady()), 80, 14)
	onScreenOf(g, navEntry{screen: screenReview, ticket: "JIG-2"})
	depth := len(g.m.nav)
	before := len(g.r.lines())
	g.press("?")
	if g.m.help == nil {
		t.Fatal("? did not open the keys")
	}
	g.m.notice = "kept"
	for _, k := range []string{"g", "k", "d", "p", "a", "u", "v", "5", "c", "t", "r", "m", "h", "o", "enter", "left", "[", "]", "1", "2", "3"} {
		sendKey(g, k)
		if g.m.help == nil || len(g.m.nav) != depth || g.m.Screen() != "review" || g.m.tail || g.m.notice != "kept" ||
			g.m.ask != nil || g.m.input != nil || len(g.r.lines()) != before {
			t.Fatalf("%s acted while the keys were up: screen %s depth %d tail %v notice %q commands %q", k, g.m.Screen(), len(g.m.nav), g.m.tail, g.m.notice, g.r.lines()[before:])
		}
	}

	// Up and down scroll it, and the list is long for 14 rows.
	top := g.view()
	g.press("down", "down")
	if g.m.help.scroll != 2 || g.view() == top {
		t.Errorf("down did not scroll the keys: scroll %d", g.m.help.scroll)
	}
	g.press("up")
	if g.m.help.scroll != 1 {
		t.Errorf("up did not scroll back: scroll %d", g.m.help.scroll)
	}
	g.press("end")
	last := g.m.help.scroll
	g.press("down")
	if g.m.help.scroll != last {
		t.Errorf("the keys scroll past their end: %d, then %d", last, g.m.help.scroll)
	}
	g.press("up", "up", "up", "up", "up", "up", "up", "up", "up", "up", "up", "up", "up", "up", "up", "up")
	if g.m.help.scroll != 0 {
		t.Errorf("the keys scroll above their start: %d", g.m.help.scroll)
	}

	// esc closes it and the screen is where it was, and q still quits.
	g.press("esc")
	if g.m.help != nil || len(g.m.nav) != depth {
		t.Errorf("esc did not close the keys alone: help %v depth %d, want %d", g.m.help != nil, len(g.m.nav), depth)
	}
	g.press("?", "q")
	if !g.m.quitting {
		t.Errorf("q did not quit with the keys up")
	}
}
