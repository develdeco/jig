package e2e

// End-to-end tests of a ticket that spans the repos of an orchestrator: the
// shape `jig init --orchestrator` leaves, two repos nested in a folder, a slice
// in each that names its repo as "<repo>/<workspace>". The fake harness plays a
// session in each repo; everything else is the real binary.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/fixture"
	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/store"
)

// nextRepos is the part of `jig next <ticket> --json` the tests of a ticket
// across repos read.
type nextRepos struct {
	Branch string `json:"branch"`
	State  string `json:"state"`
	Ready  []struct {
		Repo   string   `json:"repo"`
		Slices []string `json:"slices"`
		Start  string   `json:"start"`
	} `json:"ready"`
	Topology []struct {
		ID        string `json:"id"`
		Workspace string `json:"workspace"`
		Repo      string `json:"repo"`
	} `json:"topology"`
	Unplaced  []string `json:"unplaced_workspaces"`
	Ambiguous []string `json:"ambiguous_workspaces"`
}

// orchestratorSessions are a fake harness in each nested repo of an
// orchestrator fixture: the session in api and the session in web.
func orchestratorSessions(t *testing.T, fx *fixture.Fixture, env jigEnv) (api, web *fakeHarness) {
	t.Helper()
	api = newHarness(t, fx, env, "s-api")
	api.Clone = fx.NestedDirs["api"]
	web = newHarness(t, fx, env, "s-web")
	web.Clone = fx.NestedDirs["web"]
	return api, web
}

// commitSlice makes, in the clone of h, a commit that adds file with body for
// slice, through the harness's Bash tool: its PostToolUse hook is what starts
// the verification. It returns the sha.
func commitSlice(t *testing.T, h *fakeHarness, slice, file, body string) string {
	t.Helper()
	writeFileOrFatal(t, filepath.Join(h.Clone, filepath.FromSlash(file)), []byte(body))
	if res := h.Bash("git add -A"); res.Code != 0 {
		t.Fatalf("git add in %s: exit %d\n%s", h.Clone, res.Code, res.Stderr)
	}
	msg := slice + ": " + file
	if res := h.Bash("git commit -m '" + msg + "' -m 'Jig-Slice: " + slice + "'"); res.Code != 0 {
		t.Fatalf("git commit in %s: exit %d\n%s", h.Clone, res.Code, res.Stderr)
	}
	return h.git("rev-parse", "HEAD")
}

