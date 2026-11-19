package verifydeliver

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/project"
)

// TestAGateRoundTakesNoLease: a gate round works in the mapped clone and
// changes none of what the person left there: no directory is made under the
// jig home's pool, the clone's checkout, index and files (a modified tracked
// file and an untracked one among them) and its local branches are as they
// were, and the only refs it gains are origin's, under refs/remotes/origin. A
// repo with no clone mapped is refused CLONE_NOT_MAPPED.
func TestAGateRoundTakesNoLease(t *testing.T) {
	t.Parallel()
	fx, d, head := cloneBuilt(t)
	// What a person has in progress in the clone.
	modified := filepath.Join(fx.RepoDir, "alpha", "alpha.go")
	data, err := os.ReadFile(modified)
	if err != nil {
		t.Fatal(err)
	}
	data = append(data, []byte("// unsaved thought\n")...)
	if err := os.WriteFile(modified, data, 0o644); err != nil {
		t.Fatal(err)
	}
	untracked := filepath.Join(fx.RepoDir, "scratch.txt")
	if err := os.WriteFile(untracked, []byte("notes\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	before := struct{ status, heads, head string }{
		run(t, fx.RepoDir, "status", "--porcelain"),
		run(t, fx.RepoDir, "for-each-ref", "refs/heads"),
		run(t, fx.RepoDir, "rev-parse", "HEAD"),
	}

	p, err := GatePrepare(d, GateOpts{Ticket: fx.Ticket})
	if err != nil {
		t.Fatalf("GatePrepare: %v", err)
	}
	if p.Head != head {
		t.Fatalf("the round reviews %s, want the clone's head %s", p.Head, head)
	}
	if _, err := os.Stat(filepath.Join(fx.Home, "pool")); err == nil {
		t.Fatal("the round made a pool directory under the jig home")
	}
	after := struct{ status, heads, head string }{
		run(t, fx.RepoDir, "status", "--porcelain"),
		run(t, fx.RepoDir, "for-each-ref", "refs/heads"),
		run(t, fx.RepoDir, "rev-parse", "HEAD"),
	}
	if after != before {
		t.Fatalf("the clone changed:\nbefore %+v\nafter  %+v", before, after)
	}
	if got, err := os.ReadFile(modified); err != nil || string(got) != string(data) {
		t.Fatalf("the modified file changed: %v", err)
	}
	if got, err := os.ReadFile(untracked); err != nil || string(got) != "notes\n" {
		t.Fatalf("the untracked file changed: %v", err)
	}
	if got := run(t, fx.RepoDir, "rev-parse", "refs/remotes/origin/main"); got != originRef(t, fx.RepoRemote, "refs/heads/main") {
		t.Errorf("the round did not fetch origin's main into the clone: %s", got)
	}

	// No clone mapped: nothing to read.
	d.Machine = project.MachineProject{}
	_, err = GatePrepare(d, GateOpts{Ticket: fx.Ticket})
	wantAxiCode(t, err, "CLONE_NOT_MAPPED")
	if ae, ok := err.(*axi.Error); ok && len(ae.Help) == 0 {
		t.Error("CLONE_NOT_MAPPED names no way to map a clone")
	}
}
