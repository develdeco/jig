package main

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/mirror/github"
)

// readsClient is the stub that counts what a read asked it and can fail.
type readsClient struct {
	*stubGitHub
	asked []string
	err   error
}

func (c *readsClient) ItemByNumber(ctx context.Context, owner, repo string, number int) (github.ItemDetail, error) {
	c.asked = append(c.asked, owner+"/"+repo)
	if c.err != nil {
		return github.ItemDetail{}, c.err
	}
	return c.stubGitHub.ItemByNumber(ctx, owner, repo, number)
}

const unboundBody = "First line.\n\n    indented, with: a colon, and \"quotes\"\n- [ ] #12 and #13\n"

// unboundClone is a store with a GitHub tracker and no bound item, with the
// client that knows the items the tests read.
func unboundClone(t *testing.T) (env, string, *readsClient) {
	t.Helper()
	e := testEnv(t.TempDir())
	clone := filepath.Join(t.TempDir(), "clone")
	newTestOriginCloneWithGitHubTracker(t, clone)
	c := &readsClient{stubGitHub: &stubGitHub{items: map[int]github.ItemDetail{
		49: {Number: 49, NodeID: "NODE_49", Title: "Dark mode", Body: unboundBody, URL: "https://github.com/example/tracking/issues/49", Open: true, Labels: []string{"ui", "bug"}, Author: "sam"},
		50: {Number: 50, NodeID: "NODE_50", Title: "Old idea", URL: "https://github.com/example/tracking/issues/50", StateReason: "NOT_PLANNED", Author: "kim", LinkedPullRequests: 2},
		7:  {Number: 7, NodeID: "NODE_7", Title: "Elsewhere", Body: "x", URL: "https://github.com/other/repo/issues/7", Open: true, Author: "lee"},
	}}}
	e.mirrorClient = c
	return e, clone, c
}

// storeState is what must not change when an item is only read.
func storeState(t *testing.T, clone string) string {
	t.Helper()
	head, err := gitx.Run(clone, "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	status, err := gitx.Run(clone, "status", "--porcelain")
	if err != nil {
		t.Fatal(err)
	}
	return head + "\n" + status
}

// noHashHelp fails for a help line that starts a word with #, which sh and
// PowerShell take for a comment.
func noHashHelp(t *testing.T, out string) {
	t.Helper()
	_, help, _ := strings.Cut(out, "help[")
	for _, w := range strings.Fields(help) {
		if strings.HasPrefix(w, "#") {
			t.Errorf("a help line has the word %q, which a shell reads as a comment:\n%s", w, help)
		}
	}
}

// TestNextItemUnbound: an item with no bound ticket is read once and printed
// with every field, the body verbatim as a block of its own, and the three
// help lines; nothing is written, and one item is read.
func TestNextItemUnbound(t *testing.T) {
	t.Parallel()
	e, clone, c := unboundClone(t)
	before := storeState(t, clone)

	out, code := runMain(t, e, "", "next", "49", "--store", clone)
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, out)
	}
	want := "next:\n  ref: 49\n  repo: example/tracking\n  bound: no\n  url: \"https://github.com/example/tracking/issues/49\"\n  title: Dark mode\n  state: open\n  labels: ui+bug\n  linked_prs: 0\n  author: sam\n" +
		"body:\n  First line.\n  \n      indented, with: a colon, and \"quotes\"\n  - [ ] #12 and #13\n"
	if !strings.Contains(out, want) {
		t.Errorf("output lacks the next and body blocks:\n%s\nwant it to contain:\n%s", out, want)
	}
	if strings.Contains(out, "advisories") {
		t.Errorf("an open item with no linked pull request has advisories:\n%s", out)
	}
	for _, line := range []string{
		"Run intake with tracker item 49 as its need: mint nothing before the verdict (jig skill intake)",
		"The verdict makes its one issue and chart: jig chart new <name> --item 49",
		"Once bound, jig next 49 answers as the issue does, with no network",
	} {
		if !strings.Contains(out, "\n  "+line+"\n") && !strings.HasSuffix(strings.TrimRight(out, "\n"), "  "+line) {
			t.Errorf("help lacks %q:\n%s", line, out)
		}
	}
	if !strings.Contains(out, "help[3]:") {
		t.Errorf("want exactly three help lines:\n%s", out)
	}
	noHashHelp(t, out)
	if len(c.asked) != 1 || c.asked[0] != "example/tracking" {
		t.Errorf("the host was asked %v, want one read of example/tracking", c.asked)
	}
	if after := storeState(t, clone); after != before {
		t.Errorf("the store changed:\n%s\n--\n%s", before, after)
	}

	out, code = runMain(t, e, "", "next", "#49", "--store", clone, "--json")
	var got struct {
		Ref, Repo, URL, Title, State, Author, Body string
		Bound                                      struct{ Kind string }
		Labels                                     []string
		LinkedPRs                                  int `json:"linked_prs"`
		Advisories, Help                           []string
	}
	if err := json.Unmarshal([]byte(out), &got); code != 0 || err != nil {
		t.Fatalf("json: exit %d, %v\n%s", code, err, out)
	}
	if got.Ref != "49" || got.Repo != "example/tracking" || got.Bound.Kind != "no" || got.Title != "Dark mode" || got.State != "open" || got.Author != "sam" || got.Body != unboundBody || len(got.Labels) != 2 || got.LinkedPRs != 0 || len(got.Advisories) != 0 || len(got.Help) != 3 {
		t.Errorf("json = %+v", got)
	}
	if !strings.Contains(got.Help[1], "jig chart new <name> --item 49") {
		t.Errorf("json help = %v", got.Help)
	}
}

