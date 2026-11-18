package main

import (
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/journal"
)

// TestHereAndStatusShowNoWaitingSection: nothing in jig waits for a run, so
// `jig here` and `jig status` show no waiting section, in their text or their
// JSON, even over a journal written when runs did wait.
func TestHereAndStatusShowNoWaitingSection(t *testing.T) {
	t.Parallel()
	r := newVerifyRig(t)
	for _, ev := range []string{"slot-wait", "slot-taken"} {
		line := journal.Line{Event: ev, Slice: "a", Summary: "waiting for the suite slot: OTHER/x is running its suite (1 waiting)"}
		if err := journal.Append(r.st, r.fx.Ticket, line); err != nil {
			t.Fatal(err)
		}
	}
	for _, args := range [][]string{{"here"}, {"here", "--json"}, {"status", r.fx.Ticket}} {
		out, code := runMain(t, r.e, "", args...)
		if code != 0 {
			t.Fatalf("jig %s exit %d:\n%s", strings.Join(args, " "), code, out)
		}
		if strings.Contains(out, "waiting[") || strings.Contains(out, `"waiting":`) {
			t.Errorf("jig %s shows a waiting section:\n%s", strings.Join(args, " "), out)
		}
	}
}
