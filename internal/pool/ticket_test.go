package pool

import (
	"os"
	"path/filepath"
	"testing"
)

// TestCheckTicket pins which ticket ids can name a lease: one plain path
// component that cannot collide with another ticket's gate or publish lease
// on any filesystem jig runs on.
func TestCheckTicket(t *testing.T) {
	for _, id := range []string{"T-1", "JIG-1", "#12", "ENG-42", "gate", "publish", "T-1-gates", "T-1-gatekeeper", "T-1-gate-2", "T-1.gate", "T-1.broken-20260101T000000Z"} {
		if err := CheckTicket(id); err != nil {
			t.Errorf("CheckTicket(%q) = %v, want nil", id, err)
		}
	}
	for _, id := range []string{"", ".", "..", ".git", ".T-1", "a/b", `a\b`, "../T-1", "T-1-gate", "T-1-GATE", "T-1-Gate", "T-1-gate.", "T-1-gate ", "T-1-gate. .", "-gate", "T-1-publish", "T-1-PUBLISH"} {
		if err := CheckTicket(id); err == nil {
			t.Errorf("CheckTicket(%q) = nil, want an error", id)
		}
	}
}

// TestAcquireRefusesReservedTicket covers the lease-key collision at the
// pool itself: ticket T-1-gate's build lease would be ticket T-1's gate
// lease, so Acquire refuses the id before touching the pool, and T-1's gate
// lease keeps its branch and its uncommitted file.
func TestAcquireRefusesReservedTicket(t *testing.T) {
	t.Setenv("JIG_HOME", t.TempDir())
	remote := newSourceAndRemote(t)

	gate, err := Acquire("fixture", remote, "main", "jig/T-1", "T-1", Gate)
	if err != nil {
		t.Fatalf("gate Acquire: %v", err)
	}
	writeFile(t, filepath.Join(gate.Dir, "in-flight.txt"), "gate work\n")

	for _, id := range []string{"T-1-gate", "T-1-GATE", "T-1-gate."} {
		if _, err := Acquire("fixture", remote, "main", "jig/"+id, id, Build); err == nil {
			t.Fatalf("Acquire(%q, Build) = nil error, want a refusal", id)
		}
		if _, err := Dir("fixture", id, Build); err == nil {
			t.Fatalf("Dir(%q, Build) = nil error, want a refusal", id)
		}
	}
	if got := run(t, gate.Dir, "symbolic-ref", "--short", "HEAD"); got != "jig/T-1" {
		t.Fatalf("gate lease HEAD = %q, want jig/T-1", got)
	}
	if got, err := os.ReadFile(filepath.Join(gate.Dir, "in-flight.txt")); err != nil || string(got) != "gate work\n" {
		t.Fatalf("gate lease lost its uncommitted file: %q, %v", got, err)
	}
}

// TestDirRejectsRepoNameOutsidePool covers the other half of a lease path:
// a repo name must also be one directory, so no lease (and nothing Acquire
// moves aside) can ever sit outside the pool.
func TestDirRejectsRepoNameOutsidePool(t *testing.T) {
	t.Setenv("JIG_HOME", t.TempDir())
	for _, name := range []string{"", ".", "..", "a/b", `a\b`} {
		if _, err := Dir(name, "T-1", Build); err == nil {
			t.Errorf("Dir(%q, ...) = nil error, want a refusal", name)
		}
	}
}
