package verifydeliver

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/develdeco/jig/internal/pool"
	"github.com/develdeco/jig/internal/session"
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

// TestRenderDemoSectionReportsNoDemoWhenNoneRecorded: when the gate round
// has no demo recorded, renderDemoSection reports NoDemo and renders nothing.
func TestRenderDemoSectionReportsNoDemoWhenNoneRecorded(t *testing.T) {
	t.Parallel()

	st := newDemoStore(t)

	rep := reportYAML{ReviewedSHA: map[string]string{"repo": "abc1234567890"}}
	d := Deps{Store: st, Home: ""}

	result, err := renderDemoSection(d, st, "JIG-1", "repo", rep, 1)
	if err != nil {
		t.Fatalf("renderDemoSection: %v", err)
	}
	if !result.NoDemo {
		t.Errorf("renderDemoSection.NoDemo = false, want true")
	}
	if result.Section != "" {
		t.Errorf("renderDemoSection.Section = %q, want empty", result.Section)
	}
	if result.AllMediaFailed {
		t.Errorf("renderDemoSection.AllMediaFailed = true, want false")
	}
}

// TestRenderDemoSectionReportsNoDemoWhenHeadDoesNotMatch: when the demo
// was recorded for a different head, renderDemoSection reports NoDemo.
func TestRenderDemoSectionReportsNoDemoWhenHeadDoesNotMatch(t *testing.T) {
	t.Parallel()

	st := newDemoStore(t)
	mkRoundDir(t, st, "JIG-1", 1)

	// Create a demo.yaml for a different head
	demoRec := recordedDemoYAML{
		Status:  DemoRecorded,
		HeadSHA: "different1234567890",
		Summary: "Demo works",
		Media: []DemoFile{
			{Name: "demo-1.gif", SHA256: "abc123", Size: 1000, Caption: "It works"},
		},
	}
	writeDemoYAMLForTest(t, st, "JIG-1", 1, demoRec)

	rep := reportYAML{ReviewedSHA: map[string]string{"repo": "shipped1234567890"}}
	d := Deps{Store: st, Home: ""}

	result, err := renderDemoSection(d, st, "JIG-1", "repo", rep, 1)
	if err != nil {
		t.Fatalf("renderDemoSection: %v", err)
	}
	if !result.NoDemo {
		t.Errorf("renderDemoSection.NoDemo = false, want true (head mismatch)")
	}
}

// TestRenderDemoSectionErrorsWhenMediaDirCannotBeResolved: a shipped head
// with a demo.yaml genuinely recorded as recorded for it, but no jig home to
// resolve the evidence directory under, is not "no demo" - that would
// publish a body with ./<name> references to media jig cannot find and say
// nothing went wrong. It is an error, the same as any other unresolvable
// media directory (demoMediaDir), and it must fail loud rather than being
// swallowed into DemoRenderResult.NoDemo. This is what pins the fix: before
// it, renderDemoSection folded any demoMediaDir error, including this one,
// into NoDemo.
func TestRenderDemoSectionErrorsWhenMediaDirCannotBeResolved(t *testing.T) {
	t.Parallel()

	st := newDemoStore(t)
	mkRoundDir(t, st, "JIG-1", 1)

	shipHead := "abc1234567890"
	demoRec := recordedDemoYAML{
		Status:  DemoRecorded,
		HeadSHA: shipHead,
		Summary: "Demo",
		Media: []DemoFile{
			{Name: "demo-1.gif", SHA256: "abc123", Size: 1000, Caption: "caption"},
		},
	}
	writeDemoYAMLForTest(t, st, "JIG-1", 1, demoRec)

	rep := reportYAML{ReviewedSHA: map[string]string{"repo": shipHead}}
	d := Deps{Store: st, Home: ""}

	_, err := renderDemoSection(d, st, "JIG-1", "repo", rep, 1)
	if err == nil {
		t.Fatal("renderDemoSection: want an error when the media directory cannot be resolved for a recorded demo, got nil")
	}
}

// demoTestFile writes content into dir under name and returns the DemoFile
// entry demo.yaml would hold for it: its sha256 and size computed from the
// bytes actually written, the way renameDemoMedia records them, never
// hand-picked, so a test that wants a hash mismatch has to corrupt the file
// (or the record) after calling this rather than ever writing a wrong value
// by construction.
func demoTestFile(t *testing.T, dir, name, content, caption string) DemoFile {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	sum := sha256.Sum256([]byte(content))
	return DemoFile{Name: name, SHA256: hex.EncodeToString(sum[:]), Size: int64(len(content)), Caption: caption}
}

