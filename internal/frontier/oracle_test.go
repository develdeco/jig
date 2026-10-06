package frontier

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/outcome"
	"github.com/develdeco/jig/internal/session"
	"github.com/develdeco/jig/internal/store"
)

// redOracleBackend runs every dispatch on the fake backend it wraps, then,
// for slice a's first attempt, commits a test that fails, so jig's own
// oracle run at the claimed green comes back red. As a session.Resumer it
// reports a session id and, on Resume, either removes that test (fixes) or
// leaves it (never fixes). With resumable false it hides Resume and
// RunResumable, as a backend that cannot continue a session does.
type redOracleBackend struct {
	session.Backend
	t        *testing.T
	fixes    bool
	mu       sync.Mutex
	prompts  []string
	sessions []string
}

const redTestFile = "alpha/zz_red_test.go"

func (b *redOracleBackend) RunResumable(d session.Dispatch) (string, error) {
	if err := b.Backend.Run(d); err != nil {
		return "", err
	}
	if d.Slice == "a" && d.Attempt == 1 {
		body := "package alpha\n\nimport \"testing\"\n\nfunc TestRed(t *testing.T) { t.Fatal(\"red on purpose\") }\n"
		b.commitGreen(d, func() error {
			return os.WriteFile(filepath.Join(d.Worktree, filepath.FromSlash(redTestFile)), []byte(body), 0o644)
		})
	}
	return "sess-1", nil
}

func (b *redOracleBackend) Resume(d session.Dispatch, sessionID string) error {
	b.mu.Lock()
	b.prompts = append(b.prompts, d.Prompt)
	b.sessions = append(b.sessions, sessionID)
	b.mu.Unlock()
	if !b.fixes {
		head, err := gitx.RevParse(d.Worktree, "HEAD")
		if err != nil {
			return err
		}
		return writeGreen(d.ResultJSON, head)
	}
	b.commitGreen(d, func() error {
		return os.Remove(filepath.Join(d.Worktree, filepath.FromSlash(redTestFile)))
	})
	return nil
}

// commitGreen applies change in d's worktree, commits it, and writes a green
// result.json naming the new commit.
func (b *redOracleBackend) commitGreen(d session.Dispatch, change func() error) {
	b.t.Helper()
	if err := change(); err != nil {
		b.t.Fatalf("change the lease: %v", err)
	}
	runGitT(b.t, d.Worktree, "add", "-A")
	runGitT(b.t, d.Worktree, "-c", "user.name=jig-test", "-c", "user.email=test@example.invalid", "commit", "-q", "-m", "red oracle step")
	head, err := gitx.RevParse(d.Worktree, "HEAD")
	if err != nil {
		b.t.Fatal(err)
	}
	if err := writeGreen(d.ResultJSON, head); err != nil {
		b.t.Fatal(err)
	}
}

func writeGreen(path, commit string) error {
	data, err := json.Marshal(outcome.Result{Outcome: outcome.Green, Summary: "done", Commit: commit})
	if err != nil {
		return err
	}
	return os.WriteFile(path, data, 0o644)
}

// plainBackend hides a redOracleBackend's Resumer methods: a backend that
// cannot continue a session.
type plainBackend struct{ b *redOracleBackend }

func (p plainBackend) Run(d session.Dispatch) error {
	_, err := p.b.RunResumable(d)
	return err
}

// oracleLines returns slice's journaled oracle runs for attempt, in order.
func oracleLines(t *testing.T, st *store.Store, ticket, slice string, attempt int) []string {
	t.Helper()
	lines, err := journal.Read(st, ticket)
	if err != nil {
		t.Fatal(err)
	}
	var out []string
	for _, l := range lines {
		if l.Slice == slice && l.Event == "oracle" && l.Attempt == attempt {
			out = append(out, l.Outcome)
		}
	}
	return out
}

// TestRunHandsARedOracleBackToTheSameSession covers ADR 0020 at the
// frontier's dispatch: jig runs the slice's oracle (the fixture's real
// `go test ./alpha/...`) when a builder reports green, a red run goes back to
// that builder's own session with the oracle's output, and only after
// maxOracleFixes red turns, or with a backend that cannot resume, does the
// attempt fail.
func TestRunHandsARedOracleBackToTheSameSession(t *testing.T) {
	t.Run("the session fixes it", func(t *testing.T) {
		fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
		d, st := newDeps(t, fx)
		b := &redOracleBackend{Backend: d.Backend, t: t, fixes: true}
		d.Backend = b
		if _, err := Run(d, RunOpts{Ticket: fx.Ticket}); err != nil {
			t.Fatalf("Run: %v", err)
		}

		if got := oracleLines(t, st, fx.Ticket, "a", 1); strings.Join(got, ",") != "fail,pass" {
			t.Fatalf("slice a attempt 1 oracle runs = %v, want [fail pass]", got)
		}
		if len(b.prompts) != 1 || b.sessions[0] != "sess-1" {
			t.Fatalf("resumed %d time(s) with sessions %v, want once with sess-1", len(b.prompts), b.sessions)
		}
		if !strings.Contains(b.prompts[0], "red on purpose") || !strings.Contains(b.prompts[0], "./alpha/...") {
			t.Errorf("the fix turn's prompt does not carry the oracle and its output:\n%s", b.prompts[0])
		}
		aState, err := st.ReadSliceState(fx.Ticket, "a")
		if err != nil {
			t.Fatal(err)
		}
		if aState.State != "green" || aState.Attempts != 1 {
			t.Errorf("slice a = %+v, want green on its first attempt", aState)
		}
	})

	t.Run("the fix budget runs out", func(t *testing.T) {
		fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
		d, st := newDeps(t, fx)
		b := &redOracleBackend{Backend: d.Backend, t: t, fixes: false}
		d.Backend = b
		if _, err := Run(d, RunOpts{Ticket: fx.Ticket, MaxAttempts: 1}); err != nil {
			t.Fatalf("Run: %v", err)
		}

		if got := oracleLines(t, st, fx.Ticket, "a", 1); strings.Join(got, ",") != "fail,fail,fail" {
			t.Fatalf("slice a attempt 1 oracle runs = %v, want three fails (the run and %d fix turns)", got, maxOracleFixes)
		}
		if len(b.prompts) != maxOracleFixes {
			t.Fatalf("resumed %d time(s), want %d", len(b.prompts), maxOracleFixes)
		}
		data, err := os.ReadFile(resultJSONPath(st, fx.Ticket, "a", 1))
		if err != nil {
			t.Fatal(err)
		}
		res := outcome.ParseJSON("slice", data)
		if res.Outcome != outcome.CodeBug || !strings.Contains(res.Summary, "red on purpose") {
			t.Errorf("attempt 1 result = %+v, want code-bug carrying the oracle's output", res)
		}
	})

	t.Run("a backend that cannot resume", func(t *testing.T) {
		fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
		d, st := newDeps(t, fx)
		b := &redOracleBackend{Backend: d.Backend, t: t, fixes: true}
		d.Backend = plainBackend{b: b}
		if _, err := Run(d, RunOpts{Ticket: fx.Ticket, MaxAttempts: 1}); err != nil {
			t.Fatalf("Run: %v", err)
		}

		if got := oracleLines(t, st, fx.Ticket, "a", 1); strings.Join(got, ",") != "fail" {
			t.Fatalf("slice a attempt 1 oracle runs = %v, want one fail and no fix turn", got)
		}
		if len(b.prompts) != 0 {
			t.Errorf("resumed %d time(s), want none", len(b.prompts))
		}
	})
}
