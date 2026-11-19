package verifydeliver

import (
	"testing"

	"github.com/develdeco/jig/internal/journal"
)

func TestPureRuleMergeRefIsTheRoundsRefOrTheOriginsTarget(t *testing.T) {
	t.Parallel()
	if got := mergeRefOf("", "main"); got != "origin/main" {
		t.Errorf("mergeRefOf(\"\", main) = %q, want origin/main", got)
	}
	if got := mergeRefOf("origin/parent-branch", "main"); got != "origin/parent-branch" {
		t.Errorf("mergeRefOf for a stacked parent = %q, want the parent's branch", got)
	}
}

func TestPureRuleCarriedNoteSaysWhoseVerdictItIsAndWhyItHolds(t *testing.T) {
	t.Parallel()
	same := RepoPrep{Repo: "api", Head: "h1", Line: lineSame}
	merged := RepoPrep{Repo: "web", Head: "h2", Line: lineClean}
	tips := map[string]string{"web": "t2"}
	refs := map[string]string{"web": "origin/main"}

	got := carriedNote(2, 3, ReviewPrep{Repos: []RepoPrep{merged}}, false, tips, refs)
	want := "Carried over from round 2: head h2 merges origin/main (t2) and its tree equals git's own merge; the oracle ran and passed."
	if got != want {
		t.Errorf("a carried merge:\n got %q\nwant %q", got, want)
	}

	got = carriedNote(1, 1, ReviewPrep{Repos: []RepoPrep{same}}, false, nil, nil)
	want = "Carried over from round 1: head h1 is unchanged since round 1; the oracle ran and passed."
	if got != want {
		t.Errorf("a head unchanged:\n got %q\nwant %q", got, want)
	}

	// A round over several repos names each repo, and a merge of a stacked
	// parent names the parent's ref.
	refs = map[string]string{"web": "origin/parent"}
	got = carriedNote(2, 3, ReviewPrep{Repos: []RepoPrep{same, merged}}, true, tips, refs)
	want = "Carried over from round 2: api: head h1 is unchanged since round 3; web: head h2 merges origin/parent (t2) and its tree equals git's own merge; the oracle ran and passed."
	if got != want {
		t.Errorf("a carried round over two repos:\n got %q\nwant %q", got, want)
	}
}

func TestPureRuleReviewerOfIsWhatTheReportRecords(t *testing.T) {
	t.Parallel()
	if got := (reportYAML{}).reviewerOf(); got != (Provenance{}) {
		t.Errorf("a report with no reviewer = %+v, want the zero value", got)
	}
	rev := Provenance{Session: "s-reviewer", How: ReviewerByFlag}
	if got := (reportYAML{Reviewer: &rev}).reviewerOf(); got != rev {
		t.Errorf("reviewerOf = %+v, want %+v", got, rev)
	}
}

func TestPureRuleJigBuiltIsWhatTheJournalRecordsOfTheBranch(t *testing.T) {
	t.Parallel()
	for _, c := range []struct {
		name  string
		lines []journal.Line
		want  bool
	}{
		{"an empty journal", nil, false},
		{"a verified commit", []journal.Line{{Event: "verified", Commit: "c1"}}, true},
		{"a green result that names a commit", []journal.Line{{Event: "result", Outcome: "green", Commit: "c1"}}, true},
		{"a green result with no commit", []journal.Line{{Event: "result", Outcome: "green"}}, false},
		{"a failed result", []journal.Line{{Event: "result", Outcome: "red", Commit: "c1"}}, false},
		{"a verified line with no commit", []journal.Line{{Event: "verified"}}, false},
		{"lines of other events", []journal.Line{{Event: journal.EventClaim, Commit: "c1"}}, false},
	} {
		// No slices: the slice states are read only when the journal says
		// nothing, and with none there is nothing to read.
		got, err := jigBuilt(nil, "T-1", nil, c.lines)
		if err != nil || got != c.want {
			t.Errorf("%s: jigBuilt = %v (%v), want %v", c.name, got, err, c.want)
		}
	}
}
