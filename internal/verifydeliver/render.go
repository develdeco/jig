package verifydeliver

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

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
// it.
func writeMemorize(leaseDir, ticket string, slices []store.Slice, lines []journal.Line, questions []store.Question, identityEnv []string) error {
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
// each round's findings and diff changelog, plus any receipt files.
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

// writePRBody writes <ticket>/pr/<repoName>.md: the consolidated changelog
// plus a backlink to the gate evidence.
func writePRBody(st *store.Store, ticket, repoName string, consolidated string) (string, error) {
	relPath := filepath.Join(ticket, "pr", repoName+".md")
	fullPath := filepath.Join(st.Root, relPath)
	var b strings.Builder
	b.WriteString(consolidated)
	b.WriteString("\n## Evidence\n\nSee pr/evidence.md.\n")
	if err := os.MkdirAll(filepath.Dir(fullPath), 0o755); err != nil {
		return "", fmt.Errorf("verifydeliver: pr body: create dir: %w", err)
	}
	if err := os.WriteFile(fullPath, []byte(b.String()), 0o644); err != nil {
		return "", fmt.Errorf("verifydeliver: pr body: write: %w", err)
	}
	return filepath.ToSlash(relPath), nil
}