// TestTwoRepoTicketBuildsInEachRepo: an orchestrator of the repos api and web,
// and a ticket with a slice in each (api/server and web/ui). validate checks
// both manifests; next lists both repos' slices with where the branch starts
// in each; context and claim name the repo and clone of a slice wherever they
// are run from; a commit in each clone with its slice's trailer is verified in
// that clone, with the oracle of its own workspace; status shows both green.
func TestTwoRepoTicketBuildsInEachRepo(t *testing.T) {
	t.Parallel()
	fx, env := newFixture(t, fixture.Opts{Orchestrator: true})
	ticket := fx.Ticket
	apiDir, webDir := fx.NestedDirs["api"], fx.NestedDirs["web"]
	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatal(err)
	}

	// validate reads the manifest of every repo, from the orchestrator or from
	// inside either clone, and approves the brief once.
	for _, cwd := range []string{fx.OrchestratorDir, apiDir, webDir} {
		if r := runJig(t, env, cwd, "validate", ticket, "--check"); r.Code != 0 || !strings.Contains(r.Stdout, "valid: yes") {
			t.Fatalf("jig validate --check in %s: exit %d, want valid\nstdout:\n%s\nstderr:\n%s", cwd, r.Code, r.Stdout, r.Stderr)
		}
	}
	if r := runJig(t, env, fx.OrchestratorDir, "validate", ticket); r.Code != 0 || !strings.Contains(r.Stdout, "valid: yes") {
		t.Fatalf("jig validate: exit %d, want valid\nstdout:\n%s\nstderr:\n%s", r.Code, r.Stdout, r.Stderr)
	}

	// next lists both repos' slices, and where the branch starts in each.
	n := nextJSON[nextRepos](t, env, fx.OrchestratorDir, ticket, "--topology")
	var ready []string
	for _, b := range n.Ready {
		ready = append(ready, b.Repo+": "+strings.Join(b.Slices, "+")+" from "+b.Start)
	}
	if want := []string{"api: server from origin/main", "web: ui from origin/main"}; !reflect.DeepEqual(ready, want) {
		t.Errorf("ready = %v, want %v", ready, want)
	}
	if len(n.Topology) != 2 || n.Topology[0].Repo != "api" || n.Topology[0].Workspace != "api/server" || n.Topology[1].Repo != "web" || n.Topology[1].Workspace != "web/ui" {
		t.Errorf("topology = %+v, want server in api as api/server and ui in web as web/ui", n.Topology)
	}
	if len(n.Unplaced) != 0 || len(n.Ambiguous) != 0 {
		t.Errorf("unplaced %v, ambiguous %v, want none: both slices name their repo", n.Unplaced, n.Ambiguous)
	}
	r := runJig(t, env, webDir, "next", ticket)
	for _, want := range []string{"ready[2]{repo,slices,start}:\n  api,server,origin/main\n  web,ui,origin/main\n", "2 repos have ready slices (api, web)"} {
		if !strings.Contains(r.Stdout, want) {
			t.Errorf("jig next %s from web lacks %q:\n%s", ticket, want, r.Stdout)
		}
	}

	// context names the repo and clone of the slice, from the other repo too.
	for _, c := range []struct{ cwd, slice, repo, clone string }{
		{apiDir, "server", "api", apiDir},
		{webDir, "server", "api", apiDir},
		{fx.OrchestratorDir, "ui", "web", webDir},
	} {
		r := runJig(t, env, c.cwd, "context", ticket, "--slice", c.slice)
		if r.Code != 0 || !strings.Contains(r.Stdout, "\n  repo: "+c.repo+"\n") || !strings.Contains(r.Stdout, "\n  clone: ") {
			t.Errorf("jig context --slice %s in %s: exit %d, want repo %s and a clone\n%s", c.slice, c.cwd, r.Code, c.repo, r.Stdout)
		}
		if c.cwd != c.clone && !strings.Contains(r.Stdout, "builds in repo "+c.repo+": work in ") {
			t.Errorf("jig context --slice %s in %s does not send the session to %s:\n%s", c.slice, c.cwd, c.clone, r.Stdout)
		}
		var named struct {
			Repo  string `json:"repo"`
			Clone string `json:"clone"`
		}
		j := runJig(t, env, c.cwd, "context", ticket, "--slice", c.slice, "--json")
		if err := json.Unmarshal([]byte(j.Stdout), &named); err != nil || named.Repo != c.repo {
			t.Fatalf("jig context --json in %s: %v, repo %q, want %s\n%s", c.cwd, err, named.Repo, c.repo, j.Stdout)
		}
		sameDir(t, "context clone of slice "+c.slice+" from "+c.cwd, named.Clone, c.clone)
	}

	// claim binds each slice to its repo: the start point is recorded in that
	// repo, whether the claim is made in its clone or from the orchestrator.
	api, web := orchestratorSessions(t, fx, env)
	api.Branch(n.Branch)
	web.Branch(n.Branch)
	if res := api.Jig("claim", ticket, "--slice", "server"); res.Code != 0 || !strings.Contains(res.Stdout, "repo: api\n") || !strings.Contains(res.Stdout, "result: claimed") {
		t.Fatalf("jig claim server in api: exit %d\nstdout:\n%s\nstderr:\n%s", res.Code, res.Stdout, res.Stderr)
	}
	if _, err := os.Stat(st.StartSHAPath(ticket, "api")); err != nil {
		t.Errorf("claiming server recorded no start point for api: %v", err)
	}
	if _, err := os.Stat(st.StartSHAPath(ticket, "web")); err == nil {
		t.Errorf("claiming server recorded a start point for web too")
	}
	res := runJig(t, env.with("JIG_SESSION="+web.Session), fx.OrchestratorDir, "claim", ticket, "--slice", "ui")
	if res.Code != 0 || !strings.Contains(res.Stdout, "repo: web\n") || !strings.Contains(res.Stdout, "result: claimed") || !strings.Contains(res.Stdout, "Slice ui builds in repo web: work in ") {
		t.Fatalf("jig claim ui from the orchestrator: exit %d\nstdout:\n%s\nstderr:\n%s", res.Code, res.Stdout, res.Stderr)
	}
	if data, err := os.ReadFile(st.StartSHAPath(ticket, "web")); err != nil || strings.TrimSpace(string(data)) != web.git("rev-parse", "main") {
		t.Errorf("start point of web = %q (%v), want the tip of main in its clone", data, err)
	}

	// A commit in each clone, with its slice's trailer, is verified in that
	// clone. The first call of a session only starts the watch on HEAD.
	verifySlots <- struct{}{}
	defer func() { <-verifySlots }()
	api.Bash("git status --short")
	web.Bash("git status --short")
	shaAPI := commitSlice(t, api, "server", "server/handler.go", "package server\n\n// Handler names what the api serves.\nfunc Handler() string { return \"total\" }\n")
	shaWeb := commitSlice(t, web, "ui", "ui/page.go", "package ui\n\n// Page names what the web page shows.\nfunc Page() string { return \"order total\" }\n")
	for _, c := range []struct{ slice, sha, other, command string }{
		{"server", shaAPI, webDir, " test ./server/..."},
		{"ui", shaWeb, apiDir, " test ./ui/..."},
	} {
		oracle := waitJournal(t, fx, ticket, verifyWait, func(l journal.Line) bool {
			return l.Event == journal.EventOracle && l.Slice == c.slice && l.Commit == c.sha
		})
		if oracle.Outcome != "pass" || !strings.HasSuffix(oracle.Command, c.command) {
			t.Errorf("oracle line of %s = %+v, want a pass of the oracle that ends in%q: the workspace of its own repo", c.slice, oracle, c.command)
		}
		waitJournal(t, fx, ticket, verifyWait, func(l journal.Line) bool {
			return l.Event == journal.EventVerified && l.Slice == c.slice && l.Commit == c.sha
		})
		// The commit is in the clone it was verified from and not in the other:
		// nothing was verified in the wrong repo.
		if _, err := gitx.Run(c.other, "cat-file", "-e", c.sha+"^{commit}"); err == nil {
			t.Errorf("commit %s of slice %s is in %s too", c.sha[:12], c.slice, c.other)
		}
	}
	api.WaitVerifiers()
	web.WaitVerifiers()

	// status shows both slices green.
	status := runJig(t, env, fx.OrchestratorDir, "status", ticket)
	if status.Code != 0 || !strings.Contains(status.Stdout, "server,green") || !strings.Contains(status.Stdout, "ui,green") {
		t.Errorf("jig status %s: exit %d, want both slices green\n%s", ticket, status.Code, status.Stdout)
	}
	if after := nextJSON[nextRepos](t, env, fx.OrchestratorDir, ticket); after.State != "green" || len(after.Ready) != 0 {
		t.Errorf("jig next after both are built: state %q, ready %+v, want green and nothing ready", after.State, after.Ready)
	}
}

