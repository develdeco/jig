package intent

import (
	"path/filepath"
	"runtime"
	"testing"
	"time"
)

func sessionAt(t time.Time, msgs ...Message) *Session {
	return &Session{Agent: ClaudeAgentName, ID: "s1", CWD: "/repo", LastActivity: t, Messages: msgs}
}

func TestMentionMatches(t *testing.T) {
	cases := []struct {
		name    string
		mention string
		diff    string
		want    bool
	}{
		{"exact", "internal/foo.go", "internal/foo.go", true},
		{"suffix", "/srv/repo/internal/foo.go", "internal/foo.go", true},
		{"dot-slash-prefix", "./internal/foo.go", "internal/foo.go", true},
		// normalizeMention backslash-to-slash conversion goes through
		// filepath.ToSlash, which is a no-op off Windows: a backslash
		// mention only matches on the OS that would have written it.
		{"backslash", `internal\foo.go`, "internal/foo.go", runtime.GOOS == "windows"},
		{"different-dir-same-basename", "other/foo.go", "internal/foo.go", false},
		{"bare-mention-nested-diff", "foo.go", "internal/foo.go", false},
		{"bare-diff-bare-mention", "go.mod", "go.mod", true},
		{"bare-diff-nested-mention", "internal/go.mod", "go.mod", false},
		{"unrelated", "internal/bar.go", "internal/foo.go", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := mentionMatches(c.mention, c.diff); got != c.want {
				t.Errorf("mentionMatches(%q, %q) = %v, want %v", c.mention, c.diff, got, c.want)
			}
		})
	}
}

func TestBestAcceptsSingleFileMatch(t *testing.T) {
	head := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	s := sessionAt(head, Message{Role: RoleAssistant, Text: "", FilePaths: []string{"/repo/alpha/alpha.go"}})
	m := Best([]*Session{s}, []string{"alpha/alpha.go"}, head)
	if m == nil {
		t.Fatal("Best: want a match, got nil")
	}
	if m.Score != 1.0 {
		t.Errorf("Score = %v, want 1.0", m.Score)
	}
}

func TestBestRejectsNoOverlap(t *testing.T) {
	head := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	s := sessionAt(head, Message{Role: RoleAssistant, Text: "touched unrelated.go"})
	if m := Best([]*Session{s}, []string{"alpha/alpha.go"}, head); m != nil {
		t.Fatalf("Best: want nil (no overlap), got %+v", m)
	}
}

// TestBestMultiFileNeedsTwoOverlapsAndHalfScore checks the multi-file
// acceptance rule directly: mutate either bound (2 overlaps or 0.5 score)
// and a session that clears the other alone must still be rejected.
func TestBestMultiFileNeedsTwoOverlapsAndHalfScore(t *testing.T) {
	head := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	diff := []string{"alpha/a.go", "alpha/b.go", "alpha/c.go", "alpha/d.go"}

	// One overlap out of four: score 0.25, below 0.5 and below the 2-file
	// floor. Rejected on both grounds.
	oneOverlap := sessionAt(head, Message{Text: "", FilePaths: []string{"/repo/alpha/a.go"}})
	if m := Best([]*Session{oneOverlap}, diff, head); m != nil {
		t.Fatalf("Best(one overlap of four): want nil, got %+v", m)
	}

	// Two overlaps out of four: score 0.5, meets both the floor and the
	// threshold. Accepted.
	twoOverlaps := sessionAt(head, Message{Text: "", FilePaths: []string{"/repo/alpha/a.go", "/repo/alpha/b.go"}})
	m := Best([]*Session{twoOverlaps}, diff, head)
	if m == nil {
		t.Fatal("Best(two overlaps of four, score 0.5): want a match, got nil")
	}
	if m.Score != 0.5 {
		t.Errorf("Score = %v, want 0.5", m.Score)
	}

	// A two-file diff isolates the 2-overlap floor from the 0.5-score
	// floor: one overlap out of two already scores 0.5 (clearing the
	// score bar on its own), so this is rejected only because a
	// multi-file diff needs at least 2 overlapping files, not because of
	// score.
	oneOfTwo := sessionAt(head, Message{Text: "", FilePaths: []string{"/repo/alpha/a.go"}})
	if got, _ := scoreSession(oneOfTwo, []string{"alpha/a.go", "alpha/b.go"}); got != 0.5 {
		t.Fatalf("scoreSession(one overlap of two) = %v, want 0.5 (so the rejection below isolates the overlap floor)", got)
	}
	if m := Best([]*Session{oneOfTwo}, []string{"alpha/a.go", "alpha/b.go"}, head); m != nil {
		t.Fatalf("Best(one overlap of two, score 0.5): want nil (below the 2-file floor), got %+v", m)
	}
}

