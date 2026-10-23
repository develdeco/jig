package main

import (
	"bufio"
	"fmt"
	"io"

	"github.com/develdeco/jig/internal/axi"
)

const (
	hintUsageGlobal    = "your own memory files: ~/.claude/CLAUDE.md, and ~/.codex/AGENTS.md and ~/.gemini/GEMINI.md for the harnesses you have a directory for"
	hintUsageProject   = "the named project of this machine: a CLAUDE.local.md in its folder, which git ignores"
	hintUsageHere      = "the project or repository you are in, as --project does"
	hintUsageHarness   = "with --global, these memory files whether or not the harness has a directory: claude, codex, gemini (a name, a comma-separated list or all)"
	hintUsageTracked   = "with --project or --here, write the AGENTS.md the project shares (and a CLAUDE.md that does not import it) and do not hide it from git"
	hintUsageUninstall = "take jig's block out instead of putting it in"
	hintUsageYes       = "write without asking"
)

// init registers this file's verb with the registry (registry.go).
func init() {
	registerVerb(verb{
		Spec: cmdSpec{"hint", "jig hint: put a few lines in the memory files agents read that send them to jig here", []flagSpec{
			{"global", hintUsageGlobal, false},
			{"project", hintUsageProject, false},
			{"here", hintUsageHere, false},
			{"harness", hintUsageHarness, false},
			{"tracked", hintUsageTracked, false},
			{"uninstall", hintUsageUninstall, false},
			{"yes", hintUsageYes, false},
		}, false},
		Order: 513,
		Run: func(e env, args []string, stdout io.Writer, stdin io.Reader) int {
			return cmdHint(e, args, stdout, stdin)
		},
	})
}

// cmdHint implements `jig hint --global|--project <p>|--here [--harness h]
// [--tracked] [--uninstall] [--yes]`: a marked block in the memory files an
// agent reads at the start of a session. The files are the person's, so it
// asks before it writes, as jig hooks does.
func cmdHint(e env, args []string, stdout io.Writer, stdin io.Reader) int {
	fs := newFlagSet(e, "hint")
	global := fs.Bool("global", false, hintUsageGlobal)
	projectName := fs.String("project", "", hintUsageProject)
	here := fs.Bool("here", false, hintUsageHere)
	harness := fs.String("harness", "", hintUsageHarness)
	tracked := fs.Bool("tracked", false, hintUsageTracked)
	uninstall := fs.Bool("uninstall", false, hintUsageUninstall)
	yes := fs.Bool("yes", false, hintUsageYes)
	if handled, err := parseFlags(stdout, fs, args); handled {
		return 0
	} else if err != nil {
		return renderErr(stdout, err)
	}
	if fs.NArg() > 0 {
		return renderErr(stdout, &axi.Error{Msg: fmt.Sprintf("unexpected argument %q", fs.Arg(0)), Code: "VALIDATION_ERROR"})
	}
	userHome, err := e.userHomeDir()
	if err != nil {
		return renderErr(stdout, err)
	}
	harnesses, err := hintHarnesses(*harness, userHome)
	if err != nil {
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
	steps, err := hintSteps(e, installReq{kind: kind, projectName: *projectName, target: target, tracked: *tracked, uninstall: *uninstall}, harnesses)
	if err != nil {
		return renderErr(stdout, err)
	}

	cmd := "jig hint " + scopeFlagsText(kind, *projectName)
	if *harness != "" {
		cmd += " --harness " + *harness
	}
	if *tracked {
		cmd += " --tracked"
	}
	return runPiece(runOpts{
		label: "hint", scope: scopeLabelOf(kind, *projectName), uninstall: *uninstall,
		yes: *yes, tty: e.stdinIsTerminal(stdin), rd: bufio.NewReader(stdin),
		again: cmd + uninstallSuffix(*uninstall) + " --yes", undo: cmd + " --yes",
	}, steps, stdout)
}

// uninstallSuffix is the flag that makes a command an uninstall.
func uninstallSuffix(uninstall bool) string {
	if uninstall {
		return " --uninstall"
	}
	return ""
}
