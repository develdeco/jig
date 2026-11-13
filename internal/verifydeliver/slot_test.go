package verifydeliver

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/develdeco/jig/internal/envrun"
	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/machine"
	"github.com/develdeco/jig/internal/manifest"
	"github.com/develdeco/jig/internal/need"
	"github.com/develdeco/jig/internal/slot"
)

// TestTheGateTakesTheSlotOnlyForAHeavyOracle: two gate suite runs of one
// project whose heavy oracle starts together run one after the other, and an
// oracle the manifest does not mark heavy takes no slot.
func TestTheGateTakesTheSlotOnlyForAHeavyOracle(t *testing.T) {
	root := t.TempDir()
	hold := func(name, cmd string) (*slot.Share, error) {
		return slot.Hold(context.Background(), root, slot.Request{Project: "proj", Oracle: name, Command: cmd}, func(string, string) error { return nil })
	}
	suite := func(heavy []string, wantOverlap bool) int32 {
		man := manifest.Manifest{
			Workspaces: []manifest.Workspace{{ID: "root", Path: "."}},
			Oracles:    map[string]string{"test": "go test"},
			Heavy:      heavy,
		}
		var running, peak atomic.Int32
		arrived := make(chan struct{}, 2)
		run := func(cmd, dir string) (string, envrun.Usage, error) {
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
			return "", envrun.Usage{}, nil
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

// TestTheGatesHeavyRunRecordsWhatItUsedUnlessJigStoppedIt: a heavy oracle the
// gate ran to a verdict, pass or fail, leaves its project's figure for the next
// run to be admitted by; one jig stopped leaves none.
func TestTheGatesHeavyRunRecordsWhatItUsedUnlessJigStoppedIt(t *testing.T) {
	for name, tc := range map[string]struct {
		err  error
		want bool
	}{"a pass": {nil, true}, "a failure": {errors.New("boom"), true}, "a stop": {envrun.ErrStopped, false}} {
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			hold := func(name, cmd string) (*slot.Share, error) {
				return slot.Hold(context.Background(), root, slot.Request{Project: "proj", Oracle: name, Command: cmd}, func(string, string) error { return nil })
			}
			man := manifest.Manifest{
				Workspaces: []manifest.Workspace{{ID: "root", Path: "."}},
				Oracles:    map[string]string{"test": "go test"},
				Heavy:      []string{"test"},
			}
			run := func(cmd, dir string) (string, envrun.Usage, error) {
				time.Sleep(20 * time.Millisecond)
				return "", envrun.Usage{CPU: time.Second}, tc.err
			}
			_, _, _ = runSuite(t.TempDir(), man, nil, run, hold)
			_, ok := need.Read(root, "proj", "test", machine.Read(), time.Now())
			if ok != tc.want {
				t.Errorf("a figure kept = %v, want %v", ok, tc.want)
			}
		})
	}
}

// TestTheGateHoldsNoSlotWithoutAProject: Deps that name no project take no
// slot.
func TestTheGateHoldsNoSlotWithoutAProject(t *testing.T) {
	if (Deps{Home: t.TempDir()}).slotHolder("T-1") != nil {
		t.Error("a gate with no project holds a slot")
	}
}

// TestTheGatesSuiteWaitsForTheSlotAndSaysSo: runGateOracles with a project
// whose manifest marks the oracle heavy waits while another run holds the
// project's slot, journals the gate's slot-wait (no slice, the waiting line)
// and, once the slot is free, runs the oracle and journals slot-taken; while
// it waits slot.Waits reports the gate round.
func TestTheGatesSuiteWaitsForTheSlotAndSaysSo(t *testing.T) {
	t.Parallel()
	fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
	d := newDeps(t, fx)
	if d.Cfg.Name == "" {
		t.Fatal("test setup: the fixture project has no name")
	}
	var ran atomic.Bool
	d.Oracle = func(cmd, dir string) (string, error) {
		ran.Store(true)
		return "", nil
	}
	release, err := slot.Take(context.Background(), d.Home, slot.Request{Project: d.Cfg.Name, Who: slot.Who("OTHER", "x")}, nil)
	if err != nil {
		t.Fatal(err)
	}
	man := manifest.Manifest{
		Workspaces: []manifest.Workspace{{ID: "root", Path: "."}},
		Oracles:    map[string]string{"test": "echo suite"},
		Heavy:      []string{"test"},
	}
	done := make(chan error, 1)
	go func() {
		_, err := runGateOracles(d, fx.Ticket, "fixture-repo", fx.RepoDir, man, nil)
		done <- err
	}()

	count := func(event string) int {
		lines, err := journal.Read(d.Store, fx.Ticket)
		if err != nil {
			t.Fatal(err)
		}
		n := 0
		for _, l := range lines {
			if l.Event == event {
				n++
			}
		}
		return n
	}
	for deadline := time.Now().Add(60 * time.Second); count(journal.EventSlotWait) == 0; {
		if time.Now().After(deadline) {
			t.Fatal("the gate never journaled that it waits")
		}
		time.Sleep(20 * time.Millisecond)
	}
	time.Sleep(200 * time.Millisecond)
	if ran.Load() {
		t.Fatal("the gate's oracle ran while another run held the slot")
	}
	lines, _ := journal.Read(d.Store, fx.Ticket)
	waits, err := slot.Waits(d.Home, fx.Ticket, lines)
	if err != nil || len(waits) != 1 || waits[0].Slice != "" || !strings.Contains(waits[0].Line, "OTHER/x is running its suite") {
		t.Fatalf("Waits = %+v, %v; want the gate round behind OTHER/x", waits, err)
	}
	release()
	if err := <-done; err != nil {
		t.Fatalf("runGateOracles: %v", err)
	}
	if !ran.Load() {
		t.Error("the gate's oracle never ran after the slot was free")
	}
	lines, _ = journal.Read(d.Store, fx.Ticket)
	var wait, taken *journal.Line
	for i, l := range lines {
		switch l.Event {
		case journal.EventSlotWait:
			wait = &lines[i]
		case journal.EventSlotTaken:
			taken = &lines[i]
		}
	}
	if wait == nil || wait.Slice != "" || wait.Source != "gate" || wait.Oracle != "test" || !strings.HasPrefix(wait.Summary, "waiting for the suite slot: OTHER/x") {
		t.Errorf("slot-wait line = %+v", wait)
	}
	if taken == nil || taken.Slice != "" || taken.Source != "gate" {
		t.Errorf("slot-taken line = %+v", taken)
	}
	if got, _ := slot.Waits(d.Home, fx.Ticket, lines); len(got) != 0 {
		t.Errorf("the gate still shows as waiting after it took the slot: %+v", got)
	}
}
