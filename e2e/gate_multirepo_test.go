package e2e

// End-to-end tests of the gate over a ticket that spans the repos of an
// orchestrator: two repos nested in a folder, a slice in each. The gate opens
// one round that has a lease, a base, a head and a scope diff for each repo the
// ticket's slices build in; a reviewer in a session of its own reads the round,
// names its files as <repo>/<path>, and closes it; a finding's fix slice builds
// in the repo of the finding. The fake harness plays the builders and the
// reviewer; everything else is the real binary.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/store"
)

const (
	apiHandlerSource = "package server\n\n// Handler names what the api serves.\nfunc Handler() string { return \"total\" }\n"
	webPageSource    = "package ui\n\n// Page names what the web page shows.\nfunc Page() string { return \"order total\" }\n"
	webPageFixed     = "package ui\n\n// Page names what the web page shows, as dollars.\nfunc Page() string { return \"$4.00\" }\n"
)

// gateRequest is the part of a round's review.json that the tests of a gate
// over several repos read.
type gateRequest struct {
	Scope    string `json:"scope"`
	BaseSHA  string `json:"base_sha"`
	HeadSHA  string `json:"head_sha"`
	Worktree string `json:"worktree"`
	Repos    []struct {
		Repo       string   `json:"repo"`
		Worktree   string   `json:"worktree"`
		Branch     string   `json:"branch"`
		Scope      string   `json:"scope"`
		BaseSHA    string   `json:"base_sha"`
		HeadSHA    string   `json:"head_sha"`
		Oracles    []string `json:"oracles"`
		MustReview []string `json:"must_review"`
	} `json:"repos"`
	Oracles       []string `json:"oracles"`
	MustReview    []string `json:"must_review"`
	OraclesPassed []struct {
		Oracle     string   `json:"oracle"`
		Workspace  string   `json:"workspace"`
		ReusedFrom string   `json:"reused_from"`
		Slices     []string `json:"slices"`
	} `json:"oracles_passed"`
	Probed []struct {
		File  string `json:"file"`
		State string `json:"state"`
	} `json:"probed"`
	Hunks []struct {
		File   string `json:"file"`
		From   int    `json:"from"`
		Probes int    `json:"probes"`
	} `json:"hunks"`
}

// builtOrchestrator is the orchestrator fixture's ticket as the digester builds
// it: the api session committed the api's slice on the ticket's branch in its
// clone, the web session the web's, and each commit was verified, so both slices
// are green. Nothing is pushed.
type builtOrchestrator struct {
	fx             *fixture.Fixture
	env            jigEnv
	api, web       *fakeHarness
	branch         string
	shaAPI, shaWeb string
	st             *store.Store
}

// verifyPasses has h verify the commit at the head of its clone and wait for the
// oracle, and fails the test unless slice passed at sha.
func verifyPasses(t *testing.T, h *fakeHarness, slice, sha string) {
	t.Helper()
	r := h.Jig("verify", "--wait")
	if r.Code != 0 || !strings.Contains(r.Stdout, "slice: "+slice+"\n") || !strings.Contains(r.Stdout, "commit: "+sha) || !strings.Contains(r.Stdout, "state: pass") {
		t.Fatalf("jig verify --wait in %s: exit %d, want slice %s to pass at %s\nstdout:\n%s\nstderr:\n%s", h.Clone, r.Code, slice, sha, r.Stdout, r.Stderr)
	}
	h.WaitVerifiers()
}

// buildOrchestratorTicket builds the ticket described on builtOrchestrator.
func buildOrchestratorTicket(t *testing.T) builtOrchestrator {
	t.Helper()
	fx, env := newFixture(t, fixture.Opts{Orchestrator: true})
	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatal(err)
	}
	api, web := orchestratorSessions(t, fx, env)
	branch := nextJSON[nextRepos](t, env, fx.OrchestratorDir, fx.Ticket).Branch
	api.Branch(branch)
	web.Branch(branch)

	verifySlots <- struct{}{}
	defer func() { <-verifySlots }()
	b := builtOrchestrator{fx: fx, env: env, api: api, web: web, branch: branch, st: st}
	b.shaAPI = api.Commit("server", "server: handler", map[string]string{"server/handler.go": apiHandlerSource})
	verifyPasses(t, api, "server", b.shaAPI)
	b.shaWeb = web.Commit("ui", "ui: page", map[string]string{"ui/page.go": webPageSource})
	verifyPasses(t, web, "ui", b.shaWeb)
	for _, slice := range []string{"server", "ui"} {
		if state, err := st.ReadSliceState(fx.Ticket, slice); err != nil || state.State != "green" {
			t.Fatalf("slice %s is %+v (%v) after its commit verified, want green", slice, state, err)
		}
	}
	return b
}

