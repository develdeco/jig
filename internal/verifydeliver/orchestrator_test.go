package verifydeliver

// Tests of a gate and a publish over a project of several repos: the
// orchestrator fixture's two nested repos, api and web. The variations of
// which repos a ticket covers, adopts and ships live here; the e2e journeys
// run one flow of each verb over both repos through the binary.

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/project"
	"github.com/develdeco/jig/internal/store"
)

const (
	orchAPISource = "package server\n\n// Handler names what the api serves.\nfunc Handler() string { return \"total\" }\n"
	orchWebSource = "package ui\n\n// Page names what the web page shows.\nfunc Page() string { return \"order total\" }\n"
)

// orchestrated is a ticket over the orchestrator fixture, in the shape the
// gate and publish read: a project of two repos, a Deps that knows both
// clones, and a ticket whose slices sit in the repos it names.
type orchestrated struct {
	fx     *fixture.Fixture
	d      Deps
	ticket string
	branch string
	// slice is the id of the slice each repo of the ticket builds.
	slice map[string]string
}

// orchestratorTicket generates the orchestrator fixture and a second ticket
// with one slice in each of repos (api and web are the repos there are), and
// builds nothing: the ticket's branch exists in no clone, no slice has run.
func orchestratorTicket(t *testing.T, repos ...string) orchestrated {
	t.Helper()
	fx := fixture.Generate(t, fixture.Opts{Orchestrator: true, Home: t.TempDir()})
	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	cfg, err := project.LoadConfig(fx.Home, fx.StoreDir, "", fx.OrchestratorDir)
	if err != nil {
		t.Fatalf("project.LoadConfig: %v", err)
	}
	machine, err := project.LoadMachine(fx.Home)
	if err != nil {
		t.Fatalf("project.LoadMachine: %v", err)
	}
	d := Deps{Store: st, Cfg: cfg, Machine: machine[fixture.OrchestratorName], Home: fx.Home, Oracle: passingOracle}

	const ticket = "JIG-2"
	built, err := d.Store.ReadSlices(fx.Ticket)
	if err != nil {
		t.Fatalf("ReadSlices: %v", err)
	}
	if err := d.Store.CreateTicketRecord(ticket, store.Ticket{Title: "Built in some repos"}); err != nil {
		t.Fatalf("CreateTicketRecord: %v", err)
	}
	brief, err := os.ReadFile(filepath.Join(d.Store.TicketDir(fx.Ticket), "brief.md"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d.Store.TicketDir(ticket), "brief.md"), brief, 0o644); err != nil {
		t.Fatal(err)
	}
	o := orchestrated{fx: fx, d: d, ticket: ticket, slice: map[string]string{}}
	var slices []store.Slice
	for _, repo := range repos {
		s := store.Slice{BlockedBy: []string{}}
		switch repo {
		case "api":
			s.ID, s.Workspace, s.Goal, s.FromBrief = "handler", "api/server", "Add up.", built[0].FromBrief
		case "web":
			s.ID, s.Workspace, s.Goal, s.FromBrief = "page", "web/ui", "Show it.", built[1].FromBrief
		default:
			t.Fatalf("orchestratorTicket: no repo %q", repo)
		}
		s.Oracle = "test"
		o.slice[repo] = s.ID
		slices = append(slices, s)
	}
	if err := d.Store.AppendSlices(ticket, slices); err != nil {
		t.Fatalf("AppendSlices: %v", err)
	}
	if err := d.Store.Push(ticket + ": minted"); err != nil {
		t.Fatalf("push the minted ticket: %v", err)
	}
	if o.branch, err = d.Store.TicketBranch(ticket, "main"); err != nil {
		t.Fatalf("TicketBranch: %v", err)
	}
	return o
}

// build commits repo's slice on the ticket's branch in repo's clone and marks
// the slice green, and returns the commit. It is what a build leaves, with no
// verification run.
func (o orchestrated) build(t *testing.T, repo string) string {
	t.Helper()
	dir := o.d.Machine.Clones[repo]
	file, source := "server/handler.go", orchAPISource
	if repo == "web" {
		file, source = "ui/page.go", orchWebSource
	}
	run(t, dir, "checkout", "-b", o.branch)
	if err := os.MkdirAll(filepath.Dir(filepath.Join(dir, file)), 0o755); err != nil {
		t.Fatal(err)
	}
	sha := writeAndCommit(t, dir, file, source, repo+": "+file)
	if err := o.d.Store.WriteSliceState(o.ticket, o.slice[repo], store.SliceState{State: "green", Attempts: 1}); err != nil {
		t.Fatalf("write slice state %s: %v", o.slice[repo], err)
	}
	if err := o.d.Store.Push(o.ticket + ": " + repo + " green"); err != nil {
		t.Fatalf("push the slice state: %v", err)
	}
	return sha
}

