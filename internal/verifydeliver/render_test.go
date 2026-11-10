package verifydeliver

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/store"
)

// TestDemoteHeadingsMovesADocsHeadingsBelowTheSectionLevel: the shape
// `jig gate --doc <a design doc>` records as the intent text - a "# " title
// over "## " sections of its own - moves down by one shift, the least that
// puts the shallowest heading at "### ", so the body keeps exactly its own
// three "## " sections and none of the doc's outranks them. The doc's own
// nesting survives: one shift for every heading, not a clamp per heading.
func TestDemoteHeadingsMovesADocsHeadingsBelowTheSectionLevel(t *testing.T) {
	t.Parallel()

	doc := "# Retry design\n\nWhy.\n\n## Approach\n\nBackoff.\n\n### Limits\n\nTen tries.\n"
	want := "### Retry design\n\nWhy.\n\n#### Approach\n\nBackoff.\n\n##### Limits\n\nTen tries.\n"
	if got := demoteHeadings(doc); got != want {
		t.Fatalf("demoteHeadings(doc with a title) =\n%q\nwant\n%q", got, want)
	}

	sections := "## Approach\n\nBackoff.\n\n## Limits\n\nTen tries.\n"
	wantSections := "### Approach\n\nBackoff.\n\n### Limits\n\nTen tries.\n"
	if got := demoteHeadings(sections); got != wantSections {
		t.Fatalf("demoteHeadings(doc of ## sections) =\n%q\nwant\n%q", got, wantSections)
	}
}

// TestDemoteHeadingsLeavesEverythingElseAlone: text with no heading at or
// above the section level is returned byte for byte - prose, a one-line
// intent, headings that are already deep enough, CRLF line endings, and the
// things that merely look like headings ("#hashtag", seven "#", a heading
// indented into a code block).
func TestDemoteHeadingsLeavesEverythingElseAlone(t *testing.T) {
	t.Parallel()

	for _, text := range []string{
		"Make the greeting casual and clamp percentages.",
		"### Already deep\r\n\r\nWhy.\r\n",
		"#hashtag, not a heading\n\n####### seven is text\n\n    # indented four, a code block\n",
		"",
	} {
		if got := demoteHeadings(text); got != text {
			t.Fatalf("demoteHeadings(%q) = %q, want it unchanged", text, got)
		}
	}
}

// TestDemoteHeadingsSkipsFencedCodeBlocks: a "## " line inside a fenced code
// block is a code sample's own text - rendered literally, never a section of
// the body - so it is neither demoted nor allowed to decide the shift for the
// headings around it. A fence of the other character inside an open block
// does not close it.
func TestDemoteHeadingsSkipsFencedCodeBlocks(t *testing.T) {
	t.Parallel()

	doc := "## Shape\n\n```sh\n# jig gate --doc design.md\n## not a heading\n~~~\n```\n\n## Risks\n\nNone.\n"
	want := "### Shape\n\n```sh\n# jig gate --doc design.md\n## not a heading\n~~~\n```\n\n### Risks\n\nNone.\n"
	if got := demoteHeadings(doc); got != want {
		t.Fatalf("demoteHeadings(doc with a fence) =\n%q\nwant\n%q", got, want)
	}

	fencedOnly := "Prose.\n\n```md\n# Example title\n```\n"
	if got := demoteHeadings(fencedOnly); got != fencedOnly {
		t.Fatalf("demoteHeadings(headings only inside a fence) = %q, want it unchanged", got)
	}

	// A fence line naming a language does not close the block it sits in
	// either, so the "## " lines after it are still the sample's own text and
	// come back as they went in.
	infoString := "## Shape\n\n```md\n## Intent\n\n```sh\ngo test ./...\n```\n\n## Risks\n\nNone.\n"
	wantInfoString := "### Shape\n\n```md\n## Intent\n\n```sh\ngo test ./...\n```\n\n### Risks\n\nNone.\n"
	if got := demoteHeadings(infoString); got != wantInfoString {
		t.Fatalf("demoteHeadings(doc with a fence naming a language) =\n%q\nwant\n%q", got, wantInfoString)
	}
}

// TestDemoteHeadingsStopsAtSixLevels: markdown has six heading levels, so a
// doc already using all of them cannot move two down whole. The shift is
// still applied, with the deepest headings collapsed at "######", rather than
// pushed to a literal "#######" that renders as text and stops being a
// heading at all.
func TestDemoteHeadingsStopsAtSixLevels(t *testing.T) {
	t.Parallel()

	doc := "# Title\n\n##### Deep\n\n###### Deeper\n"
	want := "### Title\n\n###### Deep\n\n###### Deeper\n"
	if got := demoteHeadings(doc); got != want {
		t.Fatalf("demoteHeadings(doc using all six levels) =\n%q\nwant\n%q", got, want)
	}
}

