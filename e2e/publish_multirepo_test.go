package e2e

// End-to-end tests of publish over a ticket that spans the repos of an
// orchestrator: one pull request for each repo, from the one branch name, each
// naming the others. The fake harness plays the builders and the reviewer, a
// fake gh is the pull-request host, and everything else is the real binary.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/project"
)

// githubOrchestrator makes both nested repos of the orchestrator fixture GitHub
// repos as far as jig can tell, acme/api and acme/web: the project's marker
// names each repo's remote by a GitHub URL, which the git config of the test
// rewrites to the bare remote on disk, and a fake gh is first on PATH. It
// returns the environment to run jig in and the path of the fake gh's log; the
// harnesses of b run in it too.
func githubOrchestrator(t *testing.T, b *builtOrchestrator) (jigEnv, string) {
	t.Helper()
	env := b.env.withGitGlobal(t)
	marker, err := project.LoadMarker(filepath.Join(b.fx.OrchestratorDir, project.MarkerFile))
	if err != nil {
		t.Fatal(err)
	}
	for i, r := range marker.Repos {
		url := "https://github.com/acme/" + r.Name + ".git"
		remote := filepath.ToSlash(b.fx.NestedRemotes[r.Name])
		if _, err := gitx.RunReplaceEnv(b.fx.Dir, env.environ(), "config", "--global", "url."+remote+".insteadOf", url); err != nil {
			t.Fatalf("git config --global url.insteadOf for %s: %v", r.Name, err)
		}
		marker.Repos[i].Remote = url
	}
	if err := project.SetMarkerRepos(b.fx.OrchestratorDir, marker.Repos); err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(t.TempDir(), "gh.log")
	env = env.with(
		"PATH="+fixture.GhStub(t)+string(os.PathListSeparator)+os.Getenv("PATH"),
		"GH_STUB_LOG="+log,
		"GH_STUB_STATE="+filepath.Join(t.TempDir(), "gh.state"),
	)
	b.env, b.api.env, b.web.env = env, env, env
	return env, log
}

// reviewedClean has the api's session open the ticket's gate round and a
// reviewer that built none of it close the round clean, over both repos.
func reviewedClean(t *testing.T, b builtOrchestrator) {
	t.Helper()
	ticket := b.fx.Ticket
	if r := b.api.Jig("gate", ticket); r.Code != 0 {
		t.Fatalf("jig gate: exit %d\nstdout:\n%s\nstderr:\n%s", r.Code, r.Stdout, r.Stderr)
	}
	rev := newHarness(t, b.fx, b.env, "s-reviewer")
	rev.Clone = b.fx.NestedDirs["web"]
	if ctx := rev.Jig("review", "context", ticket); ctx.Code != 0 {
		t.Fatalf("jig review context: exit %d\nstdout:\n%s\nstderr:\n%s", ctx.Code, ctx.Stdout, ctx.Stderr)
	}
	done := rev.Jig("review", "done", ticket, "--result", cleanResultOf(t, b.request(t, "1").MustReview))
	if done.Code != 0 || !strings.Contains(done.Stdout, "verdict: clean") || !strings.Contains(done.Stdout, "independent: true") {
		t.Fatalf("jig review done: exit %d, want a clean round\nstdout:\n%s\nstderr:\n%s", done.Code, done.Stdout, done.Stderr)
	}
}

// prLines are the pr lines of the ticket's journal, and whether publish-done
// follows the last of them.
func prLines(t *testing.T, fx *fixture.Fixture, ticket string) (prs []journal.Line, doneAfter bool) {
	t.Helper()
	for _, l := range readJournal(t, fx, ticket) {
		switch l.Event {
		case journal.EventPr:
			prs, doneAfter = append(prs, l), false
		case journal.EventPublishDone:
			doneAfter = true
		}
	}
	return prs, doneAfter
}

