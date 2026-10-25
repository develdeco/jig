package mergefacts

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/project"
	"github.com/develdeco/jig/internal/ready"
	"github.com/develdeco/jig/internal/store"
)

// fakePRs is a repo host that holds pull requests' states and remembers what it
// was asked.
type fakePRs struct {
	reads map[string]PRRead
	asked []string
}

func (f *fakePRs) Read(url string) PRRead {
	f.asked = append(f.asked, url)
	return f.reads[url]
}

func mustGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	out, err := gitx.Run(dir, args...)
	if err != nil {
		t.Fatalf("git %s: %v", strings.Join(args, " "), err)
	}
	return out
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// repo is a clone with an origin: main has one commit, pushed.
type repo struct{ origin, clone string }

func newRepo(t *testing.T) repo {
	t.Helper()
	root := t.TempDir()
	r := repo{origin: filepath.Join(root, "origin.git"), clone: filepath.Join(root, "clone")}
	mustGit(t, root, "init", "--bare", "-b", "main", r.origin)
	mustGit(t, root, "init", "-b", "main", r.clone)
	mustGit(t, r.clone, "config", "user.email", "fixture@example.invalid")
	mustGit(t, r.clone, "config", "user.name", "fixture")
	mustGit(t, r.clone, "remote", "add", "origin", r.origin)
	r.commit(t, "base.txt")
	mustGit(t, r.clone, "push", "origin", "main")
	mustGit(t, r.clone, "fetch", "origin")
	return r
}

// commit adds a file named name to the clone's current branch.
func (r repo) commit(t *testing.T, name string) {
	t.Helper()
	write(t, filepath.Join(r.clone, name), name+"\n")
	mustGit(t, r.clone, "add", "-A")
	mustGit(t, r.clone, "commit", "-m", "add "+name)
}

// branch builds the ticket's branch off main with one commit and pushes it.
func (r repo) branch(t *testing.T, name string) {
	t.Helper()
	mustGit(t, r.clone, "switch", "-c", name, "main")
	r.commit(t, name+".txt")
	mustGit(t, r.clone, "push", "origin", name)
	mustGit(t, r.clone, "switch", "main")
}

// ticketStore is a store holding a published ticket JIG-1 titled "First", whose
// branch is JIG-1-first.
func ticketStore(t *testing.T, published bool, urls ...string) *store.Store {
	t.Helper()
	st := &store.Store{Root: t.TempDir()}
	write(t, filepath.Join(st.TicketDir("JIG-1"), "ticket.yaml"), "schema_version: 1\ntitle: First\n")
	for _, u := range urls {
		if err := journal.Append(st, "JIG-1", journal.Line{Event: journal.EventPr, URL: u}); err != nil {
			t.Fatal(err)
		}
	}
	if published {
		if err := journal.Append(st, "JIG-1", journal.Line{Event: journal.EventPublishDone}); err != nil {
			t.Fatal(err)
		}
	}
	return st
}

func reader(st *store.Store, r repo, prs PRs) *Reader {
	return &Reader{
		Store:  st,
		Cfg:    project.Config{Repos: []project.Repo{{Remote: r.origin, Label: "app"}}},
		Clones: map[string]string{"app": r.clone},
		PRs:    prs,
	}
}

const recordHint = "no merge on record: record its PR with `jig publish JIG-1 --pr <url>`"

