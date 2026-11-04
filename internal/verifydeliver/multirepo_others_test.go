package verifydeliver

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/store"
)

// TestOtherFindings: the findings of the other repos of a ticket, which each
// repo's review notes tell in one line. A bare path is the first repo's own
// and the other's line; a project of one repo, and a ticket in one repo of
// several, have none.
func TestOtherFindings(t *testing.T) {
	t.Parallel()
	finding := func(id, file string) findingOutcome { return findingOutcome{finding: Finding{ID: id, File: file}} }
	outcomes := []findingOutcome{finding("r1-f1", "api/server/handler.go"), finding("r1-f2", "web/ui/page.go"), finding("r1-f3", "stray.go")}
	ids := func(os []findingOutcome) []string {
		var out []string
		for _, o := range os {
			out = append(out, o.finding.ID)
		}
		return out
	}

	api := &publishTarget{repoName: "api", covered: []string{"api", "web"}, qualified: true}
	web := &publishTarget{repoName: "web", covered: []string{"api", "web"}, qualified: true}
	if got := ids(api.otherFindings(outcomes)); !slices.Equal(got, []string{"r1-f2"}) {
		t.Errorf("the api's other findings %v, want r1-f2 alone (the bare path is its own)", got)
	}
	if got := ids(web.otherFindings(outcomes)); !slices.Equal(got, []string{"r1-f1", "r1-f3"}) {
		t.Errorf("the web's other findings %v, want r1-f1 and the bare path r1-f3", got)
	}
	if got := api.ownerOf("stray.go"); got != "api" {
		t.Errorf("ownerOf a bare path = %q, want the first repo, api", got)
	}
	if got := web.ownerOf("web/ui/page.go"); got != "web" {
		t.Errorf("ownerOf web/ui/page.go = %q, want web", got)
	}

	oneOfSeveral := &publishTarget{repoName: "web", covered: []string{"web"}, qualified: true}
	if got := oneOfSeveral.otherFindings(outcomes); len(got) != 0 {
		t.Errorf("a ticket in one repo of several has other findings %v, want none", ids(got))
	}
	alone := &publishTarget{repoName: "app", covered: []string{"app"}}
	if got := alone.otherFindings(outcomes); got != nil {
		t.Errorf("a project of one repo has other findings %v, want nil", ids(got))
	}
}

func readNotes(t *testing.T, st *store.Store, ticket, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(st.TicketDir(ticket), "pr", name))
	if err != nil {
		t.Fatalf("read %s: %v", name, err)
	}
	return string(data)
}

