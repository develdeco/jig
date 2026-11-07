package filelock

import (
	"path/filepath"
	"testing"
	"time"
)

// TestTryLockReportsContentionWithoutProceeding: a held lock times out as
// held=false and a nil error, leaving what that means to the caller, and the
// lock is takable again once the holder lets go.
func TestTryLockReportsContentionWithoutProceeding(t *testing.T) {
	target := filepath.Join(t.TempDir(), "thing.ndjson")

	first, held, err := TryLock(target, time.Second)
	if err != nil || !held {
		t.Fatalf("first TryLock = held %v, err %v; want the lock", held, err)
	}
	begin := time.Now()
	second, held, err := TryLock(target, 120*time.Millisecond)
	if err != nil || held {
		t.Fatalf("second TryLock = held %v, err %v; want a timeout with no error", held, err)
	}
	if waited := time.Since(begin); waited < 100*time.Millisecond {
		t.Errorf("second TryLock gave up after %s, want it to wait out its timeout", waited)
	}
	second() // a release for a lock never held is a no-op
	if _, held, _ := TryLock(target, 0); held {
		t.Fatal("releasing a lock that was never held let go of the real holder")
	}
	first()
	again, held, err := TryLock(target, time.Second)
	if err != nil || !held {
		t.Fatalf("TryLock after the release = held %v, err %v; want the lock", held, err)
	}
	again()
}

// TestThePauseBetweenTriesGrowsToItsBound: the wait between tries starts at
// retryInterval, doubles, and stops growing at maxRetryInterval.
func TestThePauseBetweenTriesGrowsToItsBound(t *testing.T) {
	pause := retryInterval
	var got []time.Duration
	for range 6 {
		got = append(got, pause)
		pause = nextPause(pause)
	}
	want := []time.Duration{50, 100, 200, 200, 200, 200}
	for i, w := range want {
		if got[i] != w*time.Millisecond {
			t.Fatalf("pauses = %v, want 50ms, 100ms, then 200ms", got)
		}
	}
}