// TestAmbiguousWorkspaceIsAValidationProblem: a bare workspace that two repos
// declare is not owned by the first repo: validate refuses the slice, naming
// the repos and the way to say which is meant, and next puts it under no repo.
// Naming the repo settles it. A bare workspace no repo declares, and a repo's
// workspace that its manifest lacks, are refused as well.
func TestAmbiguousWorkspaceIsAValidationProblem(t *testing.T) {
	t.Parallel()
	fx, env := newFixture(t, fixture.Opts{Orchestrator: true})
	ticket := fx.Ticket
	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatal(err)
	}
	slices, err := st.ReadSlices(ticket)
	if err != nil {
		t.Fatal(err)
	}
	slicesFile := filepath.Join(st.TicketDir(ticket), "slices.yaml")
	pristine := readFileOrFatal(t, slicesFile)
	addSlice := func(id, workspace string) {
		t.Helper()
		writeFileOrFatal(t, slicesFile, pristine)
		err := st.AppendSlices(ticket, []store.Slice{{ID: id, Workspace: workspace, Goal: "Share a helper.", Oracle: "test", BlockedBy: []string{}, FromBrief: slices[0].FromBrief}})
		if err != nil {
			t.Fatal(err)
		}
	}
	validate := func() jigResult {
		t.Helper()
		return runJig(t, env, fx.OrchestratorDir, "validate", ticket, "--check")
	}

	// Both repos declare lib: a slice that writes it bare belongs to neither.
	addSlice("helper", "lib")
	r := validate()
	projRefusal(t, "validate with an ambiguous workspace", r, 2,
		"ticket JIG-1 failed validation", `slice helper: ambiguous workspace "lib"`, "repos api and web declare it", "name its repo, as api/lib or web/lib")
	if strings.Contains(r.Stdout, "slice server") || strings.Contains(r.Stdout, "slice ui") {
		t.Errorf("validate blames a slice that names its repo:\n%s", r.Stdout)
	}
	n := nextJSON[nextRepos](t, env, fx.OrchestratorDir, ticket)
	var ready []string
	for _, b := range n.Ready {
		ready = append(ready, b.Repo+": "+strings.Join(b.Slices, "+"))
	}
	if want := []string{"api: server", "web: ui", "?: helper"}; !reflect.DeepEqual(ready, want) || !reflect.DeepEqual(n.Ambiguous, []string{"lib"}) {
		t.Errorf("next: ready %v, ambiguous %v, want %v and [lib]: the first repo does not own lib", ready, n.Ambiguous, want)
	}
	r = runJig(t, env, fx.OrchestratorDir, "next", ticket)
	for _, want := range []string{"name the repo in the slice, as <repo>/<workspace>", "2 repos have ready slices (api, web)"} {
		if !strings.Contains(r.Stdout, want) {
			t.Errorf("jig next lacks %q:\n%s", want, r.Stdout)
		}
	}

	// Naming the repo settles it, in either.
	for _, workspace := range []string{"api/lib", "web/lib"} {
		addSlice("helper", workspace)
		if r := validate(); r.Code != 0 || !strings.Contains(r.Stdout, "valid: yes") {
			t.Errorf("validate with the workspace %s: exit %d, want valid\n%s", workspace, r.Code, r.Stdout)
		}
	}

	// A bare workspace no repo declares, and one a repo lacks.
	addSlice("helper", "stray")
	projRefusal(t, "validate with a workspace no repo declares", validate(), 2, `workspace "stray" is declared by none of the repos (api, web)`, "name its repo, as <repo>/stray")
	addSlice("helper", "web/server")
	projRefusal(t, "validate with a workspace its repo lacks", validate(), 2, `slice helper: workspace "server" not in the manifest of repo web`)
}

