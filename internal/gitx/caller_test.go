package gitx_test

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/gitx"
)

// tracedGit runs ask in a fresh directory with a config file that lists
// trace2.envVars and a trace file for git, both handed to ask as the env
// argument gitx takes, and returns the def_param events of the trace by
// variable name (plus "work": the directory).
func tracedGit(t *testing.T, ask func(dir string, env []string)) map[string]string {
	t.Helper()
	root := t.TempDir()
	cfg := filepath.Join(root, "config")
	if err := os.WriteFile(cfg, []byte("[trace2]\n\tenvVars = JIG_GIT_CALLER,JIG_GIT_DIR\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	trace := filepath.Join(root, "trace")
	work := filepath.Join(root, "work")
	if err := os.Mkdir(work, 0o755); err != nil {
		t.Fatal(err)
	}
	ask(work, []string{"GIT_CONFIG_GLOBAL=" + cfg, "GIT_TRACE2_EVENT=" + trace})

	f, err := os.Open(trace)
	if err != nil {
		t.Fatalf("git left no trace: %v", err)
	}
	defer f.Close()
	got := map[string]string{"work": work}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		var ev struct{ Event, Param, Value string }
		if json.Unmarshal(sc.Bytes(), &ev) == nil && ev.Event == "def_param" {
			got[ev.Param] = ev.Value
		}
	}
	return got
}

// askGit is the function a trace must name as the caller.
func askGit(dir string, env []string) {
	_, _ = gitx.RunEnv(dir, env, "version")
}

type asker struct{}

// askGit reaches gitx from a closure; the method is the caller.
func (asker) askGit(dir string, env []string) {
	func() { _, _ = gitx.RunEnv(dir, env, "version") }()
}

func TestCallerIsNamedWhenGitTraces(t *testing.T) {
	const pkg = "github.com/develdeco/jig/internal/gitx_test."
	cases := []struct {
		name string
		ask  func(dir string, env []string)
		want string
	}{
		{"function", askGit, pkg + "askGit"},
		{"method and closure", asker{}.askGit, pkg + "asker.askGit"},
		{"replaced environment", func(dir string, env []string) {
			_, _ = gitx.RunReplaceEnv(dir, append(os.Environ(), env...), "version")
		}, pkg + "TestCallerIsNamedWhenGitTraces"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := tracedGit(t, c.ask)
			if got["JIG_GIT_CALLER"] != c.want {
				t.Errorf("JIG_GIT_CALLER = %q, want %q", got["JIG_GIT_CALLER"], c.want)
			}
			abs, _ := filepath.Abs(got["work"])
			if !strings.EqualFold(filepath.Clean(got["JIG_GIT_DIR"]), abs) {
				t.Errorf("JIG_GIT_DIR = %q, want %q", got["JIG_GIT_DIR"], abs)
			}
		})
	}
}

// With no trace target a git child sees neither variable, on the appended
// path and on the replaced one.
func TestNoCallerReachesGitWithoutATrace(t *testing.T) {
	alias := []string{"-c", `alias.who=!printf '%s|%s' "$JIG_GIT_CALLER" "$JIG_GIT_DIR"`, "who"}
	dir := t.TempDir()
	off := []string{"GIT_TRACE2_EVENT="}
	if got, err := gitx.RunEnv(dir, off, alias...); err != nil || got != "|" {
		t.Errorf("RunEnv: %q, %v; want no caller", got, err)
	}
	if got, err := gitx.RunReplaceEnv(dir, append(os.Environ(), off...), alias...); err != nil || got != "|" {
		t.Errorf("RunReplaceEnv: %q, %v; want no caller", got, err)
	}
}
