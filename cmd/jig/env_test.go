package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/mirror/github"
	"github.com/develdeco/jig/internal/project"
	"github.com/develdeco/jig/internal/spawn"
)

// testEnv is the env a test hands run: JIG_HOME is jigHome, the operator's
// own home is a directory under it that does not exist (so gate intent
// inference finds no local agent sessions to read, rather than reading the
// machine's real ones), no other variable is set, and every seam has its
// production default. It reads nothing from the process, so two tests that
// use it share no state and can both be parallel.
func testEnv(jigHome string) env {
	userHome := filepath.Join(jigHome, "operator-home")
	return envFrom(map[string]string{
		"JIG_HOME":    jigHome,
		"HOME":        userHome,
		"USERPROFILE": userHome,
	})
}

// envFrom is the production env with its variable lookup answering only
// from vars, no GitHub client, and no way to start a process: a test that
// spawns hands the env its own spawner (withSpawn).
func envFrom(vars map[string]string) env {
	e := processEnv()
	e.getenv = func(key string) string { return vars[key] }
	e.mirrorClient = noGitHub()
	e.spawn = refuseSpawn
	return e
}

// refuseSpawn is the spawner every test env starts with, so a command that
// would start a detached process fails the test that did not expect it,
// rather than leaving one running.
func refuseSpawn(spawn.Spec) (int, error) {
	return 0, errors.New("spawn not allowed in test env")
}

// noGitHub is the mirror client every test env starts with. It refuses each
// request before it leaves the process, so a test whose store declares a
// GitHub tracker and sets no client of its own fails on its sync instead of
// running the operator's `gh auth token` and calling api.github.com, which
// the production default (a nil client) would do.
func noGitHub() github.Client {
	c := github.New("https://github.invalid/graphql", "test-token")
	c.HTTPClient = &http.Client{Transport: refuseRoundTripper{}}
	c.BackoffBase = time.Millisecond
	c.MutationInterval = time.Millisecond
	return c
}

// refuseRoundTripper fails every request: noGitHub's transport.
type refuseRoundTripper struct{}

func (refuseRoundTripper) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("cmd/jig test env: no GitHub client was set for this test")
}

// inDir returns e with dir as the working directory a command resolves its
// store and repo from, in place of the process's.
func (e env) inDir(dir string) env {
	e.getwd = func() (string, error) { return dir, nil }
	return e
}

// atTerminal returns e with every stdin reported as an interactive terminal,
// so a scripted stdin answers the triage prompts as a person would.
func (e env) atTerminal() env {
	e.stdinIsTerminal = func(io.Reader) bool { return true }
	return e
}

// withSpawn returns e with fn as the spawner a command starts detached
// processes through.
func (e env) withSpawn(fn func(spawn.Spec) (int, error)) env {
	e.spawn = fn
	return e
}

// withClock returns e with now as its clock.
func (e env) withClock(now func() time.Time) env {
	e.now = now
	return e
}

// withLookPath returns e with fn as its PATH lookup.
func (e env) withLookPath(fn func(file string) (string, error)) env {
	e.lookPath = fn
	return e
}

// withContext returns e with ctx as its context, which a test cancels to stop
// a command that waits or streams.
func (e env) withContext(ctx context.Context) env {
	e.ctx = ctx
	return e
}

// withExecutable returns e naming path as the running jig binary.
func (e env) withExecutable(path string) env {
	e.executable = func() (string, error) { return path, nil }
	return e
}

// withStdoutTerminal returns e with every stdout reported as an interactive
// terminal, or not.
func (e env) withStdoutTerminal(yes bool) env {
	e.stdoutIsTerminal = func(io.Writer) bool { return yes }
	return e
}

// fixedClock is a clock that always reads t.
func fixedClock(t time.Time) func() time.Time { return func() time.Time { return t } }

// spawnRecorder is a spawner that records the Specs it was asked to start and
// answers with a pid (or an error) of the test's choosing, without starting
// anything.
type spawnRecorder struct {
	// PID is the pid every spawn reports; Err, if set, is what it fails with.
	PID int
	Err error

	mu    sync.Mutex
	specs []spawn.Spec
}

// Spawn is the func to hand withSpawn.
func (r *spawnRecorder) Spawn(s spawn.Spec) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.specs = append(r.specs, s)
	return r.PID, r.Err
}

