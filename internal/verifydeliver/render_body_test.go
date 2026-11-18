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

// TestRenderVerificationSectionIsTheGates: what the reviewed head has for a
// final run and one line per gate round with its scope and verdict, and nothing of
// the findings, the tier or a path in the store.
func TestRenderVerificationSectionIsTheGates(t *testing.T) {
	t.Parallel()

	rounds := []RoundRecord{
		{Round: 1, Verdict: RoundFindings, Scope: "full", FixSlices: []string{"f1", "f2"}},
		{Round: 2, Verdict: RoundClean, Scope: "delta"},
		{Round: 3, Verdict: RoundClean},
		{Round: 4, Verdict: RoundFindings, FixSlices: []string{"f3"}},
		{Round: 5, Verdict: RoundFindings, Ask: 1},
		{Round: 6, Verdict: RoundFindings, Note: 2},
		{Round: 7, Verdict: RoundClean, CarriedFrom: 6},
		{Round: 8, Verdict: VerdictNotIndependent},
	}
	final := FinalRunReport{Repo: "fixture-repo", Head: "0123456789abcdef", Outcome: FinalRunPass, Seconds: 12, Command: "(go test ./a/...) && (go vet ./...)"}
	got := renderVerificationSection([]FinalRunReport{final}, rounds)
	want := "## Verification\n\n" +
		"Final run at the reviewed head 0123456: pass in 12 s: `(go test ./a/...) && (go vet ./...)`\n\n" +
		"Gate rounds:\n\n- Round 1 (full): 2 fix slices asked\n- Round 2 (delta): clean\n- Round 3: clean\n- Round 4: 1 fix slice asked\n- Round 5: asks for the person\n- Round 6: notes only\n- Round 7: carried over\n- Round 8: not-independent\n"
	if got != want {
		t.Fatalf("renderVerificationSection =\n%s\nwant\n%s", got, want)
	}
	for _, banned := range []string{"Findings:", "Revalidation", "first comment", "pr/", "Oracles green"} {
		if strings.Contains(got, banned) {
			t.Errorf("renderVerificationSection = %q, want no %q", got, banned)
		}
	}

	// A ticket reviewed clean in one round, the common case, still names it.
	one := renderVerificationSection(nil, []RoundRecord{{Round: 1, Verdict: RoundClean, Scope: "full"}})
	if want := "Gate rounds:\n\n- Round 1 (full): clean\n"; !strings.Contains(one, want) {
		t.Errorf("renderVerificationSection with one round = %q, want it to hold %q", one, want)
	}
}

func TestRenderVerificationSectionWithoutAReviewedHeadOrRounds(t *testing.T) {
	t.Parallel()

	none := FinalRunReport{Repo: "fixture-repo", Head: "0123456789abcdef", Outcome: FinalRunNone}
	if got, want := renderVerificationSection([]FinalRunReport{none}, nil), "## Verification\n\nNo final run at the reviewed head 0123456\n"; got != want {
		t.Fatalf("renderVerificationSection = %q, want %q", got, want)
	}
	failed := FinalRunReport{Repo: "fixture-repo", Head: "0123456789abcdef", Outcome: FinalRunFail, Seconds: 4, Command: "go test ./a/..."}
	if got, want := renderVerificationSection([]FinalRunReport{failed}, nil), "## Verification\n\nFinal run at the reviewed head 0123456 failed: `go test ./a/...`\n"; got != want {
		t.Fatalf("renderVerificationSection = %q, want %q", got, want)
	}
	two := []FinalRunReport{
		{Repo: "api", Head: "aaaaaaaaa", Outcome: FinalRunPass, Seconds: 3, Command: "go test ./api"},
		{Repo: "web", Head: "bbbbbbbbb", Outcome: FinalRunNone},
	}
	if got, want := renderVerificationSection(two, nil), "- api: Final run at the reviewed head aaaaaaa: pass in 3 s: `go test ./api`\n- web: No final run at the reviewed head bbbbbbb\n"; !strings.Contains(got, want) {
		t.Fatalf("renderVerificationSection = %q, want it to hold %q", got, want)
	}
	if got, want := renderVerificationSection(nil, nil), "## Verification\n"; got != want {
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

// TestRoundLineOfARoundHeldOpenByAnEarlierAsk: a findings round with no fix
// slice and no ask of its own, run while an earlier round's ask was still
// undecided, asks for the person; it is not "notes only".
func TestRoundLineOfARoundHeldOpenByAnEarlierAsk(t *testing.T) {
	t.Parallel()
	if got := roundLine(RoundRecord{Verdict: RoundFindings, HeldAsks: 1, Note: 1}); got != "asks for the person" {
		t.Errorf("roundLine = %q, want %q", got, "asks for the person")
	}
	if got := roundLine(RoundRecord{Verdict: RoundFindings, Note: 1}); got != "notes only" {
		t.Errorf("roundLine = %q, want %q", got, "notes only")
	}
}
