package main

import (
	"context"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/project"
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

// TestStatusShowsTheWaitingTableFromTheCommandsJigHome: `jig status` names the
// run that waits for a slot of any project the store serves, reading the slots
// under the command's own jig home.
func TestStatusShowsTheWaitingTableFromTheCommandsJigHome(t *testing.T) {
	t.Parallel()
	r := newVerifyRig(t)
	cfg, err := project.LoadStoreConfig(r.fx.StoreDir)
	if err != nil || cfg.Name == "" {
		t.Fatalf("store config = %+v, %v", cfg, err)
	}
	if err := journal.Append(r.st, r.fx.Ticket, journal.Line{Event: journal.EventSlotWait, Slice: "a", Summary: "waiting"}); err != nil {
		t.Fatal(err)
	}
	holder, err := slot.Take(context.Background(), r.fx.Home, cfg.Name, 1, slot.Who("DEMO-1", "x"), nil)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	waiting := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		if rel, err := slot.Take(ctx, r.fx.Home, cfg.Name, 1, slot.Who(r.fx.Ticket, "a"), func() { close(waiting) }); err == nil {
			rel()
		}
	}()
	defer func() {
		cancel()
		<-done
		holder()
	}()
	<-waiting

	want := "waiting for the suite slot: DEMO-1/x is running its suite (1 waiting)"
	out, code := runMain(t, r.e, "", "status", r.fx.Ticket)
	if code != 0 || !strings.Contains(out, "waiting[1]{slice,line}:") || !strings.Contains(out, want) {
		t.Errorf("jig status (exit %d) lacks the waiting table with %q:\n%s", code, want, out)
	}

	// A store that serves several projects names none of its own: every one it
	// serves is read.
	multi := project.Config{Projects: []string{"other", cfg.Name}}
	blocks, err := waitingBlocks(r.fx.Home, multi)(r.st, r.fx.Ticket)
	if err != nil || len(blocks) != 1 || !strings.Contains(blocks[0], want) {
		t.Errorf("waitingBlocks for a multi-project store = %v, %v; want the table with %q", blocks, err, want)
	}
	if blocks, _ := waitingBlocks(t.TempDir(), multi)(r.st, r.fx.Ticket); len(blocks) != 0 {
		t.Errorf("another jig home shows the wait: %v", blocks)
	}
}