// TestGateOverOneRepoOfSeveral: a ticket of the orchestrator whose slices are
// all in one repo, and that repo is not the first. The gate covers that repo
// and no other: one repo at its head with the file to review, no lease for the
// other, and a clean round that reports the one repo's heads alone.
func TestGateOverOneRepoOfSeveral(t *testing.T) {
	t.Parallel()
	o := orchestratorTicket(t, "web")
	head := o.build(t, "web")

	p, err := GatePrepare(o.d, GateOpts{Ticket: o.ticket})
	if err != nil {
		t.Fatalf("GatePrepare: %v", err)
	}
	if len(p.Repos) != 1 || p.Repos[0].Repo != "web" || p.Repos[0].Head != head {
		t.Fatalf("Prepared repos = %+v, want the web alone at %s", p.Repos, head)
	}
	if want := []string{"web/ui/page.go"}; !reflect.DeepEqual(p.Request.MustReview, want) {
		t.Errorf("must_review = %v, want %v", p.Request.MustReview, want)
	}
	if _, err := os.Stat(filepath.Join(o.d.Home, "pool", "api", o.ticket+"-gate")); err == nil {
		t.Errorf("the gate made a lease for the api, which the ticket does not build in")
	}

	if _, err := GateFinish(o.d, p, cleanReport(p), Provenance{Session: "s-reviewer", How: ReviewerByFlag}); err != nil {
		t.Fatalf("GateFinish: %v", err)
	}
	rep := readReport(t, o.d, o.ticket, 1)
	if rep.Verdict != "clean" || !reflect.DeepEqual(rep.ReviewedSHA, map[string]string{"web": head}) {
		t.Errorf("report = %q reviewed_sha %v, want clean with the web's head alone", rep.Verdict, rep.ReviewedSHA)
	}
	if len(rep.TargetSHA) != 1 || rep.TargetSHA["web"] == "" {
		t.Errorf("target_sha = %v, want the web's alone", rep.TargetSHA)
	}
}

// TestGateAdoptsABranchInEveryRepo: a ticket built outside jig, on one branch
// that both repos of the orchestrator have. A gate that names the branch
// reviews it in both and adopts it: the ticket records the branch, and the
// start of the work in each repo is the tip the branch had there. A branch one
// of the repos lacks is refused, naming the repo, and records nothing.
func TestGateAdoptsABranchInEveryRepo(t *testing.T) {
	t.Parallel()
	o := orchestratorTicket(t, "api", "web")
	api, web := o.d.Machine.Clones["api"], o.d.Machine.Clones["web"]

	// feature/y is in the api alone.
	run(t, api, "checkout", "-b", "feature/y")
	writeAndCommit(t, api, "server/handler.go", orchAPISource, "api: handler")
	_, err := GatePrepare(o.d, GateOpts{Ticket: o.ticket, Branch: "feature/y", Early: true})
	wantAxiCode(t, err, "BRANCH_NOT_FOUND")
	if !strings.Contains(err.Error(), "repo web") {
		t.Errorf("err = %v, want it to name the repo the branch is missing in, web", err)
	}
	if rec, err := o.d.Store.ReadTicket(o.ticket); err != nil || rec.Branch != "" {
		t.Errorf("the refused gate recorded the branch: %+v (%v)", rec, err)
	}

	// feature/x is in both.
	run(t, api, "checkout", "main")
	run(t, api, "checkout", "-b", "feature/x")
	tipAPI := writeAndCommit(t, api, "server/handler.go", orchAPISource, "api: handler")
	run(t, web, "checkout", "-b", "feature/x")
	tipWeb := writeAndCommit(t, web, "ui/page.go", orchWebSource, "web: page")
	p, err := GatePrepare(o.d, GateOpts{Ticket: o.ticket, Branch: "feature/x", Early: true})
	if err != nil {
		t.Fatalf("GatePrepare --branch feature/x: %v", err)
	}
	if p.Branch != "feature/x" || len(p.Repos) != 2 || p.Repos[0].Head != tipAPI || p.Repos[1].Head != tipWeb {
		t.Errorf("Prepared = branch %q repos %+v, want feature/x at %s in the api and %s in the web", p.Branch, p.Repos, tipAPI, tipWeb)
	}
	if rec, err := o.d.Store.ReadTicket(o.ticket); err != nil || rec.Branch != "feature/x" {
		t.Errorf("the ticket records %+v (%v), want the adopted branch feature/x", rec, err)
	}
	for repo, want := range map[string]string{"api": tipAPI, "web": tipWeb} {
		got, err := os.ReadFile(o.d.Store.StartSHAPath(o.ticket, repo))
		if err != nil || strings.TrimSpace(string(got)) != want {
			t.Errorf("start of the work in %s = %q (%v), want the tip of feature/x there, %s", repo, got, err, want)
		}
	}
}