// TestCloseOpenFenceClosesWhatTheTextLeavesOpen: an intent text ending inside
// a fenced code block is closed before it is embedded in the body, where
// everything after it - ## What changed, ## Verification and the findings
// line - would otherwise render as the inside of that block. The closing
// fence matches the one that opened it in character and length, and nothing
// else about the text changes: a text that closes every block it opens comes
// back byte for byte.
func TestCloseOpenFenceClosesWhatTheTextLeavesOpen(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct{ name, text, want string }{
		{
			"unclosed fence",
			"Shape:\n\n```md\n## Intent",
			"Shape:\n\n```md\n## Intent\n```",
		},
		{
			"unclosed fence ending in a newline",
			"Shape:\n\n```md\n## Intent\n",
			"Shape:\n\n```md\n## Intent\n```",
		},
		{
			"a tilde fence is closed with tildes, at its own length",
			"~~~~\ncode",
			"~~~~\ncode\n~~~~",
		},
		{
			"a shorter fence inside the block does not close it",
			"````\n```\ncode",
			"````\n```\ncode\n````",
		},
		{
			"a fence of the other character does not close it",
			"```md\n~~~\ncode",
			"```md\n~~~\ncode\n```",
		},
		{
			"a fence carrying an info string does not close it",
			"Shape:\n\n```md\n## Intent\n```sh",
			"Shape:\n\n```md\n## Intent\n```sh\n```",
		},
		{
			"nor does a tilde fence carrying one",
			"~~~\ncode\n~~~ python",
			"~~~\ncode\n~~~ python\n~~~",
		},
		{"balanced", "Prose.\n\n```sh\ngo test ./...\n```\n", "Prose.\n\n```sh\ngo test ./...\n```\n"},
		{
			"spaces after the closing run still close",
			"Prose.\n\n```sh\ngo test ./...\n```  \n",
			"Prose.\n\n```sh\ngo test ./...\n```  \n",
		},
		{
			"a CRLF closing fence closes",
			"Prose.\r\n\r\n```sh\r\ngo test ./...\r\n```\r\n",
			"Prose.\r\n\r\n```sh\r\ngo test ./...\r\n```\r\n",
		},
		{"no fence at all", "Make the greeting casual.", "Make the greeting casual."},
		{"empty", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := closeOpenFence(tc.text); got != tc.want {
				t.Fatalf("closeOpenFence(%q) = %q, want %q", tc.text, got, tc.want)
			}
		})
	}
}

// newTestStore opens a bare store.Store rooted at a fresh t.TempDir(), with
// nothing but the project.yaml a store needs to open at all - render.go's
// own unit tests need a *store.Store to resolve ticket paths, never a full
// fixture.
func newTestStore(t *testing.T) *store.Store {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "project.yaml"), []byte("schema_version: 1\n"), 0o644); err != nil {
		t.Fatalf("write project.yaml: %v", err)
	}
	st, err := store.Open(dir)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	return st
}

// TestAdoptedAuthorCommits: every commit from the merge base with target up
// to startSHA, oldest first, with its subject and full sha.
func TestAdoptedAuthorCommits(t *testing.T) {
	t.Parallel()

	dir, _ := tinyRepo(t)
	run(t, dir, "checkout", "-b", "add-retry")
	first := writeAndCommit(t, dir, "a.txt", "one", "author: first commit")
	second := writeAndCommit(t, dir, "b.txt", "two", "author: second commit")
	run(t, dir, "push", "origin", "add-retry")

	got, err := adoptedAuthorCommits(dir, "main", second)
	if err != nil {
		t.Fatalf("adoptedAuthorCommits: %v", err)
	}
	if len(got) != 2 || got[0].SHA != first || got[1].SHA != second {
		t.Fatalf("adoptedAuthorCommits = %+v, want [%s %s] oldest first", got, first, second)
	}
	if got[0].Subject != "author: first commit" || got[1].Subject != "author: second commit" {
		t.Fatalf("adoptedAuthorCommits subjects = %q, %q, want the commits' own subjects", got[0].Subject, got[1].Subject)
	}
}

