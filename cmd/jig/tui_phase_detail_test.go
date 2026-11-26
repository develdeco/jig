package main

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/verifydeliver"
)

// TestTUIPipeFramesShowAPhaseAsAListAndItsDetail follows a ticket through the real
// verbs and the screens of jig tui on a pipe at 100x30: the build screen prints a
// Slices panel with a row for each slice and, under it, the first slice's oracle
// once, a down moves the detail to the next slice; a round that raised a finding
// closes, and the review screen prints the round's detail with the finding and
// the fix slice it became, which the build screen then reads as its finding by
// round.
func TestTUIPipeFramesShowAPhaseAsAListAndItsDetail(t *testing.T) {
	t.Parallel()
	fx := newFixture(t, fixture.Opts{ScenarioBranch: "reviewer"})
	e := testEnv(fx.Home)
	ticket := fx.Ticket
	buildFixtureTicket(t, fx)
	flags := []string{"--store", fx.StoreDir}
	must := func(args ...string) string {
		t.Helper()
		out, code := runMain(t, e, "", append(args, flags...)...)
		if code != 0 {
			t.Fatalf("jig %s: exit = %d\n%s", strings.Join(args, " "), code, out)
		}
		return out
	}
	must("gate", ticket)
	var req verifydeliver.ReviewRequest
	if err := json.Unmarshal([]byte(must("review", "context", ticket, "--json")), &req); err != nil {
		t.Fatalf("review context --json: %v", err)
	}
	must("finding", ticket, "--file", req.MustReview[0], "--line", "1", "--title", "Add overflows past the int range",
		"--detail", "Say what happens past the int range.", "--action", "fix", "--risk", "medium", "--risk-rationale", "a silent wrap",
		"--oracle", "go test ./alpha")
	must("review", "done", ticket, "--reviewed", strings.Join(req.MustReview, ","), "--summary", "one fix")
	status := must("status", ticket)
	if !strings.Contains(status, "from_gate,findings}:") || !strings.Contains(status, ",1,r1-f1\n") {
		t.Fatalf("the round did not send a fix slice back that names its finding:\n%s", status)
	}

	keys, frames, wait := startTUI(t, e, ticket, "--store", fx.StoreDir)
	frames.untilWords(t, "4 of 5 green") // the digest was read: the step keys open
	keys.Write([]byte("2"))              // the build screen
	frames.untilWords(t, "slice state tries took goal")
	build := lastScreen(frames.String())
	for _, id := range []string{"a", "b", "c", "d"} {
		if !strings.Contains(between(build, "Slices", "╰"), " "+id+" ") {
			t.Errorf("the Slices panel has no row for slice %s:\n%s", id, build)
		}
	}
	first := between(build, "╭─ Detail · a", "╰")
	if strings.Count(first, "oracle") != 1 || !strings.Contains(first, "workspace") {
		t.Errorf("the detail of the first slice does not say its oracle and workspace once:\n%s", build)
	}
	if !strings.Contains(build, "round 1 · alpha/") {
		t.Errorf("the fix slice's row does not read as its finding by round:\n%s", build)
	}
	keys.Write([]byte("\x1b[B")) // down: the next slice
	frames.until(t, "╭─ Detail · b")

	keys.Write([]byte("3")) // the review screen
	frames.until(t, "╭─ Rounds")
	review := lastScreen(frames.String())
	if strings.Contains(review, "Sent back") {
		t.Errorf("the review screen has a panel of the fix slices sent back:\n%s", review)
	}
	if !strings.Contains(review, "╭─ Detail · round 1") {
		t.Fatalf("the review screen has no detail of its round:\n%s", review)
	}
	detail := between(review, "╭─ Detail · round 1", "╰")
	for _, want := range []string{"Add overflows past the int range", "to fix", "fix slice fix-"} {
		if !strings.Contains(detail, want) {
			t.Errorf("the round's detail lacks %q:\n%s", want, review)
		}
	}
	keys.Write([]byte("q"))
	if code := wait(); code != 0 {
		t.Fatalf("exit %d\n%s", code, frames.String())
	}
}

// between is the text of s after the first from and before the next to.
func between(s, from, to string) string {
	_, rest, _ := strings.Cut(s, from)
	out, _, _ := strings.Cut(rest, to)
	return out
}
