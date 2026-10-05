package tracker

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/project"
)

// githubAdapter projects tickets onto GitHub Issues via the gh CLI,
// invoked as argv (never through a shell).
type githubAdapter struct {
	gh            string
	owner         string
	repo          string
	attachSupport map[string]bool // cached --attach support, keyed by subcommand ("pr create", "pr edit")
}

func newGithubAdapter(cfg project.Config) (*githubAdapter, error) {
	if len(cfg.Repos) == 0 {
		return nil, &axi.Error{
			Msg:  "github tracker requires at least one repo in project.yaml",
			Code: "VALIDATION_ERROR",
		}
	}
	owner, repo, err := parseOwnerRepo(cfg.Repos[0].Remote)
	if err != nil {
		return nil, &axi.Error{Msg: err.Error(), Code: "VALIDATION_ERROR"}
	}
	ghPath, err := exec.LookPath("gh")
	if err != nil {
		return nil, &axi.Error{
			Msg:  "gh CLI is not installed",
			Code: "GH_NOT_INSTALLED",
			Help: []string{"Install gh from https://cli.github.com"},
		}
	}
	return &githubAdapter{gh: ghPath, owner: owner, repo: repo}, nil
}

func (a *githubAdapter) Name() string { return "github" }

func (a *githubAdapter) repoSpec() string { return a.owner + "/" + a.repo }

// Remote URL shapes: ssh (git@host:owner/repo[.git]), https/http
// (scheme://host/owner/repo[.git]), and plain ("owner/repo", also matching
// a bare local path used in tests).
var (
	sshRemoteRE   = regexp.MustCompile(`^[^@\s]+@[^:\s]+:([^/]+)/(.+)$`)
	httpRemoteRE  = regexp.MustCompile(`^https?://[^/]+/([^/]+)/(.+)$`)
	plainRemoteRE = regexp.MustCompile(`^([^/\\]+)/([^/\\]+)$`)
)

func parseOwnerRepo(remote string) (string, string, error) {
	s := strings.TrimSpace(remote)
	s = strings.TrimSuffix(s, "/")
	s = strings.TrimSuffix(s, ".git")
	if m := sshRemoteRE.FindStringSubmatch(s); m != nil {
		return m[1], m[2], nil
	}
	if m := httpRemoteRE.FindStringSubmatch(s); m != nil {
		return m[1], m[2], nil
	}
	if m := plainRemoteRE.FindStringSubmatch(s); m != nil {
		return m[1], m[2], nil
	}
	return "", "", fmt.Errorf("tracker: cannot parse owner/repo from remote %q", remote)
}

// run invokes gh with args, returning trimmed stdout.
func (a *githubAdapter) run(args ...string) (string, error) {
	return a.runInDir("", args...)
}

// runInDir invokes gh with args in the given directory, returning trimmed
// stdout. If dir is empty, runs in the current directory. Trimmed stdout is
// returned even when gh fails (folded into the error's own message, after
// stderr): `gh pr create --attach` can print the pull request's URL on
// stdout and then fail attaching a file, and a caller that discarded stdout
// on that path would lose the one record of a pull request it already
// opened.
func (a *githubAdapter) runInDir(dir string, args ...string) (string, error) {
	cmd := exec.Command(a.gh, args...)
	if dir != "" {
		cmd.Dir = dir
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
		return out, fmt.Errorf("tracker: gh %s: %s", strings.Join(args, " "), msg)
	}
	return strings.TrimSpace(stdout.String()), nil
}

// issueNumber strips a leading "#" from a tracker id, validating it is
// numeric (gh issue commands take a bare number).
func issueNumber(id string) (string, error) {
	n := strings.TrimPrefix(id, "#")
	if n == "" {
		return "", fmt.Errorf("tracker: empty issue id")
	}
	if _, err := strconv.Atoi(n); err != nil {
		return "", fmt.Errorf("tracker: invalid issue id %q", id)
	}
	return n, nil
}

var issueURLRE = regexp.MustCompile(`/issues/(\d+)`)

// Mint creates a GitHub issue and returns its id as "#<number>".
func (a *githubAdapter) Mint(d Draft) (string, error) {
	out, err := a.run("issue", "create", "--repo", a.repoSpec(), "--title", d.Title, "--body", d.Body)
	if err != nil {
		return "", err
	}
	m := issueURLRE.FindStringSubmatch(out)
	if m == nil {
		return "", fmt.Errorf("tracker: gh issue create: could not parse issue number from %q", out)
	}
	return "#" + m[1], nil
}

