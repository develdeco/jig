package store

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestLockWarnsOnTimeout simulates held=false (a tiny timeout while another
// goroutine holds the lock) and asserts Lock says so out loud on stderr,
// naming the path and the timeout, so a proceeding-without-lock write is
// never silent.
func TestLockWarnsOnTimeout(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "warn.txt")

	release, held, err := Lock(target, 30*time.Second)
	if err != nil || !held {
		t.Fatalf("Lock (holder): held=%v err=%v", held, err)
	}
	defer release()

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	origStderr := os.Stderr
	os.Stderr = w

	_, held2, err2 := Lock(target, 20*time.Millisecond)

	os.Stderr = origStderr
	w.Close()
	var buf bytes.Buffer
	if _, err := io.Copy(&buf, r); err != nil {
		t.Fatal(err)
	}

	if err2 != nil {
		t.Fatalf("Lock (contended): %v", err2)
	}
	if held2 {
		t.Fatal("Lock (contended): held = true, want false while the holder still has it")
	}

	got := buf.String()
	want := "jig: proceeding without lock on " + target + " (timeout after 20ms)"
	if !strings.Contains(got, want) {
		t.Fatalf("stderr = %q, want it to contain %q", got, want)
	}
}

// TestLockLoadBearing demonstrates that Lock is load-bearing: four
// goroutines read-modify-write one file with a deliberate stall inside the
// critical section. Under the lock all four updates survive; a lock-less
// control performing the same read-stall-write loses updates.
func TestLockLoadBearing(t *testing.T) {
	const n = 4

	t.Run("with lock", func(t *testing.T) {
		dir := t.TempDir()
		target := filepath.Join(dir, "counter.txt")
		if err := os.WriteFile(target, []byte("0"), 0o644); err != nil {
			t.Fatal(err)
		}

		var wg sync.WaitGroup
		for i := 0; i < n; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				release, _, err := Lock(target, 30*time.Second)
				if err != nil {
					t.Errorf("Lock: %v", err)
					return
				}
				defer release()

				data, err := os.ReadFile(target)
				if err != nil {
					t.Errorf("ReadFile: %v", err)
					return
				}
				v, _ := strconv.Atoi(strings.TrimSpace(string(data)))
				time.Sleep(20 * time.Millisecond) // stall inside the critical section
				v++
				if err := AtomicWrite(target, []byte(strconv.Itoa(v))); err != nil {
					t.Errorf("AtomicWrite: %v", err)
				}
			}()
		}
		wg.Wait()

		data, err := os.ReadFile(target)
		if err != nil {
			t.Fatal(err)
		}
		if got := strings.TrimSpace(string(data)); got != strconv.Itoa(n) {
			t.Fatalf("with lock: counter = %q, want %d (a lost write means the lock did not serialize)", got, n)
		}
	})

	t.Run("control without lock loses writes", func(t *testing.T) {
		dir := t.TempDir()
		target := filepath.Join(dir, "counter.txt")
		if err := os.WriteFile(target, []byte("0"), 0o644); err != nil {
			t.Fatal(err)
		}

		var wg sync.WaitGroup
		var startWG sync.WaitGroup
		barrier := make(chan struct{})
		startWG.Add(n)
		for i := 0; i < n; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				data, err := os.ReadFile(target)
				if err != nil {
					t.Errorf("ReadFile: %v", err)
					return
				}
				v, _ := strconv.Atoi(strings.TrimSpace(string(data)))
				startWG.Done()
				<-barrier // every goroutine reads before any of them writes
				v++
				_ = os.WriteFile(target, []byte(strconv.Itoa(v)), 0o644)
			}()
		}
		startWG.Wait()
		close(barrier)
		wg.Wait()

		data, err := os.ReadFile(target)
		if err != nil {
			t.Fatal(err)
		}
		if got := strings.TrimSpace(string(data)); got == strconv.Itoa(n) {
			t.Fatalf("control: counter = %q, want writes to be lost (all readers observed 0 before any write)", got)
		}
	})
}
