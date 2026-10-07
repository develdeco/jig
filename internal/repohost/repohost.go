package repohost

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os/exec"
	"regexp"
	"strings"

	"github.com/develdeco/jig/internal/axi"
)

// Host is a repository host capable of managing pull requests. nil means
// no pull request host (e.g., a local path remote).
type Host interface {
	// CreatePR opens a pull request for head against base with title and body from bodyFile.
	CreatePR(head, base, title, bodyFile string) (url string, err error)

	// CreatePRWithMedia opens a pull request with optional media files attached.
	CreatePRWithMedia(head, base, title, bodyFile, mediaDir string, mediaFiles []string) (url string, attached bool, err error)

	// FindOpenPR returns the URL of the open pull request from head into base, or "" when there is none.
	FindOpenPR(head, base string) (url string, err error)

	// UpdatePR replaces the body of the pull request at url.
	UpdatePR(url, bodyFile string) error

	// UpdatePRWithMedia replaces the body of the pull request at url with optional media attached.
	UpdatePRWithMedia(url, bodyFile, mediaDir string, mediaFiles []string) (attached bool, err error)

	// CommentPR posts a comment on the pull request at url.
	CommentPR(url, bodyFile string) error

	// ReadPRBody returns the current body of the pull request at url.
	ReadPRBody(url string) (string, error)
}

// Remote URL shapes: ssh (git@host:owner/repo[.git] or ssh://user@host/owner/repo[.git]),
// https/http (scheme://host/owner/repo[.git]).
var (
	sshRemoteRE    = regexp.MustCompile(`^[^@\s]+@github\.com:([^/]+)/(.+)$`)
	sshURLRemoteRE = regexp.MustCompile(`^ssh://[^@\s]+@github\.com/([^/]+)/(.+)$`)
	httpRemoteRE   = regexp.MustCompile(`^https?://github\.com/([^/]+)/(.+)$`)
)

// parseGitHubOwnerRepo extracts owner and repo from a GitHub remote URL.
// Returns error if the remote is not a GitHub URL.
func parseGitHubOwnerRepo(remote string) (string, string, error) {
	s := strings.TrimSpace(remote)
	s = strings.TrimSuffix(s, "/")
	s = strings.TrimSuffix(s, ".git")
	if m := sshRemoteRE.FindStringSubmatch(s); m != nil {
		return m[1], m[2], nil
	}
	if m := sshURLRemoteRE.FindStringSubmatch(s); m != nil {
		return m[1], m[2], nil
	}
	if m := httpRemoteRE.FindStringSubmatch(s); m != nil {
		return m[1], m[2], nil
	}
	return "", "", fmt.Errorf("repohost: not a GitHub remote: %q", remote)
}

// New returns a Host for the given remote, or nil if the remote
// is not a GitHub remote. It checks that gh is installed when returning a GitHub host.
func New(remote string) (Host, error) {
	return NewWithEnv(remote, "", nil)
}

// NewWithEnv is New with an explicit gh binary and environment: ghBin is
// resolved on PATH when empty, and env is the gh subprocess's whole
// environment, inherited from this process when nil. A test hands its own
// fake gh and the variables that drive it this way, instead of editing the
// environment its own process runs in.
func NewWithEnv(remote, ghBin string, env []string) (Host, error) {
	owner, repo, err := parseGitHubOwnerRepo(remote)
	if err != nil {
		return nil, nil
	}

	if ghBin == "" {
		ghBin, err = exec.LookPath("gh")
		if err != nil {
			return nil, &axi.Error{
				Msg:  "gh CLI is not installed",
				Code: "GH_NOT_INSTALLED",
				Help: []string{"Install gh from https://cli.github.com"},
			}
		}
	}

	return &githubHost{gh: ghBin, owner: owner, repo: repo, env: env}, nil
}

// githubHost is a GitHub pull request host.
type githubHost struct {
	gh            string
	owner         string
	repo          string
	attachSupport map[string]bool
	env           []string // gh's whole environment; nil inherits this process's
}

func (h *githubHost) repoSpec() string { return h.owner + "/" + h.repo }

// run invokes gh with args, returning trimmed stdout.
func (h *githubHost) run(args ...string) (string, error) {
	return h.runInDir("", args...)
}

// runInDir invokes gh with args in the given directory, returning trimmed
// stdout. If dir is empty, runs in the current directory. Trimmed stdout is
// returned even when gh fails (folded into the error's own message, after
// stderr): `gh pr create --attach` can print the pull request's URL on
// stdout and then fail attaching a file, and a caller that discarded stdout
// on that path would lose the one record of a pull request it already
// opened. With h.env set, the subprocess runs with that environment instead
// of inheriting this process's own.
func (h *githubHost) runInDir(dir string, args ...string) (string, error) {
	cmd := exec.Command(h.gh, args...)
	if dir != "" {
		cmd.Dir = dir
	}
	if h.env != nil {
		cmd.Env = h.env
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		out := strings.TrimSpace(stdout.String())
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = err.Error()
		}
		if out != "" {
			msg = fmt.Sprintf("%s (stdout: %s)", msg, out)
		}
		return out, fmt.Errorf("repohost: gh %s: %s", strings.Join(args, " "), msg)
	}
	return strings.TrimSpace(stdout.String()), nil
}

