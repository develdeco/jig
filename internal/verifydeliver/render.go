package verifydeliver

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/media"
	"github.com/develdeco/jig/internal/pool"
	"github.com/develdeco/jig/internal/store"
)

// lastGreenCommits maps each slice id to the commit sha of its last green
// result line.
func lastGreenCommits(lines []journal.Line) map[string]string {
	out := map[string]string{}
	for _, l := range lines {
		if l.Event == "result" && l.Outcome == "green" && l.Commit != "" {
			out[l.Slice] = l.Commit
		}
	}
	return out
}

// renderMemorize builds the ticket's retrieval-notes file: a heading, one
// section per slice with its goal and final commit sha, and any answered
// questions.
func renderMemorize(ticket string, slices []store.Slice, lines []journal.Line, questions []store.Question) string {
	commits := lastGreenCommits(lines)

	var b strings.Builder
	fmt.Fprintf(&b, "# %s - retrieval notes\n\n", ticket)
	for _, s := range slices {
		fmt.Fprintf(&b, "## %s\n\n", s.ID)
		fmt.Fprintf(&b, "- goal: %s\n", s.Goal)
		if sha, ok := commits[s.ID]; ok {
			fmt.Fprintf(&b, "- commit: %s\n", sha)
		}
		b.WriteString("\n")
	}

	if len(questions) > 0 {
		b.WriteString("## Questions\n\n")
		for _, q := range questions {
			fmt.Fprintf(&b, "- %s (%s): %s", q.ID, q.Slice, q.Body)
			if q.Answer != "" {
				fmt.Fprintf(&b, " -> %s", q.Answer)
			}
			b.WriteString("\n")
		}
	}

	return b.String()
}

// writeMemorize writes and commits the retrieval-notes file inside the
// publish lease, before the squash commit so the squash's tree contains
// it. The commit holds the notes and nothing else. The lease has run every
// oracle by now, when the target moved, and an oracle can leave anything
// behind - a file git does not ignore, a rewrite of a tracked file, a change
// it staged - none of which is the notes' to carry to a branch someone else
// may have built. The lease is disposable, so it is put back at its head
// first, as it is before every acquire; what git ignores stays ignored.
func writeMemorize(leaseDir, ticket string, slices []store.Slice, lines []journal.Line, questions []store.Question, identityEnv []string) error {
	if err := resetLeasePristine(leaseDir, "HEAD"); err != nil {
		return fmt.Errorf("verifydeliver: memorize: restore the lease after the oracles: %w", err)
	}
	content := renderMemorize(ticket, slices, lines, questions)
	path := filepath.Join(leaseDir, ".claude", "retrieval", ticket+".md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("verifydeliver: memorize: create retrieval dir: %w", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return fmt.Errorf("verifydeliver: memorize: write retrieval notes: %w", err)
	}
	_, err := commitIfChanged(leaseDir, fmt.Sprintf("docs: memorize %s", ticket), identityEnv)
	if err != nil {
		return fmt.Errorf("verifydeliver: memorize: commit: %w", err)
	}
	return nil
}

// commitIfChanged stages every change in dir and commits it with
// identityEnv, reporting whether a commit was made.
func commitIfChanged(dir, msg string, identityEnv []string) (bool, error) {
	if _, err := gitx.Run(dir, "add", "-A"); err != nil {
		return false, err
	}
	out, err := gitx.Run(dir, "diff", "--cached", "--name-only")
	if err != nil {
		return false, err
	}
	if strings.TrimSpace(out) == "" {
		return false, nil
	}
	if _, err := gitx.RunEnv(dir, identityEnv, "commit", "-m", msg); err != nil {
		return false, err
	}
	return true, nil
}

// writeChangelogs writes one changelog per workspace plus the consolidated
// changelog, under <ticket>/changelog/ in the store.
func writeChangelogs(st *store.Store, ticket string, slices []store.Slice, lines []journal.Line) error {
	dir := filepath.Join(st.TicketDir(ticket), "changelog")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("verifydeliver: changelog: create dir: %w", err)
	}
	sliceWS := sliceWorkspaceMap(slices)
	for _, ws := range distinctWorkspaces(slices) {
		content := journal.RenderChangelog(lines, ws, sliceWS)
		if err := os.WriteFile(filepath.Join(dir, ws+".md"), []byte(content), 0o644); err != nil {
			return fmt.Errorf("verifydeliver: changelog: write %s.md: %w", ws, err)
		}
	}
	consolidated := journal.RenderConsolidated(lines)
	if err := os.WriteFile(filepath.Join(dir, "consolidated.md"), []byte(consolidated), 0o644); err != nil {
		return fmt.Errorf("verifydeliver: changelog: write consolidated.md: %w", err)
	}
	return nil
}

// answersBlock renders the ledger's "**Answers:**" section: one bullet per
// answered question, or "none".
func answersBlock(questions []store.Question) string {
	var lines []string
	for _, q := range questions {
		if q.Status != "answered" || q.Answer == "" {
			continue
		}
		lines = append(lines, fmt.Sprintf("- %s: %s -> %s", q.ID, q.Body, q.Answer))
	}
	if len(lines) == 0 {
		return "none"
	}
	return strings.Join(lines, "\n")
}

// appendLedgerEntry appends this ticket's ledger entry to the store's
// ledger.md, under a lock.
func appendLedgerEntry(st *store.Store, ticket, title string, slices []store.Slice, questions []store.Question) error {
	path := filepath.Join(st.Root, "ledger.md")
	release, _, err := store.Lock(path, 30*time.Second)
	if err != nil {
		return fmt.Errorf("verifydeliver: ledger: lock: %w", err)
	}
	defer release()

	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("verifydeliver: ledger: read: %w", err)
	}
	entry := fmt.Sprintf(
		"\n## %s - %s\n\nDelivered %d slice(s).\n\n**Answers:**\n%s\n",
		ticket, title, len(slices), answersBlock(questions),
	)
	out := append(append([]byte{}, existing...), []byte(entry)...)
	return store.AtomicWrite(path, out)
}

// appendContractIndexEntry appends this ticket's contract-index entry
// under the store's platform directory, under a lock.
func appendContractIndexEntry(st *store.Store, cfgPlatform, ticket string, slices []store.Slice, oracleNames []string) error {
	platform := cfgPlatform
	if platform == "" {
		platform = "platform/"
	}
	path := filepath.Join(st.Root, filepath.FromSlash(platform), "contract-index.md")
	release, _, err := store.Lock(path, 30*time.Second)
	if err != nil {
		return fmt.Errorf("verifydeliver: contract-index: lock: %w", err)
	}
	defer release()

	existing, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("verifydeliver: contract-index: read: %w", err)
	}
	ws := distinctWorkspaces(slices)
	sort.Strings(ws)
	names := append([]string{}, oracleNames...)
	sort.Strings(names)
	entry := fmt.Sprintf("- %s: workspaces %s, oracles %s\n", ticket, strings.Join(ws, ","), strings.Join(names, ","))
	out := append(append([]byte{}, existing...), []byte(entry)...)
	return store.AtomicWrite(path, out)
}

