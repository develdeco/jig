package need

import (
	"testing"
	"time"

	"github.com/develdeco/jig/internal/envrun"
	"github.com/develdeco/jig/internal/machine"
)

var m = machine.Reading{CPUs: 8, MemoryBytes: 32 << 30}

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
	if _, ok := Read(root, "p", "test", m); ok {
		t.Fatal("a figure with no run")
	}
	Record(root, "p", "test", m, 10*time.Second, use(0, 1<<30))
	if _, ok := Read(root, "p", "test", m); ok {
		t.Fatal("a run with no CPU measurement made a figure")
	}
}

// TestOneRunMakesTheFigure: cores are CPU over wall, memory is the peak.
func TestOneRunMakesTheFigure(t *testing.T) {
	root := t.TempDir()
	Record(root, "p", "test", m, 10*time.Second, use(25*time.Second, 2<<30))
	got, ok := Read(root, "p", "test", m)
	if !ok || got.Cores != 2.5 || got.PeakBytes != 2<<30 || got.Basis != envrun.BasisJob {
		t.Fatalf("figure = %+v, %v; want 2.5 cores, 2 GiB on the job basis", got, ok)
	}
	if _, ok := Read(root, "p", "vet", m); ok {
		t.Error("another oracle of the project shares the figure")
	}
	if _, ok := Read(root, "q", "test", m); ok {
		t.Error("another project shares the figure")
	}
}

// TestTheLargestOfTheLastFiveCounts: a big run is kept against small ones
// that follow it, until it falls out of the last five.
func TestTheLargestOfTheLastFiveCounts(t *testing.T) {
	root := t.TempDir()
	Record(root, "p", "test", m, 10*time.Second, use(60*time.Second, 4<<30))
	for i := range Keep - 1 {
		Record(root, "p", "test", m, 10*time.Second, use(time.Duration(10+i)*time.Second, 1<<30))
	}
	got, _ := Read(root, "p", "test", m)
	if got.Cores != 6 || got.PeakBytes != 4<<30 {
		t.Fatalf("figure = %+v, want the big run's 6 cores and 4 GiB", got)
	}
	Record(root, "p", "test", m, 10*time.Second, use(10*time.Second, 1<<30))
	got, _ = Read(root, "p", "test", m)
	if got.Cores >= 6 || got.PeakBytes != 1<<30 {
		t.Fatalf("figure = %+v, want the big run gone after five newer ones", got)
	}
}

// TestAbsentPeakMemoryLeavesCoresOnly: a run that measured no memory makes a
// figure of cores alone.
func TestAbsentPeakMemoryLeavesCoresOnly(t *testing.T) {
	root := t.TempDir()
	Record(root, "p", "test", m, 10*time.Second, use(20*time.Second, 0))
	got, ok := Read(root, "p", "test", m)
	if !ok || got.Cores != 2 || got.PeakBytes != 0 || got.Basis != "" {
		t.Fatalf("figure = %+v, %v; want 2 cores and no memory", got, ok)
	}
}

// TestAFigureFromAnotherMachineOrTooOldIsIgnored.
func TestAFigureFromAnotherMachineOrTooOldIsIgnored(t *testing.T) {
	root := t.TempDir()
	at := time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC)
	defer func(f func() time.Time) { now = f }(now)
	now = func() time.Time { return at }
	Record(root, "p", "test", m, 10*time.Second, use(20*time.Second, 1<<30))
	for name, other := range map[string]machine.Reading{
		"another CPU count": {CPUs: 16, MemoryBytes: m.MemoryBytes},
		"another memory":    {CPUs: m.CPUs, MemoryBytes: 64 << 30},
	} {
		if _, ok := Read(root, "p", "test", other); ok {
			t.Errorf("a figure from %s counted", name)
		}
	}
	now = func() time.Time { return at.Add(MaxAge - time.Hour) }
	if _, ok := Read(root, "p", "test", m); !ok {
		t.Error("a figure just inside the age limit was dropped")
	}
	now = func() time.Time { return at.Add(MaxAge + time.Hour) }
	if _, ok := Read(root, "p", "test", m); ok {
		t.Error("a figure past the age limit counted")
	}
}
