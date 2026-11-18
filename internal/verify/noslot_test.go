package verify

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/journal"
)

// TestAWholeSuiteOracleStartsAtOnceAndJournalsNoSlotLine: an oracle an older
// manifest marked heavy, run while a second run of the same oracle is in
// flight, starts without waiting for it and writes no slot line: nothing in
// jig admits a run, whatever else runs on the machine.
func TestAWholeSuiteOracleStartsAtOnceAndJournalsNoSlotLine(t *testing.T) {
	t.Parallel()
	r := newRig(t, fixture.Opts{})
	decl := "workspaces:\n  - id: alpha\n    path: alpha\n  - id: beta\n    path: beta\noracles:\n  test: \"echo full {path}\"\nheavy: [test]\n"
	r.commit("a", "declare", map[string]string{".claude/jig.yaml": decl, "a.txt": "a"})
	head := r.commit("b", "second", map[string]string{"b.txt": "b"})

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
		// Each run waits for the other to be running too: a run that was made
		// to wait for the first would never see it and give up at the deadline.
		deadline := time.After(10 * time.Second)
		for len(arrived) < 2 {
			select {
			case <-deadline:
				running.Add(-1)
				return "", nil
			case <-time.After(10 * time.Millisecond):
			}
		}
		running.Add(-1)
		return "", nil
	}
	var wg sync.WaitGroup
	for _, slice := range []string{"a", "b"} {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, err := Run(r.deps, r.request(slice, head))
			if err != nil || res.State != journal.VerifyPass {
				t.Errorf("Run %s = %+v, %v", slice, res, err)
			}
		}()
	}
	wg.Wait()
	if got := peak.Load(); got != 2 {
		t.Errorf("%d runs were in flight at once, want 2: the second waited for the first", got)
	}
	for _, l := range r.lines() {
		if l.Event == "slot-wait" || l.Event == "slot-taken" {
			t.Errorf("a slot line was journaled: %+v", l)
		}
	}
}
