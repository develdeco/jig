package main

import (
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/store"
)

// TestKeepsClaimRecordsABranchThatNamesNoTicket: a claim made on a branch of
// the session's own, which says nothing about a ticket, records that branch
// as the ticket's. A branch that names a ticket (this one's or another's) or
// is the target branch is left alone, and a ticket that already has a branch
// keeps it.
func TestKeepsClaimRecordsABranchThatNamesNoTicket(t *testing.T) {
	t.Parallel()
	fx, st, e := workFixture(t)
	const other = "JIG-2"
	if err := st.CreateTicketRecord(other, store.Ticket{Title: "Another ticket"}); err != nil {
		t.Fatal(err)
	}
	onBranch := func(name string) {
		t.Helper()
		if _, err := gitx.Run(fx.RepoDir, "switch", name); err != nil {
			gitMust(t, fx.RepoDir, "switch", "-c", name, "main")
		}
	}
	claim := func(slice string) string {
		t.Helper()
		out, code := runMain(t, e, "", "claim", fx.Ticket, "--slice", slice, "--session", "s-claims")
		if code != 0 {
			t.Fatalf("jig claim --slice %s: exit %d\n%s", slice, code, out)
		}
		return out
	}
	recorded := func() string {
		t.Helper()
		rec, err := st.ReadTicket(fx.Ticket)
		if err != nil {
			t.Fatal(err)
		}
		return rec.Branch
	}

	// Branches that are, or name, something of the ticket's own: not recorded.
	for _, branch := range []string{"main", "JIG-1-upload-item-gallery", "jig/JIG-1", other + "-another-ticket"} {
		onBranch(branch)
		claim("a")
		if got := recorded(); got != "" {
			t.Fatalf("a claim on %s recorded %q as the ticket's branch", branch, got)
		}
		if out, code := runMain(t, e, "", "claim", fx.Ticket, "--slice", "a", "--session", "s-claims", "--release"); code != 0 {
			t.Fatalf("jig claim --release: exit %d\n%s", code, out)
		}
	}

	// A branch of the session's own is.
	onBranch("gallery-work")
	out := claim("a")
	if got := recorded(); got != "gallery-work" {
		t.Fatalf("a claim on gallery-work recorded %q, want gallery-work", got)
	}
	if !strings.Contains(out, "Branch gallery-work names no ticket") {
		t.Errorf("the claim does not say it recorded the branch:\n%s", out)
	}

	// The record is the ticket's: a later claim on another branch does not replace it.
	onBranch("other-work")
	claim("c")
	if got := recorded(); got != "gallery-work" {
		t.Errorf("a second claim replaced the recorded branch with %q", got)
	}
}
