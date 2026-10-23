package main

import (
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/home"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/ledger"
	"github.com/develdeco/jig/internal/setup"
)

const (
	githookUsageGlobal    = "every repository of this machine: point git's global core.hooksPath at jig's wrappers"
	githookUsageProject   = "the clones of the named project of this machine"
	githookUsageHere      = "the repository you are in"
	githookUsageUninstall = "remove what jig installed instead"
)

// init registers this file's verbs with the registry (registry.go): the
// installer, and the hook git runs, which is hidden.
func init() {
	registerVerb(verb{
		Spec: cmdSpec{"githook", "jig githook: record every commit of this machine, through git's hooks", []flagSpec{
			{"global", githookUsageGlobal, false},
			{"project", githookUsageProject, false},
			{"here", githookUsageHere, false},
			{"uninstall", githookUsageUninstall, false},
		}, false},
		Order: 511,
		Run: func(e env, args []string, stdout io.Writer, _ io.Reader) int {
			return cmdGithook(e, args, stdout)
		},
	})
	registerVerb(verb{
		Spec:  cmdSpec{"githook post-commit", "record the commit just made in the ledger (git runs it)", nil, true},
		Order: 512,
		Run: func(e env, args []string, stdout io.Writer, _ io.Reader) int {
			return cmdGithookPostCommit(e, stdout)
		},
		NoFlagSet: true,
	})
}

// cmdGithook implements `jig githook --global|--project <p>|--here
// [--uninstall]`. --global points git's global core.hooksPath at jig's
// wrappers, which run each repository's own hooks and the hook that was set
// before, and add jig's step to post-commit. --project and --here do not
// touch git's config: they make sure each clone records its commits, covered by
// the wrappers or with a block in its own post-commit.
func cmdGithook(e env, args []string, stdout io.Writer) int {
	fs := newFlagSet(e, "githook")
	global := fs.Bool("global", false, githookUsageGlobal)
	projectName := fs.String("project", "", githookUsageProject)
	here := fs.Bool("here", false, githookUsageHere)
	uninstall := fs.Bool("uninstall", false, githookUsageUninstall)
	if handled, err := parseFlags(stdout, fs, args); handled {
		return 0
	} else if err != nil {
		return renderErr(stdout, err)
	}
	if fs.NArg() > 0 {
		return renderErr(stdout, &axi.Error{Msg: fmt.Sprintf("unexpected argument %q", fs.Arg(0)), Code: "VALIDATION_ERROR"})
	}
	kind, err := chooseScope(*global, *projectName, *here)
	if err != nil {
		return renderErr(stdout, err)
	}
	target, err := resolveScope(e, kind, *projectName)
	if err != nil {
		return renderErr(stdout, err)
	}
	jigHome, err := e.jigHome()
	if err != nil {
		return renderErr(stdout, err)
	}
	command := ""
	if !*uninstall {
		if command, err = jigCommand(e); err != nil {
			return renderErr(stdout, err)
		}
	}
	hooksDir := home.HooksDir(jigHome)
	g := gitConfig(e)

	if kind == scopeGlobal {
		return githookGlobal(g, hooksDir, command, *uninstall, stdout)
	}
	return githookClones(g, target, hooksDir, command, kind, *projectName, *uninstall, stdout)
}

func githookGlobal(g setup.GitConfig, hooksDir, command string, uninstall bool, stdout io.Writer) int {
	var (
		res setup.GlobalResult
		err error
	)
	if uninstall {
		res, err = g.UninstallGlobal(hooksDir)
	} else {
		res, err = g.InstallGlobal(hooksDir, command)
	}
	if err != nil {
		return renderErr(stdout, err)
	}
	result := "unchanged"
	switch {
	case res.Changed && uninstall:
		result = "removed"
	case res.Changed:
		result = "installed"
	}
	previous := res.Previous
	if previous == "" {
		previous = "none"
	}
	axi.Render(stdout,
		axi.KV("githook", [][2]string{
			{"scope", scopeGlobal},
			{"hooks_dir", hooksDir},
			{"core.hooksPath", res.Config},
			{"previous", previous},
			{"wrappers_written", fmt.Sprint(len(res.Wrote))},
			{"result", result},
		}),
		axi.Help(githookGlobalHelp(uninstall)),
	)
	return 0
}

func githookGlobalHelp(uninstall bool) string {
	if uninstall {
		return "Run `jig githook --global` to record commits again"
	}
	return "A repository that sets its own core.hooksPath is not covered: run `jig githook --here` in it. Undo with `jig githook --global --uninstall`"
}

func githookClones(g setup.GitConfig, t scopeTarget, hooksDir, command, kind, projectName string, uninstall bool, stdout io.Writer) int {
	if len(t.Clones) == 0 {
		return renderErr(stdout, &axi.Error{
			Msg:  "there is no git repository in that scope",
			Code: "NOT_IN_PROJECT",
			Help: []string{"Run it from inside a repository, or use --global"},
		})
	}
	var (
		rows    [][]string
		changed bool
	)
	for _, clone := range t.Clones {
		var (
			res setup.CloneResult
			err error
		)
		if uninstall {
			res, err = g.UninstallClone(clone, hooksDir)
		} else {
			res, err = g.InstallClone(clone, hooksDir, command)
		}
		if err != nil {
			return renderErr(stdout, err)
		}
		changed = changed || res.Changed
		detail := res.Path
		if res.Reason != "" {
			detail = res.Reason
		}
		rows = append(rows, []string{clone, res.State, detail})
	}
	result := "unchanged"
	switch {
	case changed && uninstall:
		result = "removed"
	case changed:
		result = "installed"
	}
	scopeLabel := kind
	if kind == scopeProject {
		scopeLabel += " " + projectName
	}
	axi.Render(stdout,
		axi.KV("githook", [][2]string{{"scope", scopeLabel}, {"result", result}}),
		axi.Table("clones", []string{"clone", "state", "detail"}, rows),
		axi.Help("Run `jig githook "+scopeFlagsText(kind, projectName)+githookUndoFlag(uninstall)+"` to undo or redo it"),
	)
	return 0
}

func githookUndoFlag(uninstall bool) string {
	if uninstall {
		return ""
	}
	return " --uninstall"
}

// cmdGithookPostCommit is the hook git runs after a commit, from jig's wrapper
// or from the block in a repository's own post-commit: it records the commit in
// the machine ledger as a git-commit line with the commit, the directory and the
// branch. It exits 0 whatever happens, since a record of a commit must never
// fail the commit; what went wrong, if anything, is said on stdout, which git
// shows beside the commit.
func cmdGithookPostCommit(e env, stdout io.Writer) int {
	if err := recordGitCommit(e); err != nil {
		fmt.Fprintf(stdout, "jig githook post-commit: %v\n", err)
	}
	return 0
}

func recordGitCommit(e env) error {
	cwd, err := e.getwd()
	if err != nil {
		return err
	}
	root, err := e.jigHome()
	if err != nil {
		return err
	}
	sha, err := gitx.RevParse(cwd, "HEAD")
	if err != nil {
		return err
	}
	// A detached HEAD has no branch: the line then carries none.
	branch, _ := gitx.Run(cwd, "branch", "--show-current")
	return ledger.Append(root, journal.Line{
		TS:     e.now().UTC().Format(time.RFC3339Nano),
		Event:  journal.EventGitCommit,
		Commit: sha,
		Cwd:    cwd,
		Branch: strings.TrimSpace(branch),
	})
}
