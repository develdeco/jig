package main

import (
	"bufio"
	"flag"
	"fmt"
	"io"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/project"
	"github.com/develdeco/jig/internal/setup"
)

// jig setup puts jig's pieces where the agents a person uses read them: the
// skills, the harness hooks, git's hooks and the hint. It takes an explicit
// scope (--global, --project <name> or --here) and explicit pieces, and owns
// only what it wrote. jig attach is setup --here for a folder that is already
// a jig project.

const (
	setupUsageGlobal    = "set up for you, on this machine: ~/.claude and ~/.agents skills, harness hooks, git's global hooks, the hint"
	setupUsageProject   = "set up the named project of this machine, in its folder"
	setupUsageHere      = "set up the project or repository you are in"
	setupUsageSkills    = "the project skills (here, intake, chart, build, review, publish): in full globally, as one stub in a project"
	setupUsageHooks     = "the harness hook entries that tell jig what a session does (Claude Code ignores project hooks in an untrusted folder and with --bare)"
	setupUsageGithook   = "git's hooks, so that every commit is recorded"
	setupUsageHint      = "the few lines in CLAUDE.md, AGENTS.md and GEMINI.md that send an agent to jig here"
	setupUsageArtifacts = "all four pieces: skills, hooks, githook and hint"
	setupUsageHelpers   = "the helper skills jig-init and jig-attach, which let an agent start a project or attach a repository"
	setupUsageAll       = "--artifacts and --helpers"
	setupUsageHarness   = "the harnesses the hooks go to: a name, a comma-separated list or all (default claude, or what the project's jig.yaml expects)"
	setupUsageUninstall = "take away what jig installed instead"
	setupUsageYes       = "write the settings and memory files without asking"
	setupUsageTracked   = "with --project or --here, install what a team commits (full skills, .claude/settings.json, AGENTS.md) instead of the untracked form git ignores"
	setupUsageMembers   = "also set up each member repository of an orchestrator project"
	setupUsageCloud     = "print a shell script that installs jig and runs this setup, for a cloud environment's setup script, and write nothing"
)

// init registers this file's verb with the registry (registry.go).
func init() {
	registerVerb(verb{
		Spec:  cmdSpec{"setup", "jig setup: install jig's skills, hooks, git hooks and hint where your agents read them; extra skills by name (retro, platform-sync)", setupFlagSpecs(true), false},
		Order: 500,
		Run: func(e env, args []string, stdout io.Writer, stdin io.Reader) int {
			return cmdSetup(e, args, stdout, stdin)
		},
	})
}

// setupParams is what `jig setup` and `jig attach` were asked for.
type setupParams struct {
	global      bool
	projectName string
	here        bool

	skills, hooks, githook, hint bool
	artifacts, helpers, all      bool

	harness   string
	uninstall bool
	yes       bool
	tracked   bool
	members   bool
	cloud     bool
	names     []string
	// byDefault says the pieces were not asked for: attach's own default, so
	// the command that repeats the run need not spell them out.
	byDefault bool
}

// setupFlag is one flag of setup or attach: its name, usage and which verbs
// take it.
type setupFlag struct {
	name, usage string
	// scope is a scope flag, and cloud the cloud flag: attach takes neither.
	scope, cloud bool
}

func setupFlagTable() []setupFlag {
	return []setupFlag{
		{"global", setupUsageGlobal, true, false},
		{"project", setupUsageProject, true, false},
		{"here", setupUsageHere, true, false},
		{"skills", setupUsageSkills, false, false},
		{"hooks", setupUsageHooks, false, false},
		{"githook", setupUsageGithook, false, false},
		{"hint", setupUsageHint, false, false},
		{"artifacts", setupUsageArtifacts, false, false},
		{"helpers", setupUsageHelpers, false, false},
		{"all", setupUsageAll, false, false},
		{"harness", setupUsageHarness, false, false},
		{"uninstall", setupUsageUninstall, false, false},
		{"yes", setupUsageYes, false, false},
		{"tracked", setupUsageTracked, false, false},
		{"members", setupUsageMembers, false, false},
		{"cloud", setupUsageCloud, false, true},
	}
}

// setupFlagSpecs is the help row's flags: every flag of setup, or those of
// attach (setup's less the scope flags and --cloud).
func setupFlagSpecs(setupVerb bool) []flagSpec {
	var specs []flagSpec
	for _, f := range setupFlagTable() {
		if !setupVerb && (f.scope || f.cloud) {
			continue
		}
		specs = append(specs, flagSpec{f.name, f.usage, false})
	}
	return specs
}

