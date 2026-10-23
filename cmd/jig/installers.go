package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"runtime/debug"
	"sort"
	"strings"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/home"
	"github.com/develdeco/jig/internal/project"
	"github.com/develdeco/jig/internal/setup"
	skillspkg "github.com/develdeco/jig/skills"
)

// What the installer verbs share: the skill sets, and the steps that put each
// piece in place. A step is planned before anything is written, so `jig setup`
// can ask once for everything it is about to do to a file that is the person's
// and then make every change.

// The words a step's result takes.
const (
	resultInstalled = "installed"
	resultRemoved   = "removed"
	resultUnchanged = "unchanged"
	resultSkipped   = "skipped"
	resultDeclined  = "declined"
)

// step is one change an installer verb makes.
type step struct {
	// piece is skills, hooks, githook or hint; harness is the harness it is
	// for ("" when it has none); target is the file or directory it puts
	// things in.
	piece, harness, target string
	// consent says the step edits a file that is the person's (a settings
	// file, a memory file), so it needs a yes before it writes. pending says
	// it would change something, summary says what in a clause.
	consent, pending bool
	summary          string
	// detail is what the report says beside the result; note, when set,
	// replaces it once the step has run.
	detail string
	note   func() string
	// apply makes the change and says what it did: installed, removed,
	// unchanged or skipped.
	apply func() (string, error)
}

// installReq is what an installer verb was asked to do, once its flags were
// read and its scope resolved.
type installReq struct {
	kind        string
	projectName string
	target      scopeTarget
	tracked     bool
	uninstall   bool
}

// projectDir is the folder the project's own files go in, or the refusal that
// says the scope names none on this machine.
func projectDir(t scopeTarget) (string, error) {
	if t.Dir == "" {
		return "", &axi.Error{
			Msg:  fmt.Sprintf("project %q has no folder on this machine: it has no jig.yaml marker and more than one clone", t.Project),
			Code: "NO_PROJECT_ROOT",
			Help: []string{"Run it from inside the project with --here"},
		}
	}
	return t.Dir, nil
}

// binaryVersion is the version of the running jig, as `jig version` shows it:
// what a skill or stub is stamped with, and what doctor compares it against.
func binaryVersion() string {
	info, _ := debug.ReadBuildInfo()
	return formatVersion(info)
}

// parseHarnesses reads a --harness value: one name, a comma-separated list, or
// all. An empty value is def.
func parseHarnesses(value string, def []string) ([]setup.Harness, error) {
	names := def
	if v := strings.TrimSpace(value); v != "" {
		names = nil
		for _, n := range strings.Split(v, ",") {
			if n = strings.TrimSpace(n); n == "all" {
				names = append(names, setup.HarnessNames()...)
			} else if n != "" {
				names = append(names, n)
			}
		}
	}
	var out []setup.Harness
	seen := map[string]bool{}
	for _, n := range names {
		if seen[n] {
			continue
		}
		seen[n] = true
		h, ok := setup.HarnessNamed(n)
		if !ok {
			return nil, &axi.Error{
				Msg:  fmt.Sprintf("harness %q is not supported", n),
				Code: "VALIDATION_ERROR",
				Help: []string{"Supported: " + strings.Join(setup.HarnessNames(), ", ") + ", or all"},
			}
		}
		out = append(out, h)
	}
	return out, nil
}

// --- the skill sets ---

// projectSkillNames are the skills a project gets: the ones that run its work.
func projectSkillNames() []string {
	return []string{"here", "intake", "build", "review", "publish"}
}

// helperSkillNames are the skills that set jig up: they let an agent in any
// folder start a project or attach a repository to one.
func helperSkillNames() []string { return []string{"jig-init", "jig-attach"} }

var skillNameRe = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// embeddedNames lists the skills the binary carries, sorted.
func embeddedNames() []string {
	entries, err := skillspkg.FS.ReadDir(".")
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names
}

