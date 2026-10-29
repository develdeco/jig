package project

import (
	"fmt"
	"sort"
	"strings"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/gitx"
)

// ShareOpts are what `jig share` was told.
type ShareOpts struct {
	// Root is the project's root (the folder holding jig.yaml), Marker its
	// marker, and StoreDir the store on this machine.
	Root     string
	Marker   Marker
	StoreDir string
	// StoreRemote is the URL to make the store's origin (--store-remote).
	StoreRemote string
	// Remotes are the URLs to make the origin of a repo, by repo name
	// (--remote <name>=<url>): the project's own repo (the folder holding
	// jig.yaml) goes by the project's name, or ".".
	Remotes map[string]string
	// Create makes a repository through the host's CLI for each repo that
	// has no remote and was given none (--create).
	Create bool
}

// ShareTarget is one repository `jig share` sets a remote on and pushes.
type ShareTarget struct {
	// Name is what the repo is called: "store", the project's name for its
	// own repo, or a nested repo's name.
	Name string
	// Dir is the repo on this machine, and Branch the branch it pushes.
	Dir    string
	Branch string
	// Origin is the origin it has now ("" for none).
	Origin string
	// Set is the URL to make its origin, when Origin is none and one was
	// given; Create says to make a repository through the host instead.
	Set    string
	Create bool
	// Push says whether share pushes Branch: the store and the project's own
	// repo always, a nested repo only when share gave it its remote.
	Push bool
	// Submodule reports that .gitmodules carries a nested repo to collaborators.
	Submodule bool
}

// SharePlan is what `jig share` would do, in the order it does it.
type SharePlan struct {
	Root   string
	Marker Marker
	// Targets are the store first, then the nested repos, then the project's
	// own repo, which is pushed last: it carries jig.yaml, which names all the
	// others.
	Targets []ShareTarget
	// Without are the nested repos that still have no remote after share:
	// collaborators cannot get them.
	Without []string
}

// Summary says in a line each what the plan will do.
func (p SharePlan) Summary() []string {
	var out []string
	for _, t := range p.Targets {
		switch {
		case t.Set != "":
			out = append(out, fmt.Sprintf("make %s the origin of the %s (%s)", t.Set, t.Name, t.Dir))
		case t.Create:
			out = append(out, fmt.Sprintf("create a private repository %q through the host's CLI as the origin of the %s (%s)", t.hostName(p.Marker), t.Name, t.Dir))
		}
		if t.Push {
			out = append(out, fmt.Sprintf("push %s of the %s", t.Branch, t.Name))
		}
	}
	out = append(out, fmt.Sprintf("write the store's remote into %s and commit it", MarkerFile))
	return out
}

// hostName is the name the repository is created under on the host.
func (t ShareTarget) hostName(m Marker) string {
	if t.Name == "store" {
		return m.Project + "-tickets"
	}
	return t.Name
}

