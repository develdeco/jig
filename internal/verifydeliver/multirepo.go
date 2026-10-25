package verifydeliver

// A ticket whose slices build in several repos publishes one pull request for
// each, all from the one branch name, and each pull request names the others.
// This file holds what publish decides about the repos of a ticket: which it
// covers, which pull requests the others have, and when the ticket is done.

import (
	"fmt"
	"sort"
	"strings"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/project"
)

// publishRepos are the repos a publish works in, out of covered, the repos the
// ticket's slices build in (gateRepos): all of them, or with a repo named (`jig
// publish --repo`) that one. A name that is no repo of the project, or that the
// ticket does not build in, is refused and says which repos there are.
func publishRepos(cfg project.Config, ticket string, covered []*gateRepo, name string) ([]*gateRepo, error) {
	if name == "" {
		return covered, nil
	}
	for _, gr := range covered {
		if gr.name == name {
			return []*gateRepo{gr}, nil
		}
	}
	var inProject, built []string
	for _, r := range cfg.Repos {
		inProject = append(inProject, r.Name())
	}
	for _, gr := range covered {
		built = append(built, gr.name)
	}
	for _, have := range inProject {
		if have == name {
			return nil, &axi.Error{
				Msg:  fmt.Sprintf("%s builds in %s, not in %s", ticket, joinNames(built), name),
				Code: "VALIDATION_ERROR",
				Help: []string{fmt.Sprintf("Name one of its repos with --repo (%s), or leave --repo out to cover them all", strings.Join(built, ", "))},
			}
		}
	}
	return nil, &axi.Error{
		Msg:  fmt.Sprintf("the project has no repo %q (it has %s)", name, strings.Join(inProject, ", ")),
		Code: "VALIDATION_ERROR",
	}
}

// joinNames lists names in prose: "a", "a and b", "a, b and c".
func joinNames(names []string) string {
	if len(names) < 2 {
		return strings.Join(names, "")
	}
	return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
}

// recordedPRs are the pull requests a ticket's journal holds, by the repo each
// pr line names: the latest of each repo that has a url. A line that names no
// repo (a project of one repo writes none) is filed under "".
func recordedPRs(lines []journal.Line) map[string]string {
	out := map[string]string{}
	for _, l := range lines {
		if l.Event == journal.EventPr && l.URL != "" {
			out[l.Repo] = l.URL
		}
	}
	return out
}

// linksFor are the other repos of the ticket as t's pull request names them,
// each with the url known for its pull request (known, by repo name) or none.
// A ticket that publishes in one repo has no other.
func linksFor(t *publishTarget, known map[string]string) []prLink {
	var links []prLink
	for _, name := range t.covered {
		if name != t.repoName {
			links = append(links, prLink{Repo: name, URL: known[name]})
		}
	}
	return links
}

// sameLinks reports whether a and b name the same pull requests.
func sameLinks(a, b []prLink) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// pendingRepos are the repos of covered that have no pull request recorded
// since the ticket's last publish-done, in the order of covered: a publish is
// done when there are none. A ticket that publishes in one repo is done with
// the first pr line, whatever repo it names.
func pendingRepos(lines []journal.Line, covered []string) []string {
	start := 0
	for i, l := range lines {
		if l.Event == journal.EventPublishDone {
			start = i + 1
		}
	}
	recorded := map[string]bool{}
	for _, l := range lines[start:] {
		if l.Event == journal.EventPr {
			recorded[l.Repo] = true
		}
	}
	if len(covered) == 1 {
		if len(recorded) > 0 {
			return nil
		}
		return covered
	}
	var pending []string
	for _, name := range covered {
		if !recorded[name] {
			pending = append(pending, name)
		}
	}
	return pending
}

// unionNames is the names of every list, sorted and once each.
func unionNames(lists ...[]string) []string {
	seen := map[string]bool{}
	var out []string
	for _, list := range lists {
		for _, name := range list {
			if !seen[name] {
				seen[name] = true
				out = append(out, name)
			}
		}
	}
	sort.Strings(out)
	return out
}
