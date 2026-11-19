package gittest_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/gittest"
	"github.com/develdeco/jig/internal/gitx"
)

// pinChildEnvVar makes a re-run of this test binary pin the fixture identity
// before Run, as the packages that call PinIdentity do.
const pinChildEnvVar = "GITTEST_CHILD_PIN"

func TestMain(m *testing.M) {
	if os.Getenv(pinChildEnvVar) != "" {
		gittest.PinIdentity()
	}
	os.Exit(gittest.Run(m))
}

// TestRunSetsHermeticConfig proves Run's own effect: a repo created inside
// this test binary sees maintenance.auto, receive.autogc and gc.autoDetach
// as false, with no other config source (like a host's system config)
// overriding them.
func TestRunSetsHermeticConfig(t *testing.T) {
	dir := t.TempDir()
	if _, err := gitx.Run(dir, "init", "-b", "main"); err != nil {
		t.Fatalf("git init: %v", err)
	}

	for _, key := range []string{"maintenance.auto", "receive.autogc", "gc.autoDetach"} {
		got, err := gitx.Run(dir, "config", "--get", key)
		if err != nil {
			t.Fatalf("git config --get %s: %v", key, err)
		}
		if got != "false" {
			t.Errorf("%s = %q, want %q", key, got, "false")
		}
	}

	// The ignore and attributes files are gittest's own, beside its config,
	// so the user's $HOME/.config/git/ignore and attributes never apply.
	own := filepath.Dir(os.Getenv("GIT_CONFIG_GLOBAL"))
	for key, name := range map[string]string{"core.excludesFile": "ignore", "core.attributesFile": "attributes"} {
		got, err := gitx.Run(dir, "config", "--get", key)
		if err != nil {
			t.Fatalf("git config --get %s: %v", key, err)
		}
		if want := filepath.ToSlash(filepath.Join(own, name)); got != want {
			t.Errorf("%s = %q, want %q", key, got, want)
		}
	}
}

// TestRunConfigRefusesWrites proves no test can change the config every test
// of the binary shares: git config --global fails, the file keeps its bytes,
// and reading it still works.
func TestRunConfigRefusesWrites(t *testing.T) {
	cfg := os.Getenv("GIT_CONFIG_GLOBAL")
	before, err := os.ReadFile(cfg)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	for _, args := range [][]string{
		{"config", "--global", "core.hooksPath", "/some/hooks"},
		{"config", "--global", "--unset", "maintenance.auto"},
	} {
		if out, err := gitx.Run(dir, args...); err == nil || !strings.Contains(err.Error(), "could not lock config file") {
			t.Errorf("git %v = %q, %v, want the refusal to lock the shared config", args, out, err)
		}
	}
	if after, err := os.ReadFile(cfg); err != nil || string(after) != string(before) {
		t.Errorf("the shared config changed:\n%s\nwant:\n%s", after, before)
	}
	if got, err := gitx.Run(dir, "config", "--global", "--get", "maintenance.auto"); err != nil || got != "false" {
		t.Errorf("maintenance.auto = %q, %v, want false read from the shared config", got, err)
	}
}

// TestRunConfigRecordsCallerVariables proves the hermetic config lists the
// variables gitx sets when git traces, so every git process of a test binary
// can record who started it.
func TestRunConfigRecordsCallerVariables(t *testing.T) {
	got, err := gitx.Run(t.TempDir(), "config", "--get", "trace2.envVars")
	if err != nil {
		t.Fatalf("git config --get trace2.envVars: %v", err)
	}
	if want := "JIG_GIT_CALLER,JIG_GIT_DIR"; got != want {
		t.Errorf("trace2.envVars = %q, want %q", got, want)
	}
}
