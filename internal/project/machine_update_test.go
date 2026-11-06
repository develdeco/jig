package project

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/develdeco/jig/internal/filelock"
	"github.com/develdeco/jig/internal/home"
)

// TestUpdateMachineConcurrent: many writers each add their own project to one
// machine mapping at once, and every one of them lands - none loses another's
// entry to a read-modify-write race.
func TestUpdateMachineConcurrent(t *testing.T) {
	jigHome := t.TempDir()
	const writers, each = 8, 5

	var wg sync.WaitGroup
	for w := 0; w < writers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < each; i++ {
				name := fmt.Sprintf("p%d-%d", w, i)
				err := UpdateMachine(jigHome, func(m map[string]MachineProject) error {
					m[name] = MachineProject{Store: "/stores/" + name, Clones: map[string]string{name: "/repos/" + name}, Root: "/repos/" + name}
					return nil
				})
				if err != nil {
					t.Errorf("UpdateMachine(%s): %v", name, err)
					return
				}
			}
		}()
	}
	wg.Wait()

	m, err := LoadMachine(jigHome)
	if err != nil {
		t.Fatalf("LoadMachine after concurrent updates: %v", err)
	}
	if len(m) != writers*each {
		t.Fatalf("mapping holds %d projects, want %d: an update was lost", len(m), writers*each)
	}
	if got := m["p3-2"]; got.Store != "/stores/p3-2" || got.Root != "/repos/p3-2" {
		t.Errorf("p3-2 = %+v, want its own store and root", got)
	}
}

// TestUpdateMachineErrorWritesNothing: fn's error stops the write and comes
// back as it was; a change fn made to the map before failing is not saved.
func TestUpdateMachineErrorWritesNothing(t *testing.T) {
	jigHome := t.TempDir()
	if err := SaveMachine(jigHome, map[string]MachineProject{"keep": {Store: "/s", Clones: map[string]string{"k": "/c"}}}); err != nil {
		t.Fatal(err)
	}
	boom := errors.New("boom")
	err := UpdateMachine(jigHome, func(m map[string]MachineProject) error {
		m["half"] = MachineProject{Store: "/h"}
		delete(m, "keep")
		return boom
	})
	if !errors.Is(err, boom) {
		t.Fatalf("UpdateMachine = %v, want fn's error", err)
	}
	got, err := LoadMachine(jigHome)
	if err != nil || !reflect.DeepEqual(got, map[string]MachineProject{"keep": {Store: "/s", Clones: map[string]string{"k": "/c"}}}) {
		t.Fatalf("mapping after a failed update = %+v, %v; want it untouched", got, err)
	}
}

// TestUpdateMachineCreatesAndRefusesNoHome: the first update makes the file,
// and no jig home is refused as SaveMachine's is.
func TestUpdateMachineCreatesAndRefusesNoHome(t *testing.T) {
	jigHome := filepath.Join(t.TempDir(), "not", "yet")
	err := UpdateMachine(jigHome, func(m map[string]MachineProject) error {
		m["first"] = MachineProject{Store: "/s", Root: "/r"}
		return nil
	})
	if err != nil {
		t.Fatalf("UpdateMachine on a home that does not exist yet: %v", err)
	}
	got, err := LoadMachine(jigHome)
	if err != nil || got["first"].Root != "/r" {
		t.Fatalf("LoadMachine = %+v, %v", got, err)
	}
	if err := UpdateMachine("", func(map[string]MachineProject) error { return nil }); err == nil {
		t.Error("UpdateMachine with no jig home = nil error, want a refusal")
	}
}

