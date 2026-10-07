package main

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
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

// TestInitResolvesRelativeStoreAndCloneAgainstTheEnvsWorkingDirectory: the
// machine mapping `jig init --store --clone` writes names the paths as placed
// under the env's directory, and the command prints the store as it was given.
func TestInitResolvesRelativeStoreAndCloneAgainstTheEnvsWorkingDirectory(t *testing.T) {
	t.Parallel()

	fx := newFixture(t, fixture.Opts{})
	relStore, err := filepath.Rel(fx.Dir, fx.StoreDir)
	if err != nil {
		t.Fatal(err)
	}
	relRepo, err := filepath.Rel(fx.Dir, fx.RepoDir)
	if err != nil {
		t.Fatal(err)
	}
	newHome := t.TempDir()
	out, code := runMain(t, testEnv(newHome).inDir(fx.Dir), "", "init", "--store", relStore, "--clone", "fixture-repo="+relRepo)
	if code != 0 {
		t.Fatalf("jig init --store %s --clone fixture-repo=%s: exit %d\n%s", relStore, relRepo, code, out)
	}
	if !strings.Contains(out, "store: "+relStore+"\n") {
		t.Errorf("init output does not print the store as given (%s):\n%s", relStore, out)
	}
	cfg, err := project.Load(filepath.Join(fx.StoreDir, "project.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	machine, err := project.LoadMachine(newHome)
	if err != nil {
		t.Fatal(err)
	}
	mp, ok := machine[cfg.Name]
	if !ok || mp.Store != fx.StoreDir || mp.Clones["fixture-repo"] != fx.RepoDir {
		t.Fatalf("machine mapping for %s = %+v (found %v), want store %s and clone fixture-repo=%s", cfg.Name, mp, ok, fx.StoreDir, fx.RepoDir)
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
