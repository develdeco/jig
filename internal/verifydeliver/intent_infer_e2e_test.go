package verifydeliver

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/home"
)

// intentInferExcerptMarker is a token only the synthetic transcript holds, in
// its opening line: text that appears anywhere it should not proves the
// excerpt leaked there.
const intentInferExcerptMarker = "TRANSCRIPT-ONLY-MARKER-0f3c9a"

// writeSyntheticClaudeSession writes a minimal Claude Code transcript at
// home/.claude/projects/<proj>/<sessionID>.jsonl: cwd is cwd, and its
// assistant turn mentions every file in mentionedFiles by name (a plain
// structural text mention, matching internal/intent's own scan). mtime is
// set on the file itself, since that - not any timestamp inside the
// content - is what the reader's discovery window filters on.
func writeSyntheticClaudeSession(t *testing.T, home, sessionID, cwd string, mentionedFiles []string, mtime time.Time) {
	t.Helper()
	dir := filepath.Join(home, ".claude", "projects", "proj1")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	cwdJSON, err := json.Marshal(cwd)
	if err != nil {
		t.Fatal(err)
	}
	text := "While working on this I edited " + strings.Join(mentionedFiles, ", ") + "."
	textJSON, err := json.Marshal(text)
	if err != nil {
		t.Fatal(err)
	}
	lines := []string{
		`{"type":"user","cwd":` + string(cwdJSON) + `,"timestamp":"2026-01-01T00:10:00.000Z","message":{"role":"user","content":"Please clamp percentages and make the greeting casual. ` + intentInferExcerptMarker + `"}}`,
		`{"type":"assistant","cwd":` + string(cwdJSON) + `,"timestamp":"2026-01-01T00:20:00.000Z","message":{"role":"assistant","content":[{"type":"text","text":` + string(textJSON) + `}]}}`,
	}
	path := filepath.Join(dir, sessionID+".jsonl")
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, mtime, mtime); err != nil {
		t.Fatal(err)
	}
}

// storeRemoteHistory returns every commit, with its patch, on every ref of
// the fixture's store remote: everything the store has ever pushed.
func storeRemoteHistory(t *testing.T, fx *fixture.Fixture) string {
	t.Helper()
	out, err := gitx.Run(fx.StoreRemote, "log", "--all", "-p", "--no-color")
	if err != nil {
		t.Fatalf("git log in the store remote: %v", err)
	}
	return out
}

// storeTreeHolding returns the path of the first file under the store's
// working tree (its git directory aside) that holds text, or "".
func storeTreeHolding(t *testing.T, storeDir, text string) string {
	t.Helper()
	var hit string
	err := filepath.WalkDir(storeDir, func(p string, de fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if de.IsDir() {
			if de.Name() == ".git" {
				return fs.SkipDir
			}
			return nil
		}
		data, rerr := os.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		if hit == "" && strings.Contains(string(data), text) {
			hit = p
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk the store: %v", err)
	}
	return hit
}

// assertExcerptStaysOutOfTheStore checks the central rule of gate intent
// inference: the session excerpt is written under the jig home - the
// transcript's own marker line is in it - and its text reaches neither the
// store's working tree nor any commit the store pushed to its remote.
func assertExcerptStaysOutOfTheStore(t *testing.T, fx *fixture.Fixture, sessionID string) {
	t.Helper()
	excerpt := filepath.Join(home.IntentExcerptDir(fx.Home), fx.Ticket, sessionID+".md")
	data, err := os.ReadFile(excerpt)
	if err != nil {
		t.Fatalf("read the excerpt under the jig home: %v", err)
	}
	if !strings.Contains(string(data), intentInferExcerptMarker) {
		t.Fatalf("the excerpt %s does not hold the transcript's marker %q:\n%s", excerpt, intentInferExcerptMarker, data)
	}
	if hit := storeTreeHolding(t, fx.StoreDir, intentInferExcerptMarker); hit != "" {
		t.Errorf("the store's working tree holds the excerpt's text in %s", hit)
	}
	if strings.Contains(storeRemoteHistory(t, fx), intentInferExcerptMarker) {
		t.Errorf("the store remote's history holds the excerpt's text")
	}
}
