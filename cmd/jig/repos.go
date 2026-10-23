package main

import (
	"bufio"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/project"
)

// init registers this file's verbs with the registry (registry.go): `repos`
// lists a project's repos, and `repos add`, `repos remove` and `repos sync`
// keep the list in its jig.yaml.
func init() {
	registerVerb(verb{
		Spec:  cmdSpec{"repos", "list the project's repos and whether each is checked out here", []flagSpec{}, false},
		Order: 400,
		Run: func(e env, args []string, stdout io.Writer, _ io.Reader) int {
			return cmdRepos(e, args, stdout)
		},
	})
	registerVerb(verb{
		Spec: cmdSpec{"repos add", "list a repo nested in the orchestrator: a path to it, or a git URL to fetch into it", []flagSpec{
			{"clone", "fetch a URL as a gitignored clone instead of a submodule", false},
		}, false},
		Order: 401,
		Run: func(e env, args []string, stdout io.Writer, _ io.Reader) int {
			return cmdReposAdd(e, args, stdout)
		},
	})
	registerVerb(verb{
		Spec:  cmdSpec{"repos remove", "take a repo out of the project's list (its folder is left alone)", []flagSpec{}, false},
		Order: 402,
		Run: func(e env, args []string, stdout io.Writer, _ io.Reader) int {
			return cmdReposRemove(e, args, stdout)
		},
	})
	registerVerb(verb{
		Spec: cmdSpec{"repos sync", "make the list match the folder: add the repos nested in it, drop those whose folder is gone", []flagSpec{
			{"yes", "take the defaults without asking (keep a listed submodule that is not checked out here)", false},
		}, false},
		Order: 403,
		Run: func(e env, args []string, stdout io.Writer, stdin io.Reader) int {
			return cmdReposSync(e, args, stdout, stdin)
		},
	})
}

// projectHere is the project the working directory is in, as its jig.yaml
// says: the marker, the directory it is in, and this machine's jig home.
type projectHere struct {
	root    string
	marker  project.Marker
	jigHome string
}

// findProject finds the jig.yaml the working directory is under (passing over
// a member's, which points up at the project). A directory under none is
// refused, naming `jig init`.
func findProject(e env) (projectHere, error) {
	cwd, err := e.getwd()
	if err != nil {
		return projectHere{}, err
	}
	jigHome, err := e.jigHome()
	if err != nil {
		return projectHere{}, err
	}
	root, m, ok, err := project.FindProjectMarker(cwd)
	if err != nil {
		return projectHere{}, err
	}
	if !ok {
		return projectHere{}, &axi.Error{
			Msg:  fmt.Sprintf("no %s found from %s up: this is not in a jig project", project.MarkerFile, cwd),
			Code: "VALIDATION_ERROR",
			Help: []string{"Run `jig init` at the root of the repo or the orchestrator"},
		}
	}
	if m.Project == "" {
		m.Project = filepath.Base(root)
	}
	return projectHere{root: root, marker: m, jigHome: jigHome}, nil
}

// requireOrchestrator refuses a project that is not an orchestrator: only an
// orchestrator keeps a list of repos nested in it.
func (p projectHere) requireOrchestrator(verbName string) error {
	if p.marker.Kind == project.KindOrchestrator {
		return nil
	}
	return &axi.Error{
		Msg:  fmt.Sprintf("%s is for an orchestrator, and %s is a single-repo project", verbName, p.marker.Project),
		Code: "VALIDATION_ERROR",
		Help: []string{"A single-repo project's one repo is the folder holding its jig.yaml"},
	}
}

// cmdRepos implements `jig repos`: the project's repos, and for an
// orchestrator the nested repos its list lacks.
func cmdRepos(e env, args []string, stdout io.Writer) int {
	fs := newFlagSet(e, "repos")
	positionals, handled, err := parseWithPositionals(stdout, fs, args)
	if handled {
		return 0
	} else if err != nil {
		return renderErr(stdout, err)
	}
	if len(positionals) > 0 {
		return renderErr(stdout, &axi.Error{
			Msg:  fmt.Sprintf("jig repos takes no argument, got %q", positionals[0]),
			Code: "VALIDATION_ERROR",
			Help: []string{"Keep the list with `jig repos add`, `jig repos remove` and `jig repos sync`"},
		})
	}
	p, err := findProject(e)
	if err != nil {
		return renderErr(stdout, err)
	}

	kind := p.marker.Kind
	if kind == "" {
		kind = project.KindProject
	}
	blocks := []string{axi.KV("project", [][2]string{{"name", p.marker.Project}, {"kind", kind}, {"root", p.root}})}
	var rows [][]string
	if kind == project.KindOrchestrator {
		for _, s := range project.Statuses(p.root, p.marker.Repos) {
			rows = append(rows, []string{project.RepoName(s.Repo), s.Repo.Path, repoRemoteOf(s), s.State})
		}
	} else {
		cfg := project.WithMarkerRepos(project.Config{}, p.root, p.marker)
		for _, r := range cfg.Repos {
			remote := r.Remote
			if remote == p.root {
				remote = ""
			}
			rows = append(rows, []string{r.Name(), ".", remote, project.RepoCheckedOut})
		}
	}
	blocks = append(blocks, axi.Table("repos", []string{"name", "path", "remote", "state"}, rows))

	var help []string
	if kind == project.KindOrchestrator {
		listed := map[string]bool{}
		for _, r := range p.marker.Repos {
			listed[r.Path] = true
		}
		var unlisted [][]string
		for _, n := range project.NestedRepos(p.root) {
			if !listed[n.Path] {
				unlisted = append(unlisted, []string{n.Name, n.Path})
			}
		}
		if len(unlisted) > 0 {
			blocks = append(blocks, axi.Table("unlisted", []string{"name", "path"}, unlisted))
			help = append(help, "Run `jig repos sync` to list the nested repos the project lacks")
		}
	}
	help = append(help, "Run `jig share` to publish the project: a repo with no remote cannot be fetched by a collaborator")
	blocks = append(blocks, axi.Help(help...))
	axi.Render(stdout, blocks...)
	return 0
}

