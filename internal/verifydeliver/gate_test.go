package verifydeliver

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/pool"
	"github.com/develdeco/jig/internal/session"
	"github.com/develdeco/jig/internal/store"
)

// alwaysCleanSource is a GateSource stub that always reports a clean
// round, for tests that only care about the frontier/model/oracle path.
type alwaysCleanSource struct{}

func (alwaysCleanSource) Round(RoundInput) (Round, bool, error) { return Round{}, false, nil }

func TestGateRound1FixSlice(t *testing.T) {
	t.Setenv("JIG_HOME", t.TempDir())
	fx := fixture.Generate(t, fixture.Opts{})
	driveBuild(t, fx, "rung-a")

	d := newDeps(t, fx)
	src := NewFakeGateSource(fx.ScenarioDir)
	report, err := Gate(d, src, GateOpts{Ticket: fx.Ticket})
	if err != nil {
		t.Fatalf("Gate: %v", err)
	}
	if report.Round != 1 {
		t.Fatalf("Round = %d, want 1", report.Round)
	}
	if report.Verdict != "fix-slices" {
		t.Fatalf("Verdict = %q, want fix-slices", report.Verdict)
	}

	roundDir := filepath.Join(d.Store.TicketDir(fx.Ticket), "gate", "round-1")
	for _, name := range []string{"findings.md", "report.yaml", "diff-changelog.md"} {
		if _, err := os.Stat(filepath.Join(roundDir, name)); err != nil {
			t.Fatalf("missing %s: %v", name, err)
		}
	}
	if _, err := os.Stat(filepath.Join(d.Store.TicketDir(fx.Ticket), "evidence", "round-1", "oracle-output.txt")); err != nil {
		t.Fatalf("missing receipt: %v", err)
	}

	slices, err := d.Store.ReadSlices(fx.Ticket)
	if err != nil {
		t.Fatalf("ReadSlices: %v", err)
	}
	var fix1 *store.Slice
	for i := range slices {
		if slices[i].ID == "fix-1" {
			fix1 = &slices[i]
		}
	}
	if fix1 == nil {
		t.Fatal("fix-1 was not appended to slices.yaml")
	}
	if fix1.FromGate != 1 {
		t.Fatalf("fix-1.FromGate = %d, want 1", fix1.FromGate)
	}
	st, err := d.Store.ReadSliceState(fx.Ticket, "fix-1")
	if err != nil {
		t.Fatalf("ReadSliceState fix-1: %v", err)
	}
	if st.State != "queued" {
		t.Fatalf("fix-1 state = %q, want queued", st.State)
	}

	lines, err := journal.Read(d.Store, fx.Ticket)
	if err != nil {
		t.Fatalf("journal.Read: %v", err)
	}
	want := map[string]bool{"gate-open": false, "gate-round": false, "fix-slice": false}
	for _, l := range lines {
		if _, ok := want[l.Event]; ok {
			want[l.Event] = true
		}
	}
	for event, seen := range want {
		if !seen {
			t.Fatalf("journal missing event %q", event)
		}
	}
}

func TestGateCleanRound2(t *testing.T) {
	t.Setenv("JIG_HOME", t.TempDir())
	fx := fixture.Generate(t, fixture.Opts{})
	driveBuild(t, fx, "rung-a")

	d := newDeps(t, fx)
	src := NewFakeGateSource(fx.ScenarioDir)
	if _, err := Gate(d, src, GateOpts{Ticket: fx.Ticket}); err != nil {
		t.Fatalf("Gate round 1: %v", err)
	}

	round1Dir := filepath.Join(d.Store.TicketDir(fx.Ticket), "gate", "round-1")
	before := readDirBytes(t, round1Dir)

	driveFix1(t, fx, "rung-a")

	report, err := Gate(d, src, GateOpts{Ticket: fx.Ticket})
	if err != nil {
		t.Fatalf("Gate round 2: %v", err)
	}
	if report.Round != 2 {
		t.Fatalf("Round = %d, want 2", report.Round)
	}
	if report.Verdict != "clean" {
		t.Fatalf("Verdict = %q, want clean", report.Verdict)
	}

	after := readDirBytes(t, round1Dir)
	if len(before) != len(after) {
		t.Fatalf("round-1 file count changed: %d -> %d", len(before), len(after))
	}
	for name, data := range before {
		if string(after[name]) != string(data) {
			t.Fatalf("round-1/%s bytes changed after round 2", name)
		}
	}

	lines, err := journal.Read(d.Store, fx.Ticket)
	if err != nil {
		t.Fatalf("journal.Read: %v", err)
	}
	sawClean := false
	for _, l := range lines {
		if l.Event == "gate-clean" && l.Attempt == 2 {
			sawClean = true
		}
	}
	if !sawClean {
		t.Fatal("journal missing gate-clean for round 2")
	}
}

// readDirBytes reads every regular file directly under dir into a map
// keyed by base name, for byte-level before/after comparison.
func readDirBytes(t *testing.T, dir string) map[string][]byte {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir %s: %v", dir, err)
	}
	out := map[string][]byte{}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		data, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			t.Fatalf("read %s: %v", e.Name(), err)
		}
		out[e.Name()] = data
	}
	return out
}

func TestGateModelDisjoint(t *testing.T) {
	t.Setenv("JIG_HOME", t.TempDir())
	fx := fixture.Generate(t, fixture.Opts{})
	driveBuild(t, fx, "rung-a")

	d := newDeps(t, fx)
	report, err := Gate(d, alwaysCleanSource{}, GateOpts{Ticket: fx.Ticket})
	if err != nil {
		t.Fatalf("Gate: %v", err)
	}
	if report.Model != "rung-b" {
		t.Fatalf("Model = %q, want rung-b (builders used rung-a)", report.Model)
	}
}

func TestGateEarlyAndFrontierGuard(t *testing.T) {
	t.Setenv("JIG_HOME", t.TempDir())
	fx := fixture.Generate(t, fixture.Opts{})

	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	dir := buildLeaseDir(t, fx)
	driveAttempt(t, st, fx, dir, "rung-a", "a", 1)
	// b, c, d are left queued: the frontier is not empty.

	d2 := newDeps(t, fx)
	_, err = Gate(d2, alwaysCleanSource{}, GateOpts{Ticket: fx.Ticket})
	var ae *axi.Error
	if !errors.As(err, &ae) || ae.Code != "GATE_NOT_GREEN" {
		t.Fatalf("Gate without --early: err = %v, want *axi.Error GATE_NOT_GREEN", err)
	}

	report, err := Gate(d2, alwaysCleanSource{}, GateOpts{Ticket: fx.Ticket, Early: true})
	if err != nil {
		t.Fatalf("Gate --early: %v", err)
	}
	if report.Verdict != "clean" {
		t.Fatalf("Verdict = %q, want clean", report.Verdict)
	}
}

// duplicateFixSliceSource is a GateSource stub whose round-1 fix slice
// reuses an id that already exists in the ticket's slices.yaml.
type duplicateFixSliceSource struct{}

func (duplicateFixSliceSource) Round(in RoundInput) (Round, bool, error) {
	if in.Round == 1 {
		return Round{
			FindingsMD: "duplicate id",
			FixSlices:  []store.Slice{{ID: "a", Workspace: "root", Goal: "dup", Oracle: "test"}},
		}, true, nil
	}
	return Round{}, false, nil
}

// TestGateSurfacesDuplicateFixSliceID checks that Gate does not swallow
// store.AppendSlices's duplicate-id refusal when a round's fix slice reuses
// an id already present in slices.yaml.
func TestGateSurfacesDuplicateFixSliceID(t *testing.T) {
	t.Setenv("JIG_HOME", t.TempDir())
	fx := fixture.Generate(t, fixture.Opts{})
	driveBuild(t, fx, "rung-a")

	d := newDeps(t, fx)
	_, err := Gate(d, duplicateFixSliceSource{}, GateOpts{Ticket: fx.Ticket})
	var ae *axi.Error
	if !errors.As(err, &ae) || ae.Code != "SLICE_ID_DUPLICATE" {
		t.Fatalf("err = %v, want *axi.Error SLICE_ID_DUPLICATE", err)
	}
}

// TestGateRefusesStalledSlice checks that a slice left "stalled" (not
// merely "queued") also blocks gate without --early: any non-green state is
// an incomplete delivery.
func TestGateRefusesStalledSlice(t *testing.T) {
	t.Setenv("JIG_HOME", t.TempDir())
	fx := fixture.Generate(t, fixture.Opts{})
	driveBuild(t, fx, "rung-a")

	d := newDeps(t, fx)
	if err := d.Store.WriteSliceState(fx.Ticket, "d", store.SliceState{State: "stalled", Reason: "stall"}); err != nil {
		t.Fatalf("write slice state d: %v", err)
	}
	if err := d.Store.Push(fx.Ticket + ": slice d stalled"); err != nil {
		t.Fatalf("push after stalling d: %v", err)
	}

	_, err := Gate(d, alwaysCleanSource{}, GateOpts{Ticket: fx.Ticket})
	var ae *axi.Error
	if !errors.As(err, &ae) || ae.Code != "GATE_NOT_GREEN" {
		t.Fatalf("Gate with a stalled slice: err = %v, want *axi.Error GATE_NOT_GREEN", err)
	}
}

// TestGateRefusesQueuedFixSlice checks that a queued fix slice (FromGate !=
// 0, appended by an earlier round) also blocks a later gate round: it used
// to be exempted from the frontier check entirely.
func TestGateRefusesQueuedFixSlice(t *testing.T) {
	t.Setenv("JIG_HOME", t.TempDir())
	fx := fixture.Generate(t, fixture.Opts{})
	driveBuild(t, fx, "rung-a")

	d := newDeps(t, fx)
	if err := d.Store.AppendSlices(fx.Ticket, []store.Slice{{ID: "fix-x", Workspace: "root", Goal: "fix", Oracle: "test", FromGate: 1}}); err != nil {
		t.Fatalf("AppendSlices: %v", err)
	}
	// fix-x defaults to the zero-value "queued" state (no state file
	// written yet), same as a fix slice fresh off a gate round.
	if err := d.Store.Push(fx.Ticket + ": append fix-x"); err != nil {
		t.Fatalf("push after appending fix-x: %v", err)
	}

	_, err := Gate(d, alwaysCleanSource{}, GateOpts{Ticket: fx.Ticket})
	var ae *axi.Error
	if !errors.As(err, &ae) || ae.Code != "GATE_NOT_GREEN" {
		t.Fatalf("Gate with a queued fix slice: err = %v, want *axi.Error GATE_NOT_GREEN", err)
	}
}

