// Package mergefacts reads whether the work of a ticket has merged: the fact a
// ticket that waits for another is held on (decision 43 of the digester). A
// ticket has merged in a repo when the pull request recorded for it (`jig
// publish --pr`, or a pr line in its journal) is merged, as the repo host says;
// with none recorded, when its branch tip is an ancestor of the repo's target
// branch on its origin. Nothing else stands for either: a patch that happens to
// be equivalent is not a merge.
//
// The judging is ready.JudgeRepo, which reads nothing; this package reads what
// it is given: the ticket's journal, the repo host (through a PRs, which keeps
// what it learned so a command asking about many tickets does not ask the
// network for each) and the clones. It is what the mirror's checkpoint could
// call to record a merge at the moment the mirror syncs.
package mergefacts

import (
	"os"
	"strings"

	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/project"
	"github.com/develdeco/jig/internal/ready"
	"github.com/develdeco/jig/internal/repohost"
	"github.com/develdeco/jig/internal/store"
)

// Reader reads the merge facts of the tickets of one project, and remembers
// each for the life of the value: a command that asks about the same blocker
// twice reads it once. It is not safe for concurrent use.
type Reader struct {
	Store *store.Store
	Cfg   project.Config
	// Clones maps a repo's name to its clone on this machine, as the machine
	// mapping holds it. A repo with none is judged by its pull request alone.
	Clones map[string]string
	// PRs says what state a pull request is in; nil reads none, so a recorded
	// pull request is one the host could not be asked about.
	PRs PRs

	memo map[string]ready.Merge
}

// Of is the merge fact of ticket: not met unless it published, then one fact
// per repo its slices build in.
func (r *Reader) Of(ticket string) (ready.Merge, error) {
	if m, ok := r.memo[ticket]; ok {
		return m, nil
	}
	lines, err := journal.Read(r.Store, ticket)
	if err != nil {
		return ready.Merge{}, err
	}
	var m ready.Merge
	var urls []string
	for _, l := range lines {
		switch l.Event {
		case journal.EventPublishDone:
			m.Published = true
		case journal.EventPr:
			if l.URL != "" {
				urls = append(urls, l.URL)
			}
		}
	}
	// A ticket that has not published has nothing to look for: no host is
	// asked and no clone is read.
	if m.Published {
		if m.Repos, err = r.repos(ticket, urls); err != nil {
			return ready.Merge{}, err
		}
	}
	if r.memo == nil {
		r.memo = map[string]ready.Merge{}
	}
	r.memo[ticket] = m
	return m, nil
}

// repoRef is one repo as the facts need it.
type repoRef struct {
	name   string
	remote string
	target string
	dir    string
}

// repos judges each repo the ticket builds in. urls are the pull requests its
// journal records, oldest first.
func (r *Reader) repos(ticket string, urls []string) ([]ready.RepoMerge, error) {
	rec, err := r.Store.ReadTicket(ticket)
	if err != nil {
		return nil, err
	}
	refs, err := r.builtIn(ticket)
	if err != nil {
		return nil, err
	}
	out := make([]ready.RepoMerge, 0, len(refs))
	for _, repo := range refs {
		ev := ready.Evidence{Ticket: ticket, PR: pullRequestOf(repo, len(refs), urls)}
		// A project of several repos names the repo in what it says.
		if len(refs) > 1 {
			ev.Repo = repo.name
		}
		if ev.PR != "" && r.PRs != nil {
			read := r.PRs.Read(ev.PR)
			ev.State, ev.AsOf, ev.Offline = read.State, read.AsOf, read.Offline
		}
		// The branch is the way left when the pull request did not decide.
		if ev.State == "" {
			ev.Ancestor = r.inTarget(ticket, rec, repo)
		}
		out = append(out, ready.JudgeRepo(ev))
	}
	return out, nil
}

