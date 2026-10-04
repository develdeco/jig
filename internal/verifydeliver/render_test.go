package verifydeliver

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/store"
)

// TestWriteMemorizeCommitsOnlyTheRetrievalNotes: the memorize commit holds the
// retrieval notes and nothing else. By the time publish writes it, the
// publish lease may have run every oracle (when the target moved), and an
// oracle can leave anything behind: a file git does not ignore, a rewrite of a
// tracked file, a change it staged. None of that belongs in a commit that is
// pushed to a branch its author built, so the lease is put back at its head
// before the notes are written, and the notes are the only change the commit
// makes. A second call, with the notes unchanged, makes no second commit.
func TestWriteMemorizeCommitsOnlyTheRetrievalNotes(t *testing.T) {
	t.Parallel()

	_, remote := tinyRepo(t)
	clone := cloneFrom(t, remote)
	for name, content := range map[string]string{
		"stray.txt":  "left behind by an oracle\n",
		"f.txt":      "rewritten by a formatter\n", // tracked since the repo's first commit
		"staged.txt": "staged by an oracle\n",
	} {
		if err := os.WriteFile(filepath.Join(clone, name), []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
	run(t, clone, "add", "staged.txt")
	before := run(t, clone, "rev-parse", "HEAD")

	if err := writeMemorize(clone, "T-1", nil, nil, nil, buildGitEnv); err != nil {
		t.Fatalf("writeMemorize: %v", err)
	}

	if got := run(t, clone, "rev-parse", "HEAD~1"); got != before {
		t.Fatalf("the memorize commit's parent is %s, want the head it was made on, %s", got, before)
	}
	if got := run(t, clone, "log", "-1", "--format=%s"); got != "docs: memorize T-1" {
		t.Errorf("the commit's subject is %q, want %q", got, "docs: memorize T-1")
	}
	if got := run(t, clone, "show", "--name-only", "--format=", "HEAD"); got != ".claude/retrieval/T-1.md" {
		t.Errorf("the memorize commit changes %q, want only the retrieval notes", got)
	}
	if status := run(t, clone, "status", "--porcelain"); status != "" {
		t.Errorf("git status after the memorize commit:\n%s\nwant a lease with nothing left over from the oracles", status)
	}

	head := run(t, clone, "rev-parse", "HEAD")
	if err := writeMemorize(clone, "T-1", nil, nil, nil, buildGitEnv); err != nil {
		t.Fatalf("writeMemorize again: %v", err)
	}
	if got := run(t, clone, "rev-parse", "HEAD"); got != head {
		t.Errorf("writing unchanged notes made a commit: head %s, was %s", got, head)
	}
}

// TestFirstBriefSectionHandlesALeadingHeadingAndCRLF: a brief that opens
// directly with "## " on its very first line (no "# " title above it) must
// not have that heading mistaken for the section it introduces - the bug a
// hand-rolled "skip line 0" rule reintroduces. CRLF line endings are
// normalized first, the same as store.BriefSectionHashes, so a trailing \r
// never leaks into the section body.
func TestFirstBriefSectionHandlesALeadingHeadingAndCRLF(t *testing.T) {
	t.Parallel()

	brief := "## Intent\r\n\r\nWhy this matters.\r\n\r\n## Other\r\n\r\nUnrelated.\r\n"
	if got, want := firstBriefSection(brief), "Why this matters."; got != want {
		t.Fatalf("firstBriefSection(leading heading, CRLF) = %q, want %q", got, want)
	}
}

// TestFirstBriefSectionSkipsALeadingTitle: the ordinary shape, a "# " title
// line before the first "## " section, still resolves to that section's
// body and not the next one.
func TestFirstBriefSectionSkipsALeadingTitle(t *testing.T) {
	t.Parallel()

	brief := "# Title\n\n## Intent\n\nBody text.\n\n## Other\n\nmore\n"
	if got, want := firstBriefSection(brief), "Body text."; got != want {
		t.Fatalf("firstBriefSection(leading title) = %q, want %q", got, want)
	}
}

// TestFirstBriefSectionEmptyWithNoHeading: a brief with no "## " heading at
// all has no section to extract.
func TestFirstBriefSectionEmptyWithNoHeading(t *testing.T) {
	t.Parallel()

	if got := firstBriefSection("# Title\n\njust a title, no sections\n"); got != "" {
		t.Fatalf("firstBriefSection(no heading) = %q, want empty", got)
	}
}

// TestFirstBriefSectionSkipsFencedCodeBlocks: a brief whose first section
// quotes markdown - what every brief that shows the pull request body's own
// "## " sections does - keeps the whole fenced example. A "## " line inside a
// fence is a code sample's own text, never the next section, so the Intent is
// neither cut mid-example nor handed on with an opening fence and no closing
// one, which would render the rest of the body as the inside of a code block.
func TestFirstBriefSectionSkipsFencedCodeBlocks(t *testing.T) {
	t.Parallel()

	quoted := "The body has exactly three sections, in this order:\n\n" +
		"```md\n## Intent\n\n## What changed\n\n## Verification\n```\n\n" +
		"Nothing else, and no heading of the intent's own above them."
	brief := "# T-1\n\n## Intent\n\n" + quoted + "\n\n## Plan\n\nStep one.\n"
	if got := firstBriefSection(brief); got != quoted {
		t.Fatalf("firstBriefSection(brief quoting markdown) =\n%q\nwant\n%q", got, quoted)
	}

	// A fence that opens before the brief's first real heading does not make
	// the "## " line inside it that heading either.
	preamble := "# T-1\n\nShaped like this:\n\n```md\n## Intent\n```\n\n## Intent\n\nThe real section.\n"
	if got, want := firstBriefSection(preamble), "The real section."; got != want {
		t.Fatalf("firstBriefSection(fence before the first heading) = %q, want %q", got, want)
	}

	// A fence line carrying an info string is the open block's own content,
	// not its closing fence - markdown allows an info string only on an
	// opening fence - so the "## Plan" that stops the section is the one
	// after the bare fence that really closes it, and the example comes back
	// whole.
	infoString := "Shaped like this, checked like that:\n\n" +
		"```md\n## Intent\n\n```sh\ngo test ./...\n```"
	brief = "# T-1\n\n## Intent\n\n" + infoString + "\n\n## Plan\n\nStep one.\n"
	if got := firstBriefSection(brief); got != infoString {
		t.Fatalf("firstBriefSection(brief quoting a fence with an info string) =\n%q\nwant\n%q", got, infoString)
	}

	// A brief that never closes its own fence has no later "## " to stop at:
	// the section runs to EOF and comes back unbalanced, for closeOpenFence
	// to close where it is embedded.
	unclosed := "# T-1\n\n## Intent\n\n```md\n## What changed\n"
	if got, want := firstBriefSection(unclosed), "```md\n## What changed"; got != want {
		t.Fatalf("firstBriefSection(brief with an unclosed fence) = %q, want %q", got, want)
	}

	// The same brief with its closing fence naming a language closes nothing:
	// the section runs to EOF, "## Plan" and all, which is what GitHub
	// renders too - and closeOpenFence then closes the block it leaves open.
	unclosedInfo := "# T-1\n\n## Intent\n\n```md\nexample\n```sh\n\n## Plan\n\nStep one.\n"
	if got, want := firstBriefSection(unclosedInfo), "```md\nexample\n```sh\n\n## Plan\n\nStep one."; got != want {
		t.Fatalf("firstBriefSection(brief whose closing fence has an info string) =\n%q\nwant\n%q", got, want)
	}
}

// TestFirstBriefSectionEmptyForAnEmptyFirstSection: a brief whose first
// "## " section has nothing under it has no intent text to publish, the same
// as one with no heading at all. There is no fallback to the brief's own
// bytes here - the brief's later sections would land inside a body that has
// exactly three "## " headings of its own.
func TestFirstBriefSectionEmptyForAnEmptyFirstSection(t *testing.T) {
	t.Parallel()

	brief := "# T-1\n\n## Intent\n\n## Plan\n\nStep one.\n"
	if got := firstBriefSection(brief); got != "" {
		t.Fatalf("firstBriefSection(empty first section) = %q, want empty", got)
	}
}

// TestFirstBriefSectionEmptyForABriefWithNoSection: the owner's rule for a
// brief that states no "## " section - nothing upstream refuses one - is that
// it has no first section to publish, so no ## Intent section is published.
// A one-paragraph hand-written brief and a long one sectioned with "### "
// headings read the same way here: neither yields the brief's own text,
// which as a whole-brief fallback published the author's Out of scope, Tests
// and Decisions as the intent they are not.
func TestFirstBriefSectionEmptyForABriefWithNoSection(t *testing.T) {
	t.Parallel()

	paragraph := "# T-1: make the greeting casual\n\nThe greeting shouts at everyone.\nIt should not.\n"
	if got := firstBriefSection(paragraph); got != "" {
		t.Fatalf("firstBriefSection(one-paragraph brief) = %q, want empty", got)
	}

	subsections := "# T-1: make the greeting casual\n\nThe greeting shouts at everyone.\n\n" +
		"### Out of scope\n\nThe CLI's other commands.\n\n" +
		"### Decisions\n\nA note the author made to themselves.\n"
	if got := firstBriefSection(subsections); got != "" {
		t.Fatalf("firstBriefSection(brief sectioned with \"### \") = %q, want empty", got)
	}
}

// TestFirstBriefSectionEmptyWhenEveryHeadingIsFenced: a brief whose prose
// quotes markdown - the body's own "## " sections, say - has no section of
// its own either. The "## " lines inside its fence are a code sample's text,
// never sections it has, so there is nothing to publish as the ## Intent and
// the quoted example is not published in its place.
func TestFirstBriefSectionEmptyWhenEveryHeadingIsFenced(t *testing.T) {
	t.Parallel()

	brief := "# T-1\n\nThe published body must end up shaped like this:\n\n```md\n## Intent\n```\n"
	if got := firstBriefSection(brief); got != "" {
		t.Fatalf("firstBriefSection(brief whose only headings are fenced) = %q, want empty", got)
	}
}

// TestStripIntentFrontMatterRemovesTheBlock: the fallback for an explicit
// intent.md that fails to parse strips the "---\n...\n---\n" front matter
// block rather than publishing it: a hand-edited file's front matter can
// name an agent and session, which must never reach a pull request.
func TestStripIntentFrontMatterRemovesTheBlock(t *testing.T) {
	t.Parallel()

	data := []byte("---\nsource: explicit\nagent: claude\nsession: abc-123\n---\nThe actual intent text.\n")
	got := stripIntentFrontMatter(data)
	if want := "The actual intent text."; got != want {
		t.Fatalf("stripIntentFrontMatter = %q, want %q", got, want)
	}
	if strings.Contains(got, "source:") || strings.Contains(got, "agent:") || strings.Contains(got, "---") {
		t.Fatalf("stripIntentFrontMatter leaked front matter: %q", got)
	}
}

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

// TestRenderIntentSectionExplicitStripsFrontMatterWhenUnparseable: an
// intent.md a hand edit left unparseable (an unknown front-matter key,
// which store.ParseIntent's strict decode refuses) still renders an
// ## Intent section, with the front matter stripped rather than dumped
// raw.
func TestRenderIntentSectionExplicitStripsFrontMatterWhenUnparseable(t *testing.T) {
	t.Parallel()

	st := newTestStore(t)
	const ticket = "T-1"
	if err := os.MkdirAll(st.TicketDir(ticket), 0o755); err != nil {
		t.Fatal(err)
	}
	raw := "---\nsource: explicit\nbogus: field\n---\nThe hand-edited intent text.\n"
	if err := os.WriteFile(st.IntentPath(ticket), []byte(raw), 0o644); err != nil {
		t.Fatal(err)
	}

	rep := reportYAML{Intent: reportIntentYAML{Source: IntentSourceExplicit}}
	section, _, err := renderIntentSection(rep, st, ticket)
	if err != nil {
		t.Fatalf("renderIntentSection: %v", err)
	}
	if !strings.Contains(section, "The hand-edited intent text.") {
		t.Fatalf("section = %q, want the intent text", section)
	}
	if strings.Contains(section, "source:") || strings.Contains(section, "bogus:") || strings.Contains(section, "---") {
		t.Fatalf("section leaked front matter: %q", section)
	}
}

// TestRenderIntentSectionExplicitDemotesADocsHeadings: an explicit intent
// recorded from `jig gate --doc <path>` is a whole design doc, headings and
// all. Its text is rendered intact, but every heading in it is demoted below
// the section level, so the body still has exactly the three "## " headings
// it promises and no heading of the doc's outranks them.
func TestRenderIntentSectionExplicitDemotesADocsHeadings(t *testing.T) {
	t.Parallel()

	st := newTestStore(t)
	const ticket = "T-1"
	if err := os.MkdirAll(st.TicketDir(ticket), 0o755); err != nil {
		t.Fatal(err)
	}
	doc := "# Retry design\n\nCalls to the pricing API fail in bursts.\n\n## Approach\n\nExponential backoff, five tries.\n\n## Out of scope\n\nThe write path."
	if err := st.WriteIntent(ticket, store.Intent{Source: IntentSourceExplicit, Text: doc}); err != nil {
		t.Fatalf("WriteIntent: %v", err)
	}

	rep := reportYAML{Intent: reportIntentYAML{Source: IntentSourceExplicit}}
	section, _, err := renderIntentSection(rep, st, ticket)
	if err != nil {
		t.Fatalf("renderIntentSection: %v", err)
	}
	want := "## Intent\n\n### Retry design\n\nCalls to the pricing API fail in bursts.\n\n#### Approach\n\nExponential backoff, five tries.\n\n#### Out of scope\n\nThe write path.\n\n"
	if section != want {
		t.Fatalf("renderIntentSection(--doc intent) =\n%q\nwant\n%q", section, want)
	}
	var sectionHeadings int
	for _, line := range strings.Split(section, "\n") {
		if atxHeadingLevel(line) == 2 {
			sectionHeadings++
		}
	}
	if sectionHeadings != 1 {
		t.Fatalf("renderIntentSection(--doc intent) has %d \"## \" headings, want only its own:\n%s", sectionHeadings, section)
	}
}

// TestRenderIntentSectionClosesAnIntentTextsOpenFence: `jig gate --doc`
// records a whole file as the intent text, so a file that ends inside a
// fenced code block hands over an opening fence with no closing one. Embedded
// as it stands, that fence swallows the rest of the body: ## What changed,
// ## Verification and the findings count line all render as the inside of the
// code block. The section closes it, with the doc's own text otherwise
// intact, and the sections that follow are the body's own again.
func TestRenderIntentSectionClosesAnIntentTextsOpenFence(t *testing.T) {
	t.Parallel()

	st := newTestStore(t)
	const ticket = "T-1"
	if err := os.MkdirAll(st.TicketDir(ticket), 0o755); err != nil {
		t.Fatal(err)
	}
	doc := "# Retry design\n\nCheck it with:\n\n```sh\ngo test ./..."
	if err := st.WriteIntent(ticket, store.Intent{Source: IntentSourceExplicit, Text: doc}); err != nil {
		t.Fatalf("WriteIntent: %v", err)
	}

	rep := reportYAML{Intent: reportIntentYAML{Source: IntentSourceExplicit}}
	section, _, err := renderIntentSection(rep, st, ticket)
	if err != nil {
		t.Fatalf("renderIntentSection: %v", err)
	}
	want := "## Intent\n\n### Retry design\n\nCheck it with:\n\n```sh\ngo test ./...\n```\n\n"
	if section != want {
		t.Fatalf("renderIntentSection(--doc intent ending mid-fence) =\n%q\nwant\n%q", section, want)
	}
	var fences int
	for _, line := range strings.Split(section, "\n") {
		if _, run, _ := codeFenceRun(line); run > 0 {
			fences++
		}
	}
	if fences%2 != 0 {
		t.Fatalf("renderIntentSection(--doc intent ending mid-fence) embeds %d fences, want them balanced:\n%s", fences, section)
	}
}

// TestRenderIntentSectionNeverEmitsABareHeading: a source file that yields
// no intent text of its own publishes no ## Intent section at all, rather
// than a "## Intent" heading with nothing under it stating no intent. Both
// binding sources can reach that state: a brief whose first "## " section is
// empty, and an intent.md that is front matter and nothing else. Only the
// brief reports the omission for publish to warn about: an empty explicit
// intent is refused a round earlier (INTENT_EMPTY), so nothing reaches a
// published body that way without a hand edit publish refuses in turn.
func TestRenderIntentSectionNeverEmitsABareHeading(t *testing.T) {
	t.Parallel()

	st := newTestStore(t)
	const ticket = "T-1"
	if err := os.MkdirAll(st.TicketDir(ticket), 0o755); err != nil {
		t.Fatal(err)
	}
	brief := "# T-1\n\n## Intent\n\n## Plan\n\nStep one.\n"
	if err := os.WriteFile(filepath.Join(st.TicketDir(ticket), "brief.md"), []byte(brief), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(st.IntentPath(ticket), []byte("---\nsource: explicit\n---\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	for _, source := range []string{IntentSourceBrief, IntentSourceExplicit} {
		t.Run(source, func(t *testing.T) {
			rep := reportYAML{Intent: reportIntentYAML{Source: source}}
			section, omittedBriefIntent, err := renderIntentSection(rep, st, ticket)
			if err != nil {
				t.Fatalf("renderIntentSection: %v", err)
			}
			if section != "" {
				t.Fatalf("renderIntentSection(%s) = %q, want no section at all rather than a bare heading", source, section)
			}
			if want := source == IntentSourceBrief; omittedBriefIntent != want {
				t.Fatalf("renderIntentSection(%s) reported omittedBriefIntent = %v, want %v", source, omittedBriefIntent, want)
			}
		})
	}
}

// TestRenderIntentSectionOmittedForABriefWithNoSection: a brief with no
// "## " section - here a long one the author sectioned with "### " headings,
// which no rule upstream refuses - publishes no ## Intent section at all,
// the same as an inferred or absent intent. Never the whole brief: that put
// the author's Out of scope, Tests and Decisions in the body as the intent,
// against the brief's own rule for the section, and with their headings
// rendered as subsections of an Intent they are not part of. The omission is
// reported back for publish to warn about, so the body does not lose its one
// statement of why the change exists in silence.
func TestRenderIntentSectionOmittedForABriefWithNoSection(t *testing.T) {
	t.Parallel()

	st := newTestStore(t)
	const ticket = "T-1"
	if err := os.MkdirAll(st.TicketDir(ticket), 0o755); err != nil {
		t.Fatal(err)
	}
	brief := "# T-1: make the greeting casual\n\nThe greeting shouts at everyone.\n\n" +
		"### Out of scope\n\nThe CLI's other commands.\n\n" +
		"### Tests\n\ngo test ./...\n\n" +
		"### Decisions\n\nA note the author made to themselves.\n"
	if err := os.WriteFile(filepath.Join(st.TicketDir(ticket), "brief.md"), []byte(brief), 0o644); err != nil {
		t.Fatal(err)
	}

	rep := reportYAML{Intent: reportIntentYAML{Source: IntentSourceBrief}}
	section, omittedBriefIntent, err := renderIntentSection(rep, st, ticket)
	if err != nil {
		t.Fatalf("renderIntentSection: %v", err)
	}
	if section != "" {
		t.Fatalf("renderIntentSection(brief with no \"## \" section) = %q, want no section at all", section)
	}
	if !omittedBriefIntent {
		t.Fatalf("renderIntentSection(brief with no \"## \" section) reported omittedBriefIntent = false, want the omission reported for publish to warn about")
	}
}

// TestRenderIntentSectionOmittedForInferredAndNone pins the one rule in the
// ticket with a stated security promise behind it (.github/SECURITY.md,
// docs/adr/0012): an inferred intent - jig's own summary of the operator's
// private Claude Code session - and no intent at all both produce no
// ## Intent section whatsoever, not an empty one and not the underlying
// file's content. A future change that adds, say, a fallback for an empty
// Intent section must fail this test before it can publish a transcript
// summary to a pull request.
func TestRenderIntentSectionOmittedForInferredAndNone(t *testing.T) {
	t.Parallel()

	st := newTestStore(t)
	const ticket = "T-1"
	if err := os.MkdirAll(st.TicketDir(ticket), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := st.WriteIntent(ticket, store.Intent{
		Source: IntentSourceInferred,
		Text:   "a private summary of the operator's own Claude Code session",
	}); err != nil {
		t.Fatalf("WriteIntent: %v", err)
	}

	for _, source := range []string{IntentSourceInferred, IntentSourceNone} {
		t.Run(source, func(t *testing.T) {
			rep := reportYAML{Intent: reportIntentYAML{Source: source}}
			section, omittedBriefIntent, err := renderIntentSection(rep, st, ticket)
			if err != nil {
				t.Fatalf("renderIntentSection: %v", err)
			}
			if section != "" {
				t.Fatalf("renderIntentSection(%s) = %q, want no section at all", source, section)
			}
			if omittedBriefIntent {
				t.Fatalf("renderIntentSection(%s) reported omittedBriefIntent = true, want nothing for publish to warn about: no brief bound as the intent", source)
			}
		})
	}
}

// TestRenderWhatChangedSectionOmitsParensForMissingCommit: a green slice
// absent from the commits map (no journaled green result line) renders its
// goal alone, never "- <goal> ()": the same guard renderMemorize already
// applies to the same map.
func TestRenderWhatChangedSectionOmitsParensForMissingCommit(t *testing.T) {
	t.Parallel()

	slices := []store.Slice{{ID: "a", Goal: "Do the thing"}}
	got := renderWhatChangedSection(slices, map[string]string{}, nil)
	if strings.Contains(got, "()") {
		t.Fatalf("renderWhatChangedSection rendered empty parentheses: %q", got)
	}
	if !strings.Contains(got, "- Do the thing\n") {
		t.Fatalf("renderWhatChangedSection = %q, want the bare goal with no parens", got)
	}
}

// TestRenderWhatChangedSectionListsAuthorCommitsFirst: an adopted ticket's
// own pre-adoption commits come first, ahead of jig's own slices.
func TestRenderWhatChangedSectionListsAuthorCommitsFirst(t *testing.T) {
	t.Parallel()

	authorCommits := []authorCommit{{SHA: "abc1234567890", Subject: "author: fix the thing"}}
	slices := []store.Slice{{ID: "fix-1", Goal: "Fix the finding", FromGate: 1}}
	got := renderWhatChangedSection(slices, map[string]string{"fix-1": "deadbee000000"}, authorCommits)

	wantPrefix := "## What changed\n\n- author: fix the thing (abc1234)\n\n"
	if !strings.HasPrefix(got, wantPrefix) {
		t.Fatalf("renderWhatChangedSection = %q, want prefix %q", got, wantPrefix)
	}
	if !strings.Contains(got, "Fixes from review:\n\n- Fix the finding (deadbee)\n") {
		t.Fatalf("renderWhatChangedSection = %q, want the fix slice grouped under Fixes from review", got)
	}
}

// TestRenderWhatChangedSectionBulletsAFixSlicesFirstLineOnly: a fix slice's
// goal is the builder prompt buildFixSlices wrote - a header line, then
// every finding's file:line, title, detail and risk rationale - and the
// bullet is its first line and the short sha, nothing else. The whole goal
// would close the list item and render the gate's own finding text as a
// paragraph of its own, with the sha stranded at the end of it, and would
// put in the body the detail the brief moves to the review-notes comment.
// With the gate's fixes the only block the section has, "Fixes from
// review:" needs no separator above it either: the section opens on it.
func TestRenderWhatChangedSectionBulletsAFixSlicesFirstLineOnly(t *testing.T) {
	t.Parallel()

	const detail = "Add has a one-line comment but never says what happens when a+b overflows."
	goal := "Fix these gate findings:\n\nalpha/alpha.go:5 Add's doc comment omits overflow behavior\n" +
		detail + "\nRisk (high): silent overflow could misfeed a downstream calculation"
	slices := []store.Slice{{ID: "fix-1-alpha-test", Goal: goal, FromGate: 1}}
	got := renderWhatChangedSection(slices, map[string]string{"fix-1-alpha-test": "deadbee000000"}, nil)

	want := "## What changed\n\nFixes from review:\n\n- Fix these gate findings (deadbee)\n\n"
	if got != want {
		t.Fatalf("renderWhatChangedSection = %q, want %q", got, want)
	}
	if strings.Contains(got, detail) || strings.Contains(got, "Risk (high)") {
		t.Fatalf("renderWhatChangedSection = %q, want none of the gate's own finding text in the body", got)
	}
}

// TestRenderWhatChangedSectionOmitsTheAuthorCommitSeparatorWithNoBullets: on
// an adopted ticket whose only jig work is the gate's fixes, or none at all,
// the author's commits are the section's last bullets, and the blank line
// that separates them from jig's own is not written: the section ends in one
// blank line before ## Verification, never two.
func TestRenderWhatChangedSectionOmitsTheAuthorCommitSeparatorWithNoBullets(t *testing.T) {
	t.Parallel()

	authorCommits := []authorCommit{{SHA: "abc1234567890", Subject: "author: fix the thing"}}
	got := renderWhatChangedSection(nil, map[string]string{}, authorCommits)

	want := "## What changed\n\n- author: fix the thing (abc1234)\n\n"
	if got != want {
		t.Fatalf("renderWhatChangedSection = %q, want %q", got, want)
	}
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
	data, err := marshalFindingsYAML(fy.Scope, fy.ReviewedPaths, fy.Findings, fy.Cleared, fy.Summary)
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

	outcomes, reviewedPaths, lastSummary, err := collectFindingsWithOutcomes(ticketDir, 2, slices)
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

// TestRenderVerificationSectionNamesOraclesAndCountsFindings: "Oracles:"
// names the manifest's own oracle names (never a repo name), and the
// trailing line counts findings by how they ended, pointing at the pull
// request's first comment rather than a store path.
func TestRenderVerificationSectionNamesOraclesAndCountsFindings(t *testing.T) {
	t.Parallel()

	rep := reportYAML{TargetSHA: map[string]string{"fixture-repo": "deadbeef"}}
	outcomes := []findingOutcome{
		{status: "fixed"}, {status: "fixed"},
		{status: "dismissed"},
		{status: "noted"},
		{status: "asked"},
	}
	got := renderVerificationSection(rep, "oracles-only", []string{"go-test", "go-vet"}, outcomes)

	if !strings.Contains(got, "Oracles:\n\n- go-test\n- go-vet\n") {
		t.Fatalf("renderVerificationSection = %q, want the oracle names listed, not a repo name", got)
	}
	if strings.Contains(got, "fixture-repo") {
		t.Fatalf("renderVerificationSection = %q, want no repo name under Oracles", got)
	}
	want := "Findings: 2 fixed, 1 dismissed, 1 noted, 1 asked. See the pull request's first comment for detail.\n"
	if !strings.Contains(got, want) {
		t.Fatalf("renderVerificationSection = %q, want it to contain %q", got, want)
	}
	if strings.Contains(got, "pr/review-notes.md") || strings.Contains(got, "pr/evidence.md") {
		t.Fatalf("renderVerificationSection = %q, want no store path", got)
	}
}