// embeddedBody is the SKILL.md of the embedded skill called name.
func embeddedBody(name string) ([]byte, bool) {
	if !skillNameRe.MatchString(name) {
		return nil, false
	}
	data, err := skillspkg.FS.ReadFile(name + "/SKILL.md")
	return data, err == nil
}

// embeddedBodies maps every embedded skill to its text, for telling the files
// `jig skills install` wrote from a person's own.
func embeddedBodies() map[string][]byte {
	out := map[string][]byte{}
	for _, n := range embeddedNames() {
		if b, ok := embeddedBody(n); ok {
			out[n] = b
		}
	}
	return out
}

// skillSelection is which skills a request names: the project set, the
// helpers, and any others by name.
type skillSelection struct {
	project, helpers bool
	extras           []string
}

func (s skillSelection) empty() bool { return !s.project && !s.helpers && len(s.extras) == 0 }

// resolve returns the selected skills the binary carries, each once, in the
// order project set, helpers, extras. A name from a set the binary does not
// carry yet (build, say, before the build skill lands) comes back in lacking;
// an extra it does not carry is a refusal.
func (s skillSelection) resolve() (have []setup.Skill, lacking []string, err error) {
	seen := map[string]bool{}
	add := func(name string, required bool) error {
		if seen[name] {
			return nil
		}
		seen[name] = true
		body, ok := embeddedBody(name)
		switch {
		case ok:
			have = append(have, setup.Skill{Name: name, Body: body})
		case required:
			return &axi.Error{
				Msg:  fmt.Sprintf("jig has no skill named %q", name),
				Code: "VALIDATION_ERROR",
				Help: []string{"Skills this jig carries: " + strings.Join(embeddedNames(), ", ")},
			}
		default:
			lacking = append(lacking, name)
		}
		return nil
	}
	if s.project {
		for _, n := range projectSkillNames() {
			if err := add(n, false); err != nil {
				return nil, nil, err
			}
		}
	}
	if s.helpers {
		for _, n := range helperSkillNames() {
			if err := add(n, false); err != nil {
				return nil, nil, err
			}
		}
	}
	for _, n := range s.extras {
		if err := add(n, true); err != nil {
			return nil, nil, err
		}
	}
	return have, lacking, nil
}

func skillNames(skills []setup.Skill) []string {
	out := make([]string, len(skills))
	for i, s := range skills {
		out[i] = s.Name
	}
	return out
}

func lackingNote(lacking []string) string {
	if len(lacking) == 0 {
		return ""
	}
	return "not in this jig yet: " + strings.Join(lacking, ", ")
}

// skillRoots are the skills directories of a scope: Claude Code's and the
// shared agents one, under the person's home or under the project folder.
func skillRoots(e env, req installReq) (roots []string, dir string, err error) {
	if req.kind == scopeGlobal {
		userHome, err := e.userHomeDir()
		if err != nil {
			return nil, "", err
		}
		return []string{filepath.Join(userHome, ".claude", "skills"), filepath.Join(userHome, ".agents", "skills")}, "", nil
	}
	dir, err = projectDir(req.target)
	if err != nil {
		return nil, "", err
	}
	return []string{filepath.Join(dir, ".claude", "skills"), filepath.Join(dir, ".agents", "skills")}, dir, nil
}

