package slot

import (
	"context"
	"testing"
	"time"

	"github.com/develdeco/jig/internal/envrun"
	"github.com/develdeco/jig/internal/machine"
	"github.com/develdeco/jig/internal/need"
)

// testMachine is the machine the admission tests run on: after the headroom it
// has 9 cores and 85 GiB.
var testMachine = machine.Reading{CPUs: 10, MemoryBytes: 100 << 30}

// measure records a run of project's oracle test that kept cores busy and
// peaked at peak bytes, so the next run of it asks for that.
func measure(root, project string, cores int, peak uint64) {
	use := envrun.Usage{CPU: time.Duration(cores) * 10 * time.Second, PeakBytes: peak}
	if peak > 0 {
		use.Basis = envrun.BasisJob
	}
	need.Record(root, project, "test", testMachine, time.Now(), 10*time.Second, use, false)
}

func ask1(project, who string) Request {
	return Request{Project: project, Who: who, Oracle: "test", machine: func() machine.Reading { return testMachine }}
}

func takeNow(t *testing.T, root string, r Request) func() {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), patience)
	defer cancel()
	rel, err := Take(ctx, root, r, func() { t.Errorf("%s waited, want it admitted at once", r.Who) })
	if err != nil {
		t.Fatalf("%s: %v", r.Who, err)
	}
	return rel
}

// TestAProjectThatMeasuredOneCoreRunsSeveralAtOnce: on a many-core machine the
// runs of a project that kept one core busy are admitted together.
func TestAProjectThatMeasuredOneCoreRunsSeveralAtOnce(t *testing.T) {
	root := t.TempDir()
	measure(root, "p", 1, 1<<30)
	var rels []func()
	for i := range 9 {
		rels = append(rels, takeNow(t, root, ask1("p", Who("T", string(rune('a'+i))))))
	}
	waiting, got := ask(t, root, ask1("p", "T/tenth"))
	recv(t, waiting, "the tenth run, past the 9 cores, waits")
	notYet(t, got, "the tenth run has cores the others hold")
	rels[0]()
	recv(t, got, "the tenth run is admitted when a core frees")()
	for _, r := range rels[1:] {
		r()
	}
}

// TestAProjectWithNothingMeasuredRunsAlone: it waits for every holder, and
// holds the whole machine against a run that is measured.
func TestAProjectWithNothingMeasuredRunsAlone(t *testing.T) {
	root := t.TempDir()
	measure(root, "small", 1, 1<<30)
	small := takeNow(t, root, ask1("small", "S/a"))
	waiting, got := ask(t, root, ask1("fresh", "F/a"))
	recv(t, waiting, "the unmeasured run waits for a holder")
	notYet(t, got, "the unmeasured run shares the machine")
	small()
	whole := recv(t, got, "the unmeasured run is admitted once the machine is free")
	w2, g2 := ask(t, root, ask1("small", "S/b"))
	recv(t, w2, "a measured run waits while the whole machine is held")
	notYet(t, g2, "a measured run shares a machine held whole")
	whole()
	recv(t, g2, "the measured run is admitted after")()
}

// TestANeedPastTheMachineAsksForTheWholeMachine.
func TestANeedPastTheMachineAsksForTheWholeMachine(t *testing.T) {
	root := t.TempDir()
	measure(root, "big", 12, 1<<30)
	big := takeNow(t, root, ask1("big", "B/a"))
	waiting, got := ask(t, root, ask1("big", "B/b"))
	recv(t, waiting, "a second run waits")
	notYet(t, got, "a second run shares the machine with a run that needs more than it has")
	big()
	recv(t, got, "the second run is admitted after")()
}

// TestNothingPassesTheOldestWaitingRun: a run that would fit still waits behind
// an older run that does not, so small runs never starve a large one.
func TestNothingPassesTheOldestWaitingRun(t *testing.T) {
	root := t.TempDir()
	measure(root, "four", 4, 0)
	measure(root, "one", 1, 0)
	a := takeNow(t, root, ask1("four", "A/x"))
	b := takeNow(t, root, ask1("four", "B/x"))
	cw, cgot := ask(t, root, ask1("four", "C/x"))
	recv(t, cw, "the third four-core run waits")
	dw, dgot := ask(t, root, ask1("one", "D/x"))
	recv(t, dw, "the one-core run waits behind it though it would fit")
	notYet(t, dgot, "the one-core run passed an older run")
	a()
	recv(t, cgot, "the older run is admitted when its cores are free")()
	recv(t, dgot, "the one-core run follows")()
	b()
}

// TestMemoryBindsToo: runs that fit in cores wait when their peak memory does
// not fit, and a run with no memory figure is held back by cores alone.
func TestMemoryBindsToo(t *testing.T) {
	root := t.TempDir()
	measure(root, "heavy", 1, 50<<30)
	measure(root, "nomem", 1, 0)
	a := takeNow(t, root, ask1("heavy", "A/x"))
	waiting, got := ask(t, root, ask1("heavy", "B/x"))
	recv(t, waiting, "a second 50 GiB run waits")
	notYet(t, got, "two 50 GiB runs share 85 GiB")
	a()
	recv(t, got, "the second run is admitted after")()
	defer takeNow(t, root, ask1("nomem", "C/x"))()
}

