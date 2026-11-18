package tui

import (
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/journal"
)

// TestBuildScreenSaysWhetherTheLastRunCoversTheSlicesLastCommit: the status's
// last_runs table tells the screen each slice's last run and whether it covers
// the slice's last commit, and the build screen says it in the same words
// under the slice's commits; a slice with no run says nothing of one.
func TestBuildScreenSaysWhetherTheLastRunCoversTheSlicesLastCommit(t *testing.T) {
	t.Parallel()
	g := newRig(t, "JIG-1")
	status := strings.Join([]string{
		"ticket: JIG-1", "state: building",
		axi.Table("slices", []string{"id", "state", "attempts", "blocked_by", "question", "goal", "workspace", "oracle", "from_gate"}, [][]string{
			{"a", "building", "1", "-", "-", "Greet", "app", "unit", "-"},
			{"b", "building", "1", "-", "-", "Bow", "app", "unit", "-"},
			{"c", "queued", "0", "-", "-", "Wave", "app", "unit", "-"},
		}),
		axi.Table("last_runs", []string{"slice", "commit", "result", "covers", "seconds", "cpu_seconds", "peak_memory", "basis"}, [][]string{
			{"a", "aaaaaaaaaaaa", "pass", "yes", "4", "not measured", "not measured", "not measured"},
			{"b", "bbbbbbbbbbbb", "fail", "no: slice's last commit is cccccccccccc", "9", "not measured", "not measured", "not measured"},
		}),
		"questions: none",
		axi.Help("Run `jig next JIG-1`"),
	}, "\n") + "\n"
	g.r.answer("status JIG-1 --store /s", status, 0)
	g.feed(jl(at(0, 1), "JIG-1", "a", journal.EventClaim, with("s-builder", "", "", "", "", ""))).press("enter")
	text := pageText(g)
	for _, want := range []string{"last run: pass · covers: yes", "last run: fail · covers: no: slice's last commit is cccccccccccc"} {
		if !strings.Contains(text, want) {
			t.Errorf("the build screen lacks %q:\n%s", want, text)
		}
	}
	if strings.Count(text, "last run:") != 2 {
		t.Errorf("a slice with no run says something of one:\n%s", text)
	}
}