// renderDemoSectionDeps builds a Deps with a real jig home, so
// renderDemoSection's own demoMediaDir resolves to a real, writable
// directory, and returns it alongside that directory (computed the same way
// demoMediaDir does, so a test can place files exactly where renderDemoSection
// will look for them).
func renderDemoSectionDeps(t *testing.T, st *store.Store, ticket, head string) (Deps, string) {
	t.Helper()
	d := Deps{Store: st, Home: t.TempDir()}
	dir, err := demoMediaDir(d, ticket, head)
	if err != nil {
		t.Fatalf("demoMediaDir: %v", err)
	}
	return d, dir
}

// TestRenderDemoSectionRendersVerifiedFilesByTheirOwnNames: every verified
// file renders in demo.yaml's own media order, never renumbered from the
// verified slice's own index - the rule
// TestRenderDemoSectionRendersIndependentOfOmittedOrder below pins against a
// renumbering regression - and in the one reference form gh actually
// rewrites for its kind (gate finding r1-f3, DECISIONS.md): an image as
// markdown image syntax, a video as the bare path bullet gh has never
// reliably rewritten.
func TestRenderDemoSectionRendersVerifiedFilesByTheirOwnNames(t *testing.T) {
	t.Parallel()

	st := newDemoStore(t)
	mkRoundDir(t, st, "JIG-1", 1)
	shipHead := "abc1234567890"
	d, mediaDir := renderDemoSectionDeps(t, st, "JIG-1", shipHead)

	f1 := demoTestFile(t, mediaDir, "demo-1.png", "first file bytes", "the form")
	f2 := demoTestFile(t, mediaDir, "demo-2.mp4", "second file bytes, longer", "the flow")
	writeDemoYAMLForTest(t, st, "JIG-1", 1, recordedDemoYAML{
		Status: DemoRecorded, HeadSHA: shipHead, Summary: "it works", Media: []DemoFile{f1, f2},
	})

	rep := reportYAML{ReviewedSHA: map[string]string{"repo": shipHead}}
	result, err := renderDemoSection(d, st, "JIG-1", "repo", rep, 1)
	if err != nil {
		t.Fatalf("renderDemoSection: %v", err)
	}
	if result.NoDemo || result.DemoRefused || result.AllMediaFailed {
		t.Fatalf("result = %+v, want a rendered section", result)
	}
	if len(result.Omitted) != 0 {
		t.Errorf("Omitted = %v, want none", result.Omitted)
	}
	want := "## Demo\n\nit works\n\n- ![the form](./demo-1.png)\n- ./demo-2.mp4: the flow\n\n"
	if result.Section != want {
		t.Errorf("Section = %q, want %q", result.Section, want)
	}
	if len(result.MediaFiles) != 2 || result.MediaFiles[0].Name != "demo-1.png" || result.MediaFiles[1].Name != "demo-2.mp4" {
		t.Errorf("MediaFiles = %+v, want demo-1.png then demo-2.mp4", result.MediaFiles)
	}
	if result.MediaDir != mediaDir {
		t.Errorf("MediaDir = %q, want the evidence directory %q renderDemoSection verified against", result.MediaDir, mediaDir)
	}
}

// TestRenderDemoSectionOmitsAMissingFile: a file demo.yaml lists but that is
// no longer in the evidence directory is left out of the section and named
// in Omitted, and the file that is still there still renders - deleting this
// rule breaks nothing else, which is why it needs its own test.
func TestRenderDemoSectionOmitsAMissingFile(t *testing.T) {
	t.Parallel()

	st := newDemoStore(t)
	mkRoundDir(t, st, "JIG-1", 1)
	shipHead := "abc1234567890"
	d, mediaDir := renderDemoSectionDeps(t, st, "JIG-1", shipHead)

	present := demoTestFile(t, mediaDir, "demo-1.png", "still here", "kept")
	missing := DemoFile{Name: "demo-2.mp4", SHA256: strings.Repeat("a", 64), Size: 99, Caption: "gone"}
	writeDemoYAMLForTest(t, st, "JIG-1", 1, recordedDemoYAML{
		Status: DemoRecorded, HeadSHA: shipHead, Summary: "s", Media: []DemoFile{present, missing},
	})

	rep := reportYAML{ReviewedSHA: map[string]string{"repo": shipHead}}
	result, err := renderDemoSection(d, st, "JIG-1", "repo", rep, 1)
	if err != nil {
		t.Fatalf("renderDemoSection: %v", err)
	}
	if result.AllMediaFailed {
		t.Error("AllMediaFailed = true, want false: one file still verified")
	}
	if fmt.Sprint(result.Omitted) != "[demo-2.mp4]" {
		t.Errorf("Omitted = %v, want [demo-2.mp4]", result.Omitted)
	}
	if !strings.Contains(result.Section, "![kept](./demo-1.png)") {
		t.Errorf("Section = %q, want the surviving file rendered", result.Section)
	}
	if strings.Contains(result.Section, "demo-2.mp4") {
		t.Errorf("Section = %q, want the missing file left out", result.Section)
	}
}

