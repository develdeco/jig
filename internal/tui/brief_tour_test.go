package tui

import (
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/axi"
)

// bigBriefStatus is the status of a task whose brief has six sections and two
// slices, approved before one section was edited and another was dropped, with
// its illustration added since.
func bigBriefStatus(task string) string {
	return strings.Join([]string{
		"ticket: " + task,
		"state: building",
		axi.Table("slices", []string{"id", "state", "attempts", "blocked_by", "question", "goal", "workspace", "oracle", "from_gate"}, [][]string{
			{"a", "green", "1", "-", "-", "Greet by name", "app", "unit", "-"},
			{"b", "queued", "0", "a", "-", "Say goodbye, politely", "app", "unit", "-"},
		}),
		"questions: none",
		axi.KV("brief", [][2]string{{"approval", "changed"}, {"approved_by", "Ada Lovelace"}, {"identity", "Ada Lovelace"}, {"approved_at", "2026-10-23T09:00:00Z"}, {"provenance", "person"}, {"illustration_since_approval", "added"}}),
		axi.Table("brief_sections", []string{"heading", "slices", "since_approval"}, [][]string{
			{"Goal", "-", "same"}, {"Context", "-", "same"}, {"Rules", "a", "changed"}, {"Greeting", "a", "same"}, {"Farewell", "b", "same"}, {"Not here", "-", "same"},
		}),
		axi.Table("brief_removed", []string{"heading"}, [][]string{{"Old rules"}}),
		axi.Help("Run `jig next " + task + "`"),
	}, "\n") + "\n"
}

// bigBriefText is `jig brief show --text` for it: each section a few lines of
// markdown, the rules with bullets, a code span and a fence.
func bigBriefText(task string) string {
	bodies := [][2]string{
		{"Goal", "Say hello and goodbye to a person by name."},
		{"Context", "Facts read at the brief's writing.\n\n- the greeting is `Hello, <name>`\n- the farewell is **never** twice"},
		{"Rules", "Use `alpha.Clamp` for the range.\n\n- first rule\n  - a nested rule\n- second rule\n\n```\nHello, <name>\n```\n\nSee [the guide](https://example.test/guide)."},
		{"Greeting", "Use the first name."},
		{"Farewell", "Say goodbye politely."},
		{"Not here", "Left out."},
	}
	var rows [][]string
	blocks := []string{axi.KV("brief", [][2]string{{"ticket", task}, {"title", "Greeting copy"}, {"illustration", "yes"}})}
	for _, b := range bodies {
		rows = append(rows, []string{b[0], strings.SplitN(b[1], "\n", 2)[0]})
	}
	blocks = append(blocks, axi.Table("sections", []string{"heading", "summary"}, rows))
	for _, b := range bodies {
		blocks = append(blocks, "section: "+b[0]+"\n  "+strings.ReplaceAll(b[1], "\n", "\n  "))
	}
	return strings.Join(append(blocks, axi.Help("serve it")), "\n") + "\n"
}

// TestTheBriefScreenIsAToursOfItsPartsAtEverySize: a task of an issue whose
// brief has six sections, two slices, an illustration and an approval a section
// was edited after has a row for each part, at every size the screens are drawn
// at; the detail of the cursor's part is under the list, whole or cut with a mark
// inside its own box, and the text of a section keeps the backticks of a code span
// in a plain frame, whole when its pane is zoomed.
func TestTheBriefScreenIsAToursOfItsPartsAtEverySize(t *testing.T) {
	t.Parallel()
	for _, size := range [][2]int{{60, 20}, {80, 24}, {100, 30}, {160, 40}, {230, 50}} {
		g := planMain(t, planSpec{})
		g.r.answer("status SHOP-2 --store /s", bigBriefStatus("SHOP-2"), 0)
		g.r.answer("brief show SHOP-2 --text --store /s", bigBriefText("SHOP-2"), 0)
		g.sized(size[0], size[1]).onRow("SHOP-2").press("enter", "1")
		if g.m.Screen() != "brief" {
			t.Fatalf("%v: on %s, want the brief", size, g.m.Screen())
		}
		// A row for each part: the issue, six sections, two slices, the
		// illustration and the approval.
		var parts []rowRef
		for _, r := range g.m.page().refs() {
			if r.kind != rowLog && r.kind != rowStop {
				parts = append(parts, r)
			}
		}
		want := []string{rowIssue, rowSection, rowSection, rowSection, rowSection, rowSection, rowSection, rowSlice, rowSlice, rowIllustration, rowApproval}
		if len(parts) != len(want) {
			t.Fatalf("%v: %d parts, want %d: %+v", size, len(parts), len(want), parts)
		}
		for i, p := range parts {
			if p.kind != want[i] {
				t.Errorf("%v: part %d is a %s, want a %s", size, i, p.kind, want[i])
			}
		}
		// The cursor walks every part, and the detail of each is under the list,
		// in its own box, whole or cut with a mark.
		g.press("home")
		for i := range parts {
			view := g.view()
			lines := strings.Split(view, "\n")
			detailAt, listAt := -1, -1
			for n, l := range lines {
				if strings.Contains(l, "╭─ Detail · ") && detailAt < 0 {
					detailAt = n
				}
				if strings.Contains(l, "╭─ Parts") && listAt < 0 {
					listAt = n
				}
			}
			if detailAt < 0 || listAt < 0 || detailAt <= listAt {
				t.Errorf("%v part %d (%s): the detail is not under the list:\n%s", size, i, parts[i].kind, view)
			}
			if len(lines) != size[1] {
				t.Errorf("%v part %d: the frame is %d lines, want %d", size, i, len(lines), size[1])
			}
			g.press("down")
		}
		// The rules section, whole when its detail is zoomed, keeps its code span's
		// backticks in a plain frame and draws its list as bullets.
		g.focus("section-3").tabTo("Detail · Rules").press("z")
		zoomed := g.view()
		for _, w := range []string{"`alpha.Clamp`", "• first rule", "  • a nested rule", "See the guide (https://example.test/guide).", "Hello, <name>"} {
			if !strings.Contains(zoomed, w) {
				t.Errorf("%v: the zoomed detail of Rules lacks %q:\n%s", size, w, zoomed)
			}
		}
		g.press("z")
		// The approval names what changed, windowed or whole.
		g.focus("approval").tabTo("Detail · approval").press("z")
		if zoomed := readWords(g.view()); !strings.Contains(zoomed, "changed Rules") || !strings.Contains(zoomed, "removed Old rules") || !strings.Contains(zoomed, "illustration added") {
			t.Errorf("%v: the approval's detail does not name what changed:\n%s", size, g.view())
		}
	}
}
