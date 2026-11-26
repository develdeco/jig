package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/verifydeliver"
)

// TestTUIPipeFramesWalkARoundToItsFixSlice follows a round that raised a fix
// through jig tui on a pipe at 100x30 over the real store, with keys: the review
// screen prints the round, two tabs reach its finding, enter prints the
// finding whole with its fix slice, enter again prints the slice with the finding
// named, and two esc print the review screen again.
func TestTUIPipeFramesWalkARoundToItsFixSlice(t *testing.T) {
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
		"--detail", "Say what happens past the int range.\nThen test it.", "--action", "fix", "--risk", "medium", "--risk-rationale", "a silent wrap",
		"--oracle", "go test ./alpha")
	must("review", "done", ticket, "--reviewed", strings.Join(req.MustReview, ","), "--summary", "one fix")

	keys, frames, wait := startTUI(t, e, ticket, "--store", fx.StoreDir)
	// A key sent before the screen can take it is lost: it is sent again until the
	// last screen holds want.
	press := func(key, want string) string {
		t.Helper()
		for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); {
			keys.Write([]byte(key))
			for i := 0; i < 40; i++ {
				if screen := lastScreen(frames.String()); strings.Contains(screen, want) {
					return screen
				}
				time.Sleep(25 * time.Millisecond)
			}
		}
		t.Fatalf("after %q the screen never held %q; the last screen was:\n%s", key, want, lastScreen(frames.String()))
		return ""
	}
	// A key that must be sent once: moves are not idempotent.
	once := func(key, want string) string {
		t.Helper()
		keys.Write([]byte(key))
		for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); time.Sleep(25 * time.Millisecond) {
			if screen := lastScreen(frames.String()); strings.Contains(screen, want) {
				return screen
			}
		}
		t.Fatalf("after %q the screen never held %q; the last screen was:\n%s", key, want, lastScreen(frames.String()))
		return ""
	}

	review := press("3", "╭─ Rounds")
	if !strings.Contains(between(review, "╭─ Rounds", "╰"), "round 1") {
		t.Errorf("the review screen does not print the round:\n%s", review)
	}
	once("\t", "move in Asks")
	once("\t", "move in Detail · round 1")
	finding := once("\r", "╭─ Finding r1-f1")
	for _, want := range []string{"Add overflows past the int range", "Say what happens past the int range.", "Then test it.", "a silent wrap", "Fix slices", "fix-"} {
		if !strings.Contains(finding, want) {
			t.Errorf("the finding screen lacks %q:\n%s", want, finding)
		}
	}
	slice := once("\r", "╭─ Slice fix-")
	if panel := between(slice, "╭─ Finding", "╰"); !strings.Contains(panel, "round 1") || !strings.Contains(panel, "r1-f1") {
		t.Errorf("the slice screen's Finding panel does not name the round and the finding:\n%s", slice)
	}
	once("\x1b", "╭─ Finding r1-f1")
	back := once("\x1b", "╭─ Rounds")
	if !strings.Contains(back, "╭─ Detail · round 1") || !strings.Contains(back, "▸") {
		t.Errorf("two esc did not print the review screen with its cursor:\n%s", back)
	}
	keys.Write([]byte("q"))
	if code := wait(); code != 0 {
		t.Fatalf("exit %d\n%s", code, frames.String())
	}
}
