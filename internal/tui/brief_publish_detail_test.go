package tui

import (
	"strings"
	"testing"
)

// publishScreen is the publish screen of a ticket published in one repo, its
// body and changelog 12 lines each, at w x h.
func publishScreen(t *testing.T, w, h int) *rig {
	t.Helper()
	g := newRig(t, "JIG-2")
	g.r.answer("status JIG-2 --store /s", statusJIG2DoneWith(numbered("body", 12), numbered("changelog", 12)), 0)
	g.feed(jig2()...).press("up", "enter")
	if g.m.Screen() != "publish" {
		t.Fatalf("on %s, want publish", g.m.Screen())
	}
	return g.sized(w, h)
}

// TestPublishScreenIsARowPerCheckAndTheCursorCheckWhole: the checklist is a row
// for each check, and the detail of the check under the cursor holds what the
// status and the journal carry of it: the body's lines whole with its file, who
// approved which head, the branch, the pull request, the changelog. Nothing is
// shared out between the body and the changelog: each is whole in its own row's
// detail.
func TestPublishScreenIsARowPerCheckAndTheCursorCheckWhole(t *testing.T) {
	t.Parallel()
	g := publishScreen(t, 100, 60)
	view := g.view()
	list := readWords(between(view, "Checklist", "╰"))
	for _, want := range []string{"body written for", "approved by you for ccccccc", "branch jig/JIG-2 pushed", "pull request · opened · https://example.test/acme/demo/pull/7", "changelog written", "published"} {
		if !strings.Contains(list, want) {
			t.Errorf("the checklist lacks a row for %q:\n%s", want, view)
		}
	}
	if strings.Contains(list, "body line 1") || strings.Contains(list, "changelog line 1") {
		t.Errorf("the checklist shares the texts out beside its rows:\n%s", view)
	}
	d := readWords(between(view, "╭─ Detail · body", "╰"))
	for _, want := range []string{"file tickets/JIG-2/pr/demo.md", "body line 1 body line 2", "body line 12"} {
		if !strings.Contains(d, want) {
			t.Errorf("the detail of the body lacks %q:\n%s", want, view)
		}
	}
	g.press("down")
	if d := readWords(between(g.view(), "╭─ Detail · approval", "╰")); !strings.Contains(d, "approved by you") || !strings.Contains(d, "for ccccccc") {
		t.Errorf("the detail of the approval lacks who and which head:\n%s", g.view())
	}
	g.press("down")
	if d := readWords(between(g.view(), "╭─ Detail · branch", "╰")); !strings.Contains(d, "branch jig/JIG-2") {
		t.Errorf("the detail of the branch lacks its name:\n%s", g.view())
	}
	g.press("down", "down")
	d = readWords(between(g.view(), "╭─ Detail · changelog", "╰"))
	for _, want := range []string{"file tickets/JIG-2/changelog/consolidated.md", "changelog line 1 changelog line 2", "changelog line 12"} {
		if !strings.Contains(d, want) {
			t.Errorf("the detail of the changelog lacks %q:\n%s", want, g.view())
		}
	}
	g.press("enter")
	if g.m.Screen() != "publish" {
		t.Errorf("a check row opened %s", g.m.Screen())
	}
}

// TestAPullRequestChecksDetailNamesItsAddressWhole: a ticket published in two
// repos has a pull request check for each, whose detail has the repo, the number,
// state and target and the address whole, wrapped at a narrow width, and a check
// not done says what it waits for.
func TestAPullRequestChecksDetailNamesItsAddressWhole(t *testing.T) {
	t.Parallel()
	g := newRig(t, "JIG-2")
	g.r.answer("status JIG-2 --store /s", statusJIG2Repos(), 0)
	g.feed(jig2Repos()...).press("up", "enter")
	if g.m.Screen() != "publish" {
		t.Fatalf("on %s, want publish", g.m.Screen())
	}
	g.sized(60, 50)
	g.press("down", "down", "down") // pull request api
	view := g.view()
	d := readWords(between(view, "╭─ Detail · pull request api", "╰"))
	for _, want := range []string{"repo api", "https://example.test/acme/api/pull/7", "outcome opened"} {
		if !strings.Contains(d, want) {
			t.Errorf("the detail of the api pull request lacks %q:\n%s", want, view)
		}
	}
	g.press("down")
	if d := readWords(between(g.view(), "╭─ Detail · pull request web", "╰")); !strings.Contains(d, "https://example.test/acme/web/pull/3") {
		t.Errorf("the detail of the web pull request lacks its address:\n%s", g.view())
	}
	// A repo with no pull request yet says what it waits for.
	lines := jig2Repos()
	g2 := newRig(t, "JIG-2")
	g2.r.answer("status JIG-2 --store /s", statusJIG2Repos(), 0)
	g2.feed(lines[:len(lines)-2]...)
	g2.press("enter")
	if g2.m.Screen() != "publish" {
		t.Fatalf("on %s, want publish", g2.m.Screen())
	}
	g2.sized(100, 50)
	g2.press("down", "down", "down", "down") // pull request web
	if d := readWords(between(g2.view(), "╭─ Detail · pull request web", "╰")); !strings.Contains(d, "waits for pull request web opened") {
		t.Errorf("a pull request not opened does not say what it waits for:\n%s", g2.view())
	}
}
