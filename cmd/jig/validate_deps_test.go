package main

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/project"
	"github.com/develdeco/jig/internal/store"
)

// mustMkdirTicket creates an empty ticket folder for id under st, the
// minimum a ticket.yaml check needs to find the ticket itself.
func mustMkdirTicket(t *testing.T, st *store.Store, id string) {
	t.Helper()
	if err := os.MkdirAll(st.TicketDir(id), 0o755); err != nil {
		t.Fatal(err)
	}
}

// replaceTicketDeps stands in for a hand edit of ticket.yaml's blocked_by:
// the record is rewritten with deps in place of whatever it held, keeping its
// title, or created when the ticket had none. It goes through the store's own
// record writer, so the file stays one jig would have written.
func replaceTicketDeps(t *testing.T, st *store.Store, ticket string, deps []store.TicketBlockedBy) {
	t.Helper()
	rec, err := st.ReadTicket(ticket)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(st.TicketDir(ticket), "ticket.yaml")); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	rec.BlockedBy = deps
	if err := st.CreateTicketRecord(ticket, rec); err != nil {
		t.Fatal(err)
	}
}

// TestValidateTicketDepsAbsent covers a ticket without ticket.yaml: it
// validates as before, with no problems reported.
func TestValidateTicketDepsAbsent(t *testing.T) {
	t.Parallel()
	st := &store.Store{Root: t.TempDir()}
	mustMkdirTicket(t, st, "T-1")

	if got := validateTicketDeps(st, "T-1"); got != nil {
		t.Fatalf("validateTicketDeps on an absent ticket.yaml = %v, want nil", got)
	}
}

// TestValidateTicketDepsParseError covers a ticket.yaml that is not valid
// YAML.
func TestValidateTicketDepsParseError(t *testing.T) {
	t.Parallel()
	st := &store.Store{Root: t.TempDir()}
	mustMkdirTicket(t, st, "T-1")
	path := filepath.Join(st.TicketDir("T-1"), "ticket.yaml")
	if err := os.WriteFile(path, []byte("blocked_by: [this is not valid yaml"), 0o644); err != nil {
		t.Fatal(err)
	}

	got := validateTicketDeps(st, "T-1")
	if len(got) == 0 || !strings.Contains(got[0], "could not be read") {
		t.Fatalf("validateTicketDeps = %v, want a \"could not be read\" problem first", got)
	}
}

// TestValidateTicketDepsCarriesTheReadRefusalsNextSteps covers what follows
// the "could not be read" problem: the next steps ReadTicket's refusal carries
// (which name the fix - upgrade jig, or repair the key - that its message
// alone does not), verbatim and in order, as further lines of the same report,
// for every kind of refusal that has any.
func TestValidateTicketDepsCarriesTheReadRefusalsNextSteps(t *testing.T) {
	t.Parallel()
	for name, content := range map[string]string{
		"not yaml":     "blocked_by: [this is not valid yaml",
		"unknown key":  "schema_version: 1\nfrobnicate: yes\n",
		"newer schema": "schema_version: 2\n",
	} {
		t.Run(name, func(t *testing.T) {
			st := &store.Store{Root: t.TempDir()}
			mustMkdirTicket(t, st, "T-1")
			path := filepath.Join(st.TicketDir("T-1"), "ticket.yaml")
			if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}
			_, readErr := st.ReadTicket("T-1")
			var ae *axi.Error
			if !errors.As(readErr, &ae) || len(ae.Help) == 0 {
				t.Fatalf("test setup: ReadTicket = %v, want an *axi.Error with next steps", readErr)
			}

			want := append([]string{"ticket.yaml could not be read: " + readErr.Error()}, ae.Help...)
			if got := validateTicketDeps(st, "T-1"); !reflect.DeepEqual(got, want) {
				t.Fatalf("validateTicketDeps = %q, want the failure followed by its next steps %q", got, want)
			}
		})
	}
}

