package e2e

import (
	"testing"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/store"
)

// TestStatusGoldenConstructed writes slice states directly through the store
// package (no run/gate involved) and asserts `jig status` renders exactly
// cmd/jig's RenderStatus format (status.go) for that snapshot.
func TestStatusGoldenConstructed(t *testing.T) {
	fx, _ := newFixture(t, fixture.Opts{})
	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}

	states := map[string]store.SliceState{
		"a": {State: "green", Attempts: 1},
		"b": {State: "queued", Attempts: 0},
		"c": {State: "queued", Attempts: 0},
		"d": {State: "queued", Attempts: 0},
	}
	for slice, st2 := range states {
		if err := st.WriteSliceState(fx.Ticket, slice, st2); err != nil {
			t.Fatalf("WriteSliceState(%s): %v", slice, err)
		}
	}

	r := runJig(t, fx.StoreDir, "status", fx.Ticket)
	if r.Code != 0 {
		t.Fatalf("jig status exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", r.Code, r.Stdout, r.Stderr)
	}
	assertGolden(t, "status-a-green-b-blocked.txt", r.Stdout)
}
