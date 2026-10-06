package verifydeliver

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/journal"
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

// recordOraclePass journals the pass frontier writes when a slice's oracle
// passes at its green on a clean tree (ADR 0020).
func recordOraclePass(t *testing.T, d Deps, ticket, slice, commit string) {
	t.Helper()
	if err := journal.Append(d.Store, ticket, journal.Line{Slice: slice, Event: "oracle", Outcome: "pass", Commit: commit, Attempt: 1}); err != nil {
		t.Fatal(err)
	}
}

// TestGateReusesAnOraclePassOnTheSameTree covers ADR 0021 at the gate: a run
// of the suite that a slice's oracle already passed on a commit whose tree is
// the gate's head tree is reused, not run again, and review.json says so;
// every other run still runs.
func TestGateReusesAnOraclePassOnTheSameTree(t *testing.T) {
	t.Parallel()
	// The fixture repo's suite is test@alpha and test@beta; slice a builds in
	// alpha and slice c in beta.
	failing := map[string]string{
		"alpha/zz_red_test.go": "package alpha\n\nimport \"testing\"\n\nfunc TestRed(t *testing.T) { t.Fatal(\"red on purpose\") }\n",
		"beta/zz_red_test.go":  "package beta\n\nimport \"testing\"\n\nfunc TestRed(t *testing.T) { t.Fatal(\"red on purpose\") }\n",
	}

	t.Run("every run reused", func(t *testing.T) {
		t.Parallel()
		fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
		driveBuild(t, fx, "rung-a")
		d := newDeps(t, fx)
		// Tests that fail are committed at the head, so a gate that ran the
		// oracles itself would stop with GATE_ORACLE_FAILED and fail
		// gateReviewRequest: the recorded passes are what lets it through.
		head := commitInBuildLease(t, fx, failing)
		recordOraclePass(t, d, fx.Ticket, "a", head)
		recordOraclePass(t, d, fx.Ticket, "c", head)

		runs := gateReviewRequest(t, d, fx.Ticket, GateOpts{}).OraclesPassed
		if len(runs) != 2 {
			t.Fatalf("oracles_passed = %+v, want test@alpha and test@beta", runs)
		}
		for _, r := range runs {
			if r.ReusedFrom != head {
				t.Errorf("oracles_passed entry %+v, want reused_from %s", r, head)
			}
		}
	})

	t.Run("a pass on another tree is not reused", func(t *testing.T) {
		t.Parallel()
		fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
		driveBuild(t, fx, "rung-a")
		d := newDeps(t, fx)
		earlier := commitInBuildLease(t, fx, map[string]string{"alpha/note.txt": "one\n"})
		recordOraclePass(t, d, fx.Ticket, "a", earlier)
		recordOraclePass(t, d, fx.Ticket, "c", earlier)
		commitInBuildLease(t, fx, map[string]string{"alpha/note.txt": "two\n"})

		for _, r := range gateReviewRequest(t, d, fx.Ticket, GateOpts{}).OraclesPassed {
			if r.ReusedFrom != "" {
				t.Errorf("oracles_passed entry %+v reused a pass from a different tree", r)
			}
		}
	})

	t.Run("a partial pass runs the rest", func(t *testing.T) {
		t.Parallel()
		fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
		driveBuild(t, fx, "rung-a")
		d := newDeps(t, fx)
		head := commitInBuildLease(t, fx, map[string]string{"alpha/note.txt": "one\n"})
		recordOraclePass(t, d, fx.Ticket, "a", head)

		got := map[string]string{}
		for _, r := range gateReviewRequest(t, d, fx.Ticket, GateOpts{}).OraclesPassed {
			got[r.Workspace] = r.ReusedFrom
		}
		if got["alpha"] != head || got["beta"] != "" {
			t.Errorf("reused_from by workspace = %v, want alpha reused from %s and beta run", got, head)
		}
	})
}