// supportsAttach checks whether the installed gh supports --attach on
// subcommand ("pr create" or "pr edit"), probing that exact subcommand's own
// --help: the two subcommands' flag sets are not the same thing, so a
// support check made against one must never gate the other. The result is
// cached per subcommand, after its first check.
func (a *githubAdapter) supportsAttach(subcommand string) bool {
	if supported, ok := a.attachSupport[subcommand]; ok {
		return supported
	}
	args := append(strings.Fields(subcommand), "--help")
	out, err := a.run(args...)
	supported := err == nil && strings.Contains(out, "--attach")
	if a.attachSupport == nil {
		a.attachSupport = map[string]bool{}
	}
	a.attachSupport[subcommand] = supported
	return supported
}

// CreatePR implements tracker.PRCreator: it opens a GitHub pull request for
// head against base via `gh pr create`, invoked as argv (never through a
// shell, same as every other gh call this adapter makes), and returns the
// PR's URL parsed from the last line of gh's stdout.
func (a *githubAdapter) CreatePR(head, base, title, bodyFile string) (string, error) {
	url, _, err := a.CreatePRWithMedia(head, base, title, bodyFile, "", nil)
	return url, err
}

// CreatePRWithMedia implements tracker.PRCreatorWithMedia: it opens a GitHub
// pull request for head against base via `gh pr create`, run with mediaDir as
// gh's own working directory so each of mediaFiles names a plain file gh can
// find, adding one `--attach <file>` per file when mediaFiles is non-empty
// and the installed gh supports it. attached reports whether the flags were
// added; false whenever mediaFiles is empty or the installed gh does not
// support --attach, in which case the pull request is still opened, with no
// media.
func (a *githubAdapter) CreatePRWithMedia(head, base, title, bodyFile, mediaDir string, mediaFiles []string) (string, bool, error) {
	args := []string{"pr", "create", "--repo", a.repoSpec(), "--title", title, "--body-file", bodyFile, "--base", base, "--head", head}

	attached := len(mediaFiles) > 0 && a.supportsAttach("pr create")
	if attached {
		for _, f := range mediaFiles {
			args = append(args, "--attach", f)
		}
	}

	out, err := a.runInDir(mediaDir, args...)
	if err != nil {
		return "", false, err
	}
	lines := strings.Split(out, "\n")
	url := strings.TrimSpace(lines[len(lines)-1])
	if url == "" {
		return "", false, fmt.Errorf("tracker: gh pr create: could not parse PR url from %q", out)
	}
	return url, attached, nil
}

// pullRequest is one row of the pull requests REST endpoint's answer.
type pullRequest struct {
	HTMLURL string `json:"html_url"`
}

// FindOpenPR implements tracker.PRUpdater: the open pull request from head
// into base in this repo, asked of the pull requests endpoint with the
// qualified head filter (`owner:branch`), which GitHub applies itself: a pull
// request from a fork whose branch happens to share head's name is another
// head and is never in the answer. (`gh pr list --head` takes the bare branch
// name, so it lists every fork's, one page of 30 at a time, and an answer the
// page cut short reads as "none".) At most one open pull request can exist for
// a head and base, so more than one is an error, not a choice.
func (a *githubAdapter) FindOpenPR(head, base string) (string, error) {
	out, err := a.run("api", "repos/"+a.repoSpec()+"/pulls", "--method", "GET",
		"-f", "head="+a.owner+":"+head, "-f", "base="+base, "-f", "state=open")
	if err != nil {
		return "", err
	}
	var rows []pullRequest
	if err := json.Unmarshal([]byte(out), &rows); err != nil {
		return "", fmt.Errorf("tracker: parse gh api pulls output %q: %w", out, err)
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
	return "", fmt.Errorf("tracker: gh api pulls found %d open pull requests from %s into %s in %s: %s", len(urls), head, base, a.repoSpec(), strings.Join(urls, " "))
}

// UpdatePR implements tracker.PRUpdater: it replaces the body of the pull
// request at url with bodyFile's content via `gh pr edit --body-file`.
func (a *githubAdapter) UpdatePR(url, bodyFile string) error {
	_, err := a.UpdatePRWithMedia(url, bodyFile, "", nil)
	return err
}

// UpdatePRWithMedia implements tracker.PRUpdaterWithMedia: it replaces the
// body of the pull request at url via `gh pr edit --body-file`, run with
// mediaDir as gh's own working directory, adding one `--attach <file>` per
// file when mediaFiles is non-empty and the installed gh's `pr edit`
// supports it (checked on its own: `pr create` and `pr edit` do not
// necessarily agree). attached reports whether the flags were added, the
// same as CreatePRWithMedia's own.
func (a *githubAdapter) UpdatePRWithMedia(url, bodyFile, mediaDir string, mediaFiles []string) (bool, error) {
	args := []string{"pr", "edit", url, "--repo", a.repoSpec(), "--body-file", bodyFile}

	attached := len(mediaFiles) > 0 && a.supportsAttach("pr edit")
	if attached {
		for _, f := range mediaFiles {
			args = append(args, "--attach", f)
		}
	}

	_, err := a.runInDir(mediaDir, args...)
	return attached, err
}

// CommentPR implements tracker.PRCommenter: it posts a comment on the pull
// request at url with the content of bodyFile via `gh pr comment`.
func (a *githubAdapter) CommentPR(url, bodyFile string) error {
	_, err := a.run("pr", "comment", url, "--repo", a.repoSpec(), "--body-file", bodyFile)
	return err
}

// ReadPRBody reads the body of the pull request at url via `gh pr view`.
func (a *githubAdapter) ReadPRBody(url string) (string, error) {
	out, err := a.run("pr", "view", url, "--repo", a.repoSpec(), "--json", "body")
	if err != nil {
		return "", err
	}
	// Parse the JSON output to extract the body field
	var result map[string]string
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		return "", fmt.Errorf("tracker: failed to parse pr view output: %w", err)
	}
	return result["body"], nil
}

