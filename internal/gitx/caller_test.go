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

// tracedGit runs `git version` through gitx under a config that lists
// trace2.envVars and a trace file, via run, and returns the def_param events
// of the trace by variable name.
func tracedGit(t *testing.T, run func(dir string)) map[string]string {
	t.Helper()
	root := t.TempDir()
	cfg := filepath.Join(root, "config")
	if err := os.WriteFile(cfg, []byte("[trace2]\n\tenvVars = JIG_GIT_CALLER,JIG_GIT_DIR\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	trace := filepath.Join(root, "trace")
	t.Setenv("GIT_CONFIG_GLOBAL", cfg)
	t.Setenv("GIT_TRACE2_EVENT", trace)
	work := filepath.Join(root, "work")
	if err := os.Mkdir(work, 0o755); err != nil {
		t.Fatal(err)
	}
	run(work)

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
func askGit(dir string) {
	_, _ = gitx.Run(dir, "version")
}

type asker struct{}

func (asker) askGit(dir string) {
	func() { _, _ = gitx.Run(dir, "version") }()
}

func TestCallerIsNamedWhenGitTraces(t *testing.T) {
	const pkg = "github.com/develdeco/jig/internal/gitx_test."
	cases := []struct {
		name string
		run  func(dir string)
		want string
	}{
		{"function", askGit, pkg + "askGit"},
		{"method and closure", asker{}.askGit, pkg + "asker.askGit"},
		{"replaced environment", func(dir string) {
			_, _ = gitx.RunReplaceEnv(dir, append(os.Environ(), "X_UNUSED=1"), "version")
		}, pkg + "TestCallerIsNamedWhenGitTraces"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := tracedGit(t, func(dir string) { c.run(dir) })
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
