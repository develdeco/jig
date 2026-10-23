package store

import (
	"os"
	"os/exec"
	"path/filepath"
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