// writeEvidence writes <ticket>/pr/evidence.md: a per-round list linking
// each round's findings and diff changelog, plus any receipt files. It
// stays a store record even though the PR body no longer links to it
// (the body's own detail link is the pull request's first comment,
// pr/review-notes.md).
func writeEvidence(st *store.Store, ticket string, lastRound int) error {
	ticketDir := st.TicketDir(ticket)
	var b strings.Builder
	b.WriteString("# Evidence\n\n")
	for n := 1; n <= lastRound; n++ {
		fmt.Fprintf(&b, "## Round %d\n\n", n)
		fmt.Fprintf(&b, "- findings: gate/round-%d/findings.md\n", n)
		fmt.Fprintf(&b, "- diff changelog: gate/round-%d/diff-changelog.md\n", n)
		evDir := filepath.Join(ticketDir, "evidence", fmt.Sprintf("round-%d", n))
		if entries, err := os.ReadDir(evDir); err == nil {
			for _, e := range entries {
				if e.IsDir() {
					continue
				}
				fmt.Fprintf(&b, "- receipt: evidence/round-%d/%s\n", n, e.Name())
			}
		}
		b.WriteString("\n")
	}
	dir := filepath.Join(ticketDir, "pr")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("verifydeliver: evidence: create pr dir: %w", err)
	}
	return os.WriteFile(filepath.Join(dir, "evidence.md"), []byte(b.String()), 0o644)
}

// shortSHA returns the first 7 characters of a commit SHA for display
func shortSHA(sha string) string {
	if len(sha) < 7 {
		return sha
	}
	return sha[:7]
}

// firstBriefSection returns the body of briefMD's first "## " section,
// whatever it is titled: from the line after that heading up to (but
// excluding) the next "## " heading or EOF. CRLF is normalized first, the
// same way store.BriefSectionHashes normalizes it before splitting on "## "
// - this mirrors that split instead of hand-rolling its own, but keeps only
// the first section rather than hashing every one.
//
// It is the whole of the ## Intent body for a brief-sourced intent, and ""
// whenever briefMD has no first section with text under it - no "## "
// heading at all, or one with nothing below it. renderIntentSection then
// writes no section whatsoever, the same as for an inferred or absent
// intent: a brief that states no section's worth of intent has none to
// publish, and the whole brief is not it. Owner decision, DECISIONS.md.
//
// A "## " line inside a fenced code block is not a heading and ends nothing
// (fenceScanner): a brief whose first section quotes markdown - one showing
// the very sections this body is built from, say - keeps its whole example,
// and the section it yields never stops mid-fence. A brief whose only "## "
// lines sit inside a fence has no section of its own by the same rule. That
// is the one way this split diverges from store.BriefSectionHashes', whose
// hashes are a section's identity for slices.yaml rather than something
// rendered.
func firstBriefSection(briefMD string) string {
	text := strings.ReplaceAll(briefMD, "\r\n", "\n")
	lines := strings.Split(text, "\n")
	var fence fenceScanner
	start := -1
	for i, line := range lines {
		if !fence.fenced(line) && strings.HasPrefix(line, "## ") {
			start = i + 1
			break
		}
	}
	if start == -1 {
		return ""
	}
	end := start
	for end < len(lines) {
		if !fence.fenced(lines[end]) && strings.HasPrefix(lines[end], "## ") {
			break
		}
		end++
	}
	return strings.TrimSpace(strings.Join(lines[start:end], "\n"))
}

// stripIntentFrontMatter strips intent.md's "---\n...\n---\n" front matter
// block from data's raw bytes, if one is there, and returns the rest
// trimmed: the fallback for a hand-edited intent.md store.ParseIntent could
// not read normally still renders only the author's own text, never the
// source/agent/session/score keys that front the file. When no front matter
// block can be found at all, the whole text is returned trimmed, since that
// is still more useful to a reviewer than an empty section.
func stripIntentFrontMatter(data []byte) string {
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	if !strings.HasPrefix(text, "---\n") {
		return strings.TrimSpace(text)
	}
	rest := text[len("---\n"):]
	end := strings.Index(rest, "\n---\n")
	if end == -1 {
		return strings.TrimSpace(text)
	}
	return strings.TrimSpace(rest[end+len("\n---\n"):])
}

// atxHeadingLevel returns line's ATX heading level - the "#" run that opens
// it - and 0 when line is not a heading at all. The rules are the ones
// GitHub renders a pull request body by: at most three leading spaces, one
// to six "#", and then a space, a tab or the end of the line. So "#hashtag"
// is text, and a run of seven "#" is text at every level.
func atxHeadingLevel(line string) int {
	rest := strings.TrimLeft(line, " ")
	if len(line)-len(rest) > 3 {
		return 0
	}
	level := 0
	for level < len(rest) && rest[level] == '#' {
		level++
	}
	if level == 0 || level > 6 {
		return 0
	}
	after := strings.TrimRight(rest[level:], "\r")
	if after == "" || strings.HasPrefix(after, " ") || strings.HasPrefix(after, "\t") {
		return level
	}
	return 0
}

// codeFenceRun returns the character a line opens or closes a fenced code
// block with, how long that run is, and the info string that follows the run,
// or 0, 0 and "" when the line is not a fence: three or more "`" or "~" after
// at most three leading spaces.
//
// The info string comes back with spaces, tabs and the CR of a CRLF line
// ending trimmed off either end, so it is "" for exactly the lines markdown
// lets close a block - "```", "``` " and "```\r" - and the language of a
// "```sh" for the lines that only ever open one.
func codeFenceRun(line string) (byte, int, string) {
	rest := strings.TrimLeft(line, " ")
	if len(line)-len(rest) > 3 || rest == "" {
		return 0, 0, ""
	}
	marker := rest[0]
	if marker != '`' && marker != '~' {
		return 0, 0, ""
	}
	run := 0
	for run < len(rest) && rest[run] == marker {
		run++
	}
	if run < 3 {
		return 0, 0, ""
	}
	return marker, run, strings.Trim(rest[run:], " \t\r")
}

// fenceScanner tracks, as a markdown text's lines are walked in order,
// whether the line in hand falls inside a fenced code block. Everything in
// one is a code sample's own text: it renders literally, so a "## " line
// there is neither a section of a brief nor a heading of the body, and both
// functions that look for "## " lines - firstBriefSection and
// demoteHeadings - ask this before believing one.
//
// The zero value starts outside any block. Every line of the text must be
// fed, in order and none skipped, since any of them can open or close a
// block.
type fenceScanner struct {
	marker byte
	run    int
}

// fenced reports whether line is inside a fenced code block, consuming the
// fence it opens or closes (a fence line is itself inside the block). A block
// is closed only by a fence of its own character, at least as long as the one
// that opened it, and carrying no info string: inside a "```" block both a
// "~~~" and a "```sh" are sample text like any other line. An info string is
// allowed only on an opening fence - which is how cmark-gfm, what GitHub
// renders a pull request body with, reads them - so a text whose intended
// closing fence names a language still leaves its block open.
func (f *fenceScanner) fenced(line string) bool {
	if marker, run, info := codeFenceRun(line); run > 0 {
		switch {
		case f.run == 0:
			f.marker, f.run = marker, run
		case marker == f.marker && run >= f.run && info == "":
			f.marker, f.run = 0, 0
		}
		return true
	}
	return f.run > 0
}

