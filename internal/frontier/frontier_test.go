package frontier

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/manifest"
	"github.com/develdeco/jig/internal/outcome"
	"github.com/develdeco/jig/internal/pool"
	"github.com/develdeco/jig/internal/project"
	"github.com/develdeco/jig/internal/session"
	"github.com/develdeco/jig/internal/staircase"
	"github.com/develdeco/jig/internal/store"
)

// newDeps wires Deps against a generated fixture, using the fake
// session backend against fx.ScenarioDir.
func newDeps(t *testing.T, fx *fixture.Fixture) (Deps, *store.Store) {
	t.Helper()
	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	cfg, err := project.Load(filepath.Join(fx.StoreDir, "project.yaml"))
	if err != nil {
		t.Fatalf("project.Load: %v", err)
	}
	backend, err := session.New("fake", session.Options{ScenarioDir: fx.ScenarioDir})
	if err != nil {
		t.Fatalf("session.New: %v", err)
	}
	d := Deps{
		Store:   st,
		Cfg:     cfg,
		Backend: backend,
		Rungs:   staircase.Default(),
		Journal: func(l journal.Line) error { return journal.Append(st, fx.Ticket, l) },
		Home:    fx.Home,
	}
	return d, st
}

func containsID(ids []string, want string) bool {
	for _, id := range ids {
		if id == want {
			return true
		}
	}
	return false
}

func TestRunScenarioMainChain(t *testing.T) {
	fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
	d, st := newDeps(t, fx)

	report, err := Run(d, RunOpts{Ticket: fx.Ticket})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	for _, want := range []string{"a", "b", "d"} {
		if !containsID(report.Green, want) {
			t.Fatalf("Green = %v, want it to contain %s", report.Green, want)
		}
	}
	if len(report.NeedsInput) != 1 || report.NeedsInput[0] != "c" {
		t.Fatalf("NeedsInput = %v, want [c]", report.NeedsInput)
	}
	if report.PendingQuestion != "q-001" {
		t.Fatalf("PendingQuestion = %q, want q-001", report.PendingQuestion)
	}

	bState, err := st.ReadSliceState(fx.Ticket, "b")
	if err != nil {
		t.Fatalf("ReadSliceState(b): %v", err)
	}
	if bState.State != "green" || bState.Attempts != 2 {
		t.Fatalf("b state = %+v, want green/attempts=2 (code-bug then retry)", bState)
	}

	lines, err := journal.Read(st, fx.Ticket)
	if err != nil {
		t.Fatalf("journal.Read: %v", err)
	}
	var dispatches, results int
	for _, l := range lines {
		switch l.Event {
		case "dispatch":
			dispatches++
		case "result":
			results++
		}
	}
	if dispatches == 0 || results == 0 {
		t.Fatalf("expected dispatch/result journal lines, got dispatches=%d results=%d", dispatches, results)
	}

	repoName := d.Cfg.Repos[0].Name()
	shaPath := filepath.Join(st.TicketDir(fx.Ticket), "start."+repoName+".sha")
	gotSHA, err := os.ReadFile(shaPath)
	if err != nil {
		t.Fatalf("read %s: %v", shaPath, err)
	}
	wantSHA, err := gitx.RevParse(fx.RepoDir, "main")
	if err != nil {
		t.Fatalf("RevParse: %v", err)
	}
	if strings.TrimSpace(string(gotSHA)) != wantSHA {
		t.Fatalf("start sha = %q, want origin/main sha %q", gotSHA, wantSHA)
	}
}

// TestRunUsesRecordedBranchForBuildLease covers frontier's own
// resolve-and-acquire call site (processSlice): with a branch recorded on
// the ticket, the build lease pool.Acquire creates is checked out on that
// branch, not the "jig/<ticket>" default - proof frontier's own
// TicketBranch call site is exercised, since no other frontier test here
// ever records one, so reverting that call site to a hardcoded
// "jig/"+ticket would still leave every one of them green. A recorded branch
// is on origin by definition, so the test puts it there, at the target's tip.
func TestRunUsesRecordedBranchForBuildLease(t *testing.T) {
	fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
	d, st := newDeps(t, fx)
	if err := st.WriteTicketBranch(fx.Ticket, "feature/custom"); err != nil {
		t.Fatalf("WriteTicketBranch: %v", err)
	}
	runGitT(t, fx.RepoRemote, "branch", "feature/custom", "main")

	if _, err := Run(d, RunOpts{Ticket: fx.Ticket}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	repoName := d.Cfg.Repos[0].Name()
	leaseDir, err := pool.Dir(d.Home, repoName, fx.Ticket, pool.Build)
	if err != nil {
		t.Fatalf("pool.Dir: %v", err)
	}
	head, err := gitx.Run(leaseDir, "symbolic-ref", "--short", "HEAD")
	if err != nil {
		t.Fatalf("symbolic-ref: %v", err)
	}
	if head != "feature/custom" {
		t.Fatalf("build lease branch = %q, want the recorded %q", head, "feature/custom")
	}
}

// TestRunFailsWithTheRefusalOfTicketBranch covers the other side of
// TestRunUsesRecordedBranchForBuildLease: whatever Store.TicketBranch refuses
// is the run's own failure, with the refusal's code, and no build lease is
// created for it. The two refusals a ticket.yaml can earn are a recorded
// branch equal to the target (the one branch a build must never land on) and a
// record jig cannot read (here one a newer jig wrote, which may name a branch
// this jig does not understand). A Run that fell back to "jig/<ticket>" on
// either would build there instead, which is the silent fallback the refusals
// exist to prevent; a fallback that tells the two apart and keeps only one
// refusal is still one.
func TestRunFailsWithTheRefusalOfTicketBranch(t *testing.T) {
	for _, tc := range []struct {
		name     string
		record   func(t *testing.T, st *store.Store, ticket string)
		wantCode string
	}{
		{
			name: "recorded branch equal to the target",
			record: func(t *testing.T, st *store.Store, ticket string) {
				if err := st.WriteTicketBranch(ticket, "main"); err != nil {
					t.Fatalf("WriteTicketBranch: %v", err)
				}
			},
			wantCode: "TICKET_BRANCH_INVALID",
		},
		{
			// Written with os.WriteFile: WriteTicketBranch refuses a
			// record of a schema this jig cannot read.
			name: "record of a newer schema",
			record: func(t *testing.T, st *store.Store, ticket string) {
				if err := os.WriteFile(filepath.Join(st.TicketDir(ticket), "ticket.yaml"), []byte("schema_version: 2\n"), 0o644); err != nil {
					t.Fatalf("write the newer-schema record: %v", err)
				}
			},
			wantCode: "TICKET_SCHEMA_UNSUPPORTED",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
			d, st := newDeps(t, fx)
			tc.record(t, st, fx.Ticket)
			var ae *axi.Error
			if _, err := st.TicketBranch(fx.Ticket, "main"); !errors.As(err, &ae) || ae.Code != tc.wantCode {
				t.Fatalf("test setup: TicketBranch err = %v, want an *axi.Error %s", err, tc.wantCode)
			}

			_, err := Run(d, RunOpts{Ticket: fx.Ticket})
			ae = nil
			if !errors.As(err, &ae) || ae.Code != tc.wantCode {
				t.Fatalf("Run over a ticket.yaml TicketBranch refuses: err = %v, want an *axi.Error %s", err, tc.wantCode)
			}

			leaseDir, err := pool.Dir(d.Home, d.Cfg.Repos[0].Name(), fx.Ticket, pool.Build)
			if err != nil {
				t.Fatalf("pool.Dir: %v", err)
			}
			if _, err := os.Stat(leaseDir); !os.IsNotExist(err) {
				t.Fatalf("the refused Run left a build lease at %s (stat err %v), want none", leaseDir, err)
			}
		})
	}
}

