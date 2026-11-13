package gitx

import (
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestNoCallerWithoutATrace(t *testing.T) {
	for _, env := range [][]string{
		{"A=1", "GIT_TRACE2=1", "B=2"},
		{"GIT_TRACE2_EVENT=", "A=1"},
		{"GIT_TRACE2_EVENT=/x", "GIT_TRACE2_EVENT="},
		nil,
	} {
		if got := withCaller(slices.Clone(env), "."); !slices.Equal(got, env) {
			t.Errorf("withCaller(%v) = %v; the environment must be unchanged", env, got)
		}
	}
}

func TestCallerVariablesAreAddedWithATrace(t *testing.T) {
	got := withCaller([]string{"GIT_TRACE2_EVENT=/x"}, "")
	if !slices.Contains(got, "JIG_GIT_DIR=") {
		t.Errorf("empty dir should give an empty JIG_GIT_DIR: %v", got)
	}
	if !slices.ContainsFunc(got, func(s string) bool { return strings.HasPrefix(s, "JIG_GIT_CALLER=") }) {
		t.Errorf("no JIG_GIT_CALLER: %v", got)
	}
}

func TestCallerDirIsAbsolute(t *testing.T) {
	got := withCaller([]string{"GIT_TRACE2_EVENT=/x"}, "rel/dir")
	want, err := filepath.Abs("rel/dir")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(got, "JIG_GIT_DIR="+want) {
		t.Errorf("want JIG_GIT_DIR=%s in %v", want, got)
	}
}

func TestCallerNameStripsClosuresAndSkipsTheStandardLibrary(t *testing.T) {
	for in, want := range map[string]string{
		"a.example/m/pkg.F.func1":       "a.example/m/pkg.F",
		"a.example/m/pkg.F.func1.2":     "a.example/m/pkg.F",
		"a.example/m/pkg.F.func1.func2": "a.example/m/pkg.F",
		"a.example/m/pkg.(*T).M.func3":  "a.example/m/pkg.(*T).M",
		"a.example/m/pkg.F":             "a.example/m/pkg.F",
	} {
		if got := closureSuffix.ReplaceAllString(in, ""); got != want {
			t.Errorf("closureSuffix on %q = %q, want %q", in, got, want)
		}
	}
	for name, std := range map[string]bool{
		"testing.tRunner": true, "runtime.goexit": true, "main.run": false, "os/exec.(*Cmd).Run": true,
		"a.example/m/pkg.F": false, "github.com/x/y.Z": false,
	} {
		if isStdlib(name) != std {
			t.Errorf("isStdlib(%q) = %v, want %v", name, !std, std)
		}
	}
}

// Called straight from a test of this package, the only frames are gitx's
// own, the testing package's and the runtime's: all skipped, so the caller is
// named by an empty value rather than by one of them.
func TestCallerIsEmptyWhenOnlyOwnAndStandardFramesAreOnTheStack(t *testing.T) {
	got := withCaller([]string{"GIT_TRACE2_EVENT=/x"}, "")
	if !slices.Contains(got, "JIG_GIT_CALLER=") {
		t.Errorf("want JIG_GIT_CALLER with an empty value: %v", got)
	}
}