// TestGateBranchCopiesBriefDoc checks that `--branch --doc <path>` actually
// wires the spec-axis input swap: the doc's content lands in this round's
// gate/round-<n>/spec-input.md rather than being silently accepted and
// dropped.
func TestGateBranchCopiesBriefDoc(t *testing.T) {
	t.Setenv("JIG_HOME", t.TempDir())
	fx := fixture.Generate(t, fixture.Opts{})
	// --branch validates a hand-written branch's oracles for real, so it
	// needs one where they actually pass: drive the ticket's own branch to
	// green, then push it to origin so the (separate) gate lease this call
	// acquires can see it as "jig/<ticket>", same as a real hand-written
	// branch pushed for review.
	driveBuild(t, fx, "rung-a")
	buildDir := buildLeaseDir(t, fx)
	if _, err := gitx.Run(buildDir, "push", "origin", ticketBranch(fx.Ticket)); err != nil {
		t.Fatalf("push build branch: %v", err)
	}

	briefDoc := filepath.Join(t.TempDir(), "brief.md")
	if err := os.WriteFile(briefDoc, []byte("# spec axis input\n"), 0o644); err != nil {
		t.Fatalf("write brief doc: %v", err)
	}

	d := newDeps(t, fx)
	report, err := Gate(d, alwaysCleanSource{}, GateOpts{Ticket: fx.Ticket, Branch: ticketBranch(fx.Ticket), BriefDoc: briefDoc, Early: true})
	if err != nil {
		t.Fatalf("Gate --branch --doc: %v", err)
	}

	specInput := filepath.Join(d.Store.TicketDir(fx.Ticket), "gate", fmt.Sprintf("round-%d", report.Round), "spec-input.md")
	data, err := os.ReadFile(specInput)
	if err != nil {
		t.Fatalf("read spec-input.md: %v", err)
	}
	if string(data) != "# spec axis input\n" {
		t.Fatalf("spec-input.md content = %q, want brief doc content", data)
	}
}

// TestGateBranchMissingDocErrors checks that a --doc path that cannot be
// read is refused with BRIEF_DOC_MISSING rather than silently ignored.
func TestGateBranchMissingDocErrors(t *testing.T) {
	t.Setenv("JIG_HOME", t.TempDir())
	fx := fixture.Generate(t, fixture.Opts{})

	d := newDeps(t, fx)
	_, err := Gate(d, alwaysCleanSource{}, GateOpts{
		Ticket:   fx.Ticket,
		Branch:   "main",
		BriefDoc: filepath.Join(t.TempDir(), "missing.md"),
		Early:    true,
	})
	var ae *axi.Error
	if !errors.As(err, &ae) || ae.Code != "BRIEF_DOC_MISSING" {
		t.Fatalf("err = %v, want *axi.Error BRIEF_DOC_MISSING", err)
	}
}

// TestGateBranchModeBriefPathIsTheDocItself guards a regression: in
// --branch mode with --doc, review.json's brief_path must be the absolute
// path of the --doc file itself, not the ticket's own brief.md and not
// this round's own gate/round-<n>/spec-input.md (PR #8's recorded
// decision: pointing at spec-input.md would leave a partial round dir on
// disk if the reviewer then failed, since that file is written only after
// the round succeeds).
func TestGateBranchModeBriefPathIsTheDocItself(t *testing.T) {
	t.Setenv("JIG_HOME", t.TempDir())
	fx := fixture.Generate(t, fixture.Opts{})
	driveBuild(t, fx, "rung-a")
	buildDir := buildLeaseDir(t, fx)
	if _, err := gitx.Run(buildDir, "push", "origin", ticketBranch(fx.Ticket)); err != nil {
		t.Fatalf("push build branch: %v", err)
	}

	briefDoc := filepath.Join(t.TempDir(), "spec-axis-input.md")
	if err := os.WriteFile(briefDoc, []byte("# spec axis input\n"), 0o644); err != nil {
		t.Fatalf("write brief doc: %v", err)
	}
	wantPath, err := filepath.Abs(briefDoc)
	if err != nil {
		t.Fatalf("abs briefDoc: %v", err)
	}

	d := newDeps(t, fx)
	var gotBriefPath string
	backend := stubBackend{run: func(sd session.Dispatch) error {
		data, err := os.ReadFile(sd.SliceJSON)
		if err != nil {
			t.Fatalf("read review.json: %v", err)
		}
		var req ReviewRequest
		if err := json.Unmarshal(data, &req); err != nil {
			t.Fatalf("parse review.json: %v", err)
		}
		gotBriefPath = req.BriefPath
		writeMustReviewResult(t, sd)
		return nil
	}}

	_, err = Gate(d, NewReviewerGateSource(backend), GateOpts{
		Ticket: fx.Ticket, Branch: ticketBranch(fx.Ticket), BriefDoc: briefDoc, Early: true,
	})
	if err != nil {
		t.Fatalf("Gate --branch --doc: %v", err)
	}
	if gotBriefPath != wantPath {
		t.Fatalf("review.json brief_path = %q, want the --doc file itself: %q", gotBriefPath, wantPath)
	}
}

// TestGateWipesLeftoverLeaseDirtBeforeOracles reproduces NM2 scenario A: a
// reviewer that outlived a killed jig (no signal handler reaches a future
// reviewer source's own defer restore) can leave an untracked failing test
// file and a tracked edit sitting in the gate lease. The next `jig gate`
// must not have its oracles fail or get skewed by that leftover dirt - Gate
// itself restores the lease to a pristine head before any oracle runs.
func TestGateWipesLeftoverLeaseDirtBeforeOracles(t *testing.T) {
	t.Setenv("JIG_HOME", t.TempDir())
	fx := fixture.Generate(t, fixture.Opts{})
	driveBuild(t, fx, "rung-a")

	d := newDeps(t, fx)
	if _, err := Gate(d, alwaysCleanSource{}, GateOpts{Ticket: fx.Ticket}); err != nil {
		t.Fatalf("Gate round 1: %v", err)
	}

	gateLease, err := pool.Acquire("fixture-repo", fx.RepoRemote, "main", ticketBranch(fx.Ticket), fx.Ticket, pool.Gate)
	if err != nil {
		t.Fatalf("reacquire gate lease: %v", err)
	}
	// An untracked failing test file, as a reviewer's own repro would leave
	// behind: the fixture's "test" oracle runs `go test ./alpha/...`.
	reproPath := filepath.Join(gateLease.Dir, "alpha", "zz_repro_test.go")
	repro := "package alpha\n\nimport \"testing\"\n\nfunc TestZZRepro(t *testing.T) { t.Fatal(\"boom\") }\n"
	if err := os.WriteFile(reproPath, []byte(repro), 0o644); err != nil {
		t.Fatalf("write leftover repro test: %v", err)
	}
	// A tracked edit left uncommitted, as an oracle rewrite or a killed
	// reviewer's own edit would leave behind.
	trackedPath := filepath.Join(gateLease.Dir, "alpha", "alpha.go")
	orig, err := os.ReadFile(trackedPath)
	if err != nil {
		t.Fatalf("read tracked file: %v", err)
	}
	if err := os.WriteFile(trackedPath, append(orig, []byte("\n// leftover dirt\n")...), 0o644); err != nil {
		t.Fatalf("dirty tracked file: %v", err)
	}

	report2, err := Gate(d, alwaysCleanSource{}, GateOpts{Ticket: fx.Ticket})
	if err != nil {
		t.Fatalf("Gate round 2 should succeed despite leftover lease dirt: %v", err)
	}
	if report2.Round != 2 || report2.Verdict != "clean" {
		t.Fatalf("report2 = %+v, want round 2 clean", report2)
	}

	if _, err := os.Stat(reproPath); !os.IsNotExist(err) {
		t.Fatalf("leftover untracked repro file still present after Gate: err = %v", err)
	}
	status, err := gitx.Run(gateLease.Dir, "status", "--porcelain")
	if err != nil {
		t.Fatalf("git status: %v", err)
	}
	if status != "" {
		t.Fatalf("gate lease left dirty after Gate: %q", status)
	}
}