// TestAdoptedAuthorCommitsEmptyWhenBranchForksAtStartSHA: a branch adopted
// at exactly origin/target's own tip has no pre-adoption commit of its own.
func TestAdoptedAuthorCommitsEmptyWhenBranchForksAtStartSHA(t *testing.T) {
	t.Parallel()

	dir, _ := tinyRepo(t)
	head := run(t, dir, "rev-parse", "HEAD")

	got, err := adoptedAuthorCommits(dir, "main", head)
	if err != nil {
		t.Fatalf("adoptedAuthorCommits: %v", err)
	}
	if len(got) != 0 {
		t.Fatalf("adoptedAuthorCommits = %+v, want none: the branch forked exactly at the target", got)
	}
}

// writeFindingsYAMLAt writes ticketDir/gate/round-<round>/findings.yaml
// directly, for a collectFindingsWithOutcomes test that drives the function
// against hand-built rounds rather than a full gate run.
func writeFindingsYAMLAt(t *testing.T, ticketDir string, round int, fy findingsYAML) {
	t.Helper()
	dir := filepath.Join(ticketDir, "gate", fmt.Sprintf("round-%d", round))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	data, err := marshalFindingsYAML(fy.Scope, fy.ReviewedPaths, fy.Findings, fy.Cleared, fy.KeptFixed, fy.Summary, fy.Probes, fy.Skipped)
	if err != nil {
		t.Fatalf("marshalFindingsYAML: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "findings.yaml"), data, 0o644); err != nil {
		t.Fatalf("write findings.yaml: %v", err)
	}
}

// TestCollectFindingsWithOutcomes covers three rules in one pass: (1) the
// last round's own summary, not round 1's; (2) the union of every round's
// reviewed_paths; (3) a finding fixed in a later round credits the slice
// that recorded it (store.Slice.Findings) and names the round it cleared;
// (4) a human dismissal stays "dismissed by a human" even once
// ApplyRound's rule 2 re-reports it with its own Triage field reset to "",
// which is the bug a later round's record alone would otherwise
// misattribute to automation.
func TestCollectFindingsWithOutcomes(t *testing.T) {
	t.Parallel()

	ticketDir := t.TempDir()
	writeFindingsYAMLAt(t, ticketDir, 1, findingsYAML{
		ReviewedPaths: []string{"alpha.go"},
		Findings: []Finding{
			{ID: "r1-f1", File: "alpha.go", Risk: RiskLow, Status: StatusDismissed, Triage: TriageHuman},
			{ID: "r1-f2", File: "beta.go", Risk: RiskHigh, Status: StatusOpen},
		},
		Summary: "round 1 summary",
	})
	// Round 2 re-reports r1-f1 (ApplyRound's rule 2 resets Triage to "" on
	// a dismissed finding's repeat) and clears r1-f2, fixed by slice fix-1.
	writeFindingsYAMLAt(t, ticketDir, 2, findingsYAML{
		ReviewedPaths: []string{"beta.go"},
		Findings: []Finding{
			{ID: "r1-f1", File: "alpha.go", Risk: RiskLow, Status: StatusDismissed},
		},
		Cleared: []string{"r1-f2"},
		Summary: "round 2 summary",
	})
	slices := []store.Slice{{ID: "fix-1", FromGate: 1, Findings: []string{"r1-f2"}}}

	outcomes, reviewedPaths, lastSummary, err := collectFindingsWithOutcomes(ticketDir, nil, 2, slices)
	if err != nil {
		t.Fatalf("collectFindingsWithOutcomes: %v", err)
	}
	if lastSummary != "round 2 summary" {
		t.Fatalf("lastSummary = %q, want round 2's own summary", lastSummary)
	}
	if got := strings.Join(reviewedPaths, ","); got != "alpha.go,beta.go" {
		t.Fatalf("reviewedPaths = %q, want the union of every round's own", got)
	}

	byID := map[string]findingOutcome{}
	for _, o := range outcomes {
		byID[o.finding.ID] = o
	}
	if o := byID["r1-f1"]; o.status != "dismissed" || o.detail != "dismissed by a human" {
		t.Fatalf("r1-f1 outcome = %+v, want dismissed by a human even though round 2's own record reset Triage", o)
	}
	if o := byID["r1-f2"]; o.status != "fixed" || o.detail != "fixed by slice fix-1 and cleared at round 2" {
		t.Fatalf("r1-f2 outcome = %+v, want fixed by slice fix-1 and cleared at round 2", o)
	}
}

// TestCheckUnrewrittenReferences pins the exact-reference rule: a line is
// reported only when it carries one of mediaFiles' own ./<name> references,
// literally, never a bare substring match on "./demo-" over every line of
// the body - which would also catch an unrelated mention in prose, a quoted
// brief, or a fenced snippet - and the file name is what is reported, never
// the whole matching line.
func TestCheckUnrewrittenReferences(t *testing.T) {
	t.Parallel()

	mediaFiles := []DemoFile{{Name: "demo-1.png"}, {Name: "demo-2.mp4"}}

	cases := []struct {
		name string
		body string
		want []string
	}{
		{
			name: "both rewritten to uploaded URLs",
			body: "## Demo\n\n- https://github.example/user-attachments/assets/1: a\n- https://github.example/user-attachments/assets/2: b\n",
		},
		{
			name: "one left unrewritten",
			body: "## Demo\n\n- ./demo-1.png: a\n- https://github.example/user-attachments/assets/2: b\n",
			want: []string{"demo-1.png"},
		},
		{
			name: "a ./demo- mention elsewhere in the body is not a media reference",
			body: "## Intent\n\nThe brief said to show ./demo-1.gif as an example.\n\n## Demo\n\n- https://x/1: a\n- https://x/2: b\n",
		},
		{
			name: "the exact ./<name> reference in the Intent section is not the Demo bullet",
			body: "## Intent\n\nThe brief says to record ./demo-1.png for this.\n\n" +
				"## Demo\n\n- https://x/1: a\n",
		},
		{
			name: "the Intent section's own mention does not hide a real unrewritten Demo bullet",
			body: "## Intent\n\nThe brief says to record ./demo-1.png for this.\n\n" +
				"## Demo\n\n- ./demo-1.png: a\n",
			want: []string{"demo-1.png"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := checkUnrewrittenReferences(tc.body, mediaFiles)
			if fmt.Sprint(got) != fmt.Sprint(tc.want) {
				t.Errorf("checkUnrewrittenReferences = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestRewriteUnrewrittenReferences pins publish's own second edit: a name
// gh appended an upload URL for (a `[<name>](<url>)` line of its own) gets
// that URL moved to where its bare reference stands, with the appended line
// removed so the body names the file once, not twice; a name with no such
// line - gh's read-back carries no record of it at all - is left exactly as
// it was and reported back for the caller to warn about.
//
// Every case below carries a ## Verification section after ## Demo, the
// shape writePRBody always renders (Intent, What changed, Demo, then
// Verification): gh's own appended link always lands after every section a
// real body has, never inside ## Demo's own bounds, which is what lets the
// scoping below tell it apart from an unrelated link of the same name
// somewhere earlier in the body.
func TestRewriteUnrewrittenReferences(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name            string
		body            string
		unrewritten     []string
		wantBody        string
		wantChanged     bool
		wantUnrewritten []string
	}{
		{
			name: "a bare reference is patched from gh's own appended link",
			body: "## Demo\n\n- ./demo-1.mp4: a video\n\n## Verification\n\nok\n\n" +
				"[demo-1.mp4](https://x/assets/1)\n",
			unrewritten: []string{"demo-1.mp4"},
			wantBody:    "## Demo\n\n- https://x/assets/1: a video\n\n## Verification\n\nok\n",
			wantChanged: true,
		},
		{
			name: "only the named file's own appended link is consumed",
			body: "## Demo\n\n- ./demo-1.mp4: a video\n- ./demo-2.mp4: another\n\n" +
				"## Verification\n\nok\n\n" +
				"[demo-1.mp4](https://x/assets/1)\n[demo-2.mp4](https://x/assets/2)\n",
			unrewritten: []string{"demo-1.mp4"},
			wantBody: "## Demo\n\n- https://x/assets/1: a video\n- ./demo-2.mp4: another\n\n" +
				"## Verification\n\nok\n\n" +
				"[demo-2.mp4](https://x/assets/2)\n",
			wantChanged: true,
		},
		{
			name:            "no appended link at all leaves the reference untouched",
			body:            "## Demo\n\n- ./demo-1.png: still here, unrewritten\n",
			unrewritten:     []string{"demo-1.png"},
			wantBody:        "## Demo\n\n- ./demo-1.png: still here, unrewritten\n",
			wantUnrewritten: []string{"demo-1.png"},
		},
		{
			name: "an Intent sentence naming the same file is left exactly as written",
			body: "## Intent\n\nThe brief says to record ./demo-1.mp4 for this.\n\n" +
				"## Demo\n\n- ./demo-1.mp4: a video\n\n## Verification\n\nok\n\n" +
				"[demo-1.mp4](https://x/assets/9)\n",
			unrewritten: []string{"demo-1.mp4"},
			wantBody: "## Intent\n\nThe brief says to record ./demo-1.mp4 for this.\n\n" +
				"## Demo\n\n- https://x/assets/9: a video\n\n## Verification\n\nok\n",
			wantChanged: true,
		},
		{
			// r2-f5: the Intent section carries its own, unrelated link using
			// the same file name as its link text - the exact shape gh's own
			// appended link takes, and the one an unscoped read-back could
			// mistake for it (the doc's stale URL, not the uploaded asset).
			name: "an Intent section's own link to the same name is not gh's appended link",
			body: "## Intent\n\nThe doc links the clip as\n\n[demo-1.mp4](https://docs.example/old-clip)\n\n" +
				"and explains it.\n\n## Demo\n\n- ./demo-1.mp4: a video\n\n## Verification\n\nok\n\n" +
				"[demo-1.mp4](https://x/assets/9)\n",
			unrewritten: []string{"demo-1.mp4"},
			wantBody: "## Intent\n\nThe doc links the clip as\n\n[demo-1.mp4](https://docs.example/old-clip)\n\n" +
				"and explains it.\n\n## Demo\n\n- https://x/assets/9: a video\n\n## Verification\n\nok\n",
			wantChanged: true,
		},
		{
			// r2-f5: the demo summary itself mentions the file's relative path
			// in passing, ahead of the real bullet in the same ## Demo section;
			// only the bullet - "- ./<name>", writePRBody's own rendered form -
			// is the reference to rewrite.
			name: "a demo summary mentioning the file's own path does not hide the real bullet",
			body: "## Demo\n\nrecorded ./demo-1.mp4 for the clamp\n\n- ./demo-1.mp4: a video\n\n" +
				"## Verification\n\nok\n\n[demo-1.mp4](https://x/assets/9)\n",
			unrewritten: []string{"demo-1.mp4"},
			wantBody: "## Demo\n\nrecorded ./demo-1.mp4 for the clamp\n\n- https://x/assets/9: a video\n\n" +
				"## Verification\n\nok\n",
			wantChanged: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			patched, changed, stillUnrewritten := rewriteUnrewrittenReferences(tc.body, tc.unrewritten)
			if patched != tc.wantBody {
				t.Errorf("patched body = %q, want %q", patched, tc.wantBody)
			}
			if changed != tc.wantChanged {
				t.Errorf("changed = %v, want %v", changed, tc.wantChanged)
			}
			if fmt.Sprint(stillUnrewritten) != fmt.Sprint(tc.wantUnrewritten) {
				t.Errorf("stillUnrewritten = %v, want %v", stillUnrewritten, tc.wantUnrewritten)
			}
		})
	}
}

// TestRewriteUnrewrittenReferencesToleratesTheShapesGhPrViewActuallyReturns:
// `gh pr view --json body` commonly returns a body with no trailing newline
// at all, and one last edited in the web UI comes back with "\r\n" line
// endings throughout - both are shapes the fix for r2-f2 must patch exactly
// like the plain "\n", trailing-newline body the other cases above cover.
func TestRewriteUnrewrittenReferencesToleratesTheShapesGhPrViewActuallyReturns(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name     string
		body     string
		wantBody string
	}{
		{
			name: "no trailing newline on the appended link's own line",
			body: "## Demo\n\n- ./demo-1.mp4: a video\n\n## Verification\n\nok\n\n" +
				"[demo-1.mp4](https://x/assets/1)",
			wantBody: "## Demo\n\n- https://x/assets/1: a video\n\n## Verification\n\nok\n",
		},
		{
			name: "a CRLF body",
			body: "## Demo\r\n\r\n- ./demo-1.mp4: a video\r\n\r\n## Verification\r\n\r\nok\r\n\r\n" +
				"[demo-1.mp4](https://x/assets/1)\r\n",
			wantBody: "## Demo\r\n\r\n- https://x/assets/1: a video\r\n\r\n## Verification\r\n\r\nok\r\n",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			patched, changed, stillUnrewritten := rewriteUnrewrittenReferences(tc.body, []string{"demo-1.mp4"})
			if patched != tc.wantBody {
				t.Errorf("patched body = %q, want %q", patched, tc.wantBody)
			}
			if !changed {
				t.Error("changed = false, want true")
			}
			if len(stillUnrewritten) != 0 {
				t.Errorf("stillUnrewritten = %v, want none", stillUnrewritten)
			}
		})
	}
}
