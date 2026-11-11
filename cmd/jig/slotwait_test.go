package main

import (
	"context"
	"testing"

	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/slot"
)

// TestWaitRowsSayWhichRunWaitsForTheSlot: a ticket whose slice waits behind
// another run's suite gets one row naming the slice and the line; a gate round
// is "gate"; with no project there is nothing to say.
func TestWaitRowsSayWhichRunWaitsForTheSlot(t *testing.T) {
	root := t.TempDir()
	holder, err := slot.Take(context.Background(), root, "demo", 1, slot.Who("DEMO-1", "a"), nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	waiting := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		// Whatever the waiter takes, it lets go: a slot taken between the
		// cancel and the holder's release must not outlive the test.
		if rel, err := slot.Take(ctx, root, "demo", 1, slot.Who("DEMO-2", ""), func() { close(waiting) }); err == nil {
			rel()
		}
	}()
	// The waiter returns while the slot is still held, so it can only see the
	// cancel, never take the slot.
	defer func() {
		cancel()
		<-done
		holder()
	}()
	<-waiting

	lines := []journal.Line{{Event: journal.EventSlotWait}}
	rows := waitRows(suiteWaits(root, "demo", "DEMO-2", lines))
	want := "waiting for the suite slot: DEMO-1/a is running its suite (1 waiting)"
	if len(rows) != 1 || rows[0][0] != "gate" || rows[0][1] != want {
		t.Errorf("rows = %v, want the gate round with %q", rows, want)
	}
	if got := suiteWaits(root, "", "DEMO-2", lines); got != nil {
		t.Errorf("with no project: %v", got)
	}
	if got := suiteWaits(root, "demo", "DEMO-3", lines); got != nil {
		t.Errorf("another ticket's wait shows: %v", got)
	}
}
