package pool

import "testing"

// TestAcquireSaysItFetchedOrigin: a lease Acquire made fresh (a clone) and
// one it reused (a fetch) both have a view of origin as of now, and say so,
// so a caller that wants that view does not fetch it again.
func TestAcquireSaysItFetchedOrigin(t *testing.T) {
	t.Parallel()

	remote := newSourceAndRemote(t)
	home := t.TempDir()
	fresh, err := Acquire(home, "fixture", remote, "main", "jig/T-1", "T-1", Build)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if !fresh.Fetched {
		t.Error("a freshly cloned lease does not say it fetched origin")
	}
	reused, err := Acquire(home, "fixture", remote, "main", "jig/T-1", "T-1", Build)
	if err != nil {
		t.Fatalf("Acquire again: %v", err)
	}
	if !reused.Fetched {
		t.Error("a reused lease does not say it fetched origin")
	}
	if (Lease{Dir: fresh.Dir}).Fetched {
		t.Error("a lease Acquire did not return says it fetched")
	}
}
