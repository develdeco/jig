package main

import (
	"bufio"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/develdeco/jig/internal/axi"
	skillspkg "github.com/develdeco/jig/skills"
)

const (
	skillsUsageGlobal    = "for you, on this machine: the skills in full under ~/.claude/skills and ~/.agents/skills"
	skillsUsageProject   = "the named project of this machine: one stub in its .claude/skills and .agents/skills that git ignores"
	skillsUsageHere      = "the project or repository you are in, as --project does"
	skillsUsageHelpers   = "the helper skills jig-init and jig-attach instead of the project set"
	skillsUsageAll       = "the project set and the helpers"
	skillsUsageTracked   = "with --project or --here, the skills in full for the team to commit, not the stub git ignores"
	skillsUsageUninstall = "remove the skills jig installed (those named, else all of them) instead"
)

const (
	skillsInstallUsageProject = "install under ./.claude/skills of the current directory"
	skillsInstallUsageDest    = "install under <dir>/<name>/SKILL.md instead of the default location"
)

// init registers this file's verbs with the registry (registry.go): the piece
// verb, and skills install, the form that predates it.
func init() {
	registerVerb(verb{
		Spec: cmdSpec{"skills", "jig skills: install the project skills (or the helpers, or any by name) for you or a project; the older install subcommand writes every skill with no scope", []flagSpec{
			{"global", skillsUsageGlobal, false},
			{"project", skillsUsageProject, false},
			{"here", skillsUsageHere, false},
			{"helpers", skillsUsageHelpers, false},
			{"all", skillsUsageAll, false},
			{"tracked", skillsUsageTracked, false},
			{"uninstall", skillsUsageUninstall, false},
		}, false},
		Order: 210,
		Run: func(e env, args []string, stdout io.Writer, _ io.Reader) int {
			return cmdSkills(e, args, stdout)
		},
	})
	registerVerb(verb{
		Spec: cmdSpec{"skills install", "install every skill under ~/.claude/skills (--project: ./.claude/skills)", []flagSpec{
			{"project", skillsInstallUsageProject, false},
			{"dest", skillsInstallUsageDest, false},
		}, true},
		Order: 211,
		Run: func(e env, args []string, stdout io.Writer, _ io.Reader) int {
			return cmdSkills(e, append([]string{"install"}, args...), stdout)
		},
		FlagSet: "skills install",
	})
}

// cmdSkills implements `jig skills`: the skills piece for a scope. `skills
// install` is the older form, which writes every skill without a scope.
func cmdSkills(e env, args []string, stdout io.Writer) int {
	if len(args) > 0 && args[0] == "install" {
		return cmdSkillsInstall(e, args[1:], stdout)
	}
	fs := newFlagSet(e, "skills")
	global := fs.Bool("global", false, skillsUsageGlobal)
	projectName := fs.String("project", "", skillsUsageProject)
	here := fs.Bool("here", false, skillsUsageHere)
	helpers := fs.Bool("helpers", false, skillsUsageHelpers)
	all := fs.Bool("all", false, skillsUsageAll)
	tracked := fs.Bool("tracked", false, skillsUsageTracked)
	uninstall := fs.Bool("uninstall", false, skillsUsageUninstall)
	names, handled, err := parseWithPositionals(stdout, fs, args)
	if handled {
		return 0
	}
	if err != nil {
		return renderErr(stdout, err)
	}
	// A name that is no skill is refused before the scope is asked for.
	sel := skillSelection{project: (!*helpers && len(names) == 0) || *all, helpers: *helpers || *all, extras: names}
	if _, _, err := sel.resolve(); err != nil {
		return renderErr(stdout, err)
	}
	kind, err := chooseScope(*global, *projectName, *here)
	if err != nil {
		return renderErr(stdout, err)
	}
	if err := checkTracked(kind, *tracked); err != nil {
		return renderErr(stdout, err)
	}
	target, err := resolveScope(e, kind, *projectName)
	if err != nil {
		return renderErr(stdout, err)
	}
	steps, err := skillsSteps(e, installReq{kind: kind, projectName: *projectName, target: target, tracked: *tracked, uninstall: *uninstall}, sel)
	if err != nil {
		return renderErr(stdout, err)
	}
	cmd := "jig skills " + scopeFlagsText(kind, *projectName)
	for _, f := range []struct {
		on   bool
		name string
	}{{*helpers, " --helpers"}, {*all, " --all"}, {*tracked, " --tracked"}} {
		if f.on {
			cmd += f.name
		}
	}
	if len(names) > 0 {
		cmd += " " + strings.Join(names, " ")
	}
	return runPiece(runOpts{
		label: "skills", scope: scopeLabelOf(kind, *projectName), uninstall: *uninstall,
		yes: true, rd: bufio.NewReader(strings.NewReader("")),
		again: cmd + uninstallSuffix(*uninstall), undo: cmd,
	}, steps, stdout)
}

