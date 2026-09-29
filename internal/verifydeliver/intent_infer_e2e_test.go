package verifydeliver

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
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

// TestGateInfersIntentThroughFakeBackend: a brief-less ticket
// gated through a real reviewer source (the fake session backend, not the
// scripted GateSource) infers its intent from a synthetic local Claude Code
// transcript that mentions the round's own scope-diff files. HOME and
// USERPROFILE are pointed at a temp dir holding only that synthetic
// transcript, so this test - like every other in this package - never
// reads a real home directory.
func TestGateInfersIntentThroughFakeBackend(t *testing.T) {
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

	var gotReview ReviewRequest
	captured := false
	// Wrap the fake backend to also capture review.json's own bytes for the
	// gate dispatch (Slice "gate"), proving the inferred intent reached the
	// reviewer's own request, not only GateReport.
	var summarizerDir string
	var demoIntent map[string]any
	wrapped := stubBackend{run: func(sd session.Dispatch) error {
		if sd.Slice == session.GateDemoSlice {
			demoIntent, _ = readJSONMap(t, sd.SliceJSON)["intent"].(map[string]any)
			return os.WriteFile(sd.ResultJSON, []byte(`{"media": [], "summary": "nothing to show"}`), 0o644)
		}
		if sd.Slice == "intent" {
			summarizerDir = sd.Worktree
		}
		if sd.Slice == "gate" {
			data, rerr := os.ReadFile(sd.SliceJSON)
			if rerr != nil {
				t.Fatalf("read review.json: %v", rerr)
			}
			if jerr := json.Unmarshal(data, &gotReview); jerr != nil {
				t.Fatalf("parse review.json: %v", jerr)
			}
			captured = true
		}
		return backend.Run(sd)
	}}

	report, err := Gate(d, NewReviewerGateSource(wrapped), GateOpts{Ticket: fx.Ticket})
	if err != nil {
		t.Fatalf("Gate: %v", err)
	}
	if !captured {
		t.Fatal("the gate reviewer dispatch never ran; review.json was never captured")
	}

	// The summarizer worked in a directory of its own under the jig home,
	// not the lease, and that directory is gone with the round.
	if summarizerDir == "" {
		t.Fatal("the intent summarizer was never dispatched")
	}
	if want := home.IntentScratchDir(fx.Home); filepath.Dir(summarizerDir) != want {
		t.Errorf("the summarizer's working directory = %q, want a directory directly under %q", summarizerDir, want)
	}
	if isWithin(gateLeaseDir(t, fx), summarizerDir) {
		t.Errorf("the summarizer's working directory %q is the gate lease or inside it", summarizerDir)
	}
	if _, err := os.Lstat(summarizerDir); !os.IsNotExist(err) {
		t.Errorf("the summarizer's working directory %q survived the round (lstat err=%v)", summarizerDir, err)
	}

	if report.Intent.Source != IntentSourceInferred {
		t.Fatalf("report.Intent.Source = %q, want %q (note: %s)", report.Intent.Source, IntentSourceInferred, report.IntentNote)
	}
	if report.Verdict != "clean" {
		t.Fatalf("report.Verdict = %q, want clean", report.Verdict)
	}

	if gotReview.Intent.Source != IntentSourceInferred {
		t.Fatalf("review.json intent.source = %q, want %q", gotReview.Intent.Source, IntentSourceInferred)
	}
	if gotReview.Intent.Path == "" {
		t.Fatal("review.json intent.path is empty, want the recorded intent.md path")
	}
	// The demo is handed the very intent the reviewer was.
	if demoIntent["source"] != gotReview.Intent.Source || demoIntent["path"] != gotReview.Intent.Path {
		t.Fatalf("demo.json intent = %v, want the reviewer's %+v", demoIntent, gotReview.Intent)
	}

	in, ok, err := d.Store.ReadIntent(fx.Ticket)
	if err != nil || !ok {
		t.Fatalf("ReadIntent: ok=%v err=%v", ok, err)
	}
	if in.Source != IntentSourceInferred {
		t.Fatalf("intent.md source = %q, want %q", in.Source, IntentSourceInferred)
	}
	if in.Agent == "" || in.Session == "" {
		t.Fatalf("intent.md agent/session = %q/%q, want both recorded", in.Agent, in.Session)
	}
	if in.Score < 0.5 {
		t.Fatalf("intent.md score = %v, want >= 0.5", in.Score)
	}
	if strings.TrimSpace(in.Text) == "" {
		t.Fatal("intent.md text is empty, want the summarizer's own summary")
	}

	// The report hashes the exact bytes of the file the reviewer was pointed
	// at.
	intentBytes, err := os.ReadFile(d.Store.IntentPath(fx.Ticket))
	if err != nil {
		t.Fatalf("read intent.md: %v", err)
	}
	sum := sha256.Sum256(intentBytes)
	if want := hex.EncodeToString(sum[:]); report.IntentSHA256 != want {
		t.Fatalf("report.IntentSHA256 = %q, want %q (intent.md's own bytes)", report.IntentSHA256, want)
	}

	// The excerpt is under the jig home and nowhere in the store; the accepted
	// result is in the store's history, which shows the history check sees
	// files pushed under work/.
	assertExcerptStaysOutOfTheStore(t, fx, "session-inferred")
	if history := storeRemoteHistory(t, fx); !strings.Contains(history, "work/intent.result.json") {
		t.Fatal("the store remote's history holds no work/intent.result.json for the accepted result; the history check cannot see files pushed under work/")
	}
}

