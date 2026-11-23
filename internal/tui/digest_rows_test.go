package tui

import (
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/phase"
)

// TestAStepThatCannotStartIsDrawnQueued: the digest says whether the phase a
// ticket stands at can start. A step that cannot (it waits for a blocker, not
// for the person) is queued, not ready, in the build, the review and the
// publish; one that can is ready.
func TestAStepThatCannotStartIsDrawnQueued(t *testing.T) {
	t.Parallel()
	pending := "pending|after the step before"
	for _, tc := range []struct {
		name  string
		d     func(ready bool) *digestInfo
		step  int
		ready string
	}{
		{"the build", func(r bool) *digestInfo {
			d := task("build", r, "done|approved", "current|waits for JIG-4 to merge", pending, pending, pending)
			d.Slices = &digestSlices{Total: 2, Queued: 2}
			return d
		}, stepBuild, StateReady},
		{"the review", func(r bool) *digestInfo {
			return task("review", r, "done|approved", "done|2 slices green", "current|nothing yet", pending, pending)
		}, stepReview, StateReady},
		{"the publish", func(r bool) *digestInfo {
			return task("publish", r, "done|approved", "done|2 slices green", "done|round 1 clean", "current|nothing yet", pending)
		}, stepPublish, StateReady},
	} {
		g := newRig(t, "")
		g.m.w.setRoster([]rosterRow{{ID: "JIG-1", Title: "Ticket", Digest: tc.d(false)}})
		if got := g.m.steps(g.m.w.tickets["JIG-1"])[tc.step]; got.State != StateQueued {
			t.Errorf("%s cannot start and is drawn %s, want queued", tc.name, got.State)
		}
		g.m.w.setRoster([]rosterRow{{ID: "JIG-1", Title: "Ticket", Digest: tc.d(true)}})
		if got := g.m.steps(g.m.w.tickets["JIG-1"])[tc.step]; got.State != tc.ready {
			t.Errorf("%s can start and is drawn %s, want %s", tc.name, got.State, tc.ready)
		}
	}
}

// TestMainsRowsSayTheKindAndWhatTheDigestSays: the project's list names an
// issue and a multi-task by their kind and their own pull request's state, a
// task of one by the ticket it goes into, and a task published says so.
func TestMainsRowsSayTheKindAndWhatTheDigestSays(t *testing.T) {
	t.Parallel()
	g := newRig(t, "")
	child := task("build", true, "done|approved", "current|ready to build", "pending|after the build", "pending|after a clean review", "pending|after the publish")
	child.Parent = "JIG-80"
	published := task("land", false, "done|approved", "done|1 slice green", "done|round 1 clean", "done|pull request opened", "current|waits for a merge")
	g.m.w.setRoster([]rosterRow{
		{ID: "JIG-80", Title: "An issue", Digest: &digestInfo{Kind: "issue", State: "waiting"}},
		{ID: "JIG-81", Title: "A multi-task", Digest: &digestInfo{Kind: "multi-task", State: "ready"}},
		{ID: "JIG-82", Title: "A task of the issue", Digest: child},
		{ID: "JIG-83", Title: "A published task", Digest: published},
		{ID: "JIG-84", Title: "A broken ticket", Digest: &digestInfo{Kind: "task", PhaseProblem: "its slices.yaml cannot be read"}},
	})
	rows := map[string]string{}
	for _, line := range strings.Split(pagesText(g.m.mainPage()), "\n") {
		for _, id := range []string{"JIG-80", "JIG-81", "JIG-82", "JIG-83", "JIG-84"} {
			if strings.Contains(line, id+"  ") {
				rows[id] = line
			}
		}
	}
	for id, want := range map[string]string{
		"JIG-80": "issue · waiting",
		"JIG-81": "multi-task · ready",
		"JIG-82": "task of JIG-80 · Build: ready to build",
		"JIG-83": "published",
		"JIG-84": "its slices.yaml cannot be read",
	} {
		if !strings.Contains(rows[id], want) {
			t.Errorf("%s's row is %q, want it to say %q", id, rows[id], want)
		}
	}
	if strings.Contains(rows["JIG-83"], "task of") || strings.Contains(rows["JIG-80"], "Brief:") {
		t.Errorf("a plain task says a kind, or an issue is worded as a brief:\n%s\n%s", rows["JIG-83"], rows["JIG-80"])
	}
}

// TestParseRosterReadsWhatTheDigestSaysOfBehind: a ticket the digest says is
// behind is behind on the screens, from the roster's own JSON.
func TestParseRosterReadsWhatTheDigestSaysOfBehind(t *testing.T) {
	t.Parallel()
	rows, _, err := parseRoster(`{"tickets":[{"id":"JIG-1","state":"done","digest":{"kind":"task","parent":"JIG-80","current":"publish","ready":true,"behind":true,"phases":[{"name":"publish","state":"current","text":"x"}]}}]}` + "\n")
	if err != nil || len(rows) != 1 || rows[0].Digest == nil {
		t.Fatalf("rows = %+v, %v", rows, err)
	}
	if d := rows[0].Digest; !d.Behind || d.Parent != "JIG-80" || d.Current != string(phase.Publish) || !d.Ready {
		t.Errorf("the digest = %+v, want behind, a task of JIG-80, at its publish, ready", d)
	}
}