// TestValidateTicketDepsMissingTicket covers a blocked_by entry with no
// ticket field.
func TestValidateTicketDepsMissingTicket(t *testing.T) {
	t.Parallel()
	st := &store.Store{Root: t.TempDir()}
	mustMkdirTicket(t, st, "T-1")
	path := filepath.Join(st.TicketDir("T-1"), "ticket.yaml")
	if err := os.WriteFile(path, []byte("blocked_by:\n  - kind: merged\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got := validateTicketDeps(st, "T-1")
	if len(got) != 1 || !strings.Contains(got[0], "entry 1 has no ticket") {
		t.Fatalf("validateTicketDeps = %v, want an \"entry 1 has no ticket\" problem", got)
	}
}

// TestValidateTicketDepsUnknownBlocker covers a blocker the store has no
// ticket folder for.
func TestValidateTicketDepsUnknownBlocker(t *testing.T) {
	t.Parallel()
	st := &store.Store{Root: t.TempDir()}
	mustMkdirTicket(t, st, "T-1")
	replaceTicketDeps(t, st, "T-1", []store.TicketBlockedBy{{Ticket: "T-999", Kind: "merged"}})

	got := validateTicketDeps(st, "T-1")
	if len(got) != 1 || !strings.Contains(got[0], "T-999") || !strings.Contains(got[0], "not found") {
		t.Fatalf("validateTicketDeps = %v, want a problem naming the missing T-999 folder", got)
	}
}

// TestValidateTicketDepsBadKind covers a kind other than merged or stacked.
func TestValidateTicketDepsBadKind(t *testing.T) {
	t.Parallel()
	st := &store.Store{Root: t.TempDir()}
	mustMkdirTicket(t, st, "T-1")
	mustMkdirTicket(t, st, "T-2")
	replaceTicketDeps(t, st, "T-1", []store.TicketBlockedBy{{Ticket: "T-2", Kind: "bogus"}})

	got := validateTicketDeps(st, "T-1")
	if len(got) != 1 || !strings.Contains(got[0], `must be "merged" or "stacked"`) {
		t.Fatalf("validateTicketDeps = %v, want a bad-kind problem", got)
	}
}

// TestValidateTicketDepsCycle covers a cycle followed across two tickets'
// own ticket.yaml files, naming both tickets in the cycle.
func TestValidateTicketDepsCycle(t *testing.T) {
	t.Parallel()
	st := &store.Store{Root: t.TempDir()}
	mustMkdirTicket(t, st, "T-1")
	mustMkdirTicket(t, st, "T-2")
	replaceTicketDeps(t, st, "T-1", []store.TicketBlockedBy{{Ticket: "T-2", Kind: "merged"}})
	replaceTicketDeps(t, st, "T-2", []store.TicketBlockedBy{{Ticket: "T-1", Kind: "merged"}})

	got := validateTicketDeps(st, "T-1")
	var cyc string
	for _, p := range got {
		if strings.Contains(p, "cycle") {
			cyc = p
		}
	}
	if cyc == "" || !strings.Contains(cyc, "T-1") || !strings.Contains(cyc, "T-2") {
		t.Fatalf("validateTicketDeps = %v, want a cycle problem naming T-1 and T-2", got)
	}
}

// TestValidateTicketDepsSelfCycle covers a ticket blocked_by itself.
func TestValidateTicketDepsSelfCycle(t *testing.T) {
	t.Parallel()
	st := &store.Store{Root: t.TempDir()}
	mustMkdirTicket(t, st, "T-1")
	replaceTicketDeps(t, st, "T-1", []store.TicketBlockedBy{{Ticket: "T-1", Kind: "merged"}})

	got := validateTicketDeps(st, "T-1")
	found := false
	for _, p := range got {
		if strings.Contains(p, "cycle") {
			found = true
		}
	}
	if !found {
		t.Fatalf("validateTicketDeps = %v, want a self-cycle problem", got)
	}
}

// TestValidateTicketBranchAbsent covers a ticket without a recorded branch:
// it validates as before, with no problems reported.
func TestValidateTicketBranchAbsent(t *testing.T) {
	t.Parallel()
	st := &store.Store{Root: t.TempDir()}
	mustMkdirTicket(t, st, "T-1")

	if got := validateTicketBranch(st, project.Config{}, "T-1"); got != nil {
		t.Fatalf("validateTicketBranch with no recorded branch = %v, want nil", got)
	}
}

// TestValidateTicketBranchEqualsTarget covers a recorded branch equal to
// target: the same check TicketBranch runs on the hot path before build,
// gate and publish, surfaced here instead so `jig validate` catches a bad
// hand-edited ticket.yaml before any of them do.
func TestValidateTicketBranchEqualsTarget(t *testing.T) {
	t.Parallel()
	st := &store.Store{Root: t.TempDir()}
	mustMkdirTicket(t, st, "T-1")
	if err := st.WriteTicketBranch("T-1", "main"); err != nil {
		t.Fatal(err)
	}

	got := validateTicketBranch(st, project.Config{}, "T-1")
	if len(got) != 1 || !strings.Contains(got[0], "target") {
		t.Fatalf("validateTicketBranch with branch == target = %v, want a single problem naming the target", got)
	}
}

// TestValidateTicketBranchBadRefName covers a recorded branch git itself
// would refuse as a ref name.
func TestValidateTicketBranchBadRefName(t *testing.T) {
	t.Parallel()
	st := &store.Store{Root: t.TempDir()}
	mustMkdirTicket(t, st, "T-1")
	if err := st.WriteTicketBranch("T-1", "bad..name"); err != nil {
		t.Fatal(err)
	}

	got := validateTicketBranch(st, project.Config{}, "T-1")
	if len(got) != 1 || !strings.Contains(got[0], "bad..name") {
		t.Fatalf("validateTicketBranch with a bad ref name = %v, want a single problem naming it", got)
	}
}

// TestValidateTicketBranchSkipsUnreadableRecord covers the overlap with
// validateTicketDeps: a ticket.yaml that cannot be read - an unknown key, or
// a schema_version newer than this jig understands, which fail with
// different codes - is reported once, by validateTicketDeps, not duplicated
// by validateTicketBranch.
func TestValidateTicketBranchSkipsUnreadableRecord(t *testing.T) {
	t.Parallel()
	for name, content := range map[string]string{
		"unknown key":     "schema_version: 1\nfrobnicate: yes\n",
		"newer schema":    "schema_version: 2\nbranch: feature/x\n",
		"branch and typo": "schema_version: 1\nbranch: main\nfrobnicate: yes\n",
	} {
		t.Run(name, func(t *testing.T) {
			st := &store.Store{Root: t.TempDir()}
			mustMkdirTicket(t, st, "T-1")
			path := filepath.Join(st.TicketDir("T-1"), "ticket.yaml")
			if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}

			if got := validateTicketBranch(st, project.Config{}, "T-1"); got != nil {
				t.Fatalf("validateTicketBranch on an unreadable ticket.yaml = %v, want nil (already reported by validateTicketDeps)", got)
			}
		})
	}
}

// TestValidateTicketChecksRecordedBranchAgainstTheConfiguredTarget drives
// validateTicket itself, not validateTicketBranch, with a project whose
// primary repo targets develop: a recorded develop is the target, so it is
// reported; a recorded main is only the default target's name, so it is
// not. It fails if validateTicket stops running the branch check, or if the
// check uses "main" instead of the configured target.
func TestValidateTicketChecksRecordedBranchAgainstTheConfiguredTarget(t *testing.T) {
	t.Parallel()
	cfg := project.Config{Repos: []project.Repo{{Remote: "https://example.invalid/org/demo.git", Target: "develop"}}}
	st := &store.Store{Root: t.TempDir()}
	mustMkdirTicket(t, st, "T-1")

	problems := func() []string {
		t.Helper()
		got, err := validateTicket(st, cfg, project.MachineProject{}, "T-1")
		if err != nil {
			t.Fatalf("validateTicket: %v", err)
		}
		return got
	}
	record := func(branch string) {
		t.Helper()
		if err := st.WriteTicketBranch("T-1", branch); err != nil {
			t.Fatal(err)
		}
	}

	// What validateTicket reports about this bare ticket folder (no brief,
	// no clone) whatever branch it records: the branch check adds to it.
	baseline := problems()

	record("develop")
	_, wantErr := st.TicketBranch("T-1", "develop")
	if wantErr == nil {
		t.Fatal("test setup: a recorded develop must be refused as the target develop")
	}
	var rest []string
	found := 0
	for _, p := range problems() {
		if p == wantErr.Error() {
			found++
			continue
		}
		rest = append(rest, p)
	}
	if found != 1 || !reflect.DeepEqual(rest, baseline) {
		t.Fatalf("validateTicket with the recorded target branch develop: the branch problem appeared %d times (want 1), and the rest was %q (want the baseline %q)", found, rest, baseline)
	}

	record("main")
	if got := problems(); !reflect.DeepEqual(got, baseline) {
		t.Fatalf("validateTicket with a recorded main under target develop:\n got %q\nwant the baseline %q (main is not this project's target)", got, baseline)
	}
}

// TestValidateCommandReportsTicketDepsProblem drives `jig validate` end to
// end against a store with two minted tickets whose ticket.yaml files block
// each other, checking the CLI surfaces the cycle as a validation failure.
func TestValidateCommandReportsTicketDepsProblem(t *testing.T) {
	t.Parallel()
	jig, storeRoot := setupGraduateStore(t)

	if code, out := jig("ticket", "new", "--title", "A"); code != 0 {
		t.Fatalf("jig ticket new A: exit %d\n%s", code, out)
	}
	if code, out := jig("ticket", "new", "--title", "B"); code != 0 {
		t.Fatalf("jig ticket new B: exit %d\n%s", code, out)
	}

	st, err := store.Open(storeRoot)
	if err != nil {
		t.Fatal(err)
	}
	ents, err := os.ReadDir(filepath.Join(storeRoot, "tickets"))
	if err != nil {
		t.Fatal(err)
	}
	var ids []string
	for _, e := range ents {
		if e.IsDir() && strings.HasPrefix(e.Name(), "DEMO-") {
			ids = append(ids, e.Name())
		}
	}
	if len(ids) != 2 {
		t.Fatalf("expected 2 minted tickets, got %v", ids)
	}
	idA, idB := ids[0], ids[1]

	replaceTicketDeps(t, st, idA, []store.TicketBlockedBy{{Ticket: idB, Kind: "merged"}})
	replaceTicketDeps(t, st, idB, []store.TicketBlockedBy{{Ticket: idA, Kind: "merged"}})

	code, out := jig("validate", idA)
	if code == 0 {
		t.Fatalf("jig validate %s: exit 0, want a refusal:\n%s", idA, out)
	}
	if !strings.Contains(out, "cycle") || !strings.Contains(out, idA) || !strings.Contains(out, idB) {
		t.Fatalf("jig validate %s: output missing a cycle problem naming both tickets:\n%s", idA, out)
	}
}