// TestRunResolvesTheBranchOncePerRun covers Store.TicketBranch's rule for a
// command that names the branch more than once: one Run builds every slice
// on the branch it resolved first, even when ticket.yaml records another
// one mid-run (the first dispatch's journal line stands in for whatever
// writes it). A Run that resolved the branch per slice attempt would put
// the later slices, and the build lease's HEAD, on feature/mid-run.
func TestRunResolvesTheBranchOncePerRun(t *testing.T) {
	fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
	d, st := newDeps(t, fx)
	journalTo := d.Journal
	var record sync.Once
	d.Journal = func(l journal.Line) error {
		if l.Event == "dispatch" {
			record.Do(func() {
				if err := st.WriteTicketBranch(fx.Ticket, "feature/mid-run"); err != nil {
					t.Errorf("WriteTicketBranch mid-run: %v", err)
				}
			})
		}
		return journalTo(l)
	}

	report, err := Run(d, RunOpts{Ticket: fx.Ticket})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(report.Green) < 2 {
		t.Fatalf("Green = %v, want at least two slices built, one before the branch was recorded and one after", report.Green)
	}
	if got, err := st.TicketBranch(fx.Ticket, "main"); err != nil || got != "feature/mid-run" {
		t.Fatalf("TicketBranch after the run = %q, %v, want the branch recorded mid-run", got, err)
	}

	leaseDir, err := pool.Dir(d.Home, d.Cfg.Repos[0].Name(), fx.Ticket, pool.Build)
	if err != nil {
		t.Fatalf("pool.Dir: %v", err)
	}
	head, err := gitx.Run(leaseDir, "symbolic-ref", "--short", "HEAD")
	if err != nil {
		t.Fatalf("symbolic-ref: %v", err)
	}
	if want := "jig/" + fx.Ticket; head != want {
		t.Fatalf("build lease branch = %q, want %q, the branch the run resolved before it was recorded", head, want)
	}
	if got, err := gitx.Run(leaseDir, "branch", "--list", "feature/mid-run"); err != nil || got != "" {
		t.Fatalf("build lease has branch %q (err %v), want none: no slice may have resolved the branch recorded mid-run", got, err)
	}
}

