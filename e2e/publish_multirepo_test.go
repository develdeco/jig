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
	"github.com/develdeco/jig/internal/store"
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
	if strings.Contains(pub.Stdout, "pending") {
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
	for _, want := range []string{"publish_prs[2]{repo,pr,state,head,body_file}:", "  api,\"" + apiPR + "\",opened," + b.shaAPI + ",tickets/" + ticket + "/pr/api.md", "  web,\"" + webPR + "\",opened," + b.shaWeb + ",tickets/" + ticket + "/pr/web.md"} {
		if !strings.Contains(status.Stdout, want) {
			t.Errorf("jig status lacks %q\n%s", want, status.Stdout)
		}
	}
}

// TestPublishRecordsTheOnePRPerRepoTheHarnessOpened: the harness pushes the
// branch and opens the pull requests itself. A pull request is recorded with
// the repo it is in: without --repo jig asks which, the host's word on the head
// is held to that repo's reviewed head, the ticket is not published after the
// first, and the second finishes it.
func TestPublishRecordsTheOnePRPerRepoTheHarnessOpened(t *testing.T) {
	t.Parallel()
	b := buildOrchestratorTicket(t)
	env, log := githubOrchestrator(t, &b)
	ticket := b.fx.Ticket
	reviewedClean(t, b)
	const apiPR, webPR = "https://github.com/acme/api/pull/3", "https://github.com/acme/web/pull/4"
	hostSays := func(sha string) jigEnv {
		return env.with("GH_STUB_HEAD_REF="+b.branch, "GH_STUB_HEAD_OID="+sha)
	}

	if r := runJig(t, hostSays(b.shaAPI), b.fx.OrchestratorDir, "publish", ticket, "--pr", apiPR); r.Code == 0 || !strings.Contains(r.Stdout, "say which repo") {
		t.Errorf("jig publish --pr without --repo: exit %d, want it to ask which repo\n%s", r.Code, r.Stdout)
	}
	if r := runJig(t, hostSays(b.shaAPI), b.fx.OrchestratorDir, "publish", ticket, "--pr", apiPR, "--repo", "docs"); r.Code == 0 || !strings.Contains(r.Stdout, `the project has no repo "docs"`) {
		t.Errorf("jig publish --pr --repo docs: exit %d, want the repo refused\n%s", r.Code, r.Stdout)
	}
	// The web's head is not the api's reviewed head.
	if r := runJig(t, hostSays(b.shaWeb), b.fx.OrchestratorDir, "publish", ticket, "--pr", apiPR, "--repo", "api"); r.Code == 0 || !strings.Contains(r.Stdout, "PUBLISH_UNREVIEWED_HEAD") {
		t.Errorf("jig publish --pr of the api at the web's head: exit %d, want PUBLISH_UNREVIEWED_HEAD\n%s", r.Code, r.Stdout)
	}
	if prs, _ := prLines(t, b.fx, ticket); len(prs) != 0 {
		t.Fatalf("a refused --pr journaled %+v", prs)
	}

	first := runJig(t, hostSays(b.shaAPI), b.fx.OrchestratorDir, "publish", ticket, "--pr", apiPR, "--repo", "api")
	if first.Code != 0 || !strings.Contains(first.Stdout, "repo: api") || !strings.Contains(first.Stdout, "verified: yes") || !strings.Contains(first.Stdout, "web still needs its pull request") {
		t.Fatalf("jig publish --pr --repo api: exit %d, want it recorded and web still asked for\nstdout:\n%s\nstderr:\n%s", first.Code, first.Stdout, first.Stderr)
	}
	if prs, done := prLines(t, b.fx, ticket); len(prs) != 1 || prs[0].Repo != "api" || prs[0].URL != apiPR || done {
		t.Fatalf("after the first pr: lines %+v, publish-done %v, want the api's alone and no publish-done", prs, done)
	}

	// The web's body now names the api's pull request.
	if r := runJig(t, env, b.fx.OrchestratorDir, "publish", ticket, "--body", "--repo", "web"); r.Code != 0 {
		t.Fatalf("jig publish --body --repo web: exit %d\n%s", r.Code, r.Stdout)
	}
	if body := string(readFileOrFatal(t, filepath.Join(b.st.TicketDir(ticket), "pr", "web.md"))); !strings.Contains(body, "- also api: "+apiPR+"\n") {
		t.Errorf("pr/web.md does not name the api's pull request:\n%s", body)
	}

	second := runJig(t, hostSays(b.shaWeb), b.fx.OrchestratorDir, "publish", ticket, "--pr", webPR, "--repo", "web")
	if second.Code != 0 || strings.Contains(second.Stdout, "still needs") {
		t.Fatalf("jig publish --pr --repo web: exit %d, want it recorded and nothing left\nstdout:\n%s\nstderr:\n%s", second.Code, second.Stdout, second.Stderr)
	}
	prs, done := prLines(t, b.fx, ticket)
	if len(prs) != 2 || prs[1].Repo != "web" || prs[1].URL != webPR || !done {
		t.Errorf("after the second pr: lines %+v, publish-done %v, want both and publish-done after the web's", prs, done)
	}
	// Recording a pull request is not publishing it: jig opened none.
	for _, c := range ghCalls(t, log) {
		if strings.HasPrefix(c, "pr create") || strings.HasPrefix(c, "pr edit") {
			t.Errorf("--pr called %q", c)
		}
	}
}