// openFence returns the character and length of the fence a block the lines
// walked so far left open was opened with, and 0, 0 when the text closed
// every block it opened.
func (f *fenceScanner) openFence() (byte, int) {
	return f.marker, f.run
}

// closeOpenFence closes a fenced code block text leaves open, appending a
// fence matching the one that opened it; a text that closes every block it
// opens comes back untouched.
//
// Embedded in pr/<repo>.md, an intent text is followed by the body's own
// ## What changed and ## Verification sections, and an unclosed fence
// swallows them: GitHub renders everything after it as the inside of a code
// block, headings and all, which breaks the body's three-section promise
// silently - no error and no warning. Both binding sources can hand over
// such a text. `jig gate --doc` records a whole file, which ends mid-fence
// whenever the file does, and a brief's first section ends where the brief's
// next section begins, which can be inside a fence the brief itself never
// closed. So it is closed here, where it is embedded, rather than at either
// source.
func closeOpenFence(text string) string {
	var fence fenceScanner
	for _, line := range strings.Split(text, "\n") {
		fence.fenced(line)
	}
	marker, run := fence.openFence()
	if run == 0 {
		return text
	}
	closing := strings.Repeat(string(marker), run)
	if strings.HasSuffix(text, "\n") {
		return text + closing
	}
	return text + "\n" + closing
}

// demoteHeadings rewrites text's own ATX headings so none of them sits at or
// above the "## " level pr/<repo>.md's own sections use: every heading moves
// down by the same amount, the least that puts the shallowest one at "### ".
// Text whose headings are all below "## " already comes back untouched.
//
// This is what keeps the body's "exactly three ## sections, in this order"
// promise for an explicit intent, whose text is an arbitrary file's whole
// contents when it came from `jig gate --doc` (writeExplicitIntent). A design
// doc or spec carries its own headings, and rendered as they stand they both
// add "## " sections the body never promised - indistinguishable from jig's
// own three - and, with a "# " title, outrank all three of them. The text
// itself still reaches the reviewer intact, which is the half of the promise
// the other remedy (dropping or rewriting the doc's prose) would have cost.
//
// One shift for the whole text, not a clamp per heading, so the doc's own
// nesting survives the move. The shift stops at "######", the deepest
// heading markdown has: a doc already using all six levels has its last two
// collapsed rather than rendered as literal "#######" text.
//
// The only bytes ever added are the "#" inserted: line endings, indentation
// and everything else come back as they went in. A heading inside a fenced
// code block is left alone - it is a code sample's own text, which renders
// literally and is never a section of the body.
func demoteHeadings(text string) string {
	lines := strings.Split(text, "\n")
	levels := make([]int, len(lines))
	shallowest := 0
	var fence fenceScanner
	for i, line := range lines {
		if fence.fenced(line) {
			continue
		}
		level := atxHeadingLevel(line)
		levels[i] = level
		if level > 0 && (shallowest == 0 || level < shallowest) {
			shallowest = level
		}
	}
	if shallowest == 0 || shallowest > 2 {
		return text
	}

	shift := 3 - shallowest
	for i, level := range levels {
		if level == 0 {
			continue
		}
		want := level + shift
		if want > 6 {
			want = 6
		}
		at := strings.IndexByte(lines[i], '#')
		lines[i] = lines[i][:at] + strings.Repeat("#", want-level) + lines[i][at:]
	}
	return strings.Join(lines, "\n")
}

// intentFilePath returns the absolute path a binding intent source (brief
// or explicit) is read from: brief.md or intent.md. Only meaningful for
// those two sources - renderIntentSection and checkReviewedIntent both
// guard on that before calling it.
func intentFilePath(st *store.Store, ticket, source string) string {
	if source == IntentSourceBrief {
		return filepath.Join(st.TicketDir(ticket), "brief.md")
	}
	return st.IntentPath(ticket)
}

// renderIntentSection renders the ## Intent section when intent source is
// brief or explicit. Returns empty string for inferred or none sources: an
// inferred intent summarizes the author's own private agent session and
// never reaches a pull request. Also empty when the source file yields
// no text of its own - a brief with no first "## " section with text
// under it, whether it has no such heading or one with nothing below it
// (firstBriefSection), an intent.md that is front matter and nothing else:
// a published body never opens with a bare "## Intent" heading and nothing
// under it.
//
// Whatever the source yields is rendered intact but with its own headings
// demoted below the section level (demoteHeadings) and any fenced code block
// it leaves open closed (closeOpenFence), so the body keeps exactly its own
// three "## " sections and they all render as sections. Those are the rules
// an explicit intent needs - `jig gate --doc` records a whole design doc as
// the intent text - and for a brief the demotion is a no-op in every
// ordinary case: firstBriefSection already stops at the next "## ", so the
// body it returns holds a heading that high only when the brief put a "# "
// inside the section itself.
//
// The second return reports one of those omissions: a brief bound as the
// intent source and had no "## " section with text to publish as one - no
// such heading anywhere, or a first section with nothing under it, which
// firstBriefSection reads the same way - so the body goes out with no
// statement of why the change exists. Publish warns the operator about it
// (the omission is the owner's rule, so it is never an error - but nothing
// else in the run, the report or the store records it). The other sources
// need no such signal: an inferred or absent intent has no section to lose,
// and an explicit intent with no text of its own is refused a round earlier
// (INTENT_EMPTY, resolveIntent) and cannot be edited into one after it
// (PUBLISH_UNREVIEWED_INTENT, checkReviewedIntent).
func renderIntentSection(rep reportYAML, st *store.Store, ticket string) (string, bool, error) {
	if rep.Intent.Source != IntentSourceBrief && rep.Intent.Source != IntentSourceExplicit {
		return "", false, nil
	}

	intentPath := intentFilePath(st, ticket, rep.Intent.Source)
	data, err := os.ReadFile(intentPath)
	if err != nil {
		return "", false, fmt.Errorf("verifydeliver: render intent: read %s: %w", intentPath, err)
	}

	var content string
	if rep.Intent.Source == IntentSourceBrief {
		content = firstBriefSection(string(data))
	} else {
		// Explicit: intent.md is YAML front matter plus the intent text.
		// in.Text is used when it parses; a hand-edited file that no longer
		// parses, or whose text field comes back empty, still produces a
		// section - the front matter stripped, never dumped raw - rather
		// than silently dropping it the way an inferred or absent intent
		// does.
		in, perr := store.ParseIntent(data)
		if perr == nil && in.Text != "" {
			content = in.Text
		} else {
			content = stripIntentFrontMatter(data)
		}
	}
	if content == "" {
		return "", rep.Intent.Source == IntentSourceBrief, nil
	}
	content = closeOpenFence(demoteHeadings(content))

	var b strings.Builder
	fmt.Fprintf(&b, "## Intent\n\n%s\n\n", content)
	return b.String(), false, nil
}

// authorCommit is one of an adopted branch's own pre-adoption commits: its
// subject and full sha (shortSHA truncates it for display).
type authorCommit struct {
	SHA     string
	Subject string
}

