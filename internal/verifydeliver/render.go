package verifydeliver

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/journal"
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

// writePRBody writes <ticket>/pr/<repoName>.md: a lean pull request body
// with Intent, What changed, and Verification sections, rendered from
// recorded structure with no model call.
//
// Its second return is renderIntentSection's: the body has no ## Intent
// section although a brief bound as its source, the one omission Publish
// warns the operator about.
func writePRBody(st *store.Store, ticket, repoName string, slices []store.Slice,
	rep reportYAML, tier string, commits map[string]string, authorCommits []authorCommit,
	oracleNames []string, outcomes []findingOutcome) (string, bool, error) {
	relPath := filepath.Join(ticket, "pr", repoName+".md")
	fullPath := filepath.Join(st.Root, relPath)

	var b strings.Builder

	// Render Intent section (omitted for inferred/none sources)
	intent, omittedBriefIntent, err := renderIntentSection(rep, st, ticket)
	if err != nil {
		return "", false, err
	}
	b.WriteString(intent)

	// Render What changed section
	b.WriteString(renderWhatChangedSection(slices, commits, authorCommits))

	// Render Verification section
	b.WriteString(renderVerificationSection(rep, tier, oracleNames, outcomes))

	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		return "", false, fmt.Errorf("verifydeliver: pr body: create dir: %w", err)
	}
	if err := os.WriteFile(fullPath, []byte(b.String()), 0o644); err != nil {
		return "", false, fmt.Errorf("verifydeliver: pr body: write: %w", err)
	}
	return filepath.ToSlash(relPath), omittedBriefIntent, nil
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
