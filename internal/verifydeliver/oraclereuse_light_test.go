package verifydeliver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/session"
)

const lightGateOracle = "echo lint-only"

// declareLight commits the light rule into the build lease's manifest and
// returns that commit.
func declareLight(t *testing.T, fx *fixture.Fixture) string {
	t.Helper()
	path := filepath.Join(buildLeaseDir(t, fx), ".claude", "jig.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	decl := string(data) + "\nlight:\n  paths: [\"*.md\", \"docs/**/*.md\"]\n  oracle: \"" + lightGateOracle + "\"\n"
	return commitInBuildLease(t, fx, map[string]string{".claude/jig.yaml": decl})
}

func runsByWorkspace(runs []OracleRun) map[string]OracleRun {
	got := map[string]OracleRun{}
	for _, r := range runs {
		got[r.Workspace] = r
	}
	return got
}

// TestGateReusesThePassOfALightDelta: a head that differs from the commit of a
// recorded whole-suite pass only in light paths reuses it, runs the light
// oracle on the head and nothing else, and names the origin run and the light
// command; a delta with one non-light file, a failed full run since the pass,
// or a verify light line at the head's own tree each behave as the rule says.
func TestGateReusesThePassOfALightDelta(t *testing.T) {
	t.Parallel()
	fx := generateBuilt(t, "rung-a")
	d := newDeps(t, fx)
	alpha, beta := func() string { return testCmd(t, fx, "alpha") }, func() string { return testCmd(t, fx, "beta") }
	var base string

	step(t, "a Markdown-only delta reuses the pass and runs the light oracle", func(t *testing.T) {
		rec := &oracleRecorder{}
		d.Oracle = rec.run
		base = declareLight(t, fx)
		recordOracle(t, d, fx.Ticket, "pass", base, alpha(), "rig")
		recordOracle(t, d, fx.Ticket, "pass", base, beta(), "rig")
		commitInBuildLease(t, fx, map[string]string{"README.md": "notes\n", "CONTEXT.md": "adr\n"})

		got := runsByWorkspace(gateReviewRequest(t, d, fx.Ticket, GateOpts{}).OraclesPassed)
		for _, ws := range []string{"alpha", "beta"} {
			if got[ws].ReusedFrom != base || got[ws].Ran != lightGateOracle {
				t.Errorf("%s = %+v, want reused from %s with %q ran", ws, got[ws], base, lightGateOracle)
			}
		}
		if ran := rec.ran(); len(ran) != 1 || ran[0] != lightGateOracle {
			t.Errorf("the gate ran %q, want the light oracle alone", ran)
		}
	})

	step(t, "a delta with one non-light file runs the suite", func(t *testing.T) {
		rec := &oracleRecorder{}
		d.Oracle = rec.run
		commitInBuildLease(t, fx, map[string]string{"README.md": "more\n", "alpha/extra.txt": "x\n"})

		got := runsByWorkspace(gateReviewRequest(t, d, fx.Ticket, GateOpts{}).OraclesPassed)
		if got["alpha"].ReusedFrom != "" || got["beta"].ReusedFrom != "" {
			t.Errorf("runs = %+v, want none reused", got)
		}
		if ran := rec.ran(); len(ran) != 2 {
			t.Errorf("the gate ran %q, want both suite commands", ran)
		}
	})

	step(t, "a failed full run since the pass stops the reuse", func(t *testing.T) {
		rec := &oracleRecorder{}
		d.Oracle = rec.run
		head := commitInBuildLease(t, fx, map[string]string{"alpha/extra.txt": "y\n"})
		recordOracle(t, d, fx.Ticket, "pass", head, alpha(), "rig")
		recordOracle(t, d, fx.Ticket, "pass", head, beta(), "rig")
		other := commitInBuildLease(t, fx, map[string]string{"README.md": "pre-failure\n"})
		recordOracle(t, d, fx.Ticket, "fail", other, alpha(), "rig")
		commitInBuildLease(t, fx, map[string]string{"README.md": "after the failure\n"})

		got := runsByWorkspace(gateReviewRequest(t, d, fx.Ticket, GateOpts{}).OraclesPassed)
		if got["alpha"].ReusedFrom != "" {
			t.Errorf("alpha = %+v, want it run after a failed full run", got["alpha"])
		}
		if got["beta"].ReusedFrom != head || got["beta"].Ran != lightGateOracle {
			t.Errorf("beta = %+v, want reused from %s with the light oracle", got["beta"], head)
		}
	})

	step(t, "a verify light line at the head's tree names its origin and runs nothing", func(t *testing.T) {
		rec := &oracleRecorder{}
		d.Oracle = rec.run
		origin := commitInBuildLease(t, fx, map[string]string{"alpha/extra.txt": "w\n"})
		for _, cmd := range []string{alpha(), beta()} {
			full := journal.Line{Slice: "a", Event: "oracle", Outcome: "pass", Commit: origin, Attempt: 1, Command: cmd, Env: "rig", Seconds: 777}
			if err := journal.Append(d.Store, fx.Ticket, full); err != nil {
				t.Fatal(err)
			}
		}
		head := commitInBuildLease(t, fx, map[string]string{"README.md": "verified light\n"})
		for _, cmd := range []string{alpha(), beta()} {
			line := journal.Line{Slice: "a", Event: "oracle", Outcome: "pass", Commit: head, Attempt: 2, Command: cmd, Env: "rig", Seconds: 3, Ran: lightGateOracle, ReusedFrom: origin}
			if err := journal.Append(d.Store, fx.Ticket, line); err != nil {
				t.Fatal(err)
			}
		}

		got := runsByWorkspace(gateReviewRequest(t, d, fx.Ticket, GateOpts{}).OraclesPassed)
		if got["alpha"].ReusedFrom != origin || got["alpha"].Ran != lightGateOracle {
			t.Errorf("alpha = %+v, want the origin %s and the lint", got["alpha"], origin)
		}
		if got["alpha"].Seconds != 777 {
			t.Errorf("alpha seconds = %d, want the suite's 777, not the lint's", got["alpha"].Seconds)
		}
		if ran := rec.ran(); len(ran) != 0 {
			t.Errorf("the gate ran %q, want nothing: the lint ran on this tree already", ran)
		}
	})

	step(t, "a delta that deletes a non-light file runs the suite", func(t *testing.T) {
		rec := &oracleRecorder{}
		d.Oracle = rec.run
		doomed := commitInBuildLease(t, fx, map[string]string{"alpha/doomed.txt": "x\n"})
		recordOracle(t, d, fx.Ticket, "pass", doomed, alpha(), "rig")
		recordOracle(t, d, fx.Ticket, "pass", doomed, beta(), "rig")
		if err := os.Remove(filepath.Join(buildLeaseDir(t, fx), "alpha", "alpha.go")); err != nil {
			t.Fatal(err)
		}
		commitInBuildLease(t, fx, map[string]string{"README.md": "dropped a file\n"})

		got := runsByWorkspace(gateReviewRequest(t, d, fx.Ticket, GateOpts{}).OraclesPassed)
		if got["alpha"].ReusedFrom != "" || got["beta"].ReusedFrom != "" {
			t.Errorf("runs = %+v, want none reused across a deleted code file", got)
		}
	})

	step(t, "a head whose light oracle fails is refused", func(t *testing.T) {
		d.Oracle = func(cmd, _ string) (string, error) {
			if cmd == lightGateOracle {
				return "lint red", errString("lint red")
			}
			return "", nil
		}
		base := commitInBuildLease(t, fx, map[string]string{"alpha/extra.txt": "v\n"})
		recordOracle(t, d, fx.Ticket, "pass", base, alpha(), "rig")
		recordOracle(t, d, fx.Ticket, "pass", base, beta(), "rig")
		commitInBuildLease(t, fx, map[string]string{"README.md": "breaks the lint\n"})

		backend := stubBackend{run: func(session.Dispatch) error {
			t.Fatal("a round whose light oracle fails dispatched a reviewer")
			return nil
		}}
		_, err := Gate(d, newReviewerGateSource(backend), GateOpts{Ticket: fx.Ticket})
		wantAxiCode(t, err, "GATE_ORACLE_FAILED")
	})

	step(t, "no light rule never reuses a pass of another tree", func(t *testing.T) {
		rec := &oracleRecorder{}
		d.Oracle = rec.run
		path := filepath.Join(buildLeaseDir(t, fx), ".claude", "jig.yaml")
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		without := string(data)
		if i := strings.Index(without, "\nlight:"); i >= 0 {
			without = without[:i] + "\n"
		}
		pass := commitInBuildLease(t, fx, map[string]string{".claude/jig.yaml": without})
		recordOracle(t, d, fx.Ticket, "pass", pass, alpha(), "rig")
		commitInBuildLease(t, fx, map[string]string{"README.md": "no rule\n"})

		got := runsByWorkspace(gateReviewRequest(t, d, fx.Ticket, GateOpts{}).OraclesPassed)
		if got["alpha"].ReusedFrom != "" {
			t.Errorf("alpha = %+v, want it run: the manifest declares no light rule", got["alpha"])
		}
	})
}

type errString string

func (e errString) Error() string { return string(e) }
