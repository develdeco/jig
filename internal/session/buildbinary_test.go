package session

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/gittest"
)

// Helper binaries are built once per test binary and shared by every test
// that runs them (the pattern of fixture's envtool helper).
var (
	binMu    sync.Mutex
	binBuilt = map[string]binBuild{}
)

type binBuild struct {
	dir string
	err error
}

// buildBinary compiles the Go package at pkg (relative to the repo root) into
// a binary named name (plus .exe on Windows) inside its own directory, and
// returns that directory. The build happens once per test binary: the
// directory is an os.MkdirTemp dir (a t.TempDir would vanish with the first
// test that used it), removed through gittest.AtExit.
func buildBinary(t *testing.T, pkg, name string) string {
	t.Helper()
	binMu.Lock()
	defer binMu.Unlock()
	key := pkg + "|" + name
	b, ok := binBuilt[key]
	if !ok {
		b = compileBinary(fixture.RepoRoot(t), pkg, name)
		binBuilt[key] = b
	}
	if b.err != nil {
		t.Fatalf("%v", b.err)
	}
	return b.dir
}

func compileBinary(root, pkg, name string) binBuild {
	dir, err := os.MkdirTemp("", "jig-session-bin")
	if err != nil {
		return binBuild{err: fmt.Errorf("create build dir: %w", err)}
	}
	gittest.AtExit(func() { os.RemoveAll(dir) })
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	goBin := filepath.Join(runtime.GOROOT(), "bin", "go")
	if runtime.GOOS == "windows" {
		goBin += ".exe"
	}
	cmd := exec.Command(goBin, "build", "-buildvcs=false", "-o", filepath.Join(dir, name), filepath.Join(root, pkg))
	if output, err := cmd.CombinedOutput(); err != nil {
		return binBuild{err: fmt.Errorf("build %s: %v\n%s", pkg, err, output)}
	}
	return binBuild{dir: dir}
}
