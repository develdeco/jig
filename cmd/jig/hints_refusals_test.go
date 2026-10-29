package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/store"
)

// TestHintsMatchRefusals: whenever a claim is refused or warned, or a gate is
// refused for a slice that is not green, the refusal's help and the hints `jig
// status`, `jig here` and `jig next` print name the same prompt, since all of
// them read the one derivation (internal/phase). phaseAdvice's table pins each
// line; this is the check that each verb ends in it. The cases share one
// fixture and each has a ticket of its own.
func TestHintsMatchRefusals(t *testing.T) {
	t.Parallel()
	fx, st, e := workFixture(t)

	// named checks that every output names prompt, after "ask: " (the hints and
	// the refusals) or as `jig next`'s prompt key.
	named := func(t *testing.T, what, prompt string, outs map[string]string) {
		t.Helper()
		for name, out := range outs {
			if !strings.Contains(out, "ask: "+prompt) && !strings.Contains(out, "prompt: "+prompt+"\n") {
				t.Errorf("%s: %s does not name the prompt %q:\n%s", what, name, prompt, out)
			}
		}
	}
	// hints runs the three commands that advise the next step.
	hints := func(t *testing.T, ticket string) (status, here, next string) {
		t.Helper()
		status, _ = runMain(t, e, "", "status", ticket)
		here, _ = runMain(t, e, "", "here", ticket)
		next, _ = runMain(t, e, "", "next", ticket)
		return status, here, next
	}
	// ticket adds the ticket id, approved, with one slice x that waits for
	// blocker to merge when one is named.
	ticket := func(t *testing.T, id, blocker string) {
		t.Helper()
		rec := store.Ticket{Title: "Ticket " + id}
		if blocker != "" {
			rec.BlockedBy = []store.TicketBlockedBy{{Ticket: blocker, Kind: "merged"}}
		}
		if err := st.CreateTicketRecord(id, rec); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(st.TicketDir(id), "brief.md"), []byte("# Ticket "+id+"\n\n## Goal\n\nDo it.\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := st.AppendSlices(id, []store.Slice{{ID: "x", Workspace: "alpha", Goal: "Do it.", Oracle: "test", BlockedBy: []string{}, FromBrief: []string{}}}); err != nil {
			t.Fatal(err)
		}
		approveBrief(t, st, id)
	}

	t.Run("a brief nobody approved", func(t *testing.T) {
		T := fx.Ticket
		const prompt = "Plan JIG-1"

		refused, code := runMain(t, e, "", "claim", T, "--slice", "a", "--unattended")
		if code != 1 || !strings.Contains(refused, "code: CLAIM_NOT_APPROVED") {
			t.Fatalf("unattended claim: exit %d, want CLAIM_NOT_APPROVED\n%s", code, refused)
		}
		status, here, next := hints(t, T)
		named(t, "claim refused", prompt, map[string]string{"the refusal": refused, "status": status, "here": here, "next": next})
		if !strings.Contains(refused, prompt+" (approve its brief)") {
			t.Errorf("the refusal does not say what the prompt does:\n%s", refused)
		}

		// A person watching is warned, and the claim goes through.
		warned, code := runMain(t, e, "", "claim", T, "--slice", "a")
		if code != 0 || !strings.Contains(warned, "warnings[1]{code,message,help}:") {
			t.Fatalf("attended claim: exit %d, want a warning\n%s", code, warned)
		}
		named(t, "claim warned", prompt, map[string]string{"the warning": warned})

		// With the work under way the ticket is past the brief, whatever the
		// warning goes on saying: the hints move on to the build.
		status, _, _ = hints(t, T)
		named(t, "after the person went ahead", "Build JIG-1", map[string]string{"status": status})
	})

	t.Run("a brief that changed after its approval", func(t *testing.T) {
		ticket(t, "JIG-2", "")
		if err := os.WriteFile(filepath.Join(st.TicketDir("JIG-2"), "brief.md"), []byte("# Ticket JIG-2\n\n## Goal\n\nChanged after it was approved.\n"), 0o644); err != nil {
			t.Fatal(err)
		}

		refused, code := runMain(t, e, "", "claim", "JIG-2", "--slice", "x", "--unattended")
		if code != 1 || !strings.Contains(refused, "changed after it was approved") {
			t.Fatalf("claim after the change: exit %d\n%s", code, refused)
		}
		status, here, next := hints(t, "JIG-2")
		named(t, "claim refused", "Plan JIG-2", map[string]string{"the refusal": refused, "status": status, "here": here, "next": next})
	})

	t.Run("a slice that is not green", func(t *testing.T) {
		ticket(t, "JIG-3", "")

		for _, args := range [][]string{{"gate", "JIG-3"}, {"review", "context", "JIG-3"}} {
			name := "jig " + strings.Join(args, " ")
			refused, code := runMain(t, e, "", args...)
			if code != 1 || !strings.Contains(refused, "code: GATE_NOT_GREEN") {
				t.Fatalf("%s: exit %d, want GATE_NOT_GREEN\n%s", name, code, refused)
			}
			status, here, next := hints(t, "JIG-3")
			named(t, name+" refused", "Build JIG-3", map[string]string{"the refusal": refused, "status": status, "here": here, "next": next})
		}
	})

	t.Run("a blocker that has not published", func(t *testing.T) {
		// The blocker is a ticket at the build, so building it is what makes
		// JIG-5 ready.
		ticket(t, "JIG-4", "")
		ticket(t, "JIG-5", "JIG-4")
		const prompt = "Build JIG-4"
		refused, code := runMain(t, e, "", "claim", "JIG-5", "--slice", "x", "--unattended")
		if code != 1 || !strings.Contains(refused, "code: CLAIM_BLOCKED_MERGED") {
			t.Fatalf("claim of a blocked ticket: exit %d\n%s", code, refused)
		}
		status, here, next := hints(t, "JIG-5")
		named(t, "claim refused", prompt, map[string]string{"the refusal": refused, "status": status, "here": here, "next": next})
		if !strings.Contains(next, "code: PHASE_BLOCKED\n") || !strings.Contains(next, "ready: no\n") {
			t.Errorf("next does not say the build is blocked:\n%s", next)
		}
	})
}