// TestRunWithNothingToBuildNeverReadsTheTicketRecord covers the other half of
// resolving the branch once: it happens on the first slice attempt, not up
// front, so a Run whose frontier is empty (every slice already green, so
// only a report is due) still succeeds over a ticket.yaml it cannot read.
func TestRunWithNothingToBuildNeverReadsTheTicketRecord(t *testing.T) {
	fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
	d, st := newDeps(t, fx)
	slices, err := st.ReadSlices(fx.Ticket)
	if err != nil {
		t.Fatalf("ReadSlices: %v", err)
	}
	for _, sl := range slices {
		if err := st.WriteSliceState(fx.Ticket, sl.ID, store.SliceState{State: "green"}); err != nil {
			t.Fatalf("WriteSliceState(%s): %v", sl.ID, err)
		}
	}
	record := filepath.Join(st.TicketDir(fx.Ticket), "ticket.yaml")
	if err := os.WriteFile(record, []byte("schema_version: 1\nfrobnicate: yes\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := st.ReadTicket(fx.Ticket); err == nil {
		t.Fatal("test setup: ticket.yaml with an unknown key must be unreadable")
	}

	report, err := Run(d, RunOpts{Ticket: fx.Ticket})
	if err != nil {
		t.Fatalf("Run with every slice green over an unreadable ticket.yaml: %v", err)
	}
	if len(report.Green) != len(slices) {
		t.Fatalf("Green = %v, want all %d slices", report.Green, len(slices))
	}
}

func TestAnswerResume(t *testing.T) {
	fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
	d, st := newDeps(t, fx)

	if _, err := Run(d, RunOpts{Ticket: fx.Ticket}); err != nil {
		t.Fatalf("first Run: %v", err)
	}

	report, err := Run(d, RunOpts{Ticket: fx.Ticket, AnswerQID: "q-001", AnswerText: "Casual."})
	if err != nil {
		t.Fatalf("second Run: %v", err)
	}

	if len(report.Green) != 4 {
		t.Fatalf("Green = %v, want all 4 slices green", report.Green)
	}
	if report.PendingQuestion != "" {
		t.Fatalf("PendingQuestion = %q, want none", report.PendingQuestion)
	}
	if report.Stopped {
		t.Fatalf("Stopped = true, want false")
	}

	cState, err := st.ReadSliceState(fx.Ticket, "c")
	if err != nil {
		t.Fatalf("ReadSliceState(c): %v", err)
	}
	if cState.State != "green" {
		t.Fatalf("c state = %s, want green", cState.State)
	}
}

// TestAnswerAndRequeueClearsStalledSignature: answering a question and
// requeuing its slice must clear that slice's stall Signature and
// StallSummary alongside Reason, not just Reason - a stale signature or
// summary left behind by an earlier stall must not survive an
// answer-and-requeue. Signature and StallSummary are seeded directly onto
// the parked slice (independent of how it reached its needs-input state),
// and answerAndRequeue is called directly rather than through a second Run:
// driving a full Run risks the slice landing green in that same call, whose
// own green-route clear (a separate, already pinned mutant) would then mask
// a regression in answerAndRequeue's own clear.
func TestAnswerAndRequeueClearsStalledSignature(t *testing.T) {
	fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
	d, st := newDeps(t, fx)

	if _, err := Run(d, RunOpts{Ticket: fx.Ticket}); err != nil {
		t.Fatalf("first Run: %v", err)
	}

	qs, err := st.ReadQuestions(fx.Ticket)
	if err != nil {
		t.Fatalf("ReadQuestions: %v", err)
	}
	var q *store.Question
	for i := range qs {
		if qs[i].Status == "open" {
			q = &qs[i]
		}
	}
	if q == nil {
		t.Fatalf("no open question after first Run")
	}

	parked, err := st.ReadSliceState(fx.Ticket, q.Slice)
	if err != nil {
		t.Fatalf("ReadSliceState(%s): %v", q.Slice, err)
	}
	parked.Signature = q.Slice + "|code-bug|stale signature from a previous stall"
	parked.StallSummary = "stale summary from a previous stall"
	if err := st.WriteSliceState(fx.Ticket, q.Slice, parked); err != nil {
		t.Fatalf("write slice state: %v", err)
	}

	if err := answerAndRequeue(d, fx.Ticket, q.ID, "Casual."); err != nil {
		t.Fatalf("answerAndRequeue: %v", err)
	}

	after, err := st.ReadSliceState(fx.Ticket, q.Slice)
	if err != nil {
		t.Fatalf("ReadSliceState(%s) after answer: %v", q.Slice, err)
	}
	if after.State != "queued" {
		t.Fatalf("%s State = %q, want queued", q.Slice, after.State)
	}
	if after.Signature != "" {
		t.Fatalf("%s Signature = %q, want cleared by answerAndRequeue", q.Slice, after.Signature)
	}
	if after.StallSummary != "" {
		t.Fatalf("%s StallSummary = %q, want cleared by answerAndRequeue", q.Slice, after.StallSummary)
	}
}

func TestScheduleUsedByRunSingleRepoFixture(t *testing.T) {
	// The default fixture is a single-repo project, so Run's own use of
	// Schedule always exercises the serial (one-group) path; TestSchedule*
	// above covers the concurrent path directly against the pure function.
	fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
	d, _ := newDeps(t, fx)
	if len(d.Cfg.Repos) != 1 {
		t.Fatalf("fixture project has %d repos, want 1", len(d.Cfg.Repos))
	}
}

func TestAttemptCapExhaustion(t *testing.T) {
	fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir(), ScenarioBranch: "cap"})
	d, st := newDeps(t, fx)

	report, err := Run(d, RunOpts{Ticket: fx.Ticket})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if !report.Stopped {
		t.Fatal("Stopped = false, want true")
	}
	if !strings.Contains(report.StopReason, "attempt-cap") {
		t.Fatalf("StopReason = %q, want it to mention attempt-cap", report.StopReason)
	}

	aState, err := st.ReadSliceState(fx.Ticket, "a")
	if err != nil {
		t.Fatalf("ReadSliceState(a): %v", err)
	}
	if aState.State != "stalled" || aState.Reason != "attempt-cap" || aState.Attempts != 3 {
		t.Fatalf("a state = %+v, want stalled/attempt-cap after 3 attempts", aState)
	}
	if aState.Signature == "" {
		t.Fatalf("a state = %+v, want a non-empty stall signature on the attempt-cap path", aState)
	}
	if aState.StallSummary == "" {
		t.Fatalf("a state = %+v, want a non-empty stall summary on the attempt-cap path", aState)
	}

	bState, err := st.ReadSliceState(fx.Ticket, "b")
	if err != nil {
		t.Fatalf("ReadSliceState(b): %v", err)
	}
	if bState.State != "queued" || bState.Attempts != 0 {
		t.Fatalf("b = %+v, want untouched (blocked on a, which never went green)", bState)
	}
}

func TestStallStops(t *testing.T) {
	fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir(), ScenarioBranch: "stall"})
	d, st := newDeps(t, fx)

	report, err := Run(d, RunOpts{Ticket: fx.Ticket})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if !report.Stopped {
		t.Fatal("Stopped = false, want true")
	}
	if !strings.Contains(report.StopReason, "twice") && !strings.Contains(report.StopReason, "misconception") {
		t.Fatalf("StopReason = %q, want the framed stall message", report.StopReason)
	}

	aState, err := st.ReadSliceState(fx.Ticket, "a")
	if err != nil {
		t.Fatalf("ReadSliceState(a): %v", err)
	}
	if aState.State != "stalled" || aState.Reason != "stall" {
		t.Fatalf("a state = %+v, want stalled/stall", aState)
	}
	if aState.Signature == "" {
		t.Fatalf("a state = %+v, want a non-empty stall signature on the repeat-failure stall path", aState)
	}
	if aState.StallSummary == "" {
		t.Fatalf("a state = %+v, want a non-empty stall summary on the repeat-failure stall path", aState)
	}

	bState, err := st.ReadSliceState(fx.Ticket, "b")
	if err != nil {
		t.Fatalf("ReadSliceState(b): %v", err)
	}
	if bState.State != "queued" || bState.Attempts != 0 {
		t.Fatalf("b = %+v, want not dispatched", bState)
	}
}