// TestRenderDemoSectionOmitsAFileWithAChangedHash: a file present at the
// recorded size but whose bytes (and so sha256) no longer match the manifest
// is omitted, the same as a missing one - this is the check that catches a
// file silently replaced on disk since the gate round recorded it.
func TestRenderDemoSectionOmitsAFileWithAChangedHash(t *testing.T) {
	t.Parallel()

	st := newDemoStore(t)
	mkRoundDir(t, st, "JIG-1", 1)
	shipHead := "abc1234567890"
	d, mediaDir := renderDemoSectionDeps(t, st, "JIG-1", shipHead)

	original := demoTestFile(t, mediaDir, "demo-1.png", "original bytes", "caption")
	// Overwrite with different content of the exact same length, so only the
	// sha256 check (not the size check) can catch the change.
	replacement := "changed bytes!"
	if len(replacement) != len("original bytes") {
		t.Fatalf("test setup: replacement must be the same length as %q", "original bytes")
	}
	if err := os.WriteFile(filepath.Join(mediaDir, "demo-1.png"), []byte(replacement), 0o644); err != nil {
		t.Fatalf("overwrite demo-1.png: %v", err)
	}
	writeDemoYAMLForTest(t, st, "JIG-1", 1, recordedDemoYAML{
		Status: DemoRecorded, HeadSHA: shipHead, Summary: "s", Media: []DemoFile{original},
	})

	rep := reportYAML{ReviewedSHA: map[string]string{"repo": shipHead}}
	result, err := renderDemoSection(d, st, "JIG-1", "repo", rep, 1)
	if err != nil {
		t.Fatalf("renderDemoSection: %v", err)
	}
	if fmt.Sprint(result.Omitted) != "[demo-1.png]" {
		t.Errorf("Omitted = %v, want [demo-1.png] (sha256 mismatch)", result.Omitted)
	}
	if !result.AllMediaFailed {
		t.Error("AllMediaFailed = false, want true: the only listed file failed verification")
	}
	if result.Section != "" {
		t.Errorf("Section = %q, want empty", result.Section)
	}
}

// TestRenderDemoSectionReportsAllMediaFailedWhenEveryFileFails: when a
// recorded, non-empty demo has every one of its files fail verification,
// the result is AllMediaFailed, not NoDemo - the gate round did record a
// demo, and folding this into NoDemo would send an operator to blame the
// wrong half of the pipeline (DECISIONS.md).
func TestRenderDemoSectionReportsAllMediaFailedWhenEveryFileFails(t *testing.T) {
	t.Parallel()

	st := newDemoStore(t)
	mkRoundDir(t, st, "JIG-1", 1)
	shipHead := "abc1234567890"
	d, _ := renderDemoSectionDeps(t, st, "JIG-1", shipHead)

	missing := DemoFile{Name: "demo-1.png", SHA256: strings.Repeat("a", 64), Size: 10, Caption: "gone"}
	writeDemoYAMLForTest(t, st, "JIG-1", 1, recordedDemoYAML{
		Status: DemoRecorded, HeadSHA: shipHead, Summary: "s", Media: []DemoFile{missing},
	})

	rep := reportYAML{ReviewedSHA: map[string]string{"repo": shipHead}}
	result, err := renderDemoSection(d, st, "JIG-1", "repo", rep, 1)
	if err != nil {
		t.Fatalf("renderDemoSection: %v", err)
	}
	if result.NoDemo {
		t.Error("NoDemo = true, want false: a demo was recorded, it just all failed verification")
	}
	if !result.AllMediaFailed {
		t.Error("AllMediaFailed = false, want true")
	}
	if result.Section != "" {
		t.Errorf("Section = %q, want empty", result.Section)
	}
}

// TestRenderDemoSectionRendersNothingForAValidEmptyDemo: a demo recorded with
// no media at all (a valid result, ADR 0014: nothing to show, and the
// session said why in its summary) renders no section, but is neither
// AllMediaFailed nor NoDemo: nothing failed, and a demo was recorded.
func TestRenderDemoSectionRendersNothingForAValidEmptyDemo(t *testing.T) {
	t.Parallel()

	st := newDemoStore(t)
	mkRoundDir(t, st, "JIG-1", 1)
	shipHead := "abc1234567890"
	d, _ := renderDemoSectionDeps(t, st, "JIG-1", shipHead)

	writeDemoYAMLForTest(t, st, "JIG-1", 1, recordedDemoYAML{
		Status: DemoRecorded, HeadSHA: shipHead, Summary: "nothing to show", Media: nil,
	})

	rep := reportYAML{ReviewedSHA: map[string]string{"repo": shipHead}}
	result, err := renderDemoSection(d, st, "JIG-1", "repo", rep, 1)
	if err != nil {
		t.Fatalf("renderDemoSection: %v", err)
	}
	if result.NoDemo || result.AllMediaFailed || result.DemoRefused {
		t.Errorf("result = %+v, want none of NoDemo/AllMediaFailed/DemoRefused set", result)
	}
	if result.Section != "" {
		t.Errorf("Section = %q, want empty", result.Section)
	}
}