// TestSaveMachineIsAtomicAndKeepsRoot: SaveMachine leaves no temp file behind,
// a project's Root round-trips, and an empty Root is not written at all, so a
// mapping from before Root existed reads and writes back unchanged.
func TestSaveMachineIsAtomicAndKeepsRoot(t *testing.T) {
	jigHome := t.TempDir()
	in := map[string]MachineProject{
		"marked": {Store: "/s1", Clones: map[string]string{"a": "/c1"}, Root: "/r1"},
		"plain":  {Store: "/s2", Clones: map[string]string{"b": "/c2"}},
	}
	if err := SaveMachine(jigHome, in); err != nil {
		t.Fatal(err)
	}
	out, err := LoadMachine(jigHome)
	if err != nil || !reflect.DeepEqual(in, out) {
		t.Fatalf("round trip = %+v, %v; want %+v", out, err, in)
	}
	raw, _ := os.ReadFile(home.MachinePath(jigHome))
	if n := strings.Count(string(raw), "root:"); n != 1 {
		t.Errorf("projects.yaml has %d root: keys, want 1 (only the marked project):\n%s", n, raw)
	}
	entries, _ := os.ReadDir(jigHome)
	for _, e := range entries {
		if n := e.Name(); n != "projects.yaml" && n != "projects.yaml.lock" {
			t.Errorf("jig home holds %q after SaveMachine, want only projects.yaml and its lock", n)
		}
	}
}

// TestUpdateMachineNeverWritesWithoutTheLock: while another process holds the
// mapping's lock past the wait, UpdateMachine and SaveMachine fail naming the
// lock and write nothing, rather than going ahead unlocked and losing the
// holder's update.
func TestUpdateMachineNeverWritesWithoutTheLock(t *testing.T) {
	jigHome := t.TempDir()
	seed := map[string]MachineProject{"keep": {Store: "/s", Clones: map[string]string{"k": "/c"}}}
	if err := SaveMachine(jigHome, seed); err != nil {
		t.Fatal(err)
	}
	path := home.MachinePath(jigHome)
	release, held, err := filelock.TryLock(path, time.Second)
	if err != nil || !held {
		t.Fatalf("holding the lock: held=%v err=%v", held, err)
	}
	defer release()

	const wait = 150 * time.Millisecond

	called := false
	err = updateMachine(jigHome, func(m map[string]MachineProject) error {
		called = true
		m["lost"] = MachineProject{Store: "/l"}
		return nil
	}, wait)
	if err == nil || !strings.Contains(err.Error(), path+".lock") {
		t.Fatalf("UpdateMachine under a held lock = %v, want an error naming %s.lock", err, path)
	}
	if called {
		t.Error("UpdateMachine ran fn without the lock")
	}
	if err := saveMachine(jigHome, map[string]MachineProject{"lost": {Store: "/l"}}, wait); err == nil || !strings.Contains(err.Error(), path+".lock") {
		t.Errorf("SaveMachine under a held lock = %v, want an error naming %s.lock", err, path)
	}
	release()
	got, err := LoadMachine(jigHome)
	if err != nil || !reflect.DeepEqual(got, seed) {
		t.Fatalf("mapping after refused writes = %+v, %v; want it untouched", got, err)
	}
}

// TestReadRetryingRidesOutAFailure: a read that fails for a moment is tried
// again and returns the data; a missing file is not retried; one that keeps
// failing gives up after the window with its error.
func TestReadRetryingRidesOutAFailure(t *testing.T) {
	calls := 0
	flaky := func(string) ([]byte, error) {
		calls++
		if calls < 4 {
			return nil, errors.New("The process cannot access the file because it is being used by another process.")
		}
		return []byte("ok"), nil
	}
	data, err := readRetrying("p", flaky, 5*time.Second)
	if err != nil || string(data) != "ok" || calls != 4 {
		t.Fatalf("readRetrying = %q, %v after %d calls; want ok after 4", data, err, calls)
	}

	calls = 0
	missing := func(string) ([]byte, error) { calls++; return nil, os.ErrNotExist }
	if _, err := readRetrying("p", missing, 5*time.Second); !os.IsNotExist(err) || calls != 1 {
		t.Fatalf("a missing file: err=%v calls=%d, want ErrNotExist after 1 call", err, calls)
	}

	stuck := errors.New("stuck")
	calls = 0
	if _, err := readRetrying("p", func(string) ([]byte, error) { calls++; return nil, stuck }, 50*time.Millisecond); !errors.Is(err, stuck) || calls < 2 {
		t.Fatalf("a read that keeps failing: err=%v calls=%d, want its error after retries", err, calls)
	}
}
