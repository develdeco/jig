package verifydeliver

// A gate round covers the repos a ticket's slices build in. In a project of
// one repo that is the repo, and a round works as it always has: a finding's
// file is a path in the repo. In a project of several repos a round has a
// lease, a base, a head and a scope diff for each repo the ticket touches, and
// names a file as "<repo>/<path>" everywhere a round names one - a finding's
// file, reviewed_paths, must_review - so one list of files covers all the repos
// and a file says which repo's code it is. A workspace is named the same way
// (ready.Place): the fix slice of a finding in api/server/handler.go builds in
// the workspace api/server.

import (
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/manifest"
	"github.com/develdeco/jig/internal/pool"
	"github.com/develdeco/jig/internal/project"
	"github.com/develdeco/jig/internal/ready"
	"github.com/develdeco/jig/internal/store"
)

// gateRepo is one repo a gate round covers: the slices that build in it, the
// lease the round reads it in and what the round learned of it there.
type gateRepo struct {
	repo   project.Repo
	name   string
	target string
	// slices are the ticket's slices that build in the repo, fix slices
	// included.
	slices []store.Slice
	// built is the commits the journal records jig built in the repo: the ones
	// its copy of the branch must hold (chooseBuiltCopy).
	built []string

	lease pool.Lease
	// copy says whose copy of the branch the lease was left at.
	copy       branchCopy
	man        manifest.Manifest
	oracleRuns []OracleRun
}

// gateRepos are the repos a gate round covers, in the project's order. A
// project of one repo (or none that this machine knows: primaryRepo) is
// covered by that repo, whatever its slices say. A project of several repos is
// covered by the repos its ticket's slices build in (ready.Place) and by no
// other, which may be one repo that is not the first. A ticket whose slices
// build in none of them has nothing to review, and the round says why.
func gateRepos(d Deps, ticket string, slices []store.Slice) ([]*gateRepo, error) {
	if len(d.Cfg.Repos) < 2 {
		repo, name, target := primaryRepo(d.Cfg)
		return []*gateRepo{{repo: repo, name: name, target: target, slices: slices}}, nil
	}
	place := ready.Place(ready.ProjectRepos(d.Cfg, d.Machine.Clones), slices)
	var out []*gateRepo
	for _, repo := range d.Cfg.Repos {
		var mine []store.Slice
		for _, s := range slices {
			if place.Repo[s.Workspace] == repo.Name() {
				mine = append(mine, s)
			}
		}
		if len(mine) > 0 {
			out = append(out, &gateRepo{repo: repo, name: repo.Name(), target: repo.TargetBranch(), slices: mine})
		}
	}
	if len(out) == 0 {
		return nil, &axi.Error{
			Msg:  fmt.Sprintf("none of %s's slices builds in a repo of the project, so there is no repo to review", ticket),
			Code: "VALIDATION_ERROR",
			Help: []string{fmt.Sprintf("Name the repo in each slice's workspace, as <repo>/<workspace>; `jig validate %s` says which slices have none", ticket)},
		}
	}
	return out, nil
}

// builtIn is the commits the journal records jig built for slices: the
// verified lines of those slices, without repeats, in journal order.
func builtIn(lines []journal.Line, slices []store.Slice) []string {
	mine := make(map[string]bool, len(slices))
	for _, s := range slices {
		mine[s.ID] = true
	}
	var commits []string
	seen := map[string]bool{}
	for _, l := range lines {
		if l.Event == journal.EventVerified && l.Commit != "" && mine[l.Slice] && !seen[l.Commit] {
			seen[l.Commit] = true
			commits = append(commits, l.Commit)
		}
	}
	return commits
}

