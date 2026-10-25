package verifydeliver

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/manifest"
	"github.com/develdeco/jig/internal/project"
	"github.com/develdeco/jig/internal/store"
)

// twoRepoFiles is the files of a round over the repos api (workspaces server
// and lib, oracles test and lint) and web (workspaces ui and lib, oracle test),
// read in apiDir and webDir.
func twoRepoFiles(apiDir, webDir string) roundFiles {
	return roundFiles{qualified: true, repos: []roundRepo{
		{Name: "api", Dir: apiDir, Head: "api-head", Man: manifest.Manifest{
			Workspaces: []manifest.Workspace{{ID: "server", Path: "server"}, {ID: "lib", Path: "lib"}},
			Oracles:    map[string]string{"test": "go test ./{path}/...", "lint": "go vet ./{path}/..."},
		}},
		{Name: "web", Dir: webDir, Head: "web-head", Man: manifest.Manifest{
			Workspaces: []manifest.Workspace{{ID: "ui", Path: "ui"}, {ID: "lib", Path: "lib"}},
			Oracles:    map[string]string{"test": "go test ./{path}/..."},
		}},
	}}
}

// TestRoundFilesNameAFileByItsRepo: in a round over several repos a file is
// "<repo>/<path>", and the workspace, the oracles and the existence of a file
// are those of its repo; in a round over one repo a file is a path in the repo.
func TestRoundFilesNameAFileByItsRepo(t *testing.T) {
	t.Parallel()
	rf := twoRepoFiles("", "")

	for _, c := range []struct {
		file, repo, rel string
		ok              bool
	}{
		{"web/ui/page.go", "web", "ui/page.go", true},
		{"api/server/handler.go", "api", "server/handler.go", true},
		{"ui/page.go", "", "ui/page.go", false}, // a path of no repo
		{"docs/page.go", "", "docs/page.go", false},
		{"web", "", "web", false},
		{"web/", "", "web/", false},
	} {
		repo, rel, ok := rf.of(c.file)
		if ok != c.ok || (ok && (repo.Name != c.repo || rel != c.rel)) {
			t.Errorf("of(%q) = %q, %q, %v; want %q, %q, %v", c.file, repo.Name, rel, ok, c.repo, c.rel, c.ok)
		}
	}
	if got := rf.qualify("web", "ui/page.go"); got != "web/ui/page.go" {
		t.Errorf("qualify = %q", got)
	}
	if got := rf.qualifyAll("api", []string{"a.go", "b.go"}); !reflect.DeepEqual(got, []string{"api/a.go", "api/b.go"}) {
		t.Errorf("qualifyAll = %v", got)
	}

	// A workspace is named as a slice names it: the repo in front.
	for file, want := range map[string]string{
		"web/ui/page.go":   "web/ui",
		"api/lib/lib.go":   "api/lib",
		"web/lib/lib.go":   "web/lib",
		"web/docs/read.md": "", // in no workspace of web
		"ui/page.go":       "", // in no repo
	} {
		if got := rf.workspace(file); got != want {
			t.Errorf("workspace(%q) = %q, want %q", file, got, want)
		}
	}
	// The oracles that can prove a fix are those of the file's repo.
	if got := rf.oracles("api/server/handler.go"); !reflect.DeepEqual(got, []string{"lint", "test"}) {
		t.Errorf("oracles of an api file = %v, want lint and test", got)
	}
	if got := rf.oracles("web/ui/page.go"); !reflect.DeepEqual(got, []string{"test"}) {
		t.Errorf("oracles of a web file = %v, want test alone", got)
	}
	if got := rf.oracles("ui/page.go"); got != nil {
		t.Errorf("oracles of a file of no repo = %v, want none", got)
	}
	if got := rf.allOracles(); !reflect.DeepEqual(got, []string{"lint", "test"}) {
		t.Errorf("allOracles = %v, want lint and test, once each", got)
	}
	if got := rf.byRepo([]string{"web/ui/a.go", "api/server/b.go", "web/ui/c.go", "ui/stray.go"}); !reflect.DeepEqual(got, map[string][]string{"web": {"ui/a.go", "ui/c.go"}, "api": {"server/b.go"}}) {
		t.Errorf("byRepo = %v", got)
	}

	// A round over one repo reads a file as the repo's own path, whatever its
	// first directory is called.
	one := oneRepoFiles(manifest.Manifest{
		Workspaces: []manifest.Workspace{{ID: "web", Path: "web"}},
		Oracles:    map[string]string{"test": "go test ./..."},
	})
	if repo, rel, ok := one.of("web/ui/page.go"); !ok || rel != "web/ui/page.go" || repo.Name != "" {
		t.Errorf("one repo: of(web/ui/page.go) = %q, %q, %v, want the path as it is", repo.Name, rel, ok)
	}
	if got := one.workspace("web/ui/page.go"); got != "web" {
		t.Errorf("one repo: workspace = %q, want the bare id web", got)
	}
	if got := one.qualify("x", "a.go"); got != "a.go" {
		t.Errorf("one repo: qualify = %q, want a.go", got)
	}
}

