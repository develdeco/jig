// Package e2e drives the built jig binary end to end against the fixture
// package's materialized repos and store, exercising the full command
// surface the way an operator would from a shell. TestMain builds the
// binary once, before any test runs; if that build fails, TestMain prints
// the error and exits non-zero instead of letting every test run and report
// a misleading pass or skip. JIG_E2E_BINARY names a jig binary built
// elsewhere - a release's own, as its installer put it - to run the suite
// against instead.
//
// jig starts no session, so the tests that need a ticket built play the part
// of the agent that builds it: the fake harness (harness_test.go) commits the
// fixture scenario's patches in the operator clone, fires the hooks a harness
// would, and runs jig as a session of its own, against the real binary
// and file-path remotes. TestDigesterFlow is the whole story of one ticket, from
// the brief to a recorded pull request; run it with -v to read it as a
// transcript.
package e2e

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/gittest"
)

// jigBinary is the path to the once-built jig.exe.
var (
	jigBinary string
	repoRoot  string
)

func TestMain(m *testing.M) {
	repoRoot = findRepoRoot()
	goCaches = hostGoCaches()

	tmp, err := os.MkdirTemp("", "jig-e2e-bin")
	if err != nil {
		fmt.Fprintf(os.Stderr, "e2e: create temp dir for binary: %v\n", err)
		os.Exit(1)
	}

	if given := os.Getenv("JIG_E2E_BINARY"); given != "" {
		// Absolute, since each test runs it from a directory of its own.
		abs, err := filepath.Abs(given)
		if err == nil {
			_, err = os.Stat(abs)
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "e2e: JIG_E2E_BINARY=%s: %v\n", given, err)
			os.RemoveAll(tmp)
			os.Exit(1)
		}
		jigBinary = abs
	} else {
		goBin := filepath.Join(runtime.GOROOT(), "bin", "go"+exeSuffix())
		out := filepath.Join(tmp, "jig"+exeSuffix())
		cmd := exec.Command(goBin, "build", "-buildvcs=false", "-o", out, filepath.Join(repoRoot, "cmd", "jig"))
		cmd.Dir = repoRoot
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		if err := cmd.Run(); err != nil {
			fmt.Fprintf(os.Stderr, "e2e: go build ../cmd/jig failed: %v: %s\n", err, stderr.String())
			os.RemoveAll(tmp)
			os.Exit(1)
		}
		jigBinary = out
	}

	// The real jig binary run by runJig inherits this process's environment
	// (see helpers_test.go), so pinning identity here also pins it for every
	// product commit the subprocess itself makes in a fresh pool lease - the
	// operator identity it would otherwise need is unavailable inside this
	// hermetic test environment. See
	// internal/verifydeliver/testmain_test.go for the same reasoning.
	gittest.PinIdentity()
	code := gittest.Run(m)
	os.RemoveAll(tmp)
	os.Exit(code)
}

// goCaches is GOCACHE and GOMODCACHE as this test process's go resolves
// them, set once by TestMain. A test that moves HOME for the jig it runs
// passes these along: on Linux and macOS go puts both caches under HOME, so
// every `go test` an oracle runs would otherwise build from a cold cache
// (Windows keeps them under LOCALAPPDATA, which the tests do not move).
var goCaches []string

// hostGoCaches asks go for its build and module caches. An answer go cannot
// give leaves the children to find their own, as before.
func hostGoCaches() []string {
	goBin := filepath.Join(runtime.GOROOT(), "bin", "go"+exeSuffix())
	out, err := exec.Command(goBin, "env", "GOCACHE", "GOMODCACHE").Output()
	if err != nil {
		return nil
	}
	var kv []string
	for i, v := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		v = strings.TrimSpace(v)
		if v == "" || v == "off" || i > 1 {
			continue
		}
		kv = append(kv, []string{"GOCACHE=", "GOMODCACHE="}[i]+v)
	}
	return kv
}

// findRepoRoot locates the product repo root (the directory containing
// cmd/jig, go.mod, etc.) relative to this source file, so tests work
// regardless of the caller's working directory.
func findRepoRoot() string {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		panic("e2e: cannot determine source file location")
	}
	// This file lives at <repoRoot>/e2e/main_test.go.
	return filepath.Dir(filepath.Dir(file))
}

func exeSuffix() string {
	if runtime.GOOS == "windows" {
		return ".exe"
	}
	return ""
}

// exitCodeOf extracts a subprocess's exit code, fataling the test if the
// command could not even be started (as opposed to exiting non-zero).
func exitCodeOf(t *testing.T, err error) int {
	t.Helper()
	if err == nil {
		return 0
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		return ee.ExitCode()
	}
	t.Fatalf("run jig: %v", err)
	return -1
}
