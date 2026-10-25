package verifydeliver

import (
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/manifest"
	"github.com/develdeco/jig/internal/project"
	"github.com/develdeco/jig/internal/store"
)

// TestPublishRepos: a publish covers the repos the gate reviewed, or the one
// --repo names; a name that is no repo of the project, or that the ticket does
// not build in, is refused and says which repos there are.
func TestPublishRepos(t *testing.T) {
	t.Parallel()
	cfg := project.Config{Repos: []project.Repo{{Label: "api"}, {Label: "web"}, {Label: "docs"}}}
	slice := func(id, workspace string) store.Slice { return store.Slice{ID: id, Workspace: workspace} }
	covered, err := gateRepos(Deps{Cfg: cfg}, "JIG-1", []store.Slice{slice("s", "api/server"), slice("u", "web/ui")})
	if err != nil {
		t.Fatal(err)
	}
	names := func(repos []*gateRepo) []string {
		var out []string
		for _, r := range repos {
			out = append(out, r.name)
		}
		return out
	}

	all, err := publishRepos(cfg, "JIG-1", covered, "")
	if err != nil || !slices.Equal(names(all), []string{"api", "web"}) {
		t.Errorf("publishRepos with no name = %v (%v), want every repo the ticket builds in", names(all), err)
	}
	one, err := publishRepos(cfg, "JIG-1", covered, "web")
	if err != nil || !slices.Equal(names(one), []string{"web"}) || one[0] != covered[1] {
		t.Errorf("publishRepos(web) = %v (%v), want the web's covered repo alone", names(one), err)
	}

	for _, c := range []struct{ name, want string }{
		{"docs", "JIG-1 builds in api and web, not in docs"},
		{"shop", `the project has no repo "shop" (it has api, web, docs)`},
	} {
		_, err := publishRepos(cfg, "JIG-1", covered, c.name)
		var ae *axi.Error
		if !errors.As(err, &ae) || ae.Code != "VALIDATION_ERROR" || ae.Msg != c.want {
			t.Errorf("publishRepos(%s) = %v, want VALIDATION_ERROR %q", c.name, err, c.want)
		}
	}
}

func prLine(repo, url string) journal.Line {
	return journal.Line{Event: journal.EventPr, Repo: repo, URL: url}
}

// TestRecordedPRsAndTheLinksOfARepo: the pull requests a journal holds are the
// latest of each repo that has a url, and a repo's links are the other repos of
// the ticket, with "pending" for those with none.
func TestRecordedPRsAndTheLinksOfARepo(t *testing.T) {
	t.Parallel()
	lines := []journal.Line{
		prLine("api", "https://h/api/pull/1"),
		prLine("web", ""), // none opened: a repo with no host
		prLine("api", "https://h/api/pull/2"),
		{Event: journal.EventPrBody, Repo: "web"},
	}
	known := recordedPRs(lines)
	if want := map[string]string{"api": "https://h/api/pull/2"}; !reflect.DeepEqual(known, want) {
		t.Errorf("recordedPRs = %v, want the latest url of each repo that has one: %v", known, want)
	}
	if got := recordedPRs([]journal.Line{prLine("", "https://h/pull/3")}); got[""] != "https://h/pull/3" {
		t.Errorf("recordedPRs of a line naming no repo = %v, want it under the empty name", got)
	}

	pt := &publishTarget{repoName: "web", covered: []string{"api", "web", "docs"}}
	got := linksFor(pt, known)
	if want := []prLink{{"api", "https://h/api/pull/2"}, {"docs", ""}}; !reflect.DeepEqual(got, want) {
		t.Errorf("linksFor(web) = %v, want the api's url and docs pending: %v", got, want)
	}
	if got := linksFor(&publishTarget{repoName: "web", covered: []string{"web"}}, known); len(got) != 0 {
		t.Errorf("linksFor of a ticket in one repo = %v, want none", got)
	}
	if !sameLinks(nil, nil) || !sameLinks(got, got) || sameLinks([]prLink{{"api", ""}}, []prLink{{"api", "u"}}) || sameLinks([]prLink{{"api", ""}}, nil) {
		t.Errorf("sameLinks does not tell the same pull requests from other ones")
	}
}

// TestPendingRepos: a publish is done when every repo the ticket builds in has a
// pull request recorded since the last publish-done; a ticket in one repo is done
// with the first, whatever repo it names.
func TestPendingRepos(t *testing.T) {
	t.Parallel()
	done := journal.Line{Event: journal.EventPublishDone}
	covered := []string{"api", "web"}
	for _, c := range []struct {
		name    string
		lines   []journal.Line
		covered []string
		want    []string
	}{
		{"nothing recorded", nil, covered, []string{"api", "web"}},
		{"the first recorded", []journal.Line{prLine("api", "u")}, covered, []string{"web"}},
		{"the second recorded", []journal.Line{prLine("web", "u")}, covered, []string{"api"}},
		{"both recorded", []journal.Line{prLine("api", "u"), prLine("web", "u")}, covered, nil},
		{"a repo with no host counts", []journal.Line{prLine("api", ""), prLine("web", "")}, covered, nil},
		{"both recorded before the last publish-done", []journal.Line{prLine("api", "u"), prLine("web", "u"), done, prLine("api", "u2")}, covered, []string{"web"}},
		{"a ticket in one repo", nil, []string{"web"}, []string{"web"}},
		{"a ticket in one repo, its pr recorded", []journal.Line{prLine("web", "u")}, []string{"web"}, nil},
		{"a ticket in one repo, a pr naming no repo", []journal.Line{prLine("", "u")}, []string{"web"}, nil},
	} {
		if got := pendingRepos(c.lines, c.covered); !slices.Equal(got, c.want) {
			t.Errorf("%s: pending = %v, want %v", c.name, got, c.want)
		}
	}
}