// TestGateRecoversLeftoverTrackedDirtOnceBranchAdvances reproduces NM5, a
// residual of NM2: leftover tracked dirt in the gate lease (a reviewer that
// outlived a killed jig, or an oracle rewrite from an earlier attempt) does
// not get in the way of an oracle run by itself, but once the ticket branch
// later advances past the same file, the checkout that runs before Gate's
// post-acquire restore (fetchTicketBranchFromBuildLease's own final `git
// checkout <branch>`) refuses with "local changes ... would be
// overwritten", and Gate would return before that restore ever runs -
// leaving the lease detached and still dirty, so pool.Acquire's own
// checkout would fail the exact same way on every later attempt. Gate
// restores an existing lease pristine before Acquire ever touches it, so
// the very next gate succeeds, and a lease already left wedged by an
// earlier failed attempt (detached, still dirty, its local branch ref
// already fast-forwarded past the dirty file) recovers too.
func TestGateRecoversLeftoverTrackedDirtOnceBranchAdvances(t *testing.T) {
	t.Setenv("JIG_HOME", t.TempDir())
	fx := fixture.Generate(t, fixture.Opts{})
	driveBuild(t, fx, "rung-a")

	d := newDeps(t, fx)
	if _, err := Gate(d, alwaysCleanSource{}, GateOpts{Ticket: fx.Ticket}); err != nil {
		t.Fatalf("Gate round 1: %v", err)
	}

	gateLease, err := pool.Acquire("fixture-repo", fx.RepoRemote, "main", ticketBranch(fx.Ticket), fx.Ticket, pool.Gate)
	if err != nil {
		t.Fatalf("reacquire gate lease: %v", err)
	}
	trackedPath := filepath.Join(gateLease.Dir, "alpha", "alpha.go")
	orig, err := os.ReadFile(trackedPath)
	if err != nil {
		t.Fatalf("read tracked file: %v", err)
	}
	dirty := func(note string) {
		t.Helper()
		edited := append(append([]byte{}, orig...), []byte("\n// "+note+"\n")...)
		if err := os.WriteFile(trackedPath, edited, 0o644); err != nil {
			t.Fatalf("dirty tracked file: %v", err)
		}
	}
	dirty("leftover reviewer edit")

	buildDir := buildLeaseDir(t, fx)
	bp := filepath.Join(buildDir, "alpha", "alpha.go")
	advance := func(note string) {
		t.Helper()
		bdata, err := os.ReadFile(bp)
		if err != nil {
			t.Fatalf("read build alpha.go: %v", err)
		}
		if err := os.WriteFile(bp, append(bdata, []byte("\n// "+note+"\n")...), 0o644); err != nil {
			t.Fatalf("write build alpha.go: %v", err)
		}
		if _, err := gitx.Run(buildDir, "add", "-A"); err != nil {
			t.Fatalf("git add: %v", err)
		}
		if _, err := gitx.RunEnv(buildDir, buildGitEnv, "commit", "-m", note); err != nil {
			t.Fatalf("commit: %v", err)
		}
	}
	// Advance jig/<ticket> in the build lease with a commit touching the
	// same file, as a later slice build (e.g. after an --early gate) would.
	advance("next slice")

	report2, err := Gate(d, alwaysCleanSource{}, GateOpts{Ticket: fx.Ticket})
	if err != nil {
		t.Fatalf("Gate round 2 should succeed on the first attempt despite the leftover tracked dirt and the advanced branch: %v", err)
	}
	if report2.Round != 2 || report2.Verdict != "clean" {
		t.Fatalf("report2 = %+v, want round 2 clean", report2)
	}
	status, err := gitx.Run(gateLease.Dir, "status", "--porcelain")
	if err != nil {
		t.Fatalf("git status: %v", err)
	}
	if status != "" {
		t.Fatalf("gate lease left dirty after round 2: %q", status)
	}

	// Now forge exactly the state a pre-fix failed attempt would have left
	// behind: advance the branch again, dirty the lease again, then call
	// the real fetchTicketBranchFromBuildLease directly - its fetch force-
	// updates the local branch ref to the new tip and succeeds, but its own
	// final checkout back onto that branch fails on the dirty file, so it
	// returns with the lease detached at the old commit and still dirty.
	advance("third slice")
	dirty("leftover from a killed attempt")
	if err := fetchTicketBranchFromBuildLease(gateLease.Dir, "fixture-repo", fx.Ticket); err == nil {
		t.Fatal("test setup: fetchTicketBranchFromBuildLease should still fail on the dirty file before the pre-Acquire restore runs")
	}

	report3, err := Gate(d, alwaysCleanSource{}, GateOpts{Ticket: fx.Ticket})
	if err != nil {
		t.Fatalf("Gate round 3 should recover a lease left detached and dirty by an earlier failed attempt: %v", err)
	}
	if report3.Round != 3 || report3.Verdict != "clean" {
		t.Fatalf("report3 = %+v, want round 3 clean", report3)
	}
	head, err := gitx.RevParse(gateLease.Dir, "HEAD")
	if err != nil {
		t.Fatalf("resolve HEAD: %v", err)
	}
	branchHead, err := gitx.RevParse(gateLease.Dir, ticketBranch(fx.Ticket))
	if err != nil {
		t.Fatalf("resolve branch HEAD: %v", err)
	}
	if head != branchHead {
		t.Fatalf("gate lease HEAD %s is not the ticket branch's HEAD %s: still detached", head, branchHead)
	}
	status3, err := gitx.Run(gateLease.Dir, "status", "--porcelain")
	if err != nil {
		t.Fatalf("git status: %v", err)
	}
	if status3 != "" {
		t.Fatalf("gate lease left dirty after round 3: %q", status3)
	}
}

// TestGateBranchDiscardsStaleLocalCopyAndTracksAdvancingOrigin reproduces
// NM2 scenario B: `--branch` mode's gate lease never commits, so a local
// commit left behind by a killed reviewer must be discarded - not reviewed
// and passed clean - and origin advancing between two `--branch` gates must
// make the second gate review the new head, not a stale local copy that
// pool.Acquire would otherwise reuse as-is (pool.Acquire never resets an
// existing local branch).
func TestGateBranchDiscardsStaleLocalCopyAndTracksAdvancingOrigin(t *testing.T) {
	t.Setenv("JIG_HOME", t.TempDir())
	fx := fixture.Generate(t, fixture.Opts{})
	driveBuild(t, fx, "rung-a")
	buildDir := buildLeaseDir(t, fx)
	branch := ticketBranch(fx.Ticket)
	if _, err := gitx.Run(buildDir, "push", "origin", branch); err != nil {
		t.Fatalf("push build branch: %v", err)
	}

	d := newDeps(t, fx)
	if _, err := Gate(d, alwaysCleanSource{}, GateOpts{Ticket: fx.Ticket, Branch: branch, Early: true}); err != nil {
		t.Fatalf("Gate round 1: %v", err)
	}
	origin1, err := gitx.Run(buildDir, "rev-parse", "origin/"+branch)
	if err != nil {
		t.Fatalf("resolve origin/%s: %v", branch, err)
	}

	// Simulate a killed reviewer's leftover commit in the gate lease: a
	// local commit ahead of origin, never pushed.
	gateLease, err := pool.Acquire("fixture-repo", fx.RepoRemote, "main", branch, fx.Ticket, pool.Gate)
	if err != nil {
		t.Fatalf("reacquire gate lease: %v", err)
	}
	if err := os.WriteFile(filepath.Join(gateLease.Dir, "leftover.txt"), []byte("leftover reviewer commit\n"), 0o644); err != nil {
		t.Fatalf("write leftover file: %v", err)
	}
	if _, err := gitx.Run(gateLease.Dir, "add", "-A"); err != nil {
		t.Fatalf("git add leftover: %v", err)
	}
	if _, err := gitx.RunEnv(gateLease.Dir, buildGitEnv, "commit", "-m", "leftover reviewer commit"); err != nil {
		t.Fatalf("commit leftover: %v", err)
	}
	leftoverHead, err := gitx.RevParse(gateLease.Dir, "HEAD")
	if err != nil {
		t.Fatalf("resolve leftover HEAD: %v", err)
	}
	if leftoverHead == origin1 {
		t.Fatal("test setup: leftover commit did not move HEAD")
	}

	// Advance origin's branch, as a later build would.
	if err := os.WriteFile(filepath.Join(buildDir, "alpha", "advance.txt"), []byte("advance\n"), 0o644); err != nil {
		t.Fatalf("write advance file: %v", err)
	}
	if _, err := gitx.Run(buildDir, "add", "-A"); err != nil {
		t.Fatalf("git add advance: %v", err)
	}
	if _, err := gitx.RunEnv(buildDir, buildGitEnv, "commit", "-m", "advance origin"); err != nil {
		t.Fatalf("commit advance: %v", err)
	}
	if _, err := gitx.Run(buildDir, "push", "origin", branch); err != nil {
		t.Fatalf("push advance: %v", err)
	}
	origin2, err := gitx.Run(buildDir, "rev-parse", "origin/"+branch)
	if err != nil {
		t.Fatalf("resolve advanced origin/%s: %v", branch, err)
	}
	if origin2 == origin1 {
		t.Fatal("test setup: origin did not advance")
	}

	report2, err := Gate(d, alwaysCleanSource{}, GateOpts{Ticket: fx.Ticket, Branch: branch, Early: true})
	if err != nil {
		t.Fatalf("Gate round 2: %v", err)
	}
	if report2.Round != 2 {
		t.Fatalf("report2.Round = %d, want 2", report2.Round)
	}
	head2, err := gitx.RevParse(gateLease.Dir, "HEAD")
	if err != nil {
		t.Fatalf("resolve gate lease HEAD after round 2: %v", err)
	}
	if head2 != origin2 {
		t.Fatalf("gate lease HEAD after round 2 = %s, want the advanced origin/%s = %s (a stale local copy, or the leftover commit, must not survive)", head2, branch, origin2)
	}
}

// TestGateBranchNotFoundOnOrigin checks that gating a branch missing on
// origin fails loudly with BRANCH_NOT_FOUND, instead of pool.Acquire's own
// fallback (origin/<target> as the start point for a brand-new local
// branch) silently validating a branch that was never pushed.
func TestGateBranchNotFoundOnOrigin(t *testing.T) {
	t.Setenv("JIG_HOME", t.TempDir())
	fx := fixture.Generate(t, fixture.Opts{})

	d := newDeps(t, fx)
	_, err := Gate(d, alwaysCleanSource{}, GateOpts{Ticket: fx.Ticket, Branch: "no-such-branch", Early: true})
	var ae *axi.Error
	if !errors.As(err, &ae) || ae.Code != "BRANCH_NOT_FOUND" {
		t.Fatalf("Gate --branch missing on origin: err = %v, want *axi.Error BRANCH_NOT_FOUND", err)
	}
}

