package intent

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/develdeco/jig/internal/gitx"
)

// ClaudeAgentName is Reader.Name for the Claude Code reader.
const ClaudeAgentName = "claude-code"

// claudeReader reads Claude Code transcripts from
// <home>/.claude/projects/*/*.jsonl.
type claudeReader struct{}

// NewClaudeReader returns a Reader over Claude Code transcripts:
// <home>/.claude/projects/*/*.jsonl, one file per session, plus every
// *.jsonl under that session's own <session-id>/subagents/, at any depth
// (a workflow's own subagents nest one level deeper, under
// subagents/workflows/<wf-id>/), counted under their parent session since
// an implementer subagent does the actual editing - their messages are
// folded into the parent Session, never returned as sessions of their own.
func NewClaudeReader() Reader { return claudeReader{} }

func (claudeReader) Name() string { return ClaudeAgentName }

func (claudeReader) Discover(opts DiscoverOpts) ([]*Session, error) {
	// Nothing to identify a session's cwd against: match nothing rather
	// than guess. This also keeps a caller with no mapped clone from ever
	// reading a real project's transcript directory at all.
	if opts.RepoCommonDir == "" {
		return nil, nil
	}
	root := filepath.Join(opts.Home, ".claude", "projects")
	projectDirs, err := os.ReadDir(root)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("intent: read %s: %w", root, err)
	}

	// repoCache avoids re-shelling to git for a cwd already judged once
	// this call (several sessions in the same repo share one cwd).
	// topLevelCache does the same for each accepted session's own
	// CWDTopLevel.
	repoCache := map[string]bool{}
	topLevelCache := map[string]string{}

	var out []*Session
	for _, pd := range projectDirs {
		if !pd.IsDir() {
			continue
		}
		projDir := filepath.Join(root, pd.Name())
		files, err := os.ReadDir(projDir)
		if err != nil {
			continue
		}
		for _, f := range files {
			if f.IsDir() || !strings.HasSuffix(f.Name(), ".jsonl") {
				continue
			}
			info, err := f.Info()
			if err != nil {
				continue
			}
			mtime := info.ModTime()
			if !opts.WindowStart.IsZero() && mtime.Before(opts.WindowStart) {
				continue
			}
			if !opts.WindowEnd.IsZero() && mtime.After(opts.WindowEnd) {
				continue
			}

			sessionID := strings.TrimSuffix(f.Name(), ".jsonl")
			path := filepath.Join(projDir, f.Name())
			sess, err := loadClaudeTranscript(path, func(cwd string) bool {
				return repoMatches(cwd, opts.RepoCommonDir, repoCache)
			})
			if err != nil || sess == nil {
				continue
			}
			sess.Agent = ClaudeAgentName
			sess.ID = sessionID
			sess.LastActivity = mtime
			sess.CWDTopLevel = resolveTopLevel(sess.CWD, topLevelCache)

			// Subagent transcripts live under <session-id>/subagents/,
			// not only directly in it: a workflow's own subagents nest
			// one level deeper, at subagents/workflows/<wf-id>/agent-*
			// .jsonl. Every *.jsonl at any depth under subagents/ is
			// counted under the parent session, since an implementer
			// subagent does the actual editing the matcher needs to see.
			// Their own mtimes are not separately filtered against the
			// window: they happened during their parent session, whose
			// own mtime already passed above, but the most recent of them
			// still updates LastActivity so a session whose subagent
			// activity ran later is not judged stale by its parent file's
			// own, earlier mtime alone.
			subDir := filepath.Join(projDir, sessionID, "subagents")
			_ = filepath.WalkDir(subDir, func(p string, de fs.DirEntry, werr error) error {
				if werr != nil {
					// subDir not existing at all, or one entry under it
					// failing to stat, must not drop the rest of the
					// subagent tree.
					return nil
				}
				if de.IsDir() || !strings.HasSuffix(de.Name(), ".jsonl") {
					return nil
				}
				subSess, lerr := loadClaudeTranscript(p, nil)
				if lerr != nil || subSess == nil {
					return nil
				}
				// Marked FromSubagent before folding in: a RoleUser message
				// here is the parent agent's own prompt to this subagent,
				// not the developer's own words, even though the
				// transcript format gives both the same "user" role - the
				// excerpt renderer needs to tell them apart.
				for i := range subSess.Messages {
					subSess.Messages[i].FromSubagent = true
				}
				sess.Messages = append(sess.Messages, subSess.Messages...)
				if subInfo, ierr := de.Info(); ierr == nil && subInfo.ModTime().After(sess.LastActivity) {
					sess.LastActivity = subInfo.ModTime()
				}
				return nil
			})

			out = append(out, sess)
		}
	}
	return out, nil
}