// workFile is a file of the ticket's work directory in the store.
func (b builtOrchestrator) workFile(name string) string {
	return filepath.Join(b.st.TicketDir(b.fx.Ticket), "work", name)
}

// request reads the review.json the store keeps for round n.
func (b builtOrchestrator) request(t *testing.T, n string) gateRequest {
	t.Helper()
	var req gateRequest
	data := readFileOrFatal(t, b.workFile("gate.round-"+n+".review.json"))
	if err := json.Unmarshal(data, &req); err != nil {
		t.Fatalf("parse review.json of round %s: %v\n%s", n, err, data)
	}
	return req
}

// roundReport is the part of a recorded round's report.yaml that the tests of a
// gate over several repos read.
type roundReport struct {
	Verdict     string            `yaml:"verdict"`
	Independent any               `yaml:"independent"`
	TargetSHA   map[string]string `yaml:"target_sha"`
	ReviewedSHA map[string]string `yaml:"reviewed_sha"`
	FixSlices   []string          `yaml:"fix_slices"`
}

// report reads the report.yaml of the recorded round n.
func (b builtOrchestrator) report(t *testing.T, n string) roundReport {
	t.Helper()
	var rep roundReport
	data := readFileOrFatal(t, filepath.Join(b.st.TicketDir(b.fx.Ticket), "gate", "round-"+n, "report.yaml"))
	if err := yaml.Unmarshal(data, &rep); err != nil {
		t.Fatalf("parse report.yaml of round %s: %v\n%s", n, err, data)
	}
	return rep
}

// cleanResultOf writes the result.json of a reviewer that found nothing and read
// every file the request made it read, and returns its path.
func cleanResultOf(t *testing.T, mustReview []string) string {
	t.Helper()
	if mustReview == nil {
		mustReview = []string{}
	}
	data, err := json.Marshal(map[string]any{
		"findings": []any{}, "still_present": []any{}, "reviewed_paths": mustReview, "summary": "nothing found",
	})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "clean.json")
	writeFileOrFatal(t, path, data)
	return path
}

