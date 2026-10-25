package e2e

import (
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/store"
)

// TestValidateFixture asserts `jig validate` accepts the fixture ticket as
// committed: a well-formed brief, slices.yaml matching the current brief
// section hashes, resolvable blocked_by ids with no cycles, workspace ids
// present in the repo manifest, and non-empty oracles.
func TestValidateFixture(t *testing.T) {
	t.Parallel()
	fx, env := newFixture(t, fixture.Opts{})

	r := runJig(t, env, fx.StoreDir, "validate", fx.Ticket)
	if r.Code != 0 {
		t.Fatalf("jig validate exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", r.Code, r.Stdout, r.Stderr)
	}
	if !strings.Contains(r.Stdout, "valid: yes") {
		t.Fatalf("stdout missing \"valid: yes\":\n%s", r.Stdout)
	}
	// The fixture ticket has a brief and no ticket.yaml: it passes, and is
	// advised to be minted.
	if !strings.Contains(r.Stdout, "TICKET_NOT_MINTED") || !strings.Contains(r.Stdout, "jig ticket new") {
		t.Fatalf("stdout lacks the advisory for a ticket that was not minted:\n%s", r.Stdout)
	}
}

// TestValidateCheckRecordsNoApproval: --check is the drafting session's
// validate. It gives the verdict and the section hashes a slices.yaml cites,
// and leaves the brief unapproved, so `jig claim --unattended` still waits for
// the approval a plain validate records once the person has confirmed.
func TestValidateCheckRecordsNoApproval(t *testing.T) {
	t.Parallel()
	fx, env := newFixture(t, fixture.Opts{})

	r := runJig(t, env, fx.StoreDir, "validate", fx.Ticket, "--check")
	if r.Code != 0 || !strings.Contains(r.Stdout, "valid: yes") || !strings.Contains(r.Stdout, "not recorded (--check)") || !strings.Contains(r.Stdout, "sections[") {
		t.Fatalf("jig validate --check exit = %d, want 0 with the verdict, the sections and no approval\nstdout:\n%s\nstderr:\n%s", r.Code, r.Stdout, r.Stderr)
	}
	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok, err := st.ReadApproval(fx.Ticket); err != nil || ok {
		t.Fatalf("after --check the brief is approved (%v, %v)", ok, err)
	}
	if r := runJig(t, env, fx.StoreDir, "validate", fx.Ticket); r.Code != 0 || !strings.Contains(r.Stdout, "result: recorded") {
		t.Fatalf("jig validate after --check exit = %d, want the approval recorded\nstdout:\n%s", r.Code, r.Stdout)
	}
}