func TestRequeueFromBriefDiff(t *testing.T) {
	fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir(), ScenarioBranch: "flawed-brief"})
	d, st := newDeps(t, fx)

	report, err := Run(d, RunOpts{Ticket: fx.Ticket})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(report.NeedsInput) != 1 || report.NeedsInput[0] != "c" {
		t.Fatalf("NeedsInput = %v, want [c]", report.NeedsInput)
	}

	cState, err := st.ReadSliceState(fx.Ticket, "c")
	if err != nil {
		t.Fatalf("ReadSliceState(c): %v", err)
	}
	if cState.Reason != "flawed-brief" || cState.Attempts != 1 {
		t.Fatalf("c state = %+v, want reason=flawed-brief attempts=1", cState)
	}

	qs, err := st.ReadQuestions(fx.Ticket)
	if err != nil {
		t.Fatalf("ReadQuestions: %v", err)
	}
	var q *store.Question
	for i := range qs {
		if qs[i].Slice == "c" {
			q = &qs[i]
		}
	}
	if q == nil || !strings.Contains(strings.ToLower(q.Body), "brief") {
		t.Fatalf("question for c = %+v, want its body to mention the brief", q)
	}

	amended, err := os.ReadFile(filepath.Join(fixture.RepoRoot(t), "testdata", "fixture", "scenario-branches", "flawed-brief", "brief-amended.md"))
	if err != nil {
		t.Fatalf("read brief-amended.md: %v", err)
	}
	if err := os.WriteFile(filepath.Join(st.TicketDir(fx.Ticket), "brief.md"), amended, 0o644); err != nil {
		t.Fatalf("write amended brief.md: %v", err)
	}

	touched, err := Requeue(d, fx.Ticket, true)
	if err != nil {
		t.Fatalf("Requeue: %v", err)
	}
	if len(touched) != 1 || touched[0] != "c" {
		t.Fatalf("Requeue touched = %v, want exactly [c]", touched)
	}

	cState, err = st.ReadSliceState(fx.Ticket, "c")
	if err != nil {
		t.Fatalf("ReadSliceState(c) after requeue: %v", err)
	}
	if cState.State != "queued" || cState.Attempts != 1 || cState.Reason != "" || cState.Question != "" {
		t.Fatalf("c after requeue = %+v, want queued/attempts=1 (kept)/reason+question cleared", cState)
	}

	report2, err := Run(d, RunOpts{Ticket: fx.Ticket})
	if err != nil {
		t.Fatalf("second Run: %v", err)
	}
	if !containsID(report2.Green, "c") {
		t.Fatalf("Green = %v, want c green via its attempt-2", report2.Green)
	}

	cState, err = st.ReadSliceState(fx.Ticket, "c")
	if err != nil {
		t.Fatalf("ReadSliceState(c) after second run: %v", err)
	}
	if cState.Attempts != 2 {
		t.Fatalf("c attempts = %d, want 2 (attempt-2 landed it green)", cState.Attempts)
	}
}

// TestRequeueClearsStalledSignature: Requeue must clear a touched slice's
// stall Signature and StallSummary alongside Reason, not just Reason - a
// stale signature or summary left behind by an earlier stall must not
// survive a requeue driven by Requeue itself. Signature and StallSummary
// are seeded directly (independent of how c reached its flawed-brief state)
// so this fails on its own if Requeue's own clear is removed.
func TestRequeueClearsStalledSignature(t *testing.T) {
	fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir(), ScenarioBranch: "flawed-brief"})
	d, st := newDeps(t, fx)

	if _, err := Run(d, RunOpts{Ticket: fx.Ticket}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	cState, err := st.ReadSliceState(fx.Ticket, "c")
	if err != nil {
		t.Fatalf("ReadSliceState(c): %v", err)
	}
	cState.Signature = "c|code-bug|stale signature from a previous stall"
	cState.StallSummary = "stale summary from a previous stall"
	if err := st.WriteSliceState(fx.Ticket, "c", cState); err != nil {
		t.Fatalf("write slice state: %v", err)
	}

	amended, err := os.ReadFile(filepath.Join(fixture.RepoRoot(t), "testdata", "fixture", "scenario-branches", "flawed-brief", "brief-amended.md"))
	if err != nil {
		t.Fatalf("read brief-amended.md: %v", err)
	}
	if err := os.WriteFile(filepath.Join(st.TicketDir(fx.Ticket), "brief.md"), amended, 0o644); err != nil {
		t.Fatalf("write amended brief.md: %v", err)
	}

	touched, err := Requeue(d, fx.Ticket, true)
	if err != nil {
		t.Fatalf("Requeue: %v", err)
	}
	if len(touched) != 1 || touched[0] != "c" {
		t.Fatalf("Requeue touched = %v, want exactly [c]", touched)
	}

	after, err := st.ReadSliceState(fx.Ticket, "c")
	if err != nil {
		t.Fatalf("ReadSliceState(c) after requeue: %v", err)
	}
	if after.Signature != "" {
		t.Fatalf("c Signature = %q, want cleared by Requeue", after.Signature)
	}
	if after.StallSummary != "" {
		t.Fatalf("c StallSummary = %q, want cleared by Requeue", after.StallSummary)
	}
}

