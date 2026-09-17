package verifydeliver

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/fixture"
	"github.com/develdeco/jig/gitx"
	"github.com/develdeco/jig/journal"
	"github.com/develdeco/jig/pool"
	"github.com/develdeco/jig/project"
	"github.com/develdeco/jig/staircase"
	"github.com/develdeco/jig/store"
)

// buildGitEnv pins the identity/date used for every commit these test
// helpers make directly in a pool lease, mirroring the fake session
// backend so shas stay comparable.
var buildGitEnv = []string{
	"GIT_AUTHOR_NAME=jig-fixture",
	"GIT_AUTHOR_EMAIL=fixture@example.invalid",
	"GIT_COMMITTER_NAME=jig-fixture",
	"GIT_COMMITTER_EMAIL=fixture@example.invalid",
	"GIT_AUTHOR_DATE=2026-01-01T00:00:00Z",
	"GIT_COMMITTER_DATE=2026-01-01T00:00:00Z",
}

// testRungs is a synthetic, deterministic staircase used by every test in
// this package, cheap to dear.
func testRungs() staircase.Config {
	return staircase.Config{Rungs: []string{"rung-a", "rung-b", "rung-c"}}
}

// newDeps opens fx's store and loads its project.yaml into a Deps ready
// for Gate/Publish.
func newDeps(t *testing.T, fx *fixture.Fixture) Deps {
	t.Helper()
	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	cfg, err := project.Load(filepath.Join(fx.StoreDir, "project.yaml"))
	if err != nil {
		t.Fatalf("project.Load: %v", err)
	}
	return Deps{Store: st, Cfg: cfg, Rungs: testRungs()}
}

// buildLeaseDir returns the build lease directory make would use for the
// fixture's ticket: <pool>/fixture-repo/<ticket>.
func buildLeaseDir(t *testing.T, fx *fixture.Fixture) string {
	t.Helper()
	lease, err := pool.Acquire("fixture-repo", fx.RepoRemote, "main", ticketBranch(fx.Ticket), fx.Ticket)
	if err != nil {
		t.Fatalf("pool.Acquire build lease: %v", err)
	}
	return lease.Dir
}

// scenarioResult reads and parses a scenario attempt's result.json.
func scenarioResult(t *testing.T, fx *fixture.Fixture, slice string, attempt int) map[string]any {
	t.Helper()
	path := filepath.Join(fx.ScenarioDir, "slices", slice, fmt.Sprintf("attempt-%d", attempt), "result.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return m
}

// applyScenarioPatch applies and commits a scenario attempt's patch.diff
// in dir, if one exists and is non-empty. It mirrors the fake session
// backend's commit behavior exactly (pinned identity/date).
func applyScenarioPatch(t *testing.T, fx *fixture.Fixture, dir, ticket, slice string, attempt int) {
	t.Helper()
	path := filepath.Join(fx.ScenarioDir, "slices", slice, fmt.Sprintf("attempt-%d", attempt), "patch.diff")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return
		}
		t.Fatalf("read %s: %v", path, err)
	}
	if strings.TrimSpace(string(data)) == "" {
		return
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		t.Fatalf("abs %s: %v", path, err)
	}
	if _, err := gitx.Run(dir, "apply", abs); err != nil {
		t.Fatalf("git apply %s: %v", path, err)
	}
	if _, err := gitx.Run(dir, "add", "-A"); err != nil {
		t.Fatalf("git add after %s: %v", path, err)
	}
	msg := fmt.Sprintf("%s %s: attempt %d", ticket, slice, attempt)
	if _, err := gitx.RunEnv(dir, buildGitEnv, "commit", "-m", msg); err != nil {
		t.Fatalf("git commit after %s: %v", path, err)
	}
}