// TestReaderMergeByAncestry: with no pull request recorded, the ticket's branch
// tip being an ancestor of origin/main, as the clone has it, is the one evidence
// of a merge. Merged in the clone only, merged by squashing, or not merged, it
// is not.
func TestReaderMergeByAncestry(t *testing.T) {
	t.Parallel()
	r := newRepo(t)
	r.branch(t, "JIG-1-first")
	st := ticketStore(t, true)

	m, err := reader(st, r, nil).Of("JIG-1")
	if err != nil || m.Met() || m.Why() != recordHint {
		t.Fatalf("branch not merged: %+v, %v, want not met with %q", m, err, recordHint)
	}

	// Merged in the clone's own main, not pushed: origin/main does not have it.
	mustGit(t, r.clone, "merge", "--no-ff", "-m", "merge", "JIG-1-first")
	if m, _ := reader(st, r, nil).Of("JIG-1"); m.Met() {
		t.Errorf("merged only in the clone's main: %+v, want not met (origin/main lacks it)", m)
	}

	mustGit(t, r.clone, "push", "origin", "main")
	m, err = reader(st, r, nil).Of("JIG-1")
	if err != nil || !m.Met() || m.Via() != ready.ByAncestry {
		t.Errorf("merged and pushed: %+v, %v, want met by ancestry", m, err)
	}

	// The remote branch gone, the local one is still looked at; with both gone
	// there is no tip, which is no evidence.
	mustGit(t, r.clone, "push", "origin", "--delete", "JIG-1-first")
	mustGit(t, r.clone, "fetch", "--prune", "origin")
	if m, _ := reader(st, r, nil).Of("JIG-1"); !m.Met() {
		t.Errorf("remote branch deleted, local kept: %+v, want met", m)
	}
	mustGit(t, r.clone, "branch", "-D", "JIG-1-first")
	if m, _ := reader(st, r, nil).Of("JIG-1"); m.Met() {
		t.Errorf("no branch left: %+v, want not met", m)
	}
}

// TestReaderSquashIsNotAMerge: a branch whose work landed in main as one squash
// commit has a tip that main does not hold, and equal content is not a merge.
func TestReaderSquashIsNotAMerge(t *testing.T) {
	t.Parallel()
	r := newRepo(t)
	r.branch(t, "JIG-1-first")
	mustGit(t, r.clone, "merge", "--squash", "JIG-1-first")
	mustGit(t, r.clone, "commit", "-m", "squash")
	mustGit(t, r.clone, "push", "origin", "main")

	m, err := reader(ticketStore(t, true), r, nil).Of("JIG-1")
	if err != nil || m.Met() || m.Why() != recordHint {
		t.Errorf("squashed: %+v, %v, want not met with %q", m, err, recordHint)
	}
}

// TestReaderTicketNotPublished: a ticket that has not published has no merge to
// look for, whatever main holds, and no host is asked.
func TestReaderTicketNotPublished(t *testing.T) {
	t.Parallel()
	r := newRepo(t)
	r.branch(t, "JIG-1-first")
	mustGit(t, r.clone, "merge", "--no-ff", "-m", "merge", "JIG-1-first")
	mustGit(t, r.clone, "push", "origin", "main")

	prs := &fakePRs{}
	m, err := reader(ticketStore(t, false, "https://github.com/o/r/pull/1"), r, prs).Of("JIG-1")
	if err != nil || m.Met() || m.Why() != ready.NotPublished {
		t.Errorf("not published: %+v, %v, want not met, %q", m, err, ready.NotPublished)
	}
	if len(prs.asked) != 0 {
		t.Errorf("the host was asked about a ticket that had not published: %v", prs.asked)
	}
}