// TestRequeueSliceStalled: RequeueSlice on a stalled slice (no brief section
// involved - the "stall" branch stalls slice a on a repeat code-bug, not a
// brief question) puts it back to queued with its attempts kept and its
// reason, signature and stall summary cleared, and a second Run then
// dispatches it again.
func TestRequeueSliceStalled(t *testing.T) {
	fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir(), ScenarioBranch: "stall"})
	d, st := newDeps(t, fx)

	if _, err := Run(d, RunOpts{Ticket: fx.Ticket}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	before, err := st.ReadSliceState(fx.Ticket, "a")
	if err != nil {
		t.Fatalf("ReadSliceState(a): %v", err)
	}
	if before.State != "stalled" {
		t.Fatalf("a state = %+v, want stalled (test setup is wrong)", before)
	}

	if err := RequeueSlice(d, fx.Ticket, "a"); err != nil {
		t.Fatalf("RequeueSlice: %v", err)
	}

	after, err := st.ReadSliceState(fx.Ticket, "a")
	if err != nil {
		t.Fatalf("ReadSliceState(a) after RequeueSlice: %v", err)
	}
	if after.State != "queued" || after.Attempts != before.Attempts {
		t.Fatalf("a after RequeueSlice = %+v, want queued/attempts=%d (kept)", after, before.Attempts)
	}
	if after.Reason != "" || after.Signature != "" || after.StallSummary != "" {
		t.Fatalf("a after RequeueSlice = %+v, want reason/signature/stall summary cleared", after)
	}

	lines, err := journal.Read(st, fx.Ticket)
	if err != nil {
		t.Fatalf("journal.Read: %v", err)
	}
	sawRequeue := false
	for _, l := range lines {
		if l.Slice == "a" && l.Event == "requeue" {
			sawRequeue = true
		}
	}
	if !sawRequeue {
		t.Fatalf("no requeue journal line for slice a; journal:\n%+v", lines)
	}
}

// TestRequeueSliceEnvBlocked: RequeueSlice on an env-blocked slice puts it
// back to queued the same way it does for stalled.
func TestRequeueSliceEnvBlocked(t *testing.T) {
	fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir(), EnvFail: true})
	d, st := newDeps(t, fx)

	if _, err := Run(d, RunOpts{Ticket: fx.Ticket}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	before, err := st.ReadSliceState(fx.Ticket, "d")
	if err != nil {
		t.Fatalf("ReadSliceState(d): %v", err)
	}
	if before.State != "env-blocked" {
		t.Fatalf("d state = %+v, want env-blocked (test setup is wrong)", before)
	}

	if err := RequeueSlice(d, fx.Ticket, "d"); err != nil {
		t.Fatalf("RequeueSlice: %v", err)
	}

	after, err := st.ReadSliceState(fx.Ticket, "d")
	if err != nil {
		t.Fatalf("ReadSliceState(d) after RequeueSlice: %v", err)
	}
	if after.State != "queued" || after.Attempts != before.Attempts {
		t.Fatalf("d after RequeueSlice = %+v, want queued/attempts=%d (kept)", after, before.Attempts)
	}
	if after.Reason != "" {
		t.Fatalf("d after RequeueSlice = %+v, want reason cleared", after)
	}
}

// TestRequeueSliceRefusesOtherStates: RequeueSlice refuses a slice that is
// neither stalled nor env-blocked (queued, building, green or needs-input),
// since it cannot tell a caller's mistake from a stale id, and it must not
// silently repark an unrelated slice.
func TestRequeueSliceRefusesOtherStates(t *testing.T) {
	fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
	d, st := newDeps(t, fx)

	for _, state := range []string{"queued", "building", "green", "needs-input"} {
		t.Run(state, func(t *testing.T) {
			if err := st.WriteSliceState(fx.Ticket, "a", store.SliceState{State: state, Attempts: 1}); err != nil {
				t.Fatalf("write slice state: %v", err)
			}
			if err := RequeueSlice(d, fx.Ticket, "a"); err == nil {
				t.Fatalf("RequeueSlice on a %s slice: err = nil, want a refusal", state)
			}
			after, err := st.ReadSliceState(fx.Ticket, "a")
			if err != nil {
				t.Fatalf("ReadSliceState(a): %v", err)
			}
			if after.State != state {
				t.Fatalf("a state after refused RequeueSlice = %q, want unchanged %q", after.State, state)
			}
		})
	}
}

// TestRequeueSliceUnknownID: RequeueSlice on an id that names no slice in
// the ticket's own slices.yaml refuses with its own code naming the unknown
// id, rather than reading the absent state file's zero value as a real
// "queued" slice and reporting a wrong-state refusal for a slice that was
// never there.
func TestRequeueSliceUnknownID(t *testing.T) {
	fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
	d, _ := newDeps(t, fx)

	err := RequeueSlice(d, fx.Ticket, "no-such-slice")
	if err == nil {
		t.Fatal("RequeueSlice on an unknown id: err = nil, want a refusal")
	}
	var ae *axi.Error
	if !errors.As(err, &ae) || ae.Code != "SLICE_NOT_FOUND" {
		t.Fatalf("RequeueSlice on an unknown id: err = %v, want *axi.Error SLICE_NOT_FOUND", err)
	}
	if !strings.Contains(ae.Msg, "no-such-slice") {
		t.Fatalf("RequeueSlice on an unknown id: Msg = %q, want it to name the unknown id", ae.Msg)
	}
	if strings.Contains(ae.Msg, "queued") {
		t.Fatalf("RequeueSlice on an unknown id: Msg = %q, still claims a state", ae.Msg)
	}
}

