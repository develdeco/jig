package e2e

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

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

// TestValidateSlicesUnconnectedInOneRepo: SLICES_UNCONNECTED is about one
// repo's slices falling in two groups. Repos built in parallel with no edge
// between them are not warned; two groups inside one repo are; and two groups
// of a repo that connect only through the other repo's slice are one group.
func TestValidateSlicesUnconnectedInOneRepo(t *testing.T) {
	t.Parallel()
	fx, env := newFixture(t, fixture.Opts{Orchestrator: true})
	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatal(err)
	}
	validate := func(label string) string {
		t.Helper()
		r := runJig(t, env, fx.OrchestratorDir, "validate", fx.Ticket, "--check")
		if r.Code != 0 || !strings.Contains(r.Stdout, "valid: yes") {
			t.Fatalf("%s: jig validate exit = %d, want 0 and valid\nstdout:\n%s\nstderr:\n%s", label, r.Code, r.Stdout, r.Stderr)
		}
		return r.Stdout
	}

	if out := validate("two repos, no edge"); strings.Contains(out, "SLICES_UNCONNECTED") {
		t.Fatalf("a ticket whose repos build in parallel is warned:\n%s", out)
	}

	slices, err := st.ReadSlices(fx.Ticket)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.AppendSlices(fx.Ticket, []store.Slice{{ID: "server-2", Workspace: "api/server", Goal: "A second server concern.", Oracle: "test", BlockedBy: []string{}, FromBrief: slices[0].FromBrief}}); err != nil {
		t.Fatal(err)
	}
	out := validate("two groups in api")
	if !strings.Contains(out, "SLICES_UNCONNECTED") || !strings.Contains(out, "repo api") || strings.Contains(out, "repo web") {
		t.Fatalf("want one SLICES_UNCONNECTED for repo api and none for repo web:\n%s", out)
	}

	byID := map[string]*store.Slice{}
	all, err := st.ReadSlices(fx.Ticket)
	if err != nil {
		t.Fatal(err)
	}
	for i := range all {
		byID[all[i].ID] = &all[i]
	}
	byID["ui"].BlockedBy = []string{"server"}
	byID["server-2"].BlockedBy = []string{"ui"}
	data, err := yaml.Marshal(store.SliceFile{Slices: all})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(st.TicketDir(fx.Ticket), "slices.yaml"), data, 0o644); err != nil {
		t.Fatal(err)
	}
	if out := validate("api groups joined through web"); strings.Contains(out, "SLICES_UNCONNECTED") {
		t.Fatalf("two groups that connect only through the other repo are warned:\n%s", out)
	}
}