// TestGateRestoresLeaseWhenIntentSummarizerLeavesItDirty: a summarizer
// that reaches the lease (its working directory is a scratch directory, so
// through the lease's own path) and leaves it dirty must not
// carry that dirt into the reviewer's own dispatch - inferIntent restores
// the lease itself and keeps failing open, the same way every other
// inference failure does, rather than letting the reviewer's own
// read-only guard trip over edits that were never its own.
func TestGateRestoresLeaseWhenIntentSummarizerLeavesItDirty(t *testing.T) {
	t.Parallel()
	tmpHome := t.TempDir()

	fx := fixture.Generate(t, fixture.Opts{ScenarioBranch: "inferred-intent", Home: t.TempDir()})
	d := newDeps(t, fx)
	d.UserHome = tmpHome
	d.Machine = project.MachineProject{Clones: map[string]string{"fixture-repo": fx.RepoDir}}
	removeBrief(t, d, fx.Ticket)
	driveBuild(t, fx, "rung-a")

	mentioned := []string{"alpha/alpha.go", "alpha/percent.go", "alpha/percent_test.go", "beta/beta.go", "beta/version.go"}
	sessionMTime := time.Date(2026, 1, 1, 0, 30, 0, 0, time.UTC)
	writeSyntheticClaudeSession(t, tmpHome, "session-inferred", fx.RepoDir, mentioned, sessionMTime)

	backend, err := session.New("fake", session.Options{ScenarioDir: fx.ScenarioDir})
	if err != nil {
		t.Fatalf("session.New: %v", err)
	}

	// Simulate a summarizer dispatch that (against its own prompt) reaches
	// the lease and edits a tracked file, the shape that would otherwise
	// fail the round with REVIEW_INVALID at the reviewer's own read-only
	// guard.
	dirtied := false
	wrapped := stubBackend{run: func(sd session.Dispatch) error {
		if sd.Slice == "intent" {
			p := filepath.Join(gateLeaseDir(t, fx), "alpha", "alpha.go")
			data, rerr := os.ReadFile(p)
			if rerr != nil {
				t.Fatalf("read alpha/alpha.go in lease: %v", rerr)
			}
			if werr := os.WriteFile(p, append(data, []byte("\n// stray summarizer edit\n")...), 0o644); werr != nil {
				t.Fatalf("dirty alpha/alpha.go in lease: %v", werr)
			}
			dirtied = true
		}
		return backend.Run(sd)
	}}

	report, err := Gate(d, NewReviewerGateSource(wrapped), GateOpts{Ticket: fx.Ticket})
	if err != nil {
		t.Fatalf("Gate: %v", err)
	}
	if !dirtied {
		t.Fatal("the intent summarizer dispatch never ran; nothing was dirtied")
	}

	// Failed open, not a REVIEW_INVALID round failure: the round must
	// still complete clean, with intent left at "none" and a reason
	// naming what happened.
	if report.Verdict != "clean" {
		t.Fatalf("report.Verdict = %q, want clean", report.Verdict)
	}
	if report.Intent.Source != IntentSourceNone {
		t.Fatalf("report.Intent.Source = %q, want %q (summarizer dirtied the lease)", report.Intent.Source, IntentSourceNone)
	}
	if !strings.Contains(report.IntentNote, "changed the gate lease") {
		t.Fatalf("report.IntentNote = %q, want it to say the summarizer changed the gate lease", report.IntentNote)
	}

	// The lease itself must come out of the round pristine: the stray
	// edit restored, nothing left dirty for the next round (or the next
	// gate command) to trip over.
	leaseDir, err := pool.Dir(fx.Home, "fixture-repo", fx.Ticket, pool.Gate)
	if err != nil {
		t.Fatalf("pool.Dir: %v", err)
	}
	status, err := gitx.Run(leaseDir, "status", "--porcelain")
	if err != nil {
		t.Fatalf("git status: %v", err)
	}
	if status != "" {
		t.Fatalf("gate lease status after Gate = %q, want clean (restored)", status)
	}
}

