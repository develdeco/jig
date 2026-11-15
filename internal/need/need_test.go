package need

import (
	"testing"
	"time"

	"github.com/develdeco/jig/internal/envrun"
	"github.com/develdeco/jig/internal/machine"
)

var m = machine.Reading{CPUs: 8, MemoryBytes: 32 << 30}

var t0 = time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)

func use(cpu time.Duration, peak uint64) envrun.Usage {
	u := envrun.Usage{CPU: cpu, PeakBytes: peak}
	if peak > 0 {
		u.Basis = envrun.BasisJob
	}
	return u
}

// TestNothingRecordedIsNothingMeasured: no figure before a run is recorded,
// and a run that measured no CPU time makes none.
func TestNothingRecordedIsNothingMeasured(t *testing.T) {
	root := t.TempDir()
	if _, ok := Read(root, "p", "test", m, t0); ok {
		t.Fatal("a figure with no run")
	}
	Record(root, "p", "test", "cmd", m, t0, 10*time.Second, use(0, 1<<30), false)
	if _, ok := Read(root, "p", "test", m, t0); ok {
		t.Fatal("a run with no CPU measurement made a figure")
	}
}

// TestOneRunMakesTheFigure: cores are CPU over wall, memory is the peak.
func TestOneRunMakesTheFigure(t *testing.T) {
	root := t.TempDir()
	Record(root, "p", "test", "cmd", m, t0, 10*time.Second, use(25*time.Second, 2<<30), false)
	got, ok := Read(root, "p", "test", m, t0)
	if !ok || got.Cores != 2.5 || got.PeakBytes != 2<<30 || got.Basis != envrun.BasisJob {
		t.Fatalf("figure = %+v, %v; want 2.5 cores, 2 GiB on the job basis", got, ok)
	}
	if _, ok := Read(root, "p", "vet", m, t0); ok {
		t.Error("another oracle of the project shares the figure")
	}
	if _, ok := Read(root, "q", "test", m, t0); ok {
		t.Error("another project shares the figure")
	}
}

// TestTheLargestOfTheLastFiveCounts: a big run is kept against small ones
// that follow it, until it falls out of the last five.
func TestTheLargestOfTheLastFiveCounts(t *testing.T) {
	root := t.TempDir()
	Record(root, "p", "test", "cmd", m, t0, 10*time.Second, use(60*time.Second, 4<<30), false)
	for i := range Keep - 1 {
		Record(root, "p", "test", "cmd", m, t0, 10*time.Second, use(time.Duration(10+i)*time.Second, 1<<30), false)
	}
	got, _ := Read(root, "p", "test", m, t0)
	if got.Cores != 6 || got.PeakBytes != 4<<30 {
		t.Fatalf("figure = %+v, want the big run's 6 cores and 4 GiB", got)
	}
	Record(root, "p", "test", "cmd", m, t0, 10*time.Second, use(10*time.Second, 1<<30), false)
	got, _ = Read(root, "p", "test", m, t0)
	if got.Cores >= 6 || got.PeakBytes != 1<<30 {
		t.Fatalf("figure = %+v, want the big run gone after five newer ones", got)
	}
}

// TestAbsentPeakMemoryLeavesCoresOnly: a run that measured no memory makes a
// figure of cores alone.
func TestAbsentPeakMemoryLeavesCoresOnly(t *testing.T) {
	root := t.TempDir()
	Record(root, "p", "test", "cmd", m, t0, 10*time.Second, use(20*time.Second, 0), false)
	got, ok := Read(root, "p", "test", m, t0)
	if !ok || got.Cores != 2 || got.PeakBytes != 0 || got.Basis != "" {
		t.Fatalf("figure = %+v, %v; want 2 cores and no memory", got, ok)
	}
}

// TestAFigureFromAnotherMachineOrTooOldIsIgnored.
func TestAFigureFromAnotherMachineOrTooOldIsIgnored(t *testing.T) {
	root := t.TempDir()
	at := t0
	Record(root, "p", "test", "cmd", m, at, 10*time.Second, use(20*time.Second, 1<<30), false)
	for name, other := range map[string]machine.Reading{
		"another CPU count": {CPUs: 16, MemoryBytes: m.MemoryBytes},
		"another memory":    {CPUs: m.CPUs, MemoryBytes: 64 << 30},
	} {
		if _, ok := Read(root, "p", "test", other, t0); ok {
			t.Errorf("a figure from %s counted", name)
		}
	}
	if _, ok := Read(root, "p", "test", m, at.Add(MaxAge-time.Hour)); !ok {
		t.Error("a figure just inside the age limit was dropped")
	}
	if _, ok := Read(root, "p", "test", m, at.Add(MaxAge+time.Hour)); ok {
		t.Error("a figure past the age limit counted")
	}
}

// TestRunsKeepWhatTheRuleReads: each run keeps its wall time and whether it
// shared, oldest first, over more runs than make the figure; a run recorded
// without a wall time, and an old one, are not read.
func TestRunsKeepWhatTheRuleReads(t *testing.T) {
	root := t.TempDir()
	for i := range History + 3 {
		Record(root, "p", "test", "cmd", m, t0.Add(time.Duration(i)*time.Minute), time.Duration(10+i)*time.Second, use(20*time.Second, 1<<30), i%2 == 1)
	}
	at := t0.Add(time.Hour)
	runs := Runs(root, "p", "test", "cmd", at)
	if len(runs) != History {
		t.Fatalf("%d runs kept, want %d", len(runs), History)
	}
	first := runs[0]
	if first.Wall != 13*time.Second || !first.Shared || first.CPUs != m.CPUs || first.MemBytes != m.MemoryBytes {
		t.Errorf("oldest kept run = %+v, want 13s, shared, on this machine", first)
	}
	if got, ok := Read(root, "p", "test", m, at); !ok || got.Cores <= 0 {
		t.Errorf("the figure over the last %d runs = %+v, %v", Keep, got, ok)
	}
	if old := Runs(root, "p", "test", "cmd", t0.Add(MaxAge+2*time.Hour)); len(old) != 0 {
		t.Errorf("runs older than the age bound were read: %d", len(old))
	}
	if other := Runs(root, "q", "test", "cmd", at); len(other) != 0 {
		t.Error("another project's runs were read")
	}
}
