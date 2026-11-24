package tui

import (
	"reflect"
	"strings"
	"testing"
)

// bodyOut is what `jig publish --body` prints: the what row (or one for each
// repo) and the help lines.
func bodyOut(whatRows []string, help ...string) string {
	out := "publish_body:\n  ticket: JIG-2\n  branch: jig-2-config\n"
	for _, r := range whatRows {
		out += "  " + r + "\n"
	}
	out += "help[" + string(rune('0'+len(help))) + "]:\n"
	for _, h := range help {
		out += "  " + h + "\n"
	}
	return out
}

const whatHelpLine = "Write the pull request's What, a few bullets with the first saying why the change exists, to /home/what/JIG-2.md, then give it: `jig publish JIG-2 --body --what /home/what/JIG-2.md`"

// TestPublishStopsOnAPlaceholderWhat: p renders the body first; a pending What
// asks nothing and pushes nothing, and the verb's own help line for it is on the
// frame whole.
func TestPublishStopsOnAPlaceholderWhat(t *testing.T) {
	t.Parallel()
	for name, rows := range map[string][]string{
		"one repo":  {"what: pending", "what_request: /home/what/JIG-2.md"},
		"two repos": {"what_api: stored", "what_web: pending", "what_request_web: /home/what/JIG-2.md"},
	} {
		g := quietRigAt(t, "JIG-2", publishReady())
		g.r.answer("publish JIG-2 --body --store /s", bodyOut(rows, whatHelpLine, "Push the branch and open the pull request"), 0)
		g.feed(jig2Mid()...).press("p")
		if g.m.ask != nil {
			t.Fatalf("%s: p asked to publish with a What nobody wrote:\n%s", name, g.view())
		}
		if got, want := g.r.lines(), []string{"publish JIG-2 --body --store /s"}; !reflect.DeepEqual(got, want) {
			t.Fatalf("%s: commands run = %q, want only the render", name, got)
		}
		g.press("y")
		if got := len(g.r.lines()); got != 1 {
			t.Errorf("%s: y after the stop ran %q", name, g.r.lines())
		}
		rows := panelBelowCommand(g.view())
		var said []string
		for _, r := range rows[1:] {
			said = append(said, strings.TrimSpace(r))
		}
		if len(rows) < 3 || rows[0] != "  ✓ jig publish JIG-2 --body" || strings.Join(said, " ") != whatHelpLine {
			t.Errorf("%s: the panel does not show the verb's help line whole:\n%s", name, g.view())
		}
		if strings.Contains(g.view(), "Push the branch") {
			t.Errorf("%s: a second help line is on the frame", name)
		}
	}
}

// TestPublishAsksOnAStoredWhat: with every What given or stored p asks the same
// confirmation as before and runs the approval and the publish on y; a render the
// verb refuses is shown as a refusal and asks nothing.
func TestPublishAsksOnAStoredWhat(t *testing.T) {
	t.Parallel()
	g := quietRigAt(t, "JIG-2", publishReady())
	g.r.answer("publish JIG-2 --body --store /s", bodyOut([]string{"what: stored"}, "Push the branch"), 0)
	g.feed(jig2Mid()...).press("p")
	if g.m.ask == nil || !strings.Contains(g.view(), "publish JIG-2: approve the reviewed head, push it and open the pull request?") {
		t.Fatalf("p did not ask on a stored What:\n%s", g.view())
	}
	if v := g.view(); strings.Contains(v, "jig publish JIG-2 --body") {
		t.Errorf("the render left a line of its own beside the question:\n%s", v)
	}
	g.press("y")
	want := []string{"publish JIG-2 --body --store /s", "publish JIG-2 --approve --store /s", "publish JIG-2 --yes --store /s"}
	if got := g.r.lines(); !reflect.DeepEqual(got, want) {
		t.Errorf("commands run = %q, want the render, the approval and the publish", got)
	}

	g = quietRigAt(t, "JIG-2", publishReady())
	g.r.answer("publish JIG-2 --body --store /s", "error: JIG-2 is behind\ncode: PUBLISH_BEHIND\n", 1)
	g.feed(jig2Mid()...).press("p")
	if g.m.ask != nil {
		t.Errorf("p asked to publish after the render was refused")
	}
	if v := g.view(); !strings.Contains(v, "✗ jig publish JIG-2 --body  JIG-2 is behind (PUBLISH_BEHIND)") {
		t.Errorf("the refusal is not on the frame:\n%s", v)
	}
}