// TestGateRefusesAMultiRepoTicketUntilItCoversThem: jig gate and jig publish
// cover one repo, so a ticket whose slices build in two is refused by both, in
// every form of publish, whatever state its slices are in; a ticket that builds
// in one repo of the same project is not refused for that.
func TestGateRefusesAMultiRepoTicketUntilItCoversThem(t *testing.T) {
	t.Parallel()
	fx, env := newFixture(t, fixture.Opts{Orchestrator: true})
	ticket := fx.Ticket
	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatal(err)
	}
	slices, err := st.ReadSlices(ticket)
	if err != nil {
		t.Fatal(err)
	}
	const want = "this ticket's slices span repos api and web; the gate covers one repo until multi-repo review lands"

	for _, cwd := range []string{fx.OrchestratorDir, fx.NestedDirs["api"], fx.NestedDirs["web"]} {
		for _, args := range [][]string{{"gate", ticket}, {"publish", ticket}, {"publish", ticket, "--body"}} {
			r := runJig(t, env, cwd, args...)
			projRefusal(t, "jig "+strings.Join(args, " ")+" in "+filepath.Base(cwd), r, 1, "error: "+want, "code: MULTI_REPO_NOT_YET")
		}
	}

	// The same once every slice is green: the refusal is about where the slices
	// build, not about whether they are done.
	setStates(t, st, ticket, map[string]string{"server": "green", "ui": "green"})
	projRefusal(t, "jig gate with every slice green", runJig(t, env, fx.OrchestratorDir, "gate", ticket), 1, "code: MULTI_REPO_NOT_YET")
	projRefusal(t, "jig publish with every slice green", runJig(t, env, fx.OrchestratorDir, "publish", ticket), 1, "code: MULTI_REPO_NOT_YET")

	// A ticket of the same project that builds in one repo is judged as ever:
	// not green, it is turned away for that.
	if err := st.CreateTicketRecord("JIG-2", store.Ticket{Title: "Api only"}); err != nil {
		t.Fatal(err)
	}
	if err := st.AppendSlices("JIG-2", []store.Slice{{ID: "total", Workspace: "api/server", Goal: "Add up.", Oracle: "test", BlockedBy: []string{}, FromBrief: slices[0].FromBrief}}); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"gate", "JIG-2"}, {"publish", "JIG-2"}} {
		r := runJig(t, env, fx.OrchestratorDir, args...)
		if strings.Contains(r.Stdout, "MULTI_REPO_NOT_YET") || !strings.Contains(r.Stdout, "code: GATE_NOT_GREEN") {
			t.Errorf("jig %s for a ticket in one repo: exit %d, want GATE_NOT_GREEN and no multi-repo refusal\n%s", strings.Join(args, " "), r.Code, r.Stdout)
		}
	}
}

