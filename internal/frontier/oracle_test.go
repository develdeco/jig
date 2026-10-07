package frontier

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

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
	dirty    bool // leave the failing test as an uncommitted tracked edit, not a commit
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
		path := filepath.Join(d.Worktree, filepath.FromSlash(redTestFile))
		if b.dirty {
			// Commit the file empty of tests, then leave the failing test as
			// an edit the builder never committed.
			b.commitGreen(d, func() error { return os.WriteFile(path, []byte("package alpha\n"), 0o644) })
			if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
				b.t.Fatal(err)
			}
			return "sess-1", nil
		}
		b.commitGreen(d, func() error { return os.WriteFile(path, []byte(body), 0o644) })
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
	path := filepath.Join(d.Worktree, filepath.FromSlash(redTestFile))
	if b.dirty {
		// Revert the uncommitted edit, back to the committed, test-free file,
		// and report the same commit green again.
		if err := os.WriteFile(path, []byte("package alpha\n"), 0o644); err != nil {
			return err
		}
		head, err := gitx.RevParse(d.Worktree, "HEAD")
		if err != nil {
			return err
		}
		return writeGreen(d.ResultJSON, head)
	}
	b.commitGreen(d, func() error { return os.Remove(path) })
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
	t.Parallel()
	t.Run("the session fixes it", func(t *testing.T) {
		fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
		d, st := newDeps(t, fx)
		d.Oracle = nil // the real oracle: the fixture's go test
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
		d.Oracle = nil // the real oracle: the fixture's go test
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

	t.Run("uncommitted changes go back to the session", func(t *testing.T) {
		fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
		d, st := newDeps(t, fx)
		d.Oracle = nil // the real oracle: the fixture's go test
		b := &redOracleBackend{Backend: d.Backend, t: t, fixes: true, dirty: true}
		d.Backend = b
		if _, err := Run(d, RunOpts{Ticket: fx.Ticket}); err != nil {
			t.Fatalf("Run: %v", err)
		}

		if len(b.prompts) != 1 || !strings.Contains(b.prompts[0], "uncommitted changes") || !strings.Contains(b.prompts[0], redTestFile) {
			t.Fatalf("fix turns = %q, want one naming the uncommitted change to %s", b.prompts, redTestFile)
		}
		if got := oracleLines(t, st, fx.Ticket, "a", 1); strings.Join(got, ",") != "pass" {
			t.Errorf("slice a attempt 1 oracle runs = %v, want one pass, run only once the tree was the commit", got)
		}
	})

	t.Run("a backend that cannot resume", func(t *testing.T) {
		fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
		d, st := newDeps(t, fx)
		d.Oracle = nil // the real oracle: the fixture's go test
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

// TestRunTellsTheNextBuilderTheOraclesLastRunTime: jig times each oracle run
// at green and journals it, and the next builder of the same oracle command
// on the ticket reads that time in slice.json's oracle_seconds; the first
// builder of a command reads 0 (ADR 0024). The fixture's slices a and b share
// workspace alpha's oracle, and b waits for a.
func TestRunTellsTheNextBuilderTheOraclesLastRunTime(t *testing.T) {
	t.Parallel()
	fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
	d, st := newDeps(t, fx)
	d.Oracle = func(string, string) (string, error) {
		time.Sleep(1100 * time.Millisecond)
		return "", nil
	}
	if _, err := Run(d, RunOpts{Ticket: fx.Ticket}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	lines, err := journal.Read(st, fx.Ticket)
	if err != nil {
		t.Fatalf("journal.Read: %v", err)
	}
	ran := false
	for _, l := range lines {
		if l.Event == "oracle" && l.Slice == "a" {
			ran = true
			if l.Seconds < 1 {
				t.Errorf("slice a's oracle line records %d seconds, want at least 1", l.Seconds)
			}
		}
	}
	if !ran {
		t.Fatalf("no oracle line for slice a; journal: %+v", lines)
	}

	read := func(slice string) sliceJSONBody {
		t.Helper()
		data, err := os.ReadFile(sliceJSONPath(st, fx.Ticket, slice, 1))
		if err != nil {
			t.Fatalf("read %s's slice.json: %v", slice, err)
		}
		var body sliceJSONBody
		if err := json.Unmarshal(data, &body); err != nil {
			t.Fatalf("parse %s's slice.json: %v", slice, err)
		}
		return body
	}
	if got := read("a").OracleSeconds; got != 0 {
		t.Errorf("slice a's oracle_seconds = %d, want 0: nothing ran its oracle before it", got)
	}
	if got := read("b").OracleSeconds; got < 1 {
		t.Errorf("slice b's oracle_seconds = %d, want slice a's run time, at least 1", got)
	}
}

// TestRunHandsALaterSliceWhatEarlierSlicesBuilt: a slice dispatched after
// another slice of the ticket verified green reads, in slice.json's
// earlier_slices, that slice's summary and the files its verified attempt
// changed, and never itself (ADR 0025). The fixture's slice b waits for a.
func TestRunHandsALaterSliceWhatEarlierSlicesBuilt(t *testing.T) {
	t.Parallel()
	fx := fixture.Generate(t, fixture.Opts{Home: t.TempDir()})
	d, st := newDeps(t, fx)
	if _, err := Run(d, RunOpts{Ticket: fx.Ticket}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	data, err := os.ReadFile(sliceJSONPath(st, fx.Ticket, "b", 1))
	if err != nil {
		t.Fatalf("read b's slice.json: %v", err)
	}
	var body sliceJSONBody
	if err := json.Unmarshal(data, &body); err != nil {
		t.Fatalf("parse b's slice.json: %v", err)
	}
	var a *earlierSlice
	for i, e := range body.EarlierSlices {
		if e.ID == "b" {
			t.Errorf("b's earlier_slices lists b itself: %+v", body.EarlierSlices)
		}
		if e.ID == "a" {
			a = &body.EarlierSlices[i]
		}
	}
	if a == nil {
		t.Fatalf("b's earlier_slices = %+v, want slice a, which verified before b", body.EarlierSlices)
	}
	if a.Summary == "" {
		t.Errorf("slice a's entry has no summary: %+v", *a)
	}
	alpha := false
	for _, f := range a.Files {
		alpha = alpha || strings.HasPrefix(f, "alpha/")
	}
	if !alpha {
		t.Errorf("slice a's files = %v, want the alpha/ files its attempt changed", a.Files)
	}
	// A beta slice's range never reaches back over slice a's commits.
	for _, e := range body.EarlierSlices {
		if e.ID != "c" && e.ID != "d" {
			continue
		}
		for _, f := range e.Files {
			if strings.HasPrefix(f, "alpha/") {
				t.Errorf("beta slice %s lists alpha file %s: its range reaches over another slice's commits", e.ID, f)
			}
		}
	}
}
