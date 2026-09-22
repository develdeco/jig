package verifydeliver

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/pool"
	"github.com/develdeco/jig/internal/store"
)

// alwaysCleanSource is a GateSource stub that always reports a clean
// round, for tests that only care about the frontier/model/oracle path.
type alwaysCleanSource struct{}

func (alwaysCleanSource) Round(int) (Round, bool, error) { return Round{}, false, nil }

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

func (duplicateFixSliceSource) Round(n int) (Round, bool, error) {
	if n == 1 {
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

	gateLease, err := pool.Acquire("fixture-repo", fx.RepoRemote, "main", ticketBranch(fx.Ticket), fx.Ticket+"-gate")
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

	gateLease, err := pool.Acquire("fixture-repo", fx.RepoRemote, "main", ticketBranch(fx.Ticket), fx.Ticket+"-gate")
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
	gateLease, err := pool.Acquire("fixture-repo", fx.RepoRemote, "main", branch, fx.Ticket+"-gate")
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

func TestGatePRModeNotImplemented(t *testing.T) {
	_, err := Gate(Deps{}, nil, GateOpts{PRMode: true})
	var ae *axi.Error
	if !errors.As(err, &ae) || ae.Code != "NOT_IMPLEMENTED" {
		t.Fatalf("err = %v, want *axi.Error NOT_IMPLEMENTED", err)
	}
}
