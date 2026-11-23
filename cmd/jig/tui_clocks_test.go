package main

import (
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/develdeco/jig/internal/journal"
)

// untilFrame waits for the latest frame to satisfy ok.
func untilFrame(t *testing.T, out *tuiOut, what string, ok func(frame string) bool) string {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		if f := lastFrame(out.String()); ok(f) {
			return f
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("the screens never showed %s; the last frame was:\n%s", what, lastFrame(out.String()))
	return ""
}

// stepsRowOf is the line of a frame's Steps panel that names step.
func stepsRowOf(frame, step string) string {
	for _, l := range strings.Split(frame, "\n") {
		if strings.Contains(l, " "+step+" ") && strings.Contains(l, "│") {
			return l
		}
	}
	return ""
}

// TestTUIClocksCountWorkAndStopWithIt: a brief drafted ninety minutes ago in six
// seconds and not approved reads its six seconds on its row and in the Steps
// panel's total, and the hour and a half it has waited as a wait; once it is
// approved and the build claimed the row reads the same six seconds and the wait
// is gone. The same flow the package's rig tests, through the real stream.
func TestTUIClocksCountWorkAndStopWithIt(t *testing.T) {
	t.Parallel()
	fx, st, e := workFixture(t)
	T := fx.Ticket
	then := time.Now().UTC().Add(-90 * time.Minute)
	for _, at := range []time.Time{then.Add(-6 * time.Second), then} {
		if err := journal.Append(st, T, journal.Line{TS: at.Format(time.RFC3339Nano), Event: journal.EventRevalidate, Session: "s-planner"}); err != nil {
			t.Fatal(err)
		}
	}

	keys, out, wait := startTUI(t, e, T)
	waited := regexp.MustCompile(`waiting for your approval 1h 3[01]m`)
	frame := untilFrame(t, out, "the brief's wait of an hour and a half", func(f string) bool { return waited.MatchString(f) })
	row := stepsRowOf(frame, "Brief")
	if !strings.Contains(row, " 6s ") || !regexp.MustCompile(`worked 6s`).MatchString(frame) {
		t.Errorf("the brief drafted in 6 seconds reads\n%s\nwant its 6s on the row and in the total of work", frame)
	}
	if strings.Contains(frame, "total ") || strings.Contains(frame, "idle since") {
		t.Errorf("the frame counts a wait or idles a step that nobody works on:\n%s", frame)
	}

	// The person approves, and the builder claims a slice: the wait is over.
	sum, err := st.BriefSHA256(T)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := recordApproval(e, st, T, sum, "person"); err != nil {
		t.Fatal(err)
	}
	if err := journal.Append(st, T, journal.Line{Event: journal.EventClaim, Slice: "a", Session: "s-1"}); err != nil {
		t.Fatal(err)
	}
	frame = untilFrame(t, out, "the approved brief", func(f string) bool {
		return !strings.Contains(f, "waiting for your approval") && strings.Contains(stepsRowOf(f, "Build"), "Build")
	})
	if row := stepsRowOf(frame, "Brief"); !strings.Contains(row, " 6s ") || strings.Contains(row, "waiting") {
		t.Errorf("the approved brief reads\n%s\nwant the same 6s with no wait", frame)
	}

	keys.Write([]byte("q"))
	if code := wait(); code != 0 {
		t.Fatalf("exit %d\n%s", code, out.String())
	}
}
