package store

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// holdCheckpointInAnotherProcess starts a process that takes the checkpoint
// lock of the store at work and keeps it until the returned release is
// called (or the test ends).
func holdCheckpointInAnotherProcess(t *testing.T, work string) (release func()) {
	t.Helper()
	signals := t.TempDir()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(self)
	cmd.Env = append(os.Environ(), "JIG_CHECKPOINT_CHILD="+work, "JIG_CHECKPOINT_SIGNALS="+signals)
	cmd.Stdout, cmd.Stderr = os.Stderr, os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	var once bool
	release = func() {
		if once {
			return
		}
		once = true
		_ = writeMarker(signals, "release")
		<-done
	}
	t.Cleanup(release)
	if !waitMarker(signals, "held", time.Now().Add(30*time.Second)) {
		t.Fatal("the other process never took the checkpoint lock")
	}
	return release
}

// TestSyncPushAndClaimWaitForTheCheckpointLock: each of the three takes the
// lock before it touches git, so none runs while another process holds it,
// and each goes on as soon as that process lets go.
func TestSyncPushAndClaimWaitForTheCheckpointLock(t *testing.T) {
	ops := map[string]func(st *Store) error{
		"Sync": func(st *Store) error { return st.Sync() },
		"Push": func(st *Store) error { return st.Push("record") },
		"Claim": func(st *Store) error {
			_, err := st.Claim(
				func() (string, []string, error) {
					id, err := st.Mint("JIG", Ticket{Title: "x"})
					if err != nil {
						return "", nil, err
					}
					return id, []string{st.TicketRelDir(id)}, nil
				},
				func(id string) string { return id + ": new ticket" },
			)
			return err
		},
	}
	for name, op := range ops {
		t.Run(name, func(t *testing.T) {
			st, work := newTestStandaloneStore(t)
			release := holdCheckpointInAnotherProcess(t, work)

			done := make(chan error, 1)
			go func() { done <- op(st) }()
			select {
			case err := <-done:
				t.Fatalf("%s returned (%v) while another process held the checkpoint lock", name, err)
			case <-time.After(300 * time.Millisecond):
			}
			release()
			select {
			case err := <-done:
				if err != nil {
					t.Fatalf("%s after the lock was released: %v", name, err)
				}
			case <-time.After(30 * time.Second):
				t.Fatalf("%s never went on after the other process released the lock", name)
			}
		})
	}
}