// repoRemoteOf is the remote a listed repo is known by: the one jig.yaml
// lists, else its clone's origin or its submodule URL, "" when it has none.
func repoRemoteOf(s project.RepoStatus) string {
	if s.Repo.Remote != "" {
		return s.Repo.Remote
	}
	if s.State == project.RepoCheckedOut {
		return project.OriginOf(s.Dir)
	}
	return ""
}

// cmdReposAdd implements `jig repos add <path|url> [--clone]`.
func cmdReposAdd(e env, args []string, stdout io.Writer) int {
	fs := newFlagSet(e, "repos add")
	clone := fs.Bool("clone", false, "fetch a URL as a gitignored clone instead of a submodule")
	positionals, handled, err := parseWithPositionals(stdout, fs, args)
	if handled {
		return 0
	} else if err != nil {
		return renderErr(stdout, err)
	}
	arg, err := exactlyOne(positionals, "jig repos add", "<path|url>")
	if err != nil {
		return renderErr(stdout, err)
	}
	if *clone && !project.IsRepoURL(arg) {
		return renderErr(stdout, &axi.Error{Msg: "--clone is for a URL: a path names a repo that is already here", Code: "VALIDATION_ERROR"})
	}
	p, err := findProject(e)
	if err != nil {
		return renderErr(stdout, err)
	}
	if err := p.requireOrchestrator("jig repos add"); err != nil {
		return renderErr(stdout, err)
	}
	resolved, err := project.ResolveRepoArgs(p.root, []string{arg}, false)
	if err != nil {
		return renderErr(stdout, err)
	}
	ra := resolved[0]
	// Refuse a repo the list has before anything is fetched.
	if _, err := project.MergeRepos(p.marker.Repos, []project.MarkerRepo{ra.Repo}); err != nil {
		return renderErr(stdout, err)
	}
	entry := ra.Repo
	if ra.URL != "" {
		if entry, err = project.FetchRepo(p.root, ra, *clone); err != nil {
			return renderErr(stdout, err)
		}
	}
	list, err := project.MergeRepos(p.marker.Repos, []project.MarkerRepo{entry})
	if err != nil {
		return renderErr(stdout, err)
	}
	if err := project.SetMarkerRepos(p.root, list); err != nil {
		return renderErr(stdout, err)
	}
	statuses := project.Statuses(p.root, []project.MarkerRepo{entry})
	if err := project.MapRepos(p.jigHome, p.marker.Project, "", p.root, statuses); err != nil {
		return renderErr(stdout, err)
	}
	source := "nested"
	if ra.URL != "" {
		source = "submodule"
		if *clone {
			source = "clone"
		}
	}
	axi.Render(stdout,
		axi.KV("repos_add", [][2]string{{"project", p.marker.Project}, {"name", project.RepoName(entry)}, {"path", entry.Path}, {"source", source}, {"state", statuses[0].State}}),
		axi.Help(
			fmt.Sprintf("Commit %s (and .gitmodules) so a clone of the orchestrator lists it", project.MarkerFile),
			"Run `jig repos` to see the project's repos",
		),
	)
	return 0
}

