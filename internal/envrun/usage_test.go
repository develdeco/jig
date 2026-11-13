package envrun

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
	"testing"
	"time"
)

// usageHelperEnv makes a run of this test binary the program TestUsageHelper
// plays: "parent" or "child".
const usageHelperEnv = "JIG_ENVRUN_USAGE_HELPER"

const (
	helperHold = 128 << 20
	// helperMinHold is how long each process keeps its block, so the sampler
	// of a once-a-second reader sees both blocks held together.
	helperMinHold = 3 * time.Second
)

// burn spins n goroutines until this process's own CPU clock reaches target.
// Its own clock, not the wall clock: a loaded machine lengthens the run
// instead of lowering the figure.
func burn(n int, target time.Duration) {
	var wg sync.WaitGroup
	for range n {
		wg.Add(1)
		go func() {
			defer wg.Done()
			x := uint64(1)
			for processCPU() < target {
				for range 1 << 18 {
					x = x*6364136223846793005 + 1442695040888963407
				}
			}
			_ = x
		}()
	}
	wg.Wait()
}

// hold touches helperHold bytes so they are resident and committed.
func hold() []byte {
	b := make([]byte, helperHold)
	for i := 0; i < len(b); i += 4096 {
		b[i] = 1
	}
	return b
}

// TestUsageHelper is the program the usage tests run through the real path.
// The parent burns 2 cores for 1 CPU-second each and starts one child that
// burns 1 core for 2 CPU-seconds, so the tree uses 4 CPU-seconds; each holds
// 128 MiB for at least 3 seconds. Outside a run it does nothing.
func TestUsageHelper(t *testing.T) {
	role := os.Getenv(usageHelperEnv)
	if role == "" {
		t.Skip("only the usage tests run this")
	}
	start := time.Now()
	block := hold()
	if role == "child" {
		burn(1, 2*time.Second)
	} else {
		child := exec.Command(os.Args[0], "-test.run=^TestUsageHelper$")
		child.Env = append(os.Environ(), usageHelperEnv+"=child")
		if err := child.Start(); err != nil {
			t.Fatal(err)
		}
		burn(2, 2*time.Second)
		defer func() { _ = child.Wait() }()
	}
	if rest := helperMinHold - time.Since(start); rest > 0 {
		time.Sleep(rest)
	}
	block[len(block)-1] = 2
}

// helperCommand is the sh command line that runs this test binary as the
// usage helper's parent.
func helperCommand() string {
	return "'" + filepath.ToSlash(os.Args[0]) + "' -test.run='^TestUsageHelper$'"
}

// TestShellOutputUsageReadsTheWholeTree: through the real run path, the usage
// of a tree of two processes is the CPU time of both and the memory they held
// together, not the shell's or the largest process's alone.
func TestShellOutputUsageReadsTheWholeTree(t *testing.T) {
	if testing.Short() {
		t.Skip("runs a tree for several seconds")
	}
	_, use, err := ShellOutputUsage(helperCommand(), t.TempDir(), 3*time.Minute,
		[]string{usageHelperEnv + "=parent"})
	if err != nil {
		t.Fatal(err)
	}
	const lo, hi = 3600 * time.Millisecond, 5000 * time.Millisecond
	if use.CPU < lo || use.CPU > hi {
		t.Errorf("CPU = %v, want %v to %v (4 CPU-seconds were burned)", use.CPU, lo, hi)
	}
	if use.PeakBytes == 0 {
		if runtime.GOOS == "windows" {
			t.Fatal("no peak memory was read from the job")
		}
		t.Skip("ps is not usable here: peak memory is not measured")
	}
	const loMem, hiMem = 230 << 20, 384 << 20
	if use.PeakBytes < loMem || use.PeakBytes > hiMem {
		t.Errorf("peak = %d MiB, want %d to %d (two 128 MiB blocks held together)",
			use.PeakBytes>>20, loMem>>20, hiMem>>20)
	}
	want := BasisSampled
	if runtime.GOOS == "windows" {
		want = BasisJob
	}
	if use.Basis != want {
		t.Errorf("basis = %q, want %q", use.Basis, want)
	}
}

// TestShellOutputUsageSurvivesAFailureAndAKill: a command that exits non-zero
// or is killed at its limit still returns what its tree used until it ended,
// and the verdict is the same one ShellOutputEnv gives.
func TestShellOutputUsageSurvivesAFailureAndAKill(t *testing.T) {
	dir := t.TempDir()
	out, use, err := ShellOutputUsage("echo hi; exit 3", dir, time.Minute, nil)
	if err == nil || trimEOL(out) != "hi" {
		t.Fatalf("out = %q, err = %v, want hi and a failure", out, err)
	}
	if use.CPU <= 0 {
		t.Errorf("a failed run has CPU %v, want it measured", use.CPU)
	}
	if (use.PeakBytes == 0) != (use.Basis == "") {
		t.Errorf("peak %d and basis %q disagree", use.PeakBytes, use.Basis)
	}

	_, use, err = ShellOutputUsage("sleep 600", dir, 2*time.Second, nil)
	if !errors.Is(err, ErrTimedOut) {
		t.Fatalf("err = %v, want a timeout", err)
	}
	if use.CPU <= 0 {
		t.Errorf("a killed run has CPU %v, want it measured", use.CPU)
	}
}

// TestShellOutputEnvKeepsItsResults: the function that returns no usage runs
// the same command to the same result.
func TestShellOutputEnvKeepsItsResults(t *testing.T) {
	out, err := ShellOutputEnv("echo "+strconv.Itoa(7)+"; echo x >&2", t.TempDir(), time.Minute, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got := trimEOL(out); got != "7\nx" && got != "7\r\nx" {
		t.Errorf("out = %q", out)
	}
}
