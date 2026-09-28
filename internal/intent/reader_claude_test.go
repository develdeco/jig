package intent

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/develdeco/jig/internal/gitx"
)

// initTestRepo creates a minimal git repo at a fresh temp dir with one
// commit and, when remote is non-empty, an "origin" remote - every call
// goes through gitx.Run, never git directly.
func initTestRepo(t *testing.T, remote string) string {
	t.Helper()
	dir := t.TempDir()
	if _, err := gitx.Run(dir, "init", "-b", "main"); err != nil {
		t.Fatalf("git init: %v", err)
	}
	if _, err := gitx.Run(dir, "config", "user.email", "fixture@example.invalid"); err != nil {
		t.Fatal(err)
	}
	if _, err := gitx.Run(dir, "config", "user.name", "jig-fixture"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := gitx.Run(dir, "add", "-A"); err != nil {
		t.Fatal(err)
	}
	if _, err := gitx.Run(dir, "commit", "-m", "init"); err != nil {
		t.Fatal(err)
	}
	if remote != "" {
		if _, err := gitx.Run(dir, "remote", "add", "origin", remote); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// escapeJSONString escapes backslashes for embedding a raw OS path (a
// Windows path above all) into a JSON string literal already wrapped in
// double quotes by the testdata template.
func escapeJSONString(s string) string {
	return strings.ReplaceAll(s, `\`, `\\`)
}

// writeTranscript loads testdata/<name>, substitutes {{CWD}} and
// {{CWD_SLASH}} (cwd plus a trailing "/", for a tool-call file_path) with
// cwd - JSON-escaped, since a Windows path's backslashes must not break the
// surrounding string literal - writes the result to path, and sets its
// mtime.
func writeTranscript(t *testing.T, name, path, cwd string, mtime time.Time) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read testdata/%s: %v", name, err)
	}
	escaped := escapeJSONString(cwd)
	content := strings.ReplaceAll(string(data), "{{CWD_SLASH}}", escaped+"/")
	content = strings.ReplaceAll(content, "{{CWD}}", escaped)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, mtime, mtime); err != nil {
		t.Fatal(err)
	}
}

// sessionByID indexes sessions by their own ID for assertions.
func sessionByID(sessions []*Session, id string) *Session {
	for _, s := range sessions {
		if s.ID == id {
			return s
		}
	}
	return nil
}

// TestClaudeReaderDiscover covers the reader's own filters end to end
// against synthetic testdata transcripts: a real match, a below-threshold
// session, a session in another repo, a jig-lease-style clone of the same
// remote (must not match, since identity is the git common dir, never the
// remote URL), a stale partial session, a worktree of the same repo (must
// match), a subagent file folded into its parent, and a session outside
// the mtime window entirely.
func TestClaudeReaderDiscover(t *testing.T) {
	mainRepo := initTestRepo(t, "https://example.invalid/x.git")
	otherRepo := initTestRepo(t, "https://example.invalid/y.git")

	leaseClone := filepath.Join(t.TempDir(), "lease-clone")
	if _, err := gitx.Run(t.TempDir(), "clone", mainRepo, leaseClone); err != nil {
		t.Fatalf("git clone (lease): %v", err)
	}
	// A plain `git clone` points the new repo's own "origin" at the local
	// path it was cloned from, not at that source's own remote - so give
	// it mainRepo's real remote URL by hand. Without this, a same-remote
	// clone's own origin (mainRepo's path) never equals mainRepo's origin
	// ("https://example.invalid/x.git") anyway, and a repoMatches mutant
	// that compared origin URLs instead of git common dir would reject
	// this clone for the wrong reason and the test would not catch it.
	// With the same remote URL on both, only a common-dir comparison -
	// never an origin-URL one - can still tell them apart.
	if _, err := gitx.Run(leaseClone, "remote", "set-url", "origin", "https://example.invalid/x.git"); err != nil {
		t.Fatalf("git remote set-url (lease): %v", err)
	}

	worktreeDir := filepath.Join(t.TempDir(), "worktree")
	if _, err := gitx.Run(mainRepo, "worktree", "add", "--detach", worktreeDir); err != nil {
		t.Fatalf("git worktree add: %v", err)
	}

	repoCommonDir, err := gitx.CommonDir(mainRepo)
	if err != nil {
		t.Fatalf("CommonDir(mainRepo): %v", err)
	}

	home := t.TempDir()
	windowStart := time.Date(2025, 12, 29, 0, 0, 0, 0, time.UTC)
	windowEnd := time.Date(2026, 1, 1, 2, 0, 0, 0, time.UTC) // head (01:00) + 1h
	headTime := time.Date(2026, 1, 1, 1, 0, 0, 0, time.UTC)
	recentMtime := time.Date(2026, 1, 1, 0, 30, 0, 0, time.UTC)
	staleMtime := time.Date(2025, 12, 30, 0, 0, 0, 0, time.UTC) // 25h before headTime, still in-window
	outsideWindowMtime := time.Date(2025, 12, 20, 0, 0, 0, 0, time.UTC)

	proj := func(n int) string { return filepath.Join(home, ".claude", "projects", fmt.Sprintf("proj%d", n)) }

	writeTranscript(t, "match.jsonl", filepath.Join(proj(1), "session-match.jsonl"), mainRepo, recentMtime)
	writeTranscript(t, "below-threshold.jsonl", filepath.Join(proj(1), "session-below.jsonl"), mainRepo, recentMtime)
	writeTranscript(t, "match.jsonl", filepath.Join(proj(2), "session-other.jsonl"), otherRepo, recentMtime)
	writeTranscript(t, "match.jsonl", filepath.Join(proj(3), "session-lease.jsonl"), leaseClone, recentMtime)
	writeTranscript(t, "stale-partial.jsonl", filepath.Join(proj(1), "session-stale.jsonl"), mainRepo, staleMtime)
	writeTranscript(t, "match.jsonl", filepath.Join(proj(4), "session-worktree.jsonl"), worktreeDir, recentMtime)
	writeTranscript(t, "match.jsonl", filepath.Join(proj(1), "session-outside-window.jsonl"), mainRepo, outsideWindowMtime)
	writeTranscript(t, "subagent-parent.jsonl", filepath.Join(proj(5), "session-parent.jsonl"), mainRepo, recentMtime)
	writeTranscript(t, "subagent-child.jsonl", filepath.Join(proj(5), "session-parent", "subagents", "agent-1.jsonl"), mainRepo, recentMtime)
	// A workflow's own subagent transcripts nest one level deeper than a
	// plain subagent's: subagents/workflows/<wf-id>/agent-*.jsonl. The
	// parent transcript here carries no file mentions of its own, so this
	// session only scores a match at all if that nested file is found too.
	writeTranscript(t, "subagent-parent.jsonl", filepath.Join(proj(6), "session-workflow-parent.jsonl"), mainRepo, recentMtime)
	writeTranscript(t, "subagent-child.jsonl", filepath.Join(proj(6), "session-workflow-parent", "subagents", "workflows", "wf-1", "agent-2.jsonl"), mainRepo, recentMtime)

	reader := NewClaudeReader()
	if got := reader.Name(); got != ClaudeAgentName {
		t.Fatalf("Name() = %q, want %q", got, ClaudeAgentName)
	}

	sessions, err := reader.Discover(DiscoverOpts{
		Home:          home,
		WindowStart:   windowStart,
		WindowEnd:     windowEnd,
		RepoCommonDir: repoCommonDir,
	})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}

	// Repo identity: another repo and a same-remote clone must both be
	// absent, a worktree of the same repo must be present.
	if s := sessionByID(sessions, "session-other"); s != nil {
		t.Errorf("Discover returned session-other (a different repo), want it excluded")
	}
	if s := sessionByID(sessions, "session-lease"); s != nil {
		t.Errorf("Discover returned session-lease (a same-remote clone, different common dir), want it excluded")
	}
	worktreeSess := sessionByID(sessions, "session-worktree")
	if worktreeSess == nil {
		t.Fatal("Discover: session-worktree (a worktree of the same repo) missing, want it included")
	}

	// The mtime window: a session's file mtime outside [WindowStart,
	// WindowEnd] must be excluded, even though it lives in the same repo
	// with matching content.
	if s := sessionByID(sessions, "session-outside-window"); s != nil {
		t.Errorf("Discover returned session-outside-window, want it excluded by the mtime window")
	}

	matchSess := sessionByID(sessions, "session-match")
	belowSess := sessionByID(sessions, "session-below")
	staleSess := sessionByID(sessions, "session-stale")
	parentSess := sessionByID(sessions, "session-parent")
	if matchSess == nil || belowSess == nil || staleSess == nil || parentSess == nil {
		t.Fatalf("Discover: missing an expected in-window, same-repo session among %d returned", len(sessions))
	}

	diffFiles := []string{"src/foo.go", "src/bar.go", "src/baz.go"}

	// The full-content match and its worktree twin both score 1.0 and must
	// win Best over everything else in the corpus.
	best := Best(sessions, diffFiles, headTime)
	if best == nil {
		t.Fatal("Best(full corpus): want a match, got nil")
	}
	if best.Session.ID != "session-match" && best.Session.ID != "session-worktree" {
		t.Fatalf("Best(full corpus) = %q, want session-match or session-worktree", best.Session.ID)
	}
	if best.Score != 1.0 {
		t.Fatalf("Best(full corpus).Score = %v, want 1.0", best.Score)
	}

	// A below-threshold session alone (1/3 overlap: below both the 0.5
	// floor and the 2-file minimum) must never be chosen.
	if m := Best([]*Session{belowSess}, diffFiles, headTime); m != nil {
		t.Fatalf("Best(session-below alone) = %+v, want nil (below threshold)", m)
	}

	// A stale partial session (2/3 overlap, active >24h before headTime)
	// needs 0.8 and must be rejected at 0.667.
	if m := Best([]*Session{staleSess}, diffFiles, headTime); m != nil {
		t.Fatalf("Best(session-stale alone) = %+v, want nil (stale, below 0.8)", m)
	}

	// The subagent file's own mentions must be folded into its parent
	// session: the parent's own top-level transcript mentions nothing, so
	// without the fold this would score 0 and never be chosen.
	if len(parentSess.Messages) == 0 {
		t.Fatal("session-parent has no messages at all; the subagent fold produced nothing")
	}
	if m := Best([]*Session{parentSess}, diffFiles, headTime); m == nil {
		t.Fatal("Best(session-parent alone): want a match from its folded-in subagent content, got nil")
	} else if len(m.Overlap) < 2 {
		t.Fatalf("Best(session-parent alone).Overlap = %v, want at least 2 files from the subagent's own edits", m.Overlap)
	}

	// The same fold must reach a workflow's own subagent transcripts too,
	// nested one level deeper at subagents/workflows/<wf-id>/agent-*.jsonl
	// rather than directly under subagents/.
	workflowParentSess := sessionByID(sessions, "session-workflow-parent")
	if workflowParentSess == nil {
		t.Fatal("Discover: session-workflow-parent missing, want it included")
	}
	if len(workflowParentSess.Messages) == 0 {
		t.Fatal("session-workflow-parent has no messages at all; the nested workflow subagent fold produced nothing")
	}
	if m := Best([]*Session{workflowParentSess}, diffFiles, headTime); m == nil {
		t.Fatal("Best(session-workflow-parent alone): want a match from its nested workflow subagent content, got nil")
	} else if len(m.Overlap) < 2 {
		t.Fatalf("Best(session-workflow-parent alone).Overlap = %v, want at least 2 files from the nested subagent's own edits", m.Overlap)
	}
}

// TestClaudeReaderDiscoverCaseVariantCWD checks that a session whose
// recorded cwd differs from the mapped clone's own spelling only in
// drive-letter case still matches - the same on-disk path NTFS itself
// treats as identical, which gitx.CommonDir's symlink-eval canonicalization
// must paper over so a probe transcript written from a lowercase-drive cwd
// is not silently dropped. Meaningful only on Windows: elsewhere the
// drive-letter concept does not apply.
func TestClaudeReaderDiscoverCaseVariantCWD(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("drive-letter case only exists on Windows")
	}
	mainRepo := initTestRepo(t, "https://example.invalid/case.git")
	repoCommonDir, err := gitx.CommonDir(mainRepo)
	if err != nil {
		t.Fatalf("CommonDir(mainRepo): %v", err)
	}
	if len(mainRepo) < 2 || mainRepo[1] != ':' {
		t.Fatalf("mainRepo %q has no drive letter to vary", mainRepo)
	}

	// Flip the drive letter's case in the cwd the transcript records - the
	// same on-disk path, spelled differently.
	drive := mainRepo[0]
	switch {
	case drive >= 'a' && drive <= 'z':
		drive -= 'a' - 'A'
	case drive >= 'A' && drive <= 'Z':
		drive += 'a' - 'A'
	}
	variantCWD := string(drive) + mainRepo[1:]

	home := t.TempDir()
	windowStart := time.Date(2025, 12, 29, 0, 0, 0, 0, time.UTC)
	windowEnd := time.Date(2026, 1, 1, 2, 0, 0, 0, time.UTC)
	recentMtime := time.Date(2026, 1, 1, 0, 30, 0, 0, time.UTC)
	proj := filepath.Join(home, ".claude", "projects", "proj-case")
	writeTranscript(t, "match.jsonl", filepath.Join(proj, "session-case.jsonl"), variantCWD, recentMtime)

	sessions, err := NewClaudeReader().Discover(DiscoverOpts{
		Home:          home,
		WindowStart:   windowStart,
		WindowEnd:     windowEnd,
		RepoCommonDir: repoCommonDir,
	})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if s := sessionByID(sessions, "session-case"); s == nil {
		t.Fatal("Discover: session-case (drive-letter case variant of the mapped clone) missing, want it included")
	}
}

// TestClaudeReaderDiscoverNoRepoCommonDirMatchesNothing checks that an
// empty RepoCommonDir (no mapped clone) returns no sessions at all,
// without even reading the home directory's project listing.
func TestClaudeReaderDiscoverNoRepoCommonDirMatchesNothing(t *testing.T) {
	home := filepath.Join(t.TempDir(), "does-not-exist") // Discover must never even stat this
	sessions, err := NewClaudeReader().Discover(DiscoverOpts{Home: home, RepoCommonDir: ""})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(sessions) != 0 {
		t.Fatalf("Discover with no RepoCommonDir returned %d sessions, want 0", len(sessions))
	}
}

// TestClaudeReaderDiscoverMissingHomeIsNotAnError checks that a home with
// no .claude/projects directory at all (never having run Claude Code)
// returns no sessions, not an error.
func TestClaudeReaderDiscoverMissingHomeIsNotAnError(t *testing.T) {
	home := t.TempDir()
	sessions, err := NewClaudeReader().Discover(DiscoverOpts{Home: home, RepoCommonDir: "/anything"})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(sessions) != 0 {
		t.Fatalf("Discover = %d sessions, want 0", len(sessions))
	}
}

// TestClaudeReaderDiscoverSubdirCWDToolPath: a session started in a repo
// subdirectory (identity accepts this - repo identity is the git common
// dir, the same for every subdirectory of one repo) must still have its
// tool-call paths matched against a diff file shaped relative to the repo
// root, not relative to that subdirectory. Relativized against the
// subdirectory, an absolute path directly inside it would become a bare
// basename, which mentionMatches's own bare-mention rule refuses to match
// against a non-bare diff file - silently losing every tool-call mention
// from a subdirectory session.
func TestClaudeReaderDiscoverSubdirCWDToolPath(t *testing.T) {
	mainRepo := initTestRepo(t, "https://example.invalid/subdir.git")
	sub := filepath.Join(mainRepo, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	repoCommonDir, err := gitx.CommonDir(mainRepo)
	if err != nil {
		t.Fatalf("CommonDir(mainRepo): %v", err)
	}

	home := t.TempDir()
	windowStart := time.Date(2025, 12, 29, 0, 0, 0, 0, time.UTC)
	windowEnd := time.Date(2026, 1, 1, 2, 0, 0, 0, time.UTC)
	recentMtime := time.Date(2026, 1, 1, 0, 30, 0, 0, time.UTC)
	proj := filepath.Join(home, ".claude", "projects", "proj-subdir")
	writeTranscript(t, "subdir-cwd.jsonl", filepath.Join(proj, "session-subdir.jsonl"), sub, recentMtime)

	sessions, err := NewClaudeReader().Discover(DiscoverOpts{
		Home:          home,
		WindowStart:   windowStart,
		WindowEnd:     windowEnd,
		RepoCommonDir: repoCommonDir,
	})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	sess := sessionByID(sessions, "session-subdir")
	if sess == nil {
		t.Fatal("Discover: session-subdir missing, want it included (same repo, in-window)")
	}
	if sess.CWDTopLevel == "" {
		t.Fatal("session-subdir.CWDTopLevel is empty, want the repo's own top level resolved")
	}

	headTime := time.Date(2026, 1, 1, 1, 0, 0, 0, time.UTC)
	m := Best([]*Session{sess}, []string{"sub/foo.go"}, headTime)
	if m == nil {
		t.Fatal("Best: want a match for the subdirectory session's own tool-call path, got nil")
	}
}

// TestLoadClaudeTranscriptToleratesMalformedLine checks that a single
// malformed (not-even-JSON) line does not drop the rest of an otherwise
// good transcript: only that one line is skipped, never the whole file.
func TestLoadClaudeTranscriptToleratesMalformedLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	lines := []string{
		`{"type":"user","cwd":"/repo","timestamp":"2026-01-01T00:00:00.000Z","message":{"role":"user","content":"fix the rounding bug"}}`,
		`not even json`,
		`{"type":"assistant","cwd":"/repo","timestamp":"2026-01-01T00:01:00.000Z","message":{"role":"assistant","content":[{"type":"text","text":"done, edited foo.go"}]}}`,
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	sess, err := loadClaudeTranscript(path, nil)
	if err != nil {
		t.Fatalf("loadClaudeTranscript: %v", err)
	}
	if sess == nil {
		t.Fatal("loadClaudeTranscript: want a session despite the malformed line, got nil")
	}
	if sess.CWD != "/repo" {
		t.Errorf("CWD = %q, want /repo", sess.CWD)
	}
	if len(sess.Messages) != 2 {
		t.Fatalf("Messages = %d, want 2 (only the malformed line skipped)", len(sess.Messages))
	}
}

// TestLoadClaudeTranscriptSkipsOverLongLineWithoutAbortingFile checks that
// a line over maxTranscriptLineBytes - an oversized embedded tool_result,
// say - is skipped rather than aborting the whole file's read the way a
// bufio.Scanner's own fixed maximum token size once did (ErrTooLong): the
// messages around it, from otherwise ordinary lines, still come through.
func TestLoadClaudeTranscriptSkipsOverLongLineWithoutAbortingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	huge := `{"type":"user","cwd":"/repo","timestamp":"2026-01-01T00:05:00.000Z","message":{"role":"user","content":"` +
		strings.Repeat("x", maxTranscriptLineBytes+1024) + `"}}`
	lines := []string{
		`{"type":"user","cwd":"/repo","timestamp":"2026-01-01T00:00:00.000Z","message":{"role":"user","content":"look into the huge tool result issue"}}`,
		huge,
		`{"type":"assistant","cwd":"/repo","timestamp":"2026-01-01T00:10:00.000Z","message":{"role":"assistant","content":[{"type":"text","text":"edited foo.go and bar.go"}]}}`,
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	sess, err := loadClaudeTranscript(path, nil)
	if err != nil {
		t.Fatalf("loadClaudeTranscript: %v", err)
	}
	if sess == nil {
		t.Fatal("loadClaudeTranscript: want a session despite the over-long line, got nil")
	}
	if sess.CWD != "/repo" {
		t.Errorf("CWD = %q, want /repo", sess.CWD)
	}
	if len(sess.Messages) != 2 {
		t.Fatalf("Messages = %d, want 2 (the over-long line skipped, not the whole file aborted)", len(sess.Messages))
	}
	for _, m := range sess.Messages {
		if strings.Contains(m.Text, "xxxxxxxxxx") {
			t.Fatal("a message carries the over-long line's own content; it should have been skipped, not parsed")
		}
	}
}

// TestLoadClaudeTranscriptStopsAtCWDMismatchBeforeStreamingRest checks
// that identity is checked at the first cwd-bearing record, not after
// parsing the whole file: a transcript for a repo matchesCWD rejects
// carries a line that would otherwise be over the size cap further down,
// and loadClaudeTranscript must reject cleanly (nil, nil) without ever
// reading far enough to trip over it.
func TestLoadClaudeTranscriptStopsAtCWDMismatchBeforeStreamingRest(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	huge := `{"type":"assistant","cwd":"/other-repo","timestamp":"2026-01-01T00:01:00.000Z","message":{"role":"assistant","content":[{"type":"text","text":"` +
		strings.Repeat("x", maxTranscriptLineBytes+1024) + `"}]}}`
	lines := []string{
		`{"type":"user","cwd":"/other-repo","timestamp":"2026-01-01T00:00:00.000Z","message":{"role":"user","content":"hello"}}`,
		huge,
	}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	sess, err := loadClaudeTranscript(path, func(cwd string) bool { return cwd == "/repo" })
	if err != nil {
		t.Fatalf("loadClaudeTranscript: unexpected error %v - identity should have rejected this transcript cleanly, before ever reaching the over-long line further down", err)
	}
	if sess != nil {
		t.Fatalf("loadClaudeTranscript: want nil (cwd %q rejected by matchesCWD), got %+v", sess.CWD, sess)
	}
}

// TestLoadClaudeTranscriptKeepsOnlyTheDevelopersOwnWords renders a
// transcript holding every kind of record the reader must tell apart, and
// checks what reaches a Message and the excerpt built from it: the
// developer's own words (a record attributed to the human, or with no
// origin at all as older Claude Code versions write it, a slash command the
// human typed, the text beside a tool result) and the assistant's own
// reply are kept; a background task's notification, a peer session's
// message, a workflow coordinator's message, a compaction recap, a
// transcript-only notice, a harness-injected meta record, every tool result
// (string or array content), a thinking block and a tool call's own input
// values are not. The record's text decides nothing: the human-attributed
// command echo is kept.
func TestLoadClaudeTranscriptKeepsOnlyTheDevelopersOwnWords(t *testing.T) {
	cwd := t.TempDir()
	path := filepath.Join(t.TempDir(), "session.jsonl")
	writeTranscript(t, "record-kinds.jsonl", path, cwd, time.Date(2026, 1, 1, 0, 30, 0, 0, time.UTC))

	sess, err := loadClaudeTranscript(path, nil)
	if err != nil || sess == nil {
		t.Fatalf("loadClaudeTranscript: sess=%v err=%v", sess, err)
	}
	var all strings.Builder
	for _, m := range sess.Messages {
		all.WriteString(m.Text)
		all.WriteString("\n")
	}
	texts := all.String()
	excerpt := RenderExcerpt(sess)

	for _, kept := range []string{
		"HUMAN-ASK",          // origin.kind human
		"LEGACY-ASK",         // no origin: an older Claude Code version
		"HUMAN-COMMAND-ECHO", // a slash command the human typed; text decides nothing
		"MIXED-TEXT",         // the words beside a tool result
		"ASSISTANT-REPLY",    // the assistant's own reply
	} {
		if !strings.Contains(texts, kept) {
			t.Errorf("Messages lost %s, want it kept", kept)
		}
		if !strings.Contains(excerpt, kept) {
			t.Errorf("the excerpt lost %s, want it kept", kept)
		}
	}
	for _, dropped := range []string{
		"TASK-NOTICE",            // origin.kind task-notification
		"PEER-MESSAGE",           // origin.kind peer
		"COORDINATOR-MESSAGE",    // origin.kind coordinator: a third foreign kind, so no two-name denylist passes
		"COMPACT-RECAP",          // isCompactSummary
		"TRANSCRIPT-ONLY-NOTICE", // isVisibleInTranscriptOnly
		"META-CAVEAT",            // isMeta
		"TOOL-RESULT-STRING",     // a tool_result with string content
		"TOOL-RESULT-ARRAY",      // a tool_result with array content
		"MIXED-TOOL-RESULT",      // a tool_result beside the developer's words
		"THINKING-BLOCK",         // a thinking block
		"TOOL-INPUT-COMMAND",     // a tool call's own input values
		"secret-looking-value",   // ditto
		"TOOL-INPUT-OLD",         // ditto
		"TOOL-INPUT-NEW",         // ditto
	} {
		if strings.Contains(texts, dropped) {
			t.Errorf("Messages carry %s, want it dropped", dropped)
		}
		if strings.Contains(excerpt, dropped) {
			t.Errorf("the excerpt carries %s, want it dropped", dropped)
		}
	}

	// A tool call's file path still reaches the matcher, though its input
	// values never reach the text.
	wantPath := filepath.ToSlash(cwd) + "/src/foo.go"
	found := false
	for _, m := range sess.Messages {
		for _, p := range m.FilePaths {
			if filepath.ToSlash(p) == wantPath {
				found = true
			}
		}
	}
	if !found {
		t.Errorf("no message carries the tool call's file path %s", wantPath)
	}
}

// discoverIn runs a Claude reader over home for the repo at mainRepo in the
// given mtime window.
func discoverIn(t *testing.T, home, mainRepo string, start, end time.Time) []*Session {
	t.Helper()
	repoCommonDir, err := gitx.CommonDir(mainRepo)
	if err != nil {
		t.Fatalf("CommonDir(%s): %v", mainRepo, err)
	}
	sessions, err := NewClaudeReader().Discover(DiscoverOpts{
		Home: home, WindowStart: start, WindowEnd: end, RepoCommonDir: repoCommonDir,
	})
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	return sessions
}

// TestClaudeReaderDiscoverWindowEnd: a session whose transcript was last
// written after the window's end is not discovered - it cannot be about a
// change that had not happened yet - while one written exactly at the
// window's end still is.
func TestClaudeReaderDiscoverWindowEnd(t *testing.T) {
	mainRepo := initTestRepo(t, "https://example.invalid/window-end.git")
	home := t.TempDir()
	start := time.Date(2025, 12, 29, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 1, 1, 2, 0, 0, 0, time.UTC)
	proj := filepath.Join(home, ".claude", "projects", "proj-window")
	writeTranscript(t, "match.jsonl", filepath.Join(proj, "session-at-end.jsonl"), mainRepo, end)
	writeTranscript(t, "match.jsonl", filepath.Join(proj, "session-after-end.jsonl"), mainRepo, end.Add(time.Minute))

	sessions := discoverIn(t, home, mainRepo, start, end)
	if sessionByID(sessions, "session-at-end") == nil {
		t.Error("Discover: session-at-end (written exactly at the window's end) missing, want it included")
	}
	if s := sessionByID(sessions, "session-after-end"); s != nil {
		t.Errorf("Discover returned session-after-end (written after the window's end), want it excluded")
	}
}

// TestClaudeReaderDiscoverFoldsSubagents pins what Discover does with a
// subagent's own transcript, as opposed to what the matcher then does with
// it: every folded message is marked FromSubagent (the parent agent's own
// prompt to a subagent is not the developer's words, and the excerpt labels
// it so), the parent's own messages are not, and the session's last
// activity is the later of the parent file's mtime and any subagent
// file's - a subagent still working after its parent's file was last
// written keeps the session current, while an older subagent file never
// pulls it back.
func TestClaudeReaderDiscoverFoldsSubagents(t *testing.T) {
	mainRepo := initTestRepo(t, "https://example.invalid/subagents.git")
	home := t.TempDir()
	start := time.Date(2025, 12, 29, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 1, 1, 6, 0, 0, 0, time.UTC)
	parentMTime := time.Date(2026, 1, 1, 1, 0, 0, 0, time.UTC)
	newerChild := parentMTime.Add(2 * time.Hour)
	olderChild := parentMTime.Add(-2 * time.Hour)
	proj := filepath.Join(home, ".claude", "projects", "proj-sub")
	writeTranscript(t, "subagent-parent.jsonl", filepath.Join(proj, "session-newer.jsonl"), mainRepo, parentMTime)
	writeTranscript(t, "subagent-child.jsonl", filepath.Join(proj, "session-newer", "subagents", "agent-1.jsonl"), mainRepo, newerChild)
	writeTranscript(t, "subagent-parent.jsonl", filepath.Join(proj, "session-older.jsonl"), mainRepo, parentMTime)
	writeTranscript(t, "subagent-child.jsonl", filepath.Join(proj, "session-older", "subagents", "agent-1.jsonl"), mainRepo, olderChild)

	sessions := discoverIn(t, home, mainRepo, start, end)
	for id, wantActivity := range map[string]time.Time{"session-newer": newerChild, "session-older": parentMTime} {
		s := sessionByID(sessions, id)
		if s == nil {
			t.Fatalf("Discover: %s missing", id)
		}
		if !s.LastActivity.Equal(wantActivity) {
			t.Errorf("%s LastActivity = %v, want %v", id, s.LastActivity, wantActivity)
		}
		var own, folded int
		for _, m := range s.Messages {
			if m.FromSubagent {
				folded++
			} else {
				own++
			}
		}
		// subagent-parent.jsonl and subagent-child.jsonl hold two messages
		// each.
		if own != 2 || folded != 2 {
			t.Errorf("%s: %d own and %d folded messages, want 2 and 2 (only the subagent's are FromSubagent)", id, own, folded)
		}
	}
}

// TestClaudeReaderDiscoverFiltersSubagentRecordsLikeAnyOther: a folded
// subagent file goes through the same filter as a session's own: a prompt
// with no origin (the parent agent's own, kept and marked FromSubagent) is
// kept, while a coordinator's message and a record the harness flagged are
// dropped, as anywhere else in a transcript.
func TestClaudeReaderDiscoverFiltersSubagentRecordsLikeAnyOther(t *testing.T) {
	mainRepo := initTestRepo(t, "https://example.invalid/subagent-origins.git")
	home := t.TempDir()
	start := time.Date(2025, 12, 29, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 1, 1, 6, 0, 0, 0, time.UTC)
	mtime := time.Date(2026, 1, 1, 1, 0, 0, 0, time.UTC)
	proj := filepath.Join(home, ".claude", "projects", "proj-sub-origins")
	writeTranscript(t, "subagent-parent.jsonl", filepath.Join(proj, "session-origins.jsonl"), mainRepo, mtime)
	writeTranscript(t, "subagent-origins.jsonl", filepath.Join(proj, "session-origins", "subagents", "agent-1.jsonl"), mainRepo, mtime)

	s := sessionByID(discoverIn(t, home, mainRepo, start, end), "session-origins")
	if s == nil {
		t.Fatal("Discover: session-origins missing")
	}
	var folded []string
	for _, m := range s.Messages {
		if m.FromSubagent {
			folded = append(folded, m.Text)
		}
	}
	if len(folded) != 1 || !strings.Contains(folded[0], "SUB-PROMPT") {
		t.Fatalf("folded subagent messages = %q, want only the prompt with no origin (SUB-PROMPT); a coordinator's message and a flagged record are dropped", folded)
	}
}

// makeJunction creates a Windows directory junction at link pointing to
// target (no privilege needed, unlike a symlink).
func makeJunction(t *testing.T, link, target string) {
	t.Helper()
	out, err := exec.Command("cmd", "/c", "mklink", "/J", link, target).CombinedOutput()
	if err != nil {
		t.Fatalf("mklink /J %s %s: %v\n%s", link, target, err, out)
	}
	// Removed before the temp dir holding it, so the directory cleanup never
	// walks through the junction into the repo it points at.
	t.Cleanup(func() { os.Remove(link) })
}

// TestClaudeReaderDiscoverThroughJunction: a mapped clone recorded through
// an NTFS junction is the same repository as the real path a session's cwd
// names, though the two common dirs are spelled differently
// (filepath.EvalSymlinks does not resolve a junction).
func TestClaudeReaderDiscoverThroughJunction(t *testing.T) {
	if runtime.GOOS != "windows" {
		t.Skip("directory junctions only exist on Windows")
	}
	mainRepo := initTestRepo(t, "https://example.invalid/junction.git")
	junction := filepath.Join(t.TempDir(), "via-junction")
	makeJunction(t, junction, mainRepo)

	home := t.TempDir()
	start := time.Date(2025, 12, 29, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 1, 1, 2, 0, 0, 0, time.UTC)
	proj := filepath.Join(home, ".claude", "projects", "proj-junction")
	writeTranscript(t, "match.jsonl", filepath.Join(proj, "session-real.jsonl"), mainRepo, time.Date(2026, 1, 1, 0, 30, 0, 0, time.UTC))

	// The operator's mapped clone is the junction; the session ran in the
	// real path.
	sessions := discoverIn(t, home, junction, start, end)
	if sessionByID(sessions, "session-real") == nil {
		t.Fatal("Discover: session-real (cwd at the real path) missing for a repo identified through a junction, want it included")
	}
}
