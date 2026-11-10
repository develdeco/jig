package verifydeliver

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/develdeco/jig/internal/fileshare"
	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/stack"
	"github.com/develdeco/jig/internal/store"
)

// lastGreenCommits maps each slice id to the commit sha of its last green
// result line, or of its last verified line: a commit made by hand and
// verified by `jig verify` has the one and no result line.
func lastGreenCommits(lines []journal.Line) map[string]string {
	out := map[string]string{}
	for _, l := range lines {
		isGreen := l.Event == journal.EventResult && l.Outcome == "green"
		if (isGreen || l.Event == journal.EventVerified) && l.Commit != "" {
			out[l.Slice] = l.Commit
		}
	}
	return out
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
		// A workspace of a project of several repos is named <repo>/<id>: its
		// changelog is in a directory of the repo.
		path := filepath.Join(dir, filepath.FromSlash(ws)+".md")
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return fmt.Errorf("verifydeliver: changelog: create dir for %s.md: %w", ws, err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
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
// answered question, or "none". A question or an answer that runs over
// several lines is folded onto one (oneLine): a line of its own in the entry
// that starts "- " would read as another answer's bullet, and one that starts
// "## " as the next entry's heading.
func answersBlock(questions []store.Question) string {
	var lines []string
	for _, q := range questions {
		if q.Status != "answered" || q.Answer == "" {
			continue
		}
		id := q.ID
		if q.Kind == store.KindCall {
			id += " (call)"
		}
		lines = append(lines, fmt.Sprintf("- %s: %s -> %s", id, oneLine(q.Body), oneLine(q.Answer)))
	}
	if len(lines) == 0 {
		return "none"
	}
	return strings.Join(lines, "\n")
}

// editStoreDoc rewrites the store file at path under its lock: edit gets the
// file's text, "" when there is no file yet, and returns the text to write.
// what names the file in an error.
func editStoreDoc(path, what string, edit func(text string) string) error {
	release, _, err := store.Lock(path, 30*time.Second)
	if err != nil {
		return fmt.Errorf("verifydeliver: %s: lock: %w", what, err)
	}
	defer release()

	existing, err := fileshare.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("verifydeliver: %s: read: %w", what, err)
	}
	return store.AtomicWrite(path, []byte(edit(string(existing))))
}

// writeLedgerEntry puts this ticket's entry in the store's ledger.md, under a
// lock: in the place the ticket's earlier entry held when a publish already
// wrote one, so a republish replaces it and the file keeps one entry per
// ticket, and at the end otherwise.
func writeLedgerEntry(st *store.Store, ticket, title string, slices []store.Slice, questions []store.Question) error {
	entry := fmt.Sprintf(
		"## %s - %s\n\nDelivered %d slice(s).\n\n**Answers:**\n%s\n",
		ticket, oneLine(title), len(slices), answersBlock(questions),
	)
	return editStoreDoc(st.LedgerPath(), "ledger", func(text string) string {
		return store.ReplaceLedgerEntry(text, ticket, entry)
	})
}

// RefreshLedgerEntry rewrites the ticket's ledger entry, in place, from the
// store as it is now: the same entry a republish writes, so an answer or a
// call recorded after the publish shows without one. A ledger with no entry
// for the ticket (it is not published) is left as it is, and a ledger that
// does not exist is not created.
func RefreshLedgerEntry(st *store.Store, ticket string) error {
	existing, err := fileshare.ReadFile(st.LedgerPath())
	if os.IsNotExist(err) || (err == nil && !store.HasLedgerEntry(string(existing), ticket)) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("verifydeliver: ledger: read: %w", err)
	}
	rec, err := st.ReadTicket(ticket)
	if err != nil {
		return fmt.Errorf("verifydeliver: ledger: read ticket record: %w", err)
	}
	sls, err := st.ReadSlices(ticket)
	if err != nil {
		return fmt.Errorf("verifydeliver: ledger: read slices: %w", err)
	}
	questions, err := st.ReadQuestions(ticket)
	if err != nil {
		return fmt.Errorf("verifydeliver: ledger: read questions: %w", err)
	}
	return writeLedgerEntry(st, ticket, publishTitle(st.TicketName(ticket, rec), ticket), sls, questions)
}

