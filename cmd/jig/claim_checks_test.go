package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/ready"
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

// mergesOf is a mergeSource that answers from a table: the merge fact of each
// ticket it holds, and of any other the zero one (it has not published).
type mergesOf map[string]ready.Merge

func (m mergesOf) Of(ticket string) (ready.Merge, error) { return m[ticket], nil }

// TestClaimProblemsOfAMergedBlocker: a merged blocker meets the claim once its
// work has merged (its recorded pull request is merged, or with none recorded
// its branch is in the target); published is not enough. Each way it is not met
// is named, with what meets it, and a claim that reads no merge facts judges the
// blocker by its journal alone.
func TestClaimProblemsOfAMergedBlocker(t *testing.T) {
	t.Parallel()
	st := checkStore(t, "merged")
	approveBrief(t, st, "JIG-2")
	const pr = "https://github.com/o/r/pull/4"

	got, err := claimProblems(st, "JIG-2", claimDeps{merges: mergesOf{}})
	if err != nil || codes(got) != claimBlockedMerged || !strings.Contains(got[0].Message, "JIG-1, which has not published") {
		t.Fatalf("unpublished blocker: %+v, %v, want CLAIM_BLOCKED_MERGED saying JIG-1 has not published", got, err)
	}

	merge := func(r ready.RepoMerge) mergesOf {
		return mergesOf{"JIG-1": {Published: true, Repos: []ready.RepoMerge{r}}}
	}
	for _, tc := range []struct {
		name     string
		fact     ready.RepoMerge
		want     string
		message  string
		helpWith string
	}{
		{"its pull request is merged", ready.JudgeRepo(ready.Evidence{Ticket: "JIG-1", PR: pr, State: ready.PRMerged}), "", "", ""},
		{"its branch is in the target", ready.JudgeRepo(ready.Evidence{Ticket: "JIG-1", Ancestor: true}), "", "", ""},
		{"its pull request is open", ready.JudgeRepo(ready.Evidence{Ticket: "JIG-1", PR: pr, State: ready.PROpen}), claimBlockedMerged,
			"JIG-2 waits for JIG-1: its PR is open (" + pr + ")", "Merge " + pr},
		{"its pull request was closed", ready.JudgeRepo(ready.Evidence{Ticket: "JIG-1", PR: pr, State: ready.PRClosed}), claimBlockedMerged,
			"JIG-2 waits for JIG-1: its PR was closed without merging (" + pr + ")", "jig publish JIG-1 --pr <url>"},
		{"the host cannot be asked", ready.JudgeRepo(ready.Evidence{Ticket: "JIG-1", PR: pr, Offline: true}), claimBlockedMerged,
			"JIG-2 waits for JIG-1: its PR could not be read from the host (" + pr + ")", "gh auth status"},
		{"nothing is on record", ready.JudgeRepo(ready.Evidence{Ticket: "JIG-1"}), claimBlockedMerged,
			"JIG-2 waits for JIG-1: no merge on record: record its PR with `jig publish JIG-1 --pr <url>`", "record it with `jig publish JIG-1 --pr <url>`"},
	} {
		got, err := claimProblems(st, "JIG-2", claimDeps{merges: merge(tc.fact)})
		if err != nil || codes(got) != tc.want {
			t.Errorf("%s: %+v, %v, want %q", tc.name, got, err, tc.want)
			continue
		}
		if tc.want != "" && (got[0].Message != tc.message || !strings.Contains(got[0].Help, tc.helpWith)) {
			t.Errorf("%s: message %q, help %q, want message %q and help naming %q", tc.name, got[0].Message, got[0].Help, tc.message, tc.helpWith)
		}
	}

	// With no merge facts to read, publishing is all the journal says.
	if got, _ := claimProblems(st, "JIG-2", claimDeps{}); codes(got) != claimBlockedMerged {
		t.Errorf("an unpublished blocker with no facts to read: %+v, want it blocked", got)
	}
	if err := journal.Append(st, "JIG-1", journal.Line{Event: journal.EventPublishDone}); err != nil {
		t.Fatal(err)
	}
	if got, _ := claimProblems(st, "JIG-2", claimDeps{}); got != nil {
		t.Errorf("with no merge facts to read, a published blocker still blocks: %+v", got)
	}
}

// TestClaimProblemsOfAMergedBlockerInSeveralRepos: a blocker that built in two
// repos is met only when each is, and what is missing names its repo.
func TestClaimProblemsOfAMergedBlockerInSeveralRepos(t *testing.T) {
	t.Parallel()
	st := checkStore(t, "merged")
	approveBrief(t, st, "JIG-2")
	const (
		apiPR = "https://github.com/o/api/pull/1"
		webPR = "https://github.com/o/web/pull/2"
	)
	merged := func(repo, pr string) ready.RepoMerge {
		return ready.JudgeRepo(ready.Evidence{Ticket: "JIG-1", Repo: repo, PR: pr, State: ready.PRMerged})
	}
	merges := mergesOf{"JIG-1": {Published: true, Repos: []ready.RepoMerge{
		merged("api", apiPR),
		ready.JudgeRepo(ready.Evidence{Ticket: "JIG-1", Repo: "web", PR: webPR, State: ready.PROpen}),
	}}}
	got, err := claimProblems(st, "JIG-2", claimDeps{merges: merges})
	if err != nil || codes(got) != claimBlockedMerged {
		t.Fatalf("one repo merged, one open: %+v, %v, want CLAIM_BLOCKED_MERGED", got, err)
	}
	if want := "JIG-2 waits for JIG-1: web: its PR is open (" + webPR + ")"; got[0].Message != want {
		t.Errorf("message = %q, want %q (only the repo that is missing)", got[0].Message, want)
	}
	if !strings.Contains(got[0].Help, "web: Merge "+webPR) || strings.Contains(got[0].Help, "api") {
		t.Errorf("help = %q, want it to name the web repo's pull request only", got[0].Help)
	}
	merges["JIG-1"] = ready.Merge{Published: true, Repos: []ready.RepoMerge{merged("api", apiPR), merged("web", webPR)}}
	if got, err := claimProblems(st, "JIG-2", claimDeps{merges: merges}); err != nil || got != nil {
		t.Errorf("both repos merged: %+v, %v, want none", got, err)
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