// TestReaderRecordedPullRequestDecides: the latest pull request recorded is the
// one asked about, and what the host says of it decides, ahead of the branch;
// the branch is the way left only when the host could not say.
func TestReaderRecordedPullRequestDecides(t *testing.T) {
	t.Parallel()
	const (
		old = "https://github.com/o/app/pull/3"
		cur = "https://github.com/o/app/pull/9"
	)
	r := newRepo(t)
	r.branch(t, "JIG-1-first")
	st := ticketStore(t, true, old, cur)

	for _, tc := range []struct {
		name string
		read PRRead
		met  bool
		by   string
		why  string
	}{
		{"merged", PRRead{State: ready.PRMerged}, true, ready.ByPR, ""},
		{"open", PRRead{State: ready.PROpen}, false, "", "its PR is open (" + cur + ")"},
		{"closed", PRRead{State: ready.PRClosed}, false, "", "its PR was closed without merging (" + cur + ")"},
		{"the host cannot say", PRRead{Offline: true}, false, "", "its PR could not be read from the host (" + cur + ")"},
	} {
		prs := &fakePRs{reads: map[string]PRRead{cur: tc.read}}
		m, err := reader(st, r, prs).Of("JIG-1")
		if err != nil || m.Met() != tc.met || m.Why() != tc.why || m.Via() != tc.by {
			t.Errorf("%s: %+v, %v, want met %v by %q, why %q", tc.name, m, err, tc.met, tc.by, tc.why)
		}
		if len(prs.asked) != 1 || prs.asked[0] != cur {
			t.Errorf("%s: the host was asked %v, want only the latest pull request, %s", tc.name, prs.asked, cur)
		}
	}

	// Merged in main, but the pull request recorded is open: it decides.
	mustGit(t, r.clone, "merge", "--no-ff", "-m", "merge", "JIG-1-first")
	mustGit(t, r.clone, "push", "origin", "main")
	open := &fakePRs{reads: map[string]PRRead{cur: {State: ready.PROpen}}}
	if m, _ := reader(st, r, open).Of("JIG-1"); m.Met() {
		t.Errorf("an open pull request, the branch in main: %+v, want not met (the pull request decides)", m)
	}
	// The host cannot say, and the branch is in main: that is evidence.
	down := &fakePRs{reads: map[string]PRRead{cur: {Offline: true}}}
	if m, _ := reader(st, r, down).Of("JIG-1"); !m.Met() || m.Via() != ready.ByAncestry {
		t.Errorf("host down, branch in main: %+v, want met by ancestry", m)
	}
}

// TestReaderRemembersAFactForTheRun: the fact of a ticket is read once, so a
// listing that meets one blocker twice asks the host once.
func TestReaderRemembersAFactForTheRun(t *testing.T) {
	t.Parallel()
	const pr = "https://github.com/o/app/pull/9"
	prs := &fakePRs{reads: map[string]PRRead{pr: {State: ready.PRMerged}}}
	rd := reader(ticketStore(t, true, pr), newRepo(t), prs)
	for i := 0; i < 3; i++ {
		if m, err := rd.Of("JIG-1"); err != nil || !m.Met() {
			t.Fatalf("Of #%d = %+v, %v, want met", i, m, err)
		}
	}
	if len(prs.asked) != 1 {
		t.Errorf("the host was asked %d times, want once", len(prs.asked))
	}
}

// TestReaderNoRepoNoClone: a project that names no repo, or a repo with no
// clone here, is judged by its pull request alone.
func TestReaderNoRepoNoClone(t *testing.T) {
	t.Parallel()
	const pr = "https://github.com/o/app/pull/9"
	st := ticketStore(t, true, pr)
	prs := &fakePRs{reads: map[string]PRRead{pr: {State: ready.PRMerged}}}
	rd := &Reader{Store: st, PRs: prs}
	if m, err := rd.Of("JIG-1"); err != nil || !m.Met() || m.Repos[0].Repo != "" {
		t.Errorf("no repo: %+v, %v, want met by its pull request", m, err)
	}
	bare := &Reader{Store: ticketStore(t, true), Cfg: project.Config{Repos: []project.Repo{{Remote: "https://github.com/o/app.git"}}}}
	if m, err := bare.Of("JIG-1"); err != nil || m.Met() || m.Why() != recordHint {
		t.Errorf("a repo with no clone, no pull request: %+v, %v, want not met with %q", m, err, recordHint)
	}
}