// TestPublishOpensOnePRPerRepo: a ticket built in both repos of the orchestrator
// and reviewed clean. A body for one repo names the other as pending and writes
// nothing for it. A bare publish pushes the one branch to each repo's origin,
// opens a pull request in each, and updates the first with the url of the
// second, so each body names the other; publish-done follows the second pull
// request, and the status lists both.
func TestPublishOpensOnePRPerRepo(t *testing.T) {
	t.Parallel()
	b := buildOrchestratorTicket(t)
	env, log := githubOrchestrator(t, &b)
	ticket := b.fx.Ticket
	reviewedClean(t, b)
	prDir := filepath.Join(b.st.TicketDir(ticket), "pr")

	// A body for web alone: api is named, pending, and has no body of its own.
	body := runJig(t, env, b.fx.OrchestratorDir, "publish", ticket, "--body", "--repo", "web")
	if body.Code != 0 || !strings.Contains(body.Stdout, "repo: web") || !strings.Contains(body.Stdout, "head: "+b.shaWeb) || !strings.Contains(body.Stdout, "branch: "+b.branch) {
		t.Fatalf("jig publish --body --repo web: exit %d, want the web's body at its reviewed head\nstdout:\n%s\nstderr:\n%s", body.Code, body.Stdout, body.Stderr)
	}
	if !strings.Contains(body.Stdout, "--pr <url> --repo web") {
		t.Errorf("the help of a body for one repo of two does not say to record its pull request with --repo web:\n%s", body.Stdout)
	}
	webBody := string(readFileOrFatal(t, filepath.Join(prDir, "web.md")))
	for _, want := range []string{"## Part of " + ticket, "- also api: pending"} {
		if !strings.Contains(webBody, want) {
			t.Errorf("pr/web.md lacks %q:\n%s", want, webBody)
		}
	}
	if _, err := os.Stat(filepath.Join(prDir, "api.md")); err == nil {
		t.Errorf("--body --repo web wrote a body for api")
	}
	if got := len(ghCalls(t, log)); got != 0 {
		t.Errorf("--body called gh %d time(s): %v", got, ghCalls(t, log))
	}

	// Both bodies: a table with a row for each repo.
	both := runJig(t, env, b.fx.OrchestratorDir, "publish", ticket, "--body")
	if both.Code != 0 || !strings.Contains(both.Stdout, "bodies[2]{repo,base,head,title,pr_body,review_notes}:") {
		t.Fatalf("jig publish --body: exit %d, want a body for each repo\nstdout:\n%s\nstderr:\n%s", both.Code, both.Stdout, both.Stderr)
	}
	for _, name := range []string{"api.md", "web.md", "review-notes-api.md", "review-notes-web.md"} {
		if _, err := os.Stat(filepath.Join(prDir, name)); err != nil {
			t.Errorf("--body left no pr/%s: %v", name, err)
		}
	}

	// A bare publish that is declined pushes nothing to either repo.
	if r := runJig(t, env, b.fx.OrchestratorDir, "publish", ticket); r.Code == 0 || !strings.Contains(r.Stdout, "PUBLISH_DECLINED") {
		t.Errorf("jig publish without --yes: exit %d, want PUBLISH_DECLINED\n%s", r.Code, r.Stdout)
	}
	for _, name := range fixture.NestedRepoNames {
		if got := originRefOrEmpty(t, b.fx.NestedRemotes[name], "refs/heads/"+b.branch); got != "" {
			t.Fatalf("a declined publish pushed %s to the origin of %s", got, name)
		}
	}

	pub := runJig(t, env, b.fx.OrchestratorDir, "publish", ticket, "--yes")
	if pub.Code != 0 {
		t.Fatalf("jig publish --yes: exit %d, want 0\nstdout:\n%s\nstderr:\n%s", pub.Code, pub.Stdout, pub.Stderr)
	}
	const apiPR, webPR = "https://github.example/acme/api/pull/1", "https://github.example/acme/web/pull/1"
	for _, want := range []string{"pr_url[2]{repo,url,action}:", "  api,\"" + apiPR + "\",opened", "  web,\"" + webPR + "\",opened"} {
		if !strings.Contains(pub.Stdout, want) {
			t.Errorf("jig publish output lacks %q\n%s", want, pub.Stdout)
		}
	}
	if strings.Contains(pub.Stdout, "\n  pending:") {
		t.Errorf("jig publish says a repo is pending after opening both:\n%s", pub.Stdout)
	}

	// The one branch is on each origin at the head the round reviewed there.
	for name, sha := range map[string]string{"api": b.shaAPI, "web": b.shaWeb} {
		if got := originRefOrEmpty(t, b.fx.NestedRemotes[name], "refs/heads/"+b.branch); got != sha {
			t.Errorf("origin of %s: %s = %s, want the reviewed head %s", name, b.branch, got, sha)
		}
	}

	// A pull request in each repo, from that branch, then the first body updated
	// once the second pull request had a url.
	var creates, edits []string
	for _, c := range ghCalls(t, log) {
		switch {
		case strings.HasPrefix(c, "pr create"):
			creates = append(creates, c)
		case strings.HasPrefix(c, "pr edit"):
			edits = append(edits, c)
		}
	}
	if len(creates) != 2 || !strings.Contains(creates[0], "--repo acme/api") || !strings.Contains(creates[1], "--repo acme/web") {
		t.Fatalf("gh pr create calls = %q, want one in acme/api then one in acme/web", creates)
	}
	for _, c := range creates {
		if !strings.Contains(c, "--head "+b.branch) || !strings.Contains(c, "--base main") {
			t.Errorf("gh pr create call = %q, want --head %s --base main", c, b.branch)
		}
	}
	if len(edits) != 1 || !strings.HasPrefix(edits[0], "pr edit "+apiPR) {
		t.Errorf("gh pr edit calls = %q, want the api's pull request updated once, with the web's url", edits)
	}

	// Each body names the other repo's pull request.
	apiBody := string(readFileOrFatal(t, filepath.Join(prDir, "api.md")))
	webBody = string(readFileOrFatal(t, filepath.Join(prDir, "web.md")))
	if !strings.Contains(apiBody, "## Part of "+ticket+"\n\n- also web: "+webPR+"\n") {
		t.Errorf("pr/api.md does not name the web's pull request:\n%s", apiBody)
	}
	if !strings.Contains(webBody, "## Part of "+ticket+"\n\n- also api: "+apiPR+"\n") {
		t.Errorf("pr/web.md does not name the api's pull request:\n%s", webBody)
	}
	if strings.Contains(apiBody, "pending") || strings.Contains(webBody, "pending") {
		t.Errorf("a body says pending after both pull requests were opened:\napi:\n%s\nweb:\n%s", apiBody, webBody)
	}

	// The journal: a pr line for each repo, naming it, and publish-done after the
	// second.
	prs, done := prLines(t, b.fx, ticket)
	if len(prs) != 2 || prs[0].Repo != "api" || prs[0].URL != apiPR || prs[0].Commit != b.shaAPI ||
		prs[1].Repo != "web" || prs[1].URL != webPR || prs[1].Commit != b.shaWeb || prs[0].Branch != b.branch || prs[1].Branch != b.branch {
		t.Errorf("pr lines = %+v, want the api's then the web's, each at its reviewed head on %s", prs, b.branch)
	}
	if !done {
		t.Errorf("no publish-done after the second pull request")
	}

	// jig status lists each repo's pull request.
	status := runJig(t, env, b.fx.OrchestratorDir, "status", ticket)
	for _, want := range []string{"publish_prs[2]{repo,pr,state,approval,head,body_file}:", "  api,\"" + apiPR + "\",opened,none," + b.shaAPI + ",tickets/" + ticket + "/pr/api.md", "  web,\"" + webPR + "\",opened,none," + b.shaWeb + ",tickets/" + ticket + "/pr/web.md"} {
		if !strings.Contains(status.Stdout, want) {
			t.Errorf("jig status lacks %q\n%s", want, status.Stdout)
		}
	}
}

