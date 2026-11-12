package main

import (
	"bytes"
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/gatelock"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/store"
)

// lockedBuffer is a writer a test reads while a command still writes to it.
type lockedBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *lockedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *lockedBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// TestSecondGateWaitsForTheFirstAndSaysSo: a gate of a ticket whose lock is
// held names the holder on stderr, waits, and goes on once the holder lets go;
// a gate that did not wait prints neither line.
func TestSecondGateWaitsForTheFirstAndSaysSo(t *testing.T) {
	t.Parallel()
	jigHome := t.TempDir()
	e := testEnv(jigHome)
	fx := fixture.Generate(t, fixture.Opts{Home: jigHome})
	buildFixtureTicket(t, fx)

	var stderr lockedBuffer
	e.stderr = &stderr

	// No one holds the lock: no wait lines.
	if out, code := runMain(t, e, "", "gate", fx.Ticket, "--store", fx.StoreDir); code != 0 {
		t.Fatalf("gate: exit = %d, want 0\n%s", code, out)
	}
	if got := stderr.String(); strings.Contains(got, "waits for the gate") || strings.Contains(got, "took the lock") {
		t.Fatalf("a gate that did not wait said so:\n%s", got)
	}

	// Another gate holds the lock.
	release, err := gatelock.Take(context.Background(), jigHome, "fixture", fx.Ticket, nil)
	if err != nil {
		t.Fatal(err)
	}
	type result struct {
		out  string
		code int
	}
	done := make(chan result, 1)
	go func() {
		out, code := runMain(t, e, "", "gate", fx.Ticket, "--store", fx.StoreDir)
		done <- result{out, code}
	}()

	deadline := time.Now().Add(30 * time.Second)
	for !strings.Contains(stderr.String(), "waits for the gate of "+fx.Ticket+" (pid ") {
		if time.Now().After(deadline) {
			release()
			t.Fatalf("the second gate never said it waits:\n%s", stderr.String())
		}
		time.Sleep(20 * time.Millisecond)
	}
	select {
	case r := <-done:
		release()
		t.Fatalf("the second gate went on while the lock was held: exit = %d\n%s", r.code, r.out)
	case <-time.After(300 * time.Millisecond):
	}
	release()
	select {
	case r := <-done:
		if r.code != 0 {
			t.Fatalf("second gate: exit = %d, want 0\n%s", r.code, r.out)
		}
	case <-time.After(60 * time.Second):
		t.Fatal("the second gate did not go on after the lock was released")
	}
	if got := stderr.String(); !strings.Contains(got, "jig: gate "+fx.Ticket+" took the lock after ") {
		t.Errorf("the second gate did not say it took the lock:\n%s", got)
	}
	if _, ok := gatelock.Holder(jigHome, "fixture", fx.Ticket); ok {
		t.Error("the gate left its holder file after it returned")
	}
}

// TestSecondGateReportsTheRoundStandingAtItsHead: a gate that waited for
// another, whose round stands at the branch's head, opens no round and runs no
// suite; it reports that round. A gate that did not wait still opens one.
func TestSecondGateReportsTheRoundStandingAtItsHead(t *testing.T) {
	t.Parallel()
	jigHome := t.TempDir()
	e := testEnv(jigHome)
	fx := fixture.Generate(t, fixture.Opts{Home: jigHome})
	buildFixtureTicket(t, fx)
	var stderr lockedBuffer
	e.stderr = &stderr

	// The first gate opens round 1 (no wait).
	if out, code := runMain(t, e, "", "gate", fx.Ticket, "--store", fx.StoreDir); code != 0 || strings.Contains(out, "stands at its head") {
		t.Fatalf("first gate: exit = %d, want 0 and an ordinary report\n%s", code, out)
	}
	opens := func() int {
		st, err := store.Open(fx.StoreDir)
		if err != nil {
			t.Fatal(err)
		}
		lines, err := journal.Read(st, fx.Ticket)
		if err != nil {
			t.Fatal(err)
		}
		n := 0
		for _, l := range lines {
			if l.Event == journal.EventGateOpen {
				n++
			}
		}
		return n
	}
	before := opens()

	release, err := gatelock.Take(context.Background(), jigHome, "fixture", fx.Ticket, nil)
	if err != nil {
		t.Fatal(err)
	}
	type result struct {
		out  string
		code int
	}
	done := make(chan result, 1)
	go func() {
		out, code := runMain(t, e, "", "gate", fx.Ticket, "--store", fx.StoreDir)
		done <- result{out, code}
	}()
	deadline := time.Now().Add(30 * time.Second)
	for !strings.Contains(stderr.String(), "waits for the gate") {
		if time.Now().After(deadline) {
			release()
			t.Fatal("the second gate never waited")
		}
		time.Sleep(20 * time.Millisecond)
	}
	release()
	select {
	case r := <-done:
		if r.code != 0 || !strings.Contains(r.out, "round: 1 stands at its head (not opened again)") || !strings.Contains(r.out, "verdict: open") {
			t.Fatalf("second gate: exit = %d, want 0 and the standing round\n%s", r.code, r.out)
		}
	case <-time.After(60 * time.Second):
		t.Fatal("the second gate did not finish")
	}
	if after := opens(); after != before {
		t.Errorf("gate-open lines = %d, want %d: the waiting gate ran a gate of its own", after, before)
	}
}