func TestEnvPauseDeferCI(t *testing.T) {
	fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir(), EnvFail: true})
	d, st := newDeps(t, fx)

	report, err := Run(d, RunOpts{Ticket: fx.Ticket})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if !containsID(report.EnvBlocked, "d") {
		t.Fatalf("EnvBlocked = %v, want it to contain d", report.EnvBlocked)
	}
	if report.Stopped {
		t.Fatal("Stopped = true, want false (an env pause is not a stop)")
	}

	dState, err := st.ReadSliceState(fx.Ticket, "d")
	if err != nil {
		t.Fatalf("ReadSliceState(d): %v", err)
	}
	if dState.State != "env-blocked" || dState.Reason != "env-up-failed" {
		t.Fatalf("d state = %+v, want env-blocked/env-up-failed", dState)
	}

	lines, err := journal.Read(st, fx.Ticket)
	if err != nil {
		t.Fatalf("journal.Read: %v", err)
	}
	sawDeferCI := false
	for _, l := range lines {
		if l.Slice == "d" && l.Event == "env-unavailable" && l.Outcome == "defer-ci" {
			sawDeferCI = true
		}
	}
	if !sawDeferCI {
		t.Fatal("expected a journal line: slice=d event=env-unavailable outcome=defer-ci")
	}
}

func TestOracleWrongRoundTrip(t *testing.T) {
	fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir(), ScenarioBranch: "oracle-wrong"})
	d, st := newDeps(t, fx)

	report, err := Run(d, RunOpts{Ticket: fx.Ticket})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if !containsID(report.Green, "a") {
		t.Fatalf("Green = %v, want it to contain a", report.Green)
	}

	aState, err := st.ReadSliceState(fx.Ticket, "a")
	if err != nil {
		t.Fatalf("ReadSliceState(a): %v", err)
	}
	if aState.Attempts != 2 {
		t.Fatalf("a attempts = %d, want 2 (oracle-wrong then green)", aState.Attempts)
	}

	lines, err := journal.Read(st, fx.Ticket)
	if err != nil {
		t.Fatalf("journal.Read: %v", err)
	}
	sawOracleWrong := false
	for _, l := range lines {
		if l.Slice == "a" && l.Event == "result" && l.Outcome == "oracle-wrong" && l.Attempt == 1 {
			sawOracleWrong = true
		}
	}
	if !sawOracleWrong {
		t.Fatal("expected a result line: slice=a outcome=oracle-wrong attempt=1")
	}
}

// initTestGitRepo creates a minimal git repo at dir with an initial commit,
// returning that commit's sha.
func initTestGitRepo(t *testing.T, dir string) string {
	t.Helper()
	runGitT(t, dir, "init", "-b", "main")
	runGitT(t, dir, "config", "user.email", "fixture@example.invalid")
	runGitT(t, dir, "config", "user.name", "jig-fixture")
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("one"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	runGitT(t, dir, "add", "-A")
	runGitT(t, dir, "commit", "-m", "init")
	return runGitT(t, dir, "rev-parse", "HEAD")
}

func runGitT(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := gitx.Run(dir, args...)
	if err != nil {
		t.Fatalf("git %v: %v", args, err)
	}
	return out
}

// newTestStore opens a Store rooted at a fresh git repo with a bare
// project.yaml: enough for WriteSliceState/Push (no remote configured, so
// Push commits locally and never touches the network).
func newTestStore(t *testing.T) *store.Store {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "project.yaml"), []byte("name: t\n"), 0o644); err != nil {
		t.Fatalf("write project.yaml: %v", err)
	}
	runGitT(t, dir, "init", "-b", "main")
	st, err := store.Open(dir)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	return st
}

// TestBringUpEnvHandleUsedForTeardown covers m0: bringUpEnv must return the
// *envrun.Handle Up allocated, and tearDownEnv must tear down THAT instance
// (in particular its allocated port), not a fabricated zero-value one. The
// env class's down command writes the port it was substituted to a file, so
// the test can tell a real allocated port from the bug's hardcoded 0.
func TestBringUpEnvHandleUsedForTeardown(t *testing.T) {
	dir := t.TempDir()
	portFile := filepath.Join(dir, "port.txt")

	ec := manifest.EnvClass{
		Up:    "echo up",
		Check: "echo check",
		// The space before ">" matters under sh -c: "echo {port}>FILE" with
		// no space parses the bare number immediately before ">" as a file
		// descriptor (echo runs with fd <port> redirected, and the file
		// stays empty) rather than as the redirection target.
		Down: fmt.Sprintf("echo {port} > %s", portFile),
	}
	m := manifest.Manifest{Envs: map[string]manifest.EnvClass{"e": ec}}
	sl := store.Slice{ID: "s1", Env: "e"}
	lease := pool.Lease{Dir: dir}

	rc := &runCtx{
		d:      Deps{Journal: func(journal.Line) error { return nil }},
		ticket: "T",
	}

	h, ok := rc.bringUpEnv(sl, m, lease)
	if !ok || h == nil {
		t.Fatalf("bringUpEnv = (%v, %v), want a handle and ok=true", h, ok)
	}
	if h.Port == 0 {
		t.Fatal("handle Port = 0, want the allocated port")
	}

	rc.tearDownEnv(sl, h)

	data, err := os.ReadFile(portFile)
	if err != nil {
		t.Fatalf("read port file: %v", err)
	}
	gotPort := strings.TrimSpace(string(data))
	wantPort := fmt.Sprintf("%d", h.Port)
	if gotPort != wantPort {
		t.Fatalf("teardown substituted port = %q, want the allocated port %q (not 0)", gotPort, wantPort)
	}
}

