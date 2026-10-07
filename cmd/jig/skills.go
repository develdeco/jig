package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"

	"github.com/develdeco/jig/internal/axi"
	skillspkg "github.com/develdeco/jig/skills"
)

// cmdSkills implements `jig skills install [--project] [--dest <dir>]`.
func cmdSkills(e env, args []string, stdout io.Writer) int {
	sub, rest, err := requirePositional(args, "subcommand (install)")
	if err != nil {
		return renderErr(stdout, err)
	}
	if sub == "" {
		// requirePositional passed a leading -h/--help through.
		sub = "install"
	}
	if sub != "install" {
		return renderErr(stdout, &axi.Error{
			Msg:  fmt.Sprintf("unknown skills subcommand %q; only \"install\" is supported", sub),
			Code: "VALIDATION_ERROR",
		})
	}

	fs := newFlagSet(e, "skills install")
	projectFlag := fs.Bool("project", false, "install under ./.claude/skills of the current directory")
	destFlag := fs.String("dest", "", "install under <dir>/<name>/SKILL.md instead of the default location")
	if handled, err := parseFlags(stdout, fs, rest); handled {
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
