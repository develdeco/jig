// Package gitx wraps the handful of git plumbing calls shared by pool,
// make, and verifydeliver: always argv-based (never a shell), always
// scoped to a working directory.
package gitx

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/develdeco/jig/axi"
)

// Run invokes git with args in dir, returning trimmed stdout. On failure the
// returned error wraps git's stderr output.
func Run(dir string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		return "", fmt.Errorf("git %s: %s", strings.Join(args, " "), msg)
	}
	return strings.TrimSpace(stdout.String()), nil
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
