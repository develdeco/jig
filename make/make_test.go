package make

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/fixture"
	"github.com/develdeco/jig/gitx"
	"github.com/develdeco/jig/journal"
	"github.com/develdeco/jig/project"
	"github.com/develdeco/jig/session"
	"github.com/develdeco/jig/staircase"
	"github.com/develdeco/jig/store"
)

// newDeps wires make.Deps against a generated fixture, using the fake
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
	t.Setenv("JIG_HOME", t.TempDir())
	fx := fixture.Generate(t, fixture.Opts{})
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

func TestAnswerResume(t *testing.T) {
	t.Setenv("JIG_HOME", t.TempDir())
	fx := fixture.Generate(t, fixture.Opts{})
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

func TestScheduleUsedByRunSingleRepoFixture(t *testing.T) {
	// The default fixture is a single-repo project, so Run's own use of
	// Schedule always exercises the serial (one-group) path; TestSchedule*
	// above covers the concurrent path directly against the pure function.
	t.Setenv("JIG_HOME", t.TempDir())
	fx := fixture.Generate(t, fixture.Opts{})
	d, _ := newDeps(t, fx)
	if len(d.Cfg.Repos) != 1 {
		t.Fatalf("fixture project has %d repos, want 1", len(d.Cfg.Repos))
	}
}

func TestAttemptCapExhaustion(t *testing.T) {
	t.Setenv("JIG_HOME", t.TempDir())
	fx := fixture.Generate(t, fixture.Opts{ScenarioBranch: "cap"})
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

	bState, err := st.ReadSliceState(fx.Ticket, "b")
	if err != nil {
		t.Fatalf("ReadSliceState(b): %v", err)
	}
	if bState.State != "queued" || bState.Attempts != 0 {
		t.Fatalf("b = %+v, want untouched (blocked on a, which never went green)", bState)
	}
}

func TestStallStops(t *testing.T) {
	t.Setenv("JIG_HOME", t.TempDir())
	fx := fixture.Generate(t, fixture.Opts{ScenarioBranch: "stall"})
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

	bState, err := st.ReadSliceState(fx.Ticket, "b")
	if err != nil {
		t.Fatalf("ReadSliceState(b): %v", err)
	}
	if bState.State != "queued" || bState.Attempts != 0 {
		t.Fatalf("b = %+v, want not dispatched", bState)
	}
}

func TestRequeueFromBriefDiff(t *testing.T) {
	t.Setenv("JIG_HOME", t.TempDir())
	fx := fixture.Generate(t, fixture.Opts{ScenarioBranch: "flawed-brief"})
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

	amended, err := os.ReadFile(filepath.Join("..", "testdata", "fixture", "scenario-branches", "flawed-brief", "brief-amended.md"))
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

func TestEnvPauseDeferCI(t *testing.T) {
	t.Setenv("JIG_HOME", t.TempDir())
	fx := fixture.Generate(t, fixture.Opts{EnvFail: true})
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
	t.Setenv("JIG_HOME", t.TempDir())
	fx := fixture.Generate(t, fixture.Opts{ScenarioBranch: "oracle-wrong"})
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
