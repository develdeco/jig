package main

import (
	"fmt"
	"slices"
	"strings"

	"github.com/develdeco/jig/internal/project"
	"github.com/develdeco/jig/internal/ready"
	"github.com/develdeco/jig/internal/store"
)

// projectTargets are the target branches of the project's repos, in its order
// and without repeats, or the default target for a project with no repo. The
// branch of a ticket has one name in every repo, so it must not be any of them.
func projectTargets(cfg project.Config) []string {
	var targets []string
	seen := map[string]bool{}
	for _, r := range cfg.Repos {
		if t := r.TargetBranch(); !seen[t] {
			seen[t] = true
			targets = append(targets, t)
		}
	}
	if len(targets) == 0 {
		targets = append(targets, project.Repo{}.TargetBranch())
	}
	return targets
}

// sliceHome is the repo a slice builds in and the directory to build it in. In
// a project of one repo that is the repo the work is in. In a project of
// several it is the repo the slice's workspace names, or the one manifest that
// declares it; the directory is then the work tree around the working
// directory when that is in the repo, else the repo's clone ("" when it has
// none on this machine). A slice no repo owns builds where the work is.
func sliceHome(w work, sl store.Slice) (repo, dir string) {
	if len(w.Cfg.Repos) < 2 {
		return w.Where.Repo, w.buildDir()
	}
	place := ready.Place(ready.ProjectRepos(w.Cfg, w.MP.Clones), []store.Slice{sl})
	repo = place.Repo[sl.Workspace]
	switch {
	case repo == ready.Unplaced || repo == w.Where.Repo:
		return w.Where.Repo, w.buildDir()
	}
	return repo, w.MP.Clones[repo]
}

// otherRepoHelp is the line that sends a session to the repo a slice builds in
// when it is run from somewhere else (another repo, or the orchestrator
// itself); "" when the slice builds where the work is.
func otherRepoHelp(w work, sl store.Slice, repo, dir string) string {
	if len(w.Cfg.Repos) < 2 || repo == "" || repo == w.Where.Repo {
		return ""
	}
	where := "this directory is in " + w.Where.Repo
	if w.Where.Repo == "" {
		where = "this directory is in none of the repos"
	}
	if dir == "" {
		return fmt.Sprintf("Slice %s builds in repo %s, which has no clone on this machine (%s)", sl.ID, repo, where)
	}
	return fmt.Sprintf("Slice %s builds in repo %s: work in %s (%s)", sl.ID, repo, dir, where)
}

// validateWorkspaces checks that every slice's workspace is declared by the
// repo it builds in, and returns what is wrong. A project of one repo builds
// every slice there, in a workspace written as a bare id. In a project of
// several repos a slice names its repo, "<repo>/<workspace>", or writes a bare
// id that exactly one repo's manifest declares; a bare id that two repos
// declare is ambiguous, and one that none declares belongs to no repo. Each
// repo a slice builds in needs a clone on this machine to read its manifest
// from, and a manifest that resolves: a repo no slice builds in is not asked
// for a clone, though one that has a clone and a manifest that does not
// resolve is told.
func validateWorkspaces(repos []ready.Repo, all []store.Slice) []string {
	if len(repos) == 0 {
		return []string{"the project declares no repos"}
	}
	multi := len(repos) > 1
	byName := make(map[string]ready.Repo, len(repos))
	var problems []string
	for _, r := range repos {
		byName[r.Name] = r
		switch {
		case r.Dir == "" && !multi:
			problems = append(problems, fmt.Sprintf("no machine-mapped clone for repo %q", r.Name))
		case r.Err != nil && !multi:
			problems = append(problems, fmt.Sprintf("manifest resolve failed: %v", r.Err))
		case r.Err != nil:
			problems = append(problems, fmt.Sprintf("repo %s: manifest resolve failed: %v", r.Name, r.Err))
		}
	}

	place := ready.Place(repos, all)
	noClone := map[string]bool{}
	for _, sl := range all {
		written, name := sl.Workspace, place.Repo[sl.Workspace]
		if name == ready.Unplaced {
			problems = append(problems, unplacedWorkspace(repos, place, sl))
			continue
		}
		r := byName[name]
		switch {
		case r.Dir == "":
			if multi && !noClone[name] {
				noClone[name] = true
				problems = append(problems, fmt.Sprintf("no machine-mapped clone for repo %q", name))
			}
		case r.Err != nil:
			// Told once above.
		case !slices.Contains(r.Workspaces, place.ID[written]):
			if multi {
				problems = append(problems, fmt.Sprintf("slice %s: workspace %q not in the manifest of repo %s", sl.ID, place.ID[written], name))
			} else {
				problems = append(problems, fmt.Sprintf("slice %s: workspace %q not in manifest", sl.ID, written))
			}
		case multi:
			if p := foreignOracle(repos, r, sl); p != "" {
				problems = append(problems, p)
			}
		}
	}
	return problems
}

// unplacedWorkspace is the problem of a slice whose workspace no repo owns:
// ambiguous, when more than one repo declares it, else declared by none.
func unplacedWorkspace(repos []ready.Repo, place ready.Placement, sl store.Slice) string {
	written := sl.Workspace
	if owners := place.Declared[written]; len(owners) > 1 {
		named := make([]string, len(owners))
		for i, o := range owners {
			named[i] = o + "/" + written
		}
		return fmt.Sprintf("slice %s: ambiguous workspace %q: repos %s declare it; name its repo, as %s",
			sl.ID, written, strings.Join(owners, " and "), strings.Join(named, " or "))
	}
	var names, unread []string
	for _, r := range repos {
		names = append(names, r.Name)
		if !r.Read() {
			unread = append(unread, r.Name)
		}
	}
	msg := fmt.Sprintf("slice %s: workspace %q is declared by none of the repos (%s): name its repo, as <repo>/%s", sl.ID, written, strings.Join(names, ", "), written)
	if len(unread) > 0 {
		msg += fmt.Sprintf("; the manifest of %s could not be read", strings.Join(unread, ", "))
	}
	return msg
}

// foreignOracle is the problem of a slice whose oracle is one that another repo
// of the project declares and the repo the slice builds in does not: the slice
// is run with the manifest of its own repo, where that name is no oracle but a
// command, and a command that is the name of another repo's oracle is a slice
// written for the wrong repo. "" when the oracle is the repo's own, or is
// declared by none (a literal command, which a manifest cannot judge).
func foreignOracle(repos []ready.Repo, own ready.Repo, sl store.Slice) string {
	if slices.Contains(own.Oracles, sl.Oracle) {
		return ""
	}
	var declared []string
	for _, r := range repos {
		if r.Name != own.Name && slices.Contains(r.Oracles, sl.Oracle) {
			declared = append(declared, r.Name)
		}
	}
	if len(declared) == 0 {
		return ""
	}
	return fmt.Sprintf("slice %s: oracle %q is not in the manifest of repo %s (repo %s declares it)", sl.ID, sl.Oracle, own.Name, strings.Join(declared, " and "))
}