// cmdReposRemove implements `jig repos remove <name>`.
func cmdReposRemove(e env, args []string, stdout io.Writer) int {
	fs := newFlagSet(e, "repos remove")
	positionals, handled, err := parseWithPositionals(stdout, fs, args)
	if handled {
		return 0
	} else if err != nil {
		return renderErr(stdout, err)
	}
	name, err := exactlyOne(positionals, "jig repos remove", "<name>")
	if err != nil {
		return renderErr(stdout, err)
	}
	p, err := findProject(e)
	if err != nil {
		return renderErr(stdout, err)
	}
	if err := p.requireOrchestrator("jig repos remove"); err != nil {
		return renderErr(stdout, err)
	}
	list, gone, err := project.RemoveRepo(p.marker.Repos, name)
	if err != nil {
		return renderErr(stdout, err)
	}
	if err := project.SetMarkerRepos(p.root, list); err != nil {
		return renderErr(stdout, err)
	}
	if err := project.UnmapRepos(p.jigHome, p.marker.Project, []string{name}); err != nil {
		return renderErr(stdout, err)
	}
	help := []string{fmt.Sprintf("Commit %s so a clone of the orchestrator stops listing it", project.MarkerFile)}
	if project.GitmodulesURL(p.root, gone.Path) != "" {
		help = append(help, fmt.Sprintf("%s is a submodule: `git submodule deinit -f %s` and `git rm -f %s` take it out of the orchestrator", gone.Path, gone.Path, gone.Path))
	} else {
		help = append(help, "Its folder was left alone")
	}
	axi.Render(stdout,
		axi.KV("repos_remove", [][2]string{{"project", p.marker.Project}, {"name", name}, {"path", gone.Path}}),
		axi.Help(help...),
	)
	return 0
}

// cmdReposSync implements `jig repos sync [--yes]`: it adds the nested repos
// the list lacks, removes the listed ones whose folder is gone, and asks about
// a listed submodule that is merely not fetched here (--yes, and a terminal-less
// run, keep it).
func cmdReposSync(e env, args []string, stdout io.Writer, stdin io.Reader) int {
	fs := newFlagSet(e, "repos sync")
	yes := fs.Bool("yes", false, "take the defaults without asking (keep a listed submodule that is not checked out here)")
	positionals, handled, err := parseWithPositionals(stdout, fs, args)
	if handled {
		return 0
	} else if err != nil {
		return renderErr(stdout, err)
	}
	if len(positionals) > 0 {
		return renderErr(stdout, &axi.Error{Msg: fmt.Sprintf("jig repos sync takes no argument, got %q", positionals[0]), Code: "VALIDATION_ERROR"})
	}
	p, err := findProject(e)
	if err != nil {
		return renderErr(stdout, err)
	}
	if err := p.requireOrchestrator("jig repos sync"); err != nil {
		return renderErr(stdout, err)
	}

	var drop func(project.MarkerRepo) bool
	if !*yes && e.stdinIsTerminal(stdin) {
		in := bufio.NewReader(stdin)
		drop = func(r project.MarkerRepo) bool {
			fmt.Fprintf(stdout, "%s is a submodule not checked out here. Keep it in the project? [Y/n] ", project.RepoName(r))
			answer, _ := in.ReadString('\n')
			a := strings.ToLower(strings.TrimSpace(answer))
			return a == "n" || a == "no"
		}
	}
	list, rep := project.Sync(p.root, p.marker.Repos, drop)
	if len(rep.Added) > 0 || len(rep.Removed) > 0 {
		if err := project.SetMarkerRepos(p.root, list); err != nil {
			return renderErr(stdout, err)
		}
		if len(rep.Added) > 0 {
			if err := project.MapRepos(p.jigHome, p.marker.Project, "", p.root, project.Statuses(p.root, rep.Added)); err != nil {
				return renderErr(stdout, err)
			}
		}
		var gone []string
		for _, r := range rep.Removed {
			gone = append(gone, project.RepoName(r))
		}
		if len(gone) > 0 {
			if err := project.UnmapRepos(p.jigHome, p.marker.Project, gone); err != nil {
				return renderErr(stdout, err)
			}
		}
	}

	blocks := []string{axi.KV("repos_sync", [][2]string{
		{"project", p.marker.Project},
		{"added", fmt.Sprint(len(rep.Added))},
		{"removed", fmt.Sprint(len(rep.Removed))},
		{"not_here", fmt.Sprint(len(rep.NotHere))},
	})}
	for _, t := range []struct {
		label string
		repos []project.MarkerRepo
	}{{"added", rep.Added}, {"removed", rep.Removed}, {"not_here", rep.NotHere}} {
		if len(t.repos) == 0 {
			continue
		}
		rows := make([][]string, len(t.repos))
		for i, r := range t.repos {
			rows[i] = []string{project.RepoName(r), r.Path}
		}
		blocks = append(blocks, axi.Table(t.label, []string{"name", "path"}, rows))
	}
	var help []string
	if len(rep.Added) > 0 || len(rep.Removed) > 0 {
		help = append(help, fmt.Sprintf("Commit %s so a clone of the orchestrator has the same list", project.MarkerFile))
	} else {
		help = append(help, "The list already matches the folder")
	}
	for _, r := range rep.Removed {
		if project.GitmodulesURL(p.root, r.Path) != "" {
			help = append(help, fmt.Sprintf("%s is still declared in .gitmodules, so the next sync lists it again: `git rm -f %s` takes the submodule out", r.Path, r.Path))
		}
	}
	if len(rep.NotHere) > 0 {
		help = append(help, "`git submodule update --init` fetches the submodules that are not checked out here")
	}
	blocks = append(blocks, axi.Help(help...))
	axi.Render(stdout, blocks...)
	return 0
}