// TestGateBranchDeletedOnOriginAfterEarlierGate checks that a branch which
// existed and gated cleanly, then got deleted on origin before the next
// gate, fails BRANCH_NOT_FOUND rather than reviewing the stale tip that
// pool.Acquire's own fetch (no --prune) leaves behind in
// refs/remotes/origin/<branch>.
func TestGateBranchDeletedOnOriginAfterEarlierGate(t *testing.T) {
	t.Setenv("JIG_HOME", t.TempDir())
	fx := fixture.Generate(t, fixture.Opts{})
	driveBuild(t, fx, "rung-a")
	buildDir := buildLeaseDir(t, fx)
	branch := "feature-x"
	if _, err := gitx.Run(buildDir, "push", "origin", "HEAD:refs/heads/"+branch); err != nil {
		t.Fatalf("push branch: %v", err)
	}

	d := newDeps(t, fx)
	opts := GateOpts{Ticket: fx.Ticket, Branch: branch, Early: true}
	if _, err := Gate(d, alwaysCleanSource{}, opts); err != nil {
		t.Fatalf("Gate round 1: %v", err)
	}

	if _, err := gitx.Run(buildDir, "push", "origin", "--delete", branch); err != nil {
		t.Fatalf("delete branch on origin: %v", err)
	}

	_, err := Gate(d, alwaysCleanSource{}, opts)
	var ae *axi.Error
	if !errors.As(err, &ae) || ae.Code != "BRANCH_NOT_FOUND" {
		t.Fatalf("Gate --branch after origin deleted it: err = %v, want *axi.Error BRANCH_NOT_FOUND", err)
	}
	if len(ae.Help) == 0 {
		t.Fatal("BRANCH_NOT_FOUND has no Help line")
	}
}

// TestGateReviewerFindingsBookkeepingAcrossRounds is findings bookkeeping's
// own Gate() integration test: it drives two real reviewer rounds through
// Gate itself (not reviewerGateSource.Round directly) and checks that the
// wiring findings.go adds - review.json's open list built from the fold,
// the verdict derived from applying the round, findings.yaml/md written to
// disk, and reviewed_sha recorded - all agree with each other.
func TestGateReviewerFindingsBookkeepingAcrossRounds(t *testing.T) {
	t.Setenv("JIG_HOME", t.TempDir())
	fx := fixture.Generate(t, fixture.Opts{})
	driveBuild(t, fx, "rung-a")
	d := newDeps(t, fx)

	round := 0
	backend := stubBackend{run: func(sd session.Dispatch) error {
		round++
		reviewData, err := os.ReadFile(sd.SliceJSON)
		if err != nil {
			t.Fatalf("read review.json: %v", err)
		}
		var req ReviewRequest
		if err := json.Unmarshal(reviewData, &req); err != nil {
			t.Fatalf("parse review.json: %v", err)
		}

		var result ReviewResult
		switch round {
		case 1:
			if len(req.Open) != 0 {
				t.Fatalf("round 1 review.json Open = %+v, want none", req.Open)
			}
			result = ReviewResult{
				Findings: []ResultFinding{{
					File: "alpha/alpha.go", Line: 1, Title: "needs a fix",
					Detail: "leaks a tenant id", Action: ActionFix,
					Risk: RiskHigh, RiskRationale: "customer data", Oracle: "test",
				}},
				ReviewedPaths: req.MustReview,
				Summary:       "round 1 summary",
			}
		case 2:
			if len(req.Open) != 1 || req.Open[0].Title != "needs a fix" || req.Open[0].File != "alpha/alpha.go" {
				t.Fatalf("round 2 review.json Open = %+v, want the round 1 finding fed back", req.Open)
			}
			// Nothing new reported: the round 1 finding clears (rule 3).
			result = ReviewResult{ReviewedPaths: req.MustReview, Summary: "round 2 summary"}
		default:
			t.Fatalf("unexpected round %d", round)
		}

		return os.WriteFile(sd.ResultJSON, marshalReviewResult(t, result), 0o644)
	}}

	src := NewReviewerGateSource(backend)

	report1, err := Gate(d, src, GateOpts{Ticket: fx.Ticket})
	if err != nil {
		t.Fatalf("Gate round 1: %v", err)
	}
	if report1.Round != 1 {
		t.Fatalf("round = %d, want 1", report1.Round)
	}
	if report1.Verdict != "fix-slices" {
		t.Fatalf("round 1 Verdict = %q, want fix-slices", report1.Verdict)
	}
	if report1.ReviewedSHA == nil || report1.ReviewedSHA["fixture-repo"] == "" {
		t.Fatalf("round 1 ReviewedSHA = %+v, want a non-empty fixture-repo entry", report1.ReviewedSHA)
	}

	ff1, ok, err := readFindingsYAML(d.Store, fx.Ticket, 1)
	if err != nil {
		t.Fatalf("read round 1 findings.yaml: %v", err)
	}
	if !ok {
		t.Fatal("round 1 findings.yaml missing")
	}
	if len(ff1.Findings) != 1 {
		t.Fatalf("round 1 findings = %+v, want 1", ff1.Findings)
	}
	f1 := ff1.Findings[0]
	if f1.Status != StatusOpen {
		t.Errorf("round 1 finding status = %q, want open", f1.Status)
	}
	if f1.Workspace != "alpha" {
		t.Errorf("round 1 finding workspace = %q, want alpha", f1.Workspace)
	}
	if f1.ID == "" {
		t.Error("round 1 finding has no id")
	}
	if len(report1.FixSlices) != 1 {
		t.Fatalf("round 1 FixSlices = %v, want 1 (routing kept the fix)", report1.FixSlices)
	}
	sliceID := report1.FixSlices[0]
	fixSlices, err := d.Store.ReadSlices(fx.Ticket)
	if err != nil {
		t.Fatalf("read slices: %v", err)
	}
	var fs *store.Slice
	for i := range fixSlices {
		if fixSlices[i].ID == sliceID {
			fs = &fixSlices[i]
		}
	}
	if fs == nil {
		t.Fatalf("appended fix slice %q not found in slices.yaml", sliceID)
	}
	if !equalStrings(fs.Findings, []string{f1.ID}) {
		t.Errorf("fix slice %s Findings = %v, want [%s]", sliceID, fs.Findings, f1.ID)
	}
	if fs.Workspace != "alpha" || fs.Oracle != "test" {
		t.Errorf("fix slice %s = %+v, want workspace alpha, oracle test", sliceID, fs)
	}

	mdPath := filepath.Join(gateRoundDir(d.Store, fx.Ticket, 1), "findings.md")
	if md, err := os.ReadFile(mdPath); err != nil || len(md) == 0 {
		t.Fatalf("round 1 findings.md: data=%q err=%v", md, err)
	}

	// Round 2 runs --early: the fix slice routing just appended is still
	// queued, and driving it green belongs to frontier, not this test
	// (which only exercises Gate's own findings bookkeeping/routing).
	report2, err := Gate(d, src, GateOpts{Ticket: fx.Ticket, Early: true})
	if err != nil {
		t.Fatalf("Gate round 2: %v", err)
	}
	if report2.Verdict != "clean" {
		t.Fatalf("round 2 Verdict = %q, want clean", report2.Verdict)
	}
	if report2.ReviewedSHA == nil || report2.ReviewedSHA["fixture-repo"] == "" {
		t.Fatalf("round 2 ReviewedSHA = %+v, want a non-empty fixture-repo entry", report2.ReviewedSHA)
	}

	ff2, ok, err := readFindingsYAML(d.Store, fx.Ticket, 2)
	if err != nil {
		t.Fatalf("read round 2 findings.yaml: %v", err)
	}
	if !ok {
		t.Fatal("round 2 findings.yaml missing")
	}
	if !equalStrings(ff2.Cleared, []string{f1.ID}) {
		t.Fatalf("round 2 cleared = %v, want [%s]", ff2.Cleared, f1.ID)
	}

	cum, err := cumulativeFindings(d.Store, fx.Ticket, 3)
	if err != nil {
		t.Fatalf("cumulativeFindings: %v", err)
	}
	if !isClean(cum) {
		t.Fatalf("cumulative state after round 2 = %+v, want clean", cum)
	}
}

// TestGateReviewerClearsFromAbsoluteInLeaseReviewedPath reproduces the
// review finding: a reviewer session whose file-read tool hands back
// absolute paths (the ordinary case for a headless backend) must still
// clear an open finding once its file is covered, and findings.yaml must
// record the coverage as repo-relative paths only, never a host path.
func TestGateReviewerClearsFromAbsoluteInLeaseReviewedPath(t *testing.T) {
	t.Setenv("JIG_HOME", t.TempDir())
	fx := fixture.Generate(t, fixture.Opts{})
	driveBuild(t, fx, "rung-a")
	d := newDeps(t, fx)

	round := 0
	backend := stubBackend{run: func(sd session.Dispatch) error {
		round++
		reviewData, err := os.ReadFile(sd.SliceJSON)
		if err != nil {
			t.Fatalf("read review.json: %v", err)
		}
		var req ReviewRequest
		if err := json.Unmarshal(reviewData, &req); err != nil {
			t.Fatalf("parse review.json: %v", err)
		}

		var result ReviewResult
		switch round {
		case 1:
			result = ReviewResult{
				Findings: []ResultFinding{{
					File: "alpha/alpha.go", Line: 1, Title: "needs a fix",
					Detail: "leaks a tenant id", Action: ActionFix,
					Risk: RiskHigh, RiskRationale: "customer data", Oracle: "test",
				}},
				ReviewedPaths: req.MustReview,
				Summary:       "round 1 summary",
			}
		case 2:
			// Every entry is the lease-absolute form, as a headless backend's
			// file-read tool would echo back.
			abs := make([]string, len(req.MustReview))
			for i, p := range req.MustReview {
				abs[i] = filepath.Join(sd.Worktree, filepath.FromSlash(p))
			}
			result = ReviewResult{ReviewedPaths: abs, Summary: "round 2 summary"}
		default:
			t.Fatalf("unexpected round %d", round)
		}

		return os.WriteFile(sd.ResultJSON, marshalReviewResult(t, result), 0o644)
	}}

	src := NewReviewerGateSource(backend)

	report1, err := Gate(d, src, GateOpts{Ticket: fx.Ticket})
	if err != nil {
		t.Fatalf("Gate round 1: %v", err)
	}
	if report1.Verdict != "fix-slices" {
		t.Fatalf("round 1 Verdict = %q, want fix-slices", report1.Verdict)
	}
	ff1, ok, err := readFindingsYAML(d.Store, fx.Ticket, 1)
	if err != nil || !ok {
		t.Fatalf("read round 1 findings.yaml: ok=%v err=%v", ok, err)
	}
	f1 := ff1.Findings[0]

	// --early: driving the fix slice green belongs to frontier, not this
	// test, which only exercises the reviewer's clearing/coverage path.
	report2, err := Gate(d, src, GateOpts{Ticket: fx.Ticket, Early: true})
	if err != nil {
		t.Fatalf("Gate round 2: %v", err)
	}
	if report2.Verdict != "clean" {
		t.Fatalf("round 2 Verdict = %q, want clean (the absolute-path reviewed_paths should clear %s)", report2.Verdict, f1.ID)
	}

	ff2, ok, err := readFindingsYAML(d.Store, fx.Ticket, 2)
	if err != nil || !ok {
		t.Fatalf("read round 2 findings.yaml: ok=%v err=%v", ok, err)
	}
	if !equalStrings(ff2.Cleared, []string{f1.ID}) {
		t.Fatalf("round 2 cleared = %v, want [%s]", ff2.Cleared, f1.ID)
	}
	for _, p := range ff2.ReviewedPaths {
		if filepath.IsAbs(p) || strings.Contains(p, "\\") {
			t.Errorf("round 2 findings.yaml reviewed_paths entry %q is not repo-relative", p)
		}
	}
}

