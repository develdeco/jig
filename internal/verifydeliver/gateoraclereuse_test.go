package verifydeliver

import (
	"testing"

	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/manifest"
	"github.com/develdeco/jig/internal/session"
	"github.com/develdeco/jig/internal/store"
)

// gateLines returns the ticket's gate-oracle lines of command, in order.
func gateLines(t *testing.T, d Deps, ticket, command string) []journal.Line {
	t.Helper()
	lines, err := journal.Read(d.Store, ticket)
	if err != nil {
		t.Fatal(err)
	}
	var got []journal.Line
	for _, l := range lines {
		if l.Event == journal.EventGateOracle && l.Command == command {
			got = append(got, l)
		}
	}
	return got
}

// TestGateReusesItsOwnEarlierPass: a pass the gate itself journaled stands for
// a later round on the same tree, or on a tree that differs only in light
// paths, as a slice's oracle pass does; the reuse is journaled naming the
// commit it stands on, so the next round chains from it; a failed gate run on a
// tree is journaled and makes the next gate on that tree run the suite.
func TestGateReusesItsOwnEarlierPass(t *testing.T) {
	t.Parallel()
	fx := generateBuilt(t, "rung-a")
	d := newDeps(t, fx)
	alpha, beta := func() string { return testCmd(t, fx, "alpha") }, func() string { return testCmd(t, fx, "beta") }
	var first string

	step(t, "a gate pass is journaled with the env class that was up", func(t *testing.T) {
		rec := &oracleRecorder{}
		d.Oracle = rec.run
		declareLight(t, fx)
		first = commitInBuildLease(t, fx, map[string]string{"alpha/gate-one.txt": "one\n"})

		gateReviewRequest(t, d, fx.Ticket, GateOpts{})
		if ran := rec.ran(); len(ran) != 2 {
			t.Fatalf("the first gate ran %q, want both suite commands", ran)
		}
		got := gateLines(t, d, fx.Ticket, alpha())
		if len(got) != 1 || got[0].Outcome != "pass" || got[0].Commit != first || got[0].Env != "rig" {
			t.Errorf("gate-oracle lines = %+v, want one pass at %s with env rig", got, first)
		}
	})

	step(t, "a light delta runs only the light oracle and journals the reuse", func(t *testing.T) {
		rec := &oracleRecorder{}
		d.Oracle = rec.run
		second := commitInBuildLease(t, fx, map[string]string{"README.md": "notes\n"})

		got := runsByWorkspace(gateReviewRequest(t, d, fx.Ticket, GateOpts{}).OraclesPassed)
		for _, ws := range []string{"alpha", "beta"} {
			if got[ws].ReusedFrom != first || got[ws].Ran != lightGateOracle {
				t.Errorf("%s = %+v, want reused from %s with %q ran", ws, got[ws], first, lightGateOracle)
			}
		}
		if ran := rec.ran(); len(ran) != 1 || ran[0] != lightGateOracle {
			t.Errorf("the gate ran %q, want the light oracle alone", ran)
		}
		lines := gateLines(t, d, fx.Ticket, alpha())
		last := lines[len(lines)-1]
		if last.Commit != second || last.ReusedFrom != first || last.Ran != lightGateOracle || last.Env != "rig" {
			t.Errorf("the reuse line = %+v, want commit %s reused from %s with the light command and env rig", last, second, first)
		}
	})

	step(t, "a second light delta chains from the reuse", func(t *testing.T) {
		rec := &oracleRecorder{}
		d.Oracle = rec.run
		commitInBuildLease(t, fx, map[string]string{"README.md": "more notes\n"})

		got := runsByWorkspace(gateReviewRequest(t, d, fx.Ticket, GateOpts{}).OraclesPassed)
		if got["alpha"].ReusedFrom != first || got["beta"].ReusedFrom != first {
			t.Errorf("runs = %+v, want both reused from %s", got, first)
		}
		if ran := rec.ran(); len(ran) != 1 || ran[0] != lightGateOracle {
			t.Errorf("the gate ran %q, want the light oracle alone", ran)
		}
	})

	step(t, "a delta with a non-light file runs the suite", func(t *testing.T) {
		rec := &oracleRecorder{}
		d.Oracle = rec.run
		commitInBuildLease(t, fx, map[string]string{"alpha/gate-two.txt": "two\n"})

		got := runsByWorkspace(gateReviewRequest(t, d, fx.Ticket, GateOpts{}).OraclesPassed)
		if got["alpha"].ReusedFrom != "" || got["beta"].ReusedFrom != "" {
			t.Errorf("runs = %+v, want none reused", got)
		}
		if ran := rec.ran(); len(ran) != 2 {
			t.Errorf("the gate ran %q, want both suite commands", ran)
		}
	})

	step(t, "a failed gate run is journaled and the next gate on that tree runs the suite", func(t *testing.T) {
		head := commitInBuildLease(t, fx, map[string]string{"alpha/gate-three.txt": "three\n"})
		d.Oracle = func(cmd, _ string) (string, error) {
			if cmd == alpha() {
				return "red", errString("red")
			}
			return "", nil
		}
		backend := stubBackend{run: func(session.Dispatch) error {
			t.Fatal("a round whose oracle fails dispatched a reviewer")
			return nil
		}}
		_, err := Gate(d, newReviewerGateSource(backend), GateOpts{Ticket: fx.Ticket})
		wantAxiCode(t, err, "GATE_ORACLE_FAILED")

		got := gateLines(t, d, fx.Ticket, alpha())
		last := got[len(got)-1]
		if last.Outcome != "fail" || last.Commit != head || last.Cause != "" {
			t.Errorf("the failed gate line = %+v, want a fail at %s with no cause", last, head)
		}

		rec := &oracleRecorder{}
		d.Oracle = rec.run
		runs := runsByWorkspace(gateReviewRequest(t, d, fx.Ticket, GateOpts{}).OraclesPassed)
		if runs["alpha"].ReusedFrom != "" || runs["beta"].ReusedFrom != "" {
			t.Errorf("runs = %+v, want none reused beside a failed run on the tree", runs)
		}
		if ran := rec.ran(); len(ran) != 2 {
			t.Errorf("the gate ran %q, want both suite commands", ran)
		}

		// The rerun passed, so both commands have a pass on this tree; the
		// command that failed here is sampled again, the other stands. A gate
		// opens no round on a head it already reviewed, so the rule is read
		// where the next round would read it.
		st, err := store.Open(fx.StoreDir)
		if err != nil {
			t.Fatal(err)
		}
		slices, err := st.ReadSlices(fx.Ticket)
		if err != nil {
			t.Fatal(err)
		}
		dir := buildLeaseDir(t, fx)
		man, err := manifest.Resolve(dir)
		if err != nil {
			t.Fatal(err)
		}
		reused, err := reusablePasses(d, fx.Ticket, dir, man, slices)
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := reused[alpha()]; ok {
			t.Errorf("alpha's pass stands beside a failed gate run on its tree: %+v", reused[alpha()])
		}
		if p, ok := reused[beta()]; !ok || p.Commit != head {
			t.Errorf("beta's pass = %+v (found %v), want the pass at %s", p, ok, head)
		}
	})
}