// Specs returns the Specs spawned so far, oldest first.
func (r *spawnRecorder) Specs() []spawn.Spec {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]spawn.Spec(nil), r.specs...)
}

// newFixture is fixture.Generate with the fixture's machine mapping under a
// jig home of its own, never the one the process environment names. The
// home is fx.Home: pass testEnv(fx.Home) to run.
func newFixture(t *testing.T, opts fixture.Opts) *fixture.Fixture {
	t.Helper()
	if opts.Home == "" {
		opts.Home = t.TempDir()
	}
	return fixture.Generate(t, opts)
}

// runMain runs the CLI in process with env e and a scripted stdin and
// returns its stdout and exit code. It never fails the test on a non-zero
// code: several steps below (the deliberately broken round 1 attempt)
// expect one.
func runMain(t *testing.T, e env, stdin string, args ...string) (string, int) {
	t.Helper()
	var buf bytes.Buffer
	code := run(e, args, &buf, strings.NewReader(stdin))
	return buf.String(), code
}

// TestEnvFromAnswersOnlyFromItsVariables pins that a test env reads nothing
// from the process: a variable it was not given is empty, however the
// process sets it.
func TestEnvFromAnswersOnlyFromItsVariables(t *testing.T) {
	t.Parallel()

	e := envFrom(map[string]string{"JIG_HOME": "/some/home"})
	if got := e.getenv("JIG_HOME"); got != "/some/home" {
		t.Errorf("getenv(JIG_HOME) = %q, want the given value", got)
	}
	if got := e.getenv("PATH"); got != "" {
		t.Errorf("getenv(PATH) = %q, want empty: a test env never reads the process's", got)
	}
	root, err := e.jigHome()
	if err != nil || root != "/some/home" {
		t.Errorf("jigHome() = %q, %v, want /some/home", root, err)
	}
}

// TestTestEnvHasItsOwnOperatorHome pins that an env built for a test names an
// operator home of its own, under the jig home it was given.
func TestTestEnvHasItsOwnOperatorHome(t *testing.T) {
	t.Parallel()

	jigHome := t.TempDir()
	userHome, err := testEnv(jigHome).userHomeDir()
	if err != nil {
		t.Fatalf("userHomeDir: %v", err)
	}
	if !strings.HasPrefix(userHome, jigHome) {
		t.Errorf("operator home = %q, want a directory under the jig home %q", userHome, jigHome)
	}
}

// TestMainReadsTheProcessEnvironment is the one test of the binary's own
// wiring: Main resolves the jig home from the process's environment, here
// to find a project by name in the machine mapping JIG_HOME holds. It sets
// JIG_HOME for the whole process, so it is not parallel.
func TestMainReadsTheProcessEnvironment(t *testing.T) {
	fx := newFixture(t, fixture.Opts{})
	cfg, err := project.Load(filepath.Join(fx.StoreDir, "project.yaml"))
	if err != nil {
		t.Fatalf("project.Load: %v", err)
	}
	t.Setenv("JIG_HOME", fx.Home)

	var buf bytes.Buffer
	code := Main([]string{"status", fx.Ticket, "--project", cfg.Name}, &buf, strings.NewReader(""))
	if code != 0 || !strings.HasPrefix(buf.String(), "ticket: "+fx.Ticket+"\n") {
		t.Fatalf("jig status --project %s: exit %d, want the ticket's status from the store JIG_HOME's mapping names:\n%s", cfg.Name, code, buf.String())
	}
}

// TestEnvAbsResolvesAgainstTheEnvsWorkingDirectory pins that a relative path
// is placed under the env's working directory, never the process's, and that
// an absolute or empty path is left as it is.
func TestEnvAbsResolvesAgainstTheEnvsWorkingDirectory(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	e := testEnv(t.TempDir()).inDir(dir)
	for _, c := range []struct{ in, want string }{
		{"", ""},
		{"rel", filepath.Join(dir, "rel")},
		{filepath.Join(".", "a", "..", "b"), filepath.Join(dir, "b")},
		{filepath.Join(dir, "abs"), filepath.Join(dir, "abs")},
	} {
		got, err := e.abs(c.in)
		if err != nil || got != c.want {
			t.Errorf("abs(%q) = %q, %v, want %q", c.in, got, err, c.want)
		}
	}

	// The working directory is asked for only when a path needs it.
	broken := e
	broken.getwd = func() (string, error) { return "", errors.New("no working directory") }
	if got, err := broken.abs(filepath.Join(dir, "abs")); err != nil || got != filepath.Join(dir, "abs") {
		t.Errorf("abs of an absolute path with no working directory = %q, %v, want it unchanged", got, err)
	}
	if got, err := broken.abs("rel"); err == nil {
		t.Errorf("abs(rel) with no working directory = %q, want the error", got)
	}
}

