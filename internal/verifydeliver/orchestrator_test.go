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
