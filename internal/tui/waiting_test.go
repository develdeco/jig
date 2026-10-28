package tui

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// awaitRig is a screen opened on SHOP-1 before the store has a folder for it:
// the ticket list does not name it and a status of it is refused.
func awaitRig(t *testing.T) *rig {
	t.Helper()
	r := newRunner()
	r.answer("status SHOP-1 --store /s", "error: ticket SHOP-1 not found in the store at /s\ncode: VALIDATION_ERROR\n", 2)
	m := New(Config{
		RetryDelay: time.Millisecond, AwaitEvery: -1, IdleTick: -1, Run: r.run, Ticket: "SHOP-1", Awaiting: true,
		Scope: []string{"--store", "/s"}, Project: "demo", Zone: time.UTC, Width: 80, Height: 24,
	})
	return &rig{t: t, m: m, r: r}
}

// statusReads counts the reads of ticket's status the runner has been asked for.
func (f *fakeRunner) statusReads(ticket string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	n := 0
	for _, c := range f.calls {
		if len(c.Args) > 1 && c.Args[0] == "status" && c.Args[1] == ticket {
			n++
		}
	}
	return n
}

// rosterWith is `jig next --json` once the store has SHOP-1 too.
func rosterWith(t *testing.T) string {
	t.Helper()
	data, err := json.Marshal(map[string]any{"tickets": []map[string]any{
		{"id": "JIG-1", "title": "Greeting copy", "state": "ready"},
		{"id": "SHOP-1", "title": "Cart totals", "state": "ready"},
	}})
	if err != nil {
		t.Fatal(err)
	}
	return string(data) + "\n"
}

// TestATicketThatDoesNotExistYetIsWaitedFor: `jig tui SHOP-1` before the
// session has planned SHOP-1 opens its screen with a line saying so. Nothing is
// read of it, nothing is refused, and no key acts on it.
func TestATicketThatDoesNotExistYetIsWaitedFor(t *testing.T) {
	t.Parallel()
	g := awaitRig(t)
	g.feed()
	golden(t, "ticket-waiting", g.view())

	if n := g.r.statusReads("SHOP-1"); n != 0 {
		t.Errorf("the status of a ticket that does not exist was read %d times, want none", n)
	}
	if g.m.notice != "" {
		t.Errorf("notice = %q, want none while waiting", g.m.notice)
	}
	for _, key := range []string{"g", "p"} {
		g.press(key)
		if !strings.Contains(g.m.notice, "SHOP-1 does not exist yet") || g.m.ask != nil {
			t.Errorf("after %q: notice %q, confirmation %v, want it refused", key, g.m.notice, g.m.ask)
		}
	}
	if lines := g.r.lines(); len(lines) != 0 {
		t.Errorf("the keys ran %v, want no command", lines)
	}
}

// TestAWaitedTicketFillsInWhenTheTicketListNamesIt: the ticket list is read
// again while the ticket is awaited, and a ticket that exists but has no line
// yet shows there; its status is read and the steps appear.
func TestAWaitedTicketFillsInWhenTheTicketListNamesIt(t *testing.T) {
	t.Parallel()
	g := awaitRig(t)
	g.feed()
	g.m.awaitEvery = time.Millisecond
	if g.m.awaitAfter() == nil {
		t.Fatalf("a waited ticket arms no poll of the ticket list")
	}

	g.r.answer("next --json --store /s", rosterWith(t), 0)
	g.r.answer("status SHOP-1 --store /s", statusSHOP1(), 0)
	g.m.awaitEvery = -1
	g.send(awaitMsg{})
	if g.m.awaitingTicket() {
		t.Fatalf("the ticket is still awaited after the ticket list named it")
	}
	v := g.view()
	for _, want := range []string{"› SHOP-1  Cart totals", "╭─ Steps", "Build ─"} {
		if !strings.Contains(v, want) {
			t.Errorf("the screen lacks %q once the ticket exists:\n%s", want, v)
		}
	}
	if strings.Contains(v, "waiting for SHOP-1") {
		t.Errorf("the screen still says it waits:\n%s", v)
	}
	if _, cmd := g.m.Update(awaitMsg{}); cmd != nil {
		t.Errorf("a ticket that exists is still polled for")
	}
}

// TestAWaitedTicketFillsInOnItsFirstLine: the first journal line of the ticket
// is enough, with the ticket list still silent.
func TestAWaitedTicketFillsInOnItsFirstLine(t *testing.T) {
	t.Parallel()
	g := awaitRig(t)
	g.r.answer("status SHOP-1 --store /s", statusSHOP1(), 0)
	g.feed()
	if !strings.Contains(g.view(), "waiting for SHOP-1 to be created") {
		t.Fatalf("not waiting:\n%s", g.view())
	}
	g.feed(shop1()[0])
	v := g.view()
	if !strings.Contains(v, "╭─ Steps") || strings.Contains(v, "waiting for SHOP-1") {
		t.Errorf("the first line did not fill the screen in:\n%s", v)
	}
	if n := g.r.statusReads("SHOP-1"); n == 0 {
		t.Errorf("the status was never read once the ticket had a line")
	}
}

// TestATicketOpenedTheUsualWayDoesNotWait: only a ticket asked to be awaited is.
func TestATicketOpenedTheUsualWayDoesNotWait(t *testing.T) {
	t.Parallel()
	g := newRig(t, "JIG-1")
	g.feed()
	if g.m.awaitingTicket() || strings.Contains(g.view(), "waiting for") {
		t.Errorf("a ticket opened without Awaiting waits:\n%s", g.view())
	}
}
