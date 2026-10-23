package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/store"
)

// checkStore is a store of bare ticket folders (it is not a git repo), with
// ticket JIG-2 blocked by JIG-1 of the given kind and holding a brief.
func checkStore(t *testing.T, kind string) *store.Store {
	t.Helper()
	st := &store.Store{Root: t.TempDir()}
	put := func(ticket, name, content string) {
		path := filepath.Join(st.TicketDir(ticket), name)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	put("JIG-1", "ticket.yaml", "schema_version: 1\ntitle: First\n")
	put("JIG-2", "ticket.yaml", fmt.Sprintf("schema_version: 1\ntitle: Second\nblocked_by:\n  - ticket: JIG-1\n    kind: %s\n", kind))
	put("JIG-2", "brief.md", "# Second\n\n## Goal\n\nDo it.\n")
	return st
}

func approveBrief(t *testing.T, st *store.Store, ticket string) {
	t.Helper()
	sum, err := st.BriefSHA256(ticket)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.WriteApproval(ticket, store.Approval{By: "P", At: "2026-10-23T00:00:00Z", BriefSHA256: sum}); err != nil {
		t.Fatal(err)
	}
}

func codes(problems []claimProblem) string {
	var out []string
	for _, p := range problems {
		out = append(out, p.Code)
	}
	return strings.Join(out, ",")
}

// TestClaimProblemsOfAMergedBlocker: a merged blocker meets the claim once it
// published, and when it has a pull request the host can tell is not merged
// it does not; a host that cannot tell blocks nothing.
func TestClaimProblemsOfAMergedBlocker(t *testing.T) {
	t.Parallel()
	st := checkStore(t, "merged")
	approveBrief(t, st, "JIG-2")
	never := claimDeps{merged: func(string) (bool, bool) {
		t.Error("the host was asked about a blocker that had not published")
		return true, true
	}}

	got, err := claimProblems(st, "JIG-2", never)
	if err != nil || codes(got) != claimBlockedMerged || !strings.Contains(got[0].Message, "JIG-1") {
		t.Fatalf("unpublished blocker: %+v, %v, want CLAIM_BLOCKED_MERGED naming JIG-1", got, err)
	}

	for _, l := range []journal.Line{{Event: journal.EventPublishDone}, {Event: journal.EventPr, URL: "https://github.com/o/r/pull/4"}} {
		if err := journal.Append(st, "JIG-1", l); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		name          string
		merged, known bool
		want          string
	}{
		{"merged", true, true, ""},
		{"not merged yet", false, true, claimBlockedMerged},
		{"the host cannot tell", false, false, ""},
	} {
		var asked string
		deps := claimDeps{merged: func(url string) (bool, bool) { asked = url; return tc.merged, tc.known }}
		got, err := claimProblems(st, "JIG-2", deps)
		if err != nil || codes(got) != tc.want {
			t.Errorf("%s: %+v, %v, want %q", tc.name, got, err, tc.want)
		}
		if asked != "https://github.com/o/r/pull/4" {
			t.Errorf("%s: the host was asked about %q, want the pull request of the blocker", tc.name, asked)
		}
	}
	if got, _ := claimProblems(st, "JIG-2", claimDeps{}); got != nil {
		t.Errorf("with no host to ask, a published blocker still blocks: %+v", got)
	}
}

// TestClaimProblemsOfAStackedParent: a stacked blocker meets the claim only
// with a clean gate round as its latest.
func TestClaimProblemsOfAStackedParent(t *testing.T) {
	t.Parallel()
	st := checkStore(t, "stacked")
	approveBrief(t, st, "JIG-2")
	round := func(n int, verdict string) {
		dir := filepath.Join(st.TicketDir("JIG-1"), "gate", fmt.Sprintf("round-%d", n))
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "report.yaml"), []byte("verdict: "+verdict+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		name    string
		verdict string
		round   int
		want    string
	}{
		{"no round yet", "", 0, claimStackedParentNotClean},
		{"a round that found things", "findings", 1, claimStackedParentNotClean},
		{"a clean round after it", "clean", 2, ""},
	} {
		if tc.round > 0 {
			round(tc.round, tc.verdict)
		}
		got, err := claimProblems(st, "JIG-2", claimDeps{})
		if err != nil || codes(got) != tc.want {
			t.Errorf("%s: %+v, %v, want %q", tc.name, got, err, tc.want)
		}
	}
}

// TestClaimProblemsOfTheApproval: a brief with no approval, or edited since,
// does not meet the claim; a ticket with no brief has nothing to approve.
func TestClaimProblemsOfTheApproval(t *testing.T) {
	t.Parallel()
	st := checkStore(t, "merged")
	if err := journal.Append(st, "JIG-1", journal.Line{Event: journal.EventPublishDone}); err != nil {
		t.Fatal(err)
	}
	got, err := claimProblems(st, "JIG-2", claimDeps{})
	if err != nil || codes(got) != claimNotApproved || !strings.Contains(got[0].Message, "not approved") {
		t.Fatalf("no approval: %+v, %v, want CLAIM_NOT_APPROVED", got, err)
	}
	approveBrief(t, st, "JIG-2")
	if got, err := claimProblems(st, "JIG-2", claimDeps{}); err != nil || got != nil {
		t.Fatalf("approved: %+v, %v, want none", got, err)
	}
	brief := filepath.Join(st.TicketDir("JIG-2"), "brief.md")
	if err := os.WriteFile(brief, []byte("# Second\n\n## Goal\n\nDo more.\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, err = claimProblems(st, "JIG-2", claimDeps{})
	if err != nil || codes(got) != claimNotApproved || !strings.Contains(got[0].Message, "changed after") {
		t.Fatalf("edited brief: %+v, %v, want CLAIM_NOT_APPROVED saying it changed", got, err)
	}
	if err := os.Remove(brief); err != nil {
		t.Fatal(err)
	}
	if got, err := claimProblems(st, "JIG-2", claimDeps{}); err != nil || got != nil {
		t.Fatalf("no brief: %+v, %v, want nothing to approve", got, err)
	}
}

// TestClaimProblemsListEveryOneInOrder: a ticket that fails all its checks
// is told each, blockers first and the approval last.
func TestClaimProblemsListEveryOneInOrder(t *testing.T) {
	t.Parallel()
	st := checkStore(t, "merged")
	got, err := claimProblems(st, "JIG-2", claimDeps{})
	if err != nil || codes(got) != claimBlockedMerged+","+claimNotApproved {
		t.Fatalf("%+v, %v, want the blocker, then the approval", got, err)
	}
}