// adoptedAuthorCommits returns an adopted ticket's own commits, oldest
// first: every commit from the merge base with target up to startSHA, the
// branch's tip as the author left it when the ticket adopted it
// (store.WriteStartSHA/StartSHAPath). Nil when there is nothing before that
// point (the branch forked from origin/target at exactly startSHA).
func adoptedAuthorCommits(leaseDir, target, startSHA string) ([]authorCommit, error) {
	base, err := gitx.MergeBase(leaseDir, "origin/"+target, startSHA)
	if err != nil {
		return nil, fmt.Errorf("verifydeliver: render: merge-base origin/%s %s: %w", target, startSHA, err)
	}
	if base == startSHA {
		return nil, nil
	}
	out, err := gitx.Run(leaseDir, "log", "--reverse", "--format=%H%x09%s", base+".."+startSHA)
	if err != nil {
		return nil, fmt.Errorf("verifydeliver: render: list author commits %s..%s: %w", base, startSHA, err)
	}
	out = strings.TrimSpace(out)
	if out == "" {
		return nil, nil
	}
	var commits []authorCommit
	for _, line := range strings.Split(out, "\n") {
		sha, subject, ok := strings.Cut(line, "\t")
		if !ok {
			continue
		}
		commits = append(commits, authorCommit{SHA: sha, Subject: subject})
	}
	return commits, nil
}

// bulletGoal returns the one line of a slice's goal a ## What changed
// bullet prints: the goal's first line, with a trailing ":" dropped so the
// bullet reads as a phrase of its own rather than promising a continuation
// the body no longer has.
//
// A goal an author wrote in slices.yaml is one line already and comes back
// as it stands. A fix slice's goal is not a goal at all but the builder
// prompt buildFixSlices wrote for it: "Fix these gate findings:" or "Gate
// finding <id>, kept by the human:", then every finding's file:line, title,
// full detail, risk rationale, decision and previous fix slice. Only its
// first line belongs in a bullet. The rest would close the list item and
// render the gate's whole finding text as a paragraph of its own, with the
// short sha stranded at the end of the last line instead of beside the
// bullet, and would reproduce in the body exactly the detail and rationale
// the brief moves to pr/review-notes.md.
func bulletGoal(goal string) string {
	line, _, _ := strings.Cut(goal, "\n")
	return strings.TrimSuffix(strings.TrimSpace(line), ":")
}

// renderWhatChangedSection renders the ## What changed section: for an
// adopted ticket, the author's own pre-adoption commits first (subject and
// short sha); then one bullet per green slice, in slice order, with the
// first line of its goal (bulletGoal) and short commit sha - omitted when a
// slice has no recorded green commit, rather than printing empty
// parentheses - and fix slices built from gate findings (FromGate > 0)
// grouped after the others as fixes from review.
//
// Each of those three is a block, and the blank lines between them are
// separators: exactly one between consecutive blocks, and one closing the
// last of them before the next "## " heading. A block that has nothing in
// it contributes no separator of its own, so a ticket with only the gate's
// fixes does not open the section with a blank line, and one with no jig
// bullet at all - an adopted branch whose only jig work is gate fixes, or
// none - does not end it with two.
func renderWhatChangedSection(slices []store.Slice, commits map[string]string, authorCommits []authorCommit) string {
	var regular, fixes []store.Slice
	for _, s := range slices {
		if s.FromGate > 0 {
			fixes = append(fixes, s)
		} else {
			regular = append(regular, s)
		}
	}

	sliceBullets := func(group []store.Slice) string {
		var sb strings.Builder
		for _, s := range group {
			if sha, ok := commits[s.ID]; ok {
				fmt.Fprintf(&sb, "- %s (%s)\n", bulletGoal(s.Goal), shortSHA(sha))
			} else {
				fmt.Fprintf(&sb, "- %s\n", bulletGoal(s.Goal))
			}
		}
		return sb.String()
	}

	var blocks []string
	if len(authorCommits) > 0 {
		var ab strings.Builder
		for _, c := range authorCommits {
			fmt.Fprintf(&ab, "- %s (%s)\n", c.Subject, shortSHA(c.SHA))
		}
		blocks = append(blocks, ab.String())
	}
	if len(regular) > 0 {
		blocks = append(blocks, sliceBullets(regular))
	}
	if len(fixes) > 0 {
		blocks = append(blocks, "Fixes from review:\n\n"+sliceBullets(fixes))
	}

	var b strings.Builder
	b.WriteString("## What changed\n\n")
	if len(blocks) > 0 {
		b.WriteString(strings.Join(blocks, "\n"))
		b.WriteString("\n")
	}
	return b.String()
}

// countFindingOutcomes tallies outcomes by their final bucket, for the PR
// body's one-line findings count.
func countFindingOutcomes(outcomes []findingOutcome) (fixed, dismissed, noted, asked int) {
	for _, o := range outcomes {
		switch o.status {
		case "fixed":
			fixed++
		case "dismissed":
			dismissed++
		case "noted":
			noted++
		case "asked":
			asked++
		}
	}
	return fixed, dismissed, noted, asked
}

// renderVerificationSection renders the ## Verification section: the
// oracles green at the last clean gate round (oracleNames, the manifest's
// own, sorted), the reviewed head, the revalidation tier, and one line
// counting the review's findings by how they ended, pointing at the pull
// request's first comment (pr/review-notes.md) for the detail - never a
// store path nobody on GitHub can open.
func renderVerificationSection(rep reportYAML, tier string, oracleNames []string, outcomes []findingOutcome) string {
	var b strings.Builder
	b.WriteString("## Verification\n\n")

	if len(oracleNames) > 0 {
		b.WriteString("Oracles:\n\n")
		for _, name := range oracleNames {
			fmt.Fprintf(&b, "- %s\n", name)
		}
		b.WriteString("\n")
	}

	// Reviewed head
	if len(rep.ReviewedSHA) > 0 {
		repos := make([]string, 0, len(rep.ReviewedSHA))
		for repo := range rep.ReviewedSHA {
			repos = append(repos, repo)
		}
		sort.Strings(repos)
		b.WriteString("Reviewed head:\n\n")
		for _, repo := range repos {
			fmt.Fprintf(&b, "- %s: %s\n", repo, shortSHA(rep.ReviewedSHA[repo]))
		}
		b.WriteString("\n")
	}

	// Revalidation tier
	fmt.Fprintf(&b, "Revalidation tier: %s\n\n", tier)

	fixed, dismissed, noted, asked := countFindingOutcomes(outcomes)
	fmt.Fprintf(&b, "Findings: %d fixed, %d dismissed, %d noted, %d asked. See the pull request's first comment for detail.\n", fixed, dismissed, noted, asked)
	return b.String()
}