// TestVerifyGreenRejectsStartSHAItself covers m4: a claimed-green commit
// that IS the run's recorded start sha (no new work at all) must not
// verify, even though the commit exists in the lease. Driven through
// runCtx.route (not verifyGreen directly) so the assertion also proves the
// failure is what actually reaches routeFailure/the journal.
func TestVerifyGreenRejectsStartSHAItself(t *testing.T) {
	dir := t.TempDir()
	startSHA := initTestGitRepo(t, dir)

	st := newTestStore(t)
	rc := &runCtx{
		d:           Deps{Store: st, Journal: func(journal.Line) error { return nil }},
		ticket:      "T",
		maxAttempts: 1, // attempt 1 hits the attempt-cap branch immediately
	}
	sl := store.Slice{ID: "a"}
	lease := pool.Lease{Dir: dir}
	res := outcome.Result{Outcome: outcome.Green, Commit: startSHA}

	rc.route(sl, lease, 1, res, startSHA)

	_, reason := rc.stopState()
	if !strings.Contains(reason, "green did not verify") {
		t.Fatalf("StopReason = %q, want it to say green did not verify", reason)
	}

	aState, err := st.ReadSliceState("T", "a")
	if err != nil {
		t.Fatalf("ReadSliceState: %v", err)
	}
	if aState.State != "stalled" || aState.Reason != "attempt-cap" {
		t.Fatalf("a state = %+v, want stalled/attempt-cap (verify-green failure routed as a normal failure)", aState)
	}
}