// TestReaderSeveralRepos: a ticket that builds in two repos is met when each is;
// the pull requests are the repos' by the repository they are on, the repos it
// does not build in do not count, and what is missing names its repo.
func TestReaderSeveralRepos(t *testing.T) {
	t.Parallel()
	const (
		apiPR = "https://github.com/o/api/pull/1"
		webPR = "https://github.com/o/web/pull/2"
	)
	cfg := project.Config{Repos: []project.Repo{
		{Remote: "https://github.com/o/api.git"},
		{Remote: "https://github.com/o/web.git"},
		{Remote: "https://github.com/o/docs.git"},
	}}
	slices := func(st *store.Store, workspaces ...string) {
		var b strings.Builder
		b.WriteString("slices:\n")
		for i, w := range workspaces {
			b.WriteString("  - id: s" + string(rune('a'+i)) + "\n    workspace: " + w + "\n    goal: g\n    oracle: test\n    blocked_by: []\n    from_brief: []\n")
		}
		write(t, filepath.Join(st.TicketDir("JIG-1"), "slices.yaml"), b.String())
	}
	of := func(st *store.Store, prs map[string]string) (ready.Merge, []string) {
		reads := map[string]PRRead{}
		for u, s := range prs {
			reads[u] = PRRead{State: s}
		}
		f := &fakePRs{reads: reads}
		m, err := (&Reader{Store: st, Cfg: cfg, PRs: f}).Of("JIG-1")
		if err != nil {
			t.Fatal(err)
		}
		return m, f.asked
	}

	st := ticketStore(t, true, apiPR, webPR)
	slices(st, "api/server", "web/ui")
	m, asked := of(st, map[string]string{apiPR: ready.PRMerged, webPR: ready.PROpen})
	if m.Met() || m.Why() != "web: its PR is open ("+webPR+")" {
		t.Errorf("api merged, web open: met %v, why %q, want only web missing", m.Met(), m.Why())
	}
	if len(m.Repos) != 2 || m.Repos[0].Repo != "api" || m.Repos[1].Repo != "web" {
		t.Errorf("repos = %+v, want api then web (docs is not built in)", m.Repos)
	}
	if len(asked) != 2 {
		t.Errorf("the host was asked %v, want each repo's own pull request once", asked)
	}
	m, _ = of(st, map[string]string{apiPR: ready.PRMerged, webPR: ready.PRMerged})
	if !m.Met() || m.Via() != ready.ByPR {
		t.Errorf("both merged: %+v, want met by pull request", m)
	}

	// A pull request on neither repo's repository is nobody's: web has none.
	stray := ticketStore(t, true, apiPR, "https://github.com/other/place/pull/5")
	slices(stray, "api/server", "web/ui")
	m, _ = of(stray, map[string]string{apiPR: ready.PRMerged})
	if m.Met() || m.Why() != "web: "+recordHint {
		t.Errorf("a pull request on no repo's repository: met %v, why %q, want web with nothing on record", m.Met(), m.Why())
	}

	// A ticket that builds in one repo takes every pull request it recorded as
	// that repo's.
	one := ticketStore(t, true, "https://github.example/owner/repo/pull/7")
	slices(one, "api/server")
	m, _ = of(one, map[string]string{"https://github.example/owner/repo/pull/7": ready.PRMerged})
	if !m.Met() || len(m.Repos) != 1 || m.Repos[0].Repo != "" {
		t.Errorf("one repo built in: %+v, want met, and no repo named", m)
	}
}

// TestReaderPullRequestOfARepoWithoutAGitHubRemote: a remote that is not
// GitHub's names only a repository, so a pull request is the repo's when it is
// on a repository of that name, whoever owns it; one on another repository is
// not.
func TestReaderPullRequestOfARepoWithoutAGitHubRemote(t *testing.T) {
	t.Parallel()
	cfg := project.Config{Repos: []project.Repo{
		{Remote: "/srv/git/api.git"},
		{Remote: `C:\work\web`},
		{Remote: "git@git.example:team/docs.git", Label: "handbook"},
	}}
	urls := map[string]string{
		"https://git.example/team/api/pull/1":      "api",
		"https://git.example/team/web/pull/2":      "web",
		"https://git.example/team/docs/pull/3":     "handbook",
		"https://git.example/team/handbook/pull/4": "handbook",
		"https://git.example/team/other/pull/5":    "",
	}
	for url, want := range urls {
		for _, r := range cfg.Repos {
			ref := repoRef{name: r.Name(), remote: r.Remote}
			if got := isOf(ref, url); got != (r.Name() == want) {
				t.Errorf("isOf(%s, %s) = %v, want %v", r.Name(), url, got, r.Name() == want)
			}
		}
	}
	for remote, want := range map[string]string{
		"/srv/git/api.git":              "api",
		`C:\work\web\`:                  "web",
		"git@git.example:team/docs.git": "docs",
		"https://github.com/o/r.git":    "r",
		"plain":                         "plain",
	} {
		if got := remoteBase(remote); got != want {
			t.Errorf("remoteBase(%q) = %q, want %q", remote, got, want)
		}
	}
}
