package e2e

import (
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/store"
)

// TestStatusGoldenConstructed writes slice states directly through the store
// package (no run/gate involved) and asserts `jig status` renders exactly
// cmd/jig's RenderStatus format (status.go) for that snapshot.
func TestStatusGoldenConstructed(t *testing.T) {
	t.Parallel()
	fx, env := newFixture(t, fixture.Opts{})
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

	r := runJig(t, env, fx.StoreDir, "status", fx.Ticket)
	if r.Code != 0 {
		t.Fatalf("jig status exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", r.Code, r.Stdout, r.Stderr)
	}
	assertGolden(t, "status-a-green-b-blocked.txt", r.Stdout)
}

// TestStatusGoldenStalled writes a stalled slice state directly through the
// store package (no run involved) and asserts `jig status` renders the
// stalled custody surface: the "stalled" overall state, the stalled table
// with its human-readable stall summary (not the normalized signature,
// which stays the matching key underneath but is unfit to show a human),
// and the stalled remediation hint, alongside TestStatusGoldenConstructed's
// plain building state.
func TestStatusGoldenStalled(t *testing.T) {
	t.Parallel()
	fx, env := newFixture(t, fixture.Opts{})
	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}

	states := map[string]store.SliceState{
		"a": {State: "stalled", Attempts: 2, Reason: "stall", Signature: "a|code-bug|clamp still returns for clamp   the upper bound check is missing", StallSummary: "clamp still returns for clamp(, , ); the upper bound check is missing."},
		"b": {State: "queued", Attempts: 0},
		"c": {State: "queued", Attempts: 0},
		"d": {State: "queued", Attempts: 0},
	}
	for slice, s := range states {
		if err := st.WriteSliceState(fx.Ticket, slice, s); err != nil {
			t.Fatalf("WriteSliceState(%s): %v", slice, err)
		}
	}

	r := runJig(t, env, fx.StoreDir, "status", fx.Ticket)
	if r.Code != 0 {
		t.Fatalf("jig status exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", r.Code, r.Stdout, r.Stderr)
	}
	assertGolden(t, "status-stalled.txt", r.Stdout)
}

// TestStatusNamesATicketNothingStarted: through the binary, a ticket just
// minted reads `new`, one whose slices nobody has claimed reads `queued`, and
// a claim makes it `building`, which a release does not undo: building means
// a slice was claimed, not that the ticket has slices.
func TestStatusNamesATicketNothingStarted(t *testing.T) {
	t.Parallel()
	fx, env := newFixture(t, fixture.Opts{})
	env = env.without("JIG_SLICE", "JIG_SESSION")

	stateOf := func(ticket string) string {
		t.Helper()
		r := runJig(t, env, fx.StoreDir, "status", ticket)
		if r.Code != 0 {
			t.Fatalf("jig status %s exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", ticket, r.Code, r.Stdout, r.Stderr)
		}
		for _, l := range strings.Split(r.Stdout, "\n") {
			if w, ok := strings.CutPrefix(l, "state: "); ok {
				return w
			}
		}
		t.Fatalf("jig status %s has no state line:\n%s", ticket, r.Stdout)
		return ""
	}
	want := func(ticket, state string) {
		t.Helper()
		if got := stateOf(ticket); got != state {
			t.Errorf("jig status %s: state = %s, want %s", ticket, got, state)
		}
	}

	// The fixture's own ticket has four queued slices and no claim.
	want(fx.Ticket, "queued")

	const ticket = "JIG-2"
	if r := runJig(t, env, fx.StoreDir, "ticket", "new", "--title", "Nothing started"); r.Code != 0 || !strings.Contains(r.Stdout, "id: "+ticket) {
		t.Fatalf("jig ticket new exit = %d, want 0 minting %s\nstdout:\n%s\nstderr:\n%s", r.Code, ticket, r.Stdout, r.Stderr)
	}
	want(ticket, "new")

	slices := "slices:\n" +
		"  - id: x\n    workspace: alpha\n    goal: Build x.\n    oracle: test\n    blocked_by: []\n    from_brief: []\n" +
		"  - id: y\n    workspace: alpha\n    goal: Build y.\n    oracle: test\n    blocked_by: []\n    from_brief: []\n"
	writeFileOrFatal(t, joinPath(fx.StoreDir, "tickets", ticket, "slices.yaml"), []byte(slices))
	want(ticket, "queued")

	if r := runJig(t, env, fx.RepoDir, "claim", ticket, "--slice", "x", "--session", "s-1"); r.Code != 0 {
		t.Fatalf("jig claim exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", r.Code, r.Stdout, r.Stderr)
	}
	want(ticket, "building")

	if r := runJig(t, env, fx.RepoDir, "claim", ticket, "--slice", "x", "--session", "s-1", "--release"); r.Code != 0 {
		t.Fatalf("jig claim --release exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", r.Code, r.Stdout, r.Stderr)
	}
	if got := sliceStateOf(t, fx, ticket, "x"); got != "queued" {
		t.Fatalf("test setup: after the release slice x is %s, want queued", got)
	}
	want(ticket, "building")

	// The other ticket never moved.
	want(fx.Ticket, "queued")
}

// sliceStateOf is the state the store holds for a slice.
func sliceStateOf(t *testing.T, fx *fixture.Fixture, ticket, slice string) string {
	t.Helper()
	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	return sliceState(t, st, ticket, slice)
}
