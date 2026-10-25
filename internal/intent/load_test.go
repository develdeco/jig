package intent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// TestClaudeReaderLoadReadsTheFileItIsGiven: Load reads the one transcript at
// the path a record named, wherever it is - no .claude/projects layout, no
// repository the cwd must belong to, no window the file's time must fall in -
// and names the session after the file.
func TestClaudeReaderLoadReadsTheFileItIsGiven(t *testing.T) {
	repo := initTestRepo(t, "")
	path := filepath.Join(t.TempDir(), "transcripts", "session-recorded.jsonl")
	mtime := time.Date(2026, 3, 4, 5, 6, 0, 0, time.UTC)
	writeTranscript(t, "match.jsonl", path, repo, mtime)

	s, err := NewClaudeReader().Load(path)
	if err != nil || s == nil {
		t.Fatalf("Load = %+v, %v, want the session", s, err)
	}
	if s.ID != "session-recorded" || s.Agent != ClaudeAgentName || s.CWD != repo || !s.LastActivity.Equal(mtime) {
		t.Errorf("session = id %q agent %q cwd %q last activity %v, want session-recorded, %s, %s and %v", s.ID, s.Agent, s.CWD, s.LastActivity, ClaudeAgentName, repo, mtime)
	}
	if len(s.Messages) != 2 || !strings.Contains(s.Messages[0].Text, "rounding bug") {
		t.Errorf("messages = %+v, want the transcript's user and assistant turns", s.Messages)
	}
	if s.CWDTopLevel == "" {
		t.Errorf("CWDTopLevel is empty for a cwd in a git work tree")
	}

	// A cwd that is in no repository the caller knows is still the session
	// that was asked for: the record vouches for it, not the repository.
	elsewhere := filepath.Join(t.TempDir(), "session-elsewhere.jsonl")
	writeTranscript(t, "match.jsonl", elsewhere, filepath.Join(t.TempDir(), "gone"), mtime)
	if s, err := NewClaudeReader().Load(elsewhere); err != nil || s == nil || s.ID != "session-elsewhere" {
		t.Errorf("Load of a transcript whose cwd is gone = %+v, %v, want the session", s, err)
	}
}

// TestClaudeReaderLoadFoldsTheSubagentsBeside: the subagents of the session,
// under <session-id>/subagents next to its file, are folded in as Discover
// folds them, marked as the parent agent's own prompts and edits.
func TestClaudeReaderLoadFoldsTheSubagentsBeside(t *testing.T) {
	repo := initTestRepo(t, "")
	dir := t.TempDir()
	mtime := time.Date(2026, 1, 1, 0, 30, 0, 0, time.UTC)
	writeTranscript(t, "subagent-parent.jsonl", filepath.Join(dir, "session-parent.jsonl"), repo, mtime)
	writeTranscript(t, "subagent-child.jsonl", filepath.Join(dir, "session-parent", "subagents", "agent-1.jsonl"), repo, mtime.Add(time.Hour))

	s, err := NewClaudeReader().Load(filepath.Join(dir, "session-parent.jsonl"))
	if err != nil || s == nil {
		t.Fatalf("Load = %+v, %v, want the session", s, err)
	}
	var own, folded int
	for _, m := range s.Messages {
		if m.FromSubagent {
			folded++
		} else {
			own++
		}
	}
	if own != 2 || folded != 2 {
		t.Errorf("%d own and %d folded messages, want 2 and 2", own, folded)
	}
	if want := mtime.Add(time.Hour); !s.LastActivity.Equal(want) {
		t.Errorf("LastActivity = %v, want the subagent's later %v", s.LastActivity, want)
	}
}

// TestClaudeReaderLoadSaysWhyItCannotRead: a path that is not there is an
// error, and a file with no cwd anywhere in it - not a Claude Code transcript
// - is no session, with no error.
func TestClaudeReaderLoadSaysWhyItCannotRead(t *testing.T) {
	dir := t.TempDir()
	if s, err := NewClaudeReader().Load(filepath.Join(dir, "gone.jsonl")); err == nil || s != nil {
		t.Errorf("Load of a missing file = %+v, %v, want an error", s, err)
	}
	notOurs := filepath.Join(dir, "rollout.jsonl")
	if err := os.WriteFile(notOurs, []byte(`{"type":"session_meta","payload":{"id":"x"}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if s, err := NewClaudeReader().Load(notOurs); err != nil || s != nil {
		t.Errorf("Load of a file with no cwd = %+v, %v, want no session and no error", s, err)
	}
}

// TestReaderForNamesTheHarnessesJigReads: the ledger's name for Claude Code
// (what its hook adapter reports) has a reader, and a harness whose
// transcripts jig does not read has none.
func TestReaderForNamesTheHarnessesJigReads(t *testing.T) {
	if r := ReaderFor("claude"); r == nil || r.Name() != ClaudeAgentName {
		t.Errorf("ReaderFor(claude) = %v, want the Claude Code reader", r)
	}
	for _, harness := range []string{"", "codex", "gemini", "neutral", "claude-code"} {
		if r := ReaderFor(harness); r != nil {
			t.Errorf("ReaderFor(%q) = %v, want none", harness, r)
		}
	}
}
