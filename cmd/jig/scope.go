package main

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/project"
	"github.com/develdeco/jig/internal/setup"
)

// The commands that install jig's pieces (hooks, githook, and the setup that
// calls them) take one scope each, always spelled out: --global for the whole
// machine, --project <name> for one of its projects, --here for the project or
// repository the working directory is in. Nothing is installed from a guess.

const (
	scopeGlobal  = "global"
	scopeProject = "project"
	scopeHere    = "here"
)

// scopeTarget is what a scope names on this machine.
type scopeTarget struct {
	Kind string
	// Project is the project's name under --project.
	Project string
	// Dir is the folder that holds the project's own files (its .claude
	// directory, say): the project root, the folder of its jig.yaml marker,
	// or the repository itself for a project that has no marker. Empty for
	// --global, and when a --project has no root this machine knows.
	Dir string
	// Clones are the git repositories in scope: the project's clones, or the
	// one the working directory is in. Empty for --global.
	Clones []string
}

// chooseScope checks that exactly one of the scope flags is given and returns
// which. Given none, it asks, in the register of a command that waits for an
// answer, so a caller without a terminal can read the choices.
func chooseScope(global bool, projectName string, here bool) (string, error) {
	var kinds []string
	if global {
		kinds = append(kinds, scopeGlobal)
	}
	if projectName != "" {
		kinds = append(kinds, scopeProject)
	}
	if here {
		kinds = append(kinds, scopeHere)
	}
	switch len(kinds) {
	case 1:
		return kinds[0], nil
	case 0:
		return "", &axi.Error{
			Msg:  "no scope given: say where it goes",
			Code: axi.NeedsInput,
			Help: []string{
				"--global: for you, on this machine",
				"--project <name>: one of this machine's projects",
				"--here: the project or repository you are in",
			},
		}
	}
	return "", &axi.Error{
		Msg:  fmt.Sprintf("give one scope, not %d: %s", len(kinds), strings.Join(kinds, " and ")),
		Code: "VALIDATION_ERROR",
	}
}

// resolveScope finds what kind names. A --project is looked up in this
// machine's project map; --here looks for a jig.yaml marker walking up from the
// working directory, and for the git repository around it.
func resolveScope(e env, kind, projectName string) (scopeTarget, error) {
	t := scopeTarget{Kind: kind}
	switch kind {
	case scopeGlobal:
		return t, nil
	case scopeProject:
		root, err := e.jigHome()
		if err != nil {
			return t, err
		}
		machine, err := project.LoadMachine(root)
		if err != nil {
			return t, err
		}
		mp, ok := machine[projectName]
		if !ok {
			names := make([]string, 0, len(machine))
			for n := range machine {
				names = append(names, n)
			}
			sort.Strings(names)
			ae := &axi.Error{Msg: fmt.Sprintf("this machine has no project %q", projectName), Code: "VALIDATION_ERROR"}
			if len(names) > 0 {
				ae.Help = []string{"Projects on this machine: " + strings.Join(names, ", ")}
			}
			return t, ae
		}
		t.Project = projectName
		clones := make([]string, 0, len(mp.Clones))
		for _, name := range sortedKeys(mp.Clones) {
			clones = append(clones, mp.Clones[name])
		}
		t.Clones = clones
		switch {
		case mp.Root != "":
			t.Dir = mp.Root
		case len(clones) == 1:
			t.Dir = clones[0]
		}
		return t, nil
	case scopeHere:
		cwd, err := e.getwd()
		if err != nil {
			return t, err
		}
		markerDir, _, hasMarker, err := project.FindMarker(cwd)
		if err != nil {
			return t, err
		}
		top, topErr := gitx.TopLevel(cwd)
		if !hasMarker && topErr != nil {
			return t, &axi.Error{
				Msg:  "the working directory is in no jig project and no git repository",
				Code: "NOT_IN_PROJECT",
				Help: []string{"Run it from inside a project, or use --global or --project <name>"},
			}
		}
		if topErr == nil {
			t.Clones = []string{top}
		}
		if hasMarker {
			t.Dir = markerDir
		} else {
			t.Dir = top
		}
		return t, nil
	}
	return t, fmt.Errorf("unknown scope %q", kind)
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// scopeFlagsText is the scope flags as they were given, for the command a
// refusal tells the caller to run.
func scopeFlagsText(kind, projectName string) string {
	switch kind {
	case scopeGlobal:
		return "--global"
	case scopeProject:
		return "--project " + projectName
	}
	return "--here"
}

// jigCommand is the program part of every hook command this binary installs
// (setup.JigCommand): the bare word jig when the jig a shell finds on PATH is
// this very binary, so the entry survives an upgrade in place, otherwise this
// binary's absolute path in quotes.
func jigCommand(e env) (string, error) {
	exe, err := e.executable()
	if err != nil {
		return "", fmt.Errorf("find the running jig binary: %w", err)
	}
	onPath := false
	if found, err := e.lookPath("jig"); err == nil && sameFile(found, exe) {
		onPath = true
	}
	cmd, err := setup.JigCommand(exe, onPath)
	if err != nil {
		return "", &axi.Error{
			Msg:  err.Error(),
			Code: "BINARY_NAME",
			Help: []string{"Run the binary as jig (put it on PATH under that name)"},
		}
	}
	return cmd, nil
}

// sameFile reports whether a and b are one file.
func sameFile(a, b string) bool {
	ia, err := os.Stat(a)
	if err != nil {
		return false
	}
	ib, err := os.Stat(b)
	if err != nil {
		return false
	}
	return os.SameFile(ia, ib)
}

// gitConfig is the git config access the installers use: the variables that
// pick which config files git reads, taken from e like every other variable a
// command reads, so a test hands the command a config file of its own.
func gitConfig(e env) setup.GitConfig {
	var vars []string
	for _, k := range []string{"GIT_CONFIG_GLOBAL", "GIT_CONFIG_SYSTEM", "GIT_CONFIG_NOSYSTEM"} {
		if v := e.getenv(k); v != "" {
			vars = append(vars, k+"="+v)
		}
	}
	return setup.GitConfig{Env: vars}
}