// TestPublishNotesListTheOtherReposFindings: a ticket built in both repos whose
// first round finds one problem in each repo, both fixed, and a clean second
// round. Each repo's review notes tell its own finding in full and the other
// repo's in one line, under "## Findings in other repos", before the coverage.
func TestPublishNotesListTheOtherReposFindings(t *testing.T) {
	t.Parallel()
	b := buildOrchestratorTicket(t)
	env, _ := githubOrchestrator(t, &b)
	ticket := b.fx.Ticket
	const webTitle, apiTitle = "The page shows cents, not dollars", "The handler drops the currency"

	if r := b.api.Jig("gate", ticket); r.Code != 0 {
		t.Fatalf("jig gate: exit %d\nstdout:\n%s\nstderr:\n%s", r.Code, r.Stdout, r.Stderr)
	}
	rev := newHarness(t, b.fx, b.env, "s-reviewer")
	rev.Clone = b.fx.NestedDirs["web"]
	if ctx := rev.Jig("review", "context", ticket, "--model", "test-model"); ctx.Code != 0 {
		t.Fatalf("jig review context: exit %d\nstdout:\n%s\nstderr:\n%s", ctx.Code, ctx.Stdout, ctx.Stderr)
	}
	for _, f := range [][2]string{{"web/ui/page.go", webTitle}, {"api/server/handler.go", apiTitle}} {
		r := rev.Jig("finding", ticket, "--file", f[0], "--line", "4", "--title", f[1], "--detail", "It is wrong.",
			"--action", "fix", "--risk", "high", "--risk-rationale", "The shop shows a wrong total.", "--oracle", "test")
		if r.Code != 0 {
			t.Fatalf("jig finding in %s: exit %d\nstdout:\n%s\nstderr:\n%s", f[0], r.Code, r.Stdout, r.Stderr)
		}
	}
	done := rev.Jig("review", "done", ticket, "--reviewed", strings.Join(b.request(t, "1").MustReview, ","), "--summary", "one problem in each repo")
	if done.Code != 0 || !strings.Contains(done.Stdout, "verdict: fix-slices") {
		t.Fatalf("jig review done: exit %d, want fix slices\nstdout:\n%s\nstderr:\n%s", done.Code, done.Stdout, done.Stderr)
	}

	// Each fix slice builds in the repo of its finding.
	slicesNow, err := b.st.ReadSlices(ticket)
	if err != nil {
		t.Fatal(err)
	}
	fixes := map[string]string{}
	for _, s := range slicesNow {
		if s.FromGate == 1 {
			fixes[strings.SplitN(s.Workspace, "/", 2)[0]] = s.ID
		}
	}
	if fixes["web"] == "" || fixes["api"] == "" || len(fixes) != 2 {
		t.Fatalf("the fix slices by repo = %v, want one in api and one in web", fixes)
	}
	verifySlots <- struct{}{}
	defer func() { <-verifySlots }()
	verifyPasses(t, b.web, fixes["web"], b.web.Commit(fixes["web"], "fix: page", map[string]string{"ui/page.go": webPageFixed}))
	verifyPasses(t, b.api, fixes["api"], b.api.Commit(fixes["api"], "fix: handler", map[string]string{"server/handler.go": apiHandlerFixed}))

	if r := b.api.Jig("gate", ticket); r.Code != 0 || !strings.Contains(r.Stdout, "round: 2") {
		t.Fatalf("jig gate after the fixes: exit %d, want round 2\nstdout:\n%s\nstderr:\n%s", r.Code, r.Stdout, r.Stderr)
	}
	if ctx := rev.Jig("review", "context", ticket); ctx.Code != 0 {
		t.Fatalf("jig review context of round 2: exit %d\nstdout:\n%s\nstderr:\n%s", ctx.Code, ctx.Stdout, ctx.Stderr)
	}
	done2 := rev.Jig("review", "done", ticket, "--result", cleanResultOf(t, b.request(t, "2").MustReview))
	if done2.Code != 0 || !strings.Contains(done2.Stdout, "verdict: clean") {
		t.Fatalf("jig review done of round 2: exit %d, want a clean round\nstdout:\n%s\nstderr:\n%s", done2.Code, done2.Stdout, done2.Stderr)
	}

	if body := runJig(t, env, b.fx.OrchestratorDir, "publish", ticket, "--body"); body.Code != 0 {
		t.Fatalf("jig publish --body: exit %d\nstdout:\n%s\nstderr:\n%s", body.Code, body.Stdout, body.Stderr)
	}
	prDir := filepath.Join(b.st.TicketDir(ticket), "pr")
	for _, c := range []struct{ repo, own, other, otherLine string }{
		{"web", webTitle, apiTitle, "- api: server/handler.go: " + apiTitle},
		{"api", apiTitle, webTitle, "- web: ui/page.go: " + webTitle},
	} {
		notes := strings.ReplaceAll(string(readFileOrFatal(t, filepath.Join(prDir, "review-notes-"+c.repo+".md"))), "\r\n", "\n")
		round, findings, others, coverage := strings.Index(notes, "## Round 2"), strings.Index(notes, "## Findings\n"), strings.Index(notes, "## Findings in other repos\n"), strings.Index(notes, "## Coverage\n")
		if !(round >= 0 && round < findings && findings < others && others < coverage) {
			t.Fatalf("review-notes-%s.md sections out of order (Round %d, Findings %d, Findings in other repos %d, Coverage %d):\n%s", c.repo, round, findings, others, coverage, notes)
		}
		own, rest := notes[findings:others], notes[others:coverage]
		if !strings.Contains(own, c.own) || !strings.Contains(own, "Status: fixed") || strings.Contains(own, c.other) {
			t.Errorf("review-notes-%s.md Findings block lacks its own finding in full, or holds the other repo's:\n%s", c.repo, own)
		}
		if want := "## Findings in other repos\n\n" + c.otherLine + "\n\n"; rest != want {
			t.Errorf("review-notes-%s.md other repos block =\n%q\nwant\n%q", c.repo, rest, want)
		}
	}
}