// driveAttempt plays one scenario attempt for slice against the build
// lease exactly as make would: it writes the dispatch line, applies any
// patch, then routes the scenario result to a journal result line and slice
// state the way make's Run does.
func driveAttempt(t *testing.T, st *store.Store, fx *fixture.Fixture, dir, model, slice string, attempt int) {
	t.Helper()
	ticket := fx.Ticket

	if err := journal.Append(st, ticket, journal.Line{Slice: slice, Event: "dispatch", Model: model, Attempt: attempt}); err != nil {
		t.Fatalf("journal dispatch %s/%d: %v", slice, attempt, err)
	}
	applyScenarioPatch(t, fx, dir, ticket, slice, attempt)
	res := scenarioResult(t, fx, slice, attempt)
	outcome, _ := res["outcome"].(string)

	switch outcome {
	case "green":
		sha, err := gitx.RevParse(dir, "HEAD")
		if err != nil {
			t.Fatalf("resolve HEAD after %s/%d: %v", slice, attempt, err)
		}
		if err := journal.Append(st, ticket, journal.Line{Slice: slice, Event: "result", Outcome: "green", Commit: sha, Attempt: attempt}); err != nil {
			t.Fatalf("journal result %s/%d: %v", slice, attempt, err)
		}
		if err := st.WriteSliceState(ticket, slice, store.SliceState{State: "green", Attempts: attempt}); err != nil {
			t.Fatalf("write slice state %s: %v", slice, err)
		}
		if err := st.Push(fmt.Sprintf("%s: slice %s green", ticket, slice)); err != nil {
			t.Fatalf("push after %s green: %v", slice, err)
		}
	case "needs-input":
		if err := journal.Append(st, ticket, journal.Line{Slice: slice, Event: "result", Outcome: outcome, Attempt: attempt}); err != nil {
			t.Fatalf("journal result %s/%d: %v", slice, attempt, err)
		}
		question, _ := res["question"].(string)
		qid := st.NextQuestionID(ticket)
		if err := st.WriteQuestion(ticket, store.Question{ID: qid, Slice: slice, Status: "open", Body: question}); err != nil {
			t.Fatalf("write question for %s: %v", slice, err)
		}
		if err := st.WriteSliceState(ticket, slice, store.SliceState{State: "needs-input", Attempts: attempt, Question: qid}); err != nil {
			t.Fatalf("write slice state %s: %v", slice, err)
		}
		if err := journal.Append(st, ticket, journal.Line{Slice: slice, Event: "question", Attempt: attempt}); err != nil {
			t.Fatalf("journal question %s/%d: %v", slice, attempt, err)
		}
		if err := st.Push(fmt.Sprintf("%s: slice %s needs input", ticket, slice)); err != nil {
			t.Fatalf("push after %s needs-input: %v", slice, err)
		}
	default:
		if err := journal.Append(st, ticket, journal.Line{Slice: slice, Event: "result", Outcome: outcome, Attempt: attempt}); err != nil {
			t.Fatalf("journal result %s/%d: %v", slice, attempt, err)
		}
		if err := st.WriteSliceState(ticket, slice, store.SliceState{State: "queued", Attempts: attempt}); err != nil {
			t.Fatalf("write slice state %s: %v", slice, err)
		}
		if err := st.Push(fmt.Sprintf("%s: slice %s %s", ticket, slice, outcome)); err != nil {
			t.Fatalf("push after %s %s: %v", slice, outcome, err)
		}
	}
}

// answerSliceC answers slice c's open question and re-queues it, mirroring
// store.Answer + make's requeue-on-answer step.
func answerSliceC(t *testing.T, st *store.Store, ticket, text string) {
	t.Helper()
	questions, err := st.ReadQuestions(ticket)
	if err != nil {
		t.Fatalf("read questions: %v", err)
	}
	var qid string
	for _, q := range questions {
		if q.Slice == "c" && q.Status == "open" {
			qid = q.ID
		}
	}
	if qid == "" {
		t.Fatal("no open question for slice c")
	}
	slice, err := st.Answer(ticket, qid, text)
	if err != nil {
		t.Fatalf("answer %s: %v", qid, err)
	}
	if err := journal.Append(st, ticket, journal.Line{Slice: slice, Event: "answer"}); err != nil {
		t.Fatalf("journal answer: %v", err)
	}
	cur, err := st.ReadSliceState(ticket, slice)
	if err != nil {
		t.Fatalf("read slice state %s: %v", slice, err)
	}
	cur.State = "queued"
	cur.Question = ""
	if err := st.WriteSliceState(ticket, slice, cur); err != nil {
		t.Fatalf("requeue slice %s: %v", slice, err)
	}
	if err := st.Push(fmt.Sprintf("%s: answer %s", ticket, qid)); err != nil {
		t.Fatalf("push after answer %s: %v", qid, err)
	}
}

// driveBuild plays the fixture's whole a/b/c/d scenario against a fresh
// build lease, exactly as make's frontier loop would, and records the
// ticket's start sha the way make's first dispatch does. Every builder
// dispatch uses model.
func driveBuild(t *testing.T, fx *fixture.Fixture, model string) {
	t.Helper()
	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	dir := buildLeaseDir(t, fx)

	startSHA, err := gitx.RevParse(dir, "origin/main")
	if err != nil {
		t.Fatalf("resolve origin/main: %v", err)
	}
	startPath := filepath.Join(st.TicketDir(fx.Ticket), "start.fixture-repo.sha")
	if err := os.MkdirAll(filepath.Dir(startPath), 0o755); err != nil {
		t.Fatalf("create ticket dir: %v", err)
	}
	if err := os.WriteFile(startPath, []byte(startSHA), 0o644); err != nil {
		t.Fatalf("write start sha: %v", err)
	}
	if err := st.Push(fx.Ticket + ": record start sha"); err != nil {
		t.Fatalf("push start sha: %v", err)
	}

	driveAttempt(t, st, fx, dir, model, "a", 1)
	driveAttempt(t, st, fx, dir, model, "b", 1)
	driveAttempt(t, st, fx, dir, model, "b", 2)
	driveAttempt(t, st, fx, dir, model, "c", 1)
	answerSliceC(t, st, fx.Ticket, "Casual.")
	driveAttempt(t, st, fx, dir, model, "c", 2)
	driveAttempt(t, st, fx, dir, model, "d", 1)
}

// driveFix1 plays the fixture's fix-1 scenario attempt (gate round 1's fix
// slice) against the build lease, green on the first try.
func driveFix1(t *testing.T, fx *fixture.Fixture, model string) {
	t.Helper()
	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	dir := buildLeaseDir(t, fx)
	driveAttempt(t, st, fx, dir, model, "fix-1", 1)
}
