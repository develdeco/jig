package tui

import (
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/events"
	"github.com/develdeco/jig/internal/journal"
)

// briefRig is the brief screen of JIG-1 at w x h.
func briefRig(t *testing.T, w, h int) *rig {
	t.Helper()
	g := newRig(t, "JIG-1")
	g.r.answer("status JIG-1 --store /s", statusJIG1Brief(), 0)
	g.r.answer("brief show JIG-1 --text --store /s", briefTextJIG1(), 0)
	g.feed(append([]events.Envelope{
		jl(at(0, 0), "JIG-1", "", journal.EventApproval, with("s-planner", "", "", "Ada Lovelace <ada@example.invalid>", "0123456789ab", "agent")),
	}, jig1Records()...)...).press("up", "enter")
	if g.m.Screen() != "brief" {
		t.Fatalf("on %s, want brief", g.m.Screen())
	}
	return g.sized(w, h)
}

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

// TestBriefScreenIsARowPerSectionAndSliceAndTheCursorPartWhole: the sections and
// the slices are one list of rows, sections first, and the detail under them is
// the part the cursor is on: a section's heading, who cites it and its text; a
// slice's goal, oracle, workspace and the sections that cite it. A section opens
// nothing and a slice opens its screen.
func TestBriefScreenIsARowPerSectionAndSliceAndTheCursorPartWhole(t *testing.T) {
	t.Parallel()
	g := briefRig(t, 100, 40)
	view := g.view()
	for _, want := range []string{"Goal", "Greeting", "Farewell", "Tone"} {
		if !strings.Contains(between(view, "Sections", "╰"), want) {
			t.Errorf("the Sections list has no row for %q:\n%s", want, view)
		}
	}
	for _, id := range []string{"a", "b", "c", "d"} {
		if !strings.Contains(between(view, "Slices", "╰"), " "+id+" ") {
			t.Errorf("the Slices list has no row for slice %s:\n%s", id, view)
		}
	}
	if d := readWords(between(view, "╭─ Detail · Goal", "╰")); !strings.Contains(d, "Say hello and goodbye to a person by name, in the tone the product has.") {
		t.Errorf("the detail of the first section lacks its text:\n%s", view)
	}
	// Opening a section opens nothing.
	g.press("enter")
	if g.m.Screen() != "brief" {
		t.Errorf("a section row opened %s", g.m.Screen())
	}
	g.press("down", "down", "down") // the last section
	d := readWords(between(g.view(), "╭─ Detail · Tone", "╰"))
	for _, want := range []string{"heading Tone", "cited by", "c · needs-input", "d · queued", "Casual or formal: the owner decides. Until then neither is shipped, so the copy waits for the answer."} {
		if !strings.Contains(d, want) {
			t.Errorf("the detail of the last section lacks %q:\n%s", want, g.view())
		}
	}
	g.press("down") // the first slice
	d = readWords(between(g.view(), "╭─ Detail · a", "╰"))
	for _, want := range []string{"goal Greet by name", "oracle unit", "workspace app", "cited in Greeting"} {
		if !strings.Contains(d, want) {
			t.Errorf("the detail of the first slice lacks %q:\n%s", want, g.view())
		}
	}
	g.press("down", "down", "down") // slice d, which waits for b and c
	if d := readWords(between(g.view(), "╭─ Detail · d", "╰")); !strings.Contains(d, "waits for b, c") || !strings.Contains(d, "cited in Tone") {
		t.Errorf("the detail of slice d lacks what it waits for and who cites it:\n%s", g.view())
	}
	g.press("enter")
	if g.m.Screen() != "slice" {
		t.Errorf("a slice row opened %s, want slice", g.m.Screen())
	}
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