// TestRelativeStoreFlagResolvesAgainstTheEnvsWorkingDirectory: --store given
// as a relative path finds the store under the env's directory, so a test's
// working directory is per call even though the process's is some other.
func TestRelativeStoreFlagResolvesAgainstTheEnvsWorkingDirectory(t *testing.T) {
	t.Parallel()

	fx := newFixture(t, fixture.Opts{})
	rel, err := filepath.Rel(fx.Dir, fx.StoreDir)
	if err != nil {
		t.Fatal(err)
	}
	out, code := runMain(t, testEnv(fx.Home).inDir(fx.Dir), "", "status", fx.Ticket, "--store", rel)
	if code != 0 || !strings.HasPrefix(out, "ticket: "+fx.Ticket+"\n") {
		t.Fatalf("jig status --store %s from %s: exit %d, want the ticket's status:\n%s", rel, fx.Dir, code, out)
	}
}

// TestSkillsInstallPlacesARelativeDestUnderTheEnvsWorkingDirectory: --dest as a
// relative path is written under the env's directory and shown as given.
func TestSkillsInstallPlacesARelativeDestUnderTheEnvsWorkingDirectory(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	e := testEnv(t.TempDir()).inDir(dir)
	var buf bytes.Buffer
	if code := cmdSkills(e, []string{"install", "--dest", "out"}, &buf); code != 0 {
		t.Fatalf("cmdSkills install --dest out: exit %d\n%s", code, buf.String())
	}
	for _, name := range wantSkillNames(t) {
		if _, err := os.Stat(filepath.Join(dir, "out", name, "SKILL.md")); err != nil {
			t.Errorf("%s: not installed under the env's directory: %v", name, err)
		}
		if want := filepath.Join("out", name, "SKILL.md"); !strings.Contains(buf.String(), want) {
			t.Errorf("table does not show %s as given:\n%s", want, buf.String())
		}
	}
}

// TestProcessEnvOperatorHomeAgreesWithTheStdlib pins that the binary's
// operator-home lookup, os.UserHomeDir's rule over a getenv, gives on this
// machine exactly what os.UserHomeDir gives: the same directory, or the same
// error. It only reads the process, so it is parallel.
func TestProcessEnvOperatorHomeAgreesWithTheStdlib(t *testing.T) {
	t.Parallel()

	got, gotErr := processEnv().userHomeDir()
	want, wantErr := os.UserHomeDir()
	if got != want || fmt.Sprint(gotErr) != fmt.Sprint(wantErr) {
		t.Errorf("processEnv().userHomeDir() = %q, %v, want os.UserHomeDir's %q, %v", got, gotErr, want, wantErr)
	}
}