// DemoRenderResult holds the outcome of renderDemoSection.
type DemoRenderResult struct {
	Section string // rendered section, empty when no demo
	// MediaDir is the evidence directory renderDemoSection resolved and
	// verified MediaFiles against for the shipped head - the directory
	// gh's own media-attach calls must run in, so Publish never recomputes
	// it (and risks a different, post-squash head).
	MediaDir string
	Omitted  []string // files that failed verification
	NoDemo   bool     // no demo recorded for this head at all
	// DemoRefused is set when demo.yaml itself recorded a refusal for the
	// shipped head (RefusalReason is why); distinct from NoDemo, which is
	// true when there is nothing recorded for this head at all.
	DemoRefused   bool
	RefusalReason string
	// AllMediaFailed is set when a recorded demo's every listed file failed
	// re-verification at publish time: distinct from a demo recorded with
	// no media at all (ADR 0014, a valid "nothing to show" result), which
	// renders no section but is neither this nor a refusal.
	AllMediaFailed bool
	MediaFiles     []DemoFile // verified media files ready for attachment
	// ScrubbedSummary is set when the recorded summary named one of jig's own
	// directories (any spelling jig may have handed the demo session, a WSL
	// mount among them) and was left out of the rendered section for it,
	// rather than published verbatim (the owner's decision on r1-f13,
	// DECISIONS.md).
	ScrubbedSummary bool
	// ScrubbedCaptions names every verified file whose own caption was left
	// out of the rendered section for the same reason.
	ScrubbedCaptions []string
}

// demoCaptionRenderCap bounds a caption's length in the rendered pull
// request body. demo.yaml's own copy stays exactly as the session wrote it,
// unbounded like the reviewer's result.json (ADR 0014); this cap is only
// for what a published, public pull request shows beside a file, which is
// meant to be a short label, not a paragraph (the owner's decision on
// r1-f13, DECISIONS.md).
const demoCaptionRenderCap = 200

// capDemoCaption truncates caption to demoCaptionRenderCap runes for the
// rendered pull request body, marking a truncated one with "...".
func capDemoCaption(caption string) string {
	r := []rune(caption)
	if len(r) <= demoCaptionRenderCap {
		return caption
	}
	return string(r[:demoCaptionRenderCap]) + "..."
}

// demoCaptionBracketStripper drops '[', ']', '(' and ')' from a caption
// before it is interpolated into the rendered pull request body. An image's
// caption becomes the alt text of `![caption](./<name>)`: a caption's own
// "]" closes that alt text early, after which "(<anything>)" is read as the
// image's own URL rather than a continuation of the session's words - so an
// unbalanced "[" loses the image reference entirely (the "]" jig writes
// closes the caption's own "[" instead), and a caption that itself contains
// "](" can redirect the rendered image at an arbitrary URL of its choosing.
// Dropping all four, rather than only escaping them, keeps the fix the same
// for every caption regardless of kind - a video or captionless file never
// sits inside a `![...](...)`  construct at all, but the same four
// characters could just as easily open a markdown link of their own in that
// bullet's plain text.
var demoCaptionBracketStripper = strings.NewReplacer("[", "", "]", "", "(", "", ")", "")

// sanitizeDemoCaption drops the characters demoCaptionBracketStripper names
// from caption. demo.yaml's own record is untouched; this governs only what
// gets interpolated into the rendered pull request body.
func sanitizeDemoCaption(caption string) string {
	return demoCaptionBracketStripper.Replace(caption)
}

// demoHTMLEscaper renders '&', '<' and '>' as HTML entities. GitHub renders a
// sanitized subset of raw HTML inside a pull request body, which neither
// demoCaptionBracketStripper (it drops only '[', ']', '(' and ')') nor the
// summary's demoteHeadings/closeOpenFence touches: a caption or summary
// carrying `<img src="...">` or `<a href="...">` would otherwise reach the
// body as a live element rather than the session's literal words - true of
// the video/captionless bullet form and of the summary outright, though an
// image caption was already inert there, landing inside `![...](...)`'s alt
// text where HTML is never parsed. Escaping "&" as well as the two brackets
// closes a second route to the same result: a caption or summary that
// already reads `&lt;img src="..."&gt;` would otherwise have that entity
// sequence decoded back into a live tag by GitHub's own renderer, with no
// literal "<" or ">" of its own for this function to have caught. demo.yaml's
// own record is untouched; this governs only what gets interpolated into the
// rendered pull request body, applied last, after every other transform, so
// none of the characters those transforms add is itself escaped.
var demoHTMLEscaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")

// escapeDemoHTML applies demoHTMLEscaper to text.
func escapeDemoHTML(text string) string {
	return demoHTMLEscaper.Replace(text)
}