// TestRenderDemoSectionReportsARefusedDemo: a demo.yaml recorded as refused
// for the shipped head reports DemoRefused with its own reason, never folded
// into NoDemo - the brief's own rule that publish's output says which of the
// two it was.
func TestRenderDemoSectionReportsARefusedDemo(t *testing.T) {
	t.Parallel()

	st := newDemoStore(t)
	mkRoundDir(t, st, "JIG-1", 1)
	shipHead := "abc1234567890"

	if _, err := writeDemoRecord(st, "JIG-1", 1, shipHead, nil, "", &demoRefusal{Reason: "the demo session failed: TIMEOUT"}); err != nil {
		t.Fatalf("writeDemoRecord: %v", err)
	}

	rep := reportYAML{ReviewedSHA: map[string]string{"repo": shipHead}}
	d := Deps{Store: st, Home: ""}
	result, err := renderDemoSection(d, st, "JIG-1", "repo", rep, 1)
	if err != nil {
		t.Fatalf("renderDemoSection: %v", err)
	}
	if result.NoDemo {
		t.Error("NoDemo = true, want false: the demo was refused, not absent")
	}
	if !result.DemoRefused || result.RefusalReason != "the demo session failed: TIMEOUT" {
		t.Errorf("DemoRefused=%v RefusalReason=%q, want true and the recorded reason", result.DemoRefused, result.RefusalReason)
	}
	if result.Section != "" {
		t.Errorf("Section = %q, want empty", result.Section)
	}
}

// TestRenderDemoSectionFallsBackToAnEarlierRoundsRecordedDemo: ADR 0014's
// contract is one demo per reviewed head, so a later clean round on the same
// head (a routine re-gate) runs no demo and writes no demo.yaml of its own
// (gateDemo's DemoExisting). renderDemoSection must still find the earlier
// round's recording for the shipped head rather than reporting NoDemo, which
// is what reading only gate/round-<lastRound>/demo.yaml would do.
func TestRenderDemoSectionFallsBackToAnEarlierRoundsRecordedDemo(t *testing.T) {
	t.Parallel()

	st := newDemoStore(t)
	mkRoundDir(t, st, "JIG-1", 1)
	mkRoundDir(t, st, "JIG-1", 2)
	shipHead := "abc1234567890"
	d, mediaDir := renderDemoSectionDeps(t, st, "JIG-1", shipHead)

	f := demoTestFile(t, mediaDir, "demo-1.png", "bytes", "caption")
	// Round 1 recorded the demo; round 2 is a later clean round on the same
	// head, so it ran no demo at all (no demo.yaml written for round 2).
	writeDemoYAMLForTest(t, st, "JIG-1", 1, recordedDemoYAML{
		Status: DemoRecorded, HeadSHA: shipHead, Summary: "from round 1", Media: []DemoFile{f},
	})

	rep := reportYAML{ReviewedSHA: map[string]string{"repo": shipHead}}
	result, err := renderDemoSection(d, st, "JIG-1", "repo", rep, 2)
	if err != nil {
		t.Fatalf("renderDemoSection: %v", err)
	}
	if result.NoDemo {
		t.Fatal("NoDemo = true, want the round 1 recording picked up instead")
	}
	if !strings.Contains(result.Section, "from round 1") || !strings.Contains(result.Section, "![caption](./demo-1.png)") {
		t.Errorf("Section = %q, want round 1's summary and file rendered", result.Section)
	}
}

// TestRenderDemoSectionDoesNotFallBackPastAHeadChange: the fallback to an
// earlier round only ever picks up a demo recorded for the exact shipped
// head (recordedDemoRound's own head check); a round whose head changed
// since gets no demo of someone else's, and reports NoDemo like any other
// head with nothing recorded for it.
func TestRenderDemoSectionDoesNotFallBackPastAHeadChange(t *testing.T) {
	t.Parallel()

	st := newDemoStore(t)
	mkRoundDir(t, st, "JIG-1", 1)
	mkRoundDir(t, st, "JIG-1", 2)
	d := Deps{Store: st, Home: ""}

	writeDemoYAMLForTest(t, st, "JIG-1", 1, recordedDemoYAML{
		Status: DemoRecorded, HeadSHA: "old-head-1234567890", Summary: "old", Media: nil,
	})

	rep := reportYAML{ReviewedSHA: map[string]string{"repo": "new-head-1234567890"}}
	result, err := renderDemoSection(d, st, "JIG-1", "repo", rep, 2)
	if err != nil {
		t.Fatalf("renderDemoSection: %v", err)
	}
	if !result.NoDemo {
		t.Errorf("NoDemo = false, want true: round 1's demo was for a different head")
	}
}

