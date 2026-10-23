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
	hooksUsageProject   = "the named project of this machine: its .claude/settings.local.json, which git ignores"
	hooksUsageHere      = "the project or repository you are in, as --project does"
	hooksUsageHarness   = "the harness to edit: claude (the default)"
	hooksUsageUninstall = "take jig's entries out instead of putting them in"
	hooksUsageYes       = "write without asking"
)

// init registers this file's verb with the registry (registry.go).
func init() {
	registerVerb(verb{
		Spec: cmdSpec{"hooks", "jig hooks: add or remove jig's hook entries in an agent harness's settings", []flagSpec{
			{"global", hooksUsageGlobal, false},
			{"project", hooksUsageProject, false},
			{"here", hooksUsageHere, false},
			{"harness", hooksUsageHarness, false},
			{"uninstall", hooksUsageUninstall, false},
			{"yes", hooksUsageYes, false},
		}, false},
		Order: 510,
		Run: func(e env, args []string, stdout io.Writer, stdin io.Reader) int {
			return cmdHooks(e, args, stdout, stdin)
		},
	})
}

// harnessClaude is the one harness jig edits today.
const harnessClaude = "claude"

// cmdHooks implements `jig hooks --global|--project <p>|--here [--harness h]
// [--uninstall] [--yes]`: it puts jig's hook entries into the harness's
// settings file for the scope, or takes them out, and touches nothing else in
// the file. It asks before it writes, unless --yes; a caller with no terminal
// to ask on is told to pass it.
func cmdHooks(e env, args []string, stdout io.Writer, stdin io.Reader) int {
	fs := newFlagSet(e, "hooks")
	global := fs.Bool("global", false, hooksUsageGlobal)
	projectName := fs.String("project", "", hooksUsageProject)
	here := fs.Bool("here", false, hooksUsageHere)
	harness := fs.String("harness", harnessClaude, hooksUsageHarness)
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
	if *harness != harnessClaude {
		return renderErr(stdout, &axi.Error{
			Msg:  fmt.Sprintf("harness %q is not supported yet", *harness),
			Code: "VALIDATION_ERROR",
			Help: []string{"Supported: " + harnessClaude},
		})
	}
	kind, err := chooseScope(*global, *projectName, *here)
	if err != nil {
		return renderErr(stdout, err)
	}
	target, err := resolveScope(e, kind, *projectName)
	if err != nil {
		return renderErr(stdout, err)
	}
	path, err := claudeSettingsPath(e, target)
	if err != nil {
		return renderErr(stdout, err)
	}

	command := ""
	if !*uninstall {
		if command, err = jigCommand(e); err != nil {
			return renderErr(stdout, err)
		}
	}
	plan, err := setup.PlanClaude(path, command, *uninstall)
	if err != nil {
		return renderErr(stdout, err)
	}

	scopeLabel := kind
	if kind == scopeProject {
		scopeLabel += " " + *projectName
	}
	report := func(result string, help ...string) int {
		rows := [][2]string{{"harness", harnessClaude}, {"scope", scopeLabel}, {"file", path}, {"result", result}}
		if !*uninstall {
			rows = append(rows, [2]string{"command", setup.ClaudeCommand(command)})
			rows = append(rows, [2]string{"events", claudeEventNames()})
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

	if !*yes {
		summary := hooksSummary(plan, *uninstall)
		if !e.stdinIsTerminal(stdin) {
			rerun := "jig hooks " + scopeFlagsText(kind, *projectName)
			if *uninstall {
				rerun += " --uninstall"
			}
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

	switch {
	case *uninstall && kind != scopeGlobal && plan.LeavesEmptyObject():
		// The file held nothing but jig's entries: it was jig's to begin with.
		if err := os.Remove(path); err != nil {
			return renderErr(stdout, err)
		}
		if _, err := setup.UnexcludeFile(path); err != nil {
			return renderErr(stdout, err)
		}
		_ = os.Remove(filepath.Dir(path)) // only if empty
	default:
		if err := plan.Apply(); err != nil {
			return renderErr(stdout, err)
		}
		if kind != scopeGlobal && !*uninstall {
			if _, err := setup.ExcludeFile(path); err != nil {
				return renderErr(stdout, err)
			}
		}
	}

	if *uninstall {
		return report("removed", "Run `jig hooks "+scopeFlagsText(kind, *projectName)+" --yes` to add them again")
	}
	return report("installed",
		"Start a new session for the harness to read them; Claude Code ignores hooks in a folder it does not trust and under --bare",
	)
}

// claudeSettingsPath is the Claude Code settings file the scope names: the
// user's own for --global, and for a project the settings.local.json under its
// folder, the one Claude Code reads for the person alone and git does not
// share.
func claudeSettingsPath(e env, t scopeTarget) (string, error) {
	if t.Kind == scopeGlobal {
		userHome, err := e.userHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(userHome, ".claude", "settings.json"), nil
	}
	if t.Dir == "" {
		return "", &axi.Error{
			Msg:  fmt.Sprintf("project %q has no folder on this machine: it has no jig.yaml marker and more than one clone", t.Project),
			Code: "NO_PROJECT_ROOT",
			Help: []string{"Run it from inside the project with --here"},
		}
	}
	return filepath.Join(t.Dir, ".claude", "settings.local.json"), nil
}

func claudeEventNames() string {
	var names []string
	for _, h := range setup.ClaudeHooks() {
		names = append(names, h.Event)
	}
	return strings.Join(names, " ")
}

// hooksSummary says in a clause what applying plan would do.
func hooksSummary(plan setup.FilePlan, uninstall bool) string {
	if uninstall {
		return "remove jig's hook entries from " + plan.Path
	}
	return "add jig's hook entries (" + strings.Join(strings.Fields(claudeEventNames()), ", ") + ") to " + plan.Path
}
