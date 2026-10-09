package e2e

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

// intentExcerptMarker is a token only the synthetic transcript holds, in its
// opening line: text that appears anywhere it should not proves the excerpt
// leaked there.
const intentExcerptMarker = "TRANSCRIPT-ONLY-MARKER-0f3c9a"

// writeSyntheticClaudeSession writes a minimal Claude Code transcript at
// home/.claude/projects/<proj>/<sessionID>.jsonl: cwd is cwd, and its
// assistant turn mentions every file in mentionedFiles by name (a plain
// structural text mention, matching internal/intent's own scan). mtime is
// set on the file itself, since that - not any timestamp inside the
// content - is what the reader's discovery window filters on. Mirrors
// internal/verifydeliver's own test helper of the same name; duplicated
// here rather than exported, since it lives in that package's own _test.go
// file.
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
		`{"type":"user","cwd":` + string(cwdJSON) + `,"timestamp":"2026-01-01T00:10:00.000Z","message":{"role":"user","content":"Please clamp percentages and make the greeting casual. ` + intentExcerptMarker + `"}}`,
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

// TestGateInfersIntentBriefLess is the end-to-end case: a brief-less
// ticket, gated through the real jig binary (--backend fake, not the
// scripted GateSource other suites use), infers its intent from a
// synthetic local Claude Code transcript under HOME - the same HOME
// newFixture points at a fresh temp dir, never the real one, in the
// environment runJig hands its subprocess.
func TestGateInfersIntentBriefLess(t *testing.T) {
	t.Parallel()
	fx, env := newFixture(t, fixture.Opts{ScenarioBranch: "inferred-intent"})
	ticket := fx.Ticket

	if err := os.Remove(filepath.Join(fx.StoreDir, ticket, "brief.md")); err != nil {
		t.Fatalf("remove brief.md: %v", err)
	}

	// newFixture already pointed HOME/USERPROFILE at a fresh temp dir in the
	// environment runJig hands the subprocess below; use it rather than
	// duplicating that decision here.
	homeDir := env.userHome
	if homeDir == "" {
		t.Fatal("no user home - expected newFixture to have set one")
	}
	mentioned := []string{"alpha/alpha.go", "alpha/percent.go", "alpha/percent_test.go", "beta/beta.go", "beta/version.go"}
	writeSyntheticClaudeSession(t, homeDir, "session-inferred", fx.RepoDir, mentioned, time.Date(2026, 1, 1, 0, 30, 0, 0, time.UTC))

	// Drive the fixture's own a/b/c/d scenario to green, exactly as
	// TestGateReviewerNonTerminalTriage does: run pauses at slice c's own
	// question (q-001), answering it completes the frontier.
	r1 := runJig(t, env, fx.StoreDir, "run", ticket, "--backend", "fake", "--scenario", fx.ScenarioDir)
	if r1.Code != 2 {
		t.Fatalf("run 1 exit = %d, want 2 (paused at q-001)\nstdout:\n%s\nstderr:\n%s", r1.Code, r1.Stdout, r1.Stderr)
	}
	r2 := runJig(t, env, fx.StoreDir, "run", ticket, "--answer", "q-001", "Casual.", "--backend", "fake", "--scenario", fx.ScenarioDir)
	if r2.Code != 0 {
		t.Fatalf("run 2 (answer) exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", r2.Code, r2.Stdout, r2.Stderr)
	}

	r3 := runJig(t, env, fx.StoreDir, "gate", ticket, "--backend", "fake", "--scenario", fx.ScenarioDir)
	if r3.Code != 0 {
		t.Fatalf("gate exit = %d, want 0\nstdout:\n%s\nstderr:\n%s", r3.Code, r3.Stdout, r3.Stderr)
	}
	if !strings.Contains(r3.Stdout, "verdict: clean") {
		t.Fatalf("gate stdout missing verdict: clean:\n%s", r3.Stdout)
	}
	if !strings.Contains(r3.Stdout, "intent: inferred") {
		t.Fatalf("gate stdout missing the inferred intent row:\n%s", r3.Stdout)
	}

	data, err := os.ReadFile(filepath.Join(fx.StoreDir, ticket, "intent.md"))
	if err != nil {
		t.Fatalf("read intent.md: %v", err)
	}
	if !strings.Contains(string(data), "source: inferred") {
		t.Fatalf("intent.md missing source: inferred:\n%s", data)
	}

	// The inferred intent must also reach review.json itself - not only
	// the printed row and intent.md - since that is what the reviewer
	// session dispatched for this round was actually given.
	reviewData, err := os.ReadFile(filepath.Join(fx.StoreDir, ticket, "work", "gate.round-1.review.json"))
	if err != nil {
		t.Fatalf("read work/gate.round-1.review.json: %v", err)
	}
	var review struct {
		Intent struct {
			Source string `json:"source"`
		} `json:"intent"`
	}
	if err := json.Unmarshal(reviewData, &review); err != nil {
		t.Fatalf("parse gate.round-1.review.json: %v", err)
	}
	if review.Intent.Source != "inferred" {
		t.Fatalf("gate.round-1.review.json intent.source = %q, want %q", review.Intent.Source, "inferred")
	}

	// The excerpt of the session sits under the jig home - the transcript's
	// marker line is in it - and its text reaches neither the store's
	// working tree nor any commit the gate pushed to the store's remote.
	// The accepted result is in that history, which shows the history check
	// can see files pushed under work/.
	excerpt, err := os.ReadFile(filepath.Join(home.IntentExcerptDir(env.home), ticket, "session-inferred.md"))
	if err != nil {
		t.Fatalf("read the excerpt under the jig home: %v", err)
	}
	if !strings.Contains(string(excerpt), intentExcerptMarker) {
		t.Fatalf("the excerpt does not hold the transcript's marker %q:\n%s", intentExcerptMarker, excerpt)
	}
	if hit := fileHolding(t, fx.StoreDir, intentExcerptMarker); hit != "" {
		t.Errorf("the store's working tree holds the excerpt's text in %s", hit)
	}
	history, err := gitx.Run(fx.StoreRemote, "log", "--all", "-p", "--no-color")
	if err != nil {
		t.Fatalf("git log in the store remote: %v", err)
	}
	if strings.Contains(history, intentExcerptMarker) {
		t.Error("the store remote's history holds the excerpt's text")
	}
	if !strings.Contains(history, "work/intent.result.json") {
		t.Error("the store remote's history holds no work/intent.result.json for the accepted result; the history check cannot see files pushed under work/")
	}
}

// fileHolding returns the path of the first file under dir (its .git
// directory aside) that holds text, or "".
func fileHolding(t *testing.T, dir, text string) string {
	t.Helper()
	var hit string
	err := filepath.WalkDir(dir, func(p string, de fs.DirEntry, err error) error {
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
		t.Fatalf("walk %s: %v", dir, err)
	}
	return hit
}