// TestRenderDemoSectionLeavesOutASummaryNamingTheJigHome: the owner's
// decision on r1-f13 (DECISIONS.md) - a summary is the session's own words,
// recorded in demo.yaml as written and unbounded (ADR 0014), and jig does
// not filter it going in, but a published pull request body is public, so
// one that repeats the jig home back is left out of the rendered section
// whole, not merely edited, and reported in ScrubbedSummary. The file
// beside it, whose own caption names no such path, still renders.
func TestRenderDemoSectionLeavesOutASummaryNamingTheJigHome(t *testing.T) {
	t.Parallel()

	st := newDemoStore(t)
	mkRoundDir(t, st, "JIG-1", 1)
	shipHead := "abc1234567890"
	d, mediaDir := renderDemoSectionDeps(t, st, "JIG-1", shipHead)

	f := demoTestFile(t, mediaDir, "demo-1.png", "bytes", "fine caption")
	summary := "wrote the screenshot under " + d.Home + " and it worked"
	writeDemoYAMLForTest(t, st, "JIG-1", 1, recordedDemoYAML{
		Status: DemoRecorded, HeadSHA: shipHead, Summary: summary, Media: []DemoFile{f},
	})

	rep := reportYAML{ReviewedSHA: map[string]string{"repo": shipHead}}
	result, err := renderDemoSection(d, st, "JIG-1", "repo", rep, 1)
	if err != nil {
		t.Fatalf("renderDemoSection: %v", err)
	}
	if !result.ScrubbedSummary {
		t.Error("ScrubbedSummary = false, want true")
	}
	if strings.Contains(result.Section, d.Home) || strings.Contains(result.Section, "worked") {
		t.Errorf("Section = %q, want the whole summary left out, not just the path inside it", result.Section)
	}
	if !strings.Contains(result.Section, "- ![fine caption](./demo-1.png)") {
		t.Errorf("Section = %q, want the file's own caption still rendered", result.Section)
	}
}

// TestRenderDemoSectionLeavesOutACaptionNamingMediaDir: the same rule for a
// caption, the per-file half of the session's own words: one naming
// media_dir is left out and the file named in ScrubbedCaptions, but the
// file reference itself (./demo-1.png, which names no host path) still
// renders, with no caption.
func TestRenderDemoSectionLeavesOutACaptionNamingMediaDir(t *testing.T) {
	t.Parallel()

	st := newDemoStore(t)
	mkRoundDir(t, st, "JIG-1", 1)
	shipHead := "abc1234567890"
	d, mediaDir := renderDemoSectionDeps(t, st, "JIG-1", shipHead)

	f := demoTestFile(t, mediaDir, "demo-1.png", "bytes", "see "+mediaDir+" for the raw frame")
	writeDemoYAMLForTest(t, st, "JIG-1", 1, recordedDemoYAML{
		Status: DemoRecorded, HeadSHA: shipHead, Summary: "it works", Media: []DemoFile{f},
	})

	rep := reportYAML{ReviewedSHA: map[string]string{"repo": shipHead}}
	result, err := renderDemoSection(d, st, "JIG-1", "repo", rep, 1)
	if err != nil {
		t.Fatalf("renderDemoSection: %v", err)
	}
	if fmt.Sprint(result.ScrubbedCaptions) != "[demo-1.png]" {
		t.Errorf("ScrubbedCaptions = %v, want [demo-1.png]", result.ScrubbedCaptions)
	}
	if strings.Contains(result.Section, mediaDir) || strings.Contains(result.Section, "raw frame") {
		t.Errorf("Section = %q, still names media_dir or carries its caption", result.Section)
	}
	if !strings.Contains(result.Section, "- ![](./demo-1.png)\n") {
		t.Errorf("Section = %q, want the file still listed with no caption", result.Section)
	}
}

