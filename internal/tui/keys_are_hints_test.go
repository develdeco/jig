package tui

import (
	"strings"
	"testing"
)

// TestEveryScreensKeysAreItsHints: on each screen, with slice b stalled and an
// open question on slice c, a key runs exactly when its hint is listed: a listed
// key runs its command (or opens its question), and an unlisted action key runs
// nothing and says why.
func TestEveryScreensKeysAreItsHints(t *testing.T) {
	t.Parallel()
	actions := map[string]string{"a": "a answer", "k": "k keep", "d": "d dismiss", "g": "g gate", "p": "p publish", "u": "u requeue", "v": "v approve"}
	type place struct {
		name  string
		nav   func(g *rig)
		slice string // the row the cursor is put on, "" for none
	}
	push := func(e navEntry) func(g *rig) {
		return func(g *rig) { g.m.nav = append(g.m.nav[:2:2], e) }
	}
	var places []place
	places = append(places,
		place{"main", func(g *rig) { g.m.nav = g.m.nav[:1] }, ""},
		place{"ticket", func(g *rig) {}, ""},
		place{"brief", push(navEntry{screen: screenBrief, ticket: "JIG-1"}), ""},
		place{"review", push(navEntry{screen: screenReview, ticket: "JIG-1"}), ""},
		place{"publish", push(navEntry{screen: screenPublish, ticket: "JIG-1"}), ""},
		place{"land", push(navEntry{screen: screenLand, ticket: "JIG-1"}), ""},
	)
	for _, s := range []string{"a", "b", "c"} {
		places = append(places,
			place{"build on slice " + s, push(navEntry{screen: screenBuild, ticket: "JIG-1"}), s},
			place{"slice " + s, push(navEntry{screen: screenSlice, ticket: "JIG-1", slice: s}), ""},
		)
	}
	for _, p := range places {
		g := stuckRig(t, map[string]string{"b": StateStalled})
		p.nav(g)
		if p.slice != "" {
			for range 8 {
				if ref := g.m.selected(); ref != nil && ref.slice == p.slice {
					break
				}
				g.press("down")
			}
		}
		hint := footerKeys(g.view())
		for key, listed := range actions {
			g.m.ask, g.m.input, g.m.notice = nil, nil, ""
			before := len(g.r.lines())
			g.press(key)
			did := len(g.r.lines()) != before || g.m.ask != nil || g.m.input != nil
			switch {
			case strings.Contains(hint, listed) && g.m.notice != "" && !did:
				// A listed key that needs a pick (several rows) says so; none of
				// these screens has that.
				t.Errorf("%s: %s is listed and did nothing: %q", p.name, key, g.m.notice)
			case strings.Contains(hint, listed) && !did:
				t.Errorf("%s: %s is listed and did nothing", p.name, key)
			case !strings.Contains(hint, listed) && (did || g.m.notice == ""):
				t.Errorf("%s: %s is not listed but did something (%v) or said nothing (%q)", p.name, key, did, g.m.notice)
			}
			if g.m.Screen() != strings.Fields(p.name)[0] && p.name != "ticket" && !strings.HasPrefix(p.name, "build") {
				t.Errorf("%s: %s moved the screen to %s", p.name, key, g.m.Screen())
			}
		}
	}
}

// TestAFrameBuildsItsPageOnce: a frame builds the page of its screen once for
// the keys' subject, whatever the screen; a key reads the subject with one build
// of its own.
func TestAFrameBuildsItsPageOnce(t *testing.T) {
	t.Parallel()
	for name, g := range map[string]*rig{
		"main":   mainRig(t),
		"ticket": stuckRig(t, map[string]string{"b": StateStalled}),
	} {
		g.view()
		before := g.m.pageBuilds
		g.view()
		if got := g.m.pageBuilds - before; got != 1 {
			t.Errorf("%s: a frame built %d pages, want 1", name, got)
		}
		before = g.m.pageBuilds
		g.m.can(actGate)
		if got := g.m.pageBuilds - before; got != 1 {
			t.Errorf("%s: a key's can built %d pages, want 1", name, got)
		}
	}
}