// Comment posts body as a comment on ticketID.
func (a *githubAdapter) Comment(ticketID string, body string) error {
	n, err := issueNumber(ticketID)
	if err != nil {
		return err
	}
	_, err = a.run("issue", "comment", n, "--repo", a.repoSpec(), "--body", body)
	return err
}

// Project sets ticketID's body to p.Description, links each subtask as a
// GitHub sub-issue with its blocked-by relations, and posts p.Comments.
func (a *githubAdapter) Project(ticketID string, p Projection) error {
	n, err := issueNumber(ticketID)
	if err != nil {
		return err
	}
	if _, err := a.run("issue", "edit", n, "--repo", a.repoSpec(), "--body", p.Description); err != nil {
		return err
	}

	if len(p.Subtasks) > 0 {
		parentNodeID, err := a.resolveNodeID(n)
		if err != nil {
			return err
		}
		for _, sub := range p.Subtasks {
			childN, err := issueNumber(sub.ID)
			if err != nil {
				return err
			}
			childNodeID, err := a.resolveNodeID(childN)
			if err != nil {
				return err
			}
			if err := a.addSubIssue(parentNodeID, childNodeID); err != nil {
				return err
			}
			for _, dep := range sub.BlockedBy {
				depN, err := issueNumber(dep)
				if err != nil {
					return err
				}
				depNodeID, err := a.resolveNodeID(depN)
				if err != nil {
					return err
				}
				if err := a.blockedBy(childN, depNodeID); err != nil {
					return err
				}
			}
		}
	}

	for _, c := range p.Comments {
		if err := a.Comment(ticketID, c); err != nil {
			return err
		}
	}
	return nil
}

// graphqlResolveResp is the shape read back from the node-id resolve
// query. It only reads .data.repository.issue.id, so it tolerates any
// extra sibling fields a real (or stubbed) gh response also returns.
type graphqlResolveResp struct {
	Data struct {
		Repository struct {
			Issue *struct {
				ID string `json:"id"`
			} `json:"issue"`
		} `json:"repository"`
	} `json:"data"`
}

// resolveNodeID resolves an issue number to its GraphQL node id.
func (a *githubAdapter) resolveNodeID(number string) (string, error) {
	query := fmt.Sprintf(
		`query { repository(owner: "%s", name: "%s") { issue: issue(number: %s) { id } } }`,
		a.owner, a.repo, number,
	)
	out, err := a.run("api", "graphql", "-f", "query="+query)
	if err != nil {
		return "", err
	}
	var resp graphqlResolveResp
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		return "", fmt.Errorf("tracker: parse graphql resolve response %q: %w", out, err)
	}
	if resp.Data.Repository.Issue == nil || resp.Data.Repository.Issue.ID == "" {
		return "", fmt.Errorf("tracker: graphql resolve returned no node id for issue #%s", number)
	}
	return resp.Data.Repository.Issue.ID, nil
}

// addSubIssue links childNodeID as a sub-issue of parentNodeID.
func (a *githubAdapter) addSubIssue(parentNodeID, childNodeID string) error {
	mutation := fmt.Sprintf(
		`mutation { addSubIssue(input: { issueId: "%s", subIssueId: "%s" }) { subIssue { number } } }`,
		parentNodeID, childNodeID,
	)
	_, err := a.run("api", "graphql", "-f", "query="+mutation)
	return err
}

// blockedBy records that issue childNumber is blocked by the issue whose
// node id is blockingNodeID.
func (a *githubAdapter) blockedBy(childNumber, blockingNodeID string) error {
	path := fmt.Sprintf("repos/%s/%s/issues/%s/dependencies/blocked_by", a.owner, a.repo, childNumber)
	_, err := a.run("api", path, "--method", "POST", "-F", "issue_id="+blockingNodeID)
	return err
}
