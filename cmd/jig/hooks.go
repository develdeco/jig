package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/setup"
)

const (
	hooksUsageGlobal    = "your own settings, for every project: ~/.claude/settings.json"
	hooksUsageProject   = "the named project of this machine: its .claude/settings.local.json, which git ignores (Claude Code ignores project hooks in an untrusted folder and with --bare)"
	hooksUsageHere      = "the project or repository you are in, as --project does"
	hooksUsageHarness   = "the harness to edit: claude (the default), codex, gemini, cursor, copilot or opencode"
	hooksUsageTracked   = "with --project or --here, edit the file the project shares and commits (Claude Code's .claude/settings.json) and do not hide it from git"
	hooksUsageUninstall = "take jig's entries out instead of putting them in"
	hooksUsageYes       = "write without asking"
)

// init registers this file's verb with the registry (registry.go).
func init() {
	registerVerb(verb{
		Spec: cmdSpec{"hooks", "jig hooks: add or remove jig's hook entries in an agent harness's settings (Claude Code ignores them in an untrusted folder and with --bare)", []flagSpec{
			{"global", hooksUsageGlobal, false},
			{"project", hooksUsageProject, false},
			{"here", hooksUsageHere, false},
			{"harness", hooksUsageHarness, false},
			{"tracked", hooksUsageTracked, false},
			{"uninstall", hooksUsageUninstall, false},
			{"yes", hooksUsageYes, false},
		}, false},
		Order: 510,
		Run: func(e env, args []string, stdout io.Writer, stdin io.Reader) int {
			return cmdHooks(e, args, stdout, stdin)
		},
	})
}

// harnessClaude is the harness `jig hooks` edits when none is named.
const harnessClaude = "claude"