// renderDemoSection renders the ## Demo section when the shipped head has a
// recorded demo: the latest gate round's own gate/round-<lastRound>/demo.yaml
// when it names that head, or - when the latest round ran no demo because an
// earlier round on the same head already recorded one (ADR 0014's one demo
// per reviewed head; gateDemo reports that as DemoExisting and writes no
// demo.yaml of its own) - the earliest earlier round that did
// (recordedDemoRound). Only files that exist and match the manifest (sha256
// and size) are referenced; omitted files are named in the result for
// publish's output to report, and so is a demo.yaml recorded as refused for
// the shipped head, with its own reason.
//
// Each verified file renders in the one reference form `gh ... --attach`
// actually rewrites for its kind (gate finding r1-f3, DECISIONS.md): an
// image as markdown image syntax, `![caption](./<name>)`, which gh rewrites
// to the uploaded URL in place; a video as the bare path bullet it has never
// reliably rewritten, `./<name>: caption`, which publish reads back and
// patches itself (checkUnrewrittenReferences, rewriteUnrewrittenReferences)
// once the attach call returns.
func renderDemoSection(d Deps, st *store.Store, ticket, repoName string, rep reportYAML, lastRound int) (DemoRenderResult, error) {
	shipHead := rep.ReviewedSHA[repoName]
	if shipHead == "" {
		return DemoRenderResult{NoDemo: true}, nil
	}

	rec, ok, err := readDemoRecord(st, ticket, lastRound)
	if err != nil {
		return DemoRenderResult{}, err
	}
	if !ok {
		round, rerr := recordedDemoRound(st, ticket, lastRound+1, shipHead)
		if rerr != nil {
			return DemoRenderResult{}, rerr
		}
		if round == 0 {
			return DemoRenderResult{NoDemo: true}, nil
		}
		rec, ok, err = readDemoRecord(st, ticket, round)
		if err != nil {
			return DemoRenderResult{}, err
		}
		if !ok {
			return DemoRenderResult{NoDemo: true}, nil
		}
	}
	if rec.HeadSHA != shipHead {
		return DemoRenderResult{NoDemo: true}, nil
	}
	if rec.Status == DemoRefused {
		return DemoRenderResult{DemoRefused: true, RefusalReason: rec.Reason}, nil
	}

	// Get the media directory where the demo files should be.
	mediaDir, err := demoMediaDir(d, ticket, shipHead)
	if err != nil {
		return DemoRenderResult{}, fmt.Errorf("verifydeliver: render demo section: %w", err)
	}
	// The gate lease: where the demo session actually ran (DemoInput.LeaseDir,
	// session.Dispatch.Worktree), so it is one of the directories a session may
	// have been told about, in whatever spelling its backend handed it
	// (herdr's own WSL mount on Windows). It need not exist by publish time;
	// only its path, jig's own, is being compared against.
	leaseDir, err := pool.Dir(d.Home, repoName, ticket, pool.Gate)
	if err != nil {
		return DemoRenderResult{}, fmt.Errorf("verifydeliver: render demo section: resolve the gate lease directory: %w", err)
	}
	knownHostDirs := []hostDir{
		{mediaDir, "media_dir"},
		{d.Home, "<jig home>"},
		{leaseDir, "<lease>"},
		{d.Store.Root, "<store>"},
	}

	// Verify files against the manifest: check existence and hash.
	var verified []DemoFile
	var omitted []string
	for _, f := range rec.Media {
		path := filepath.Join(mediaDir, f.Name)
		info, err := media.LstatPinned(path)
		if err != nil {
			omitted = append(omitted, f.Name)
			continue
		}
		if !info.Mode().IsRegular() {
			omitted = append(omitted, f.Name)
			continue
		}
		// Verify the sha256.
		sum, err := media.HashRegularFile(path, info)
		if err != nil || sum != f.SHA256 {
			omitted = append(omitted, f.Name)
			continue
		}
		// Verify the size.
		if info.Size() != f.Size {
			omitted = append(omitted, f.Name)
			continue
		}
		verified = append(verified, f)
	}

	// If no files verified, report whether that is a failure (something was
	// listed and none of it held up) or simply nothing to show (an
	// intentionally empty, valid recorded demo, ADR 0014).
	if len(verified) == 0 {
		return DemoRenderResult{MediaDir: mediaDir, Omitted: omitted, AllMediaFailed: len(rec.Media) > 0}, nil
	}

	// Render the section. The summary and every caption are the session's own
	// words, recorded as written and unbounded in demo.yaml (ADR 0014); jig
	// does not filter them going in, but a published pull request body is
	// public, so before rendering, each is checked here against the exact
	// paths jig itself knows (knownHostDirs) and left out whole, by exact
	// string match only, when it names one (the owner's decision on r1-f13,
	// DECISIONS.md). A caption that clears that check is still collapsed to
	// one line (demoOneLine), stripped of the characters that could let it
	// break out of its own bullet or, for an image, out of `![...](...)`'s
	// alt text (sanitizeDemoCaption), and capped to a short label's length
	// (capDemoCaption, in that order so the cap governs what is actually
	// rendered); the summary goes through the same
	// demoteHeadings+closeOpenFence treatment renderIntentSection gives text
	// jig did not write, so it cannot forge or outrank one of jig's own
	// "## " sections or leave a fence open over the rest of the body. Both are
	// then run through escapeDemoHTML, the last step before either reaches
	// the body, so no HTML the session wrote - or that any earlier transform
	// added - renders (the owner's decision on r4-f3, DECISIONS.md).
	var b strings.Builder
	b.WriteString("## Demo\n\n")
	summary := rec.Summary
	var scrubbedSummary bool
	if summary != "" && containsHostPath(summary, knownHostDirs...) {
		scrubbedSummary = true
		summary = ""
	}
	if summary != "" {
		fmt.Fprintf(&b, "%s\n\n", escapeDemoHTML(closeOpenFence(demoteHeadings(summary))))
	}
	var scrubbedCaptions []string
	for _, f := range verified {
		caption := f.Caption
		if caption != "" && containsHostPath(caption, knownHostDirs...) {
			scrubbedCaptions = append(scrubbedCaptions, f.Name)
			caption = ""
		} else {
			caption = escapeDemoHTML(capDemoCaption(sanitizeDemoCaption(demoOneLine(caption))))
		}
		ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(f.Name), "."))
		switch {
		case media.Kind(ext) == "image":
			fmt.Fprintf(&b, "- ![%s](./%s)\n", caption, f.Name)
		case caption == "":
			fmt.Fprintf(&b, "- ./%s\n", f.Name)
		default:
			fmt.Fprintf(&b, "- ./%s: %s\n", f.Name, caption)
		}
	}
	b.WriteString("\n")

	return DemoRenderResult{
		Section:          b.String(),
		MediaDir:         mediaDir,
		Omitted:          omitted,
		MediaFiles:       verified,
		ScrubbedSummary:  scrubbedSummary,
		ScrubbedCaptions: scrubbedCaptions,
	}, nil
}

// writePRBody writes <ticket>/pr/<repoName>.md: a lean pull request body
// with Intent, What changed, Demo (if available), and Verification sections,
// rendered from recorded structure with no model call.
//
// Its second return is renderIntentSection's: the body has no ## Intent
// section although a brief bound as its source, the one omission Publish
// warns the operator about. The third return is renderDemoSection's result
// for reporting omitted demo files and demo-unavailability reasons.
func writePRBody(st *store.Store, ticket, repoName string, slices []store.Slice,
	rep reportYAML, tier string, commits map[string]string, authorCommits []authorCommit,
	oracleNames []string, outcomes []findingOutcome, d Deps, lastRound int) (string, bool, DemoRenderResult, error) {
	relPath := filepath.Join(ticket, "pr", repoName+".md")
	fullPath := filepath.Join(st.Root, relPath)

	var b strings.Builder

	// Render Intent section (omitted for inferred/none sources)
	intent, omittedBriefIntent, err := renderIntentSection(rep, st, ticket)
	if err != nil {
		return "", false, DemoRenderResult{}, err
	}
	b.WriteString(intent)

	// Render What changed section
	b.WriteString(renderWhatChangedSection(slices, commits, authorCommits))

	// Render Demo section (if available)
	demoResult, err := renderDemoSection(d, st, ticket, repoName, rep, lastRound)
	if err != nil {
		return "", false, DemoRenderResult{}, err
	}
	b.WriteString(demoResult.Section)

	// Render Verification section
	b.WriteString(renderVerificationSection(rep, tier, oracleNames, outcomes))

	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		return "", false, DemoRenderResult{}, fmt.Errorf("verifydeliver: pr body: create dir: %w", err)
	}
	if err := os.WriteFile(fullPath, []byte(b.String()), 0o644); err != nil {
		return "", false, DemoRenderResult{}, fmt.Errorf("verifydeliver: pr body: write: %w", err)
	}
	return filepath.ToSlash(relPath), omittedBriefIntent, demoResult, nil
}

// findingOutcome pairs one finding with how it ended. status is the bucket
// both the PR body's one-line count and review-notes' own sort/wording key
// off of: "fixed", "dismissed", "noted", "asked", "decided" (a kept ask
// whose fix slice has not cleared it) or "open" (reachable only before a
// gate round has even cleared it, never at a clean, publishable round).
// detail is review-notes' own prose for it.
type findingOutcome struct {
	finding Finding
	status  string
	detail  string
}

// sliceForFinding maps a finding id to the id of the slice that recorded it
// in its own Findings list (store.Slice.Findings: "gate finding ids this
// slice resolves"). A finding fixed more than once (a recurrence whose
// first fix slice did not resolve it) keys to the last such slice in
// slices.yaml's own build order.
func sliceForFinding(slices []store.Slice) map[string]string {
	out := map[string]string{}
	for _, s := range slices {
		for _, id := range s.Findings {
			out[id] = s.ID
		}
	}
	return out
}

// riskOrder returns a numeric order for risk levels, for sorting.
func riskOrder(risk string) int {
	switch strings.ToLower(risk) {
	case "critical":
		return 0
	case "high":
		return 1
	case "medium":
		return 2
	case "low":
		return 3
	default:
		return 4
	}
}