// TestGateRestoresLeaseWhenIntentSummarizerDirtiesThenErrors:
// TestGateRestoresLeaseWhenIntentSummarizerLeavesItDirty above covers a
// summarizer dispatch that dirties the lease and still returns success;
// this covers the far more common real shape of a fail-open dispatch (a
// headless timeout, CLAUDE_NOT_FOUND, a max-turns stop) - one that dirties
// the lease and then returns an error. The lease must be restored whatever
// the dispatch returned, or the round's own reviewer dispatch inherits a
// dirty lease and is blamed for a guard failure that was never its own.
func TestGateRestoresLeaseWhenIntentSummarizerDirtiesThenErrors(t *testing.T) {
	t.Parallel()
	tmpHome := t.TempDir()

	fx := fixture.Generate(t, fixture.Opts{ScenarioBranch: "inferred-intent", Home: t.TempDir()})
	d := newDeps(t, fx)
	d.UserHome = tmpHome
	d.Machine = project.MachineProject{Clones: map[string]string{"fixture-repo": fx.RepoDir}}
	removeBrief(t, d, fx.Ticket)
	driveBuild(t, fx, "rung-a")

	mentioned := []string{"alpha/alpha.go", "alpha/percent.go", "alpha/percent_test.go", "beta/beta.go", "beta/version.go"}
	sessionMTime := time.Date(2026, 1, 1, 0, 30, 0, 0, time.UTC)
	writeSyntheticClaudeSession(t, tmpHome, "session-inferred", fx.RepoDir, mentioned, sessionMTime)

	backend, err := session.New("fake", session.Options{ScenarioDir: fx.ScenarioDir})
	if err != nil {
		t.Fatalf("session.New: %v", err)
	}

	dirtied := false
	wrapped := stubBackend{run: func(sd session.Dispatch) error {
		if sd.Slice == "intent" {
			p := filepath.Join(gateLeaseDir(t, fx), "alpha", "alpha.go")
			data, rerr := os.ReadFile(p)
			if rerr != nil {
				t.Fatalf("read alpha/alpha.go in lease: %v", rerr)
			}
			if werr := os.WriteFile(p, append(data, []byte("\n// stray summarizer edit\n")...), 0o644); werr != nil {
				t.Fatalf("dirty alpha/alpha.go in lease: %v", werr)
			}
			dirtied = true
			return errors.New("session timed out")
		}
		return backend.Run(sd)
	}}

	report, err := Gate(d, NewReviewerGateSource(wrapped), GateOpts{Ticket: fx.Ticket})
	if err != nil {
		t.Fatalf("Gate: %v", err)
	}
	if !dirtied {
		t.Fatal("the intent summarizer dispatch never ran; nothing was dirtied")
	}

	// Failed open on the dispatch error itself, not a REVIEW_INVALID
	// round failure blamed on the reviewer.
	if report.Verdict != "clean" {
		t.Fatalf("report.Verdict = %q, want clean", report.Verdict)
	}
	if report.Intent.Source != IntentSourceNone {
		t.Fatalf("report.Intent.Source = %q, want %q (summarizer dispatch failed)", report.Intent.Source, IntentSourceNone)
	}
	if !strings.Contains(report.IntentNote, "dispatch failed") {
		t.Fatalf("report.IntentNote = %q, want it to say the summarizer dispatch failed, not a lease-changed reason meant for a different case", report.IntentNote)
	}

	leaseDir, err := pool.Dir(fx.Home, "fixture-repo", fx.Ticket, pool.Gate)
	if err != nil {
		t.Fatalf("pool.Dir: %v", err)
	}
	status, err := gitx.Run(leaseDir, "status", "--porcelain")
	if err != nil {
		t.Fatalf("git status: %v", err)
	}
	if status != "" {
		t.Fatalf("gate lease status after Gate = %q, want clean (restored despite the dispatch error)", status)
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

// TestGateDoesNotPushAnUnacceptedSummarizerResult: a summarizer that writes
// the excerpt's own text as its result and also edits the lease is not
// accepted, and its result file must not survive the round for the store
// push to commit: not in the store's working tree, and not in anything
// pushed to its remote.
func TestGateDoesNotPushAnUnacceptedSummarizerResult(t *testing.T) {
	t.Parallel()
	tmpHome := t.TempDir()

	fx := fixture.Generate(t, fixture.Opts{ScenarioBranch: "inferred-intent", Home: t.TempDir()})
	d := newDeps(t, fx)
	d.UserHome = tmpHome
	d.Machine = project.MachineProject{Clones: map[string]string{"fixture-repo": fx.RepoDir}}
	removeBrief(t, d, fx.Ticket)
	driveBuild(t, fx, "rung-a")

	mentioned := []string{"alpha/alpha.go", "alpha/percent.go", "alpha/percent_test.go", "beta/beta.go", "beta/version.go"}
	writeSyntheticClaudeSession(t, tmpHome, "session-inferred", fx.RepoDir, mentioned, time.Date(2026, 1, 1, 0, 30, 0, 0, time.UTC))

	backend, err := session.New("fake", session.Options{ScenarioDir: fx.ScenarioDir})
	if err != nil {
		t.Fatalf("session.New: %v", err)
	}

	misbehaved := false
	wrapped := stubBackend{run: func(sd session.Dispatch) error {
		if sd.Slice != "intent" {
			return backend.Run(sd)
		}
		var req IntentInferRequest
		reqData, rerr := os.ReadFile(sd.SliceJSON)
		if rerr != nil {
			t.Fatalf("read intent.json: %v", rerr)
		}
		if jerr := json.Unmarshal(reqData, &req); jerr != nil {
			t.Fatalf("parse intent.json: %v", jerr)
		}
		excerpt, rerr := os.ReadFile(req.ExcerptPath)
		if rerr != nil {
			t.Fatalf("read the excerpt the request names: %v", rerr)
		}
		result, merr := json.Marshal(IntentInferResult{Summary: string(excerpt)})
		if merr != nil {
			t.Fatal(merr)
		}
		if werr := os.WriteFile(sd.ResultJSON, result, 0o644); werr != nil {
			t.Fatalf("write the result: %v", werr)
		}
		p := filepath.Join(gateLeaseDir(t, fx), "alpha", "alpha.go")
		data, rerr := os.ReadFile(p)
		if rerr != nil {
			t.Fatalf("read alpha/alpha.go in lease: %v", rerr)
		}
		if werr := os.WriteFile(p, append(data, []byte("\n// stray summarizer edit\n")...), 0o644); werr != nil {
			t.Fatalf("dirty alpha/alpha.go in lease: %v", werr)
		}
		misbehaved = true
		return nil
	}}

	report, err := Gate(d, NewReviewerGateSource(wrapped), GateOpts{Ticket: fx.Ticket})
	if err != nil {
		t.Fatalf("Gate: %v", err)
	}
	if !misbehaved {
		t.Fatal("the intent summarizer dispatch never ran")
	}
	if report.Intent.Source != IntentSourceNone || !strings.Contains(report.IntentNote, "changed the gate lease") {
		t.Fatalf("report intent = %q note %q, want none: the summarizer changed the lease", report.Intent.Source, report.IntentNote)
	}

	if _, err := os.Stat(filepath.Join(fx.StoreDir, fx.Ticket, "work", "intent.result.json")); !os.IsNotExist(err) {
		t.Errorf("work/intent.result.json exists in the store after the round (stat err=%v), want an unaccepted result removed", err)
	}
	history := storeRemoteHistory(t, fx)
	if strings.Contains(history, "intent.result.json") {
		t.Error("the store remote's history holds an intent.result.json, want none for a result that was never accepted")
	}
	assertExcerptStaysOutOfTheStore(t, fx, "session-inferred")
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

	report, err := Gate(d, NewReviewerGateSource(wrapped), GateOpts{Ticket: fx.Ticket})
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

	report, err := Gate(d, NewReviewerGateSource(wrapped), GateOpts{Ticket: fx.Ticket})
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
