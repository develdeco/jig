// Package e2e drives the built jig binary end to end against the fixture
// package's materialized repos and store, exercising the full command
// surface the way an operator would from a shell.
//
// NOTE (read before editing): at the time this suite was authored, cmd/jig,
// frontier, and verifydeliver were still being written by sibling agents in
// the same integration pass. Every test here compiles against packages that do
// exist (fixture, store, journal, project, manifest, gitx, session, tracker,
// outcome, axi) and drives the binary purely as a subprocess, so it never
// needs those packages to exist for e2e itself to compile. TestMain builds
// the binary once, before any test runs; if that build fails, TestMain
// prints the error and exits non-zero instead of letting every test run and
// report a misleading pass or skip.
//
// Two assumptions are called out because the design does not
// pin them down precisely and the CLI could not be exercised against a real
// build while this suite was written; both are marked inline at their point
// of use as well:
//
//  1. `jig solve` is assumed to accept the same `--backend`/`--scenario`
//     flags as `jig run`/`jig gate`, even though the CLI surface table in
//     CONTRACTS.md lists only `--yes`/`--answer` for solve. Without them,
//     DoD-5 (TestSolveOneProcess) cannot run deterministically in CI. If
//     cmd/jig's flag parser rejects unknown flags, add them there rather
//     than changing this test's expectations.
//  2. The status table's 5th column (`question`) is assumed to carry the
//     slice's Reason string (e.g. "attempt-cap") when the slice is stalled
//     or env-blocked and has no open question, since the column budget in
//     the contract's status format leaves no other place for it. See
//     TestCapExhaustion.
package e2e

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
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

	tmp, err := os.MkdirTemp("", "jig-e2e-bin")
	if err != nil {
		fmt.Fprintf(os.Stderr, "e2e: create temp dir for binary: %v\n", err)
		os.Exit(1)
	}

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

	code := gittest.Run(m)
	os.RemoveAll(tmp)
	os.Exit(code)
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
