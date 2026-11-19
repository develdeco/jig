package store

import (
	"strings"
	"testing"
)

// TestSeamPlanWritersSaysWhatTheMoveWouldDoAndChangesNothing: the plan names
// the writer and branch and counts the id lines the claims branch would start
// with, and reading it leaves the store on main, in single-main mode, with no
// claims branch; once the move is made it reports the claims as there.
func TestSeamPlanWritersSaysWhatTheMoveWouldDoAndChangesNothing(t *testing.T) {
	t.Parallel()
	st, work, remote := newTestRemoteStore(t)
	runGit(t, work, "config", "jig.writer", "alice")
	mintTicket(t, st, "one")

	plan, err := st.PlanWriters()
	if err != nil {
		t.Fatalf("PlanWriters: %v", err)
	}
	if plan.Writer != "alice" || plan.Branch != "writers/alice" || plan.HasClaims || plan.IDs != 1 {
		t.Errorf("plan = %+v, want alice on writers/alice, no claims yet, one id", plan)
	}
	if on, _ := st.Mode(); on {
		t.Error("reading the plan made the store a writers store")
	}
	if got := strings.TrimSpace(runGit(t, work, "symbolic-ref", "--short", "HEAD")); got != "main" {
		t.Errorf("reading the plan left the clone on %q", got)
	}
	if out := runGit(t, "", "--git-dir", remote, "branch", "--list", "claims"); strings.TrimSpace(out) != "" {
		t.Errorf("reading the plan made the claims branch: %q", out)
	}

	if _, err := st.MigrateToWriters(); err != nil {
		t.Fatal(err)
	}
	after, err := st.PlanWriters()
	if err != nil || !after.HasClaims {
		t.Errorf("plan after the move = %+v, %v; want the claims reported as there", after, err)
	}
}
