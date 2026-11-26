package main

import (
	"strings"
	"testing"
	"time"

	"github.com/develdeco/jig/internal/journal"
)

// TestTUIPipeFramesShowTheWholeStoryOfASlice follows a slice through the real
// store and the screens of jig tui on a pipe at 100x30: the build screen prints
// the slices, enter on one prints its goal and oracle whole, its commit with the
// run that failed it and the run that passed, who ran them, and the timeline
// last.
func TestTUIPipeFramesShowTheWholeStoryOfASlice(t *testing.T) {
	t.Parallel()
	fx, st, e := workFixture(t)
	T := fx.Ticket
	now := time.Now().UTC()
	line := func(ago time.Duration, l journal.Line) {
		t.Helper()
		l.TS = now.Add(-ago).Format(time.RFC3339Nano)
		if err := journal.Append(st, T, l); err != nil {
			t.Fatal(err)
		}
	}
	const head = "0123456789abcdef0123456789abcdef01234567"
	line(5*time.Minute, journal.Line{Event: journal.EventClaim, Slice: "a", Session: "s-1"})
	line(4*time.Minute, journal.Line{Event: journal.EventOracle, Slice: "a", Session: "s-1", Commit: head, Outcome: "fail", Seconds: 12, Command: "go test ./...",
		Subject: "Greet by name", Summary: "--- FAIL: TestGreet (0.00s) want Ada, got friend"})
	line(3*time.Minute, journal.Line{Event: journal.EventOracle, Slice: "a", Session: "s-1", Commit: head, Outcome: "pass", Seconds: 14, Command: "go test ./...", Subject: "Greet by name"})

	keys, frames, wait := startTUI(t, e, T, "--store", fx.StoreDir)
	// The step keys open once the roster has been read: pressed again until the
	// screen opens.
	for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); {
		keys.Write([]byte("2"))
		for i := 0; i < 40 && !strings.Contains(lastScreen(frames.String()), "╭─ Slices"); i++ {
			time.Sleep(25 * time.Millisecond)
		}
		if strings.Contains(lastScreen(frames.String()), "╭─ Slices") {
			break
		}
	}
	frames.untilWords(t, "slice state tries took goal")
	keys.Write([]byte("\r")) // enter on the first slice
	story := untilFrame(t, frames, "the screen of slice a", func(f string) bool { return strings.Contains(f, "╭─ Slice a") })
	flat := strings.Join(strings.Fields(story), " ")
	for _, want := range []string{"goal", "oracle", "Commits", "Greet by name", "run by builder, witnessed by jig"} {
		if !strings.Contains(flat, want) {
			t.Errorf("the screen of slice a lacks %q:\n%s", want, story)
		}
	}
	if strings.Contains(flat, "jig ran it") || strings.Contains(flat, "the session ran it") {
		t.Errorf("a run is worded by who started it:\n%s", story)
	}
	// The timeline is last: read to the end of the page.
	for i := 0; i < 40 && !strings.Contains(lastScreen(frames.String()), "Timeline"); i++ {
		keys.Write([]byte("\x1b[6~")) // page down
		time.Sleep(50 * time.Millisecond)
	}
	end := lastScreen(frames.String())
	if at, commits := strings.Index(end, "Timeline"), strings.Index(end, "Commits"); at < 0 || (commits >= 0 && commits > at) {
		t.Errorf("the timeline is not the last panel:\n%s", end)
	}

	t.Logf("STORY\n%s\nEND\n%s", story, end)
	keys.Write([]byte("q"))
	if code := wait(); code != 0 {
		t.Fatalf("exit %d\n%s", code, frames.String())
	}
}