// skillsSteps plans the skills for a scope. The person's home and a tracked
// project get each skill's text in full. A project's untracked form gets one
// stub, which lists the skills and sends the agent to `jig skill <name>` for
// their text, and is kept out of git status.
func skillsSteps(e env, req installReq, sel skillSelection) ([]step, error) {
	roots, dir, err := skillRoots(e, req)
	if err != nil {
		return nil, err
	}
	stub := req.kind != scopeGlobal && !req.tracked
	version := binaryVersion()

	// What an uninstall removes: the stub, else the skills it is told to name,
	// else every skill under the roots that is jig's.
	var (
		skills  []setup.Skill
		lacking []string
		detail  string
	)
	switch {
	case req.uninstall:
		if _, _, err := (skillSelection{extras: sel.extras}).resolve(); err != nil {
			return nil, err
		}
	default:
		if skills, lacking, err = sel.resolve(); err != nil {
			return nil, err
		}
		detail = fmt.Sprintf("%d skills: %s", len(skills), strings.Join(skillNames(skills), ", "))
		if stub {
			entries := make([]setup.StubEntry, len(skills))
			for i, s := range skills {
				entries[i] = setup.StubEntry{Name: s.Name, Summary: setup.SkillSummary(s.Body)}
			}
			detail = fmt.Sprintf("a stub that lists %d skills (%s) and sends the agent to jig skill <name>", len(skills), strings.Join(skillNames(skills), ", "))
			skills = []setup.Skill{{Name: setup.StubSkillName, Body: setup.StubBody(entries)}}
		}
		if note := lackingNote(lacking); note != "" {
			detail += "; " + note
		}
	}

	var steps []step
	for _, root := range roots {
		root := root
		s := step{piece: "skills", target: root, detail: detail}
		var keptNote string
		s.note = func() string {
			if keptNote != "" {
				return keptNote
			}
			return s.detail
		}
		switch {
		case req.uninstall:
			s.apply = func() (string, error) {
				names := []string(nil)
				if stub {
					names = []string{setup.StubSkillName}
				} else if len(sel.extras) > 0 {
					names = sel.extras
				}
				removed, err := setup.RemoveSkills(root, names, embeddedBodies())
				if err != nil {
					return "", err
				}
				changed := len(removed) > 0
				if dir != "" {
					if stub {
						if c, err := setup.UnexcludeFile(filepath.Join(root, setup.StubSkillName, "SKILL.md")); err != nil {
							return "", err
						} else if c {
							changed = true
						}
					}
					setup.PruneEmptyDirs(root, dir)
				}
				if changed {
					return resultRemoved, nil
				}
				return resultUnchanged, nil
			}
		default:
			s.apply = func() (string, error) {
				written, kept, err := setup.InstallSkills(root, skills, version, !stub)
				if err != nil {
					return "", err
				}
				if len(kept) > 0 {
					keptNote = filepath.Join(root, kept[0], "SKILL.md") + " is a skill of your own, not jig's: left as it is"
					return resultSkipped, nil
				}
				changed := len(written) > 0
				if stub {
					if c, err := setup.ExcludeFile(filepath.Join(root, setup.StubSkillName, "SKILL.md")); err != nil {
						return "", err
					} else if c {
						changed = true
					}
				}
				if changed {
					return resultInstalled, nil
				}
				return resultUnchanged, nil
			}
		}
		steps = append(steps, s)
	}
	return steps, nil
}

// defaultHintHarnesses are the harnesses whose memory file gets the hint when
// none is named: Claude Code's always, and the others' when the person has a
// directory for the harness, so jig does not make one for a tool they do not
// use.
func defaultHintHarnesses(userHome string) []string {
	out := []string{"claude"}
	for _, name := range setup.HintHarnesses() {
		if name == "claude" {
			continue
		}
		if h, ok := setup.HarnessNamed(name); ok {
			if info, err := os.Stat(h.ConfigDir(userHome)); err == nil && info.IsDir() {
				out = append(out, name)
			}
		}
	}
	return out
}