// collectFindingsWithOutcomes makes the one pass over every round's
// gate/round-N/findings.yaml that both pr/<repo>.md's findings count and
// pr/review-notes.md need: every unique finding (latest occurrence wins)
// with how it ended, the union of every round's own reviewed_paths, and the
// last round's own summary. slices resolves a fixed finding's own slice id
// (sliceForFinding) for "fixed by slice X".
//
// A finding's outcome is "fixed" whenever its id appears in any round's
// cleared list, regardless of what its own last recorded status says - a
// finding that was later fixed never reads as merely dismissed or asked. A
// dismissed finding is always a human's own decision (DismissedFixIDs and a
// dismissed ask are both only ever set by the interactive triage prompt,
// never by DefaultTriage): a repeat of an already-dismissed finding
// (ApplyRound's rule 2) resets its own Triage field to "" on purpose, since
// nobody decided that round, so the wording here never depends on it.
func collectFindingsWithOutcomes(ticketDir string, lastRound int, slices []store.Slice) (outcomes []findingOutcome, reviewedPaths []string, lastSummary string, err error) {
	latest := make(map[string]Finding)
	cleared := make(map[string][]string) // finding id -> "round N" strings, every round it cleared in
	reviewed := make(map[string]bool)
	fixedBy := sliceForFinding(slices)

	for n := 1; n <= lastRound; n++ {
		findingsPath := filepath.Join(ticketDir, "gate", fmt.Sprintf("round-%d", n), "findings.yaml")
		data, rerr := os.ReadFile(findingsPath)
		if rerr != nil {
			if os.IsNotExist(rerr) {
				continue
			}
			return nil, nil, "", fmt.Errorf("verifydeliver: review notes: read round %d findings: %w", n, rerr)
		}

		var fy findingsYAML
		if uerr := yaml.Unmarshal(data, &fy); uerr != nil {
			return nil, nil, "", fmt.Errorf("verifydeliver: review notes: parse round %d findings.yaml: %w", n, uerr)
		}

		for _, f := range fy.Findings {
			latest[f.ID] = f
		}
		for _, id := range fy.Cleared {
			cleared[id] = append(cleared[id], fmt.Sprintf("round %d", n))
		}
		for _, p := range fy.ReviewedPaths {
			reviewed[p] = true
		}
		if n == lastRound {
			lastSummary = fy.Summary
		}
	}

	for id, f := range latest {
		o := findingOutcome{finding: f}
		switch {
		case len(cleared[id]) > 0:
			o.status = "fixed"
			if sliceID := fixedBy[id]; sliceID != "" {
				o.detail = fmt.Sprintf("fixed by slice %s and cleared at %s", sliceID, strings.Join(cleared[id], ", "))
			} else {
				o.detail = "fixed and cleared at " + strings.Join(cleared[id], ", ")
			}
		case f.Status == StatusDismissed:
			o.status = "dismissed"
			o.detail = "dismissed by a human"
		case f.Status == StatusNoted:
			o.status = "noted"
			o.detail = "noted"
		case f.Status == StatusAsked:
			o.status = "asked"
			o.detail = "asked and still open"
		case f.Status == StatusOpen && f.Decision != "":
			o.status = "decided"
			o.detail = "kept with the human's decision: " + f.Decision
		default:
			o.status = "open"
			o.detail = "open"
		}
		outcomes = append(outcomes, o)
	}

	reviewedPaths = make([]string, 0, len(reviewed))
	for p := range reviewed {
		reviewedPaths = append(reviewedPaths, p)
	}
	sort.Strings(reviewedPaths)

	return outcomes, reviewedPaths, lastSummary, nil
}

// sortFindingOutcomes sorts outcomes by risk, high first, then by finding id
// for a stable order within a risk: the order both review-notes' own
// Findings section and (through DECISIONS.md's own rule) any other reader of
// this slice can rely on.
func sortFindingOutcomes(outcomes []findingOutcome) {
	sort.SliceStable(outcomes, func(i, j int) bool {
		ri, rj := riskOrder(outcomes[i].finding.Risk), riskOrder(outcomes[j].finding.Risk)
		if ri != rj {
			return ri < rj
		}
		return outcomes[i].finding.ID < outcomes[j].finding.ID
	})
}

// writeReviewNotes writes <ticket>/pr/review-notes.md, the pull request's
// first comment: the last round's own summary, every finding across every
// round ordered by risk with how it ended, and coverage - the files the
// change touched (touchedFiles, Publish's own diff of the ship range)
// against the files the reviewer read (reviewedPaths). outcomes,
// reviewedPaths and lastSummary all come from the one pass
// collectFindingsWithOutcomes already makes over every round's
// findings.yaml; this function reads nothing on its own.
func writeReviewNotes(st *store.Store, ticket string, lastRound int, lastSummary string, outcomes []findingOutcome, reviewedPaths, touchedFiles []string) error {
	ticketDir := st.TicketDir(ticket)
	var b strings.Builder
	b.WriteString("# Review Notes\n\n")

	if lastSummary != "" {
		fmt.Fprintf(&b, "## Round %d\n\n%s\n\n", lastRound, lastSummary)
	}

	sorted := append([]findingOutcome(nil), outcomes...)
	sortFindingOutcomes(sorted)

	if len(sorted) > 0 {
		b.WriteString("## Findings\n\n")
		for _, o := range sorted {
			f := o.finding
			fmt.Fprintf(&b, "- **%s** (%s): %s\n", f.Title, f.Risk, f.RiskRationale)
			fmt.Fprintf(&b, "  - Status: %s\n", o.detail)
			if f.Oracle != "" {
				fmt.Fprintf(&b, "  - Oracle: %s\n", f.Oracle)
			}
			b.WriteString("\n")
		}
	}

	// Coverage: the files the change touched, against the files the
	// reviewer read.
	b.WriteString("## Coverage\n\n")
	touched := append([]string(nil), touchedFiles...)
	sort.Strings(touched)
	if len(touched) > 0 {
		b.WriteString("Touched files:\n\n")
		for _, f := range touched {
			fmt.Fprintf(&b, "- %s\n", f)
		}
		b.WriteString("\n")
	}

	reviewed := append([]string(nil), reviewedPaths...)
	sort.Strings(reviewed)
	if len(reviewed) > 0 {
		b.WriteString("Reviewed paths:\n\n")
		for _, p := range reviewed {
			fmt.Fprintf(&b, "- %s\n", p)
		}
		b.WriteString("\n")
	}

	dir := filepath.Join(ticketDir, "pr")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("verifydeliver: review notes: create pr dir: %w", err)
	}
	return os.WriteFile(filepath.Join(dir, "review-notes.md"), []byte(b.String()), 0o644)
}

// demoHeadingRe and demoNextHeadingRe tolerate a "\r\n" line ending, not
// just "\n": `gh pr view --json body` returns a body served with CRLF line
// endings when it was last edited in the web UI, the same shape
// appendedAttachmentURL below must tolerate, and demoSectionBounds has to
// find the ## Demo heading in that body too, or its scoping never applies
// to it at all.
var (
	demoHeadingRe     = regexp.MustCompile(`(?m)^## Demo\r?\n`)
	demoNextHeadingRe = regexp.MustCompile(`(?m)^## `)
)

