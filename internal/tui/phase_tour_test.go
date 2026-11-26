package tui

import (
	"fmt"
	"regexp"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/events"
	"github.com/develdeco/jig/internal/journal"
)

// tourEvents are JIG-2 in its fix round, published in two repos: a slice whose
// run failed, two review rounds with findings, a fix slice and an ask, a body and
// an approval and a pull request for each repo.
func tourEvents() []events.Envelope {
	oracle := func(l *journal.Line) { l.Command, l.Seconds, l.Source = "go test ./alpha", 12, "jig" }
	out := append(reviewFixRoundEvents(),
		jl(at(10, 40), "JIG-2", "b", journal.EventClaim, with("s-builder", "", "", "", "", "")),
		jl(at(10, 50), "JIG-2", "b", journal.EventVerifyPending, with("s-builder", sha('e'), "", "Show the config", "", "")),
		jl(at(10, 55), "JIG-2", "b", journal.EventOracle, with("", sha('e'), "fail", "TestShow: want 2, got 3", "", ""), oracle),
	)
	return append(out, jig2Repos()[8:]...)
}

// tourStatus is `jig status JIG-2` for tourEvents: the slices, the two rounds and
// the findings of the second, the ask, the brief and the publish.
func tourStatus() string {
	return strings.Join([]string{
		"ticket: JIG-2",
		"state: parked",
		"fix_budget: 2 of 3 rounds used",
		axi.Table("slices", []string{"id", "state", "attempts", "blocked_by", "question", "goal", "workspace", "oracle", "from_gate", "findings"}, [][]string{
			{"a", "green", "1", "-", "-", "Load the config", "api/alpha", "unit", "-", "-"},
			{"b", "building", "1", "a", "-", "Show the config", "web/beta", "go test ./beta && go vet ./beta", "-", "-"},
			{"fix-1-alpha", "green", "1", "-", "-", "Fix these gate findings:\n\nalpha/a.go:12 Close the file", "api/alpha", "unit", "1", "r1-f3"},
			{"fix-2-alpha", "queued", "0", "b", "-", "Fix these gate findings:\n\nalpha/c.go:7 Check the returned error\nThe error is dropped and the caller carries on.", "api/alpha", "unit", "2", "r2-f1"},
		}),
		"questions: none",
		axi.Table("outstanding_asks", []string{"id", "risk", "file:line", "title", "why"}, [][]string{
			{"r2-f2", "high", "alpha/c.go:30", "Retry loop never ends", "the fix budget is not reached, so a person decides"},
		}),
		axi.KV("brief", [][2]string{{"approval", "current"}, {"approved_by", "an agent (as Ada Lovelace)"}, {"identity", "Ada Lovelace"}, {"approved_at", "2026-10-23T09:00:00Z"}, {"provenance", "agent"}}),
		axi.Table("brief_sections", []string{"heading", "slices"}, [][]string{
			{"Goal", "-"}, {"Load", "a"}, {"Show", "b"}, {"Fixes", "fix-1-alpha+fix-2-alpha"},
		}),
		axi.Table("rounds", roundColumns, [][]string{
			{"1", "findings", "s-reviewer", "claude", "claude-opus-5", "true", "1", "1", "1", "full", sha('0'), sha('c'), "4", "0", "-", "-"},
			{"2", "findings", "s-reviewer", "claude", "claude-opus-5", "true", "1", "1", "1", "delta", sha('c'), sha('d'), "3", "0", "-", "-"},
		}),
		axi.Table("findings", findingColumns, [][]string{
			{"r2-f1", "2", "fix", "medium", "alpha/c.go:7", "Check the returned error", "open", "the error is dropped and the caller carries on", "", ""},
			{"r2-f2", "2", "ask", "high", "alpha/c.go:30", "Retry loop never ends", "asked", "a failing call is retried forever", "", ""},
			{"r2-f3", "2", "note", "low", "alpha/c.go:2", "Rename the helper", "noted", "a clearer name, whenever the file is next touched", "", ""},
		}),
		axi.KV("publish", [][2]string{
			{"ticket", "JIG-2"}, {"branch", "jig/JIG-2"},
			{"changelog_file", "tickets/JIG-2/changelog/consolidated.md"}, {"changelog", "## Slices\n- a: ccccccc\n- b: ddddddd"},
		}),
		axi.Table("publish_prs", []string{"repo", "pr", "state", "head", "body_file"}, [][]string{
			{"api", "https://example.test/acme/api/pull/7", "opened", sha('c'), "tickets/JIG-2/pr/api.md"},
			{"web", "https://example.test/acme/web/pull/3", "opened", sha('d'), "tickets/JIG-2/pr/web.md"},
		}),
		axi.Help("Run `jig triage JIG-2 <id> keep|dismiss` to decide the outstanding asks"),
	}, "\n") + "\n"
}