// bindSetupFlags registers the flags of setup (or of attach) on fs.
func bindSetupFlags(fs *flag.FlagSet, p *setupParams, setupVerb bool) {
	for _, f := range setupFlagTable() {
		if !setupVerb && (f.scope || f.cloud) {
			continue
		}
		switch f.name {
		case "global":
			fs.BoolVar(&p.global, f.name, false, f.usage)
		case "project":
			fs.StringVar(&p.projectName, f.name, "", f.usage)
		case "here":
			fs.BoolVar(&p.here, f.name, false, f.usage)
		case "skills":
			fs.BoolVar(&p.skills, f.name, false, f.usage)
		case "hooks":
			fs.BoolVar(&p.hooks, f.name, false, f.usage)
		case "githook":
			fs.BoolVar(&p.githook, f.name, false, f.usage)
		case "hint":
			fs.BoolVar(&p.hint, f.name, false, f.usage)
		case "artifacts":
			fs.BoolVar(&p.artifacts, f.name, false, f.usage)
		case "helpers":
			fs.BoolVar(&p.helpers, f.name, false, f.usage)
		case "all":
			fs.BoolVar(&p.all, f.name, false, f.usage)
		case "harness":
			fs.StringVar(&p.harness, f.name, "", f.usage)
		case "uninstall":
			fs.BoolVar(&p.uninstall, f.name, false, f.usage)
		case "yes":
			fs.BoolVar(&p.yes, f.name, false, f.usage)
		case "tracked":
			fs.BoolVar(&p.tracked, f.name, false, f.usage)
		case "members":
			fs.BoolVar(&p.members, f.name, false, f.usage)
		case "cloud":
			fs.BoolVar(&p.cloud, f.name, false, f.usage)
		}
	}
}

// what the params ask for, with the sets spelled out.
func (p setupParams) wantSkills() bool  { return p.skills || p.artifacts || p.all }
func (p setupParams) wantHooks() bool   { return p.hooks || p.artifacts || p.all }
func (p setupParams) wantGithook() bool { return p.githook || p.artifacts || p.all }
func (p setupParams) wantHint() bool    { return p.hint || p.artifacts || p.all }
func (p setupParams) wantHelpers() bool { return p.helpers || p.all }

func (p setupParams) anyPiece() bool {
	return p.wantSkills() || p.wantHooks() || p.wantGithook() || p.wantHint() || p.wantHelpers() || len(p.names) > 0
}

// selection is the skills the params name: the project set for --skills (and
// the sets that hold it), the helpers for --helpers, the positionals as
// extras.
func (p setupParams) selection() skillSelection {
	return skillSelection{project: p.wantSkills(), helpers: p.wantHelpers(), extras: p.names}
}

func (p setupParams) wantSkillSteps() bool {
	return p.wantSkills() || p.wantHelpers() || len(p.names) > 0
}

// command is the command that repeats the params, for a refusal that tells the
// person what to run again.
func (p setupParams) command(verb string, withYes bool) string {
	parts := []string{"jig", verb}
	if p.global {
		parts = append(parts, "--global")
	}
	if p.projectName != "" {
		parts = append(parts, "--project", p.projectName)
	}
	if p.here && verb == "setup" {
		parts = append(parts, "--here")
	}
	for _, f := range []struct {
		on   bool
		name string
	}{
		{p.skills, "--skills"}, {p.hooks, "--hooks"}, {p.githook, "--githook"}, {p.hint, "--hint"},
		{p.artifacts && !p.byDefault, "--artifacts"}, {p.helpers, "--helpers"}, {p.all, "--all"},
	} {
		if f.on {
			parts = append(parts, f.name)
		}
	}
	if p.harness != "" {
		parts = append(parts, "--harness", p.harness)
	}
	if p.tracked {
		parts = append(parts, "--tracked")
	}
	if p.members {
		parts = append(parts, "--members")
	}
	if p.uninstall {
		parts = append(parts, "--uninstall")
	}
	if withYes {
		parts = append(parts, "--yes")
	}
	return strings.Join(append(parts, p.names...), " ")
}

