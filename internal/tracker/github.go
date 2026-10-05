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
	"github.com/develdeco/jig/internal/repohost"
)

// githubAdapter projects tickets onto GitHub Issues via the gh CLI,
// invoked as argv (never through a shell).
type githubAdapter struct {
	gh    string
	owner string
	repo  string
	host  repohost.Host
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
	host, err := repohost.New(cfg.Repos[0].Remote)
	if err != nil {
		return nil, &axi.Error{Msg: err.Error(), Code: "GH_NOT_INSTALLED"}
	}
	return &githubAdapter{gh: ghPath, owner: owner, repo: repo, host: host}, nil
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

// PR-related methods delegate to the repohost.Host instance.

// CreatePR opens a pull request via the host.
func (a *githubAdapter) CreatePR(head, base, title, bodyFile string) (string, error) {
	return a.host.CreatePR(head, base, title, bodyFile)
}

// CreatePRWithMedia opens a pull request with media via the host.
func (a *githubAdapter) CreatePRWithMedia(head, base, title, bodyFile, mediaDir string, mediaFiles []string) (string, bool, error) {
	return a.host.CreatePRWithMedia(head, base, title, bodyFile, mediaDir, mediaFiles)
}

// FindOpenPR finds an open pull request via the host.
func (a *githubAdapter) FindOpenPR(head, base string) (string, error) {
	return a.host.FindOpenPR(head, base)
}

// UpdatePR updates a pull request via the host.
func (a *githubAdapter) UpdatePR(url, bodyFile string) error {
	return a.host.UpdatePR(url, bodyFile)
}

// UpdatePRWithMedia updates a pull request with media via the host.
func (a *githubAdapter) UpdatePRWithMedia(url, bodyFile, mediaDir string, mediaFiles []string) (bool, error) {
	return a.host.UpdatePRWithMedia(url, bodyFile, mediaDir, mediaFiles)
}

// CommentPR posts a comment on a pull request via the host.
func (a *githubAdapter) CommentPR(url, bodyFile string) error {
	return a.host.CommentPR(url, bodyFile)
}

// ReadPRBody reads a pull request body via the host.
func (a *githubAdapter) ReadPRBody(url string) (string, error) {
	return a.host.ReadPRBody(url)
}
