package e2e

import (
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// TestTwoGatesAtOnceOpenOneRound: two `jig gate` processes of one ticket
// started together wait for nothing and the ticket ends with one open round at
// the branch's head. They share the pool's gate lease and the branch's refs,
// which keep their own locking: a collision there may fail one gate with git's
// error, never with a wait, and a rerun reports the round that stands.
func TestTwoGatesAtOnceOpenOneRound(t *testing.T) {
	b := newBuiltTicket(t, true)

	// The first gate makes the ticket's gate lease, a clone that two gates
	// arriving together would both try to make; the branch then moves, so the
	// two gates open a round at a new head and race only in the lease's reuse.
	if r := b.builder.Jig("gate", b.fx.Ticket); r.Code != 0 {
		t.Fatalf("the gate that makes the lease: exit = %d\nstdout:\n%s\nstderr:\n%s", r.Code, r.Stdout, r.Stderr)
	}
	b.builder.Commit("", "the branch moves", map[string]string{"moved.txt": "moved\n"})

	var wg sync.WaitGroup
	results := make([]jigResult, 2)
	for i := range results {
		wg.Add(1)
		go func() {
			defer wg.Done()
			results[i] = b.builder.Jig("gate", b.fx.Ticket)
		}()
	}
	wg.Wait()
	opened := 0
	for i, r := range results {
		if strings.Contains(r.Stderr, "waits for the gate") {
			t.Errorf("gate %d waited:\n%s", i, r.Stderr)
		}
		if r.Code == 0 {
			opened++
		}
	}
	if opened == 0 {
		t.Fatalf("neither gate succeeded:\n%s\n%s", results[0].Stdout, results[1].Stdout)
	}
	open, err := filepath.Glob(b.workFile("gate.round-*.open.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if len(open) != 1 || filepath.Base(open[0]) != "gate.round-1.open.yaml" {
		t.Errorf("open records = %v, want round 1's alone", open)
	}
	if r := b.builder.Jig("gate", b.fx.Ticket); r.Code != 0 || !strings.Contains(r.Stdout, "round: 1 stands at its head (not opened again)") {
		t.Errorf("a gate after them: exit = %d, want the standing round\nstdout:\n%s\nstderr:\n%s", r.Code, r.Stdout, r.Stderr)
	}
}