// cmdHooks implements `jig hooks --global|--project <p>|--here [--harness h]
// [--tracked] [--uninstall] [--yes]`: it puts jig's hook entries into the
// harness's settings file for the scope, or takes them out, and touches
// nothing else in the file. It asks before it writes, unless --yes; a caller
// with no terminal to ask on is told to pass it.
func cmdHooks(e env, args []string, stdout io.Writer, stdin io.Reader) int {
	fs := newFlagSet(e, "hooks")
	global := fs.Bool("global", false, hooksUsageGlobal)
	projectName := fs.String("project", "", hooksUsageProject)
	here := fs.Bool("here", false, hooksUsageHere)
	harness := fs.String("harness", harnessClaude, hooksUsageHarness)
	tracked := fs.Bool("tracked", false, hooksUsageTracked)
	uninstall := fs.Bool("uninstall", false, hooksUsageUninstall)
	yes := fs.Bool("yes", false, hooksUsageYes)
	if handled, err := parseFlags(stdout, fs, args); handled {
		return 0
	} else if err != nil {
		return renderErr(stdout, err)
	}
	if fs.NArg() > 0 {
		return renderErr(stdout, &axi.Error{Msg: fmt.Sprintf("unexpected argument %q", fs.Arg(0)), Code: "VALIDATION_ERROR"})
	}
	h, ok := setup.HarnessNamed(*harness)
	if !ok {
		return renderErr(stdout, &axi.Error{
			Msg:  fmt.Sprintf("harness %q is not supported", *harness),
			Code: "VALIDATION_ERROR",
			Help: []string{"Supported: " + strings.Join(setup.HarnessNames(), ", ")},
		})
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

	command := ""
	if !*uninstall {
		if command, err = jigCommand(e); err != nil {
			return renderErr(stdout, err)
		}
	}
	hp, err := planHooks(e, target, h, command, *tracked, *uninstall)
	if err != nil {
		return renderErr(stdout, err)
	}
	plan := hp.Plan

	scopeLabel := kind
	if kind == scopeProject {
		scopeLabel += " " + *projectName
	}
	report := func(result string, help ...string) int {
		rows := [][2]string{{"harness", h.Name}, {"scope", scopeLabel}, {"file", hp.Path}, {"result", result}}
		if !*uninstall {
			rows = append(rows, [2]string{"command", setup.ObserveCommand(command, h.Name)})
			rows = append(rows, [2]string{"events", strings.Join(h.Events, " ")})
		}
		axi.Render(stdout, axi.KV("hooks", rows), axi.Help(help...))
		return 0
	}

	if !plan.Changed() {
		if *uninstall {
			return report("unchanged", "jig has no hook entries in that file")
		}
		return report("unchanged", "Start a new session for the harness to read them")
	}

	rerun := "jig hooks " + scopeFlagsText(kind, *projectName)
	if h.Name != harnessClaude {
		rerun += " --harness " + h.Name
	}
	if *tracked {
		rerun += " --tracked"
	}
	if *uninstall {
		rerun += " --uninstall"
	}
	if !*yes {
		summary := hooksSummary(plan, *uninstall)
		if !e.stdinIsTerminal(stdin) {
			return renderErr(stdout, &axi.Error{
				Msg:  "nothing written: it would " + summary,
				Code: axi.NeedsInput,
				Help: []string{"Run `" + rerun + " --yes` to do it"},
			})
		}
		fmt.Fprintf(stdout, "This will %s.\nApply? [y/N] ", summary)
		answer, _ := bufio.NewReader(stdin).ReadString('\n')
		if a := strings.ToLower(strings.TrimSpace(answer)); a != "y" && a != "yes" {
			fmt.Fprintln(stdout)
			return report("declined", "Nothing was written")
		}
	}

	if err := hp.apply(*uninstall); err != nil {
		return renderErr(stdout, err)
	}
	if *uninstall {
		return report("removed", "Run `"+strings.TrimSuffix(rerun, " --uninstall")+" --yes` to add them again")
	}
	return report("installed", h.Note)
}

// checkTracked refuses --tracked for a scope that has no project to commit
// to.
func checkTracked(kind string, tracked bool) error {
	if tracked && kind == scopeGlobal {
		return &axi.Error{
			Msg:  "--tracked is for --project and --here: a global file belongs to no repository",
			Code: "VALIDATION_ERROR",
		}
	}
	return nil
}

// hookPlan is a change to one harness's hook file for a scope, worked out and
// not yet made.
type hookPlan struct {
	Harness setup.Harness
	// Path is the file: the person's own for --global, and for a project the
	// one under its folder (the settings.local.json Claude Code reads for the
	// person alone, unless tracked).
	Path string
	Plan setup.FilePlan
	// Scope is the scope the plan is for.
	Scope   scopeTarget
	Tracked bool
}

// planHooks works out installing jig's entries for harness h into the file the
// scope names, or taking them out. command is the program part of the hook
// command (jigCommand), unused when uninstalling.
func planHooks(e env, t scopeTarget, h setup.Harness, command string, tracked, uninstall bool) (hookPlan, error) {
	hp := hookPlan{Harness: h, Scope: t, Tracked: tracked}
	if t.Kind == scopeGlobal {
		userHome, err := e.userHomeDir()
		if err != nil {
			return hp, err
		}
		hp.Path = h.GlobalFile(userHome)
	} else {
		dir, err := projectDir(t)
		if err != nil {
			return hp, err
		}
		hp.Path = h.ProjectFile(dir, tracked)
	}
	plan, err := h.Plan(hp.Path, command, uninstall)
	if err != nil {
		return hp, err
	}
	hp.Plan = plan
	return hp, nil
}

// apply makes the planned change. A project's file that jig made is kept out
// of git status; one that held nothing but jig's entries is deleted by an
// uninstall, with its line in the exclude file and the directories that were
// left empty. A person's global settings file is never deleted.
func (p hookPlan) apply(uninstall bool) error {
	global := p.Scope.Kind == scopeGlobal
	switch {
	case p.Plan.Delete, uninstall && !global && p.Plan.LeavesEmptyObject():
		if p.Plan.Delete {
			if err := p.Plan.Apply(); err != nil {
				return err
			}
		} else if err := os.Remove(p.Path); err != nil {
			return err
		}
		if global {
			_ = os.Remove(filepath.Dir(p.Path)) // only if empty
			return nil
		}
		if _, err := setup.UnexcludeFile(p.Path); err != nil {
			return err
		}
		setup.PruneEmptyDirs(filepath.Dir(p.Path), p.Scope.Dir)
		return nil
	}
	if err := p.Plan.Apply(); err != nil {
		return err
	}
	if !global && !uninstall && !p.Tracked {
		if _, err := setup.ExcludeFile(p.Path); err != nil {
			return err
		}
	}
	return nil
}

// hooksSummary says in a clause what applying plan would do.
func hooksSummary(plan setup.FilePlan, uninstall bool) string {
	if uninstall {
		return "remove jig's hook entries from " + plan.Path
	}
	return "add jig's hook entries to " + plan.Path
}