// cmdSkillsInstall implements `jig skills install [--project] [--dest <dir>]`,
// the form that writes every embedded skill unstamped, into the Claude Code
// skills directory of the person, of the current directory, or of --dest.
func cmdSkillsInstall(e env, args []string, stdout io.Writer) int {
	fs := newFlagSet(e, "skills install")
	projectFlag := fs.Bool("project", false, skillsInstallUsageProject)
	destFlag := fs.String("dest", "", skillsInstallUsageDest)
	if handled, err := parseFlags(stdout, fs, args); handled {
		return 0
	} else if err != nil {
		return renderErr(stdout, err)
	}

	root, err := skillsDestRoot(e, *projectFlag, *destFlag)
	if err != nil {
		return renderErr(stdout, err)
	}

	// Skills are written under the absolute root, so a relative --dest lands
	// under e's working directory; the table names each file as the root was
	// given.
	writeRoot, err := e.abs(root)
	if err != nil {
		return renderErr(stdout, err)
	}
	installed, err := installSkills(writeRoot)
	if err != nil {
		return renderErr(stdout, err)
	}

	var rows [][]string
	for _, ins := range installed {
		rows = append(rows, []string{ins.Name, filepath.Join(root, ins.Name, "SKILL.md")})
	}
	axi.Render(stdout,
		axi.Table("installed", []string{"name", "path"}, rows),
		axi.Help("Run `jig` to see every command"),
	)
	return 0
}

// skillsDestRoot resolves the directory skill subdirectories are installed
// under: an explicit --dest wins; otherwise --project resolves to
// ./.claude/skills under the current directory; otherwise it defaults to
// <user home>/.claude/skills.
func skillsDestRoot(e env, projectFlag bool, destFlag string) (string, error) {
	if destFlag != "" {
		return destFlag, nil
	}
	if projectFlag {
		cwd, err := e.getwd()
		if err != nil {
			return "", err
		}
		return filepath.Join(cwd, ".claude", "skills"), nil
	}
	home, err := e.userHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".claude", "skills"), nil
}

// installedSkill is one skill written to disk by installSkills.
type installedSkill struct {
	Name string
	Path string
}

// installSkills copies every embedded skills/<name>/SKILL.md into
// root/<name>/SKILL.md, creating directories as needed. It overwrites any
// file already there, so installing a newer jig updates them in place.
func installSkills(root string) ([]installedSkill, error) {
	entries, err := skillspkg.FS.ReadDir(".")
	if err != nil {
		return nil, err
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)

	installed := make([]installedSkill, 0, len(names))
	for _, name := range names {
		data, err := skillspkg.FS.ReadFile(name + "/SKILL.md")
		if err != nil {
			return nil, err
		}
		dir := filepath.Join(root, name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return nil, err
		}
		dest := filepath.Join(dir, "SKILL.md")
		if err := os.WriteFile(dest, data, 0o644); err != nil {
			return nil, err
		}
		installed = append(installed, installedSkill{Name: name, Path: dest})
	}
	return installed, nil
}