// TestBestStaleSessionNeedsHigherScore checks the 24h staleness rule: a
// session last active more than 24h before headTime needs a score of at
// least 0.8, not the ordinary 0.5. Mentions come from message text (bare
// basenames), not FilePaths, so the test does not depend on how an
// absolute path happens to be relativized on the host OS.
func TestBestStaleSessionNeedsHigherScore(t *testing.T) {
	head := time.Date(2026, 1, 10, 12, 0, 0, 0, time.UTC)
	stale := head.Add(-25 * time.Hour)
	diff := []string{"a.go", "b.go", "c.go", "d.go"}

	// Score 0.5 (2/4): clears the ordinary floor but not the stale 0.8 bar.
	s := sessionAt(stale, Message{Text: "touched a.go and b.go"})
	if m := Best([]*Session{s}, diff, head); m != nil {
		t.Fatalf("Best(stale, score 0.5): want nil (needs 0.8), got %+v", m)
	}

	// Score 0.75 (3/4): still below 0.8, still rejected.
	s2 := sessionAt(stale, Message{Text: "touched a.go, b.go and c.go"})
	if m := Best([]*Session{s2}, diff, head); m != nil {
		t.Fatalf("Best(stale, score 0.75): want nil (needs 0.8), got %+v", m)
	}

	// Score 1.0 (4/4): clears the stale bar.
	s3 := sessionAt(stale, Message{Text: "touched a.go, b.go, c.go and d.go"})
	if m := Best([]*Session{s3}, diff, head); m == nil {
		t.Fatal("Best(stale, score 1.0): want a match, got nil")
	}
}

// TestBestTiesBreakByRecency checks that when two sessions tie on score,
// the more recently active one wins.
func TestBestTiesBreakByRecency(t *testing.T) {
	head := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	older := sessionAt(head.Add(-30*time.Minute), Message{Text: "touched a.go"})
	older.ID = "older"
	newer := sessionAt(head.Add(-10*time.Minute), Message{Text: "touched a.go"})
	newer.ID = "newer"

	m := Best([]*Session{older, newer}, []string{"a.go"}, head)
	if m == nil || m.Session.ID != "newer" {
		t.Fatalf("Best: want the more recent session to win a tie, got %+v", m)
	}
}

// TestRelativizeToCWD checks that an absolute tool-call path inside the
// session's own repo top level is turned repo-relative (so it can match a
// bare-basename diff file via the equality rule), using real, OS-native
// absolute paths rather than a hardcoded separator style. cwd and topLevel
// are the same directory here (an ordinary, non-subdirectory session) -
// TestRelativizeToCWDPrefersTopLevelOverCWD below covers the two diverging.
func TestRelativizeToCWD(t *testing.T) {
	cwd := filepath.Join(t.TempDir(), "repo")
	inside := filepath.Join(cwd, "go.mod")
	if got := relativizeToCWD(inside, cwd, cwd); got != "go.mod" {
		t.Errorf("relativizeToCWD(inside cwd) = %q, want %q", got, "go.mod")
	}
	outside := filepath.Join(t.TempDir(), "elsewhere", "go.mod")
	if got := relativizeToCWD(outside, cwd, cwd); got != outside {
		t.Errorf("relativizeToCWD(outside cwd) = %q, want it returned unchanged (%q)", got, outside)
	}
	if got := relativizeToCWD("already/relative.go", cwd, cwd); got != "already/relative.go" {
		t.Errorf("relativizeToCWD(already relative) = %q, want it unchanged", got)
	}
}

// TestRelativizeToCWDPrefersTopLevelOverCWD: a session started in a repo
// subdirectory (cwd) has its tool-call paths relativized against the
// repo's own top level, not cwd - otherwise a file directly inside cwd
// turns into a bare basename ("foo.go") that can no longer match a diff
// file shaped "sub/foo.go" (mentionMatches's own bare-mention rule refuses
// a bare mention for a non-bare diff file). When topLevel is unknown ("",
// never resolved), it falls back to cwd rather than leaving the path
// untouched.
func TestRelativizeToCWDPrefersTopLevelOverCWD(t *testing.T) {
	top := filepath.Join(t.TempDir(), "repo")
	cwd := filepath.Join(top, "sub")
	p := filepath.Join(cwd, "foo.go")

	if got := relativizeToCWD(p, cwd, top); got != filepath.Join("sub", "foo.go") {
		t.Errorf("relativizeToCWD(topLevel known) = %q, want %q", got, filepath.Join("sub", "foo.go"))
	}
	if got := relativizeToCWD(p, cwd, ""); got != "foo.go" {
		t.Errorf("relativizeToCWD(topLevel unknown, cwd fallback) = %q, want %q", got, "foo.go")
	}
}

func TestBestReturnsNilWithNoSessions(t *testing.T) {
	if m := Best(nil, []string{"a.go"}, time.Now()); m != nil {
		t.Fatalf("Best(no sessions): want nil, got %+v", m)
	}
}

func TestScanFilePathsInTextIsStructuralOnly(t *testing.T) {
	got := scanFilePathsInText("I edited internal/foo.go and also mentioned go.mod, then wrote prose about nothing file-like.")
	want := map[string]bool{"internal/foo.go": true, "go.mod": true}
	if len(got) < 2 {
		t.Fatalf("scanFilePathsInText: got %v, want at least %v", got, want)
	}
	found := map[string]bool{}
	for _, g := range got {
		found[g] = true
	}
	for w := range want {
		if !found[w] {
			t.Errorf("scanFilePathsInText: missing %q in %v", w, got)
		}
	}
}
