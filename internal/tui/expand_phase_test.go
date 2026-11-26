package tui

import (
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/journal"
)

// phaseSizes are the terminals the phase screens are drawn in: narrow, standard
// and wide, each tall enough to hold an expanded row.
var phaseSizes = [][2]int{{60, 50}, {80, 50}, {230, 50}}

// subjectOf is a commit's subject of n numbered words.
func subjectWords(n int) string { return cutTitle(n) }

// buildRig is the build screen of BIG-1 at w by h: slice s1 with a commit whose
// subject has n words.
func buildRig(t *testing.T, w, h, n int) *rig {
	t.Helper()
	status := strings.Join([]string{"ticket: BIG-1", "state: building",
		axi.Table("slices", []string{"id", "state", "attempts", "blocked_by", "question", "goal"}, [][]string{
			{"s1", "building", "1", "-", "-", "Slice s1"},
		}), "questions: none",
	}, "\n") + "\n"
	g := newRig(t, "BIG-1")
	g.r.answer("status BIG-1 --store /s", status, 0)
	g.send(tea.WindowSizeMsg{Width: w, Height: h})
	g.feed(
		jl(at(1, 0), "BIG-1", "s1", journal.EventClaim, with("s-builder", "", "", "", "", "")),
		jl(at(1, 20), "BIG-1", "s1", journal.EventVerifyPending, with("s-builder", sha('1'), "", subjectWords(n), "", "")),
	)
	g.press("enter")
	if g.m.Screen() != "build" {
		t.Fatalf("on %s, want the build screen", g.m.Screen())
	}
	return g
}

// TestABuildRowExpandsItsCutSubjectWhole: a commit's subject under a slice of the
// build screen is cut after three rows; space on the slice lays it out whole,
// and space again folds it, at a narrow, a standard and a wide terminal.
func TestABuildRowExpandsItsCutSubjectWhole(t *testing.T) {
	t.Parallel()
	for _, size := range phaseSizes {
		w, h := size[0], size[1]
		n := 40
		if w > 100 {
			n = 200
		}
		t.Run(fmt.Sprintf("%dx%d", w, h), func(t *testing.T) {
			t.Parallel()
			g := buildRig(t, w, h, n)
			g.tabTo("Slices")
			before := stripANSI(g.view())
			if !strings.Contains(before, "…") || hasWordsInOrder(before, subjectWords(n)) {
				t.Fatalf("the subject is not cut:\n%s", before)
			}
			if hint := strings.Join(strings.Fields(before), " "); !strings.Contains(hint, "space expand") {
				t.Errorf("the hint line does not offer space on the slice:\n%s", before)
			}
			g.press("space")
			open := stripANSI(g.view())
			if panel := panelText(t, open, "Slices"); strings.Contains(panel, "…") || !hasWordsInOrder(panel, subjectWords(n)) {
				t.Errorf("space did not lay the subject out whole:\n%s", open)
			}
			g.press("space")
			if folded := stripANSI(g.view()); folded != before {
				t.Errorf("folding does not give the frame back:\n--- before\n%s\n--- after\n%s", before, folded)
			}
		})
	}
}

// findingStatus is a status with one finding to fix, titled with n words.
func findingStatus(n int) string {
	return strings.Join([]string{
		"ticket: JIG-2", "state: green", "fix_budget: 1 of 3 rounds used",
		axi.Table("slices", []string{"id", "state", "attempts", "blocked_by", "question", "goal", "workspace", "oracle", "from_gate"}, [][]string{
			{"a", "green", "1", "-", "-", "Load the config", "alpha", "unit", "-"},
		}),
		"questions: none",
		axi.Table("rounds", roundColumns, [][]string{
			{"1", "findings", "s-reviewer", "claude", "claude-opus-5", "true", "1", "2", "1", "full", sha('0'), sha('c'), "4"},
		}),
		axi.Table("findings", findingColumns, [][]string{
			{"r1-f2", "1", "fix", "medium", "alpha/a.go:12", cutTitle(n), "open", longAskReason, "", ""},
		}),
		axi.Help("Run `jig triage JIG-2 <id> keep|dismiss`"),
	}, "\n") + "\n"
}

// TestAReviewRowExpandsItsCutFindingWhole: a finding's title is cut after three
// rows on the review screen; space on the finding shows it whole, with the
// rationale under it whole too, and folds back.
func TestAReviewRowExpandsItsCutFindingWhole(t *testing.T) {
	t.Parallel()
	for _, size := range phaseSizes {
		w, h := size[0], size[1]
		n := 40
		if w > 100 {
			n = 200
		}
		t.Run(fmt.Sprintf("%dx%d", w, h), func(t *testing.T) {
			t.Parallel()
			g := newRig(t, "JIG-2")
			g.r.answer("status JIG-2 --store /s", findingStatus(n), 0)
			g.send(tea.WindowSizeMsg{Width: w, Height: h})
			g.feed(jig2Mid()...)
			g.press("enter")
			if g.m.Screen() != "review" {
				t.Fatalf("on %s, want the review screen", g.m.Screen())
			}
			g.tabTo("Findings")
			before := stripANSI(g.view())
			findings := panelText(t, before, "Findings")
			if !strings.Contains(findings, "…") || hasWordsInOrder(findings, cutTitle(n)) {
				t.Fatalf("the finding is not cut:\n%s", before)
			}
			g.press("space")
			open := stripANSI(g.view())
			if panel := panelText(t, open, "Findings"); strings.Contains(between(panel, "w001", "The loop"), "…") || !hasWordsInOrder(panel, cutTitle(n)) || !hasWordsInOrder(panel, longAskReason) {
				t.Errorf("space did not show the finding whole:\n%s", open)
			}
			g.press("space")
			if folded := stripANSI(g.view()); folded != before {
				t.Errorf("folding does not give the frame back:\n--- before\n%s\n--- after\n%s", before, folded)
			}
		})
	}
}

// TestASliceScreenRowExpandsItsCutTextWhole: the slice screen's question row has
// its text under it; on a cut line of the slice's activity the same key lays the
// line out whole.
func TestASliceScreenRowExpandsItsCutTextWhole(t *testing.T) {
	t.Parallel()
	g := buildRig(t, 80, 50, 40)
	g.press("enter")
	if g.m.Screen() != "slice" {
		t.Fatalf("on %s, want the slice screen:\n%s", g.m.Screen(), g.view())
	}
	g.tabTo("Timeline")
	before := stripANSI(g.view())
	if !strings.Contains(panelText(t, before, "Timeline"), "…") {
		t.Fatalf("no line of the slice's activity is cut:\n%s", before)
	}
	g.press("space")
	open := stripANSI(g.view())
	if panel := panelText(t, open, "Timeline"); strings.Contains(panel, "…") || !hasWordsInOrder(panel, subjectWords(40)) {
		t.Errorf("space did not show the line whole:\n%s", open)
	}
	g.press("space")
	if folded := stripANSI(g.view()); folded != before {
		t.Errorf("folding does not give the frame back:\n--- before\n%s\n--- after\n%s", before, folded)
	}
}
