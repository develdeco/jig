package workerlive

import (
	"os"
	"os/exec"
	"testing"
	"time"
)

func TestTheRunningTestProcessIsAlive(t *testing.T) {
	if !Alive(os.Getpid(), time.Now()) {
		t.Fatal("the test process reads as gone")
	}
}

func TestAnExitedChildIsGone(t *testing.T) {
	c := exec.Command(os.Args[0], "-test.run=^$")
	if err := c.Run(); err != nil {
		t.Fatal(err)
	}
	if Alive(c.Process.Pid, time.Now()) {
		t.Fatal("an exited child reads as alive")
	}
}

func TestAProcessCreatedAfterTheLineIsNotTheWorker(t *testing.T) {
	// A pid reused after the worker died names a process newer than the
	// worker's line: the test process stands in for it.
	if Alive(os.Getpid(), time.Now().Add(-time.Hour)) {
		t.Fatal("a process created after notBefore reads as the worker")
	}
}

func TestNoPidIsNotAlive(t *testing.T) {
	if Alive(0, time.Now()) || Alive(-1, time.Now()) {
		t.Fatal("a non-pid reads as alive")
	}
}