// cmdSetup implements `jig setup`.
func cmdSetup(e env, args []string, stdout io.Writer, stdin io.Reader) int {
	fs := newFlagSet(e, "setup")
	var p setupParams
	bindSetupFlags(fs, &p, true)
	names, handled, err := parseWithPositionals(stdout, fs, args)
	if handled {
		return 0
	}
	if err != nil {
		return renderErr(stdout, err)
	}
	p.names = names
	if p.cloud {
		return setupCloud(stdout, p)
	}
	return runSetup(e, "setup", p, stdout, stdin)
}

// setupCloud prints the script a cloud environment runs to get jig and its
// pieces: the environment has no home to keep them in between sessions, so it
// installs the binary and sets up globally, every time. It writes nothing.
func setupCloud(stdout io.Writer, p setupParams) int {
	if p.global || p.projectName != "" || p.here || p.uninstall || p.tracked || p.members || p.yes {
		return renderErr(stdout, &axi.Error{
			Msg:  "--cloud prints one script and takes only the pieces and --harness",
			Code: "VALIDATION_ERROR",
		})
	}
	for _, n := range p.names {
		if _, ok := embeddedBody(n); !ok {
			return renderErr(stdout, &axi.Error{Msg: fmt.Sprintf("jig has no skill named %q", n), Code: "VALIDATION_ERROR",
				Help: []string{"Skills this jig carries: " + strings.Join(embeddedNames(), ", ")}})
		}
	}
	if _, err := parseHarnesses(p.harness, nil); err != nil {
		return renderErr(stdout, err)
	}
	if !p.anyPiece() {
		p.artifacts, p.helpers = true, true
	}
	p.global = true
	fmt.Fprintf(stdout, `#!/bin/sh
# jig in a cloud environment: use this as the environment's setup script.
# It installs the jig binary, then sets jig up for the user the agent runs as.
# Put $HOME/.local/bin on the PATH the agent runs with: the hook entries call jig.
set -eu
curl -fsSL https://raw.githubusercontent.com/develdeco/jig/main/scripts/install.sh | sh
PATH="$HOME/.local/bin:$PATH"
export PATH
%s
`, p.command("setup", true))
	return 0
}

// runSetup is `jig setup` and `jig attach` once their flags are read: fill in
// what is missing (ask on a terminal, else say what to choose), plan every
// step, ask once for the files that are the person's, and make the changes.
func runSetup(e env, verbName string, p setupParams, stdout io.Writer, stdin io.Reader) int {
	tty := e.stdinIsTerminal(stdin)
	rd := bufio.NewReader(stdin)

	// What can be refused before anything is asked is refused first.
	if _, err := parseHarnesses(p.harness, nil); err != nil {
		return renderErr(stdout, err)
	}
	if p.wantSkillSteps() {
		if _, _, err := p.selection().resolve(); err != nil {
			return renderErr(stdout, err)
		}
	}

	missingScope := !p.global && p.projectName == "" && !p.here
	missingPieces := !p.anyPiece()
	if missingScope || missingPieces {
		if !tty {
			var help []string
			if missingScope {
				help = append(help,
					"Where: --global (you, on this machine), --project <name> (one of this machine's projects) or --here (the project or repository you are in)")
			}
			if missingPieces {
				help = append(help,
					"What: --skills --hooks --githook --hint, or a set: --artifacts (those four), --helpers (jig-init and jig-attach), --all; extra skills by name")
			}
			help = append(help, "For example: jig setup --global --artifacts --yes")
			return renderErr(stdout, &axi.Error{Msg: "say where jig is set up and what is set up", Code: axi.NeedsInput, Help: help})
		}
		if err := promptSetup(e, rd, stdout, &p, missingScope, missingPieces); err != nil {
			return renderErr(stdout, err)
		}
	}

	kind, err := chooseScope(p.global, p.projectName, p.here)
	if err != nil {
		return renderErr(stdout, err)
	}
	if err := checkTracked(kind, p.tracked); err != nil {
		return renderErr(stdout, err)
	}
	target, err := resolveScope(e, kind, p.projectName)
	if err != nil {
		return renderErr(stdout, err)
	}
	targets := []scopeTarget{target}
	if p.members {
		if kind == scopeGlobal {
			return renderErr(stdout, &axi.Error{Msg: "--members is for --project and --here", Code: "VALIDATION_ERROR"})
		}
		members, err := memberTargets(target)
		if err != nil {
			return renderErr(stdout, err)
		}
		targets = append(targets, members...)
	}
	hooksHarnesses, err := parseHarnesses(p.harness, defaultHarnesses(target))
	if err != nil {
		return renderErr(stdout, err)
	}
	// The hint goes to the memory files of the harnesses in use unless
	// --harness names them; a --harness of harnesses with none (cursor, say)
	// leaves it out.
	userHome, err := e.userHomeDir()
	if err != nil {
		return renderErr(stdout, err)
	}
	hintHs := defaultHintHarnesses(userHome)
	if p.harness != "" {
		hintHs = nil
		for _, h := range hooksHarnesses {
			if slices.Contains(setup.HintHarnesses(), h.Name) {
				hintHs = append(hintHs, h.Name)
			}
		}
	}

	var steps []step
	for _, t := range targets {
		req := installReq{kind: kind, projectName: p.projectName, target: t, tracked: p.tracked, uninstall: p.uninstall}
		if p.wantSkillSteps() {
			s, err := skillsSteps(e, req, p.selection())
			if err != nil {
				return renderErr(stdout, err)
			}
			steps = append(steps, s...)
		}
		if p.wantHooks() {
			s, err := hooksSteps(e, req, hooksHarnesses)
			if err != nil {
				return renderErr(stdout, err)
			}
			steps = append(steps, s...)
		}
		if p.wantGithook() {
			s, err := githookSteps(e, req)
			if err != nil {
				return renderErr(stdout, err)
			}
			steps = append(steps, s...)
		}
		if p.wantHint() {
			s, err := hintSteps(e, req, hintHs)
			if err != nil {
				return renderErr(stdout, err)
			}
			steps = append(steps, s...)
		}
	}

	redo := p
	redo.uninstall = false
	return runPiece(runOpts{
		label: verbName, scope: scopeLabelOf(kind, p.projectName),
		uninstall: p.uninstall, yes: p.yes, tty: tty, rd: rd,
		again: p.command(verbName, true), undo: redo.command(verbName, true),
	}, steps, stdout)
}