// TestUnionNames: the oracle names of every repo's pull request, sorted and
// once each.
func TestUnionNames(t *testing.T) {
	t.Parallel()
	got := unionNames([]string{"test", "lint"}, nil, []string{"test", "e2e"})
	if want := []string{"e2e", "lint", "test"}; !slices.Equal(got, want) {
		t.Errorf("unionNames = %v, want %v", got, want)
	}
}

// TestPartOfSection: the section of a pull request that names the ticket's other
// repos, with the url of each one's pull request or pending; a ticket in one
// repo has none.
func TestPartOfSection(t *testing.T) {
	t.Parallel()
	if got := renderPartOfSection("JIG-1", nil); got != "" {
		t.Errorf("a ticket in one repo has a Part of section: %q", got)
	}
	got := renderPartOfSection("JIG-1", []prLink{{"api", "https://h/api/pull/2"}, {"docs", ""}})
	want := "## Part of JIG-1\n\n- also api: https://h/api/pull/2\n- also docs: pending\n\n"
	if got != want {
		t.Errorf("renderPartOfSection = %q, want %q", got, want)
	}
}

// TestReviewNotesFileOfARepo: a ticket that publishes in several repos has
// review notes for each, since each pull request carries its own.
func TestReviewNotesFileOfARepo(t *testing.T) {
	t.Parallel()
	if got := reviewNotesFile("web", false); got != "review-notes.md" {
		t.Errorf("reviewNotesFile of a ticket in one repo = %q, want review-notes.md", got)
	}
	if got := reviewNotesFile("web", true); got != "review-notes-web.md" {
		t.Errorf("reviewNotesFile of a ticket in several repos = %q, want review-notes-web.md", got)
	}
}

// TestPublishTargetShowsItsOwnRepo: the findings, the files read, the reviewed
// head and the oracles of a pull request are those of its repo when the ticket
// publishes in several, and everything when it publishes in one.
func TestPublishTargetShowsItsOwnRepo(t *testing.T) {
	t.Parallel()
	finding := func(id, file string) findingOutcome { return findingOutcome{finding: Finding{ID: id, File: file}} }
	outcomes := []findingOutcome{finding("r1-f1", "api/server/handler.go"), finding("r1-f2", "web/ui/page.go"), finding("r1-f3", "stray.go")}
	reviewed := []string{"api/server/handler.go", "web/ui/page.go"}
	ids := func(os []findingOutcome) []string {
		var out []string
		for _, o := range os {
			out = append(out, o.finding.ID)
		}
		return out
	}

	api := &publishTarget{repoName: "api", covered: []string{"api", "web"}, qualified: true}
	web := &publishTarget{repoName: "web", covered: []string{"api", "web"}, qualified: true}
	gotAPI, pathsAPI := api.ownFindings(outcomes, reviewed)
	gotWeb, pathsWeb := web.ownFindings(outcomes, reviewed)
	if !slices.Equal(ids(gotAPI), []string{"r1-f1", "r1-f3"}) || !slices.Equal(pathsAPI, []string{"server/handler.go"}) {
		t.Errorf("the api's findings %v, paths %v, want r1-f1 and the stray r1-f3 (a bare path goes to the first repo), server/handler.go", ids(gotAPI), pathsAPI)
	}
	if !slices.Equal(ids(gotWeb), []string{"r1-f2"}) || !slices.Equal(pathsWeb, []string{"ui/page.go"}) {
		t.Errorf("the web's findings %v, paths %v, want r1-f2 and ui/page.go", ids(gotWeb), pathsWeb)
	}
	alone := &publishTarget{repoName: "app", covered: []string{"app"}}
	if got, paths := alone.ownFindings(outcomes, reviewed); len(got) != 3 || !slices.Equal(paths, reviewed) {
		t.Errorf("a project of one repo: findings %v, paths %v, want all of them as they are", ids(got), paths)
	}

	rep := reportYAML{ReviewedSHA: map[string]string{"api": "aaa", "web": "bbb"}, OracleNames: []string{"lint", "test"}}
	web.gateRep, alone.gateRep = rep, rep
	if got := web.report().ReviewedSHA; !reflect.DeepEqual(got, map[string]string{"web": "bbb"}) {
		t.Errorf("the web's report shows reviewed heads %v, want its own alone", got)
	}
	if got := web.reviewedHead(); got != "bbb" {
		t.Errorf("the web's reviewed head = %q, want bbb", got)
	}
	if got := alone.report().ReviewedSHA; !reflect.DeepEqual(got, rep.ReviewedSHA) {
		t.Errorf("a ticket in one repo shows reviewed heads %v, want the round's as they are", got)
	}
	// The round's oracle names are the union of the repos'; a pull request names
	// those its own manifest declares.
	man := manifest.Manifest{Oracles: map[string]string{"test": "go test ./..."}}
	if got := web.oracleNames(man); !slices.Equal(got, []string{"test"}) {
		t.Errorf("the web's oracle names = %v, want the ones its manifest declares: test", got)
	}
	if got := alone.oracleNames(man); !slices.Equal(got, []string{"lint", "test"}) {
		t.Errorf("a ticket in one repo names %v, want the round's oracle names as they are", got)
	}
	if web.lineRepo() != "web" || alone.lineRepo() != "" {
		t.Errorf("a journal line names the repo in a project of several (%q) and not in a project of one (%q)", web.lineRepo(), alone.lineRepo())
	}
	if !strings.HasPrefix(web.notesName(), "review-notes-") || alone.notesName() != "review-notes.md" {
		t.Errorf("notes names: %q and %q", web.notesName(), alone.notesName())
	}
}