// TestAPinnedRunsFigureCountsAgainstTheBudget: a pinned project's run is
// admitted by its count, and what it measured is taken from the budget.
func TestAPinnedRunsFigureCountsAgainstTheBudget(t *testing.T) {
	root := t.TempDir()
	measure(root, "pin", 8, 0)
	measure(root, "other", 4, 0)
	pin := ask1("pin", "P/a")
	pin.Pinned = 1
	p := takeNow(t, root, pin)
	waiting, got := ask(t, root, ask1("other", "O/a"))
	recv(t, waiting, "a 4-core run waits beside a pinned 8-core run on 9 cores")
	notYet(t, got, "the budget run fits beside the pinned run")
	p()
	recv(t, got, "the budget run is admitted after the pinned run ends")()
}

// TestAHeldRunsMeasurementChangesTheNextAsk: a run that records what it used
// leaves its project asking for that, not the whole machine.
func TestAHeldRunsMeasurementChangesTheNextAsk(t *testing.T) {
	root := t.TempDir()
	share, err := Hold(context.Background(), root, ask1("p", "T/a"), func(string, string) error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	share.Record(10*time.Second, envrun.Usage{CPU: 10 * time.Second, PeakBytes: 1 << 30, Basis: envrun.BasisJob})
	share.Release()
	defer takeNow(t, root, ask1("p", "T/b"))()
	defer takeNow(t, root, ask1("p", "T/c"))()
}

// TestAMemoryNeedPastTheMachineAsksForTheWholeMachine: a run that measured one
// core but more memory than the budget has is clamped to the whole machine, so
// it is admitted when the machine is free and holds it against a small run,
// instead of waiting forever and blocking the queue behind it.
func TestAMemoryNeedPastTheMachineAsksForTheWholeMachine(t *testing.T) {
	root := t.TempDir()
	measure(root, "bigmem", 1, 90<<30)
	measure(root, "small", 1, 1<<30)
	big := takeNow(t, root, ask1("bigmem", "B/a"))
	waiting, got := ask(t, root, ask1("small", "S/a"))
	recv(t, waiting, "a small run waits beside a run that needs more memory than the machine has")
	notYet(t, got, "a small run shares the machine with a run clamped to all of it")
	big()
	recv(t, got, "the small run is admitted after")()
}

// pin asks for a share that is counted by its project's own count, which a
// test can hold beside any other run.
func pin(project, who string) Request {
	r := ask1(project, who)
	r.Pinned = 1
	return r
}

func holdShare(t *testing.T, root string, r Request) *Share {
	t.Helper()
	s, err := Hold(context.Background(), root, r, func(string, string) error { return nil })
	if err != nil {
		t.Fatalf("%s: %v", r.Who, err)
	}
	return s
}

// TestRunsHeldTogetherSharedTheMachineInEitherArrivalOrder: both runs report
// it, the one admitted first and the one admitted later.
func TestRunsHeldTogetherSharedTheMachineInEitherArrivalOrder(t *testing.T) {
	root := t.TempDir()
	first := holdShare(t, root, pin("a", "A/1"))
	if first.Shared() {
		t.Fatal("a run alone on the machine reports it shared")
	}
	second := holdShare(t, root, pin("b", "B/1"))
	// first.Shared() above fixed its answer at the moment its oracle "ended";
	// a run whose oracle is still going sees the later arrival.
	third := holdShare(t, root, pin("c", "C/1"))
	if !second.Shared() || !third.Shared() {
		t.Errorf("shared: second %v, third %v; want both", second.Shared(), third.Shared())
	}
	if first.Shared() {
		t.Error("the answer given when the first run ended changed afterwards")
	}
	first.Release()
	second.Release()
	third.Release()
}

// TestARunAdmittedBeforeAnotherJoinedItSharedToo.
func TestARunAdmittedBeforeAnotherJoinedItSharedToo(t *testing.T) {
	root := t.TempDir()
	first := holdShare(t, root, pin("a", "A/1"))
	second := holdShare(t, root, pin("b", "B/1"))
	if !first.Shared() || !second.Shared() {
		t.Errorf("shared: first %v, second %v; want both", first.Shared(), second.Shared())
	}
	first.Release()
	second.Release()
}

// TestSequentialRunsDoNotShare: a run admitted after another released, and one
// that waited for it, ran alone.
func TestSequentialRunsDoNotShare(t *testing.T) {
	root := t.TempDir()
	measure(root, "p", 9, 0)
	first := holdShare(t, root, ask1("p", "P/1"))
	waiting, got := ask(t, root, ask1("p", "P/2"))
	recv(t, waiting, "a run that needs the whole budget waits")
	if first.Shared() {
		t.Error("a run that ran with only a waiter beside it reports it shared")
	}
	first.Release()
	recv(t, got, "the second run is admitted after")()
	third := holdShare(t, root, ask1("p", "P/3"))
	if third.Shared() {
		t.Error("a run admitted after the others left reports it shared")
	}
	third.Release()
}

// TestARunRecordsWhetherItSharedForTheRule: the machine's figures keep the
// fact and the wall time per run, so the rule reads them.
func TestARunRecordsWhetherItSharedForTheRule(t *testing.T) {
	root := t.TempDir()
	use := envrun.Usage{CPU: 10 * time.Second}
	alone := holdShare(t, root, pin("p", "P/1"))
	alone.Record(10*time.Second, use)
	alone.Release()
	a := holdShare(t, root, pin("p", "P/2"))
	b := holdShare(t, root, pin("other", "O/1"))
	a.Record(25*time.Second, use)
	a.Release()
	b.Release()
	runs := need.Runs(root, "p", "test", time.Now())
	if len(runs) != 2 || runs[0].Shared || !runs[1].Shared || runs[1].Wall != 25*time.Second {
		t.Errorf("recorded runs = %+v, want one alone of 10s and one shared of 25s", runs)
	}
}