// TestGateFailedRoundPushesStoreBestEffortSoARerunNeedsNoCleanup reproduces
// the review finding that a round failing after its gate-open journal line
// (REVIEW_INVALID here; a REVIEW_FAILED or a routing error take the exact
// same path) left the store's working copy with a tracked, uncommitted
// change: the round returns before Gate's own end-of-round Store.Push, so
// the journal line jig itself just appended sits uncommitted, and the very
// next command's Store.Sync (git pull --rebase) refuses against it - the
// documented recovery ("the operator reruns") did not actually work
// without a manual `git checkout`/`git clean` on the store first.
func TestGateFailedRoundPushesStoreBestEffortSoARerunNeedsNoCleanup(t *testing.T) {
	t.Setenv("JIG_HOME", t.TempDir())
	fx := fixture.Generate(t, fixture.Opts{})
	driveBuild(t, fx, "rung-a")
	d := newDeps(t, fx)

	attempt := 0
	backend := stubBackend{run: func(sd session.Dispatch) error {
		attempt++
		result := ReviewResult{Summary: "round 1"}
		if attempt == 1 {
			// Missing coverage: REVIEW_INVALID, after the gate-open
			// journal line has already been appended.
			result.ReviewedPaths = nil
		} else {
			reviewData, err := os.ReadFile(sd.SliceJSON)
			if err != nil {
				t.Fatalf("read review.json: %v", err)
			}
			var req ReviewRequest
			if err := json.Unmarshal(reviewData, &req); err != nil {
				t.Fatalf("parse review.json: %v", err)
			}
			result.ReviewedPaths = req.MustReview
		}
		return os.WriteFile(sd.ResultJSON, marshalReviewResult(t, result), 0o644)
	}}
	src := NewReviewerGateSource(backend)

	_, err := Gate(d, src, GateOpts{Ticket: fx.Ticket})
	if err == nil {
		t.Fatal("Gate: want an error (missing must_review coverage)")
	}
	var ae *axi.Error
	if !errors.As(err, &ae) || ae.Code != "REVIEW_INVALID" {
		t.Fatalf("err = %v, want *axi.Error REVIEW_INVALID", err)
	}

	// The store's working copy must already be clean and pushed: no
	// leftover tracked change from the failed round's own gate-open
	// journal line, review.json or result.json.
	status, err := gitx.Run(d.Store.Root, "status", "--porcelain")
	if err != nil {
		t.Fatalf("store status: %v", err)
	}
	if status != "" {
		t.Fatalf("store working copy is dirty after the failed round:\n%s", status)
	}

	// A plain rerun (no manual cleanup) must actually work: Sync must not
	// refuse, and the corrected round must succeed as round 1 (the failed
	// attempt above wrote no gate/round-1/ directory).
	report, err := Gate(d, src, GateOpts{Ticket: fx.Ticket})
	if err != nil {
		t.Fatalf("Gate rerun: %v", err)
	}
	if report.Round != 1 {
		t.Fatalf("rerun Round = %d, want 1", report.Round)
	}
}

// TestGateFailedRoundBeforeRoundKnownRecordsRealRoundAndNoRawError
// reproduces the review finding that a failure between the gate-open
// journal line and the old roundNum assignment (manifest.Resolve,
// runGateOracles, or existingGateRounds itself) committed a permanent,
// pushed store subject reading "gate round 0" - roundNum's zero value,
// never actually resolved - and interpolated the raw error with %v, which
// for a manifest parse failure includes the gate lease's absolute host
// path. This forces the failure to land on round 2 (not round 1) so a
// wrong zero cannot be mistaken for a coincidentally-correct round number,
// and breaks manifest.Resolve specifically so the raw error would carry an
// absolute path if it leaked.
func TestGateFailedRoundBeforeRoundKnownRecordsRealRoundAndNoRawError(t *testing.T) {
	t.Setenv("JIG_HOME", t.TempDir())
	fx := fixture.Generate(t, fixture.Opts{})
	driveBuild(t, fx, "rung-a")
	d := newDeps(t, fx)

	if _, err := Gate(d, alwaysCleanSource{}, GateOpts{Ticket: fx.Ticket}); err != nil {
		t.Fatalf("Gate round 1: %v", err)
	}

	// Push an unparsable .claude/jig.yaml onto the ticket branch: round 2's
	// manifest.Resolve(lease.Dir) then fails after the gate-open journal
	// line, naming the gate lease's own absolute path in its error text.
	buildDir := buildLeaseDir(t, fx)
	claudeDir := filepath.Join(buildDir, ".claude")
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		t.Fatalf("mkdir .claude: %v", err)
	}
	if err := os.WriteFile(filepath.Join(claudeDir, "jig.yaml"), []byte("workspaces: [this is not valid yaml"), 0o644); err != nil {
		t.Fatalf("write broken jig.yaml: %v", err)
	}
	if _, err := gitx.Run(buildDir, "add", "-A"); err != nil {
		t.Fatalf("git add jig.yaml: %v", err)
	}
	if _, err := gitx.RunEnv(buildDir, buildGitEnv, "commit", "-m", "break manifest"); err != nil {
		t.Fatalf("commit jig.yaml: %v", err)
	}
	branch := ticketBranch(fx.Ticket)
	if _, err := gitx.Run(buildDir, "push", "origin", branch); err != nil {
		t.Fatalf("push branch: %v", err)
	}

	gateLease, err := pool.Acquire("fixture-repo", fx.RepoRemote, "main", branch, fx.Ticket, pool.Gate)
	if err != nil {
		t.Fatalf("reacquire gate lease: %v", err)
	}

	_, err = Gate(d, alwaysCleanSource{}, GateOpts{Ticket: fx.Ticket})
	if err == nil {
		t.Fatal("Gate round 2: want an error (broken manifest)")
	}

	subject, gerr := gitx.Run(d.Store.Root, "log", "-1", "--format=%s")
	if gerr != nil {
		t.Fatalf("read store log: %v", gerr)
	}
	if strings.Contains(subject, "round 0") {
		t.Fatalf("push subject = %q, round was never actually resolved before journaling", subject)
	}
	if !strings.Contains(subject, "round 2") {
		t.Fatalf("push subject = %q, want it to name the real round 2", subject)
	}
	if strings.Contains(subject, gateLease.Dir) {
		t.Fatalf("push subject = %q, leaked the gate lease's absolute host path", subject)
	}
	if strings.Contains(subject, "yaml") {
		t.Fatalf("push subject = %q, leaked the raw manifest parse error instead of a code", subject)
	}
}

