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
// Before the build, the gate and publish turn the ticket away for what it
// lacks and not for being in two repos; during it, a commit made for a slice of
// the other repo is not verified, and jig verify from the orchestrator has to be
// told the clone.
//
// This is the one chain of a ticket in two repos. Which repos a slice names or
// is placed in, and the refusal of a workspace two repos declare, of one none
// declares, of one the repo lacks and of an oracle only the other repo has,
// are cmd/jig's TestValidateWorkspacesOfAProjectOfSeveralRepos and
// internal/ready's TestPlace and TestProjectRepos.
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

	// The gate and publish both cover every repo a ticket builds in, so neither
	// turns away a ticket whose slices build in two for that. Each is turned away
	// for what the ticket lacks: a slice that is not built, and with every slice
	// green the branch (the gate) or the round (publish). A ticket that builds in
	// one repo of the same project is judged as ever by both. The repos a round
	// covers are internal/verifydeliver's (TestGateRepos, TestPublishRepos).
	slices, err := st.ReadSlices(ticket)
	if err != nil {
		t.Fatal(err)
	}
	for _, cwd := range []string{fx.OrchestratorDir, apiDir, webDir} {
		for _, args := range [][]string{{"gate", ticket}, {"publish", ticket}, {"publish", ticket, "--body"}} {
			projRefusal(t, "jig "+strings.Join(args, " ")+" in "+filepath.Base(cwd), runJig(t, env, cwd, args...), 1, "code: GATE_NOT_GREEN")
		}
	}
	setStates(t, st, ticket, map[string]string{"server": "green", "ui": "green"})
	projRefusal(t, "jig gate with every slice green and no branch", runJig(t, env, fx.OrchestratorDir, "gate", ticket), 1, "code: BRANCH_NOT_FOUND", "error: repo api: ")
	projRefusal(t, "jig publish with every slice green and no round", runJig(t, env, fx.OrchestratorDir, "publish", ticket), 1, "no gate rounds recorded for "+ticket)
	setStates(t, st, ticket, map[string]string{"server": "queued", "ui": "queued"})
	if err := st.CreateTicketRecord("JIG-2", store.Ticket{Title: "Api only"}); err != nil {
		t.Fatal(err)
	}
	if err := st.AppendSlices("JIG-2", []store.Slice{{ID: "total", Workspace: "api/server", Goal: "Add up.", Oracle: "test", BlockedBy: []string{}, FromBrief: slices[0].FromBrief}}); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"gate", "JIG-2"}, {"publish", "JIG-2"}} {
		projRefusal(t, "jig "+strings.Join(args, " ")+" for a ticket in one repo", runJig(t, env, fx.OrchestratorDir, args...), 1, "code: GATE_NOT_GREEN")
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

	// A commit is verified in the repo it was made in, for a slice of that repo.
	// One whose trailer names a slice of the other repo is not verified: the hook
	// that sees it starts nothing, and jig verify says why. The sessions that
	// commit here claimed nothing: a session that claimed a slice has a slice to
	// verify a commit for.
	api2, web2 := newHarness(t, fx, env, "s-api-2"), newHarness(t, fx, env, "s-web-2")
	api2.Clone, web2.Clone = apiDir, webDir
	api2.Bash("git status --short")
	web2.Bash("git status --short")
	wrong := commitSlice(t, web2, "server", "ui/wrong.go", "package ui\n\n// Wrong is a change in the web repo made for the api's slice.\nfunc Wrong() {}\n")
	if got := spawnedFor(readLedger(t, env), wrong); got != 0 {
		t.Errorf("%d verifications were started for a commit in web made for the api's slice, want none", got)
	}
	projRefusal(t, "jig verify of a commit for another repo's slice", web2.Jig("verify"), 2,
		"slice server of "+ticket+" builds in repo api, but this commit is in repo web", "Commit it in the clone of api")

	// The same, for the web's slice, is verified: here by the command, which waits
	// for the oracle. The api's session does the same in its own clone.
	right := commitSlice(t, web2, "ui", "ui/right.go", "package ui\n\n// Right is a change in the web repo made for its own slice.\nfunc Right() {}\n")
	if r := web2.Jig("verify", "--wait"); r.Code != 0 || !strings.Contains(r.Stdout, "slice: ui") || !strings.Contains(r.Stdout, "commit: "+right) || !strings.Contains(r.Stdout, "state: pass") {
		t.Errorf("jig verify --wait in web: exit %d, want the ui slice at %s to pass\nstdout:\n%s\nstderr:\n%s", r.Code, right[:12], r.Stdout, r.Stderr)
	}
	apiSha := commitSlice(t, api2, "server", "server/more.go", "package server\n\n// More names a second thing the api serves.\nfunc More() string { return \"more\" }\n")
	if r := api2.Jig("verify", "--wait"); r.Code != 0 || !strings.Contains(r.Stdout, "slice: server") || !strings.Contains(r.Stdout, "commit: "+apiSha) || !strings.Contains(r.Stdout, "state: pass") {
		t.Errorf("jig verify --wait in api: exit %d, want the server slice at %s to pass\nstdout:\n%s\nstderr:\n%s", r.Code, apiSha[:12], r.Stdout, r.Stderr)
	}

	// From the orchestrator, in none of the repos, the commit has no repo to be
	// in until the clone is named.
	projRefusal(t, "jig verify from the orchestrator", runJig(t, env.with("JIG_SESSION=s-root"), fx.OrchestratorDir, "verify", ticket, right, "--slice", "ui"), 2,
		"cannot tell which repo of the project the commit is in", "--repo-dir")
	if r := runJig(t, env.with("JIG_SESSION=s-root"), fx.OrchestratorDir, "verify", ticket, right, "--slice", "ui", "--repo-dir", webDir, "--wait", "--rerun"); r.Code != 0 || !strings.Contains(r.Stdout, "state: pass") {
		t.Errorf("jig verify --repo-dir from the orchestrator: exit %d, want a pass\nstdout:\n%s\nstderr:\n%s", r.Code, r.Stdout, r.Stderr)
	}
	api2.WaitVerifiers()
	web2.WaitVerifiers()

	// status shows both slices green.
	status := runJig(t, env, fx.OrchestratorDir, "status", ticket)
	if status.Code != 0 || !strings.Contains(status.Stdout, "server,green") || !strings.Contains(status.Stdout, "ui,green") {
		t.Errorf("jig status %s: exit %d, want both slices green\n%s", ticket, status.Code, status.Stdout)
	}
	if after := nextJSON[nextRepos](t, env, fx.OrchestratorDir, ticket); after.State != "green" || len(after.Ready) != 0 {
		t.Errorf("jig next after both are built: state %q, ready %+v, want green and nothing ready", after.State, after.Ready)
	}
}