// TestPushLetsGoOfTheCheckpointLockBeforeTheHook: the hook is the GitHub
// mirror's sync, which reaches the store through Claim; it must find the lock
// free (so a hook that is slow does not keep other processes out of the
// store) and must be able to Claim without deadlocking, on a store with an
// origin and one without.
func TestPushLetsGoOfTheCheckpointLockBeforeTheHook(t *testing.T) {
	remoteStore := func(t *testing.T) (*Store, string) {
		st, work, _ := newTestRemoteStore(t)
		return st, work
	}
	for name, open := range map[string]func(t *testing.T) (*Store, string){
		"no origin":   newTestStandaloneStore,
		"with origin": remoteStore,
	} {
		t.Run(name, func(t *testing.T) {
			st, work := open(t)
			lockFile := checkpointLockFile(work)
			if err := os.WriteFile(filepath.Join(work, "note.txt"), []byte("hi\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			hookRan := false
			st.AfterCheckpoint = func(s *Store) error {
				hookRan = true
				if heldElsewhere(t, lockFile) {
					t.Error("Push still held the checkpoint lock when it ran the hook")
				}
				_, err := s.Claim(
					func() (string, []string, error) {
						id, err := s.Mint("JIG", Ticket{Title: "from the hook"})
						if err != nil {
							return "", nil, err
						}
						return id, []string{s.TicketRelDir(id)}, nil
					},
					func(id string) string { return id + ": from the hook" },
				)
				return err
			}
			if err := st.Push("add note"); err != nil {
				t.Fatalf("Push: %v", err)
			}
			if !hookRan {
				t.Fatal("the hook never ran")
			}
			if heldElsewhere(t, lockFile) {
				t.Error("the checkpoint lock is still held after Push returned")
			}
		})
	}
}

// TestClaimHoldsTheCheckpointLockWhileItWrites: write runs under the lock.
func TestClaimHoldsTheCheckpointLockWhileItWrites(t *testing.T) {
	st, work := newTestStandaloneStore(t)
	lockFile := checkpointLockFile(work)
	_, err := st.Claim(
		func() (string, []string, error) {
			if !heldElsewhere(t, lockFile) {
				t.Error("Claim's write ran without the checkpoint lock")
			}
			id, err := st.Mint("JIG", Ticket{Title: "x"})
			if err != nil {
				return "", nil, err
			}
			return id, []string{st.TicketRelDir(id)}, nil
		},
		func(id string) string { return id + ": new ticket" },
	)
	if err != nil {
		t.Fatalf("Claim: %v", err)
	}
	if heldElsewhere(t, lockFile) {
		t.Error("the checkpoint lock is still held after Claim returned")
	}
}

// runCheckpointWriter is one of the writer processes in
// TestFiveWritersUnderAHeldCheckpointLock: it mints a ticket in the store at
// root through Claim with the bounds the parent set, printing the id, or the
// error on standard error.
func runCheckpointWriter(root string) int {
	notice, _ := time.ParseDuration(os.Getenv("JIG_CHECKPOINT_NOTICE"))
	bound, _ := time.ParseDuration(os.Getenv("JIG_CHECKPOINT_BOUND"))
	st, err := Open(root)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	setCheckpointBounds(st, notice, bound)
	st.Warn = func(string, ...any) {}
	id, err := st.Claim(
		func() (string, []string, error) {
			id, err := st.Mint("JIG", Ticket{Title: "x"})
			if err != nil {
				return "", nil, err
			}
			return id, []string{st.TicketRelDir(id)}, nil
		},
		func(id string) string { return id + ": new ticket" },
	)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fmt.Println(id)
	return 0
}

// TestFiveWritersUnderAHeldCheckpointLock: five writer processes in one clone
// while another process holds the checkpoint lock never fetch without it.
// Past the longer bound all five fail naming the holder with nothing written;
// when the holder lets go in time, all five finish with distinct ids.
func TestFiveWritersUnderAHeldCheckpointLock(t *testing.T) {
	type outcome struct {
		out string
		err error
	}
	runFive := func(t *testing.T, work string, notice, bound time.Duration) []outcome {
		self, err := os.Executable()
		if err != nil {
			t.Fatal(err)
		}
		results := make([]outcome, 5)
		var wg sync.WaitGroup
		for i := range results {
			wg.Add(1)
			go func() {
				defer wg.Done()
				cmd := exec.Command(self)
				cmd.Env = append(os.Environ(), "JIG_CHECKPOINT_WRITER="+work,
					"JIG_CHECKPOINT_NOTICE="+notice.String(), "JIG_CHECKPOINT_BOUND="+bound.String())
				var out, errOut bytes.Buffer
				cmd.Stdout, cmd.Stderr = &out, &errOut
				err := cmd.Run()
				if err != nil {
					err = fmt.Errorf("%w: %s", err, strings.TrimSpace(errOut.String()))
				}
				results[i] = outcome{strings.TrimSpace(out.String()), err}
			}()
		}
		wg.Wait()
		return results
	}

	t.Run("past the longer bound", func(t *testing.T) {
		_, work, _ := newTestRemoteStore(t)
		pid, _ := startCheckpointChild(t, work)
		head := runGit(t, work, "rev-parse", "HEAD")
		fetchHead := filepath.Join(work, ".git", "FETCH_HEAD")
		_, statBefore := os.Stat(fetchHead)

		for i, r := range runFive(t, work, 50*time.Millisecond, time.Second) {
			if r.err == nil {
				t.Errorf("writer %d finished while another process held the lock", i)
			} else if !strings.Contains(r.err.Error(), fmt.Sprint(pid)) {
				t.Errorf("writer %d: error %q does not name holder pid %d", i, r.err, pid)
			}
		}
		if _, err := os.Stat(fetchHead); (err == nil) != (statBefore == nil) {
			// The in-process fetch moves only refs/remotes/origin/<branch>, so
			// FETCH_HEAD can show just a fallback to the git program.
			t.Error("a writer fell back to the git program without the checkpoint lock: FETCH_HEAD appeared")
		}
		if got := runGit(t, work, "rev-parse", "HEAD"); got != head {
			t.Errorf("HEAD moved from %s to %s without the lock", head, got)
		}
		if entries, _ := os.ReadDir(filepath.Join(work, "tickets")); len(entries) != 0 {
			t.Errorf("tickets/ holds %d entries after every writer failed", len(entries))
		}
	})

	t.Run("holder lets go in time", func(t *testing.T) {
		_, work, _ := newTestRemoteStore(t)
		_, letGo := startCheckpointChild(t, work)
		time.AfterFunc(500*time.Millisecond, letGo)

		seen := map[string]bool{}
		for i, r := range runFive(t, work, 50*time.Millisecond, 60*time.Second) {
			if r.err != nil {
				t.Errorf("writer %d: %v", i, r.err)
				continue
			}
			if seen[r.out] {
				t.Errorf("id %s minted twice", r.out)
			}
			seen[r.out] = true
		}
		if len(seen) != 5 {
			t.Errorf("minted %d distinct ids, want 5", len(seen))
		}
	})
}