// TestRenderDemoSectionLeavesOutACaptionNamingTheGateLeaseAsAWSLMount: the
// gate lease (DemoInput.LeaseDir, where the demo session actually ran) is
// one of the directories a session may have been told about, and on
// Windows herdr hands it over spelled as a WSL mount
// (session.respellMentions), not jig's own spelling - so that spelling,
// not only the raw one, must be checked for too.
func TestRenderDemoSectionLeavesOutACaptionNamingTheGateLeaseAsAWSLMount(t *testing.T) {
	t.Parallel()

	st := newDemoStore(t)
	mkRoundDir(t, st, "JIG-1", 1)
	shipHead := "abc1234567890"
	d, mediaDir := renderDemoSectionDeps(t, st, "JIG-1", shipHead)

	leaseDir, err := pool.Dir(d.Home, "repo", "JIG-1", pool.Gate)
	if err != nil {
		t.Fatalf("pool.Dir: %v", err)
	}
	wsl := session.WSLPath(leaseDir)

	f := demoTestFile(t, mediaDir, "demo-1.png", "bytes", "ran it from "+wsl)
	writeDemoYAMLForTest(t, st, "JIG-1", 1, recordedDemoYAML{
		Status: DemoRecorded, HeadSHA: shipHead, Summary: "it works", Media: []DemoFile{f},
	})

	rep := reportYAML{ReviewedSHA: map[string]string{"repo": shipHead}}
	result, err := renderDemoSection(d, st, "JIG-1", "repo", rep, 1)
	if err != nil {
		t.Fatalf("renderDemoSection: %v", err)
	}
	if fmt.Sprint(result.ScrubbedCaptions) != "[demo-1.png]" {
		t.Errorf("ScrubbedCaptions = %v, want [demo-1.png] (the gate lease's WSL mount spelling)", result.ScrubbedCaptions)
	}
}

// TestRenderDemoSectionCapsAnOverlongCaption: a caption that names no host
// path is still a short label, not a paragraph, in a published pull
// request body, so it is truncated at demoCaptionRenderCap runes with a
// trailing "...". Capping is not scrubbing: the file is not reported in
// ScrubbedCaptions.
func TestRenderDemoSectionCapsAnOverlongCaption(t *testing.T) {
	t.Parallel()

	st := newDemoStore(t)
	mkRoundDir(t, st, "JIG-1", 1)
	shipHead := "abc1234567890"
	d, mediaDir := renderDemoSectionDeps(t, st, "JIG-1", shipHead)

	long := strings.Repeat("a", demoCaptionRenderCap+50)
	f := demoTestFile(t, mediaDir, "demo-1.png", "bytes", long)
	writeDemoYAMLForTest(t, st, "JIG-1", 1, recordedDemoYAML{
		Status: DemoRecorded, HeadSHA: shipHead, Summary: "it works", Media: []DemoFile{f},
	})

	rep := reportYAML{ReviewedSHA: map[string]string{"repo": shipHead}}
	result, err := renderDemoSection(d, st, "JIG-1", "repo", rep, 1)
	if err != nil {
		t.Fatalf("renderDemoSection: %v", err)
	}
	want := "- ![" + strings.Repeat("a", demoCaptionRenderCap) + "...](./demo-1.png)\n"
	if !strings.Contains(result.Section, want) {
		t.Errorf("Section = %q, want the caption capped at %d runes with \"...\"", result.Section, demoCaptionRenderCap)
	}
	if len(result.ScrubbedCaptions) != 0 {
		t.Errorf("ScrubbedCaptions = %v, want none: capping is not scrubbing", result.ScrubbedCaptions)
	}
}

// TestRenderDemoSectionNeverScrubsAPathJigDidNotHandOut: the owner's
// decision is to compare known strings only, never a pattern. A caption
// that merely looks like an absolute path, but names none of jig's own
// directories, is rendered exactly as the session wrote it.
func TestRenderDemoSectionNeverScrubsAPathJigDidNotHandOut(t *testing.T) {
	t.Parallel()

	st := newDemoStore(t)
	mkRoundDir(t, st, "JIG-1", 1)
	shipHead := "abc1234567890"
	d, mediaDir := renderDemoSectionDeps(t, st, "JIG-1", shipHead)

	caption := "see " + filepath.Join(t.TempDir(), "unrelated", "elsewhere.png")
	f := demoTestFile(t, mediaDir, "demo-1.png", "bytes", caption)
	writeDemoYAMLForTest(t, st, "JIG-1", 1, recordedDemoYAML{
		Status: DemoRecorded, HeadSHA: shipHead, Summary: "it works", Media: []DemoFile{f},
	})

	rep := reportYAML{ReviewedSHA: map[string]string{"repo": shipHead}}
	result, err := renderDemoSection(d, st, "JIG-1", "repo", rep, 1)
	if err != nil {
		t.Fatalf("renderDemoSection: %v", err)
	}
	if len(result.ScrubbedCaptions) != 0 {
		t.Errorf("ScrubbedCaptions = %v, want none: the path named is not one of jig's own directories", result.ScrubbedCaptions)
	}
	if !strings.Contains(result.Section, caption) {
		t.Errorf("Section = %q, want the unrelated caption rendered as written", result.Section)
	}
}