// hintHarnesses picks the harnesses whose memory file gets the hint: the ones
// --harness names that have one, or those defaultHintHarnesses finds. A
// --harness that names none of them is a refusal.
func hintHarnesses(value, userHome string) ([]string, error) {
	if strings.TrimSpace(value) == "" {
		return defaultHintHarnesses(userHome), nil
	}
	hs, err := parseHarnesses(value, nil)
	if err != nil {
		return nil, err
	}
	var out []string
	for _, h := range hs {
		for _, n := range setup.HintHarnesses() {
			if h.Name == n {
				out = append(out, n)
			}
		}
	}
	if len(out) == 0 {
		return nil, &axi.Error{
			Msg:  fmt.Sprintf("the hint is written for %s; --harness %s names none of them", strings.Join(setup.HintHarnesses(), ", "), value),
			Code: "VALIDATION_ERROR",
		}
	}
	return out, nil
}

// hintSteps plans the hint. Globally it goes in each harness's memory file in
// the person's home. In a project it goes in CLAUDE.local.md, which git
// ignores, or, tracked, in the AGENTS.md the project shares (and in a CLAUDE.md
// that does not import it).
func hintSteps(e env, req installReq, harnesses []string) ([]step, error) {
	type file struct{ harness, path string }
	var files []file
	if req.kind == scopeGlobal {
		userHome, err := e.userHomeDir()
		if err != nil {
			return nil, err
		}
		for _, h := range harnesses {
			files = append(files, file{h, setup.HintFile(h, userHome)})
		}
	} else {
		dir, err := projectDir(req.target)
		if err != nil {
			return nil, err
		}
		files = append(files, file{"claude", setup.ProjectHintFile(dir, req.tracked)})
		if req.tracked {
			if data, err := os.ReadFile(filepath.Join(dir, "CLAUDE.md")); err == nil && !strings.Contains(string(data), "@AGENTS.md") {
				files = append(files, file{"claude", filepath.Join(dir, "CLAUDE.md")})
			}
		}
	}
	var steps []step
	for _, f := range files {
		f := f
		plan, err := setup.PlanHint(f.path, req.uninstall)
		if err != nil {
			return nil, err
		}
		verb := "add jig's hint to "
		if req.uninstall {
			verb = "remove jig's hint from "
		}
		steps = append(steps, step{
			piece: "hint", harness: f.harness, target: f.path,
			consent: true, pending: plan.Changed(), summary: verb + f.path,
			detail: fmt.Sprintf("~%d tokens in every session", setup.HintTokens()),
			apply: func() (string, error) {
				if !plan.Changed() {
					return resultUnchanged, nil
				}
				if err := plan.Apply(); err != nil {
					return "", err
				}
				if req.kind != scopeGlobal && !req.tracked {
					if req.uninstall {
						_, err = setup.UnexcludeFile(f.path)
					} else {
						_, err = setup.ExcludeFile(f.path)
					}
					if err != nil {
						return "", err
					}
				}
				if req.uninstall {
					return resultRemoved, nil
				}
				return resultInstalled, nil
			},
		})
	}
	return steps, nil
}

// hooksSteps plans the hook entries of each harness for a scope.
func hooksSteps(e env, req installReq, hs []setup.Harness) ([]step, error) {
	command := ""
	if !req.uninstall {
		var err error
		if command, err = jigCommand(e); err != nil {
			return nil, err
		}
	}
	var steps []step
	for _, h := range hs {
		hp, err := planHooks(e, req.target, h, command, req.tracked, req.uninstall)
		if err != nil {
			return nil, err
		}
		detail := ""
		if !req.uninstall {
			detail = h.Note
		}
		steps = append(steps, step{
			piece: "hooks", harness: h.Name, target: hp.Path,
			consent: true, pending: hp.Plan.Changed(), summary: hooksSummary(hp.Plan, req.uninstall),
			detail: detail,
			apply: func() (string, error) {
				if !hp.Plan.Changed() {
					return resultUnchanged, nil
				}
				if err := hp.apply(req.uninstall); err != nil {
					return "", err
				}
				if req.uninstall {
					return resultRemoved, nil
				}
				return resultInstalled, nil
			},
		})
	}
	return steps, nil
}

