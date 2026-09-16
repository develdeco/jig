package verifydeliver

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/develdeco/jig/axi"
	"github.com/develdeco/jig/fixture"
	"github.com/develdeco/jig/gitx"
	"github.com/develdeco/jig/journal"
	"github.com/develdeco/jig/store"
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

func TestGatePRModeNotImplemented(t *testing.T) {
	_, err := Gate(Deps{}, nil, GateOpts{PRMode: true})
	var ae *axi.Error
	if !errors.As(err, &ae) || ae.Code != "NOT_IMPLEMENTED" {
		t.Fatalf("err = %v, want *axi.Error NOT_IMPLEMENTED", err)
	}
}