// TestGateRecurrenceBoundSurvivesANoteInBetween reproduces the review
// finding that labeling a recurrence "note" reset its identity: a noted
// finding left review.json's open list entirely, so no later round could
// ever cite it as `prior` again, and the same underlying problem reported
// afterward became a brand new id at recurrences 0 - the recurrence bound
// (two) was evadable just by alternating fix and note.
//
// Round 1 reports a fix (r1-f1, recurrences 0). Its fix slice is marked
// green directly (frontier's own job, not this test's). Round 2 cites
// prior: r1-f1 as a note: recurrences 1, status noted. Round 3 must still
// be able to cite prior: r1-f1 (proving it stayed a citable id although
// noted) and report it as a fix again: recurrences 2 forces it to asked
// (routed_as: ask) whatever this round's own label, exactly like an
// uninterrupted fix/fix recurrence would - visible here in its own
// per-finding fix slice, since the finding's build target already
// resolves in full and DefaultTriage auto-keeps it.
func TestGateRecurrenceBoundSurvivesANoteInBetween(t *testing.T) {
	t.Setenv("JIG_HOME", t.TempDir())
	fx := fixture.Generate(t, fixture.Opts{})
	driveBuild(t, fx, "rung-a")
	d := newDeps(t, fx)

	round := 0
	backend := stubBackend{run: func(sd session.Dispatch) error {
		round++
		reviewData, err := os.ReadFile(sd.SliceJSON)
		if err != nil {
			t.Fatalf("read review.json: %v", err)
		}
		var req ReviewRequest
		if err := json.Unmarshal(reviewData, &req); err != nil {
			t.Fatalf("parse review.json: %v", err)
		}

		var result ReviewResult
		switch round {
		case 1:
			result = ReviewResult{
				Findings: []ResultFinding{{
					File: "alpha/alpha.go", Line: 1, Title: "needs a fix",
					Detail: "d", Action: ActionFix, Risk: RiskMedium, RiskRationale: "r",
					Oracle: "test",
				}},
				ReviewedPaths: req.MustReview,
				Summary:       "round 1",
			}
		case 2:
			if len(req.Open) != 1 || req.Open[0].ID != "r1-f1" || req.Open[0].Recurrences != 0 {
				t.Fatalf("round 2 review.json Open = %+v, want r1-f1 at 0 recurrences", req.Open)
			}
			result = ReviewResult{
				Findings: []ResultFinding{{
					File: "alpha/alpha.go", Line: 1, Title: "still there, harmless now",
					Detail: "d", Action: ActionNote, Risk: RiskLow, RiskRationale: "r",
					Oracle: "test", Prior: "r1-f1",
				}},
				ReviewedPaths: req.MustReview,
				Summary:       "round 2",
			}
		case 3:
			// The finding jig's own round-2 bookkeeping recorded as noted
			// must still be offered as a citable id: the fix.
			if len(req.Open) != 1 || req.Open[0].ID != "r1-f1" || req.Open[0].Recurrences != 1 || req.Open[0].Action != ActionNote {
				t.Fatalf("round 3 review.json Open = %+v, want r1-f1 (noted, 1 recurrence) fed back", req.Open)
			}
			result = ReviewResult{
				Findings: []ResultFinding{{
					File: "alpha/alpha.go", Line: 1, Title: "back again",
					Detail: "d", Action: ActionFix, Risk: RiskMedium, RiskRationale: "r",
					Oracle: "test", Prior: "r1-f1",
				}},
				ReviewedPaths: req.MustReview,
				Summary:       "round 3",
			}
		default:
			t.Fatalf("unexpected round %d", round)
		}
		return os.WriteFile(sd.ResultJSON, marshalReviewResult(t, result), 0o644)
	}}
	src := NewReviewerGateSource(backend)

	report1, err := Gate(d, src, GateOpts{Ticket: fx.Ticket})
	if err != nil {
		t.Fatalf("Gate round 1: %v", err)
	}
	if len(report1.FixSlices) != 1 {
		t.Fatalf("round 1 FixSlices = %v, want exactly one", report1.FixSlices)
	}
	if err := d.Store.WriteSliceState(fx.Ticket, report1.FixSlices[0], store.SliceState{State: "green"}); err != nil {
		t.Fatalf("mark round 1 fix slice green: %v", err)
	}
	if err := d.Store.Push(fx.Ticket + ": mark " + report1.FixSlices[0] + " green"); err != nil {
		t.Fatalf("push round 1 fix slice state: %v", err)
	}

	report2, err := Gate(d, src, GateOpts{Ticket: fx.Ticket})
	if err != nil {
		t.Fatalf("Gate round 2: %v", err)
	}
	if len(report2.FixSlices) != 0 {
		t.Fatalf("round 2 FixSlices = %v, want none (a note is never routed)", report2.FixSlices)
	}
	ff2, ok, err := readFindingsYAML(d.Store, fx.Ticket, 2)
	if err != nil || !ok {
		t.Fatalf("read round 2 findings.yaml: ok=%v err=%v", ok, err)
	}
	if len(ff2.Findings) != 1 || ff2.Findings[0].Status != StatusNoted || ff2.Findings[0].Recurrences != 1 {
		t.Fatalf("round 2 findings = %+v, want [r1-f1 noted, 1 recurrence]", ff2.Findings)
	}

	// A noted finding is not outstanding work, so it no longer forces a
	// round to dispatch on its own (Q8's clean-without-dispatch shortcut):
	// a trivial, unrelated commit gives round 3 a reason to dispatch,
	// exactly like a real ticket branch that keeps advancing.
	buildDir := buildLeaseDir(t, fx)
	branch := ticketBranch(fx.Ticket)
	if err := os.WriteFile(filepath.Join(buildDir, "beta", "unrelated.txt"), []byte("unrelated\n"), 0o644); err != nil {
		t.Fatalf("write unrelated.txt: %v", err)
	}
	if _, err := gitx.Run(buildDir, "add", "-A"); err != nil {
		t.Fatalf("git add unrelated.txt: %v", err)
	}
	if _, err := gitx.RunEnv(buildDir, buildGitEnv, "commit", "-m", "add unrelated.txt"); err != nil {
		t.Fatalf("commit unrelated.txt: %v", err)
	}
	if _, err := gitx.Run(buildDir, "push", "origin", branch); err != nil {
		t.Fatalf("push branch: %v", err)
	}

	report3, err := Gate(d, src, GateOpts{Ticket: fx.Ticket})
	if err != nil {
		t.Fatalf("Gate round 3: %v", err)
	}
	// r1-f1's file (alpha/alpha.go, oracle test) already has a full build
	// target, so DefaultTriage - unattended, no terminal - auto-keeps the
	// forced ask rather than leaving it for a human: that is what proves
	// the bound actually fired here, distinctly from an ordinary fix. A
	// fix routes into the round's grouped fix-<n>-<workspace>-<oracle>
	// slice; only a (kept) ask gets its own per-finding
	// fix-<n>-<finding-id> slice (route.go's Q9 id scheme), and
	// routed_as/recurrences on the persisted finding confirm it directly.
	if len(report3.Findings) != 1 || report3.Findings[0].ID != "r1-f1" {
		t.Fatalf("round 3 Findings = %+v, want exactly [r1-f1]", report3.Findings)
	}
	f := report3.Findings[0]
	if f.RoutedAs != ActionAsk || f.Recurrences != 2 {
		t.Fatalf("round 3 finding = %+v, want routed_as ask, 2 recurrences (the bound reached)", f)
	}
	if len(report3.FixSlices) != 1 || report3.FixSlices[0] != "fix-3-r1-f1" {
		t.Fatalf("round 3 FixSlices = %v, want exactly [fix-3-r1-f1] (the ask's own per-finding slice)", report3.FixSlices)
	}
}

// TestGateReviewerRoundsProceedWhenAnOpenFindingsFileBecomesIgnoredAndGenerated
// reproduces the wedge a text match on git's cat-file message used to fall
// into: an open finding's file is later untracked and gitignored on the
// ticket branch, but an oracle regenerates it on disk in the gate lease
// before the next round's review. FileExistsAtRev must report it absent at
// head from the tree alone, never from what happens to sit in the working
// tree, or every later round fails the same way and no dismissal can get
// past it.
func TestGateReviewerRoundsProceedWhenAnOpenFindingsFileBecomesIgnoredAndGenerated(t *testing.T) {
	t.Setenv("JIG_HOME", t.TempDir())
	fx := fixture.Generate(t, fixture.Opts{})
	driveBuild(t, fx, "rung-a")
	buildDir := buildLeaseDir(t, fx)
	branch := ticketBranch(fx.Ticket)

	// Round 1 needs a tracked file for its open finding to point at.
	if err := os.WriteFile(filepath.Join(buildDir, "alpha", "gen.txt"), []byte("committed\n"), 0o644); err != nil {
		t.Fatalf("write gen.txt: %v", err)
	}
	if _, err := gitx.Run(buildDir, "add", "-A"); err != nil {
		t.Fatalf("git add gen.txt: %v", err)
	}
	if _, err := gitx.RunEnv(buildDir, buildGitEnv, "commit", "-m", "add tracked gen.txt"); err != nil {
		t.Fatalf("commit gen.txt: %v", err)
	}
	if _, err := gitx.Run(buildDir, "push", "origin", branch); err != nil {
		t.Fatalf("push branch: %v", err)
	}

	d := newDeps(t, fx)
	round := 0
	backend := stubBackend{run: func(sd session.Dispatch) error {
		round++
		reviewData, err := os.ReadFile(sd.SliceJSON)
		if err != nil {
			t.Fatalf("read review.json: %v", err)
		}
		var req ReviewRequest
		if err := json.Unmarshal(reviewData, &req); err != nil {
			t.Fatalf("parse review.json: %v", err)
		}

		var result ReviewResult
		switch round {
		case 1:
			result = ReviewResult{
				Findings: []ResultFinding{{
					File: "alpha/gen.txt", Line: 1, Title: "a generated file was committed",
					Detail: "gen.txt should not be tracked", Action: ActionFix,
					Risk: RiskLow, RiskRationale: "build artifact churn", Oracle: "test",
				}},
				ReviewedPaths: req.MustReview,
				Summary:       "round 1 summary",
			}
		case 2:
			// gen.txt is untracked and gitignored as of this round's head, so
			// it no longer belongs in must_review; nothing to report on it.
			if containsString(req.MustReview, "alpha/gen.txt") {
				t.Fatalf("round 2 must_review = %v, want it to exclude alpha/gen.txt (absent at head)", req.MustReview)
			}
			result = ReviewResult{ReviewedPaths: req.MustReview, Summary: "round 2 summary"}
		default:
			t.Fatalf("unexpected round %d", round)
		}

		return os.WriteFile(sd.ResultJSON, marshalReviewResult(t, result), 0o644)
	}}
	src := NewReviewerGateSource(backend)

	report1, err := Gate(d, src, GateOpts{Ticket: fx.Ticket})
	if err != nil {
		t.Fatalf("Gate round 1: %v", err)
	}
	if report1.Verdict != "fix-slices" {
		t.Fatalf("round 1 Verdict = %q, want fix-slices", report1.Verdict)
	}
	ff1, ok, err := readFindingsYAML(d.Store, fx.Ticket, 1)
	if err != nil || !ok {
		t.Fatalf("read round 1 findings.yaml: ok=%v err=%v", ok, err)
	}
	if len(ff1.Findings) != 1 || ff1.Findings[0].Status != StatusOpen {
		t.Fatalf("round 1 findings = %+v, want one open finding", ff1.Findings)
	}
	findingID := ff1.Findings[0].ID

	// Advance the ticket branch: untrack gen.txt, ignore it, and make the
	// alpha oracle regenerate it as a build artifact, exactly as a real
	// generated-artifact test would leave it sitting in the lease.
	if _, err := gitx.Run(buildDir, "rm", "--cached", "alpha/gen.txt"); err != nil {
		t.Fatalf("git rm --cached gen.txt: %v", err)
	}
	if err := os.WriteFile(filepath.Join(buildDir, ".gitignore"), []byte("alpha/gen.txt\n"), 0o644); err != nil {
		t.Fatalf("write .gitignore: %v", err)
	}
	alphaTest, err := os.ReadFile(filepath.Join(buildDir, "alpha", "alpha_test.go"))
	if err != nil {
		t.Fatalf("read alpha_test.go: %v", err)
	}
	regen := strings.Replace(string(alphaTest), "import \"testing\"",
		"import (\n\t\"os\"\n\t\"testing\"\n)", 1)
	regen += "\nfunc TestZZRegenGen(t *testing.T) {\n" +
		"\tif err := os.WriteFile(\"gen.txt\", []byte(\"regenerated\\n\"), 0o644); err != nil {\n" +
		"\t\tt.Fatalf(\"write gen.txt: %v\", err)\n" +
		"\t}\n}\n"
	if err := os.WriteFile(filepath.Join(buildDir, "alpha", "alpha_test.go"), []byte(regen), 0o644); err != nil {
		t.Fatalf("write regenerating alpha_test.go: %v", err)
	}
	if _, err := gitx.Run(buildDir, "add", "-A"); err != nil {
		t.Fatalf("git add: %v", err)
	}
	if _, err := gitx.RunEnv(buildDir, buildGitEnv, "commit", "-m", "untrack and regenerate gen.txt"); err != nil {
		t.Fatalf("commit: %v", err)
	}
	if _, err := gitx.Run(buildDir, "push", "origin", branch); err != nil {
		t.Fatalf("push branch: %v", err)
	}

	// The fix slice routed from round 1 is still queued; this round only
	// exercises the reviewer path, not frontier's own build loop.
	report2, err := Gate(d, src, GateOpts{Ticket: fx.Ticket, Early: true})
	if err != nil {
		t.Fatalf("Gate round 2: %v", err)
	}
	if report2.Verdict != "clean" {
		t.Fatalf("round 2 Verdict = %q, want clean (the ignored, regenerated file must clear, not wedge the round)", report2.Verdict)
	}
	ff2, ok, err := readFindingsYAML(d.Store, fx.Ticket, 2)
	if err != nil || !ok {
		t.Fatalf("read round 2 findings.yaml: ok=%v err=%v", ok, err)
	}
	if !equalStrings(ff2.Cleared, []string{findingID}) {
		t.Fatalf("round 2 cleared = %v, want [%s]", ff2.Cleared, findingID)
	}

	// A third round must proceed too: the round after the wedge is not a
	// one-time reprieve, the file stays absent at head for good.
	report3, err := Gate(d, src, GateOpts{Ticket: fx.Ticket, Early: true})
	if err != nil {
		t.Fatalf("Gate round 3: %v", err)
	}
	if report3.Verdict != "clean" {
		t.Fatalf("round 3 Verdict = %q, want clean", report3.Verdict)
	}
}

