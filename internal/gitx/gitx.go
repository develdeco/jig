// Package gitx owns git process execution: product and test code run git
// only through this package (a lint test enforces it). Every call is
// argv-based, scoped to a working directory, and runs with git's automatic
// background maintenance off for that one process; nothing is persisted, so
// a user's own git commands still maintain their repos.
package gitx

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
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

// IsAncestor reports whether ancestor is an ancestor of (or equal to)
// descendant in dir, via "git merge-base --is-ancestor": exit 0 is true,
// exit 1 is false (not an ancestor, not an error), and any other outcome
// (e.g. an unknown object after a rebase) is an error.
func IsAncestor(dir, ancestor, descendant string) (bool, error) {
	args := []string{"merge-base", "--is-ancestor", ancestor, descendant}
	var stdout, stderr bytes.Buffer
	err := run(dir, nil, &stdout, &stderr, args)
	if err == nil {
		return true, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		return false, nil
	}
	return false, callError(args, stderr.String(), err)
}

// DiffNameOnly returns the files base..head touches in dir, restricted to
// diffFilter (git's --diff-filter letters, e.g. "AMT" for added/modified/
// type-changed, or "D" for deleted) with renames off, so a renamed file
// counts as its new path under "A" and its old path under "D". Paths come
// back exactly as git prints them: repo-relative with forward slashes,
// tree order (not sorted).
func DiffNameOnly(dir, base, head, diffFilter string) ([]string, error) {
	args := []string{"diff", "--name-only", "-z", "--no-renames", "--diff-filter=" + diffFilter, base, head}
	var stdout, stderr bytes.Buffer
	if err := run(dir, nil, &stdout, &stderr, args); err != nil {
		return nil, callError(args, stderr.String(), err)
	}
	raw := strings.TrimSuffix(stdout.String(), "\x00")
	if raw == "" {
		return nil, nil
	}
	return strings.Split(raw, "\x00"), nil
}

// FileExistsAtRev reports whether path exists as a blob in dir's tree at
// rev. Any git failure other than "no such object" (an unreadable repo, a
// bad rev) is returned as an error rather than folded into false.
func FileExistsAtRev(dir, rev, path string) (bool, error) {
	args := []string{"cat-file", "-e", rev + ":" + path}
	var stdout, stderr bytes.Buffer
	err := run(dir, nil, &stdout, &stderr, args)
	if err == nil {
		return true, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return false, nil
	}
	return false, callError(args, stderr.String(), err)
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

// identityRequiredCode is the axi.Error code IdentityEnv and CheckIdentity
// return when git cannot resolve an author or committer identity in dir.
const identityRequiredCode = "IDENTITY_REQUIRED"

// identRe splits "git var GIT_AUTHOR_IDENT"'s output - "Name <email>
// <timestamp> <zone>" - into its name and email.
var identRe = regexp.MustCompile(`^(.*) <([^>]*)> \d+ [+-]\d{4}$`)

// IdentityEnv resolves the author and committer identity a commit in dir
// would get ("git var GIT_AUTHOR_IDENT" and "GIT_COMMITTER_IDENT", which
// honor the environment and dir's config) and returns it as
// GIT_AUTHOR_NAME/EMAIL and GIT_COMMITTER_NAME/EMAIL entries, without dates.
// Passing them to RunEnv makes a commit in another clone carry dir's
// identity. It fails with IDENTITY_REQUIRED when git has none.
func IdentityEnv(dir string) ([]string, error) {
	authorIdent, err := Run(dir, "var", "GIT_AUTHOR_IDENT")
	if err != nil {
		return nil, identityRequiredError(dir, err)
	}
	committerIdent, err := Run(dir, "var", "GIT_COMMITTER_IDENT")
	if err != nil {
		return nil, identityRequiredError(dir, err)
	}
	authorName, authorEmail, err := parseIdent(authorIdent)
	if err != nil {
		return nil, identityRequiredError(dir, err)
	}
	committerName, committerEmail, err := parseIdent(committerIdent)
	if err != nil {
		return nil, identityRequiredError(dir, err)
	}
	return []string{
		"GIT_AUTHOR_NAME=" + authorName,
		"GIT_AUTHOR_EMAIL=" + authorEmail,
		"GIT_COMMITTER_NAME=" + committerName,
		"GIT_COMMITTER_EMAIL=" + committerEmail,
	}, nil
}

// parseIdent splits one "git var" identity line into name and email.
func parseIdent(ident string) (name, email string, err error) {
	m := identRe.FindStringSubmatch(strings.TrimSpace(ident))
	if m == nil {
		return "", "", fmt.Errorf("unexpected identity format %q", ident)
	}
	return m[1], m[2], nil
}

// CheckIdentity reports IdentityEnv's error for dir, if any.
func CheckIdentity(dir string) error {
	_, err := IdentityEnv(dir)
	return err
}

// identityRequiredError reports a missing identity in one line, keeping only
// git's final fatal line; the setup commands go in Help.
func identityRequiredError(dir string, cause error) error {
	return &axi.Error{
		Msg:  fmt.Sprintf("git has no author/committer identity for %s: %s", dir, lastLine(cause.Error())),
		Code: identityRequiredCode,
		Help: []string{
			"Run `git config --global user.name \"Your Name\"`",
			"Run `git config --global user.email \"you@example.com\"`",
		},
	}
}

// lastLine returns s's final non-empty line, trimmed.
func lastLine(s string) string {
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if t := strings.TrimSpace(lines[i]); t != "" {
			return t
		}
	}
	return strings.TrimSpace(s)
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