// TestWriteReviewNotesListsOtherRepos: the other repos' findings are one line
// each under "## Findings in other repos", in risk then id order, between the
// repo's own findings and the coverage; with no finding of its own the section
// follows the round summary.
func TestWriteReviewNotesListsOtherRepos(t *testing.T) {
	t.Parallel()
	st := &store.Store{Root: t.TempDir()}
	others := []otherFinding{
		{repo: "web", file: "ui/page.go", title: "The page shows cents, not dollars"},
		{repo: "api", file: "server/handler.go", title: "The handler drops the currency"},
	}
	own := []findingOutcome{{finding: Finding{ID: "r1-f3", Title: "Own", Risk: "high", RiskRationale: "why"}, detail: "fixed"}}
	if err := writeReviewNotes(st, "T-1", "review-notes-docs.md", 2, nil, "Round summary.", own, others, nil, []string{"a.go"}, nil); err != nil {
		t.Fatal(err)
	}
	got := readNotes(t, st, "T-1", "review-notes-docs.md")
	section := "## Findings in other repos\n\n- web: ui/page.go: The page shows cents, not dollars\n- api: server/handler.go: The handler drops the currency\n\n"
	if !strings.Contains(got, section) {
		t.Fatalf("notes lack the exact section:\n%s", got)
	}
	f, o, c := strings.Index(got, "## Findings\n"), strings.Index(got, "## Findings in other repos"), strings.Index(got, "## Coverage")
	if !(f >= 0 && f < o && o < c) {
		t.Errorf("sections out of order (Findings %d, other repos %d, Coverage %d):\n%s", f, o, c, got)
	}

	if err := writeReviewNotes(st, "T-1", "review-notes-none.md", 2, nil, "Round summary.", nil, others, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	want := "# Review Notes\n\n## Round 2\n\nRound summary.\n\n" + section + "## Coverage\n\n"
	if got := readNotes(t, st, "T-1", "review-notes-none.md"); got != want {
		t.Errorf("notes with no own finding =\n%q\nwant\n%q", got, want)
	}
}

// TestWriteReviewNotesWithoutOthersIsUnchanged: with no other findings the
// notes are the bytes they were before the section existed.
func TestWriteReviewNotesWithoutOthersIsUnchanged(t *testing.T) {
	t.Parallel()
	st := &store.Store{Root: t.TempDir()}
	if err := writeReviewNotes(st, "T-1", "review-notes.md", 1, nil, "", nil, nil, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	if got, want := readNotes(t, st, "T-1", "review-notes.md"), "# Review Notes\n\n## Coverage\n\n"; got != want {
		t.Errorf("notes without findings = %q, want %q", got, want)
	}

	own := []findingOutcome{
		{finding: Finding{ID: "r1-f2", Title: "Low one", Risk: "low", RiskRationale: "minor"}, detail: "fixed"},
		{finding: Finding{ID: "r1-f1", Title: "High one", Risk: "high", RiskRationale: "major", Oracle: "test"}, detail: "fixed by fix-1"},
	}
	if err := writeReviewNotes(st, "T-1", "review-notes.md", 2, nil, "All fixed.", own, nil, []string{"x.go"}, []string{"x.go", "y.go"}, nil); err != nil {
		t.Fatal(err)
	}
	want := "# Review Notes\n\n## Round 2\n\nAll fixed.\n\n" +
		"## Findings\n\n" +
		"- **High one** (high): major\n  - Status: fixed by fix-1\n  - Oracle: test\n\n" +
		"- **Low one** (low): minor\n  - Status: fixed\n\n" +
		"## Coverage\n\nTouched files:\n\n- x.go\n- y.go\n\nReviewed paths:\n\n- x.go\n\n"
	if got := readNotes(t, st, "T-1", "review-notes.md"); got != want {
		t.Errorf("notes with findings =\n%q\nwant\n%q", got, want)
	}
}

// TestWriteReviewNotesListsWhoReviewedEachRound: the notes open with every
// reviewer round, oldest first, its scope, the range it read and who reviewed
// it, so a builder's full round stays on record after a later delta round by
// someone else; a scripted round is left out, a reviewer jig could not
// identify is named so, a round over several repos gives no range, and the
// list stands without a summary.
func TestWriteReviewNotesListsWhoReviewedEachRound(t *testing.T) {
	t.Parallel()
	st := &store.Store{Root: t.TempDir()}
	sha := func(c byte) string { return strings.Repeat(string(c), 40) }
	rounds := []RoundRecord{
		{Round: 1, Scope: "full", Base: sha('a'), Head: sha('b'), Reviewer: Provenance{Session: "s-builder", How: ReviewerByEnv}, Independent: IndependentNo},
		{Round: 2},
		{Round: 3, Scope: "delta", Base: sha('b'), Head: sha('c'), Independent: IndependentUnknown},
		{Round: 4, Scope: "delta", Base: sha('c'), Head: "api:" + sha('d') + "+web:" + sha('e'), Reviewer: Provenance{Session: "s-rev", Agent: "a-1"}, Independent: IndependentYes},
	}
	if err := writeReviewNotes(st, "T-1", "review-notes.md", 4, rounds, "", nil, nil, nil, nil, nil); err != nil {
		t.Fatal(err)
	}
	want := "# Review Notes\n\n## Reviewers\n\n" +
		"- Round 1 (full, aaaaaaa..bbbbbbb): reviewed by session `s-builder`, one of the builders: not an independent review.\n" +
		"- Round 3 (delta, bbbbbbb..ccccccc): reviewed by a reviewer jig could not identify; jig could not tell whether the reviewer built part of the change.\n" +
		"- Round 4 (delta): reviewed by session `s-rev`, agent `a-1`, none of the builders.\n\n" +
		"## Coverage\n\n"
	if got := readNotes(t, st, "T-1", "review-notes.md"); got != want {
		t.Errorf("notes =\n%q\nwant\n%q", got, want)
	}
}