// containsString reports whether s contains v.
func containsString(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

// addSecondOracle adds a "vet" oracle to buildDir's checked-out manifest
// (a second command over the same go binary the fixture's own "test"
// oracle already resolved to), commits it on branch, and pushes it to
// origin, so a Gate round resolving the manifest from that branch sees two
// oracles instead of one.
func addSecondOracle(t *testing.T, buildDir, branch string) {
	t.Helper()
	yamlPath := filepath.Join(buildDir, ".claude", "jig.yaml")
	raw, err := os.ReadFile(yamlPath)
	if err != nil {
		t.Fatalf("read jig.yaml: %v", err)
	}
	var doc map[string]any
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse jig.yaml: %v", err)
	}
	oracles, ok := doc["oracles"].(map[string]any)
	if !ok {
		t.Fatalf("jig.yaml oracles = %+v, want a map", doc["oracles"])
	}
	testCmd, ok := oracles["test"].(string)
	if !ok {
		t.Fatalf("jig.yaml oracles.test = %+v, want a string", oracles["test"])
	}
	oracles["vet"] = strings.Replace(testCmd, " test ", " vet ", 1)
	out, err := yaml.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal jig.yaml: %v", err)
	}
	if err := os.WriteFile(yamlPath, out, 0o644); err != nil {
		t.Fatalf("write jig.yaml: %v", err)
	}
	if _, err := gitx.Run(buildDir, "add", "-A"); err != nil {
		t.Fatalf("git add jig.yaml: %v", err)
	}
	if _, err := gitx.RunEnv(buildDir, buildGitEnv, "commit", "-m", "add a second manifest oracle"); err != nil {
		t.Fatalf("commit jig.yaml: %v", err)
	}
	if _, err := gitx.Run(buildDir, "push", "origin", branch); err != nil {
		t.Fatalf("push branch: %v", err)
	}
}

// renameOracle renames a manifest oracle (its declared name, not its
// command) on buildDir's checked-out branch and pushes it to origin,
// reproducing a manifest change between gate rounds: a finding whose
// recorded oracle was from before this becomes stale.
func renameOracle(t *testing.T, buildDir, branch, from, to string) {
	t.Helper()
	yamlPath := filepath.Join(buildDir, ".claude", "jig.yaml")
	raw, err := os.ReadFile(yamlPath)
	if err != nil {
		t.Fatalf("read jig.yaml: %v", err)
	}
	var doc map[string]any
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse jig.yaml: %v", err)
	}
	oracles, ok := doc["oracles"].(map[string]any)
	if !ok {
		t.Fatalf("jig.yaml oracles = %+v, want a map", doc["oracles"])
	}
	cmd, ok := oracles[from].(string)
	if !ok {
		t.Fatalf("jig.yaml oracles.%s = %+v, want a string", from, oracles[from])
	}
	delete(oracles, from)
	oracles[to] = cmd
	out, err := yaml.Marshal(doc)
	if err != nil {
		t.Fatalf("marshal jig.yaml: %v", err)
	}
	if err := os.WriteFile(yamlPath, out, 0o644); err != nil {
		t.Fatalf("write jig.yaml: %v", err)
	}
	if _, err := gitx.Run(buildDir, "add", "-A"); err != nil {
		t.Fatalf("git add jig.yaml: %v", err)
	}
	if _, err := gitx.RunEnv(buildDir, buildGitEnv, "commit", "-m", "rename a manifest oracle"); err != nil {
		t.Fatalf("commit jig.yaml: %v", err)
	}
	if _, err := gitx.Run(buildDir, "push", "origin", branch); err != nil {
		t.Fatalf("push branch: %v", err)
	}
}

// setupStaleOracleThroughRoundTwo drives a fixture through two green gate
// rounds recording the same open finding on a two-oracle manifest with an
// explicit oracle ("vet"), then renames that oracle to "lint" on the ticket
// branch: a fix slice recorded a valid oracle, went green twice, and the
// manifest changed between rounds so that recorded oracle no longer
// resolves. round3 supplies round 3's own review
// result (a recurrence of the same finding via Prior, reported as a note
// with its oracle omitted so validation's "must name one of the manifest's
// oracles" rule does not apply - the same way the real scenario reaches jig
// with a stale, carried-forward oracle instead of a freshly named one) and
// gets req (round 3's review.json request) to build it from. It returns the
// deps, fixture and reviewer source with rounds 1 and 2 already applied,
// ready for the caller to run round 3 through Gate.
func setupStaleOracleThroughRoundTwo(t *testing.T, round3 func(req ReviewRequest) ReviewResult) (Deps, *fixture.Fixture, GateSource) {
	t.Helper()
	t.Setenv("JIG_HOME", t.TempDir())
	fx := fixture.Generate(t, fixture.Opts{})
	driveBuild(t, fx, "rung-a")
	buildDir := buildLeaseDir(t, fx)
	branch := ticketBranch(fx.Ticket)
	addSecondOracle(t, buildDir, branch)

	d := newDeps(t, fx)
	round := 0
	backend := stubBackend{run: func(sd session.Dispatch) error {
		round++
		reviewData, err := os.ReadFile(sd.SliceJSON)
		if err != nil {
			t.Fatalf("read review.json: %v", err)
		}
		var req ReviewRequest
		if err := json.Unmarshal(reviewData, &req); err != nil {
			t.Fatalf("parse review.json: %v", err)
		}

		var result ReviewResult
		switch round {
		case 1:
			result = ReviewResult{
				Findings: []ResultFinding{{
					File: "alpha/alpha.go", Line: 1, Title: "needs a fix",
					Detail: "d", Action: ActionFix, Risk: RiskMedium, RiskRationale: "r",
					Oracle: "vet",
				}},
				ReviewedPaths: req.MustReview,
				Summary:       "round 1",
			}
		case 2:
			if len(req.Open) != 1 || req.Open[0].ID != "r1-f1" {
				t.Fatalf("round 2 review.json Open = %+v, want r1-f1 fed back", req.Open)
			}
			result = ReviewResult{
				Findings: []ResultFinding{{
					File: "alpha/alpha.go", Line: 1, Title: "needs a fix",
					Detail: "d", Action: ActionFix, Risk: RiskMedium, RiskRationale: "r",
					Oracle: "vet", Prior: "r1-f1",
				}},
				ReviewedPaths: req.MustReview,
				Summary:       "round 2",
			}
		case 3:
			if len(req.Open) != 1 || req.Open[0].ID != "r1-f1" || req.Open[0].Recurrences != 1 {
				t.Fatalf("round 3 review.json Open = %+v, want r1-f1 at 1 recurrence", req.Open)
			}
			result = round3(req)
		default:
			t.Fatalf("unexpected round %d", round)
		}
		return os.WriteFile(sd.ResultJSON, marshalReviewResult(t, result), 0o644)
	}}
	src := NewReviewerGateSource(backend)

	report1, err := Gate(d, src, GateOpts{Ticket: fx.Ticket})
	if err != nil {
		t.Fatalf("Gate round 1: %v", err)
	}
	if len(report1.FixSlices) != 1 {
		t.Fatalf("round 1 FixSlices = %v, want exactly one", report1.FixSlices)
	}
	if err := d.Store.WriteSliceState(fx.Ticket, report1.FixSlices[0], store.SliceState{State: "green"}); err != nil {
		t.Fatalf("mark round 1 fix slice green: %v", err)
	}
	if err := d.Store.Push(fx.Ticket + ": mark " + report1.FixSlices[0] + " green"); err != nil {
		t.Fatalf("push round 1 fix slice state: %v", err)
	}

	report2, err := Gate(d, src, GateOpts{Ticket: fx.Ticket})
	if err != nil {
		t.Fatalf("Gate round 2: %v", err)
	}
	if len(report2.FixSlices) != 1 {
		t.Fatalf("round 2 FixSlices = %v, want exactly one", report2.FixSlices)
	}
	if err := d.Store.WriteSliceState(fx.Ticket, report2.FixSlices[0], store.SliceState{State: "green"}); err != nil {
		t.Fatalf("mark round 2 fix slice green: %v", err)
	}
	if err := d.Store.Push(fx.Ticket + ": mark " + report2.FixSlices[0] + " green"); err != nil {
		t.Fatalf("push round 2 fix slice state: %v", err)
	}

	// Advance the branch: the oracle both green fix slices recorded no
	// longer exists in the manifest by round 3.
	renameOracle(t, buildDir, branch, "vet", "lint")

	return d, fx, src
}

