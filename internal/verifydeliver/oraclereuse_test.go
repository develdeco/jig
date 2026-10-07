package verifydeliver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/manifest"
	"github.com/develdeco/jig/internal/session"
)

// commitInBuildLease writes files (repo-relative path to content) in the
// build lease, commits them on the ticket branch and pushes it, and returns
// the new commit.
func commitInBuildLease(t *testing.T, fx *fixture.Fixture, files map[string]string) string {
	t.Helper()
	dir := buildLeaseDir(t, fx)
	for rel, body := range files {
		if err := os.WriteFile(filepath.Join(dir, filepath.FromSlash(rel)), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := gitx.Run(dir, "add", "-A"); err != nil {
		t.Fatalf("git add: %v", err)
	}
	if _, err := gitx.RunEnv(dir, buildGitEnv, "commit", "-q", "-m", "oracle reuse step"); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if _, err := gitx.Run(dir, "push", "origin", ticketBranch(fx.Ticket)); err != nil {
		t.Fatalf("push: %v", err)
	}
	head, err := gitx.RevParse(dir, "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	return head
}

// testCmd is the fixture repo's test oracle command in workspace ws, as the
// manifest at the build lease's head resolves it: the command frontier
// records on an oracle line.
func testCmd(t *testing.T, fx *fixture.Fixture, ws string) string {
	t.Helper()
	man, err := manifest.Resolve(buildLeaseDir(t, fx))
	if err != nil {
		t.Fatal(err)
	}
	w, ok := man.Workspace(ws)
	if !ok {
		t.Fatalf("no workspace %s", ws)
	}
	return man.OracleCmd("test", w)
}

// recordOracle journals an oracle line the way frontier writes one at a
// slice's green (ADR 0020).
func recordOracle(t *testing.T, d Deps, ticket, outcome, commit, command, env string) {
	t.Helper()
	if err := journal.Append(d.Store, ticket, journal.Line{Slice: "a", Event: "oracle", Outcome: outcome, Commit: commit, Attempt: 1, Command: command, Env: env}); err != nil {
		t.Fatal(err)
	}
}

// reusedByWorkspace maps each oracles_passed entry's workspace to its
// reused_from.
func reusedByWorkspace(runs []OracleRun) map[string]string {
	got := map[string]string{}
	for _, r := range runs {
		got[r.Workspace] = r.ReusedFrom
	}
	return got
}

// TestGateReusesAnOraclePassOnTheSameTree covers ADR 0021 at the gate: a run
// of the suite that jig already passed at a slice's green, with the same
// command, on a commit with the gate head's tree, under the same env classes
// and with no failed run beside it, is reused, not run again, and review.json
// says so; every other run still runs. The fixture repo's suite is
// test@alpha and test@beta, and its slice d names the env class rig, so the
// gate brings rig up and a pass stands for its run only with rig up too.
func TestGateReusesAnOraclePassOnTheSameTree(t *testing.T) {
	t.Parallel()
	failing := map[string]string{
		"alpha/zz_red_test.go": "package alpha\n\nimport \"testing\"\n\nfunc TestRed(t *testing.T) { t.Fatal(\"red on purpose\") }\n",
		"beta/zz_red_test.go":  "package beta\n\nimport \"testing\"\n\nfunc TestRed(t *testing.T) { t.Fatal(\"red on purpose\") }\n",
	}
	note := func(s string) map[string]string { return map[string]string{"alpha/note.txt": s} }

	t.Run("every run reused", func(t *testing.T) {
		t.Parallel()
		fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
		driveBuild(t, fx, "rung-a")
		d := newDeps(t, fx)
		rec := &oracleRecorder{}
		d.Oracle = rec.run
		// Tests that fail are committed at the head, so a gate that ran the
		// oracles for real would stop with GATE_ORACLE_FAILED: the recorded
		// passes are what lets it through, and the gate runs nothing.
		head := commitInBuildLease(t, fx, failing)
		recordOracle(t, d, fx.Ticket, "pass", head, testCmd(t, fx, "alpha"), "rig")
		recordOracle(t, d, fx.Ticket, "pass", head, testCmd(t, fx, "beta"), "rig")

		got := reusedByWorkspace(gateReviewRequest(t, d, fx.Ticket, GateOpts{}).OraclesPassed)
		if len(got) != 2 || got["alpha"] != head || got["beta"] != head {
			t.Errorf("reused_from by workspace = %v, want both reused from %s", got, head)
		}
		if ran := rec.ran(); len(ran) != 0 {
			t.Errorf("the gate ran %q, want nothing run", ran)
		}
	})

	t.Run("a partial pass runs the rest", func(t *testing.T) {
		t.Parallel()
		fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
		driveBuild(t, fx, "rung-a")
		d := newDeps(t, fx)
		rec := &oracleRecorder{}
		d.Oracle = rec.run
		head := commitInBuildLease(t, fx, note("one\n"))
		recordOracle(t, d, fx.Ticket, "pass", head, testCmd(t, fx, "alpha"), "rig")

		got := reusedByWorkspace(gateReviewRequest(t, d, fx.Ticket, GateOpts{}).OraclesPassed)
		if got["alpha"] != head || got["beta"] != "" {
			t.Errorf("reused_from by workspace = %v, want alpha reused from %s and beta run", got, head)
		}
		if ran := rec.ran(); len(ran) != 1 || ran[0] != testCmd(t, fx, "beta") {
			t.Errorf("the gate ran %q, want only test@beta", ran)
		}
	})

	// Each case below records a pass that must not stand for the gate's run
	// of test@alpha; the head's tests pass, so the gate runs it and the round
	// goes on.
	for _, tc := range []struct {
		name   string
		record func(t *testing.T, fx *fixture.Fixture, d Deps, head string)
	}{
		{"a pass on another tree", func(t *testing.T, fx *fixture.Fixture, d Deps, _ string) {
			t.Helper()
			// commitInBuildLease below this record makes a new head.
			earlier, err := gitx.RevParse(buildLeaseDir(t, fx), "HEAD~1")
			if err != nil {
				t.Fatal(err)
			}
			recordOracle(t, d, fx.Ticket, "pass", earlier, testCmd(t, fx, "alpha"), "rig")
		}},
		{"another env class set", func(t *testing.T, fx *fixture.Fixture, d Deps, head string) {
			t.Helper()
			recordOracle(t, d, fx.Ticket, "pass", head, testCmd(t, fx, "alpha"), "")
		}},
		{"another command", func(t *testing.T, fx *fixture.Fixture, d Deps, head string) {
			t.Helper()
			recordOracle(t, d, fx.Ticket, "pass", head, testCmd(t, fx, "alpha")+" -run TestNothing", "rig")
		}},
		{"a failed run on the same tree", func(t *testing.T, fx *fixture.Fixture, d Deps, head string) {
			t.Helper()
			recordOracle(t, d, fx.Ticket, "fail", head, testCmd(t, fx, "alpha"), "rig")
			recordOracle(t, d, fx.Ticket, "pass", head, testCmd(t, fx, "alpha"), "rig")
		}},
	} {
		t.Run(tc.name+" is not reused", func(t *testing.T) {
			t.Parallel()
			fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
			driveBuild(t, fx, "rung-a")
			d := newDeps(t, fx)
			rec := &oracleRecorder{}
			d.Oracle = rec.run
			commitInBuildLease(t, fx, note("one\n"))
			head := commitInBuildLease(t, fx, note("two\n"))
			tc.record(t, fx, d, head)

			if got := reusedByWorkspace(gateReviewRequest(t, d, fx.Ticket, GateOpts{}).OraclesPassed); got["alpha"] != "" {
				t.Errorf("test@alpha reused from %s, want it run", got["alpha"])
			}
			alpha := testCmd(t, fx, "alpha")
			found := false
			for _, c := range rec.ran() {
				found = found || c == alpha
			}
			if !found {
				t.Errorf("the gate ran %q, want test@alpha among them", rec.ran())
			}
		})
	}

	t.Run("a failed gate run says why", func(t *testing.T) {
		t.Parallel()
		fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
		driveBuild(t, fx, "rung-a")
		d := newDeps(t, fx)
		d.Oracle = nil // reuse is judged against real oracle runs
		commitInBuildLease(t, fx, failing)

		backend := stubBackend{run: func(session.Dispatch) error {
			t.Fatal("a round whose oracles fail dispatched a reviewer")
			return nil
		}}
		_, err := Gate(d, NewReviewerGateSource(backend), GateOpts{Ticket: fx.Ticket, NoDemo: true})
		wantAxiCode(t, err, "GATE_ORACLE_FAILED")
		if !strings.Contains(err.Error(), "red on purpose") {
			t.Errorf("GATE_ORACLE_FAILED = %v, want the failing test's output in it", err)
		}
	})
}