// reviewedClean takes the ticket's gate round over every repo it builds in and
// closes it clean, as a reviewer that built none of it does.
func (o orchestrated) reviewedClean(t *testing.T) {
	t.Helper()
	p, err := GatePrepare(o.d, GateOpts{Ticket: o.ticket})
	if err != nil {
		t.Fatalf("GatePrepare: %v", err)
	}
	if _, err := GateFinish(o.d, p, cleanReport(p), Provenance{Session: "s-reviewer", How: ReviewerByFlag}); err != nil {
		t.Fatalf("GateFinish: %v", err)
	}
}

// prLines are the pr lines of the ticket's journal, and whether publish-done
// follows the last of them.
func prLines(t *testing.T, d Deps, ticket string) (prs []journal.Line, doneAfter bool) {
	t.Helper()
	lines, err := journal.Read(d.Store, ticket)
	if err != nil {
		t.Fatal(err)
	}
	for _, l := range lines {
		switch l.Event {
		case journal.EventPr:
			prs, doneAfter = append(prs, l), false
		case journal.EventPublishDone:
			doneAfter = true
		}
	}
	return prs, doneAfter
}

// TestPublishHandOffPerRepo: the harness pushes the branch and opens the pull
// requests itself, one in each repo. A pull request is recorded with the repo
// it is in: without a repo jig asks which, a repo the project lacks is refused,
// the host's word on the head is held to that repo's reviewed head, the ticket
// is not published after the first, the second's body names the first, and the
// second finishes the publish. Each repo's review notes go to its own pull
// request, and recording opens and edits nothing.
func TestPublishHandOffPerRepo(t *testing.T) {
	t.Parallel()
	o := orchestratorTicket(t, "api", "web")
	headAPI, headWeb := o.build(t, "api"), o.build(t, "web")
	o.reviewedClean(t)
	logFile, addEnv := useGithubHost(t, &o.d, "", "")
	const apiPR, webPR = "https://github.com/owner/fixture-repo/pull/3", "https://github.com/owner/fixture-repo/pull/4"
	hostSays := func(sha string) { addEnv("GH_STUB_HEAD_REF="+o.branch, "GH_STUB_HEAD_OID="+sha) }

	hostSays(headAPI)
	_, err := PublishRecordPR(o.d, PublishOpts{Ticket: o.ticket}, apiPR, nil)
	if err == nil || !strings.Contains(err.Error(), "say which repo") {
		t.Errorf("--pr without a repo: err = %v, want it to ask which repo", err)
	}
	_, err = PublishRecordPR(o.d, PublishOpts{Ticket: o.ticket, Repo: "docs"}, apiPR, nil)
	if err == nil || !strings.Contains(err.Error(), `the project has no repo "docs"`) {
		t.Errorf("--pr for repo docs: err = %v, want the repo refused", err)
	}
	// The web's head is not the api's reviewed head.
	hostSays(headWeb)
	_, err = PublishRecordPR(o.d, PublishOpts{Ticket: o.ticket, Repo: "api"}, apiPR, nil)
	wantAxiCode(t, err, "PUBLISH_UNREVIEWED_HEAD")
	if prs, _ := prLines(t, o.d, o.ticket); len(prs) != 0 {
		t.Fatalf("a refused --pr journaled %+v", prs)
	}

	hostSays(headAPI)
	first, err := PublishRecordPR(o.d, PublishOpts{Ticket: o.ticket, Repo: "api"}, apiPR, nil)
	if err != nil {
		t.Fatalf("PublishRecordPR api: %v", err)
	}
	if first.Repo != "api" || !first.Verified || !reflect.DeepEqual(first.Pending, []string{"web"}) {
		t.Fatalf("first report = %+v, want the api verified with the web still asked for", first)
	}
	if prs, done := prLines(t, o.d, o.ticket); len(prs) != 1 || prs[0].Repo != "api" || prs[0].URL != apiPR || done {
		t.Fatalf("after the first pr: lines %+v, publish-done %v, want the api's alone and no publish-done", prs, done)
	}

	// The web's body now names the api's pull request.
	if _, err := PublishBody(o.d, PublishOpts{Ticket: o.ticket, Repo: "web"}); err != nil {
		t.Fatalf("PublishBody web: %v", err)
	}
	body, err := os.ReadFile(filepath.Join(o.d.Store.TicketDir(o.ticket), "pr", "web.md"))
	if err != nil || !strings.Contains(strings.ReplaceAll(string(body), "\r\n", "\n"), "- also api: "+apiPR+"\n") {
		t.Errorf("pr/web.md does not name the api's pull request (%v):\n%s", err, body)
	}

	hostSays(headWeb)
	second, err := PublishRecordPR(o.d, PublishOpts{Ticket: o.ticket, Repo: "web"}, webPR, nil)
	if err != nil {
		t.Fatalf("PublishRecordPR web: %v", err)
	}
	if second.Repo != "web" || len(second.Pending) != 0 {
		t.Fatalf("second report = %+v, want the web recorded and nothing left", second)
	}
	prs, done := prLines(t, o.d, o.ticket)
	if len(prs) != 2 || prs[1].Repo != "web" || prs[1].URL != webPR || !done {
		t.Errorf("after the second pr: lines %+v, publish-done %v, want both and publish-done after the web's", prs, done)
	}

	// Each repo's pull request carries that repo's own notes file, and the
	// review-notes line names the repo.
	comments := map[string]string{}
	for _, argv := range loggedGh(t, logFile) {
		if len(argv) > 2 && argv[0] == "pr" && argv[1] == "comment" {
			comments[argv[2]] = filepath.Base(filepath.ToSlash(argv[len(argv)-1]))
		}
	}
	if len(comments) != 2 || comments[apiPR] != "review-notes-api.md" || comments[webPR] != "review-notes-web.md" {
		t.Errorf("pr comments = %v, want the api's notes on %s and the web's on %s", comments, apiPR, webPR)
	}
	posted := map[string]string{}
	for _, l := range reviewNotesLines(t, o.d, o.ticket) {
		posted[l.Repo] = l.URL
	}
	if len(posted) != 2 || posted["api"] != apiPR || posted["web"] != webPR {
		t.Errorf("review-notes lines = %v, want one for each repo with its pull request", posted)
	}
	// Recording a pull request is not publishing it: jig opened none.
	if n := publishPrCalls(t, logFile); n != 0 {
		t.Errorf("--pr opened or edited a pull request %d time(s)", n)
	}
}

