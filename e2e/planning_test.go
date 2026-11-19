package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/store"
)

// TestValidateRecordsTheApproval: a valid brief is approved by the person
// whose identity git resolves in the store, with who vouched for it, an
// approval line in the journal and the record pushed; validating an
// unchanged brief again records nothing more, and a machine with no identity
// is refused with the store untouched.
func TestValidateRecordsTheApproval(t *testing.T) {
	t.Parallel()
	fx, env := newFixture(t, fixture.Opts{})
	env = env.withoutIdentity()
	gitLog(t, fx.StoreDir, "config", "user.name", storePersonName)
	gitLog(t, fx.StoreDir, "config", "user.email", storePersonEmail)
	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatal(err)
	}

	base := gitLog(t, fx.StoreDir, "rev-parse", "HEAD")
	r := runJig(t, env, fx.StoreDir, "validate", fx.Ticket)
	if r.Code != 0 || !strings.HasPrefix(r.Stdout, "valid: yes\n") {
		t.Fatalf("validate: exit %d, want valid\nstdout:\n%s\nstderr:\n%s", r.Code, r.Stdout, r.Stderr)
	}
	sum, err := st.BriefSHA256(fx.Ticket)
	if err != nil {
		t.Fatal(err)
	}
	a, ok, err := st.ReadApproval(fx.Ticket)
	if err != nil || !ok {
		t.Fatalf("approval after validate: %v, %v", ok, err)
	}
	if a.By != storePersonName || a.Email != storePersonEmail || a.BriefSHA256 != sum || a.At == "" {
		t.Errorf("approval = %+v, want %s <%s> for the brief %s", a, storePersonName, storePersonEmail, sum)
	}
	if a.Provenance != "agent" {
		t.Errorf("approval provenance = %q, want agent: the command had no terminal on stdin", a.Provenance)
	}
	var line journal.Line
	for _, l := range readJournal(t, fx, fx.Ticket) {
		if l.Event == journal.EventApproval {
			line = l
		}
	}
	if line.Provenance != "agent" || line.Ref != sum[:12] || !strings.Contains(line.Summary, storePersonEmail) {
		t.Errorf("approval line = %+v, want provenance agent, ref %s and the approver", line, sum[:12])
	}
	requireStorePerson(t, "validate", fx.StoreDir, base)
	if status := gitLog(t, fx.StoreDir, "status", "--porcelain"); status != "" {
		t.Errorf("the store is dirty after validate:\n%s", status)
	}
	if got, want := gitLog(t, fx.StoreRemote, "rev-parse", "refs/heads/main"), gitLog(t, fx.StoreDir, "rev-parse", "HEAD"); got != want {
		t.Errorf("origin main = %s, want the store HEAD %s: the approval was not pushed", got, want)
	}

	// The same brief again: the approval stands, nothing is recorded.
	head := gitLog(t, fx.StoreDir, "rev-parse", "HEAD")
	r = runJig(t, env, fx.StoreDir, "validate", fx.Ticket)
	if r.Code != 0 || !strings.Contains(r.Stdout, "  result: unchanged\n") {
		t.Fatalf("second validate: exit %d, want valid with the approval unchanged\n%s", r.Code, r.Stdout)
	}
	if got := gitLog(t, fx.StoreDir, "rev-parse", "HEAD"); got != head {
		t.Errorf("second validate committed %s over %s", got, head)
	}

	// No identity: refused before anything is written.
	fx2, env2 := newFixture(t, fixture.Opts{})
	env2 = env2.withoutIdentity().without("EMAIL")
	gitLog(t, fx2.StoreDir, "config", "--unset", "user.name")
	gitLog(t, fx2.StoreDir, "config", "--unset", "user.email")
	gitLog(t, fx2.StoreDir, "config", "user.useConfigOnly", "true")
	head2 := gitLog(t, fx2.StoreDir, "rev-parse", "HEAD")
	r = runJig(t, env2, fx2.StoreDir, "validate", fx2.Ticket)
	if r.Code != 1 || !strings.Contains(r.Stdout, "IDENTITY_REQUIRED") {
		t.Fatalf("validate with no identity: exit %d, want 1 with IDENTITY_REQUIRED\n%s", r.Code, r.Stdout)
	}
	if _, err := os.Stat(filepath.Join(fx2.StoreDir, "tickets", fx2.Ticket, "approval.yaml")); !os.IsNotExist(err) {
		t.Errorf("the refused validate wrote an approval: %v", err)
	}
	if got := gitLog(t, fx2.StoreDir, "rev-parse", "HEAD"); got != head2 {
		t.Errorf("the refused validate committed: HEAD %s, want %s", got, head2)
	}
	if status := gitLog(t, fx2.StoreDir, "status", "--porcelain"); status != "" {
		t.Errorf("the refused validate left the store dirty:\n%s", status)
	}
}
