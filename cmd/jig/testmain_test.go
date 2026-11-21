package main

import (
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/develdeco/jig/internal/gittest"
	"github.com/develdeco/jig/internal/gitx"
)

func TestMain(m *testing.M) {
	// See internal/verifydeliver/testmain_test.go: pins identity so any test
	// that drives a gate/publish/solve command through a fresh pool lease
	// still makes deterministic commits.
	gittest.PinIdentity()
	os.Exit(gittest.Run(m))
}

var (
	startOnce sync.Once
	startDir  string
	startErr  error
)

// startingDir is the directory a test's env starts in when the test names
// none: an empty repository made once for the whole test binary. The
// checkout's own directory would do as a cwd, but the work tree's top level
// it answers is a question for the git program wherever its config holds an
// include (CI's checkout does), and every test's locate asks it again; a
// plain repository is answered in process, and a test that cares where it
// runs names the directory with inDir.
func startingDir() (string, error) {
	startOnce.Do(func() {
		dir, err := os.MkdirTemp("", "jig-start")
		if err != nil {
			startErr = err
			return
		}
		gittest.AtExit(func() { os.RemoveAll(dir) })
		if _, err := gitx.Run(dir, "init", "-q", "-b", "main"); err != nil {
			startErr = err
			return
		}
		startDir, startErr = filepath.EvalSymlinks(dir)
	})
	return startDir, startErr
}