// builtIn are the repos the ticket's slices build in, in the project's order.
// A project of one repo is that repo; so is a ticket with no slice placed in
// any repo (the facts are then asked of every repo of the project). A project
// that names no repo at all has one that is not named and has no clone.
func (r *Reader) builtIn(ticket string) ([]repoRef, error) {
	all := make([]repoRef, 0, len(r.Cfg.Repos))
	for _, repo := range r.Cfg.Repos {
		all = append(all, repoRef{name: repo.Name(), remote: repo.Remote, target: repo.TargetBranch(), dir: r.Clones[repo.Name()]})
	}
	switch len(all) {
	case 0:
		return []repoRef{{target: "main"}}, nil
	case 1:
		return all, nil
	}
	slices, err := r.Store.ReadSlices(ticket)
	if err != nil {
		return nil, err
	}
	used := map[string]bool{}
	for _, name := range ready.BuiltIn(ready.ProjectRepos(r.Cfg, r.Clones), slices) {
		used[name] = true
	}
	var out []repoRef
	for _, repo := range all {
		if used[repo.name] {
			out = append(out, repo)
		}
	}
	return out, nil
}

// pullRequestOf is the latest pull request of urls that is the repo's. When the
// ticket builds in one repo, every pull request it recorded is that repo's;
// otherwise a pull request belongs to the repo it is on (isOf), and one that is
// on none of them is no repo's.
func pullRequestOf(repo repoRef, repos int, urls []string) string {
	for i := len(urls) - 1; i >= 0; i-- {
		if repos == 1 || isOf(repo, urls[i]) {
			return urls[i]
		}
	}
	return ""
}

// isOf reports whether the pull request at url is on repo. A GitHub remote
// names the owner and the repository, and both must be the pull request's. A
// remote that is not GitHub's (a local path, a server whose address is not
// github.com) names only a repository, by the last part of its path: the
// repo's name, or that last part, must be the repository of the pull request.
func isOf(repo repoRef, url string) bool {
	ref, err := repohost.ParsePRURL(url)
	if err != nil {
		return false
	}
	if owner, name, ok := repohost.OwnerRepo(repo.remote); ok {
		return strings.EqualFold(owner, ref.Owner) && strings.EqualFold(name, ref.Repo)
	}
	return strings.EqualFold(ref.Repo, repo.name) || strings.EqualFold(ref.Repo, remoteBase(repo.remote))
}

// remoteBase is the last part of a remote's path without a trailing ".git",
// for a URL, an scp-style address or a local path alike.
func remoteBase(remote string) string {
	s := strings.TrimRight(strings.TrimSpace(remote), `/\`)
	s = strings.TrimSuffix(s, ".git")
	if i := strings.LastIndexAny(s, `/\:`); i >= 0 {
		s = s[i+1:]
	}
	return s
}

// inTarget reports whether the ticket's branch tip in the repo's clone is an
// ancestor of the repo's target branch on its origin: the clone's own
// origin/<target>, as of its last fetch (nothing is fetched here). The branch
// is looked for as the clone has it, on the origin and local; either one
// being in the target is enough. False when there is no clone, no origin
// target, or no such branch: the lack of a branch is not evidence.
func (r *Reader) inTarget(ticket string, rec store.Ticket, repo repoRef) bool {
	if repo.dir == "" {
		return false
	}
	if fi, err := os.Stat(repo.dir); err != nil || !fi.IsDir() {
		return false
	}
	target := "refs/remotes/origin/" + repo.target
	if !hasCommit(repo.dir, target) {
		return false
	}
	branch, err := r.Store.ResolveTicketBranch(ticket, rec, repo.target)
	if err != nil {
		return false
	}
	for _, ref := range []string{"refs/remotes/origin/" + branch, "refs/heads/" + branch} {
		tip, found, _ := gitx.ResolveCommit(repo.dir, ref)
		if !found || tip == "" {
			continue
		}
		if ok, err := gitx.IsAncestor(repo.dir, tip, target); err == nil && ok {
			return true
		}
	}
	return false
}

// hasCommit reports whether ref names a commit in dir.
func hasCommit(dir, ref string) bool {
	out, found, _ := gitx.ResolveCommit(dir, ref)
	return found && out != ""
}