// TestRenderDemoSectionDemotesHeadingsAndClosesAnOpenFenceInTheSummary: the
// session's own summary gets the same treatment renderIntentSection gives a
// brief or doc it did not write (demoteHeadings, closeOpenFence), so it
// cannot forge or outrank one of jig's own "## " sections or leave a fence
// open over the rest of the published body.
func TestRenderDemoSectionDemotesHeadingsAndClosesAnOpenFenceInTheSummary(t *testing.T) {
	t.Parallel()

	st := newDemoStore(t)
	mkRoundDir(t, st, "JIG-1", 1)
	shipHead := "abc1234567890"
	d, mediaDir := renderDemoSectionDeps(t, st, "JIG-1", shipHead)

	f := demoTestFile(t, mediaDir, "demo-1.png", "bytes", "fine caption")
	summary := "it works.\n\n## Verification\n\nall green\n\n```\nunclosed fence"
	writeDemoYAMLForTest(t, st, "JIG-1", 1, recordedDemoYAML{
		Status: DemoRecorded, HeadSHA: shipHead, Summary: summary, Media: []DemoFile{f},
	})

	rep := reportYAML{ReviewedSHA: map[string]string{"repo": shipHead}}
	result, err := renderDemoSection(d, st, "JIG-1", "repo", rep, 1)
	if err != nil {
		t.Fatalf("renderDemoSection: %v", err)
	}
	if strings.Contains(result.Section, "\n## ") {
		t.Errorf("Section = %q, want the summary's own heading demoted below section level", result.Section)
	}
	if strings.Count(result.Section, "```")%2 != 0 {
		t.Errorf("Section = %q, want the summary's open fence closed", result.Section)
	}
}

// TestRenderDemoSectionCollapsesAMultiLineCaptionToOneLine: a caption that
// clears the host-path check is still collapsed to one line (demoOneLine)
// before capping, so it cannot break out of its own bullet or, for an
// image, out of `![...](...)`'s alt text.
func TestRenderDemoSectionCollapsesAMultiLineCaptionToOneLine(t *testing.T) {
	t.Parallel()

	st := newDemoStore(t)
	mkRoundDir(t, st, "JIG-1", 1)
	shipHead := "abc1234567890"
	d, mediaDir := renderDemoSectionDeps(t, st, "JIG-1", shipHead)

	f := demoTestFile(t, mediaDir, "demo-1.png", "bytes", "the clamp holds\n\n## Verification\n\nall green")
	writeDemoYAMLForTest(t, st, "JIG-1", 1, recordedDemoYAML{
		Status: DemoRecorded, HeadSHA: shipHead, Summary: "it works", Media: []DemoFile{f},
	})

	rep := reportYAML{ReviewedSHA: map[string]string{"repo": shipHead}}
	result, err := renderDemoSection(d, st, "JIG-1", "repo", rep, 1)
	if err != nil {
		t.Fatalf("renderDemoSection: %v", err)
	}
	if !strings.Contains(result.Section, "- ![the clamp holds ## Verification all green](./demo-1.png)\n") {
		t.Errorf("Section = %q, want the caption collapsed to one line", result.Section)
	}
}

// TestRenderDemoSectionCaptionCannotBreakOutOfTheImageAltText: r2-f6, a
// caption carrying its own "](" closes the alt text early and reads
// whatever follows as the image's own URL, which lets the session's own
// words point the rendered image at an address of their choosing.
// sanitizeDemoCaption drops '[', ']', '(' and ')' so the caption can never
// supply either half of that construct.
func TestRenderDemoSectionCaptionCannotBreakOutOfTheImageAltText(t *testing.T) {
	t.Parallel()

	st := newDemoStore(t)
	mkRoundDir(t, st, "JIG-1", 1)
	shipHead := "abc1234567890"
	d, mediaDir := renderDemoSectionDeps(t, st, "JIG-1", shipHead)

	caption := "x](https://evil.example/pixel.png) ![y"
	f := demoTestFile(t, mediaDir, "demo-1.png", "bytes", caption)
	writeDemoYAMLForTest(t, st, "JIG-1", 1, recordedDemoYAML{
		Status: DemoRecorded, HeadSHA: shipHead, Summary: "it works", Media: []DemoFile{f},
	})

	rep := reportYAML{ReviewedSHA: map[string]string{"repo": shipHead}}
	result, err := renderDemoSection(d, st, "JIG-1", "repo", rep, 1)
	if err != nil {
		t.Fatalf("renderDemoSection: %v", err)
	}
	want := "- ![xhttps://evil.example/pixel.png !y](./demo-1.png)\n"
	if !strings.Contains(result.Section, want) {
		t.Errorf("Section = %q, want %q", result.Section, want)
	}
	if strings.Contains(result.Section, "](https://evil.example") {
		t.Errorf("Section = %q, the caption's own URL must never close the image reference early", result.Section)
	}
}