// githookSteps plans git's side: the global wrappers for --global, and for
// each clone in scope the block or the cover that makes it record its commits.
func githookSteps(e env, req installReq) ([]step, error) {
	jigHome, err := e.jigHome()
	if err != nil {
		return nil, err
	}
	command := ""
	if !req.uninstall {
		if command, err = jigCommand(e); err != nil {
			return nil, err
		}
	}
	hooksDir := home.HooksDir(jigHome)
	g := gitConfig(e)

	if req.kind == scopeGlobal {
		return []step{{
			piece: "githook", target: hooksDir,
			detail: "git's global core.hooksPath runs jig's wrappers, which run each repository's own hooks first",
			apply: func() (string, error) {
				var res setup.GlobalResult
				var err error
				if req.uninstall {
					res, err = g.UninstallGlobal(hooksDir)
				} else {
					res, err = g.InstallGlobal(hooksDir, command)
				}
				switch {
				case err != nil:
					return "", err
				case !res.Changed:
					return resultUnchanged, nil
				case req.uninstall:
					return resultRemoved, nil
				}
				return resultInstalled, nil
			},
		}}, nil
	}

	if len(req.target.Clones) == 0 {
		return []step{{
			piece: "githook", target: req.target.Dir,
			detail: "there is no git repository in that scope",
			apply:  func() (string, error) { return resultSkipped, nil },
		}}, nil
	}
	var steps []step
	for _, clone := range req.target.Clones {
		clone := clone
		detail := ""
		steps = append(steps, step{
			piece: "githook", target: clone,
			note: func() string { return detail },
			apply: func() (string, error) {
				var res setup.CloneResult
				var err error
				if req.uninstall {
					res, err = g.UninstallClone(clone, hooksDir)
				} else {
					res, err = g.InstallClone(clone, hooksDir, command)
				}
				if err != nil {
					return "", err
				}
				detail = res.State
				if res.Reason != "" {
					detail += ": " + res.Reason
				}
				switch {
				case res.State == setup.CloneSkipped:
					return resultSkipped, nil
				case !res.Changed:
					return resultUnchanged, nil
				case req.uninstall:
					return resultRemoved, nil
				}
				return resultInstalled, nil
			},
		})
	}
	return steps, nil
}

// memberTargets are the member repositories of an orchestrator project, as
// scopes of their own: each is a folder the project's files go in too.
func memberTargets(t scopeTarget) ([]scopeTarget, error) {
	dir, err := projectDir(t)
	if err != nil {
		return nil, err
	}
	m, err := project.LoadMarker(filepath.Join(dir, project.MarkerFile))
	if err != nil || m.Kind != project.KindOrchestrator {
		return nil, &axi.Error{
			Msg:  fmt.Sprintf("--members: %s is not the folder of an orchestrator project (its jig.yaml says kind: orchestrator)", dir),
			Code: "VALIDATION_ERROR",
		}
	}
	var out []scopeTarget
	for _, r := range m.Repos {
		if r.Path == "" {
			continue
		}
		mdir := absFrom(dir, r.Path)
		if info, err := os.Stat(mdir); err != nil || !info.IsDir() {
			continue
		}
		member := scopeTarget{Kind: t.Kind, Project: t.Project, Dir: mdir}
		if top, err := gitx.TopLevel(mdir); err == nil {
			member.Clones = []string{top}
		}
		out = append(out, member)
	}
	return out, nil
}

// confirmChanges prints what is about to be done and asks the person; only a
// yes lets it proceed.
func confirmChanges(rd *bufio.Reader, stdout io.Writer, summaries []string) bool {
	if len(summaries) == 1 {
		fmt.Fprintf(stdout, "This will %s.\nApply? [y/N] ", summaries[0])
	} else {
		fmt.Fprintln(stdout, "This will:")
		for _, s := range summaries {
			fmt.Fprintf(stdout, "  - %s\n", s)
		}
		fmt.Fprint(stdout, "Apply? [y/N] ")
	}
	answer, _ := rd.ReadString('\n')
	a := strings.ToLower(strings.TrimSpace(answer))
	return a == "y" || a == "yes"
}