// TestGateCoversEveryRepo: a two-repo ticket with both slices green. jig gate
// opens a round whose context lists both repos, each with its own lease, base,
// head and files to cover; a reviewer records a finding in web, which has to be
// named as web/ui/page.go, and closes the round; the fix slice lands in web;
// the next round reviews what changed in web, and is clean.
func TestGateCoversEveryRepo(t *testing.T) {
	t.Parallel()
	b := buildOrchestratorTicket(t)
	ticket := b.fx.Ticket
	home := b.env.home

	// The builder opens the round. The context lists both repos.
	gate := b.api.Jig("gate", ticket)
	if gate.Code != 0 {
		t.Fatalf("jig gate: exit %d, want 0\nstdout:\n%s\nstderr:\n%s", gate.Code, gate.Stdout, gate.Stderr)
	}
	for _, want := range []string{
		"round: 1", "branch: " + b.branch, "repos[2]{repo,copy,head,base,scope,worktree,must_review}:",
		"  api,the clone's," + b.shaAPI + ",", "  web,the clone's," + b.shaWeb + ",",
	} {
		if !strings.Contains(gate.Stdout, want) {
			t.Errorf("jig gate output lacks %q\nstdout:\n%s", want, gate.Stdout)
		}
	}

	// jig here, from either clone, says the same of the open round: the head and
	// the copy of each repo.
	for _, h := range []*fakeHarness{b.api, b.web} {
		here := h.Jig("here")
		for _, want := range []string{"head_api: " + b.shaAPI[:7], "copy_api: ", "head_web: " + b.shaWeb[:7], "copy_web: "} {
			if here.Code != 0 || !strings.Contains(here.Stdout, want) {
				t.Errorf("jig here in %s: exit %d, lacks %q\n%s", h.Session, here.Code, want, here.Stdout)
			}
		}
	}

	// One lease, base, head, scope and list of files for each repo. The request
	// the store keeps names no top-level head, and no directory of this machine.
	req := b.request(t, "1")
	if req.Scope != "" || req.BaseSHA != "" || req.HeadSHA != "" || req.Worktree != "" {
		t.Errorf("review.json names a scope, base, head or worktree at the top (%+v), want them only in its repos", req)
	}
	if len(req.Repos) != 2 || req.Repos[0].Repo != "api" || req.Repos[1].Repo != "web" {
		t.Fatalf("review.json repos = %+v, want api and web", req.Repos)
	}
	for i, want := range []struct{ head, mustReview string }{{b.shaAPI, "api/server/handler.go"}, {b.shaWeb, "web/ui/page.go"}} {
		r := req.Repos[i]
		if r.HeadSHA != want.head || r.BaseSHA == "" || r.BaseSHA == want.head || r.Scope != "full" || r.Branch != b.branch || r.Worktree != "" {
			t.Errorf("review.json repo %s = %+v, want a full scope from its base to %s on %s", r.Repo, r, want.head, b.branch)
		}
		if !reflect.DeepEqual(r.MustReview, []string{want.mustReview}) || !reflect.DeepEqual(r.Oracles, []string{"test"}) {
			t.Errorf("review.json repo %s must_review %v, oracles %v, want [%s] and [test]", r.Repo, r.MustReview, r.Oracles, want.mustReview)
		}
	}
	if want := []string{"api/server/handler.go", "web/ui/page.go"}; !reflect.DeepEqual(req.MustReview, want) {
		t.Errorf("review.json must_review = %v, want the files of both repos, named by their repo: %v", req.MustReview, want)
	}
	// The pass jig verify recorded stands for the gate's run of the workspace it
	// verified, in each repo; the lib workspaces nobody verified the gate ran.
	reused, slicesOf := map[string]string{}, map[string]string{}
	for _, o := range req.OraclesPassed {
		reused[o.Workspace] = o.ReusedFrom
		slicesOf[o.Workspace] = strings.Join(o.Slices, ",")
	}
	if want := (map[string]string{"api/server": b.shaAPI, "api/lib": "", "web/ui": b.shaWeb, "web/lib": ""}); !reflect.DeepEqual(reused, want) {
		t.Errorf("oracles_passed reused_from = %v, want %v", reused, want)
	}
	// Each run names the slices whose oracle it is in its workspace.
	if want := (map[string]string{"api/server": "server", "api/lib": "", "web/ui": "ui", "web/lib": ""}); !reflect.DeepEqual(slicesOf, want) {
		t.Errorf("oracles_passed slices = %v, want %v", slicesOf, want)
	}
	var open struct {
		Head  string `yaml:"head"`
		Lease string `yaml:"lease"`
		Repos []struct {
			Repo  string `yaml:"repo"`
			Copy  string `yaml:"copy"`
			Head  string `yaml:"head"`
			Lease string `yaml:"lease"`
		} `yaml:"repos"`
	}
	openData := readFileOrFatal(t, b.workFile("gate.round-1.open.yaml"))
	if err := yaml.Unmarshal(openData, &open); err != nil {
		t.Fatalf("parse the open record: %v", err)
	}
	if open.Head != "" || open.Lease != "" || len(open.Repos) != 2 {
		t.Fatalf("open record = %+v, want the settings of each repo in its repos and none at the top", open)
	}
	for i, repo := range []string{"api", "web"} {
		r := open.Repos[i]
		lease := filepath.Join(home, filepath.FromSlash(r.Lease))
		if r.Repo != repo || r.Copy != "the clone's" || r.Lease != "pool/"+repo+"/"+ticket+"-gate" {
			t.Errorf("open record repo %d = %+v, want %s read from the clone's copy in its own lease", i, r, repo)
		}
		if head := gitLog(t, lease, "rev-parse", "HEAD"); head != r.Head {
			t.Errorf("the lease of %s is at %s, want the head the round opened on, %s", repo, head, r.Head)
		}
	}
	for _, name := range []string{"gate.round-1.open.yaml", "gate.round-1.review.json"} {
		if strings.Contains(strings.ReplaceAll(string(readFileOrFatal(t, b.workFile(name))), `\\`, `\`), home) {
			t.Errorf("%s names the jig home %s", name, home)
		}
	}

	// A reviewer in a session of its own reads the round: both repos, each in its
	// own worktree.
	rev := newHarness(t, b.fx, b.env, "s-reviewer")
	rev.Clone = b.fx.NestedDirs["web"]
	ctx := rev.Jig("review", "context", ticket, "--model", "test-model")
	if ctx.Code != 0 || !strings.Contains(ctx.Stdout, "repos[2]{") || !strings.Contains(ctx.Stdout, "name every file as <repo>/<path>") {
		t.Fatalf("jig review context: exit %d, want both repos and the way to name a file\nstdout:\n%s\nstderr:\n%s", ctx.Code, ctx.Stdout, ctx.Stderr)
	}
	for _, path := range []string{"api/server/handler.go", "web/ui/page.go"} {
		if !strings.Contains(ctx.Stdout, "\n  "+path+"\n") {
			t.Errorf("jig review context does not list %s to review:\n%s", path, ctx.Stdout)
		}
	}
	var full gateRequest
	if j := rev.Jig("review", "context", ticket, "--json"); j.Code != 0 || json.Unmarshal([]byte(j.Stdout), &full) != nil || len(full.Repos) != 2 {
		t.Fatalf("jig review context --json: exit %d, want the request with both repos\n%s", j.Code, j.Stdout)
	}
	for i, repo := range []string{"api", "web"} {
		sameDir(t, "worktree of "+repo, full.Repos[i].Worktree, filepath.Join(home, "pool", repo, ticket+"-gate"))
		if head := gitLog(t, full.Repos[i].Worktree, "rev-parse", "HEAD"); head != req.Repos[i].HeadSHA {
			t.Errorf("the worktree of %s is at %s, want %s", repo, head, req.Repos[i].HeadSHA)
		}
	}
	rev.Bash("git status --short")

	// A finding in web is named by its repo: a bare path is a file of no repo.
	finding := []string{
		"finding", ticket, "--line", "4", "--title", "The page shows cents, not dollars", "--detail", "Page returns the total as it is.",
		"--action", "fix", "--risk", "high", "--risk-rationale", "The shop shows a wrong total.", "--oracle", "test",
	}
	bare := rev.Jig(append(finding, "--file", "ui/page.go")...)
	projRefusal(t, "jig finding with a path of no repo", bare, 1, "code: REVIEW_INVALID", `file "ui/page.go" names no repo of this round (api, web): write it as <repo>/<path>`)
	other := rev.Jig(append(finding, "--file", "docs/page.go")...)
	projRefusal(t, "jig finding with a path of another repo", other, 1, "code: REVIEW_INVALID", "names no repo of this round")
	if f := rev.Jig(append(finding, "--file", "web/ui/page.go")...); f.Code != 0 || !strings.Contains(f.Stdout, "drafts: 1") {
		t.Fatalf("jig finding in web: exit %d, want the draft taken\nstdout:\n%s\nstderr:\n%s", f.Code, f.Stdout, f.Stderr)
	}
	// A probe is named by its repo too, and one in each repo joins the drafts
	// (ADR 0036), on the first hunk of each repo's file.
	firstHunk := map[string]int{}
	for _, h := range req.Hunks {
		if _, ok := firstHunk[h.File]; !ok {
			firstHunk[h.File] = h.From
		}
	}
	probe := func(file, outcome string) []string {
		return []string{"probe", ticket, "--seam", "the shop's total", "--file", file, "--line", strconv.Itoa(firstHunk[file]),
			"--mutation", "drop the conversion", "--command", "go test ./...", "--outcome", outcome}
	}
	projRefusal(t, "jig probe with a path of no repo", rev.Jig(probe("ui/page.go", "caught")...), 1, "code: REVIEW_INVALID", "names no repo of this round")
	for _, p := range [][2]string{{"api/server/handler.go", "caught"}, {"web/ui/page.go", "survived"}} {
		if _, ok := firstHunk[p[0]]; !ok {
			t.Fatalf("round 1 hunks = %+v, want one in %s", req.Hunks, p[0])
		}
		if f := rev.Jig(probe(p[0], p[1])...); f.Code != 0 || !strings.Contains(f.Stdout, "kind: probe") {
			t.Fatalf("jig probe in %s: exit %d, want the draft taken\nstdout:\n%s\nstderr:\n%s", p[0], f.Code, f.Stdout, f.Stderr)
		}
	}
	// reviewed_paths must cover every file of both repos, as the round names them.
	short := rev.Jig("review", "done", ticket, "--reviewed", "web/ui/page.go", "--summary", "one problem in web")
	projRefusal(t, "jig review done without the api's file", short, 1, "code: REVIEW_INVALID", `reviewed_paths is missing must_review path "api/server/handler.go"`)
	done := rev.Jig("review", "done", ticket, "--reviewed", strings.Join(req.MustReview, ","), "--summary", "one problem in web")
	if done.Code != 0 || !strings.Contains(done.Stdout, "verdict: fix-slices") || !strings.Contains(done.Stdout, "independent: true") {
		t.Fatalf("jig review done: exit %d, want a round with a fix slice, by a reviewer independent of the builders\nstdout:\n%s\nstderr:\n%s", done.Code, done.Stdout, done.Stderr)
	}
	for _, want := range []string{"target_sha[2]{repo,sha}:\n  api,", "\n  web,", "fix-1-web-ui-test"} {
		if !strings.Contains(done.Stdout, want) {
			t.Errorf("jig review done output lacks %q\n%s", want, done.Stdout)
		}
	}

	// The round is recorded with the commits of both repos, and the fix slice
	// builds in the repo of the finding.
	rep := b.report(t, "1")
	if rep.Verdict != "fix-slices" || rep.Independent != true || !reflect.DeepEqual(rep.FixSlices, []string{"fix-1-web-ui-test"}) {
		t.Errorf("report.yaml of round 1 = %+v, want fix-slices, independent, with the one fix slice", rep)
	}
	wantReviewed := map[string]string{"api": b.shaAPI, "web": b.shaWeb}
	if !reflect.DeepEqual(rep.ReviewedSHA, wantReviewed) {
		t.Errorf("reviewed_sha = %v, want the head of each repo: %v", rep.ReviewedSHA, wantReviewed)
	}
	wantTarget := map[string]string{
		"api": gitLog(t, b.fx.NestedRemotes["api"], "rev-parse", "main"),
		"web": gitLog(t, b.fx.NestedRemotes["web"], "rev-parse", "main"),
	}
	if !reflect.DeepEqual(rep.TargetSHA, wantTarget) {
		t.Errorf("target_sha = %v, want the target branch of each repo: %v", rep.TargetSHA, wantTarget)
	}
	slicesNow, err := b.st.ReadSlices(ticket)
	if err != nil {
		t.Fatal(err)
	}
	var fix *store.Slice
	for i := range slicesNow {
		if slicesNow[i].FromGate == 1 {
			fix = &slicesNow[i]
		}
	}
	if fix == nil || fix.ID != "fix-1-web-ui-test" || fix.Workspace != "web/ui" || fix.Oracle != "test" || !reflect.DeepEqual(fix.Findings, []string{"r1-f1"}) {
		t.Fatalf("the fix slice = %+v, want fix-1-web-ui-test in the workspace web/ui with the oracle test, for r1-f1", fix)
	}
	var findings struct {
		Findings []struct {
			ID        string `yaml:"id"`
			File      string `yaml:"file"`
			Workspace string `yaml:"workspace"`
			Status    string `yaml:"status"`
		} `yaml:"findings"`
	}
	if err := yaml.Unmarshal(readFileOrFatal(t, filepath.Join(b.st.TicketDir(ticket), "gate", "round-1", "findings.yaml")), &findings); err != nil || len(findings.Findings) != 1 {
		t.Fatalf("findings.yaml: %v, %+v", err, findings)
	}
	if f := findings.Findings[0]; f.File != "web/ui/page.go" || f.Workspace != "web/ui" || f.Status != "open" {
		t.Errorf("finding = %+v, want an open finding in web/ui/page.go, workspace web/ui", f)
	}

	// jig next puts the fix slice in the web, the repo of the finding.
	var ready []string
	for _, batch := range nextJSON[nextRepos](t, b.env, b.fx.OrchestratorDir, ticket).Ready {
		ready = append(ready, batch.Repo+": "+strings.Join(batch.Slices, "+"))
	}
	if want := []string{"web: fix-1-web-ui-test"}; !reflect.DeepEqual(ready, want) {
		t.Errorf("jig next after round 1: ready %v, want %v", ready, want)
	}

	// The fix lands in web, and the next round is a delta in web alone: the api
	// has nothing new to read.
	if r := b.api.Jig("gate", ticket); r.Code == 0 || !strings.Contains(r.Stdout, "code: GATE_NOT_GREEN") {
		t.Errorf("jig gate with the fix slice not built: exit %d, want GATE_NOT_GREEN\n%s", r.Code, r.Stdout)
	}
	verifySlots <- struct{}{}
	defer func() { <-verifySlots }()
	fixed := b.web.Commit("fix-1-web-ui-test", "fix: page", map[string]string{"ui/page.go": webPageFixed})
	verifyPasses(t, b.web, "fix-1-web-ui-test", fixed)

	if r := b.api.Jig("gate", ticket); r.Code != 0 || !strings.Contains(r.Stdout, "round: 2") {
		t.Fatalf("jig gate after the fix: exit %d, want round 2\nstdout:\n%s\nstderr:\n%s", r.Code, r.Stdout, r.Stderr)
	}
	req2 := b.request(t, "2")
	if len(req2.Repos) != 2 {
		t.Fatalf("round 2 repos = %+v, want api and web", req2.Repos)
	}
	apiRepo, webRepo := req2.Repos[0], req2.Repos[1]
	if apiRepo.Scope != "delta" || apiRepo.BaseSHA != b.shaAPI || apiRepo.HeadSHA != b.shaAPI || len(apiRepo.MustReview) != 0 {
		t.Errorf("round 2 api = %+v, want a delta of nothing: the api did not move", apiRepo)
	}
	if webRepo.Scope != "delta" || webRepo.BaseSHA != b.shaWeb || webRepo.HeadSHA != fixed || !reflect.DeepEqual(webRepo.MustReview, []string{"web/ui/page.go"}) {
		t.Errorf("round 2 web = %+v, want the delta %s..%s with web/ui/page.go to review", webRepo, b.shaWeb[:12], fixed[:12])
	}
	if !reflect.DeepEqual(req2.MustReview, []string{"web/ui/page.go"}) {
		t.Errorf("round 2 must_review = %v, want web/ui/page.go alone", req2.MustReview)
	}
	// The api's probe is current, its repo not having moved, and the web's
	// stale; the hunks are the whole change's in both repos, the api's still
	// probed.
	states := map[string]string{}
	for _, p := range req2.Probed {
		states[p.File] = p.State
	}
	if want := (map[string]string{"api/server/handler.go": "current", "web/ui/page.go": "stale"}); !reflect.DeepEqual(states, want) {
		t.Errorf("round 2 probed = %v, want %v", states, want)
	}
	probedIn := map[string]int{}
	for _, h := range req2.Hunks {
		probedIn[h.File] += h.Probes
	}
	if probedIn["api/server/handler.go"] != 1 || probedIn["web/ui/page.go"] != 0 {
		t.Errorf("round 2 hunks = %+v, want the api's hunk probed once and the web's unprobed", req2.Hunks)
	}
	// The reviewer reads the same in the round's view: the probes with their
	// state, and the web's hunks as the work list.
	ctx2 := rev.Jig("review", "context", ticket)
	for _, want := range []string{"probed[2]{round,file:line,outcome,state,seam,mutation}:", ",caught,current,", ",survived,stale,", "\"web/ui/page.go:"} {
		if ctx2.Code != 0 || !strings.Contains(ctx2.Stdout, want) {
			t.Errorf("jig review context of round 2: exit %d, lacks %q\n%s", ctx2.Code, want, ctx2.Stdout)
		}
	}
	done2 := rev.Jig("review", "done", ticket, "--reviewed", "web/ui/page.go", "--summary", "the page shows dollars")
	if done2.Code != 0 || !strings.Contains(done2.Stdout, "verdict: clean") || !strings.Contains(done2.Stdout, "independent: true") {
		t.Fatalf("jig review done of round 2: exit %d, want a clean round\nstdout:\n%s\nstderr:\n%s", done2.Code, done2.Stdout, done2.Stderr)
	}
	rep2 := b.report(t, "2")
	if wantHeads := (map[string]string{"api": b.shaAPI, "web": fixed}); !reflect.DeepEqual(rep2.ReviewedSHA, wantHeads) {
		t.Errorf("round 2 reviewed_sha = %v, want %v", rep2.ReviewedSHA, wantHeads)
	}
}

// TestGateJudgesIndependenceAgainstTheBuildersOfEveryRepo: a session that built
// in one repo of the ticket may not review the ticket, whichever repo its work
// is in - the builder of the api, the builder of the web, and a session that
// only edited a file of the web with its tools - and jig tells it so before the
// review. A session that built none of it reviews both repos.
func TestGateJudgesIndependenceAgainstTheBuildersOfEveryRepo(t *testing.T) {
	t.Parallel()
	b := buildOrchestratorTicket(t)
	ticket := b.fx.Ticket

	// Each builder built in one repo only, and neither is fresh for the ticket.
	for _, h := range []*fakeHarness{b.api, b.web} {
		next := h.Jig("next", ticket)
		if next.Code != 0 || !strings.Contains(next.Stdout, "fresh: no") {
			t.Errorf("jig next as %s: exit %d, want fresh: no\n%s", h.Session, next.Code, next.Stdout)
		}
	}
	if gate := b.api.Jig("gate", ticket); gate.Code != 0 {
		t.Fatalf("jig gate: exit %d\nstdout:\n%s\nstderr:\n%s", gate.Code, gate.Stdout, gate.Stderr)
	}

	// closedBy has h read the open round and close it, as a reviewer would.
	closedBy := func(h *fakeHarness, round string) jigResult {
		t.Helper()
		if ctx := h.Jig("review", "context", ticket); ctx.Code != 0 {
			t.Fatalf("jig review context as %s: exit %d\nstdout:\n%s\nstderr:\n%s", h.Session, ctx.Code, ctx.Stdout, ctx.Stderr)
		}
		return h.Jig("review", "done", ticket, "--result", cleanResultOf(t, b.request(t, round).MustReview))
	}
	// opened has the api's session open round n.
	opened := func(round string) {
		t.Helper()
		if r := b.api.Jig("gate", ticket); r.Code != 0 || !strings.Contains(r.Stdout, "round: "+round) {
			t.Fatalf("jig gate for round %s: exit %d\nstdout:\n%s\nstderr:\n%s", round, r.Code, r.Stdout, r.Stderr)
		}
	}
	notIndependent := func(h *fakeHarness, round string) {
		t.Helper()
		done := closedBy(h, round)
		if done.Code != 1 || !strings.Contains(done.Stdout, "REVIEW_NOT_INDEPENDENT") || !strings.Contains(done.Stdout, "independent: false") {
			t.Fatalf("jig review done by %s: exit %d, want REVIEW_NOT_INDEPENDENT\nstdout:\n%s\nstderr:\n%s", h.Session, done.Code, done.Stdout, done.Stderr)
		}
		if rep := b.report(t, round); rep.Verdict != "not-independent" || rep.Independent != false {
			t.Errorf("report.yaml of round %s = %+v, want a round that is not independent", round, rep)
		}
	}

	// The web's builder never touched the api, and the api's builder never
	// touched the web; each built part of what the round reviews.
	notIndependent(b.web, "1")
	opened("2")
	notIndependent(b.api, "2")

	// A session that only edited a file in the web clone with its tools: the
	// ledger names it a builder by the ticket the branch of that clone binds, in
	// either repo.
	edited := newHarness(t, b.fx, b.env, "s-edit")
	edited.Clone = b.fx.NestedDirs["web"]
	edited.Hook("PostToolUse", map[string]any{"tool_name": "Edit", "tool_input": map[string]any{"file_path": filepath.Join(edited.Clone, "ui", "page.go")}})
	opened("3")
	notIndependent(edited, "3")

	// A session that built none of it reviews round 4, over both repos.
	opened("4")
	rev := newHarness(t, b.fx, b.env, "s-reviewer")
	rev.Clone = b.fx.NestedDirs["web"]
	done := closedBy(rev, "4")
	if done.Code != 0 || !strings.Contains(done.Stdout, "verdict: clean") || !strings.Contains(done.Stdout, "independent: true") {
		t.Fatalf("jig review done by a session that built nothing: exit %d, want a clean round\nstdout:\n%s\nstderr:\n%s", done.Code, done.Stdout, done.Stderr)
	}
}

// TestGateCoversTheOneRepoATicketBuildsIn: a ticket of the orchestrator whose
// slices are all in one repo, and that repo is not the first. The gate covers
// that repo and no other: one lease, one head, and nothing of the api.
func TestGateCoversTheOneRepoATicketBuildsIn(t *testing.T) {
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

	gate := web.Jig("gate", ticket)
	if gate.Code != 0 || !strings.Contains(gate.Stdout, "repos[1]{") || !strings.Contains(gate.Stdout, "  web,the clone's,"+sha+",") {
		t.Fatalf("jig gate: exit %d, want a round over the web alone\nstdout:\n%s\nstderr:\n%s", gate.Code, gate.Stdout, gate.Stderr)
	}
	if strings.Contains(gate.Stdout, "  api,") {
		t.Errorf("jig gate lists the api, which the ticket does not build in:\n%s", gate.Stdout)
	}
	if _, err := os.Stat(filepath.Join(env.home, "pool", "api", ticket+"-gate")); err == nil {
		t.Errorf("the gate made a lease for the api, which the ticket does not build in")
	}
	data := readFileOrFatal(t, filepath.Join(st.TicketDir(ticket), "work", "gate.round-1.review.json"))
	var req gateRequest
	if err := json.Unmarshal(data, &req); err != nil {
		t.Fatal(err)
	}
	if len(req.Repos) != 1 || req.Repos[0].Repo != "web" || req.Repos[0].HeadSHA != sha || !reflect.DeepEqual(req.MustReview, []string{"web/ui/page.go"}) {
		t.Errorf("review.json = %s, want the web alone at %s with web/ui/page.go to review", data, sha)
	}

	rev := newHarness(t, fx, env, "s-reviewer")
	rev.Clone = fx.NestedDirs["web"]
	if ctx := rev.Jig("review", "context", ticket); ctx.Code != 0 {
		t.Fatalf("jig review context: exit %d\nstdout:\n%s\nstderr:\n%s", ctx.Code, ctx.Stdout, ctx.Stderr)
	}
	done := rev.Jig("review", "done", ticket, "--result", cleanResultOf(t, req.MustReview))
	if done.Code != 0 || !strings.Contains(done.Stdout, "verdict: clean") || !strings.Contains(done.Stdout, "target_sha[1]{repo,sha}:\n  web,") {
		t.Fatalf("jig review done: exit %d, want a clean round with the web's target alone\nstdout:\n%s\nstderr:\n%s", done.Code, done.Stdout, done.Stderr)
	}
	var rep struct {
		ReviewedSHA map[string]string `yaml:"reviewed_sha"`
	}
	if err := yaml.Unmarshal(readFileOrFatal(t, filepath.Join(st.TicketDir(ticket), "gate", "round-1", "report.yaml")), &rep); err != nil || !reflect.DeepEqual(rep.ReviewedSHA, map[string]string{"web": sha}) {
		t.Errorf("reviewed_sha = %v (%v), want the web's head alone", rep.ReviewedSHA, err)
	}
}

// TestGateAdoptsABranchInEveryRepo: a ticket built outside jig, on one branch
// that both repos of the orchestrator have. `jig gate --branch` reviews it in
// both and adopts it: the ticket records the branch, and the start of the work
// in each repo is the tip the branch had there. A branch one of the repos lacks
// is refused, naming the repo, and records nothing.
func TestGateAdoptsABranchInEveryRepo(t *testing.T) {
	t.Parallel()
	fx, env := newFixture(t, fixture.Opts{Orchestrator: true})
	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatal(err)
	}
	const ticket = "JIG-2"
	built, err := st.ReadSlices(fx.Ticket)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.CreateTicketRecord(ticket, store.Ticket{Title: "Built by hand"}); err != nil {
		t.Fatal(err)
	}
	writeFileOrFatal(t, filepath.Join(st.TicketDir(ticket), "brief.md"), readFileOrFatal(t, filepath.Join(st.TicketDir(fx.Ticket), "brief.md")))
	if err := st.AppendSlices(ticket, []store.Slice{
		{ID: "handler", Workspace: "api/server", Goal: "Add up.", Oracle: "test", BlockedBy: []string{}, FromBrief: built[0].FromBrief},
		{ID: "page", Workspace: "web/ui", Goal: "Show it.", Oracle: "test", BlockedBy: []string{}, FromBrief: built[1].FromBrief},
	}); err != nil {
		t.Fatal(err)
	}
	api, web := orchestratorSessions(t, fx, env)

	// feature/y is in the api alone.
	api.Branch("feature/y")
	api.Commit("", "api: handler", map[string]string{"server/handler.go": apiHandlerSource})
	r := api.Jig("gate", ticket, "--branch", "feature/y", "--early")
	projRefusal(t, "jig gate --branch of a branch the web lacks", r, 1, "code: BRANCH_NOT_FOUND", "error: repo web: ")
	if rec, err := st.ReadTicket(ticket); err != nil || rec.Branch != "" {
		t.Errorf("the refused gate recorded the branch: %+v (%v)", rec, err)
	}

	// feature/x is in both.
	api.git("checkout", "main")
	api.Branch("feature/x")
	shaAPI := api.Commit("", "api: handler", map[string]string{"server/handler.go": apiHandlerSource})
	web.Branch("feature/x")
	shaWeb := web.Commit("", "web: page", map[string]string{"ui/page.go": webPageSource})
	gate := api.Jig("gate", ticket, "--branch", "feature/x", "--early")
	for _, want := range []string{"branch: feature/x", "repos[2]{", "  api,the clone's," + shaAPI + ",", "  web,the clone's," + shaWeb + ","} {
		if !strings.Contains(gate.Stdout, want) {
			t.Errorf("jig gate --branch output lacks %q\nstdout:\n%s", want, gate.Stdout)
		}
	}
	if gate.Code != 0 {
		t.Fatalf("jig gate --branch feature/x: exit %d, want 0\nstdout:\n%s\nstderr:\n%s", gate.Code, gate.Stdout, gate.Stderr)
	}
	if rec, err := st.ReadTicket(ticket); err != nil || rec.Branch != "feature/x" {
		t.Errorf("the ticket records %+v (%v), want the adopted branch feature/x", rec, err)
	}
	for repo, want := range map[string]string{"api": shaAPI, "web": shaWeb} {
		if got := strings.TrimSpace(string(readFileOrFatal(t, st.StartSHAPath(ticket, repo)))); got != want {
			t.Errorf("start of the work in %s = %s, want the tip of feature/x there, %s", repo, got, want)
		}
	}
}