// TestVerifyHoldsACommitToItsRepo: a commit is verified in the repo it was made
// in, for a slice of that repo. A commit whose trailer names a slice of the
// other repo is not verified: the hook that sees it starts nothing, and jig
// verify says why. jig verify run from the orchestrator, in no repo, has to be
// told the clone, and then verifies there.
func TestVerifyHoldsACommitToItsRepo(t *testing.T) {
	t.Parallel()
	fx, env := newFixture(t, fixture.Opts{Orchestrator: true})
	ticket := fx.Ticket
	webDir := fx.NestedDirs["web"]
	api, web := orchestratorSessions(t, fx, env)
	web.Branch("jig/" + ticket)
	api.Branch("jig/" + ticket)

	// The web session commits a change under the trailer of the api's slice.
	web.Bash("git status --short")
	wrong := commitSlice(t, web, "server", "ui/wrong.go", "package ui\n\n// Wrong is a change in the web repo made for the api's slice.\nfunc Wrong() {}\n")
	if got := spawnedFor(readLedger(t, env), wrong); got != 0 {
		t.Errorf("%d verifications were started for a commit in web made for the api's slice, want none", got)
	}
	r := web.Jig("verify")
	projRefusal(t, "jig verify of a commit for another repo's slice", r, 2,
		"slice server of "+ticket+" builds in repo api, but this commit is in repo web", "Commit it in the clone of api")

	// The same commit, for the web's slice, is verified: here by the command, which
	// waits for the oracle. The api's session does the same in its own clone.
	right := commitSlice(t, web, "ui", "ui/right.go", "package ui\n\n// Right is a change in the web repo made for its own slice.\nfunc Right() {}\n")
	verifySlots <- struct{}{}
	defer func() { <-verifySlots }()
	r = web.Jig("verify", "--wait")
	if r.Code != 0 || !strings.Contains(r.Stdout, "slice: ui") || !strings.Contains(r.Stdout, "commit: "+right) || !strings.Contains(r.Stdout, "state: pass") {
		t.Errorf("jig verify --wait in web: exit %d, want the ui slice at %s to pass\nstdout:\n%s\nstderr:\n%s", r.Code, right[:12], r.Stdout, r.Stderr)
	}
	apiSha := api.Commit("server", "server: handler", map[string]string{"server/handler.go": "package server\n\n// Handler names what the api serves.\nfunc Handler() string { return \"total\" }\n"})
	r = api.Jig("verify", "--wait")
	if r.Code != 0 || !strings.Contains(r.Stdout, "slice: server") || !strings.Contains(r.Stdout, "commit: "+apiSha) || !strings.Contains(r.Stdout, "state: pass") {
		t.Errorf("jig verify --wait in api: exit %d, want the server slice at %s to pass\nstdout:\n%s\nstderr:\n%s", r.Code, apiSha[:12], r.Stdout, r.Stderr)
	}

	// From the orchestrator, in none of the repos, the commit has no repo to be
	// in until the clone is named.
	r = runJig(t, env.with("JIG_SESSION=s-root"), fx.OrchestratorDir, "verify", ticket, right, "--slice", "ui")
	projRefusal(t, "jig verify from the orchestrator", r, 2, "cannot tell which repo of the project the commit is in", "--repo-dir")
	r = runJig(t, env.with("JIG_SESSION=s-root"), fx.OrchestratorDir, "verify", ticket, right, "--slice", "ui", "--repo-dir", webDir, "--wait", "--rerun")
	if r.Code != 0 || !strings.Contains(r.Stdout, "state: pass") {
		t.Errorf("jig verify --repo-dir from the orchestrator: exit %d, want a pass\nstdout:\n%s\nstderr:\n%s", r.Code, r.Stdout, r.Stderr)
	}
}