// TestRouteJournalsTheCommitThatVerified: a green result that verifies leaves a
// verified line naming its commit - the record of a commit jig built
// (journal.BuiltCommits) - and one that does not leaves none, whatever
// rejected it. The result line, journaled before verification, names the
// builder's claim either way, which is why it cannot be the record.
func TestRouteJournalsTheCommitThatVerified(t *testing.T) {
	dir := t.TempDir()
	startSHA := initTestGitRepo(t, dir)
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("two"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	runGitT(t, dir, "commit", "-am", "second")
	second := runGitT(t, dir, "rev-parse", "HEAD")
	// A commit on another line, off the lease's HEAD.
	runGitT(t, dir, "checkout", "-b", "side", startSHA)
	if err := os.WriteFile(filepath.Join(dir, "side.txt"), []byte("side"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	runGitT(t, dir, "add", "-A")
	runGitT(t, dir, "commit", "-m", "side")
	side := runGitT(t, dir, "rev-parse", "HEAD")
	runGitT(t, dir, "checkout", "main")

	for _, tc := range []struct {
		name     string
		start    string
		res      outcome.Result
		verified bool
	}{
		{"a commit that verifies", startSHA, outcome.Result{Outcome: outcome.Green, Commit: second}, true},
		{"the start sha itself", startSHA, outcome.Result{Outcome: outcome.Green, Commit: startSHA}, false},
		{"a sha that is not in the lease", startSHA, outcome.Result{Outcome: outcome.Green, Commit: "0123456789abcdef0123456789abcdef01234567"}, false},
		{"a commit that does not descend from the start sha", second, outcome.Result{Outcome: outcome.Green, Commit: startSHA}, false},
		{"a commit off the lease's HEAD", startSHA, outcome.Result{Outcome: outcome.Green, Commit: side}, false},
		{"a commit missing a declared artifact", startSHA, outcome.Result{Outcome: outcome.Green, Commit: second, Artifacts: []string{"nowhere.txt"}}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var lines []journal.Line
			rc := &runCtx{
				d: Deps{Store: newTestStore(t), Journal: func(l journal.Line) error {
					lines = append(lines, l)
					return nil
				}},
				ticket:      "T",
				maxAttempts: 1,
			}

			rc.route(store.Slice{ID: "a"}, pool.Lease{Dir: dir}, 1, tc.res, tc.start)

			got := journal.BuiltCommits(lines)
			if tc.verified {
				if len(got) != 1 || got[0] != tc.res.Commit {
					t.Fatalf("BuiltCommits = %v, want [%s]", got, tc.res.Commit)
				}
				if last := lines[len(lines)-1]; last.Event != "verified" || last.Slice != "a" || last.Attempt != 1 {
					t.Fatalf("last journal line = %+v, want a's verified line for attempt 1", last)
				}
			} else if len(got) != 0 {
				t.Fatalf("BuiltCommits = %v, want none: the commit did not verify", got)
			}
		})
	}
}

// TestRouteNeverMarksASliceGreenWhenItsVerifiedLineFails: the verified line
// goes in before the slice is marked green, so a run that stops between the two
// writes has the commit recorded (journal.BuiltCommits). Written the other way
// round, a journal that fails on the verified line would leave a green slice
// whose commit is not among the commits jig built, and the next diverged build
// acquire on an adopted ticket would re-cut that commit away while the slice
// stays green. The run stops on the journal error, and the slice is not green.
func TestRouteNeverMarksASliceGreenWhenItsVerifiedLineFails(t *testing.T) {
	dir := t.TempDir()
	startSHA := initTestGitRepo(t, dir)
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("two"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	runGitT(t, dir, "commit", "-am", "second")
	commit := runGitT(t, dir, "rev-parse", "HEAD")

	st := newTestStore(t)
	var events []string
	rc := &runCtx{
		d: Deps{Store: st, Journal: func(l journal.Line) error {
			events = append(events, l.Event)
			if l.Event == "verified" {
				return errors.New("the journal is unwritable")
			}
			return nil
		}},
		ticket:      "T",
		maxAttempts: 1,
	}

	rc.route(store.Slice{ID: "a"}, pool.Lease{Dir: dir}, 1, outcome.Result{Outcome: outcome.Green, Commit: commit}, startSHA)

	if err := rc.firstErr(); err == nil || !strings.Contains(err.Error(), "verified") {
		t.Fatalf("run error = %v, want the verified line's journal failure", err)
	}
	if !rc.isHalted() {
		t.Fatal("the run kept going after its journal failed")
	}
	if got := strings.Join(events, ","); got != "verified" {
		t.Fatalf("journal events = %q, want the verified line attempted, and nothing else", got)
	}
	state, err := st.ReadSliceState("T", "a")
	if err != nil {
		t.Fatalf("ReadSliceState: %v", err)
	}
	if state.State == "green" {
		t.Fatalf("slice a is green (%+v) although its verified line was never journaled", state)
	}
}

// TestRouteGreenClearsStalledSignature: a slice that was stalled (and so
// carries a stall signature and summary), then requeued and dispatched
// again, must have both cleared once it lands green - a stale signature or
// summary must never survive a slice's eventual recovery. Driven through
// runCtx.route with the state seeded exactly as a stalled-then-requeued
// slice would look on disk (queued, no Reason, but still a Signature and
// StallSummary from before the requeue), so this fails on its own if the
// green route ever stops clearing them, independent of
// Requeue's/answerAndRequeue's own clearing.
func TestRouteGreenClearsStalledSignature(t *testing.T) {
	dir := t.TempDir()
	startSHA := initTestGitRepo(t, dir)
	if err := os.WriteFile(filepath.Join(dir, "f.txt"), []byte("two"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	runGitT(t, dir, "commit", "-am", "second")
	commit := runGitT(t, dir, "rev-parse", "HEAD")

	st := newTestStore(t)
	if err := st.WriteSliceState("T", "a", store.SliceState{
		State:        "queued",
		Attempts:     1,
		Signature:    "a|code-bug|stale signature from a previous stall",
		StallSummary: "stale summary from a previous stall",
	}); err != nil {
		t.Fatalf("write slice state: %v", err)
	}

	rc := &runCtx{
		d:           Deps{Store: st, Journal: func(journal.Line) error { return nil }},
		ticket:      "T",
		maxAttempts: 3,
	}
	sl := store.Slice{ID: "a"}
	lease := pool.Lease{Dir: dir}
	res := outcome.Result{Outcome: outcome.Green, Commit: commit}

	rc.route(sl, lease, 2, res, startSHA)

	aState, err := st.ReadSliceState("T", "a")
	if err != nil {
		t.Fatalf("ReadSliceState: %v", err)
	}
	if aState.State != "green" {
		t.Fatalf("a state = %+v, want green", aState)
	}
	if aState.Signature != "" {
		t.Fatalf("a Signature = %q, want cleared on the green route", aState.Signature)
	}
	if aState.StallSummary != "" {
		t.Fatalf("a StallSummary = %q, want cleared on the green route", aState.StallSummary)
	}
}

// TestStallSignatureIsPerSlice covers m13: two different slices that each
// fail once with an identical summary must not stall the run - the stall
// signature's unit must be the slice, not a shared literal context.
func TestStallSignatureIsPerSlice(t *testing.T) {
	fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir(), ScenarioBranch: "same-summary-stall"})
	d, st := newDeps(t, fx)

	report, err := Run(d, RunOpts{Ticket: fx.Ticket})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if report.Stopped {
		t.Fatalf("Stopped = true (reason %q), want false: two different slices failing once each with an identical summary must not stall the run", report.StopReason)
	}

	aState, err := st.ReadSliceState(fx.Ticket, "a")
	if err != nil {
		t.Fatalf("ReadSliceState(a): %v", err)
	}
	cState, err := st.ReadSliceState(fx.Ticket, "c")
	if err != nil {
		t.Fatalf("ReadSliceState(c): %v", err)
	}
	// Both slices' identical-summary first failure must be counted
	// independently: each should still land green on its own attempt 2,
	// rather than the run halting after their shared first failure.
	if aState.State != "green" || aState.Attempts != 2 {
		t.Fatalf("a state = %+v, want green/attempts=2", aState)
	}
	if cState.State != "green" || cState.Attempts != 2 {
		t.Fatalf("c state = %+v, want green/attempts=2", cState)
	}
}

// TestReportStoppedForPreExistingStall covers w1: a run whose final states
// include a stalled slice reports Stopped=true even when that stall
// pre-existed this call (this call's own frontier is empty, so it never
// exercises the stall-detection path itself).
func TestReportStoppedForPreExistingStall(t *testing.T) {
	fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir(), ScenarioBranch: "stall"})
	d, _ := newDeps(t, fx)

	first, err := Run(d, RunOpts{Ticket: fx.Ticket})
	if err != nil {
		t.Fatalf("first Run: %v", err)
	}
	if !first.Stopped {
		t.Fatal("first Run: Stopped = false, want true (the stall fires during this call)")
	}

	second, err := Run(d, RunOpts{Ticket: fx.Ticket})
	if err != nil {
		t.Fatalf("second Run: %v", err)
	}
	if !second.Stopped {
		t.Fatal("second Run: Stopped = false, want true (a pre-existing stalled slice)")
	}
	if !containsID(second.Stalled, "a") {
		t.Fatalf("second Run: Stalled = %v, want it to contain a", second.Stalled)
	}
	if !strings.Contains(second.StopReason, "a") || !strings.Contains(second.StopReason, "stall") {
		t.Fatalf("second Run: StopReason = %q, want it to name slice a and mention the stall", second.StopReason)
	}
}

// recordingBackend runs every dispatch on the backend it wraps and keeps a
// copy of each.
type recordingBackend struct {
	session.Backend
	mu  sync.Mutex
	got []session.Dispatch
}

func (r *recordingBackend) Run(d session.Dispatch) error {
	r.mu.Lock()
	r.got = append(r.got, d)
	r.mu.Unlock()
	return r.Backend.Run(d)
}

// TestRunLeavesSessionPersistenceOnForBuildDispatches: only the intent
// summarizer's dispatch asks its backend to keep no transcript. A build
// session's transcript is the operator's own record of the work and stays.
func TestRunLeavesSessionPersistenceOnForBuildDispatches(t *testing.T) {
	fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
	d, _ := newDeps(t, fx)
	rec := &recordingBackend{Backend: d.Backend}
	d.Backend = rec

	if _, err := Run(d, RunOpts{Ticket: fx.Ticket}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(rec.got) == 0 {
		t.Fatal("the run dispatched no build session")
	}
	for _, disp := range rec.got {
		if disp.NoSessionPersistence {
			t.Errorf("the build dispatch for slice %s attempt %d disables session persistence, want it left on", disp.Slice, disp.Attempt)
		}
	}
}