// PlanShare works out what `jig share` does, changing nothing. It refuses a
// repo that already has a different origin than the one it was told, a
// store or project repo that would still have no remote, a folder that is not
// a repository, and a machine with no git identity (jig.yaml is committed as
// the person).
func PlanShare(o ShareOpts) (SharePlan, error) {
	m := o.Marker
	name := m.Project
	if name == "" {
		name = baseName(o.Root)
	}
	plan := SharePlan{Root: o.Root, Marker: m}
	plan.Marker.Project = name
	used := map[string]bool{}
	var missing []string

	// target works out one repo's part. A repo that is pushed needs a branch
	// checked out; a nested repo that is only read (a submodule is detached
	// by default) does not, and is checked when it turns out to be pushed.
	target := func(label, dir, given string) (ShareTarget, error) {
		t := ShareTarget{Name: label, Dir: dir, Origin: OriginOf(dir), Push: true}
		t.Branch, _ = gitx.HeadBranch(dir)
		switch {
		case t.Origin != "" && given != "" && !sameRemote(t.Origin, given):
			return t, &axi.Error{
				Msg:  fmt.Sprintf("the %s already has the origin %s, not %s", label, t.Origin, given),
				Code: "VALIDATION_ERROR",
				Help: []string{fmt.Sprintf("Change it yourself first: git -C %s remote set-url origin %s", dir, given)},
			}
		case t.Origin == "" && given != "":
			t.Set = given
		case t.Origin == "" && o.Create:
			t.Create = true
		}
		return t, nil
	}

	// The store.
	st, err := target("store", o.StoreDir, o.StoreRemote)
	if err != nil {
		return SharePlan{}, err
	}
	if err := needsBranch(st); err != nil {
		return SharePlan{}, err
	}
	if st.Origin == "" && st.Set == "" && !st.Create {
		missing = append(missing, "the store has no remote: pass --store-remote <url>")
	}
	plan.Targets = append(plan.Targets, st)

	// The nested repos of an orchestrator.
	if m.Kind == KindOrchestrator {
		for _, s := range Statuses(o.Root, m.Repos) {
			rname := RepoName(s.Repo)
			if s.State != RepoCheckedOut {
				plan.Without = append(plan.Without, rname+" (not checked out here)")
				continue
			}
			given := o.Remotes[rname]
			used[rname] = true
			t, err := target(rname, s.Dir, given)
			if err != nil {
				return SharePlan{}, err
			}
			t.Submodule = contains(Submodules(o.Root), relativeTo(o.Root, s.Dir))
			t.Push = t.Set != "" || t.Create
			if err := needsBranch(t); err != nil {
				return SharePlan{}, err
			}
			if t.Origin == "" && t.Set == "" && !t.Create {
				plan.Without = append(plan.Without, rname)
			}
			plan.Targets = append(plan.Targets, t)
		}
	}

	// The project's own repo, last.
	if !isGitDir(o.Root) {
		return SharePlan{}, &axi.Error{
			Msg:  fmt.Sprintf("%s is not a git repository: there is nothing to share", o.Root),
			Code: "VALIDATION_ERROR",
			Help: []string{"Run `git init` there and commit the project, then run `jig share` again"},
		}
	}
	given := o.Remotes[name]
	if v, ok := o.Remotes["."]; ok {
		given = v
		used["."] = true
	}
	used[name] = true
	root, err := target(name, o.Root, given)
	if err != nil {
		return SharePlan{}, err
	}
	if err := needsBranch(root); err != nil {
		return SharePlan{}, err
	}
	if root.Origin == "" && root.Set == "" && !root.Create {
		missing = append(missing, fmt.Sprintf("the project's repo %s has no remote: pass --remote %s=<url>", name, name))
	}
	plan.Targets = append(plan.Targets, root)

	var unknown []string
	for n := range o.Remotes {
		if !used[n] {
			unknown = append(unknown, n)
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		return SharePlan{}, &axi.Error{
			Msg:  fmt.Sprintf("--remote names no repo of the project: %s", strings.Join(unknown, ", ")),
			Code: "VALIDATION_ERROR",
			Help: []string{"The project's own repo is " + name + "; an orchestrator's nested repos go by the names `jig repos` lists"},
		}
	}
	if len(missing) > 0 {
		return SharePlan{}, &axi.Error{
			Msg:  "nothing to push to: " + strings.Join(missing, "; "),
			Code: "VALIDATION_ERROR",
			Help: []string{"Or pass --create to create the repositories through the host's CLI (gh)"},
		}
	}
	if _, err := gitx.ResolveIdentity(o.Root); err != nil {
		return SharePlan{}, err
	}
	return plan, nil
}

// needsBranch refuses a repo that is to be pushed and has no branch checked
// out.
func needsBranch(t ShareTarget) error {
	if !t.Push || t.Branch != "" {
		return nil
	}
	return &axi.Error{
		Msg:  fmt.Sprintf("the %s at %s has no branch checked out", t.Name, t.Dir),
		Code: "VALIDATION_ERROR",
		Help: []string{"Check out the branch to share"},
	}
}

// HostCreate creates a private repository called name on the host and makes
// it the origin remote of the repository at dir.
type HostCreate func(name, dir string) error

// ShareResult is what RunShare did.
type ShareResult struct {
	// StoreRemote is the store's origin, as written into jig.yaml.
	StoreRemote string
	// Remotes are the origins set or created, by repo name.
	Remotes map[string]string
	// Pushed are the repos pushed, with the branch.
	Pushed []string
	// Committed reports that jig.yaml was committed in the project's repo.
	Committed bool
	// Without are the nested repos that still have no remote.
	Without []string
}

// RunShare carries plan out: it sets or creates each remote that needs one,
// writes the store's remote (and, for a nested repo that is not a submodule,
// its remote, which is the only way a collaborator's checkout learns it) into
// jig.yaml, commits jig.yaml in the project's repo as the person, and pushes.
// The caller has confirmed: a push to a remote that is not on this machine
// is made. create makes the repositories --create asked for.
func RunShare(plan SharePlan, create HostCreate) (ShareResult, error) {
	res := ShareResult{Remotes: map[string]string{}, Without: plan.Without}
	targets := append([]ShareTarget(nil), plan.Targets...)

	for i, t := range targets {
		switch {
		case t.Set != "":
			if _, err := gitx.Run(t.Dir, "remote", "add", "origin", t.Set); err != nil {
				return res, fmt.Errorf("project: set the origin of the %s: %w", t.Name, err)
			}
		case t.Create:
			if create == nil {
				return res, &axi.Error{Msg: "no host CLI to create repositories with", Code: "VALIDATION_ERROR"}
			}
			if err := create(t.hostName(plan.Marker), t.Dir); err != nil {
				return res, err
			}
		default:
			continue
		}
		targets[i].Origin = OriginOf(t.Dir)
		if targets[i].Origin == "" {
			return res, fmt.Errorf("project: the %s has no origin after setting it", t.Name)
		}
		res.Remotes[t.Name] = targets[i].Origin
	}

	// jig.yaml: the store's remote, and the remote of each nested clone that
	// .gitmodules does not carry.
	var storeRemote string
	repos := append([]MarkerRepo(nil), plan.Marker.Repos...)
	reposChanged := false
	for _, t := range targets {
		if t.Name == "store" {
			storeRemote = t.Origin
		}
	}
	for i, r := range repos {
		for _, t := range targets {
			if t.Name == RepoName(r) && t.Dir != plan.Root && !t.Submodule && t.Origin != "" && r.Remote != t.Origin {
				repos[i].Remote = t.Origin
				reposChanged = true
			}
		}
	}
	if err := SetMarkerStore(plan.Root, storeRemote); err != nil {
		return res, err
	}
	if reposChanged {
		if err := SetMarkerRepos(plan.Root, repos); err != nil {
			return res, err
		}
	}
	res.StoreRemote = storeRemote

	committed, err := commitMarker(plan.Root)
	if err != nil {
		return res, err
	}
	res.Committed = committed

	var pushLast *ShareTarget
	for i := range targets {
		t := targets[i]
		if !t.Push {
			continue
		}
		if t.Dir == plan.Root {
			pushLast = &targets[i]
			continue
		}
		if err := push(t); err != nil {
			return res, err
		}
		res.Pushed = append(res.Pushed, t.Name+" "+t.Branch)
	}
	if pushLast != nil {
		if err := push(*pushLast); err != nil {
			return res, err
		}
		res.Pushed = append(res.Pushed, pushLast.Name+" "+pushLast.Branch)
	}
	return res, nil
}

// commitMarker commits jig.yaml, and only it, in the repo at root as the
// person, when it differs from what the repo has. It reports whether it
// committed.
func commitMarker(root string) (bool, error) {
	id, err := gitx.ResolveIdentity(root)
	if err != nil {
		return false, err
	}
	if _, err := gitx.Run(root, "add", "--", MarkerFile); err != nil {
		return false, fmt.Errorf("project: stage %s: %w", MarkerFile, err)
	}
	staged, err := gitx.Run(root, "diff", "--cached", "--name-only", "--", MarkerFile)
	if err != nil {
		return false, fmt.Errorf("project: check staged %s: %w", MarkerFile, err)
	}
	if staged == "" {
		// Tracked and unchanged: the repo already has the marker as it is.
		return false, nil
	}
	msg := "jig: record the project's store in " + MarkerFile
	if _, err := gitx.Run(root, "-c", "user.name="+id.Name, "-c", "user.email="+id.Email, "commit", "-m", msg, "--", MarkerFile); err != nil {
		return false, fmt.Errorf("project: commit %s: %w", MarkerFile, err)
	}
	return true, nil
}

// push pushes the branch of t to its origin, as confirmed, and makes it the
// branch's upstream when it has none.
func push(t ShareTarget) error {
	if err := gitx.GuardedPush(t.Dir, "origin", t.Branch, true); err != nil {
		return err
	}
	if _, err := gitx.Run(t.Dir, "rev-parse", "--abbrev-ref", "--symbolic-full-name", t.Branch+"@{upstream}"); err != nil {
		if _, err := gitx.Run(t.Dir, "branch", "--set-upstream-to=origin/"+t.Branch, t.Branch); err != nil {
			return fmt.Errorf("project: set the upstream of %s in %s: %w", t.Branch, t.Dir, err)
		}
	}
	return nil
}

// sameRemote reports whether a and b are one remote written two ways: the
// same text once a trailing ".git" and slashes are dropped and backslashes
// are made slashes.
func sameRemote(a, b string) bool {
	norm := func(s string) string {
		s = strings.ReplaceAll(strings.TrimSpace(s), "\\", "/")
		s = strings.TrimRight(s, "/")
		return strings.TrimSuffix(s, ".git")
	}
	return norm(a) == norm(b)
}

func baseName(p string) string {
	p = strings.TrimRight(strings.ReplaceAll(p, "\\", "/"), "/")
	if i := strings.LastIndex(p, "/"); i >= 0 {
		return p[i+1:]
	}
	return p
}