// TestRenderDemoSectionCaptionWithAnUnbalancedBracketStillShowsTheImage:
// r2-f6, an unbalanced "[" in the caption would otherwise pair with the "]"
// jig itself writes to close the alt text, losing the image reference
// (and so the demo) entirely. sanitizeDemoCaption drops the bracket instead
// of letting it pair across the boundary jig did not intend.
func TestRenderDemoSectionCaptionWithAnUnbalancedBracketStillShowsTheImage(t *testing.T) {
	t.Parallel()

	st := newDemoStore(t)
	mkRoundDir(t, st, "JIG-1", 1)
	shipHead := "abc1234567890"
	d, mediaDir := renderDemoSectionDeps(t, st, "JIG-1", shipHead)

	f := demoTestFile(t, mediaDir, "demo-1.png", "bytes", "clamp holds at [10")
	writeDemoYAMLForTest(t, st, "JIG-1", 1, recordedDemoYAML{
		Status: DemoRecorded, HeadSHA: shipHead, Summary: "it works", Media: []DemoFile{f},
	})

	rep := reportYAML{ReviewedSHA: map[string]string{"repo": shipHead}}
	result, err := renderDemoSection(d, st, "JIG-1", "repo", rep, 1)
	if err != nil {
		t.Fatalf("renderDemoSection: %v", err)
	}
	want := "- ![clamp holds at 10](./demo-1.png)\n"
	if !strings.Contains(result.Section, want) {
		t.Errorf("Section = %q, want %q (the image reference still rendered)", result.Section, want)
	}
}

// TestRenderDemoSectionEscapesHTMLInSummaryAndCaption: gate finding r4-f3,
// the owner's decision. Neither the summary's demoteHeadings/closeOpenFence
// nor sanitizeDemoCaption's bracket strip touches raw HTML, which GitHub
// renders a sanitized subset of inside a pull request body - a video or
// captionless bullet's caption, and the summary outright, could otherwise
// put a live `<img>` or `<a>` into the published body. escapeDemoHTML renders
// '<', '>' and '&' as entities in both, so an `<img>` caption or summary
// reaches the body as literal text, never a tag.
func TestRenderDemoSectionEscapesHTMLInSummaryAndCaption(t *testing.T) {
	t.Parallel()

	st := newDemoStore(t)
	mkRoundDir(t, st, "JIG-1", 1)
	shipHead := "abc1234567890"
	d, mediaDir := renderDemoSectionDeps(t, st, "JIG-1", shipHead)

	caption := `<img src="https://evil.example/pixel.png">`
	f := demoTestFile(t, mediaDir, "demo-1.mp4", "bytes", caption)
	summary := `<img src="https://evil.example/pixel.png">`
	writeDemoYAMLForTest(t, st, "JIG-1", 1, recordedDemoYAML{
		Status: DemoRecorded, HeadSHA: shipHead, Summary: summary, Media: []DemoFile{f},
	})

	rep := reportYAML{ReviewedSHA: map[string]string{"repo": shipHead}}
	result, err := renderDemoSection(d, st, "JIG-1", "repo", rep, 1)
	if err != nil {
		t.Fatalf("renderDemoSection: %v", err)
	}
	if strings.Contains(result.Section, "<img") {
		t.Errorf("Section = %q, want no raw <img> tag anywhere", result.Section)
	}
	wantSummary := "&lt;img src=\"https://evil.example/pixel.png\"&gt;\n\n"
	if !strings.Contains(result.Section, wantSummary) {
		t.Errorf("Section = %q, want the summary escaped to %q", result.Section, wantSummary)
	}
	wantCaption := "- ./demo-1.mp4: &lt;img src=\"https://evil.example/pixel.png\"&gt;\n"
	if !strings.Contains(result.Section, wantCaption) {
		t.Errorf("Section = %q, want the caption escaped to %q", result.Section, wantCaption)
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
// somewhere earlier in the body (r2-f5, DECISIONS.md).
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

// Helper functions for demo tests

func writeDemoYAMLForTest(t *testing.T, st *store.Store, ticket string, round int, rec recordedDemoYAML) {
	t.Helper()
	path := filepath.Join(gateRoundDir(st, ticket, round), "demo.yaml")
	data, err := yaml.Marshal(rec)
	if err != nil {
		t.Fatalf("marshal demo.yaml: %v", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("write demo.yaml: %v", err)
	}
}
