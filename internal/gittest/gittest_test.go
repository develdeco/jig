package gittest

import (
	"reflect"
	"sync"
	"testing"
)

// withEmptyRegistry saves the package-level cleanup registry, clears it for
// the duration of f, and restores it afterward, so these tests don't
// interfere with cleanups this test binary's own TestMain may register.
func withEmptyRegistry(t *testing.T, f func()) {
	t.Helper()
	mu.Lock()
	saved := cleanups
	cleanups = nil
	mu.Unlock()
	defer func() {
		mu.Lock()
		cleanups = saved
		mu.Unlock()
	}()
	f()
}

func TestAtExitLIFOOrder(t *testing.T) {
	withEmptyRegistry(t, func() {
		var order []int
		AtExit(func() { order = append(order, 1) })
		AtExit(func() { order = append(order, 2) })
		AtExit(func() { order = append(order, 3) })
		runCleanups()

		want := []int{3, 2, 1}
		if !reflect.DeepEqual(order, want) {
			t.Fatalf("cleanup order = %v, want %v", order, want)
		}
	})
}

func TestAtExitConcurrentSafe(t *testing.T) {
	withEmptyRegistry(t, func() {
		var wg sync.WaitGroup
		for i := 0; i < 50; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				AtExit(func() {})
			}()
		}
		wg.Wait()

		mu.Lock()
		n := len(cleanups)
		mu.Unlock()
		if n != 50 {
			t.Fatalf("registered %d cleanups, want 50", n)
		}
		runCleanups()
	})
}