// TestPublishOverOneRepoOfSeveral: a ticket of the orchestrator whose slices
// are all in web, a repo that is not the first. Publish ships web from the
// web's reviewed head with no word of the api: no repo needs naming, nothing
// is pushed or opened for the api, and the body has no section about another
// repo.
func TestPublishOverOneRepoOfSeveral(t *testing.T) {
	t.Parallel()
	o := orchestratorTicket(t, "web")
	head := o.build(t, "web")
	o.reviewedClean(t)
	logFile, _ := useGithubHost(t, &o.d, "", "")

	// The api is not a repo of this ticket: naming it is refused.
	_, err := Publish(o.d, PublishOpts{Ticket: o.ticket, Yes: true, Repo: "api"})
	if err == nil || !strings.Contains(err.Error(), o.ticket+" builds in web, not in api") {
		t.Errorf("publish for the api: err = %v, want it refused for building in web", err)
	}

	report, err := Publish(o.d, PublishOpts{Ticket: o.ticket, Yes: true})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}
	if len(report.PRURL) != 1 || report.PRURL["web"] == "" || report.PRUpdated["web"] {
		t.Fatalf("report urls = %v updated %v, want the web's pull request alone, opened", report.PRURL, report.PRUpdated)
	}
	remotes := o.fx.NestedRemotes
	if got := originRef(t, remotes["web"], "refs/heads/"+o.branch); got != head {
		t.Errorf("origin of web: %s = %s, want the reviewed head %s", o.branch, got, head)
	}
	if got := originRef(t, remotes["api"], "refs/heads/"+o.branch); got != "" {
		t.Errorf("origin of api has %s at %s: publish shipped a repo the ticket does not build in", o.branch, got)
	}
	// The fake gh serves every repo from one host; the one pull request it was
	// asked to open is the web's.
	if n := ghCalls(loggedGh(t, logFile), "pr", "create"); n != 1 {
		t.Errorf("pr create calls = %d, want the web's alone", n)
	}
	body, err := os.ReadFile(filepath.Join(o.d.Store.TicketDir(o.ticket), "pr", "web.md"))
	if err != nil || strings.Contains(string(body), "Part of") {
		t.Errorf("pr/web.md of a ticket in one repo has a Part of section (%v):\n%s", err, body)
	}
	prs, done := prLines(t, o.d, o.ticket)
	if len(prs) != 1 || prs[0].Repo != "web" || prs[0].URL != report.PRURL["web"] || prs[0].Commit != head || !done {
		t.Errorf("pr lines = %+v, publish-done = %v, want the web's alone at %s, then publish-done", prs, done, head)
	}
	if _, err := os.Stat(filepath.Join(o.d.Home, "pool", "api", o.ticket+"-publish")); err == nil {
		t.Errorf("publish made a lease for the api, which the ticket does not build in")
	}
}
