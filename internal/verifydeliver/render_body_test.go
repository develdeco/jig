package verifydeliver

import (
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/store"
)

func TestRenderWhatSectionKeepsItsOwnSections(t *testing.T) {
	t.Parallel()

	got := renderWhatSection("\n- why\n- what\n\n")
	if want := "## What\n\n- why\n- what\n\n"; got != want {
		t.Fatalf("renderWhatSection = %q, want %q", got, want)
	}
	got = renderWhatSection("## Heading\n\n- one\n")
	if strings.Contains(got, "\n## Heading") || !strings.Contains(got, "Heading") {
		t.Fatalf("renderWhatSection = %q, want the heading kept below the section level", got)
	}
}

// TestRenderVerificationSectionIsTheGates: the oracles green at the reviewed
// head and one line per gate round with its scope and verdict, and nothing of
// the findings, the tier or a path in the store.
func TestRenderVerificationSectionIsTheGates(t *testing.T) {
	t.Parallel()

	rounds := []RoundRecord{
		{Round: 1, Verdict: RoundFindings, Scope: "full"},
		{Round: 2, Verdict: RoundClean, Scope: "delta"},
		{Round: 3, Verdict: RoundClean},
	}
	got := renderVerificationSection(reportYAML{ReviewedSHA: map[string]string{"fixture-repo": "0123456789abcdef"}}, []string{"go-test", "go-vet", "lint"}, map[string]string{"go-test": "go test ./...", "go-vet": "go vet\n  ./..."}, rounds)
	want := "## Verification\n\n" +
		"Oracles green at the reviewed head 0123456:\n\n- go-test: `go test ./...`\n- go-vet: `go vet ./...`\n- lint\n\n" +
		"Gate rounds:\n\n- Round 1 (full): findings\n- Round 2 (delta): clean\n- Round 3: clean\n"
	if got != want {
		t.Fatalf("renderVerificationSection =\n%s\nwant\n%s", got, want)
	}
	for _, banned := range []string{"Findings:", "Revalidation", "first comment", "pr/"} {
		if strings.Contains(got, banned) {
			t.Errorf("renderVerificationSection = %q, want no %q", got, banned)
		}
	}

	// A ticket reviewed clean in one round, the common case, still names it.
	one := renderVerificationSection(reportYAML{}, []string{"test"}, nil, []RoundRecord{{Round: 1, Verdict: RoundClean, Scope: "full"}})
	if want := "Gate rounds:\n\n- Round 1 (full): clean\n"; !strings.Contains(one, want) {
		t.Errorf("renderVerificationSection with one round = %q, want it to hold %q", one, want)
	}
}

func TestRenderVerificationSectionWithoutAReviewedHeadOrRounds(t *testing.T) {
	t.Parallel()

	if got, want := renderVerificationSection(reportYAML{}, []string{"test"}, nil, nil), "## Verification\n\nOracles green:\n\n- test\n"; got != want {
		t.Fatalf("renderVerificationSection = %q, want %q", got, want)
	}
	two := reportYAML{ReviewedSHA: map[string]string{"web": "bbbbbbbbb", "api": "aaaaaaaaa"}}
	if got, want := renderVerificationSection(two, []string{"test"}, nil, nil), "Oracles green at the reviewed heads api aaaaaaa, web bbbbbbb:\n"; !strings.Contains(got, want) {
		t.Fatalf("renderVerificationSection = %q, want it to hold %q", got, want)
	}
	if got, want := renderVerificationSection(reportYAML{}, nil, nil, nil), "## Verification\n"; got != want {
		t.Fatalf("renderVerificationSection with nothing = %q, want %q", got, want)
	}
}

// TestRenderSlicesSection: each slice with its commit, a fix slice with the
// titles of the findings it fixed, an adopted ticket's own commits first.
func TestRenderSlicesSection(t *testing.T) {
	t.Parallel()

	slices := []store.Slice{
		{ID: "a", Goal: "Do the first thing:\nwith more words"},
		{ID: "b", Goal: "No commit yet"},
		{ID: "fix-1", Goal: "Fix these gate findings:\nlong finding text", FromGate: 1, Findings: []string{"r1-f1", "r1-f2", "r9-f9"}},
		{ID: "x", Goal: "Not a fix", Findings: []string{"r1-f1"}},
	}
	commits := map[string]string{"a": "aaaaaaaaaa", "fix-1": "ffffffffff", "x": "1111111111"}
	outcomes := []findingOutcome{
		{finding: Finding{ID: "r1-f1", Title: "First title"}},
		{finding: Finding{ID: "r1-f2", Title: "Second title"}},
	}
	got := renderSlicesSection(slices, commits, []authorCommit{{SHA: "cccccccccc", Subject: "author work"}}, outcomes)
	want := "## Slices\n\n" +
		"- author work (ccccccc)\n" +
		"- a (aaaaaaa): Do the first thing\n" +
		"- b: No commit yet\n" +
		"- fix-1 (fffffff): Fix these gate findings\n  - First title\n  - Second title\n" +
		"- x (1111111): Not a fix\n\n"
	if got != want {
		t.Fatalf("renderSlicesSection =\n%s\nwant\n%s", got, want)
	}
	if got := renderSlicesSection(nil, nil, nil, nil); got != "" {
		t.Fatalf("renderSlicesSection with nothing = %q, want none", got)
	}
}