// defaultHarnesses are the harnesses the hooks go to when --harness names none:
// those the project's jig.yaml expects, else Claude Code.
func defaultHarnesses(t scopeTarget) []string {
	if t.Dir != "" {
		if m, err := project.LoadMarker(filepath.Join(t.Dir, project.MarkerFile)); err == nil && m.Expects != nil {
			var names []string
			for _, n := range m.Expects.Harness {
				if _, ok := setup.HarnessNamed(n); ok {
					names = append(names, n)
				}
			}
			if len(names) > 0 {
				return names
			}
		}
	}
	return []string{harnessClaude}
}

// promptSetup asks, on a terminal, for the scope and the pieces the flags left
// out.
func promptSetup(e env, rd *bufio.Reader, stdout io.Writer, p *setupParams, scope, pieces bool) error {
	ask := func(def string) string {
		fmt.Fprintf(stdout, "Choice [%s]: ", def)
		answer, _ := rd.ReadString('\n')
		if a := strings.TrimSpace(answer); a != "" {
			return a
		}
		return def
	}
	if scope {
		var names []string
		if root, err := e.jigHome(); err == nil {
			if machine, err := project.LoadMachine(root); err == nil {
				for n := range machine {
					names = append(names, n)
				}
				sort.Strings(names)
			}
		}
		fmt.Fprintln(stdout, "Where should jig be set up?")
		fmt.Fprintln(stdout, "  global - for you, on this machine")
		fmt.Fprintln(stdout, "  here   - the project or repository you are in")
		if len(names) > 0 {
			fmt.Fprintln(stdout, "  or the name of a project of this machine: "+strings.Join(names, ", "))
		}
		switch a := ask("global"); a {
		case "g", "global":
			p.global = true
		case "h", "here":
			p.here = true
		default:
			p.projectName = a
		}
	}
	if pieces {
		fmt.Fprintln(stdout, "What should it set up?")
		fmt.Fprintln(stdout, "  artifacts - skills, hooks, git hooks and the hint")
		fmt.Fprintln(stdout, "  helpers   - the jig-init and jig-attach skills")
		fmt.Fprintln(stdout, "  all       - both")
		switch a := ask("artifacts"); a {
		case "a", "artifacts":
			p.artifacts = true
		case "h", "helpers":
			p.helpers = true
		case "all":
			p.all = true
		default:
			return &axi.Error{Msg: fmt.Sprintf("%q is not one of artifacts, helpers, all", a), Code: "VALIDATION_ERROR"}
		}
	}
	return nil
}