// demoSectionBounds returns the [start, end) byte range of the ## Demo
// section's own body in body: from just after its heading line to the next
// "## " heading at the start of a line, or the end of body when ## Demo is
// the last section. ok is false when body has no "## Demo" heading at all.
//
// Every match this package makes against a demo's own ./<name> reference is
// scoped to this range, never the whole body: the ## Intent section is a
// brief's or a doc's text verbatim (`jig gate --doc` records a whole file as
// written), and this repo's own docs and brief use the exact ./demo-<n>.ext
// shape renderDemoSection renders, so an unscoped match can land in prose
// that only happens to quote it.
func demoSectionBounds(body string) (start, end int, ok bool) {
	loc := demoHeadingRe.FindStringIndex(body)
	if loc == nil {
		return 0, 0, false
	}
	start = loc[1]
	end = len(body)
	if next := demoNextHeadingRe.FindStringIndex(body[start:]); next != nil {
		end = start + next[0]
	}
	return start, end, true
}

// checkUnrewrittenReferences reports which of mediaFiles' own ./<name>
// references are still literally present in the ## Demo section of body:
// the exact references writePRBody rendered there, never a substring scan
// for "./demo-" over the whole body (demoSectionBounds), which would also
// match a ./demo- mention in the ## Intent section's own text, or a fenced
// snippet - and would report that prose verbatim rather than the file names
// actually at issue.
func checkUnrewrittenReferences(body string, mediaFiles []DemoFile) []string {
	start, end, ok := demoSectionBounds(body)
	if !ok {
		return nil
	}
	section := body[start:end]
	var unrewritten []string
	for _, f := range mediaFiles {
		if strings.Contains(section, "./"+f.Name) {
			unrewritten = append(unrewritten, f.Name)
		}
	}
	return unrewritten
}

// appendedAttachmentURL returns the URL gh appended for name - a markdown
// link using the file's own name as its link text, on a line of its own -
// and the [start, end) byte range in body spanning that line and its own
// line ending, for the caller to remove by slicing rather than by
// re-matching the line's text; or (ok=false) when body carries no such
// line at or after byte offset from. gh appends one for a file it attached
// but found no recognized reference to rewrite in place (a bare video path,
// the one form "Videos" names, DECISIONS.md), never for one it already
// rewrote.
//
// The search starts at from, never byte 0: gh always appends this link
// after every section writePRBody renders (the ## Demo section included),
// so a match before from can only be something else entirely - an ## Intent
// section carrying its own, unrelated `[<name>](<url>)` link to the same
// file name, the exact shape a brief or a linked doc writes for a file it
// already discusses - and a match there is never the one this read-back is
// looking for.
//
// The line itself may end in "\r\n" (a body served with CRLF line endings,
// what GitHub returns for a body edited in the web UI) or in nothing at all
// (the last line of a body with no trailing newline, what `gh pr view
// --json body` commonly returns) - both are matched and both are included
// in end, so the removal below always drops exactly the line and whatever
// line ending followed it, never leaving the link, a stray blank line, or a
// dangling "\r" behind.
func appendedAttachmentURL(body, name string, from int) (url string, start, end int, ok bool) {
	re := regexp.MustCompile(`(?m)^\[` + regexp.QuoteMeta(name) + `\]\((\S+)\)\r?$`)
	loc := re.FindStringSubmatchIndex(body[from:])
	if loc == nil {
		return "", 0, 0, false
	}
	start, end = from+loc[0], from+loc[1]
	for end < len(body) && (body[end] == '\r' || body[end] == '\n') {
		end++
	}
	return body[from+loc[2] : from+loc[3]], start, end, true
}

// demoBulletReferenceRe matches the bullet writePRBody renders for a video
// kind reference still in its bare ./<name> form: "- ./<name>" at the start
// of a line, with or without the ": caption" tail that follows it there, to
// the end of that line. Anchored to a line's own start, and to the literal
// "- ./" writePRBody itself writes, so a demo summary earlier in the same
// ## Demo section that merely mentions the file's name in passing - "it
// recorded ./demo-1.mp4 for this" - is never mistaken for the rendered
// reference itself.
func demoBulletReferenceRe(name string) *regexp.Regexp {
	return regexp.MustCompile(`(?m)^- \./` + regexp.QuoteMeta(name) + `(:.*)?$`)
}

// replaceDemoBulletReference replaces section's own bullet line for name's
// bare ./<name> reference with one naming url in its place, keeping
// whatever ": caption" tail followed it. ok is false when section carries
// no such bullet - name's reference was already rewritten in place, or was
// never a bullet of this form to begin with.
func replaceDemoBulletReference(section, name, url string) (patched string, ok bool) {
	loc := demoBulletReferenceRe(name).FindStringSubmatchIndex(section)
	if loc == nil {
		return section, false
	}
	tail := ""
	if loc[2] != -1 {
		tail = section[loc[2]:loc[3]]
	}
	return section[:loc[0]] + "- " + url + tail + section[loc[1]:], true
}

// rewriteUnrewrittenReferences patches publish's own second edit: for every
// name in unrewritten (checkUnrewrittenReferences' own report), it finds the
// URL gh appended for that file at or after the ## Demo section's own end
// (appendedAttachmentURL) and moves it to that file's own bullet in the
// ## Demo section (replaceDemoBulletReference) - never elsewhere, for the
// same reason checkUnrewrittenReferences stays scoped to that section -
// removing the appended line so the body names the file once, not twice. A
// name gh appended no URL for at all (a failed attach, or an image
// reference gh itself did not recognize, despite "Videos" saying it would),
// or whose own ./<name> bullet the ## Demo section no longer carries, is
// left exactly as it was, reported back in stillUnrewritten for the caller
// to warn about rather than silently drop. changed reports whether body
// differs from its input, so the caller only spends a second `gh pr edit`
// when there is actually something to fix.
func rewriteUnrewrittenReferences(body string, unrewritten []string) (patched string, changed bool, stillUnrewritten []string) {
	patched = body
	for _, name := range unrewritten {
		start, end, ok := demoSectionBounds(patched)
		if !ok {
			stillUnrewritten = append(stillUnrewritten, name)
			continue
		}
		url, _, _, ok := appendedAttachmentURL(patched, name, end)
		if !ok {
			stillUnrewritten = append(stillUnrewritten, name)
			continue
		}
		newSection, ok := replaceDemoBulletReference(patched[start:end], name, url)
		if !ok {
			stillUnrewritten = append(stillUnrewritten, name)
			continue
		}
		patched = patched[:start] + newSection + patched[end:]

		// The appended link always sits after the ## Demo section (gh appends
		// it at the very end of the body), but the section edit above shifted
		// every byte after it, so the section's own end - and the link's range
		// within it - are both found fresh rather than reused.
		if _, afterDemo, ok := demoSectionBounds(patched); ok {
			if _, linkStart, linkEnd, ok := appendedAttachmentURL(patched, name, afterDemo); ok {
				patched = patched[:linkStart] + patched[linkEnd:]
			}
		}
		changed = true
	}
	if changed {
		// Removing the appended line can leave the blank line that preceded it
		// dangling at the end of body; collapse back to a single trailing line
		// ending, in whichever style body itself used, rather than assuming
		// "\n" and leaving a bare "\r" behind in a CRLF body.
		eol := "\n"
		if strings.Contains(body, "\r\n") {
			eol = "\r\n"
		}
		patched = strings.TrimRight(patched, "\r\n") + eol
	}
	return patched, changed, stillUnrewritten
}