// repoMatches reports whether cwd names the same repository as
// repoCommonDir: cwd must exist (a cwd that no longer exists never
// matches) and its own git common dir (internal/gitx.CommonDir) must be
// the same directory as repoCommonDir (gitx.SameDir, by file identity, so
// a junction or a subst drive reaching one repository through two
// spellings still matches) - structural identity, never a remote URL or a
// path prefix comparison, so a jig lease clone of the very same remote
// never matches the operator's own mapped clone. The verdict is cached in
// cache across calls for the same cwd.
func repoMatches(cwd, repoCommonDir string, cache map[string]bool) bool {
	if cwd == "" {
		return false
	}
	if _, err := os.Stat(cwd); err != nil {
		return false
	}
	if match, ok := cache[cwd]; ok {
		return match
	}
	match := false
	if common, err := gitx.CommonDir(cwd); err == nil {
		match = gitx.SameDir(common, repoCommonDir)
	}
	cache[cwd] = match
	return match
}

// resolveTopLevel returns cwd's own git worktree top level (gitx.TopLevel),
// cached in cache across calls for the same cwd. "" when it cannot be
// resolved - a session whose cwd has since been removed, say - which the
// matcher treats as "unknown" and falls back to relativizing against cwd
// itself, the same as before CWDTopLevel existed: inference is advisory
// throughout, so a git call that fails here must never itself fail the
// round.
func resolveTopLevel(cwd string, cache map[string]string) string {
	if cwd == "" {
		return ""
	}
	if top, ok := cache[cwd]; ok {
		return top
	}
	top, err := gitx.TopLevel(cwd)
	if err != nil {
		top = ""
	}
	cache[cwd] = top
	return top
}

// maxTranscriptLineBytes bounds how much of a single transcript line
// loadClaudeTranscript will parse. A line longer than this - an oversized
// embedded tool_result, say - is skipped, its content never buffered in
// full, rather than parsed: one absurd record must not cost every other
// message in the file, the way a bufio.Scanner's own fixed maximum token
// size once aborted the whole read with ErrTooLong.
const maxTranscriptLineBytes = 16 * 1024 * 1024

// forEachTranscriptLine calls fn with each newline-delimited line in r
// ("\r\n" and "\n" both stripped), in file order, including a final line
// with no trailing newline. A line over maxTranscriptLineBytes is skipped
// - fn is never called for it - without ever buffering it in full. fn
// returning false stops reading early. The returned error is a genuine
// I/O failure reading r, never a property of any one line's content or
// size.
func forEachTranscriptLine(r io.Reader, fn func(line []byte) (keepGoing bool)) error {
	br := bufio.NewReaderSize(r, 64*1024)
	var line []byte
	skipping := false
	for {
		chunk, err := br.ReadSlice('\n')
		hasNL := len(chunk) > 0 && chunk[len(chunk)-1] == '\n'
		content := chunk
		if hasNL {
			content = chunk[:len(chunk)-1]
		}
		content = bytes.TrimSuffix(content, []byte("\r"))
		if !skipping {
			if len(line)+len(content) > maxTranscriptLineBytes {
				skipping = true
				line = nil
			} else if len(content) > 0 {
				line = append(line, content...)
			}
		}
		switch err {
		case nil:
			// ReadSlice returns a nil error only once it found the
			// delimiter, so hasNL is true here.
			if !skipping {
				if !fn(line) {
					return nil
				}
			}
			line = nil
			skipping = false
		case bufio.ErrBufferFull:
			// The line is not finished yet; keep accumulating (or
			// discarding, once over the cap) on the next iteration -
			// ReadSlice already consumed this chunk from its own buffer,
			// so the next call picks up right where this one left off.
			continue
		case io.EOF:
			if !skipping && len(line) > 0 {
				fn(line)
			}
			return nil
		default:
			return err
		}
	}
}

// claudeRecord is the part of one transcript line the reader looks at.
type claudeRecord struct {
	Type      string          `json:"type"`
	CWD       string          `json:"cwd"`
	Timestamp string          `json:"timestamp"`
	Message   json.RawMessage `json:"message"`
	// Origin says who a user record came from ({"kind": "human"},
	// "task-notification", "peer", ...). Older Claude Code versions write
	// none.
	Origin json.RawMessage `json:"origin"`
	// IsMeta, IsCompactSummary and IsVisibleInTranscriptOnly mark records the
	// harness itself wrote into the transcript: caveats and skill text
	// injected for the model, the recap a compaction leaves behind, notices
	// shown only in the transcript view.
	IsMeta                    bool `json:"isMeta"`
	IsCompactSummary          bool `json:"isCompactSummary"`
	IsVisibleInTranscriptOnly bool `json:"isVisibleInTranscriptOnly"`
}

// originKind returns the kind Origin names, "" when the record has none or
// its origin is not the {"kind": "<string>"} shape.
func (r claudeRecord) originKind() string {
	if len(r.Origin) == 0 {
		return ""
	}
	var o struct {
		Kind string `json:"kind"`
	}
	if err := json.Unmarshal(r.Origin, &o); err != nil {
		return ""
	}
	return o.Kind
}

// isDevelopersOwn reports whether a user record is the developer's own
// words, judged only by what the transcript itself records about it, never
// by its text: a record the harness wrote (isMeta, a compaction recap, a
// transcript-only notice) is not, and neither is one whose origin the
// transcript attributes to someone else (a background task's notification,
// a peer session's message). A record with no origin at all - what older
// Claude Code versions write for everything - is kept, since nothing says
// it is not: the summarizer, a model, judges what is left.
func (r claudeRecord) isDevelopersOwn() bool {
	if r.IsMeta || r.IsCompactSummary || r.IsVisibleInTranscriptOnly {
		return false
	}
	kind := r.originKind()
	return kind == "" || kind == "human"
}

