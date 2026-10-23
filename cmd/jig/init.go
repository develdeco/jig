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

// init registers this file's verb with the registry (registry.go).
func init() {
	registerVerb(verb{
		Spec: cmdSpec{"init", "define the project here: a single repo, or an orchestrator over the repos nested in it", []flagSpec{
			{"standalone", "make this folder a single-repo project (what a folder holding no nested repos is)", false},
			{"orchestrator", "make this folder an orchestrator over the repos nested in it (what a folder holding some is)", false},
			{"clone", "fetch each URL as a gitignored clone instead of a submodule", false},
			{"yes", "take the defaults without asking (make a plain folder a git repository)", false},
			{"key", "area key the store declares; default: derived from the project name", false},
		}, false},
		Order: 100,
		Run: func(e env, args []string, stdout io.Writer, stdin io.Reader) int {
			return cmdInit(e, args, stdout, stdin)
		},
	})
}

// cmdInit implements `jig init [--standalone|--orchestrator] [--clone] [--yes]
// [--key K] [<path|url>...]`: it defines the project at the working directory,
// which must be a project's root (decision 21 of the digester chart). It
// creates the store as a sibling repository and writes the jig.yaml marker; it
// publishes nothing, and commits nothing in the folder.
func cmdInit(e env, args []string, stdout io.Writer, stdin io.Reader) int {
	fs := newFlagSet(e, "init")
	standalone := fs.Bool("standalone", false, "make this folder a single-repo project (what a folder holding no nested repos is)")
	orchestrator := fs.Bool("orchestrator", false, "make this folder an orchestrator over the repos nested in it (what a folder holding some is)")
	clone := fs.Bool("clone", false, "fetch each URL as a gitignored clone instead of a submodule")
	yes := fs.Bool("yes", false, "take the defaults without asking (make a plain folder a git repository)")
	keyFlag := fs.String("key", "", "area key the store declares; default: derived from the project name")
	repoArgs, handled, err := parseWithPositionals(stdout, fs, args)
	if handled {
		return 0
	} else if err != nil {
		return renderErr(stdout, err)
	}
	if *standalone && *orchestrator {
		return renderErr(stdout, &axi.Error{Msg: "--standalone and --orchestrator are two answers to one question: pick one", Code: "VALIDATION_ERROR"})
	}
	if *standalone && len(repoArgs) > 0 {
		return renderErr(stdout, &axi.Error{Msg: "--standalone takes no repos: the folder is the repo", Code: "VALIDATION_ERROR"})
	}

	dir, err := e.getwd()
	if err != nil {
		return renderErr(stdout, err)
	}
	jigHome, err := e.jigHome()
	if err != nil {
		return renderErr(stdout, err)
	}
	if err := project.CheckInitDir(dir); err != nil {
		return renderErr(stdout, err)
	}

	kind := project.KindProject
	switch {
	case *orchestrator:
		kind = project.KindOrchestrator
	case *standalone:
	case len(repoArgs) > 0:
		// Repos named on the command line can only be an orchestrator's.
		kind = project.KindOrchestrator
		axi.Render(stdout, axi.KV("picked", [][2]string{{"mode", "orchestrator"}, {"why", "repos were named"}}))
	default:
		inf := project.InferKind(dir)
		kind = inf.Kind
		axi.Render(stdout, axi.KV("picked", [][2]string{{"mode", modeName(kind)}, {"why", inf.Reason}}))
	}

	if kind == project.KindOrchestrator && !project.IsRepo(dir) && !*yes {
		if !e.stdinIsTerminal(stdin) {
			return renderErr(stdout, &axi.Error{
				Msg:  fmt.Sprintf("nothing written: %s is not a git repository, and an orchestrator is one: init would run git init there", dir),
				Code: axi.NeedsInput,
				Help: []string{"Run `jig init --orchestrator --yes` to do it"},
			})
		}
		fmt.Fprintf(stdout, "%s is not a git repository; an orchestrator is one.\nRun git init there? [y/N] ", dir)
		answer, _ := bufio.NewReader(stdin).ReadString('\n')
		if a := strings.ToLower(strings.TrimSpace(answer)); a != "y" && a != "yes" {
			fmt.Fprintln(stdout)
			return renderErr(stdout, &axi.Error{Msg: "nothing written: init was declined", Code: axi.NeedsInput})
		}
	}

	res, err := project.Init(project.InitOpts{
		Dir:      dir,
		JigHome:  jigHome,
		Kind:     kind,
		Key:      *keyFlag,
		Repos:    repoArgs,
		Clone:    *clone,
		MakeRepo: true,
	})
	if err != nil {
		return renderErr(stdout, err)
	}

	rows := [][2]string{{"mode", modeName(res.Kind)}, {"project", res.Name}, {"store", res.Store}, {"marker", res.Marker}}
	if res.MadeGit {
		rows = append(rows, [2]string{"git", "initialized"})
	}
	blocks := []string{axi.KV("init", rows)}
	plainNested := false
	if res.Kind == project.KindOrchestrator {
		fetched := map[string]bool{}
		for _, n := range res.Fetched {
			fetched[n] = true
		}
		repoRows := make([][]string, len(res.Repos))
		for i, r := range res.Repos {
			source := "nested"
			switch {
			case r.Submodule:
				source = "submodule"
			case fetched[project.RepoName(r.Repo)]:
				source = "fetched"
			default:
				plainNested = plainNested || r.State == project.RepoCheckedOut
			}
			repoRows[i] = []string{project.RepoName(r.Repo), r.Repo.Path, source, r.State}
		}
		blocks = append(blocks, axi.Table("repos", []string{"name", "path", "source", "state"}, repoRows))
	}
	help := []string{fmt.Sprintf("Commit %s in %s so a clone of it finds the project", project.MarkerFile, filepath.Base(res.Dir))}
	if len(res.Fetched) > 0 && !*clone {
		help = append(help, "Commit .gitmodules too: it carries the submodules")
	}
	if plainNested {
		help = append(help, "Repos nested as they are show up as untracked folders in the orchestrator's git status: ignore them in .gitignore, or `git submodule add` them")
	}
	help = append(help,
		"Run `jig ticket new --title \"...\"` to mint the first ticket",
		"Run `jig share` when you want collaborators: until then the project is local",
	)
	blocks = append(blocks, axi.Help(help...))
	axi.Render(stdout, blocks...)
	return 0
}

// modeName is the word init prints for a kind of project.
func modeName(kind string) string {
	if kind == project.KindOrchestrator {
		return "orchestrator"
	}
	return "standalone"
}
