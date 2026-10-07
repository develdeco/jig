package main

import (
	"bytes"
	"io"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/project"
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
// from vars.
func envFrom(vars map[string]string) env {
	e := processEnv()
	e.getenv = func(key string) string { return vars[key] }
	return e
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