// loadClaudeTranscript reads one Claude Code .jsonl transcript file (a
// top-level session file or a subagent's own) into a Session: cwd (from
// the first record that carries one) and every user/assistant message,
// tool calls and tool results already dropped. It returns nil, nil when
// the file has no cwd anywhere in it - nothing usable for matching or
// repo identity - or, when matchesCWD is non-nil, as soon as it returns
// false for that cwd: a session outside the repo Discover is looking for
// is never fully parsed only to be thrown away, and a bad or oversized
// line elsewhere in an unrelated project's transcript can never cost that
// project's own, otherwise-good session.
func loadClaudeTranscript(path string, matchesCWD func(cwd string) bool) (*Session, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	sess := &Session{}
	rejected := false
	readErr := forEachTranscriptLine(f, func(line []byte) bool {
		if len(line) == 0 {
			return true
		}
		var raw claudeRecord
		if err := json.Unmarshal(line, &raw); err != nil {
			return true
		}
		if sess.CWD == "" && raw.CWD != "" {
			sess.CWD = raw.CWD
			if matchesCWD != nil && !matchesCWD(sess.CWD) {
				rejected = true
				return false
			}
		}
		ts, _ := time.Parse(time.RFC3339Nano, raw.Timestamp)

		switch raw.Type {
		case "user":
			if !raw.isDevelopersOwn() {
				return true
			}
			text, paths := parseClaudeUserMessage(raw.Message)
			text = strings.TrimSpace(text)
			if text == "" {
				return true
			}
			sess.Messages = append(sess.Messages, Message{Role: RoleUser, Text: text, FilePaths: paths, Timestamp: ts})
		case "assistant":
			text, paths := parseClaudeAssistantMessage(raw.Message)
			text = strings.TrimSpace(text)
			if text == "" && len(paths) == 0 {
				return true
			}
			sess.Messages = append(sess.Messages, Message{Role: RoleAssistant, Text: text, FilePaths: paths, Timestamp: ts})
		}
		return true
	})
	if readErr != nil {
		return nil, readErr
	}
	if rejected || sess.CWD == "" {
		return nil, nil
	}
	return sess, nil
}

// parseClaudeUserMessage extracts text from a user record. content may be
// a plain string or an array of typed items; tool_result items are
// dropped entirely, since they are tool output, not user intent.
func parseClaudeUserMessage(raw json.RawMessage) (string, []string) {
	if len(raw) == 0 {
		return "", nil
	}
	var msg struct {
		Content json.RawMessage `json:"content"`
	}
	if err := json.Unmarshal(raw, &msg); err != nil {
		return "", nil
	}
	if len(msg.Content) == 0 {
		return "", nil
	}
	if msg.Content[0] == '"' {
		var s string
		if err := json.Unmarshal(msg.Content, &s); err == nil {
			return s, nil
		}
	}
	var items []map[string]any
	if err := json.Unmarshal(msg.Content, &items); err != nil {
		return "", nil
	}
	var sb strings.Builder
	for _, item := range items {
		if t, _ := item["type"].(string); t == "text" {
			if s, ok := item["text"].(string); ok {
				sb.WriteString(s)
				sb.WriteString("\n")
			}
		}
	}
	return sb.String(), nil
}

// parseClaudeAssistantMessage extracts assistant text and any file paths
// referenced via tool_use input fields. Thinking blocks are dropped.
func parseClaudeAssistantMessage(raw json.RawMessage) (string, []string) {
	if len(raw) == 0 {
		return "", nil
	}
	var msg struct {
		Content []map[string]any `json:"content"`
	}
	if err := json.Unmarshal(raw, &msg); err != nil {
		return "", nil
	}
	var sb strings.Builder
	var paths []string
	for _, item := range msg.Content {
		t, _ := item["type"].(string)
		switch t {
		case "text":
			if s, ok := item["text"].(string); ok {
				sb.WriteString(s)
				sb.WriteString("\n")
			}
		case "tool_use":
			if input, ok := item["input"].(map[string]any); ok {
				paths = append(paths, extractToolPaths(input)...)
			}
		}
	}
	return sb.String(), paths
}

// extractToolPaths pulls plausible file paths from tool input fields. Agent
// tools use several key names for path-like values; cover the common
// variants so the matcher gets every plausible mention.
func extractToolPaths(input map[string]any) []string {
	var out []string
	for _, key := range []string{"file_path", "filePath", "path", "notebook_path"} {
		if s, ok := input[key].(string); ok && s != "" {
			out = append(out, s)
		}
	}
	if edits, ok := input["edits"].([]any); ok {
		for _, e := range edits {
			if m, ok := e.(map[string]any); ok {
				if s, ok := m["file_path"].(string); ok {
					out = append(out, s)
				}
			}
		}
	}
	return out
}