// TestPublishShipsTheOneRepoATicketBuildsIn: a ticket of the orchestrator whose
// slices are all in web, a repo that is not the first. Publish ships web from the
// web's reviewed head, with no word of the api: no --repo is needed, no pull
// request is opened there, and the body has no section about another repo.
func TestPublishShipsTheOneRepoATicketBuildsIn(t *testing.T) {
	t.Parallel()
	fx, env := newFixture(t, fixture.Opts{Orchestrator: true})
	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatal(err)
	}
	const ticket = "JIG-2"
	slices, err := st.ReadSlices(fx.Ticket)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.CreateTicketRecord(ticket, store.Ticket{Title: "Web only"}); err != nil {
		t.Fatal(err)
	}
	writeFileOrFatal(t, filepath.Join(st.TicketDir(ticket), "brief.md"), readFileOrFatal(t, filepath.Join(st.TicketDir(fx.Ticket), "brief.md")))
	if err := st.AppendSlices(ticket, []store.Slice{{ID: "page", Workspace: "web/ui", Goal: "Show the page.", Oracle: "test", BlockedBy: []string{}, FromBrief: slices[1].FromBrief}}); err != nil {
		t.Fatal(err)
	}
	branch := nextJSON[nextRepos](t, env, fx.OrchestratorDir, ticket).Branch
	web := newHarness(t, fx, env, "s-web")
	web.Clone = fx.NestedDirs["web"]
	web.Branch(branch)
	verifySlots <- struct{}{}
	sha := web.Commit("page", "page: show it", map[string]string{"ui/page.go": webPageSource})
	r := web.Jig("verify", "--wait")
	<-verifySlots
	if r.Code != 0 || !strings.Contains(r.Stdout, "state: pass") {
		t.Fatalf("jig verify --wait: exit %d, want a pass\nstdout:\n%s\nstderr:\n%s", r.Code, r.Stdout, r.Stderr)
	}
	web.WaitVerifiers()

	b := builtOrchestrator{fx: fx, env: env, web: web, api: newHarness(t, fx, env, "s-api"), branch: branch, shaWeb: sha, st: st}
	b.api.Clone = fx.NestedDirs["api"]
	env, log := githubOrchestrator(t, &b)
	if gate := web.Jig("gate", ticket); gate.Code != 0 {
		t.Fatalf("jig gate: exit %d\nstdout:\n%s\nstderr:\n%s", gate.Code, gate.Stdout, gate.Stderr)
	}
	rev := newHarness(t, fx, env, "s-reviewer")
	rev.Clone = fx.NestedDirs["web"]
	if ctx := rev.Jig("review", "context", ticket); ctx.Code != 0 {
		t.Fatalf("jig review context: exit %d\nstdout:\n%s\nstderr:\n%s", ctx.Code, ctx.Stdout, ctx.Stderr)
	}
	mustReview := []string{"web/ui/page.go"}
	if done := rev.Jig("review", "done", ticket, "--result", cleanResultOf(t, mustReview)); done.Code != 0 || !strings.Contains(done.Stdout, "verdict: clean") {
		t.Fatalf("jig review done: exit %d, want a clean round\nstdout:\n%s\nstderr:\n%s", done.Code, done.Stdout, done.Stderr)
	}

	// The api is not a repo of this ticket: naming it is refused.
	if r := runJig(t, env, fx.OrchestratorDir, "publish", ticket, "--yes", "--repo", "api"); r.Code == 0 || !strings.Contains(r.Stdout, ticket+" builds in web, not in api") {
		t.Errorf("jig publish --repo api: exit %d, want it refused for building in web\n%s", r.Code, r.Stdout)
	}
	pub := runJig(t, env, fx.OrchestratorDir, "publish", ticket, "--yes")
	const webPR = "https://github.example/acme/web/pull/1"
	if pub.Code != 0 || !strings.Contains(pub.Stdout, "pr_url[1]{repo,url,action}:\n  web,\""+webPR+"\",opened") {
		t.Fatalf("jig publish --yes: exit %d, want the web's pull request alone\nstdout:\n%s\nstderr:\n%s", pub.Code, pub.Stdout, pub.Stderr)
	}
	if got := originRefOrEmpty(t, fx.NestedRemotes["web"], "refs/heads/"+branch); got != sha {
		t.Errorf("origin of web: %s = %s, want the reviewed head %s", branch, got, sha)
	}
	if got := originRefOrEmpty(t, fx.NestedRemotes["api"], "refs/heads/"+branch); got != "" {
		t.Errorf("origin of api has %s at %s: publish shipped a repo the ticket does not build in", branch, got)
	}
	for _, c := range ghCalls(t, log) {
		if strings.Contains(c, "acme/api") {
			t.Errorf("gh was called for the api: %q", c)
		}
	}
	if body := string(readFileOrFatal(t, filepath.Join(st.TicketDir(ticket), "pr", "web.md"))); strings.Contains(body, "Part of") {
		t.Errorf("pr/web.md of a ticket in one repo has a Part of section:\n%s", body)
	}
	prs, done := prLines(t, fx, ticket)
	if len(prs) != 1 || prs[0].Repo != "web" || prs[0].URL != webPR || prs[0].Commit != sha || !done {
		t.Errorf("pr lines = %+v, publish-done = %v, want the web's alone at %s, then publish-done", prs, done, sha)
	}
	if _, err := os.Stat(filepath.Join(env.home, "pool", "api", ticket+"-publish")); err == nil {
		t.Errorf("publish made a lease for the api, which the ticket does not build in")
	}
}