// tourRig is the ticket of tourEvents at w x h.
func tourRig(t *testing.T, w, h int) *rig {
	t.Helper()
	g := newRig(t, "JIG-2")
	g.r.answer("status JIG-2 --store /s", tourStatus(), 0)
	g.r.answer("brief show JIG-2 --text --store /s", strings.Join([]string{
		axi.KV("brief", [][2]string{{"ticket", "JIG-2"}, {"title", "Config loader"}, {"illustration", "no"}}),
		axi.Table("sections", []string{"heading", "summary"}, [][]string{
			{"Goal", "Load the config from the file the person names, and show it."},
			{"Load", "Read the file once and name the error when it is missing."},
			{"Show", "Print the config the way the person wrote it."},
			{"Fixes", "What the gate sent back, each as a slice of its own."},
		}),
	}, "\n")+"\n", 0)
	g.feed(tourEvents()...)
	return g.sized(w, h)
}

var detailTitle = regexp.MustCompile(`╭─ Detail · (.+?) ─`)

// TestPhaseScreensAreAListAndTheDetailOfTheCursorRow: the build, review, brief and
// publish screens of a ticket in its fix round, at five sizes: every thing is a
// row of a list, the Detail panel is the cursor row's and each down moves it to
// the next row, none of its fields is cut with an ellipsis (what the screen has
// no room for is marked inside the box), the detail is under the list at every width,
// and at 100x30 the lists and details of the build and the review are
// whole in the first screenful.
func TestPhaseScreensAreAListAndTheDetailOfTheCursorRow(t *testing.T) {
	t.Parallel()
	sizes := [][2]int{{60, 20}, {80, 24}, {100, 30}, {160, 40}, {230, 50}}
	screens := []struct {
		name, key string
		rows      int
	}{
		{"build", "2", 4}, {"review", "3", 3}, {"brief", "1", 8}, {"publish", "4", 7},
	}
	for _, size := range sizes {
		w, h := size[0], size[1]
		for _, sc := range screens {
			t.Run(fmt.Sprintf("%s %dx%d", sc.name, w, h), func(t *testing.T) {
				t.Parallel()
				g := tourRig(t, w, h)
				g.press(sc.key)
				if g.m.Screen() != sc.name {
					t.Fatalf("on %s, want %s", g.m.Screen(), sc.name)
				}
				// The lists' rows: the activity panes under them have rows of their own.
				// The Detail's own rows (a finding, a try, a question) are not things of
				// the list: they are what the row under the cursor holds.
				var refs []rowRef
				pg := g.m.page()
				all := pg.refs()
				for _, p := range pg.panes() {
					if strings.HasPrefix(p.title, "Detail") {
						continue
					}
					for _, r := range all[p.first : p.first+p.rows] {
						if r.kind != rowLog {
							refs = append(refs, r)
						}
					}
				}
				if len(refs) != sc.rows {
					t.Fatalf("%d rows to stand on, want %d (a row for each thing): %+v", len(refs), sc.rows, refs)
				}
				// The person has read down to the first row: the window follows the cursor.
				g.focus(refs[0].id)
				seen := map[string]bool{}
				for i := range refs {
					view := g.view()
					m := detailTitle.FindStringSubmatch(view)
					if m == nil {
						t.Fatalf("row %d: no detail of the row under the cursor:\n%s", i, view)
					}
					if seen[m[1]] {
						t.Errorf("row %d: the detail %q is the one of an earlier row:\n%s", i, m[1], view)
					}
					seen[m[1]] = true
					// What is in the detail is whole: a field is wrapped at its words and never
					// ends in an ellipsis; what does not fit is marked in the box.
					panel := between(view, "╭─ Detail · "+m[1], "╰")
					if strings.Contains(panel, "…") {
						t.Errorf("row %d: a field of the detail is cut with an ellipsis:\n%s", i, view)
					}
					if w == 100 && (sc.name == "build" || sc.name == "review") && strings.Contains(panel, "↓ ") {
						t.Errorf("row %d: the detail is not whole in the first screenful at 100x30:\n%s", i, view)
					}
					// Down moves the focused list's cursor; at its last row tab goes on to
					// the next list.
					before := *g.m.selected()
					g.press("down")
					for k := 0; k < 8 && i+1 < len(refs); k++ {
						if got := g.m.selected(); got != nil && got.kind != rowLog && *got != before {
							break
						}
						g.press("tab")
					}
				}
			})
		}
	}
}