// TestValidateReadsASliceOracleFromItsOwnRepo: a slice is run with the oracles of
// the repo it builds in, so an oracle name that only the other repo's manifest
// has is a slice written for the wrong repo.
func TestValidateReadsASliceOracleFromItsOwnRepo(t *testing.T) {
	t.Parallel()
	fx, env := newFixture(t, fixture.Opts{Orchestrator: true})
	ticket := fx.Ticket
	// The web's manifest gains an oracle the api's does not have.
	writeFileOrFatal(t, filepath.Join(fx.NestedDirs["web"], ".claude", "jig.yaml"), []byte(
		"workspaces:\n  - id: ui\n    path: ui\n  - id: lib\n    path: lib\noracles:\n  test: go test ./{path}/...\n  lint: go vet ./{path}/...\n"))

	st, err := store.Open(fx.StoreDir)
	if err != nil {
		t.Fatal(err)
	}
	slicesFile := filepath.Join(st.TicketDir(ticket), "slices.yaml")
	body := string(readFileOrFatal(t, slicesFile))

	// The web's lint, asked of the web's slice, is its own; asked of the api's
	// slice, it is the web's.
	web := strings.Replace(body, "workspace: web/ui\n    goal: Show the total as dollars in the web page.\n    oracle: test", "workspace: web/ui\n    goal: Show the total as dollars in the web page.\n    oracle: lint", 1)
	api := strings.Replace(body, "workspace: api/server\n    goal: Add up the prices of an order in the api.\n    oracle: test", "workspace: api/server\n    goal: Add up the prices of an order in the api.\n    oracle: lint", 1)
	if web == body || api == body {
		t.Fatalf("test setup: the slices of the fixture changed:\n%s", body)
	}
	writeFileOrFatal(t, slicesFile, []byte(web))
	if r := runJig(t, env, fx.OrchestratorDir, "validate", ticket, "--check"); r.Code != 0 || !strings.Contains(r.Stdout, "valid: yes") {
		t.Errorf("validate with the web's slice on the web's lint: exit %d, want valid\n%s", r.Code, r.Stdout)
	}
	writeFileOrFatal(t, slicesFile, []byte(api))
	projRefusal(t, "validate with the api's slice on the web's lint", runJig(t, env, fx.OrchestratorDir, "validate", ticket, "--check"), 2,
		`slice server: oracle "lint" is not in the manifest of repo api (repo web declares it)`)
}
