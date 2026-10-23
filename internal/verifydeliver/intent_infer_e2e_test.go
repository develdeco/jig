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
	"github.com/develdeco/jig/internal/pool"
	"github.com/develdeco/jig/internal/project"
	"github.com/develdeco/jig/internal/session"
	"github.com/develdeco/jig/internal/store"
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

// gateLeaseDir is the path of the fixture ticket's gate lease. The intent
// summarizer's working directory is not the lease, so a test that plays a
// summarizer reaching the lease anyway names it here.
func gateLeaseDir(t *testing.T, fx *fixture.Fixture) string {
	t.Helper()
	dir, err := pool.Dir(fx.Home, "fixture-repo", fx.Ticket, pool.Gate)
	if err != nil {
		t.Fatalf("pool.Dir: %v", err)
	}
	return dir
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

// TestGateReviewerRoundHashesTheIntentItWasGiven: on a reviewer round with
// an intent already stated (the fixture's brief.md), nothing is inferred and
// the report - and report.yaml - carry that intent with the sha256 of the
// exact bytes of the file the reviewer was pointed at.
func TestGateReviewerRoundHashesTheIntentItWasGiven(t *testing.T) {
	t.Parallel()
	fx := fixture.Generate(t, fixture.Opts{ScenarioBranch: "inferred-intent", Home: t.TempDir()})
	d := newDeps(t, fx)
	driveBuild(t, fx, "rung-a")

	backend, err := session.New("fake", session.Options{ScenarioDir: fx.ScenarioDir})
	if err != nil {
		t.Fatalf("session.New: %v", err)
	}
	wrapped := stubBackend{run: func(sd session.Dispatch) error {
		if sd.Slice == "intent" {
			t.Error("the summarizer was dispatched for a ticket with a brief")
		}
		return backend.Run(sd)
	}}

	report, err := Gate(d, newReviewerGateSource(wrapped), GateOpts{Ticket: fx.Ticket})
	if err != nil {
		t.Fatalf("Gate: %v", err)
	}
	briefPath := filepath.Join(d.Store.TicketDir(fx.Ticket), "brief.md")
	want := sha256HexFile(t, briefPath)
	if report.Intent.Source != IntentSourceBrief || report.IntentSHA256 != want {
		t.Fatalf("report intent = %q sha256 %q, want %q with %q (brief.md's own bytes)", report.Intent.Source, report.IntentSHA256, IntentSourceBrief, want)
	}
	if rep := reportYAMLAt(t, d, fx.Ticket, report.Round); rep.Intent.Source != IntentSourceBrief || rep.Intent.SHA256 != want {
		t.Fatalf("report.yaml intent = %+v, want %q with sha256 %q", rep.Intent, IntentSourceBrief, want)
	}
}

// TestGateReportsTheIntentTheReviewerWasGiven: the round's report says what
// the reviewer was pointed at, not whatever brief.md or intent.md hold once
// the round is over. Here nothing states an intent when the round starts,
// and the reviewer's own session writes an intent.md during its dispatch (a
// screened shell is not a boundary): review.json and the report both say
// "none".
func TestGateReportsTheIntentTheReviewerWasGiven(t *testing.T) {
	t.Parallel()
	fx := fixture.Generate(t, fixture.Opts{ScenarioBranch: "inferred-intent", Home: t.TempDir()})
	d := newDeps(t, fx) // no mapped clone: nothing to infer from
	removeBrief(t, d, fx.Ticket)
	driveBuild(t, fx, "rung-a")

	backend, err := session.New("fake", session.Options{ScenarioDir: fx.ScenarioDir})
	if err != nil {
		t.Fatalf("session.New: %v", err)
	}
	wrapped := stubBackend{run: func(sd session.Dispatch) error {
		if sd.Slice == "gate" {
			if werr := d.Store.WriteIntent(fx.Ticket, store.Intent{Source: IntentSourceExplicit, Text: "written while the reviewer ran"}); werr != nil {
				t.Fatalf("write intent.md during the reviewer's dispatch: %v", werr)
			}
		}
		return backend.Run(sd)
	}}

	report, err := Gate(d, newReviewerGateSource(wrapped), GateOpts{Ticket: fx.Ticket})
	if err != nil {
		t.Fatalf("Gate: %v", err)
	}

	reviewData, err := os.ReadFile(reviewJSONPath(d.Store, fx.Ticket, 1))
	if err != nil {
		t.Fatalf("read review.json: %v", err)
	}
	var review ReviewRequest
	if err := json.Unmarshal(reviewData, &review); err != nil {
		t.Fatalf("parse review.json: %v", err)
	}
	if review.Intent.Source != IntentSourceNone {
		t.Fatalf("review.json intent.source = %q, want %q (nothing stated an intent when the reviewer was dispatched)", review.Intent.Source, IntentSourceNone)
	}
	if report.Intent.Source != IntentSourceNone || report.IntentSHA256 != "" {
		t.Fatalf("report intent = %q sha256 %q, want %q with no hash: the reviewer was given none", report.Intent.Source, report.IntentSHA256, IntentSourceNone)
	}
	if rep := reportYAMLAt(t, d, fx.Ticket, 1); rep.Intent.Source != IntentSourceNone || rep.Intent.SHA256 != "" {
		t.Fatalf("report.yaml intent = %+v, want %q with no hash", rep.Intent, IntentSourceNone)
	}
}

// TestGatePointsTheReviewerAtTheIntentExcerpt: a brief-less ticket gated
// through a real reviewer source (the fake session backend, not the scripted
// GateSource) is not given an intent: jig summarizes nothing and starts no
// summarizer. Its review.json names an intent_excerpt instead, the file under
// the jig home where the operator's own local Claude Code transcript that
// mentions the round's scope-diff files is excerpted. HOME and USERPROFILE
// are pointed at a temp dir holding only that synthetic transcript, so this
// test - like every other in this package - never reads a real home
// directory.
func TestGatePointsTheReviewerAtTheIntentExcerpt(t *testing.T) {
	t.Parallel()
	tmpHome := t.TempDir()

	fx := fixture.Generate(t, fixture.Opts{ScenarioBranch: "inferred-intent", Home: t.TempDir()})
	d := newDeps(t, fx)
	d.UserHome = tmpHome
	d.Machine = project.MachineProject{Clones: map[string]string{"fixture-repo": fx.RepoDir}}
	removeBrief(t, d, fx.Ticket)
	driveBuild(t, fx, "rung-a")

	// The fixture's own commits (its initial origin/main commit and every
	// fake-backend slice attempt) are all pinned to exactly
	// 2026-01-01T00:00:00Z (fixture.identityEnv / session.fakeGitEnv), so
	// merge-base and head resolve to that same instant: the discovery
	// window is [2025-12-29T00:00:00Z, 2026-01-01T01:00:00Z], deterministic
	// across any real wall-clock run.
	mentioned := []string{"alpha/alpha.go", "alpha/percent.go", "alpha/percent_test.go", "beta/beta.go", "beta/version.go"}
	sessionMTime := time.Date(2026, 1, 1, 0, 30, 0, 0, time.UTC)
	writeSyntheticClaudeSession(t, tmpHome, "session-inferred", fx.RepoDir, mentioned, sessionMTime)

	backend, err := session.New("fake", session.Options{ScenarioDir: fx.ScenarioDir})
	if err != nil {
		t.Fatalf("session.New: %v", err)
	}

	var (
		gotReview  ReviewRequest
		dispatches []string
	)
	wrapped := stubBackend{run: func(sd session.Dispatch) error {
		dispatches = append(dispatches, sd.Slice)
		if sd.Slice == "gate" {
			data, rerr := os.ReadFile(sd.SliceJSON)
			if rerr != nil {
				t.Fatalf("read review.json: %v", rerr)
			}
			if jerr := json.Unmarshal(data, &gotReview); jerr != nil {
				t.Fatalf("parse review.json: %v", jerr)
			}
			if !strings.Contains(sd.Prompt, "intent_excerpt") {
				t.Errorf("the reviewer's prompt does not tell it how to read an intent_excerpt:\n%s", sd.Prompt)
			}
		}
		return backend.Run(sd)
	}}

	report, err := Gate(d, newReviewerGateSource(wrapped), GateOpts{Ticket: fx.Ticket})
	if err != nil {
		t.Fatalf("Gate: %v", err)
	}
	if len(dispatches) != 1 || dispatches[0] != "gate" {
		t.Fatalf("sessions dispatched = %v, want only the reviewer: jig launches no summarizer", dispatches)
	}
	if report.Verdict != "clean" {
		t.Fatalf("report.Verdict = %q, want clean", report.Verdict)
	}

	want := filepath.Join(home.IntentExcerptDir(fx.Home), fx.Ticket, "session-inferred.md")
	if gotReview.IntentExcerpt != want {
		t.Fatalf("review.json intent_excerpt = %q, want %q", gotReview.IntentExcerpt, want)
	}
	// What the excerpt holds is what the author told their agent.
	data, err := os.ReadFile(gotReview.IntentExcerpt)
	if err != nil || !strings.Contains(string(data), "clamp percentages") {
		t.Fatalf("the excerpt %s = %q (%v), want the author's words", gotReview.IntentExcerpt, data, err)
	}
	// Still no intent: nothing states one, and jig records none of its own.
	if gotReview.Intent.Source != IntentSourceNone || report.Intent.Source != IntentSourceNone || report.IntentSHA256 != "" {
		t.Fatalf("intent = review %q, report %q (hash %q), want none: an excerpt is a hint, not an intent", gotReview.Intent.Source, report.Intent.Source, report.IntentSHA256)
	}
	if _, ok, err := d.Store.ReadIntent(fx.Ticket); err != nil || ok {
		t.Fatalf("ReadIntent: ok=%v err=%v, want no intent.md", ok, err)
	}

	// The excerpt is under the jig home and nowhere in the store.
	assertExcerptStaysOutOfTheStore(t, fx, "session-inferred")
}