// supportsAttach checks whether the installed gh supports --attach on
// subcommand ("pr create" or "pr edit"), probing that exact subcommand's own
// --help: the two subcommands' flag sets are not the same thing, so a
// support check made against one must never gate the other. The result is
// cached per subcommand, after its first check.
func (h *githubHost) supportsAttach(subcommand string) bool {
	if supported, ok := h.attachSupport[subcommand]; ok {
		return supported
	}
	args := append(strings.Fields(subcommand), "--help")
	out, err := h.run(args...)
	supported := err == nil && strings.Contains(out, "--attach")
	if h.attachSupport == nil {
		h.attachSupport = map[string]bool{}
	}
	h.attachSupport[subcommand] = supported
	return supported
}

var pullRequestURLRE = regexp.MustCompile(`https://github\.com/[^/]+/[^/]+/pull/\d+`)

// CreatePR opens a GitHub pull request for head against base via gh pr create.
func (h *githubHost) CreatePR(head, base, title, bodyFile string) (string, error) {
	url, _, err := h.CreatePRWithMedia(head, base, title, bodyFile, "", nil)
	return url, err
}

// CreatePRWithMedia opens a GitHub pull request for head against base via
// `gh pr create`, run with mediaDir as gh's own working directory so each of
// mediaFiles names a plain file gh can find, adding one `--attach <file>`
// per file when mediaFiles is non-empty and the installed gh supports it.
// attached reports whether the flags were added; false whenever mediaFiles
// is empty or the installed gh does not support --attach, in which case the
// pull request is still opened, with no media.
func (h *githubHost) CreatePRWithMedia(head, base, title, bodyFile, mediaDir string, mediaFiles []string) (string, bool, error) {
	args := []string{"pr", "create", "--repo", h.repoSpec(), "--title", title, "--body-file", bodyFile, "--base", base, "--head", head}

	attached := len(mediaFiles) > 0 && h.supportsAttach("pr create")
	if attached {
		for _, f := range mediaFiles {
			args = append(args, "--attach", f)
		}
	}

	out, err := h.runInDir(mediaDir, args...)
	if err != nil {
		return "", false, err
	}
	lines := strings.Split(out, "\n")
	url := strings.TrimSpace(lines[len(lines)-1])
	if url == "" {
		return "", false, fmt.Errorf("repohost: gh pr create: could not parse PR url from %q", out)
	}
	return url, attached, nil
}

// pullRequest is one row of the pull requests REST endpoint's answer.
type pullRequest struct {
	HTMLURL string `json:"html_url"`
}

// FindOpenPR returns the open pull request from head into base in this
// repo, asked of the pull requests endpoint with the qualified head filter
// (`owner:branch`), which GitHub applies itself: a pull request from a fork
// whose branch happens to share head's name is another head and is never in
// the answer. (`gh pr list --head` takes the bare branch name, so it lists
// every fork's, one page of 30 at a time, and an answer the page cut short
// reads as "none".) At most one open pull request can exist for a head and
// base, so more than one is an error, not a choice.
func (h *githubHost) FindOpenPR(head, base string) (string, error) {
	out, err := h.run("api", "repos/"+h.repoSpec()+"/pulls", "--method", "GET",
		"-f", "head="+h.owner+":"+head, "-f", "base="+base, "-f", "state=open")
	if err != nil {
		return "", err
	}
	var rows []pullRequest
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		return "", fmt.Errorf("repohost: parse gh api pulls output %q: %w", out, err)
	}
	urls := make([]string, len(rows))
	for i, r := range rows {
		urls[i] = r.HTMLURL
	}
	switch len(urls) {
	case 0:
		return "", nil
	case 1:
		return urls[0], nil
	}
	return "", fmt.Errorf("repohost: gh api pulls found %d open pull requests from %s into %s in %s: %s", len(urls), head, base, h.repoSpec(), strings.Join(urls, " "))
}

// UpdatePR replaces the body of the pull request at url.
func (h *githubHost) UpdatePR(url, bodyFile string) error {
	_, err := h.UpdatePRWithMedia(url, bodyFile, "", nil)
	return err
}

// UpdatePRWithMedia replaces the body of the pull request at url via `gh pr
// edit --body-file`, run with mediaDir as gh's own working directory, adding
// one `--attach <file>` per file when mediaFiles is non-empty and the
// installed gh's `pr edit` supports it (checked on its own: `pr create` and
// `pr edit` do not necessarily agree). attached reports whether the flags
// were added, the same as CreatePRWithMedia's own.
func (h *githubHost) UpdatePRWithMedia(url, bodyFile, mediaDir string, mediaFiles []string) (bool, error) {
	args := []string{"pr", "edit", url, "--repo", h.repoSpec(), "--body-file", bodyFile}

	attached := len(mediaFiles) > 0 && h.supportsAttach("pr edit")
	if attached {
		for _, f := range mediaFiles {
			args = append(args, "--attach", f)
		}
	}

	_, err := h.runInDir(mediaDir, args...)
	return attached, err
}

// CommentPR posts a comment on the pull request at url.
func (h *githubHost) CommentPR(url, bodyFile string) error {
	_, err := h.run("pr", "comment", url, "--repo", h.repoSpec(), "--body-file", bodyFile)
	return err
}

// ReadPRBody reads the body of the pull request at url.
func (h *githubHost) ReadPRBody(url string) (string, error) {
	out, err := h.run("pr", "view", url, "--repo", h.repoSpec(), "--json", "body")
	if err != nil {
		return "", err
	}
	var result map[string]string
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		return "", fmt.Errorf("repohost: failed to parse pr view output: %w", err)
	}
	return result["body"], nil
}