// writeContractIndexEntry puts this ticket's contract-index entry in the
// store's platform directory, under a lock, replacing the line a publish
// already wrote for it (writeLedgerEntry).
func writeContractIndexEntry(st *store.Store, cfgPlatform, ticket string, slices []store.Slice, oracleNames []string) error {
	ws := distinctWorkspaces(slices)
	sort.Strings(ws)
	names := append([]string{}, oracleNames...)
	sort.Strings(names)
	entry := fmt.Sprintf("- %s: workspaces %s, oracles %s\n", ticket, strings.Join(ws, ","), strings.Join(names, ","))
	return editStoreDoc(st.ContractIndexPath(cfgPlatform), "contract-index", func(text string) string {
		return store.ReplaceContractIndexEntry(text, ticket, entry)
	})
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
// there is neither a section nor a heading of the body, and the functions
// that look for "## " lines (demoteHeadings, demoSectionBounds) ask this
// before believing one.
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
// Embedded in pr/<repo>.md, the session's What is followed by the body's own
// ## Demo and ## Verification sections, and an unclosed fence swallows them:
// GitHub renders everything after it as the inside of a code block, headings
// and all, which breaks the body's section promise silently - no error and
// no warning. The What is a file the session wrote, which can end mid-fence
// like any file. So it is closed here, where it is embedded, rather than
// where the session wrote it.
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
// those two sources - checkReviewedIntent guards on that before calling it.
func intentFilePath(st *store.Store, ticket, source string) string {
	if source == IntentSourceBrief {
		return filepath.Join(st.TicketDir(ticket), "brief.md")
	}
	return st.IntentPath(ticket)
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

// bulletGoal returns the one line of a slice's goal a ## Slices
// bullet prints: the goal's first line, with a trailing ":" dropped so the
// bullet reads as a phrase of its own rather than promising a continuation
// a list item of the notes does not have.
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

// renderWhatSection renders the ## What section: the bullets the session
// wrote (the stored what file), or the one bullet resolveWhat falls back to.
// Whatever it holds keeps its own headings below the section level
// (demoteHeadings) and any fenced code block it leaves open is closed
// (closeOpenFence), so the body keeps exactly its own "## " sections and they
// all render as sections.
func renderWhatSection(what string) string {
	return fmt.Sprintf("## What\n\n%s\n\n", closeOpenFence(demoteHeadings(strings.TrimSpace(what))))
}

// renderSlicesSection renders the review notes' ## Slices section, where the
// pull request body no longer tells them: for an adopted ticket, the author's
// own pre-adoption commits first (subject and short sha); then one bullet per
// slice, in slice order, with its id, its short commit sha (omitted when it
// has no recorded green commit, rather than printing empty parentheses) and
// the first line of its goal (bulletGoal). A fix slice (FromGate > 0) lists
// the titles of the gate findings it fixed beneath its bullet: the findings
// its Findings names, in outcomes. "" when there is nothing to list.
func renderSlicesSection(slices []store.Slice, commits map[string]string, authorCommits []authorCommit, outcomes []findingOutcome) string {
	titles := map[string]string{}
	for _, o := range outcomes {
		titles[o.finding.ID] = o.finding.Title
	}
	var b strings.Builder
	for _, c := range authorCommits {
		fmt.Fprintf(&b, "- %s (%s)\n", c.Subject, shortSHA(c.SHA))
	}
	for _, s := range slices {
		if sha, ok := commits[s.ID]; ok {
			fmt.Fprintf(&b, "- %s (%s): %s\n", s.ID, shortSHA(sha), bulletGoal(s.Goal))
		} else {
			fmt.Fprintf(&b, "- %s: %s\n", s.ID, bulletGoal(s.Goal))
		}
		if s.FromGate == 0 {
			continue
		}
		for _, id := range s.Findings {
			if title := titles[id]; title != "" {
				fmt.Fprintf(&b, "  - %s\n", title)
			}
		}
	}
	if b.Len() == 0 {
		return ""
	}
	return "## Slices\n\n" + b.String() + "\n"
}

// renderVerificationSection renders the ## Verification section, the gates
// and nothing of the review's findings: the oracles green at the last clean
// gate round (oracleNames, the manifest's own, sorted, each with the command
// that ran, from commands) at the reviewed head,
// and one line per gate round with its scope and verdict. The findings, who
// reviewed and how the change was fixed are the review notes' (the pull
// request's first comment), never the body's.
func renderVerificationSection(rep reportYAML, oracleNames []string, commands map[string][]string, rounds []RoundRecord) string {
	var b strings.Builder
	b.WriteString("## Verification\n\n")

	if len(oracleNames) > 0 {
		fmt.Fprintf(&b, "Oracles green%s:\n\n", reviewedHeadWords(rep.ReviewedSHA))
		for _, name := range oracleNames {
			lines := oracleLines(name, commands[name])
			for i, line := range lines {
				if note := rep.Reuse[name]; note != "" && i == len(lines)-1 {
					line += " (" + note + ")"
				}
				fmt.Fprintf(&b, "- %s\n", line)
			}
		}
		b.WriteString("\n")
	}

	if len(rounds) > 0 {
		b.WriteString("Gate rounds:\n\n")
		for _, r := range rounds {
			if r.Scope != "" {
				fmt.Fprintf(&b, "- Round %d (%s): %s\n", r.Round, r.Scope, roundLine(r))
			} else {
				fmt.Fprintf(&b, "- Round %d: %s\n", r.Round, roundLine(r))
			}
		}
	}
	return strings.TrimRight(b.String(), "\n") + "\n"
}

// roundLine says what a gate round came to in the Gate rounds list: what it
// asked of the build rather than the verdict's name. A clean round is clean,
// unless it carried an earlier round's verdict over (nobody read its head); a
// round with findings says how many fix slices it asked, else that it asks
// for the person, else that it left notes only; a round that was not
// independent keeps its verdict.
func roundLine(r RoundRecord) string {
	switch {
	case r.Verdict == RoundClean && r.CarriedFrom > 0:
		return "carried over"
	case r.Verdict != RoundFindings:
		return r.Verdict
	case len(r.FixSlices) == 1:
		return "1 fix slice asked"
	case len(r.FixSlices) > 1:
		return fmt.Sprintf("%d fix slices asked", len(r.FixSlices))
	case r.Ask > 0 || r.HeldAsks > 0:
		return "asks for the person"
	}
	return "notes only"
}

// oracleLines are the lines an oracle takes in the Verification section: one
// for each distinct command that ran for it (one for each workspace), or its
// name alone when none is named.
func oracleLines(name string, commands []string) []string {
	var out []string
	for _, c := range commands {
		if line := oracleLine(name, c); !slices.Contains(out, line) {
			out = append(out, line)
		}
	}
	if len(out) == 0 {
		return []string{name}
	}
	return out
}

// oracleLine is an oracle as the Verification section lists it: its name and,
// when the journal gives one, the command that ran (on one line, in code), so
// a reader of the body sees what ran. A command that cannot sit in a code span
// is left out and the name stands alone.
func oracleLine(name, command string) string {
	command = strings.Join(strings.Fields(command), " ")
	if command == "" || strings.Contains(command, "`") {
		return name
	}
	return name + ": `" + command + "`"
}

// reviewedHeadWords is " at the reviewed head <sha>" for the heads the gate
// reviewed, each named by its repo when there are several, and "" for a
// scripted round, which recorded none.
func reviewedHeadWords(reviewed map[string]string) string {
	switch len(reviewed) {
	case 0:
		return ""
	case 1:
		for _, sha := range reviewed {
			return " at the reviewed head " + shortSHA(sha)
		}
	}
	repos := make([]string, 0, len(reviewed))
	for repo := range reviewed {
		repos = append(repos, repo)
	}
	sort.Strings(repos)
	parts := make([]string, len(repos))
	for i, repo := range repos {
		parts[i] = repo + " " + shortSHA(reviewed[repo])
	}
	return " at the reviewed heads " + strings.Join(parts, ", ")
}

// DemoFile is one media file the pull request's ## Demo section shows, as
// publish staged it: its name in the staging directory, its sha256 and size,
// and the caption shown beside it.
type DemoFile struct {
	Name    string
	SHA256  string
	Size    int64
	Caption string
}

// DemoRenderResult is the ## Demo section publish rendered from the
// recordings it picked, and what attaching them needs.
type DemoRenderResult struct {
	Section string // rendered section, empty when there is no demo
	// MediaDir is the staging directory the picked files were copied to and
	// checked in, the directory gh's own media-attach calls must run in, so
	// Publish never recomputes it (and risks a different, post-squash head).
	MediaDir   string
	MediaFiles []DemoFile // staged media files ready for attachment
	// ScrubbedSummary is set when the pick's summary named one of jig's own
	// directories (any spelling jig may have handed the pick session, a WSL
	// mount among them) and was left out of the rendered section for it,
	// rather than published verbatim (the pull request body is public).
	ScrubbedSummary bool
	// ScrubbedCaptions names every staged file whose own caption (or flow
	// title) was left out of the rendered section for the same reason.
	ScrubbedCaptions []string
}

// demoCaptionRenderCap bounds a caption's length in the rendered pull
// request body. A published, public pull request shows a short label beside a
// file, not a paragraph.
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
// from caption. It governs what gets interpolated into the rendered pull
// request body.
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
// literal "<" or ">" of its own for this function to have caught. It governs
// what gets interpolated into the rendered pull request body, applied last,
// after every other transform, so none of the characters those transforms add
// is itself escaped.
var demoHTMLEscaper = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")

// escapeDemoHTML applies demoHTMLEscaper to text.
func escapeDemoHTML(text string) string {
	return demoHTMLEscaper.Replace(text)
}

// writePRBody writes <ticket>/pr/<repoName>.md: the pull request body, which
// is the shape .github/CONTRIBUTING.md gives it and nothing else - What (the
// session's bullets, what), Demo (when there is a pick) and Verification (the
// gates) - after the Stacked on and Part of sections a stacked or multi-repo
// ticket's pull request names its neighbours in, and before the trailer the
// project's jig.yaml ends every body with, when it sets one. It is rendered
// from recorded structure with no model call.
//
// The second return is picked itself, the section publish rendered from the
// recordings it picked (ADR 0029), or the zero value when there is no pick: a
// body has a ## Demo section only from a pick.
func writePRBody(st *store.Store, ticket, repoName, what string, rep reportYAML,
	oracleNames []string, commands map[string][]string, rounds []RoundRecord, picked *DemoRenderResult, links []prLink, stacked stack.Repo, trailer string) (string, DemoRenderResult, error) {
	relPath := filepath.Join(st.TicketRelDir(ticket), "pr", repoName+".md")
	fullPath := filepath.Join(st.Root, relPath)

	var b strings.Builder

	// Render the Stacked on section, which names the branch the pull request
	// goes into while the parent the ticket is stacked on has not merged
	b.WriteString(renderStackedSection(ticket, stacked))

	// Render the Part of section, which names the pull requests of the ticket's
	// other repos (none for a ticket that publishes one)
	b.WriteString(renderPartOfSection(ticket, links))

	b.WriteString(renderWhatSection(what))

	// Render Demo section (if there is a pick): the recordings publish picked.
	var demoResult DemoRenderResult
	if picked != nil {
		demoResult = *picked
	}
	b.WriteString(demoResult.Section)

	b.WriteString(renderVerificationSection(rep, oracleNames, commands, rounds))
	b.WriteString(renderTrailer(trailer))

	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		return "", DemoRenderResult{}, fmt.Errorf("verifydeliver: pr body: create dir: %w", err)
	}
	if err := os.WriteFile(fullPath, []byte(b.String()), 0o644); err != nil {
		return "", DemoRenderResult{}, fmt.Errorf("verifydeliver: pr body: write: %w", err)
	}
	return filepath.ToSlash(relPath), demoResult, nil
}

// renderTrailer renders the text the project's jig.yaml ends every pull
// request body with: after a blank line, the trailer with CRLF read as LF and
// surrounding whitespace trimmed (a multi-line trailer keeps its lines), and a
// final newline like the sections before it. The pull request a session opens
// from the render is read back against it, so a trailer is part of the render
// and not something a session appends. An empty trailer renders nothing, which
// leaves the body byte for byte as it is without one.
func renderTrailer(trailer string) string {
	trailer = strings.TrimSpace(strings.ReplaceAll(trailer, "\r\n", "\n"))
	if trailer == "" {
		return ""
	}
	return "\n" + trailer + "\n"
}

// prLink is the pull request of another repo of the same ticket: the repo and
// the url of its pull request, "" while it has none.
type prLink struct {
	Repo string
	URL  string
}

// pendingPR is what a link says of a repo whose pull request is not opened yet.
const pendingPR = "pending"

// renderPartOfSection renders the ## Part of <ticket> section a pull request
// has when the ticket publishes in several repos: one line for each of the
// other repos, with the url of its pull request or "pending" while it has
// none. A ticket that publishes in one repo has no other, and no section.
func renderPartOfSection(ticket string, links []prLink) string {
	if len(links) == 0 {
		return ""
	}
	var b strings.Builder
	fmt.Fprintf(&b, "## Part of %s\n\n", ticket)
	for _, l := range links {
		url := l.URL
		if url == "" {
			url = pendingPR
		}
		fmt.Fprintf(&b, "- also %s: %s\n", l.Repo, url)
	}
	b.WriteString("\n")
	return b.String()
}

// renderStackedSection renders the ## Stacked on <parent> section a pull
// request has while it goes into the branch of the parent its ticket is
// stacked on: which branch that is, and that it goes into the target once the
// parent merges. A pull request into its repo's target has none.
func renderStackedSection(ticket string, r stack.Repo) string {
	if !r.Shared() || r.PRTarget == "" {
		return ""
	}
	return fmt.Sprintf("## Stacked on %s\n\n%s builds on %s: this pull request goes into `%s`, the branch of %s. Once %s merges, retarget it to `%s`.\n\n",
		r.Parent, ticket, r.Parent, r.PRTarget, r.Parent, r.Parent, r.Target)
}

// findingOutcome pairs one finding with how it ended. status is the bucket
// both the PR body's one-line count and review-notes' own sort/wording key
// off of: "fixed", "addressed" (a note the risk floor routed that a later
// round's change fixed, with no slice to credit), "dismissed", "noted", "asked", "decided" (a kept ask
// whose fix slice has not cleared it) or "open" (reachable only before a
// gate round has even cleared it, never at a clean, publishable round).
// detail is review-notes' own prose for it.
type findingOutcome struct {
	// finding is the latest version of the finding. first is its first version
	// and firstRound and lastRound the rounds of the two; they differ when a
	// later round continued the finding (--prior, the same id).
	finding               Finding
	first                 Finding
	firstRound, lastRound int
	status                string
	detail                string
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
// dismissed or kept finding is worded by who decided it, from decisions (the
// ticket's `jig triage` lines, TriageDecisions; nil for none): "dismissed by a
// person", "kept by an agent", with the decision's reason. A finding with no
// such line reads as before: "dismissed by a human". A repeat of an
// already-dismissed finding (ApplyRound's rule 2) resets its own Triage field
// to "" on purpose, so the wording comes from the line, never from that field.
func collectFindingsWithOutcomes(ticketDir string, decisions map[string]TriageDecision, lastRound int, slices []store.Slice) (outcomes []findingOutcome, reviewedPaths []string, lastSummary string, err error) {
	latest := make(map[string]Finding)
	first := make(map[string]Finding)
	firstRound := make(map[string]int)
	lastSeen := make(map[string]int)
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
			if _, seen := first[f.ID]; !seen {
				first[f.ID], firstRound[f.ID] = f, n
			}
			latest[f.ID], lastSeen[f.ID] = f, n
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
		o := findingOutcome{finding: f, first: first[id], firstRound: firstRound[id], lastRound: lastSeen[id]}
		switch {
		case len(cleared[id]) > 0 && fixedBy[id] == "" && f.Status == StatusNoted && f.RoutedAs == ActionNote && decisions[id].Outcome != TriageKeep:
			// A note the risk floor routed, cleared because a round's change
			// touched its file and the reviewer no longer saw it: no slice
			// fixed it, so it reads addressed, not fixed.
			o.status = "addressed"
			o.detail = "addressed at " + strings.Join(cleared[id], ", ")
		case len(cleared[id]) > 0 && decisions[id].Outcome == TriageKeep && (f.Status == StatusAsked || f.Status == StatusNoted):
			// A keep the closing round found already fixed (GATE-15): it was
			// kept, no slice was built from it, and a later round no longer
			// reported it.
			o.status = "fixed"
			o.detail = decidedWording("kept", TriageKeep, decisions[id], "", "kept") + ", then fixed and cleared at " + strings.Join(cleared[id], ", ")
		case len(cleared[id]) > 0:
			o.status = "fixed"
			if sliceID := fixedBy[id]; sliceID != "" {
				o.detail = fmt.Sprintf("fixed by slice %s and cleared at %s", sliceID, strings.Join(cleared[id], ", "))
			} else {
				o.detail = "fixed and cleared at " + strings.Join(cleared[id], ", ")
			}
		case f.Status == StatusDismissed:
			o.status = "dismissed"
			o.detail = decidedWording("dismissed", TriageDismiss, decisions[id], "", "dismissed by a human")
		case f.Status == StatusNoted:
			o.status = "noted"
			o.detail = "noted"
		case f.Status == StatusAsked:
			o.status = "asked"
			o.detail = "asked and still open"
		case f.Status == StatusOpen && (f.Decision != "" || decisions[id].Outcome == TriageKeep):
			o.status = "decided"
			o.detail = decidedWording("kept", TriageKeep, decisions[id], f.Decision, "kept with the human's decision: "+f.Decision)
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

// continuedWithChange reports whether a later round continued the finding and
// changed its title or risk, so the notes show its first version beside the
// latest and a finding a reviewer downgraded does not lose what it first said.
func (o findingOutcome) continuedWithChange() bool {
	return o.lastRound > o.firstRound && (o.first.Title != o.finding.Title || o.first.Risk != o.finding.Risk)
}

// sortFindingOutcomes sorts outcomes by risk, high first, then by finding id
// for a stable order within a risk: the order both review-notes' own
// Findings section and any other reader of
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

// otherFinding is a finding that belongs to another repo of the ticket,
// told in one line in this repo's review notes.
type otherFinding struct {
	repo, file, title string // file is repo-relative
}

// reviewerLines are the review notes' list of who reviewed each reviewer
// round, oldest first, with the round's scope and the range it read (a round
// no recorded reviewer reported, a scripted one, is left out). Every round is
// listed, not the last alone: a delta round reads only part of the change, so
// what the whole change had for a review is the rounds together.
func reviewerLines(rounds []RoundRecord) []string {
	var out []string
	for _, r := range rounds {
		if r.Independent == "" {
			continue
		}
		var what []string
		if r.Scope != "" {
			what = append(what, r.Scope)
		}
		if rng := shortRange(r.Base, r.Head); rng != "" {
			what = append(what, rng)
		}
		label := fmt.Sprintf("Round %d", r.Round)
		if r.CarriedFrom > 0 {
			// No reviewer read this round: its head only merged the target
			// into the head a reviewer read (GATE-16).
			out = append(out, fmt.Sprintf("- %s: carried over from round %d (the head only merges the target into the head that round reviewed; no reviewer read it).", label, r.CarriedFrom))
			continue
		}
		if len(what) > 0 {
			label += " (" + strings.Join(what, ", ") + ")"
		}
		out = append(out, "- "+label+": reviewed by "+reviewerWho(r.Reviewer, r.Independent)+".")
	}
	return out
}

// reviewerWho names a round's reviewer and says whether they are one of the
// builders, as the round recorded it.
func reviewerWho(p Provenance, independent Independence) string {
	who := "a reviewer jig could not identify"
	if p.Known() {
		who = "session `" + p.Session + "`"
		if p.Agent != "" {
			who += ", agent `" + p.Agent + "`"
		}
	}
	switch independent {
	case IndependentYes:
		return who + ", none of the builders"
	case IndependentNo:
		return who + ", one of the builders: not an independent review"
	default:
		return who + "; jig could not tell whether the reviewer built part of the change"
	}
}

// shortRange is base..head with each sha cut to 7 characters, or "" when the
// round's base is not known or its head names several repos.
func shortRange(base, head string) string {
	if base == "" || head == "" || strings.Contains(head, ":") {
		return ""
	}
	return shortSHA(base) + ".." + shortSHA(head)
}

// reviewNotesFile is the name, in the ticket's pr directory, of the review notes
// that go with the pull request of repo: review-notes.md, or for a ticket that
// publishes in several repos review-notes-<repo>.md, since each pull request
// carries its own.
func reviewNotesFile(repo string, several bool) string {
	if several {
		return "review-notes-" + repo + ".md"
	}
	return "review-notes.md"
}

// writeReviewNotes writes <ticket>/pr/<name> (see reviewNotesFile), the pull
// request's first comment: who reviewed each round (rounds, ReadRounds), the
// last round's own summary, every finding across every round ordered by risk
// with how it ended, and coverage - the files the change touched
// (touchedFiles, Publish's own diff of the ship range) against the files the
// reviewer read (reviewedPaths), and, for a reviewer's rounds, the behavior
// they tested (behavior, ADR 0036; nil for scripted rounds). Who reviewed
// comes first: whether a reviewer built part of the change is a fact of the
// round for whoever merges to weigh, not a condition publish checks (ADR
// 0046). outcomes, reviewedPaths and lastSummary all come from the one pass
// collectFindingsWithOutcomes already makes over every round's
// findings.yaml; the one thing this function reads itself is the brief's
// approval, for the line on who approved it; approvalLine, the line on who
// approved the publish, comes from the caller ("" for none), and so does
// slicesSection, the rendered ## Slices (renderSlicesSection, "" for none).
func writeReviewNotes(st *store.Store, ticket, name, approvalLine string, lastRound int, rounds []RoundRecord, lastSummary string, outcomes []findingOutcome, others []otherFinding, reviewedPaths, touchedFiles []string, behavior *behaviorCoverage, slicesSection string) error {
	ticketDir := st.TicketDir(ticket)
	var b strings.Builder
	b.WriteString("# Review Notes\n\n")

	line, err := briefApprovalLine(st, ticket)
	if err != nil {
		return err
	}
	if line != "" {
		b.WriteString(line + "\n\n")
	}
	if approvalLine != "" {
		b.WriteString(approvalLine + "\n\n")
	}

	if lines := reviewerLines(rounds); len(lines) > 0 {
		b.WriteString("## Reviewers\n\n" + strings.Join(lines, "\n") + "\n\n")
	}

	if lastSummary != "" {
		fmt.Fprintf(&b, "## Round %d\n\n%s\n\n", lastRound, lastSummary)
	}

	// The slices the change was built in, and the findings the fix slices
	// fixed: the pull request body does not tell them.
	b.WriteString(slicesSection)

	sorted := append([]findingOutcome(nil), outcomes...)
	sortFindingOutcomes(sorted)

	if len(sorted) > 0 {
		b.WriteString("## Findings\n\n")
		for _, o := range sorted {
			f := o.finding
			fmt.Fprintf(&b, "- **%s** (%s): %s\n", f.Title, f.Risk, f.RiskRationale)
			fmt.Fprintf(&b, "  - Status: %s\n", o.detail)
			if o.continuedWithChange() {
				fmt.Fprintf(&b, "  - History: %s at round %d: %s; continued at round %d as %s: %s\n", o.first.Risk, o.firstRound, o.first.Title, o.lastRound, f.Risk, f.Title)
			}
			if f.Oracle != "" {
				fmt.Fprintf(&b, "  - Oracle: %s\n", f.Oracle)
			}
			b.WriteString("\n")
		}
	}

	if len(others) > 0 {
		b.WriteString("## Findings in other repos\n\n")
		for _, o := range others {
			fmt.Fprintf(&b, "- %s: %s: %s\n", o.repo, o.file, o.title)
		}
		b.WriteString("\n")
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

	// The behavior the review tested: a clean verdict rests on these probes,
	// and says nothing of the hunks no probe or skip covers.
	if behavior != nil {
		if len(behavior.Probes) == 0 {
			b.WriteString("Probes current at the reviewed head: none\n\n")
		} else {
			b.WriteString("Probes current at the reviewed head:\n\n")
			for _, p := range behavior.Probes {
				fmt.Fprintf(&b, "- %s:%d %s: %s (%s; round %d)\n", p.File, p.Line, p.Outcome, p.Mutation, p.Seam, p.Round)
			}
			b.WriteString("\n")
		}
		if len(behavior.Skips) > 0 {
			b.WriteString("Set aside as on no critical path:\n\n")
			for _, s := range behavior.Skips {
				where := s.File
				if s.From != 0 {
					where = fmt.Sprintf("%s:%d-%d", s.File, s.From, s.To)
				}
				fmt.Fprintf(&b, "- %s: %s\n", where, s.Why)
			}
			b.WriteString("\n")
		}
		if len(behavior.Unprobed) > 0 {
			b.WriteString("Hunks no probe or skip covers:\n\n")
			for _, h := range behavior.Unprobed {
				fmt.Fprintf(&b, "- %s:%d-%d\n", h.File, h.From, h.To)
			}
			b.WriteString("\n")
		}
	}

	dir := filepath.Join(ticketDir, "pr")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("verifydeliver: review notes: create pr dir: %w", err)
	}
	return os.WriteFile(filepath.Join(dir, name), []byte(b.String()), 0o644)
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
// written), and this repo's own docs and brief use the exact ./rec-<n>.ext
// shape renderPicksSection renders, so an unscoped match can land in prose
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
// for "./rec-" over the whole body (demoSectionBounds), which would also
// match a ./rec- mention in the ## What section's own text, or a fenced
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
// the one form a real gh run left as it stood), never for one it already
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
// recorded ./rec-1.mp4 for this" - is never mistaken for the rendered
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

// briefApprovalLine is the review notes' line on who approved the brief: a
// person at a terminal, or an agent that was not one. "" for a ticket with no
// approval (a branch built outside jig).
func briefApprovalLine(st *store.Store, ticket string) (string, error) {
	a, ok, err := st.ReadApproval(ticket)
	if err != nil || !ok {
		return "", err
	}
	if a.Provenance == "agent" {
		return "Brief approved by an agent, as " + a.By + ", not by a person at a terminal", nil
	}
	return "Brief approved by " + a.By + " at a terminal", nil
}

// decidedWording words a finding a `jig triage` line decided: "<verb> by a
// person" or "<verb> by an agent", then ": <reason>" from the line, else
// fallbackReason. A finding with no line for outcome, or a line of no known
// provenance, reads as legacy.
func decidedWording(verb, outcome string, d TriageDecision, fallbackReason, legacy string) string {
	var who string
	switch {
	case d.Outcome != outcome:
		return legacy
	case d.Provenance == "person":
		who = "a person"
	case d.Provenance == "agent":
		who = "an agent"
	default:
		return legacy
	}
	reason := d.Reason
	if reason == "" {
		reason = fallbackReason
	}
	if reason != "" {
		return verb + " by " + who + ": " + reason
	}
	return verb + " by " + who
}