// stepSummaries are the summaries of the steps that need a yes and would
// change something.
func stepSummaries(steps []step) []string {
	var out []string
	for _, s := range steps {
		if s.consent && s.pending {
			out = append(out, s.summary)
		}
	}
	return out
}

// runSteps applies steps in order and returns the table rows of their results.
// A step's apply may fill in its detail as it runs.
func runSteps(steps []step) (rows [][]string, changed bool, err error) {
	for i := range steps {
		s := &steps[i]
		result, err := s.apply()
		if err != nil {
			return rows, changed, fmt.Errorf("%s %s: %w", s.piece, s.target, err)
		}
		changed = changed || result == resultInstalled || result == resultRemoved
		detail := s.detail
		if s.note != nil {
			detail = s.note()
		}
		rows = append(rows, []string{s.piece, s.harness, s.target, result, detail})
	}
	return rows, changed, nil
}

func stepsTable(rows [][]string) string {
	return axi.Table("steps", []string{"piece", "harness", "target", "result", "detail"}, rows)
}

// declinedRows is the table of steps nobody wrote.
func declinedRows(steps []step) [][]string {
	rows := make([][]string, len(steps))
	for i, s := range steps {
		rows[i] = []string{s.piece, s.harness, s.target, resultDeclined, ""}
	}
	return rows
}

// scopeLabelOf names a scope in a report: global, here, or project <name>.
func scopeLabelOf(kind, projectName string) string {
	if kind == scopeProject {
		return kind + " " + projectName
	}
	return kind
}

// runOpts is how a verb that installs a piece wants its steps run.
type runOpts struct {
	// label is the verb, and scope the scope as a report names it.
	label, scope string
	uninstall    bool
	// yes is --yes; tty whether stdin is a terminal the person can be asked
	// on, read through rd.
	yes, tty bool
	rd       *bufio.Reader
	// again is the command that repeats this run with --yes, which a refusal
	// to write without a yes tells the person to run, and undo the one that
	// reverses it.
	again, undo string
}

// runPiece runs the steps of an installer verb: it asks once for the files
// that are the person's (a terminal is asked, a caller without one is told to
// pass --yes), makes every change, and reports what each step did.
func runPiece(o runOpts, steps []step, stdout io.Writer) int {
	action := "install"
	if o.uninstall {
		action = "uninstall"
	}
	render := func(result string, rows [][]string, help string) int {
		axi.Render(stdout,
			axi.KV(o.label, [][2]string{{"scope", o.scope}, {"action", action}, {"result", result}}),
			stepsTable(rows),
			axi.Help(help),
		)
		return 0
	}
	if summaries := stepSummaries(steps); len(summaries) > 0 && !o.yes {
		if !o.tty {
			return renderErr(stdout, &axi.Error{
				Msg:  "nothing written: it would " + strings.Join(summaries, "; "),
				Code: axi.NeedsInput,
				Help: []string{"Run `" + o.again + "` to do it"},
			})
		}
		if !confirmChanges(o.rd, stdout, summaries) {
			fmt.Fprintln(stdout)
			return render(resultDeclined, declinedRows(steps), "Nothing was written")
		}
	}
	rows, changed, err := runSteps(steps)
	if err != nil {
		return renderErr(stdout, err)
	}
	help := "Run `jig doctor` to see what is in place; start a new session for a harness to read hooks and skills"
	if o.uninstall {
		help = "Run `" + o.undo + "` to put it back"
	}
	return render(outcome(changed, o.uninstall), rows, help)
}

// outcome names the result of a run as a whole.
func outcome(changed, uninstall bool) string {
	switch {
	case !changed:
		return resultUnchanged
	case uninstall:
		return resultRemoved
	}
	return resultInstalled
}