// checkBranchAgainstTargets holds the ticket's branch, which has one name in
// every repo, to the target branch of each repo after the first: the first
// repo's was judged when the branch was resolved (resolveGateBranch), and a
// branch is no ticket's if it is the target of any repo it is cut in.
func checkBranchAgainstTargets(d Deps, ticket string, gb gateBranch, repos []*gateRepo) error {
	rec, err := d.Store.ReadTicket(ticket)
	if err != nil {
		return fmt.Errorf("verifydeliver: gate: read ticket record: %w", err)
	}
	judged := map[string]bool{repos[0].target: true}
	for _, gr := range repos[1:] {
		if judged[gr.target] {
			continue
		}
		judged[gr.target] = true
		switch {
		case gb.Adopting:
			err = d.Store.CheckAdoptableBranch(ticket, gb.Name, gr.target)
		case rec.Adopted():
			_, err = d.Store.ResolveTicketBranch(ticket, rec, gr.target)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// inRepo says which repo of a project of several a refusal or a failure is
// about: an *axi.Error keeps its code and help and has the repo in front of its
// message. In a project of one repo there is nothing to say.
func inRepo(qualified bool, name string, err error) error {
	if err == nil || !qualified {
		return err
	}
	var ae *axi.Error
	if errors.As(err, &ae) {
		named := *ae
		named.Msg = "repo " + name + ": " + ae.Msg
		return &named
	}
	return fmt.Errorf("repo %s: %w", name, err)
}

// roundRepo is one repo of a round as far as the files of its findings are
// concerned: where the round reads it, the commit under review and its
// manifest.
type roundRepo struct {
	Name string
	// Dir is the gate lease the round reads the repo in, Head the commit
	// under review there.
	Dir  string
	Head string
	Man  manifest.Manifest
}

// roundFiles reads the file names of a gate round (see the top of this file):
// which repo a file is in, and the manifest it is judged against.
type roundFiles struct {
	repos     []roundRepo
	qualified bool
}

// oneRepoFiles is the files of a round over one repo with manifest man, whose
// files are paths in the repo.
func oneRepoFiles(man manifest.Manifest) roundFiles {
	return roundFiles{repos: []roundRepo{{Man: man}}}
}

// of returns the repo file is in and its path there. ok is false when a
// round over several repos is handed a file that does not begin with the name
// of one of them.
func (rf roundFiles) of(file string) (repo roundRepo, rel string, ok bool) {
	if !rf.qualified {
		return rf.repos[0], file, true
	}
	name, rest, found := strings.Cut(file, "/")
	if !found || rest == "" {
		return roundRepo{}, file, false
	}
	for _, r := range rf.repos {
		if r.Name == name {
			return r, rest, true
		}
	}
	return roundRepo{}, file, false
}

// qualify is rel, a path in repo, as the round names it.
func (rf roundFiles) qualify(repo, rel string) string {
	if !rf.qualified {
		return rel
	}
	return repo + "/" + rel
}

// qualifyAll is qualify over a list.
func (rf roundFiles) qualifyAll(repo string, rels []string) []string {
	if !rf.qualified {
		return rels
	}
	out := make([]string, len(rels))
	for i, rel := range rels {
		out[i] = rf.qualify(repo, rel)
	}
	return out
}

// repoNames lists the names of the repos of a round over several, for a
// message.
func (rf roundFiles) repoNames() string {
	names := make([]string, len(rf.repos))
	for i, r := range rf.repos {
		names[i] = r.Name
	}
	return strings.Join(names, ", ")
}

// unknownRepoFile is the reason a round over several repos cannot place a file.
func (rf roundFiles) unknownRepoFile(file string) string {
	return fmt.Sprintf("file %q names no repo of this round (%s): write it as <repo>/<path>", file, rf.repoNames())
}

// workspace is the workspace file lies in, as a slice names it: the manifest
// workspace whose path holds the file, with the repo in front in a round over
// several repos. "" when the file lies in no workspace.
func (rf roundFiles) workspace(file string) string {
	repo, rel, ok := rf.of(file)
	if !ok {
		return ""
	}
	ws := workspaceFor(rel, repo.Man)
	if ws != "" && rf.qualified {
		ws = repo.Name + "/" + ws
	}
	return ws
}

// oracles are the oracle names that can prove a fix of file: those of the
// manifest of the repo it is in.
func (rf roundFiles) oracles(file string) []string {
	repo, _, ok := rf.of(file)
	if !ok {
		return nil
	}
	return SortedOracleNames(repo.Man)
}

// allOracles are the oracle names of every repo of the round, sorted, without
// repeats.
func (rf roundFiles) allOracles() []string {
	var out []string
	seen := map[string]bool{}
	for _, r := range rf.repos {
		for _, name := range SortedOracleNames(r.Man) {
			if !seen[name] {
				seen[name] = true
				out = append(out, name)
			}
		}
	}
	sort.Strings(out)
	return out
}

// manifestOf is the manifest file is judged against, empty for a file no repo
// of the round holds.
func (rf roundFiles) manifestOf(file string) manifest.Manifest {
	repo, _, _ := rf.of(file)
	return repo.Man
}

// atHead reports whether file exists at the commit under review in its repo.
func (rf roundFiles) atHead(file string) (bool, error) {
	repo, rel, ok := rf.of(file)
	if !ok {
		return false, nil
	}
	return gitx.FileExistsAtRev(repo.Dir, repo.Head, rel)
}

// byRepo groups files by the repo they are in, as paths in the repo. A file no
// repo of the round holds is left out.
func (rf roundFiles) byRepo(files []string) map[string][]string {
	out := map[string][]string{}
	for _, f := range files {
		if repo, rel, ok := rf.of(f); ok {
			out[repo.Name] = append(out[repo.Name], rel)
		}
	}
	return out
}

// relativize reads one reviewed_paths entry (relativizeReviewedPath) against
// the leases of the round: a relative path is taken as the round names it, an
// absolute one inside a repo's lease becomes that repo's file.
func (rf roundFiles) relativize(p string) (string, bool) {
	if !rf.qualified {
		return relativizeReviewedPath(rf.repos[0].Dir, p)
	}
	if norm, err := normalizeRepoRelPath(p); err == nil {
		return norm, true
	}
	for _, r := range rf.repos {
		if rel, ok := relativizeAbsolute(r.Dir, p); ok {
			return rf.qualify(r.Name, rel), true
		}
	}
	return "", false
}

// normalizeReviewed relativizes and normalizes every reviewed_paths entry
// exactly once, right after validateResultIn has accepted the result.
// Coverage, clearing and persistence all read this one normalized list
// afterward, so an absolute in-lease path that counted as coverage also counts
// as clearing evidence and is never written to findings.yaml as a host path. An
// entry that cannot be normalized was already ignored by the coverage check, so
// it is dropped here too.
func (rf roundFiles) normalizeReviewed(paths []string) []string {
	out := make([]string, 0, len(paths))
	seen := map[string]bool{}
	for _, p := range paths {
		norm, ok := rf.relativize(p)
		if !ok || seen[norm] {
			continue
		}
		seen[norm] = true
		out = append(out, norm)
	}
	return out
}