// TestNextItemAdvisories: a closed item, one with linked pull requests and an
// item of another repo each say so, and are never refused.
func TestNextItemAdvisories(t *testing.T) {
	t.Parallel()
	e, clone, c := unboundClone(t)

	out, code := runMain(t, e, "", "next", "50", "--store", clone)
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, out)
	}
	for _, want := range []string{"  state: closed (not planned)\n", "  linked_prs: 2\n", "body: none\n", "advisories[2]:\n  50 is closed: the first sync after it is bound reopens it\n  50 has 2 linked pull request(s): check that the work is not already done\n", "help[3]:"} {
		if !strings.Contains(out, want) {
			t.Errorf("lacks %q:\n%s", want, out)
		}
	}

	out, code = runMain(t, e, "", "next", "other/repo#7", "--store", clone)
	if code != 0 {
		t.Fatalf("other repo: exit %d\n%s", code, out)
	}
	for _, want := range []string{"  ref: 7\n  repo: other/repo\n  bound: no\n", "advisories[1]:\n  7 is in other/repo, the project's tracker is example/tracking: it can be read as input but not bound\n", "help[1]:\n  Run intake with tracker item 7 of other/repo as its need; it cannot be bound to this project"} {
		if !strings.Contains(out, want) {
			t.Errorf("other repo lacks %q:\n%s", want, out)
		}
	}
	noHashHelp(t, out)
	if c.asked[len(c.asked)-1] != "other/repo" {
		t.Errorf("asked %v, want the last read to be of other/repo", c.asked)
	}
}

// TestNextItemRefused: an item GitHub does not have, a project with no GitHub
// tracker and each flag that asks about a ticket or a chart are refused, the
// flags before anything is read.
func TestNextItemRefused(t *testing.T) {
	t.Parallel()
	e, clone, c := unboundClone(t)

	out, code := runMain(t, e, "", "next", "99", "--store", clone)
	if code == 0 || !strings.Contains(out, "code: ITEM_NOT_FOUND") {
		t.Errorf("a missing item: exit %d, want ITEM_NOT_FOUND:\n%s", code, out)
	}
	asked := len(c.asked)
	for _, flags := range [][]string{{"--for", "build"}, {"--topology"}, {"--tickets", "A-1"}} {
		out, code := runMain(t, e, "", append([]string{"next", "49", "--store", clone}, flags...)...)
		if code != 2 || !strings.Contains(out, "49 is an unbound tracker item, not a ticket or a chart") || !strings.Contains(out, "code: VALIDATION_ERROR") {
			t.Errorf("%v: exit %d, want the refusal:\n%s", flags, code, out)
		}
	}
	if len(c.asked) != asked {
		t.Errorf("a refused flag read the item: asked %v", c.asked)
	}

	bare := filepath.Join(t.TempDir(), "bare")
	newTestOriginClone(t, bare)
	out, code = runMain(t, e, "", "next", "49", "--store", bare)
	if code == 0 || !strings.Contains(out, "trackers:") {
		t.Errorf("no tracker: exit %d, want a refusal naming trackers::\n%s", code, out)
	}
}

// TestNextItemUnreadable: a read that cannot be made is exit 1 with the cause,
// help for the token and no bind help.
func TestNextItemUnreadable(t *testing.T) {
	t.Parallel()
	for name, cause := range map[string]error{
		"no token": &axi.Error{Msg: "gh CLI is not installed", Code: "GH_NOT_INSTALLED"},
		"network":  errors.New("github: read example/tracking#49: dial tcp: i/o timeout"),
	} {
		e, clone, c := unboundClone(t)
		c.err = cause
		before := storeState(t, clone)
		out, code := runMain(t, e, "", "next", "49", "--store", clone)
		if code != 1 || !strings.Contains(out, "error: item 49 of example/tracking could not be read: ") || !strings.Contains(out, cause.Error()) {
			t.Errorf("%s: exit %d, want exit 1 naming the cause:\n%s", name, code, out)
		}
		for _, want := range []string{"gh auth login", "GH_TOKEN", "need no network"} {
			if !strings.Contains(out, want) {
				t.Errorf("%s: help lacks %q:\n%s", name, want, out)
			}
		}
		for _, bad := range []string{"jig chart new", "intake", "body:", "next:"} {
			if strings.Contains(out, bad) {
				t.Errorf("%s: a partial answer or bind help (%q):\n%s", name, bad, out)
			}
		}
		if storeState(t, clone) != before {
			t.Errorf("%s: the store changed", name)
		}
	}
}
