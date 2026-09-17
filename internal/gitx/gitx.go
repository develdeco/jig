// Package gitx owns git process execution: product and test code run git
// only through this package (a lint test enforces it). Every call is
// argv-based, scoped to a working directory, and runs with git's automatic
// background maintenance off for that one process; nothing is persisted, so
// a user's own git commands still maintain their repos.
package gitx

import (
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/develdeco/jig/internal/axi"
)

// Run invokes git with args in dir, returning trimmed stdout. On failure the
// error carries git's stderr and wraps the exec error (typically
// *exec.ExitError).
func Run(dir string, args ...string) (string, error) {
	return RunEnv(dir, nil, args...)
}

// RunEnv is Run with env appended to the process environment, for pinning an
// identity (GIT_AUTHOR_NAME and friends) without persisting it.
func RunEnv(dir string, env []string, args ...string) (string, error) {
	var stdout, stderr bytes.Buffer
	if err := run(dir, env, &stdout, &stderr, args); err != nil {
		return "", callError(args, stderr.String(), err)
	}
	return strings.TrimSpace(stdout.String()), nil
}

// RunRaw is Run returning git's untrimmed combined stdout and stderr, for
// callers that compare exact output.
func RunRaw(dir string, args ...string) (string, error) {
	var out bytes.Buffer
	if err := run(dir, nil, &out, &out, args); err != nil {
		return out.String(), callError(args, out.String(), err)
	}
	return out.String(), nil
}

// run spawns git in dir with "-c maintenance.auto=false" ahead of args, so
// no call leaves git's detached maintenance running after it returns.
func run(dir string, env []string, stdout, stderr io.Writer, args []string) error {
	cmd := exec.Command("git", append([]string{"-c", "maintenance.auto=false"}, args...)...)
	cmd.Dir = dir
	if len(env) > 0 {
		cmd.Env = append(os.Environ(), env...)
	}
	cmd.Stdout = stdout
	cmd.Stderr = stderr
	return cmd.Run()
}

// callError formats a failed call as "git <args>: <output>: <exec error>",
// omitting output when git printed nothing.
func callError(args []string, output string, err error) error {
	if msg := strings.TrimSpace(output); msg != "" {
		return fmt.Errorf("git %s: %s: %w", strings.Join(args, " "), msg, err)
	}
	return fmt.Errorf("git %s: %w", strings.Join(args, " "), err)
}

// MaintenanceAuto runs "git maintenance run --auto" in the foreground in dir.
// Each task checks its own threshold, so the call is cheap when there is
// nothing to do. The per-call maintenance.auto=false only stops commands
// from spawning detached maintenance; it does not disable this explicit run.
// gc.autoDetach=false keeps the gc task's "git gc --auto" child (the packing
// path before git 2.54) from detaching, so the work is done on return.
func MaintenanceAuto(dir string) error {
	_, err := Run(dir, "-c", "gc.autoDetach=false", "maintenance", "run", "--auto")
	return err
}

// RevParse resolves ref to a full commit sha in dir.
func RevParse(dir, ref string) (string, error) {
	return Run(dir, "rev-parse", ref)
}

// MergeBase returns the merge base of a and b in dir.
func MergeBase(dir, a, b string) (string, error) {
	return Run(dir, "merge-base", a, b)
}

// CommitsIn returns the commit shas selected by rangeSpec (git rev-list),
// oldest ordering as git prints it.
func CommitsIn(dir, rangeSpec string) ([]string, error) {
	out, err := Run(dir, "rev-list", rangeSpec)
	if err != nil {
		return nil, err
	}
	if out == "" {
		return nil, nil
	}
	return strings.Split(out, "\n"), nil
}

// CommitOnAnyRemote reports whether sha is reachable from any remote-
// tracking branch in dir.
func CommitOnAnyRemote(dir, sha string) (bool, error) {
	out, err := Run(dir, "branch", "-r", "--contains", sha)
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(out) != "", nil
}

// IsLocalRemote reports whether url points at the local filesystem: an
// absolute path (Windows or Unix style), a file:// URL, or an existing
// directory path.
func IsLocalRemote(url string) bool {
	url = strings.TrimSpace(url)
	if url == "" {
		return false
	}
	if strings.HasPrefix(url, "file://") {
		return true
	}
	if isAbsPath(url) {
		return true
	}
	if fi, err := os.Stat(url); err == nil && fi.IsDir() {
		return true
	}
	return false
}

// isAbsPath reports whether s looks like an absolute filesystem path,
// recognizing both the host OS's convention (so C:\x\y is absolute on
// Windows) and a leading "/" (so /tmp/x is treated as absolute even when
// jig itself runs on Windows).
func isAbsPath(s string) bool {
	if filepath.IsAbs(s) {
		return true
	}
	if strings.HasPrefix(s, "/") {
		return true
	}
	// A Windows drive path is a local path shape on any host OS (a machine
	// mapping may carry paths recorded on another machine); it is never a
	// network remote.
	if len(s) >= 3 && s[1] == ':' && (s[2] == '\\' || s[2] == '/') &&
		(('A' <= s[0] && s[0] <= 'Z') || ('a' <= s[0] && s[0] <= 'z')) {
		return true
	}
	return false
}

// pushRefusedCode is the axi.Error code returned by GuardedPush when it
// refuses to push to a non-local remote without confirmation.
const pushRefusedCode = "PUSH_REFUSED"

// GuardedPush pushes refspec to remote in dir, but refuses when the remote
// resolves to a non-local URL and confirmed is false. Passing confirmed
// true allows the push to proceed regardless of the remote's locality.
func GuardedPush(dir, remote, refspec string, confirmed bool) error {
	url, err := Run(dir, "remote", "get-url", remote)
	if err != nil {
		return fmt.Errorf("gitx: resolve remote %s: %w", remote, err)
	}
	if !IsLocalRemote(url) && !confirmed {
		return &axi.Error{
			Msg:  "remote is not a local path and the publish confirm has not run",
			Code: pushRefusedCode,
		}
	}
	if _, err := Run(dir, "push", remote, refspec); err != nil {
		return fmt.Errorf("gitx: push %s %s: %w", remote, refspec, err)
	}
	return nil
}