// TestTestEnvSeamsAndTheirHelpers pins the seams a test env starts with: it
// cannot start a process, and the helpers replace the context, clock, PATH
// lookup, executable and terminal checks without changing the env they were
// called on.
func TestTestEnvSeamsAndTheirHelpers(t *testing.T) {
	t.Parallel()

	e := testEnv(t.TempDir())
	if _, err := e.spawn(spawn.Spec{Path: "x"}); err == nil || !strings.Contains(err.Error(), "spawn not allowed in test env") {
		t.Errorf("testEnv spawn error = %v, want \"spawn not allowed in test env\"", err)
	}

	rec := &spawnRecorder{PID: 4242}
	withSpawn := e.withSpawn(rec.Spawn)
	pid, err := withSpawn.spawn(spawn.Spec{Path: "jig", Args: []string{"verify"}})
	if err != nil || pid != 4242 {
		t.Errorf("recorded spawn = %d, %v, want 4242, nil", pid, err)
	}
	if specs := rec.Specs(); len(specs) != 1 || specs[0].Path != "jig" || len(specs[0].Args) != 1 {
		t.Errorf("recorded specs = %+v, want the one spawned", specs)
	}
	if _, err := e.spawn(spawn.Spec{}); err == nil {
		t.Errorf("withSpawn changed the env it was called on")
	}

	at := time.Date(2026, 1, 2, 3, 4, 5, 6, time.UTC)
	if got := e.withClock(fixedClock(at)).now(); !got.Equal(at) {
		t.Errorf("withClock now() = %v, want %v", got, at)
	}

	if _, err := e.withLookPath(func(string) (string, error) { return "/bin/tool", nil }).lookPath("tool"); err != nil {
		t.Errorf("withLookPath lookPath = %v, want the stub's answer", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	withCtx := e.withContext(ctx)
	cancel()
	if withCtx.ctx.Err() == nil || e.ctx.Err() != nil {
		t.Errorf("withContext: env ctx err = %v, original ctx err = %v, want the first cancelled and the second not", withCtx.ctx.Err(), e.ctx.Err())
	}

	if got, err := e.withExecutable("/opt/jig").executable(); err != nil || got != "/opt/jig" {
		t.Errorf("withExecutable executable() = %q, %v, want /opt/jig", got, err)
	}
	if !e.withStdoutTerminal(true).stdoutIsTerminal(io.Discard) || e.withStdoutTerminal(false).stdoutIsTerminal(io.Discard) {
		t.Errorf("withStdoutTerminal did not set the stdout terminal answer")
	}
}

// TestProcessEnvSeamsHaveProductionDefaults pins that processEnv fills every
// seam: nothing is left nil for a command to trip over, the context is live,
// the clock is the real one, the executable is this binary, and a writer that
// is not a console is not a terminal.
func TestProcessEnvSeamsHaveProductionDefaults(t *testing.T) {
	t.Parallel()

	e := processEnv()
	if e.ctx == nil || e.ctx.Err() != nil {
		t.Errorf("processEnv ctx = %v, want a live context", e.ctx)
	}
	if e.spawn == nil || e.lookPath == nil || e.stdoutIsTerminal == nil || e.executable == nil || e.now == nil {
		t.Fatalf("processEnv left a seam nil: %+v", e)
	}
	if before, got, after := time.Now(), e.now(), time.Now(); got.Before(before) || got.After(after) {
		t.Errorf("processEnv now() = %v, want a time between %v and %v", got, before, after)
	}
	want, wantErr := os.Executable()
	if got, err := e.executable(); got != want || fmt.Sprint(err) != fmt.Sprint(wantErr) {
		t.Errorf("processEnv executable() = %q, %v, want os.Executable's %q, %v", got, err, want, wantErr)
	}
	if _, err := e.lookPath("jig-no-such-program-xyz"); err == nil {
		t.Errorf("processEnv lookPath found a program that does not exist")
	}
	if e.stdoutIsTerminal(&bytes.Buffer{}) {
		t.Errorf("processEnv stdoutIsTerminal(bytes.Buffer) = true, want false")
	}
	f, err := os.CreateTemp(t.TempDir(), "out")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if e.stdoutIsTerminal(f) {
		t.Errorf("processEnv stdoutIsTerminal(a regular file) = true, want false")
	}
}

// TestEndAfterInterrupt pins the interrupt watcher: it does nothing until the
// context is cancelled, then gives the signal back its default action and
// ends the process with the interrupted status once the grace is out. The
// exit is a stub, so nothing ends.
func TestEndAfterInterrupt(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	stopped := make(chan struct{})
	exited := make(chan int, 1)
	go endAfterInterrupt(ctx, func() { close(stopped) }, 10*time.Millisecond, func(code int) { exited <- code })

	select {
	case code := <-exited:
		t.Fatalf("exited with %d before any interrupt", code)
	case <-time.After(50 * time.Millisecond):
	}
	cancel()
	select {
	case code := <-exited:
		if code != 130 {
			t.Errorf("exit code = %d, want 130", code)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("did not exit after the context was cancelled")
	}
	select {
	case <-stopped:
	default:
		t.Errorf("stop was not called before exiting")
	}
}