// TestRoundFilesReadReviewedPathsAgainstEveryLease: a reviewed path an agent
// reports absolute, inside the lease of one repo, is that repo's file; a
// relative one is already named by its repo; one outside every lease is left out.
func TestRoundFilesReadReviewedPathsAgainstEveryLease(t *testing.T) {
	t.Parallel()
	apiDir, webDir, elsewhere := t.TempDir(), t.TempDir(), t.TempDir()
	rf := twoRepoFiles(apiDir, webDir)

	got := rf.normalizeReviewed([]string{
		filepath.Join(webDir, "ui", "page.go"),
		"api/server/handler.go",
		"./api/server/handler.go", // the same file, spelled twice
		filepath.Join(elsewhere, "x.go"),
		"../escape.go",
	})
	if want := []string{"web/ui/page.go", "api/server/handler.go"}; !reflect.DeepEqual(got, want) {
		t.Errorf("normalizeReviewed = %v, want %v", got, want)
	}
}

// TestGateRepos: the repos a gate round covers are those the ticket's slices
// build in, in the project's order, and no other. A project of one is covered
// by its repo; a ticket in one repo of several, which is not the first, is
// covered by that repo alone; a ticket that builds in none is told so.
func TestGateRepos(t *testing.T) {
	t.Parallel()
	slice := func(id, workspace string) store.Slice { return store.Slice{ID: id, Workspace: workspace} }
	one := project.Config{Repos: []project.Repo{{Label: "app"}}}
	three := project.Config{Repos: []project.Repo{{Label: "api"}, {Label: "web"}, {Label: "docs"}}}

	names := func(repos []*gateRepo) []string {
		var out []string
		for _, r := range repos {
			out = append(out, r.name)
		}
		return out
	}
	for _, c := range []struct {
		name     string
		cfg      project.Config
		slices   []store.Slice
		want     []string
		slicesOf map[string][]string
	}{
		{"a project of one, whatever the slices say", one, []store.Slice{slice("a", "alpha"), slice("b", "beta")}, []string{"app"}, map[string][]string{"app": {"a", "b"}}},
		{"a project of one with no slice", one, nil, []string{"app"}, nil},
		{"two repos of three", three, []store.Slice{slice("d", "docs/site"), slice("s", "api/server"), slice("t", "api/lib")}, []string{"api", "docs"}, map[string][]string{"api": {"s", "t"}, "docs": {"d"}}},
		{"the one repo that is not the first", three, []store.Slice{slice("u", "web/ui"), slice("v", "web/lib")}, []string{"web"}, map[string][]string{"web": {"u", "v"}}},
		{"a fix slice names its repo like any slice", three, []store.Slice{slice("u", "web/ui"), {ID: "fix-1-api-server-test", Workspace: "api/server", FromGate: 1}}, []string{"api", "web"}, nil},
	} {
		repos, err := gateRepos(Deps{Cfg: c.cfg}, "JIG-1", c.slices)
		if err != nil {
			t.Errorf("%s: %v", c.name, err)
			continue
		}
		if got := names(repos); !slices.Equal(got, c.want) {
			t.Errorf("%s: covers %v, want %v", c.name, got, c.want)
		}
		for _, r := range repos {
			var ids []string
			for _, s := range r.slices {
				ids = append(ids, s.ID)
			}
			if want, ok := c.slicesOf[r.name]; ok && !slices.Equal(ids, want) {
				t.Errorf("%s: slices of %s = %v, want %v", c.name, r.name, ids, want)
			}
			if r.target != "main" {
				t.Errorf("%s: target of %s = %q, want main", c.name, r.name, r.target)
			}
		}
	}

	// Nothing places the slices: a bare workspace nobody declares, or none.
	for name, unplaced := range map[string][]store.Slice{
		"a workspace no repo declares": {slice("x", "stray")},
		"no slice":                     nil,
	} {
		_, err := gateRepos(Deps{Cfg: three}, "JIG-1", unplaced)
		var ae *axi.Error
		if !errors.As(err, &ae) || ae.Code != "VALIDATION_ERROR" || !strings.Contains(ae.Msg, "none of JIG-1's slices builds in a repo of the project") {
			t.Errorf("%s: err = %v, want VALIDATION_ERROR saying no slice builds in a repo", name, err)
		}
	}

	// A bare workspace is placed by the manifests of the clones.
	api, web := t.TempDir(), t.TempDir()
	for dir, body := range map[string]string{
		api: "workspaces:\n  - id: server\n    path: server\n  - id: lib\n    path: lib\noracles:\n  test: go test ./{path}/...\n",
		web: "workspaces:\n  - id: ui\n    path: ui\n  - id: lib\n    path: lib\noracles:\n  test: go test ./{path}/...\n",
	} {
		if err := os.MkdirAll(filepath.Join(dir, ".claude"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, ".claude", "jig.yaml"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	d := Deps{
		Cfg:     project.Config{Repos: []project.Repo{{Label: "api"}, {Label: "web"}}},
		Machine: project.MachineProject{Clones: map[string]string{"api": api, "web": web}},
	}
	repos, err := gateRepos(d, "JIG-1", []store.Slice{slice("u", "ui"), slice("l", "lib")})
	if err != nil || !slices.Equal(names(repos), []string{"web"}) || len(repos[0].slices) != 1 {
		t.Errorf("bare ui (web's) and lib (both repos'): covers %v (%v), want web alone, with the ui slice: lib belongs to no repo", names(repos), err)
	}
}

// TestBuiltInTheCommitsOfTheSlicesOfARepo: the commits jig built in one repo are
// the verified lines of the slices that build there, once each, in journal order.
func TestBuiltInTheCommitsOfTheSlicesOfARepo(t *testing.T) {
	t.Parallel()
	lines := []journal.Line{
		{Event: journal.EventVerified, Slice: "server", Commit: "a1"},
		{Event: journal.EventVerified, Slice: "ui", Commit: "b1"},
		{Event: journal.EventOracle, Slice: "ui", Commit: "b2"},
		{Event: journal.EventVerified, Slice: "ui", Commit: "b3"},
		{Event: journal.EventVerified, Slice: "server", Commit: "a1"},
		{Event: journal.EventVerified, Slice: "gone", Commit: "c1"},
		{Event: journal.EventVerified, Slice: "ui"},
	}
	web := []store.Slice{{ID: "ui"}}
	if got := builtIn(lines, web); !slices.Equal(got, []string{"b1", "b3"}) {
		t.Errorf("builtIn(web) = %v, want b1 and b3: the verified commits of ui", got)
	}
	if got := builtIn(lines, []store.Slice{{ID: "server"}}); !slices.Equal(got, []string{"a1"}) {
		t.Errorf("builtIn(api) = %v, want a1 once", got)
	}
	if got := builtIn(nil, web); got != nil {
		t.Errorf("builtIn of no journal = %v, want none", got)
	}
}

// TestReviewRequestOfOneRepoMatchesItsGolden: the request of a round in a
// project of one repo is what it has always been, byte for byte: its scope,
// base, head and worktree at the top, and no list of repos.
func TestReviewRequestOfOneRepoMatchesItsGolden(t *testing.T) {
	t.Parallel()
	req := ReviewRequest{
		Ticket: "JIG-1", Round: 2, Scope: "delta", BaseSHA: "aaaa", HeadSHA: "bbbb", Worktree: "/lease/JIG-1-gate",
		Intent:        Intent{Source: "brief", Path: "/store/JIG-1/brief.md"},
		IntentExcerpt: "/home/excerpt.md",
		Recordings:    "/home/recordings.json",
		SlicesPath:    "/store/JIG-1/slices.yaml",
		JournalPath:   "/store/JIG-1/journal.ndjson",
		Oracles:       []string{"lint", "test"},
		OraclesPassed: []OracleRun{{Oracle: "test", Workspace: "alpha", Command: "go test ./alpha/...", ReusedFrom: "cccc"}},
		Open:          []OpenFinding{{ID: "r1-f1", File: "alpha/a.go", Line: 3, Title: "t", Detail: "d", Action: "fix", Risk: "high", RiskRationale: "r", Oracle: "test", Recurrences: 1}},
		MustReview:    []string{"alpha/a.go", "beta/b.go"},
	}
	data, err := MarshalReviewRequest(req)
	if err != nil {
		t.Fatal(err)
	}
	const golden = `{
  "ticket": "JIG-1",
  "round": 2,
  "scope": "delta",
  "base_sha": "aaaa",
  "head_sha": "bbbb",
  "worktree": "/lease/JIG-1-gate",
  "intent": {
    "source": "brief",
    "path": "/store/JIG-1/brief.md"
  },
  "intent_excerpt": "/home/excerpt.md",
  "recordings": "/home/recordings.json",
  "slices_path": "/store/JIG-1/slices.yaml",
  "journal_path": "/store/JIG-1/journal.ndjson",
  "oracles": [
    "lint",
    "test"
  ],
  "oracles_passed": [
    {
      "oracle": "test",
      "workspace": "alpha",
      "command": "go test ./alpha/...",
      "reused_from": "cccc"
    }
  ],
  "open": [
    {
      "id": "r1-f1",
      "file": "alpha/a.go",
      "line": 3,
      "title": "t",
      "detail": "d",
      "action": "fix",
      "risk": "high",
      "risk_rationale": "r",
      "oracle": "test",
      "recurrences": 1
    }
  ],
  "dismissed": [],
  "must_review": [
    "alpha/a.go",
    "beta/b.go"
  ]
}`
	if string(data) != golden {
		t.Errorf("the request of a round over one repo changed:\n%s\nwant:\n%s", data, golden)
	}
}

// TestReviewRequestOfSeveralReposListsThem: a round in a project of several
// repos lists each repo with its own worktree, branch, scope, base, head,
// oracles and files to cover, and names no scope, base, head or worktree at the
// top; a repo with nothing to cover still lists its files as [].
func TestReviewRequestOfSeveralReposListsThem(t *testing.T) {
	t.Parallel()
	req := ReviewRequest{
		Ticket: "JIG-1", Round: 1,
		Repos: []RepoRequest{
			{Repo: "api", Worktree: "/lease/api", Branch: "jig/JIG-1", Scope: "full", BaseSHA: "a0", HeadSHA: "a1", Oracles: []string{"test"}, MustReview: []string{"api/server/handler.go"}},
			{Repo: "web", Branch: "jig/JIG-1", Scope: "delta", BaseSHA: "w1", HeadSHA: "w1", Oracles: []string{"test"}},
		},
		MustReview: []string{"api/server/handler.go"},
	}
	data, err := MarshalReviewRequest(req)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"scope", "base_sha", "head_sha", "worktree"} {
		if _, ok := raw[key]; ok {
			t.Errorf("the request names %q at the top, want it only in each repo:\n%s", key, data)
		}
	}
	var back ReviewRequest
	if err := json.Unmarshal(data, &back); err != nil {
		t.Fatal(err)
	}
	if len(back.Repos) != 2 || back.Repos[0].Worktree != "/lease/api" || back.Repos[1].Repo != "web" {
		t.Fatalf("repos = %+v, want api and web", back.Repos)
	}
	var listed struct {
		Repos []map[string]json.RawMessage `json:"repos"`
	}
	if err := json.Unmarshal(data, &listed); err != nil {
		t.Fatal(err)
	}
	if got := strings.TrimSpace(string(listed.Repos[1]["must_review"])); got != "[]" {
		t.Errorf("a repo with nothing to cover marshals must_review as %s, want []", got)
	}
	if got := back.repo("web"); got.HeadSHA != "w1" {
		t.Errorf("repo(web) = %+v", got)
	}
	if got := back.repo("docs"); got.Repo != "" {
		t.Errorf("repo(docs) = %+v, want the zero entry", got)
	}
}

// TestValidateReviewResultInARoundOverSeveralRepos: a finding names its file by its
// repo; a file of no repo, a repo's file the head lacks, an oracle only another
// repo declares and a must_review file the report did not read are each refused.
func TestValidateReviewResultInARoundOverSeveralRepos(t *testing.T) {
	t.Parallel()
	apiDir, webDir := t.TempDir(), t.TempDir()
	rf := twoRepoFiles(apiDir, webDir)
	req := ReviewRequest{
		Ticket: "JIG-1", Round: 1,
		Repos: []RepoRequest{
			{Repo: "api", Oracles: []string{"lint", "test"}, MustReview: []string{"api/server/handler.go"}},
			{Repo: "web", Oracles: []string{"test"}, MustReview: []string{"web/ui/page.go"}},
		},
		MustReview: []string{"api/server/handler.go", "web/ui/page.go"},
		Open:       []OpenFinding{{ID: "r1-f1", File: "web/ui/old.go"}},
	}
	present := func(file string) (bool, error) { return file != "web/ui/missing.go", nil }
	noDeleted := map[string]bool{}
	finding := func(file, oracle string) ResultFinding {
		return ResultFinding{File: file, Line: 1, Title: "t", Action: ActionFix, Risk: RiskHigh, RiskRationale: "r", Oracle: oracle}
	}
	read := []string{"api/server/handler.go", filepath.Join(webDir, "ui", "page.go")} // the second as an agent reports it: absolute
	valid := func(findings ...ResultFinding) ReviewResult {
		return ReviewResult{Findings: findings, StillPresent: []StillPresentEntry{}, ReviewedPaths: read}
	}

	if err := validateReviewResult(req, valid(finding("web/ui/page.go", "test"), finding("api/server/handler.go", "lint")), rf, present, noDeleted); err != nil {
		t.Errorf("a valid report over both repos: %v", err)
	}
	for name, c := range map[string]struct {
		result ReviewResult
		want   string
	}{
		"a path of no repo":           {valid(finding("ui/page.go", "test")), `file "ui/page.go" names no repo of this round (api, web): write it as <repo>/<path>`},
		"a repo the round lacks":      {valid(finding("docs/x.md", "test")), `names no repo of this round`},
		"a file the head lacks":       {valid(finding("web/ui/missing.go", "test")), `neither present at head nor deleted`},
		"an oracle of another repo":   {valid(finding("web/ui/page.go", "lint")), `oracle "lint" is not a manifest oracle`},
		"no oracle where two exist":   {valid(finding("api/server/handler.go", "")), `must name one of the manifest's 2 oracles`},
		"a must_review file not read": {ReviewResult{Findings: []ResultFinding{}, StillPresent: []StillPresentEntry{}, ReviewedPaths: read[:1]}, `missing must_review path "web/ui/page.go"`},
	} {
		err := validateReviewResult(req, c.result, rf, present, noDeleted)
		var ae *axi.Error
		if !errors.As(err, &ae) || ae.Code != "REVIEW_INVALID" || !strings.Contains(ae.Msg, c.want) {
			t.Errorf("%s: err = %v, want REVIEW_INVALID with %q", name, err, c.want)
		}
	}
	// web has one oracle, so a finding there needs to name none.
	if err := validateReviewResult(req, valid(finding("web/ui/page.go", "")), rf, present, noDeleted); err != nil {
		t.Errorf("a web finding with no oracle (web declares one): %v", err)
	}
	// A still_present entry's file is judged by its repo too.
	still := ReviewResult{Findings: []ResultFinding{}, StillPresent: []StillPresentEntry{{Prior: "r1-f1", Line: 2}}, ReviewedPaths: read}
	if err := validateReviewResult(req, still, rf, present, noDeleted); err != nil {
		t.Errorf("a still_present entry for a web file: %v", err)
	}
}

// TestApplyRoundOverSeveralReposKeepsAFindingInItsRepo: a finding in web/ui/page.go
// is in the workspace web/ui and takes the oracle of web's manifest; one in
// api/lib is in api/lib. A finding in a repo with no oracle cannot be routed, and
// the refusal names the repo; the fix slice of a kept finding builds in its
// repo's workspace.
func TestApplyRoundOverSeveralReposKeepsAFindingInItsRepo(t *testing.T) {
	t.Parallel()
	rf := twoRepoFiles("", "")
	finding := func(file, oracle string) ResultFinding {
		return ResultFinding{File: file, Line: 4, Title: "t " + file, Detail: "d", Action: ActionFix, Risk: RiskHigh, RiskRationale: "r", Oracle: oracle}
	}
	result := ReviewResult{Findings: []ResultFinding{finding("web/ui/page.go", ""), finding("api/lib/lib.go", "lint"), finding("web/docs/read.md", "")}}
	reported, err := applyRound(1, map[string]Finding{}, result, nil, alwaysGreen, rf, defaultTestFixRisks(), false)
	if err != nil {
		t.Fatal(err)
	}
	byFile := map[string]Finding{}
	for _, f := range reported {
		byFile[f.File] = f
	}
	if f := byFile["web/ui/page.go"]; f.Workspace != "web/ui" || f.Oracle != "test" || f.Status != StatusOpen {
		t.Errorf("finding in web/ui/page.go = %+v, want workspace web/ui, the one oracle test of web, open", f)
	}
	if f := byFile["api/lib/lib.go"]; f.Workspace != "api/lib" || f.Oracle != "lint" || f.Status != StatusOpen {
		t.Errorf("finding in api/lib/lib.go = %+v, want workspace api/lib, oracle lint, open", f)
	}
	if f := byFile["web/docs/read.md"]; f.Workspace != "" || f.Status != StatusAsked || f.RoutedWhy != RoutedWhyBuildTarget {
		t.Errorf("finding in web/docs/read.md = %+v, want asked: it lies in no workspace of web", f)
	}

	// The fix slices of the two kept findings build in the repo of each.
	st := newReviewStore(t)
	kept := []Finding{byFile["web/ui/page.go"], byFile["api/lib/lib.go"]}
	routed, slices, err := routeRound(1, st, "JIG-1", nil, kept, nil, nil, rf, 0, 0, 5)
	if err != nil || len(routed) != 2 {
		t.Fatalf("routeRoundIn: %v, %d routed", err, len(routed))
	}
	got := map[string]string{}
	for _, s := range slices {
		got[s.ID] = s.Workspace + "/" + s.Oracle
	}
	if want := (map[string]string{"fix-1-api-lib-lint": "api/lib/lint", "fix-1-web-ui-test": "web/ui/test"}); !reflect.DeepEqual(got, want) {
		t.Errorf("fix slices = %v, want %v: one for each repo, in its workspace, with its oracle", got, want)
	}

	// A repo whose manifest declares no oracle can prove no fix.
	noOracle := twoRepoFiles("", "")
	noOracle.repos[1].Man.Oracles = nil
	_, _, err = routeRound(1, st, "JIG-1", nil, kept[:1], nil, nil, noOracle, 0, 0, 5)
	var ae *axi.Error
	if !errors.As(err, &ae) || ae.Code != "GATE_NO_ORACLE" || !strings.Contains(ae.Msg, "the manifest of repo web has no oracles") {
		t.Errorf("a finding in a repo with no oracle: err = %v, want GATE_NO_ORACLE naming web", err)
	}
	// The same refusal of a round over one repo does not name one.
	_, _, err = routeRound(1, st, "JIG-1", nil, kept[:1], nil, nil, oneRepoFiles(manifest.Manifest{Workspaces: []manifest.Workspace{{ID: "root", Path: "."}}}), 0, 0, 5)
	if !errors.As(err, &ae) || ae.Msg != "cannot build a fix slice, the manifest has no oracles" {
		t.Errorf("a round over one repo with no oracle: err = %v", err)
	}
}

// TestDefaultTriageJudgesAFindingByItsRepo: the build target of an ask in a round
// over several repos is read from the manifest of the repo its file begins with.
func TestDefaultTriageJudgesAFindingByItsRepo(t *testing.T) {
	t.Parallel()
	rf := twoRepoFiles("", "")
	repos := map[string]manifest.Manifest{"api": rf.repos[0].Man, "web": rf.repos[1].Man}
	in := TriageInput{
		Repos: repos,
		Asks: []Finding{
			{ID: "r1-f1", File: "web/ui/page.go", Workspace: "web/ui", Oracle: "test"},
			{ID: "r1-f2", File: "api/server/handler.go", Workspace: "api/server", Oracle: ""}, // api has two oracles, none named
			{ID: "r1-f3", File: "api/server/handler.go", Workspace: "api/server", Oracle: "lint"},
		},
	}
	out := DefaultTriage(in)
	if d, ok := out.Asks["r1-f1"]; !ok || !d.Keep {
		t.Errorf("Asks[r1-f1] = %+v, %v, want kept: web's one oracle resolves", d, ok)
	}
	if _, ok := out.Asks["r1-f2"]; ok {
		t.Error("Asks[r1-f2] decided, want undecided: api has two oracles and the finding names none")
	}
	if d, ok := out.Asks["r1-f3"]; !ok || !d.Keep {
		t.Errorf("Asks[r1-f3] = %+v, %v, want kept: lint is one of api's oracles", d, ok)
	}
}

// TestOpenRoundFileKeepsItsTopForOneRepoAndListsSeveral: the open record of a
// round in a project of one repo has that repo's settings at the top, as it
// always had, and no list of repos; the record of a round in a project of
// several lists them and leaves the top out.
func TestOpenRoundFileKeepsItsTopForOneRepoAndListsSeveral(t *testing.T) {
	t.Parallel()
	intent := reportIntentYAML{Source: "brief", SHA256: "x"}
	one := openRoundFile{
		Round: 1, Ticket: "JIG-1", Repo: "app", Branch: "jig/JIG-1", Copy: "origin's", Head: "h1", Base: "b1", Scope: "full",
		Target: "main", TargetSHA: "t1", Lease: "pool/app/JIG-1-gate", Intent: intent, Opened: "2026-01-01T00:00:00Z",
	}
	data, err := yaml.Marshal(one)
	if err != nil {
		t.Fatal(err)
	}
	const golden = "round: 1\nticket: JIG-1\nrepo: app\nbranch: jig/JIG-1\ncopy: origin's\nhead: h1\nbase: b1\nscope: full\n" +
		"target: main\ntarget_sha: t1\nlease: pool/app/JIG-1-gate\nintent:\n    source: brief\n    sha256: x\nopened: \"2026-01-01T00:00:00Z\"\n"
	if string(data) != golden {
		t.Errorf("the open record of a round over one repo changed:\n%s\nwant:\n%s", data, golden)
	}
	if got := one.heads(); !reflect.DeepEqual(got, map[string]string{"app": "h1"}) {
		t.Errorf("heads of one repo = %v", got)
	}

	several := openRoundFile{
		Round: 1, Ticket: "JIG-1", Branch: "jig/JIG-1", Intent: intent, Opened: "2026-01-01T00:00:00Z",
		Repos: []openRepoFile{
			{Repo: "api", Copy: "the clone's", Head: "a1", Base: "a0", Scope: "full", Target: "main", TargetSHA: "ta", Lease: "pool/api/JIG-1-gate"},
			{Repo: "web", Copy: "origin's", Head: "w1", Base: "w0", Scope: "delta", Target: "main", TargetSHA: "tw", Lease: "pool/web/JIG-1-gate"},
		},
	}
	data, err = yaml.Marshal(several)
	if err != nil {
		t.Fatal(err)
	}
	var raw map[string]any
	if err := yaml.Unmarshal(data, &raw); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"repo", "copy", "head", "base", "scope", "target", "target_sha", "lease"} {
		if _, ok := raw[key]; ok {
			t.Errorf("the open record of a round over several repos has %q at the top:\n%s", key, data)
		}
	}
	var back openRoundFile
	if err := yaml.Unmarshal(data, &back); err != nil || !reflect.DeepEqual(back, several) {
		t.Errorf("the record does not read back as written: %v\n%+v", err, back)
	}
	if got := several.heads(); !reflect.DeepEqual(got, map[string]string{"api": "a1", "web": "w1"}) {
		t.Errorf("heads of two repos = %v", got)
	}
}

// oneRepoIn is the files of a round over one repo read in leaseDir, whose
// manifest declares the oracles named.
func oneRepoIn(leaseDir string, oracleNames []string) roundFiles {
	return roundFiles{repos: []roundRepo{{Dir: leaseDir, Man: oraclesManifest(oracleNames)}}}
}