// TestGateStaleOracleRecurrenceNeedsAHumanUnattended proves the unattended
// half of the build-target rule for an oracle, not only a workspace: a
// second recurrence forces the finding to a human decision
// regardless of its own reported action, and once forced, the oracle it
// carries forward from a manifest that has since changed cannot resolve
// on its own, so it stays asked and is listed under needs_a_human (the
// exit-2 signal) - never silently rebuilt on some other oracle.
func TestGateStaleOracleRecurrenceNeedsAHumanUnattended(t *testing.T) {
	d, fx, src := setupStaleOracleThroughRoundTwo(t, func(req ReviewRequest) ReviewResult {
		return ReviewResult{
			Findings: []ResultFinding{{
				File: "alpha/alpha.go", Line: 1, Title: "still there", Detail: "d",
				Action: ActionNote, Risk: RiskLow, RiskRationale: "r", Prior: "r1-f1",
			}},
			ReviewedPaths: req.MustReview,
			Summary:       "round 3",
		}
	})

	report3, err := Gate(d, src, GateOpts{Ticket: fx.Ticket}) // nil Triage: DefaultTriage, unattended
	if err != nil {
		t.Fatalf("Gate round 3: %v", err)
	}
	if len(report3.NeedsHuman) != 1 || report3.NeedsHuman[0].ID != "r1-f1" {
		t.Fatalf("NeedsHuman = %+v, want exactly [r1-f1] (the stale-oracle recurrence)", report3.NeedsHuman)
	}
	f := report3.NeedsHuman[0]
	if f.Status != StatusAsked || f.Oracle != "vet" || f.Triage != "" {
		t.Fatalf("needs_a_human finding = %+v, want asked, the stale oracle vet still recorded, no triage decided", f)
	}
}

// TestGateStaleOracleRecurrenceTerminalKeepBuildsSliceOnTheChosenOracle
// proves the terminal half of the same rule: a human keeping the same
// finding supplies the missing oracle (exactly what cmd/jig's
// interactiveTriage prompt gathers once BuildTargetGaps reports it
// missing), and routing builds the fix slice on that chosen oracle, not the
// stale one the finding carried forward.
func TestGateStaleOracleRecurrenceTerminalKeepBuildsSliceOnTheChosenOracle(t *testing.T) {
	d, fx, src := setupStaleOracleThroughRoundTwo(t, func(req ReviewRequest) ReviewResult {
		return ReviewResult{
			Findings: []ResultFinding{{
				File: "alpha/alpha.go", Line: 1, Title: "still there", Detail: "d",
				Action: ActionNote, Risk: RiskLow, RiskRationale: "r", Prior: "r1-f1",
			}},
			ReviewedPaths: req.MustReview,
			Summary:       "round 3",
		}
	})

	// The triage hook stands in for cmd/jig's interactiveTriage: a human at
	// a terminal keeping this finding and being prompted for an oracle
	// (its own, "vet", no longer resolves) answers "lint".
	triage := func(in TriageInput) TriageResult {
		if len(in.Asks) != 1 || in.Asks[0].ID != "r1-f1" {
			t.Fatalf("TriageInput.Asks = %+v, want exactly [r1-f1]", in.Asks)
		}
		return TriageResult{Asks: map[string]AskOutcome{
			"r1-f1": {Keep: true, Decision: "use lint here", Oracle: "lint", Human: true},
		}}
	}

	report3, err := Gate(d, src, GateOpts{Ticket: fx.Ticket, Triage: triage})
	if err != nil {
		t.Fatalf("Gate round 3: %v", err)
	}
	if len(report3.NeedsHuman) != 0 {
		t.Fatalf("NeedsHuman = %+v, want none (the human decided)", report3.NeedsHuman)
	}
	if len(report3.FixSlices) != 1 {
		t.Fatalf("FixSlices = %v, want exactly one", report3.FixSlices)
	}
	slices, err := d.Store.ReadSlices(fx.Ticket)
	if err != nil {
		t.Fatalf("ReadSlices: %v", err)
	}
	var fs *store.Slice
	for i := range slices {
		if slices[i].ID == report3.FixSlices[0] {
			fs = &slices[i]
		}
	}
	if fs == nil {
		t.Fatalf("appended fix slice %q not found in slices.yaml", report3.FixSlices[0])
	}
	if fs.Oracle != "lint" {
		t.Fatalf("fix slice oracle = %q, want lint (the human's chosen oracle, not the stale one)", fs.Oracle)
	}
	if !strings.Contains(fs.Goal, "use lint here") {
		t.Fatalf("fix slice goal missing the human's decision text:\n%s", fs.Goal)
	}

	ff, ok, err := readFindingsYAML(d.Store, fx.Ticket, 3)
	if err != nil || !ok {
		t.Fatalf("read round 3 findings.yaml: ok=%v err=%v", ok, err)
	}
	if len(ff.Findings) != 1 || ff.Findings[0].Status != StatusOpen || ff.Findings[0].Oracle != "lint" || ff.Findings[0].Triage != TriageHuman {
		t.Fatalf("round 3 finding = %+v, want open, oracle lint, triage human", ff.Findings[0])
	}
}

func TestGatePRModeNotImplemented(t *testing.T) {
	_, err := Gate(Deps{}, nil, GateOpts{PRMode: true})
	var ae *axi.Error
	if !errors.As(err, &ae) || ae.Code != "NOT_IMPLEMENTED" {
		t.Fatalf("err = %v, want *axi.Error NOT_IMPLEMENTED", err)
	}
}

// TestGateNoRoundKeepsAnOutstandingAskFromGoingClean pins the one path no
// test covered: a source with nothing to review for this round, while the
// cumulative fold still holds an undecided ask.
//
// The round used to be declared clean without consulting the fold at all.
// `jig publish` gates on that verdict, so the ticket squashed, pushed and
// opened its PR with the question never answered - while `jig status`,
// which reads the fold, listed it as outstanding the whole time. Whether
// a source happened to script a round says nothing about what earlier
// rounds left behind.
func TestGateNoRoundKeepsAnOutstandingAskFromGoingClean(t *testing.T) {
	t.Setenv("JIG_HOME", t.TempDir())
	fx := fixture.Generate(t, fixture.Opts{})
	driveBuild(t, fx, "rung-a")

	d := newDeps(t, fx)
	// Round 1 is recorded directly: an ask nobody decided, exactly the
	// state a real round 1 leaves when no human is at the terminal.
	roundDir := filepath.Join(d.Store.TicketDir(fx.Ticket), "gate", "round-1")
	if err := os.MkdirAll(roundDir, 0o755); err != nil {
		t.Fatalf("mkdir round 1: %v", err)
	}
	body, err := marshalFindingsYAML("full", []string{"alpha/alpha.go"}, []Finding{{
		ID: "r1-f1", File: "alpha/alpha.go", Line: 4, Title: "is this rename intended",
		Detail: "d", Action: ActionAsk, Risk: RiskHigh, RiskRationale: "callers depend on it",
		Status: StatusAsked,
	}}, nil, "round 1")
	if err != nil {
		t.Fatalf("marshal findings.yaml: %v", err)
	}
	if err := os.WriteFile(filepath.Join(roundDir, "findings.yaml"), body, 0o644); err != nil {
		t.Fatalf("write findings.yaml: %v", err)
	}

	// alwaysCleanSource stands in for any source with nothing to review
	// for round 2: the scripted source with no round-2 directory behaves
	// identically, which is the documented --scenario compatibility path.
	report, err := Gate(d, alwaysCleanSource{}, GateOpts{Ticket: fx.Ticket})
	if err != nil {
		t.Fatalf("Gate: %v", err)
	}
	if report.Verdict == "clean" {
		t.Fatalf("Verdict = clean with r1-f1 still asked; publish gates on this verdict and would ship the ticket")
	}
	if report.Verdict != "fix-slices" {
		t.Fatalf("Verdict = %q, want fix-slices", report.Verdict)
	}
	if len(report.NeedsHuman) != 1 || report.NeedsHuman[0].ID != "r1-f1" {
		t.Fatalf("NeedsHuman = %+v, want the outstanding ask r1-f1 (the exit-2 signal)", report.NeedsHuman)
	}

	// The stored round must say the same thing the report does.
	data, err := os.ReadFile(filepath.Join(d.Store.TicketDir(fx.Ticket), "gate", "round-2", "report.yaml"))
	if err != nil {
		t.Fatalf("read round 2 report.yaml: %v", err)
	}
	if strings.Contains(string(data), "verdict: clean") {
		t.Fatalf("round 2 report.yaml records a clean verdict:\n%s", data)
	}
}
