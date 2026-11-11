package verifydeliver

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/develdeco/jig/internal/manifest"
	"github.com/develdeco/jig/internal/slot"
)

// TestTheGateTakesTheSlotOnlyForAHeavyOracle: two gate suite runs of one
// project whose heavy oracle starts together run one after the other, and an
// oracle the manifest does not mark heavy takes no slot.
func TestTheGateTakesTheSlotOnlyForAHeavyOracle(t *testing.T) {
	root := t.TempDir()
	hold := func(name, cmd string) (func(), error) {
		return slot.Hold(context.Background(), root, "proj", 1, func(string) error { return nil })
	}
	suite := func(heavy []string, wantOverlap bool) int32 {
		man := manifest.Manifest{
			Workspaces: []manifest.Workspace{{ID: "root", Path: "."}},
			Oracles:    map[string]string{"test": "go test"},
			Heavy:      heavy,
		}
		var running, peak atomic.Int32
		arrived := make(chan struct{}, 2)
		run := func(cmd, dir string) (string, error) {
			n := running.Add(1)
			for {
				p := peak.Load()
				if n <= p || peak.CompareAndSwap(p, n) {
					break
				}
			}
			arrived <- struct{}{}
			if wantOverlap {
				for deadline := time.Now().Add(10 * time.Second); len(arrived) < 2 && time.Now().Before(deadline); {
					time.Sleep(10 * time.Millisecond)
				}
			} else {
				time.Sleep(300 * time.Millisecond)
			}
			running.Add(-1)
			return "", nil
		}
		var wg sync.WaitGroup
		for range 2 {
			wg.Add(1)
			go func() {
				defer wg.Done()
				if _, err := oracleSuiteRuns(t.TempDir(), man, nil, run, hold); err != nil {
					t.Error(err)
				}
			}()
		}
		wg.Wait()
		return peak.Load()
	}
	if peak := suite([]string{"test"}, false); peak != 1 {
		t.Errorf("%d heavy suites ran at once, want 1", peak)
	}
	if peak := suite(nil, true); peak != 2 {
		t.Errorf("%d suites with no heavy oracle ran at once, want 2", peak)
	}
}

// TestTheGateHoldsNoSlotWithoutAProject: Deps that name no project take no
// slot.
func TestTheGateHoldsNoSlotWithoutAProject(t *testing.T) {
	if (Deps{Home: t.TempDir()}).slotHolder("T-1") != nil {
		t.Error("a gate with no project holds a slot")
	}
}
