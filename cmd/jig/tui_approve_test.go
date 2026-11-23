package main

import (
	"testing"

	"github.com/develdeco/jig/internal/journal"
)

// TestTUIApproveRecordsThePerson: the approval the screens' v key runs
// (`jig validate`) is the person's word, because one pressed the key: its line
// has provenance person and no session, though the terminal jig tui was launched
// from names a harness session; the same verb run by a script is an agent's.
func TestTUIApproveRecordsThePerson(t *testing.T) {
	t.Parallel()
	fx, st, e := workFixture(t)
	T := fx.Ticket
	both := map[string]string{"JIG_SESSION": "launch-jig", "CLAUDE_CODE_SESSION_ID": "launch-harness"}

	out, code := tuiRunner(withSessionEnv(e, both))([]string{"validate", T, "--store", fx.StoreDir})
	if code != 0 {
		t.Fatalf("validate through the screens' runner: exit %d\n%s", code, out)
	}
	approvals := linesOf(t, st, T, journal.EventApproval)
	if len(approvals) != 1 {
		t.Fatalf("approval lines = %+v, want one", approvals)
	}
	if l := approvals[0]; l.Provenance != "person" || l.Session != "" {
		t.Errorf("approval through the runner: provenance %q session %q, want the person's and no session", l.Provenance, l.Session)
	}
}
