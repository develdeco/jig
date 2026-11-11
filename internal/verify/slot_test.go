package verify

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/slot"
)

const slotManifest = `workspaces:
  - id: alpha
    path: alpha
  - id: beta
    path: beta
oracles:
  test: "echo full {path}"
`

// slotRig is a rig whose project has one suite slot and whose manifest marks
// the oracle heavy (or not), with commits of slices a and b to verify.
func slotRig(t *testing.T, heavy bool) (*rig, [2]string) {
	t.Helper()
	r := newRig(t, fixture.Opts{})
	r.deps.Project, r.deps.Slots = "slotproj", 1
	manifest := slotManifest
	if heavy {
		manifest += "heavy: [test]\n"
	}
	a := r.commit("a", "declare", map[string]string{".claude/jig.yaml": manifest, "a.txt": "a"})
	b := r.commit("b", "second", map[string]string{"b.txt": "b"})
	return r, [2]string{a, b}
}

// runBoth verifies slices a and b at once and returns the most oracles that
// ran at the same time. Each oracle lingers (or, when overlap is wanted, waits
// for the other to arrive).
func runBoth(t *testing.T, r *rig, shas [2]string, wantOverlap bool) int32 {
	t.Helper()
	var running, peak atomic.Int32
	arrived := make(chan struct{}, 2)
	r.deps.Oracle = func(cmd, dir string, env []string, limit time.Duration) (string, error) {
		n := running.Add(1)
		for {
			p := peak.Load()
			if n <= p || peak.CompareAndSwap(p, n) {
				break
			}
		}
		arrived <- struct{}{}
		if wantOverlap {
			deadline := time.After(10 * time.Second)
			for len(arrived) < 2 {
				select {
				case <-deadline:
					running.Add(-1)
					return "", nil
				case <-time.After(10 * time.Millisecond):
				}
			}
		} else {
			time.Sleep(400 * time.Millisecond)
		}
		running.Add(-1)
		return "", nil
	}
	var wg sync.WaitGroup
	for i, slice := range []string{"a", "b"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, err := Run(r.deps, r.request(slice, shas[i]))
			if err != nil || res.State != journal.VerifyPass {
				t.Errorf("Run %s = %+v, %v", slice, res, err)
			}
		}()
	}
	wg.Wait()
	return peak.Load()
}

func count(lines []journal.Line, event string) int {
	n := 0
	for _, l := range lines {
		if l.Event == event {
			n++
		}
	}
	return n
}

// TestHeavyOraclesOfOneProjectRunOneAfterTheOther: two verifications whose
// heavy oracles start together never overlap; the one that waited journals
// slot-wait, and both journal slot-taken.
func TestHeavyOraclesOfOneProjectRunOneAfterTheOther(t *testing.T) {
	t.Parallel()
	r, shas := slotRig(t, true)
	if peak := runBoth(t, r, shas, false); peak != 1 {
		t.Errorf("%d heavy oracles ran at once, want 1", peak)
	}
	lines := r.lines()
	if got := count(lines, journal.EventSlotWait); got != 1 {
		t.Errorf("slot-wait lines = %d, want 1 (the second run waited)", got)
	}
	if got := count(lines, journal.EventSlotTaken); got != 2 {
		t.Errorf("slot-taken lines = %d, want 2", got)
	}
}

// TestAnOracleTheManifestDoesNotMarkHeavyTakesNoSlot: with no heavy list both
// oracles run at once and nothing is journaled about a slot.
func TestAnOracleTheManifestDoesNotMarkHeavyTakesNoSlot(t *testing.T) {
	t.Parallel()
	r, shas := slotRig(t, false)
	if peak := runBoth(t, r, shas, true); peak != 2 {
		t.Errorf("%d oracles ran at once, want 2", peak)
	}
	lines := r.lines()
	if count(lines, journal.EventSlotWait)+count(lines, journal.EventSlotTaken) != 0 {
		t.Errorf("a non-heavy oracle journaled a slot: %v", events(lines))
	}
}

// TestTheOracleStartsOnlyOnceTheSlotIsTaken: while another holder has the
// project's slot the oracle (and so its bound) does not start; it starts when
// the slot is let go, and slot-wait is journaled before the oracle runs.
func TestTheOracleStartsOnlyOnceTheSlotIsTaken(t *testing.T) {
	t.Parallel()
	r, shas := slotRig(t, true)
	release, err := slot.Take(context.Background(), r.fx.Home, "slotproj", 1, "", nil)
	if err != nil {
		t.Fatal(err)
	}
	var started atomic.Bool
	var waitedBefore atomic.Bool
	r.deps.Oracle = func(cmd, dir string, env []string, limit time.Duration) (string, error) {
		started.Store(true)
		waitedBefore.Store(count(r.lines(), journal.EventSlotWait) == 1)
		return "", nil
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		if res, err := Run(r.deps, r.request("a", shas[0])); err != nil || res.State != journal.VerifyPass {
			t.Errorf("Run = %+v, %v", res, err)
		}
	}()
	for deadline := time.Now().Add(30 * time.Second); count(r.lines(), journal.EventSlotWait) == 0; {
		if time.Now().After(deadline) {
			t.Fatal("the run never said it waits for the slot")
		}
		time.Sleep(20 * time.Millisecond)
	}
	time.Sleep(200 * time.Millisecond)
	if started.Load() {
		t.Fatal("the oracle started while another holder had the slot")
	}
	release()
	select {
	case <-done:
	case <-time.After(20 * time.Second):
		t.Fatal("the run never got the slot after the release")
	}
	if !started.Load() || !waitedBefore.Load() {
		t.Errorf("started %v, slot-wait journaled before the oracle %v", started.Load(), waitedBefore.Load())
	}
}
