package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/home"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/ledger"
	"github.com/develdeco/jig/internal/project"
	"github.com/develdeco/jig/internal/setup"
)

const (
	doctorUsageProject = "check the named project of this machine too, instead of the project the working directory is in"
	doctorUsageHarness = "the harnesses to check hooks for: a name, a comma-separated list or all (default: claude, and each harness that has a directory in your home)"
)

// init registers this file's verb with the registry (registry.go).
func init() {
	registerVerb(verb{
		Spec: cmdSpec{"doctor", "jig doctor: list each piece jig sets up, whether it is in place, what it costs and the command that adds it", []flagSpec{
			{"project", doctorUsageProject, false},
			{"harness", doctorUsageHarness, false},
		}, false},
		Order: 520,
		Run: func(e env, args []string, stdout io.Writer, _ io.Reader) int {
			return cmdDoctor(e, args, stdout)
		},
	})
}

// doctor reads what is installed and writes nothing. The pieces it lists are the
// ones jig setup puts in place: the skills, the hooks of each harness, git's
// hooks and the hint. A piece counts as in place when it is in the person's
// own files or in the project's, and the report says which.

// candidate is how one place stands for a piece.
type candidate struct {
	where, state, detail string
}

var stateRank = map[string]int{setup.StateInstalled: 3, setup.StateStale: 2, setup.StatePartial: 1, setup.StateMissing: 0}

// bestOf picks the place a piece is in best shape in: installed over stale
// over partial over missing; the first of equals.
func bestOf(cands ...candidate) candidate {
	best := candidate{state: setup.StateMissing}
	for i, c := range cands {
		if i == 0 || stateRank[c.state] > stateRank[best.state] {
			best = c
		}
	}
	if best.state == setup.StateMissing {
		best.where = ""
	}
	return best
}

// doctorCtx is what the checks share.
type doctorCtx struct {
	e        env
	userHome string
	jigHome  string
	version  string
	// program is the program part of the hook commands this jig would write
	// ("" when the binary is not named jig).
	program string
	// scope names the project the checks look at as well: Kind "" for none.
	kind        string
	projectName string
	target      scopeTarget
	marker      *project.Marker
	events      []journal.Line
	notes       []string
}

func (d *doctorCtx) hasProject() bool { return d.target.Dir != "" }

func (d *doctorCtx) note(format string, args ...any) {
	d.notes = append(d.notes, fmt.Sprintf(format, args...))
}

// cmdDoctor implements `jig doctor [--project <name>] [--harness <h>]`.
func cmdDoctor(e env, args []string, stdout io.Writer) int {
	fs := newFlagSet(e, "doctor")
	projectName := fs.String("project", "", doctorUsageProject)
	harness := fs.String("harness", "", doctorUsageHarness)
	if handled, err := parseFlags(stdout, fs, args); handled {
		return 0
	} else if err != nil {
		return renderErr(stdout, err)
	}
	if fs.NArg() > 0 {
		return renderErr(stdout, &axi.Error{Msg: fmt.Sprintf("unexpected argument %q", fs.Arg(0)), Code: "VALIDATION_ERROR"})
	}

	d := &doctorCtx{e: e, version: binaryVersion()}
	var err error
	if d.userHome, err = e.userHomeDir(); err != nil {
		return renderErr(stdout, err)
	}
	if d.jigHome, err = e.jigHome(); err != nil {
		return renderErr(stdout, err)
	}
	if dir := e.getenv("CLAUDE_CONFIG_DIR"); dir != "" {
		d.note("CLAUDE_CONFIG_DIR is set to %s: Claude Code reads its settings and skills from there, and jig writes to %s", dir, filepath.Join(d.userHome, ".claude"))
	}
	if prog, err := jigCommand(e); err == nil {
		d.program = prog
	} else {
		d.note("this binary is not named jig, so the entries it would write cannot be compared with the ones in place: run it as jig")
	}
	if lines, err := ledger.Read(d.jigHome); err == nil {
		d.events = lines
	}

	scopeText := "global"
	if *projectName != "" {
		t, err := resolveScope(e, scopeProject, *projectName)
		if err != nil {
			return renderErr(stdout, err)
		}
		d.kind, d.projectName, d.target = scopeProject, *projectName, t
		scopeText += " + project " + *projectName
		if t.Dir == "" {
			d.note("project %s has no folder on this machine (no jig.yaml marker and more than one clone), so only your own files are checked", *projectName)
		}
	} else if t, err := resolveScope(e, scopeHere, ""); err == nil {
		d.kind, d.target = scopeHere, t
		scopeText += " + here"
	}
	if d.hasProject() {
		if m, err := project.LoadMarker(filepath.Join(d.target.Dir, project.MarkerFile)); err == nil {
			d.marker = &m
		}
	}

	hs, err := d.harnesses(*harness)
	if err != nil {
		return renderErr(stdout, err)
	}

	var findings []setup.Finding
	findings = append(findings, d.skills()...)
	findings = append(findings, d.hooks(hs)...)
	findings = append(findings, d.githook()...)
	findings = append(findings, d.hint()...)

	exe, _ := e.executable()
	rows := make([][]string, len(findings))
	missing := false
	for i, f := range findings {
		rows[i] = []string{f.Piece, f.Harness, f.Where, f.State, f.Detail, f.Cost, f.Fix}
		if f.State != setup.StateInstalled && f.Piece != "helpers" {
			missing = true
		}
	}
	blocks := []string{
		axi.KV("doctor", [][2]string{{"jig", d.version}, {"binary", exe}, {"jig_home", d.jigHome}, {"scope", scopeText}}),
		axi.Table("pieces", []string{"piece", "harness", "where", "state", "detail", "cost", "fix"}, rows),
	}
	if len(d.notes) > 0 {
		b := axi.Block{Header: fmt.Sprintf("notes[%d]:", len(d.notes))}
		for _, n := range d.notes {
			b.Lines = append(b.Lines, "  "+n)
		}
		blocks = append(blocks, b.String())
	}
	if missing {
		blocks = append(blocks, axi.Help("Run `jig setup --global --artifacts --yes` to install every missing piece for yourself, or the command in a row's fix column for that piece"))
	} else {
		blocks = append(blocks, axi.Help("Every piece jig sets up is in place; `jig here` names where the work is"))
	}
	axi.Render(stdout, blocks...)
	return 0
}

// harnesses are the harnesses whose hooks are checked: those --harness names,
// else Claude Code and every harness with a directory in the person's home or
// hooks of jig's in place.
func (d *doctorCtx) harnesses(value string) ([]setup.Harness, error) {
	if value != "" {
		return parseHarnesses(value, nil)
	}
	var out []setup.Harness
	for _, h := range setup.Harnesses() {
		if h.Name == harnessClaude {
			out = append(out, h)
			continue
		}
		if info, err := os.Stat(h.ConfigDir(d.userHome)); err == nil && info.IsDir() {
			out = append(out, h)
		}
	}
	return out, nil
}

func (d *doctorCtx) scopeFlags() string {
	if d.kind == scopeProject {
		return "--project " + d.projectName
	}
	return "--here"
}

// --- skills ---

func (d *doctorCtx) skills() []setup.Finding {
	expected, _, _ := skillSelection{project: true}.resolve()
	helpers, _, _ := skillSelection{helpers: true}.resolve()
	var entries []setup.StubEntry
	for _, s := range expected {
		entries = append(entries, setup.StubEntry{Name: s.Name, Summary: setup.SkillSummary(s.Body)})
	}
	stub := setup.Skill{Name: setup.StubSkillName, Body: setup.StubBody(entries)}

	inspect := func(root string, skills []setup.Skill, where string) candidate {
		st, err := setup.InspectSkills(root, skills, d.version)
		if err != nil {
			return candidate{where: where, state: setup.StateMissing, detail: err.Error()}
		}
		c := candidate{where: where, state: setup.SkillsState(st)}
		var off, absent []string
		for _, s := range st {
			switch s.State {
			case setup.StateStale:
				v := s.Version
				if v == "" {
					v = "no stamp"
				}
				off = append(off, fmt.Sprintf("%s (%s)", s.Name, v))
			case setup.StateMissing:
				absent = append(absent, s.Name)
			}
		}
		if len(off) > 0 {
			c.detail = fmt.Sprintf("out of date against jig %s: %s", d.version, strings.Join(off, ", "))
		}
		if len(absent) > 0 && c.state != setup.StateMissing {
			if c.detail != "" {
				c.detail += "; "
			}
			c.detail += "missing: " + strings.Join(absent, ", ")
		}
		return c
	}

	var out []setup.Finding
	for _, root := range []struct{ harness, rel string }{{"claude", filepath.Join(".claude", "skills")}, {"agents", filepath.Join(".agents", "skills")}} {
		cands := []candidate{inspect(filepath.Join(d.userHome, root.rel), expected, scopeGlobal)}
		if d.hasProject() {
			cands = append(cands,
				inspect(filepath.Join(d.target.Dir, root.rel), expected, "here (tracked)"),
				inspect(filepath.Join(d.target.Dir, root.rel), []setup.Skill{stub}, "here (stub)"),
			)
		}
		best := bestOf(cands...)
		fix := ""
		if best.state != setup.StateInstalled {
			fix = "jig skills --global"
			switch best.where {
			case "here (stub)":
				fix = "jig skills " + d.scopeFlags()
			case "here (tracked)":
				fix = "jig skills " + d.scopeFlags() + " --tracked"
			}
		}
		out = append(out, setup.Finding{
			Piece: "skills", Harness: root.harness, Where: best.where, State: best.state,
			Detail: best.detail, Cost: setup.SkillsCost(expected), Fix: fix,
		})
	}

	if len(helpers) > 0 {
		var all []setup.SkillStatus
		for _, rel := range []string{filepath.Join(".claude", "skills"), filepath.Join(".agents", "skills")} {
			st, err := setup.InspectSkills(filepath.Join(d.userHome, rel), helpers, d.version)
			if err == nil {
				all = append(all, st...)
			}
		}
		state := setup.SkillsState(all)
		f := setup.Finding{
			Piece: "helpers", Where: scopeGlobal, State: state,
			Detail: "optional: " + strings.Join(skillNames(helpers), " and ") + " let an agent start a project or attach a repository",
			Cost:   setup.SkillsCost(helpers),
		}
		if state == setup.StateMissing {
			f.Where = ""
		}
		if state != setup.StateInstalled {
			f.Fix = "jig skills --global --helpers"
		}
		out = append(out, f)
	}
	return out
}

// --- hooks ---

func (d *doctorCtx) hooks(hs []setup.Harness) []setup.Finding {
	var out []setup.Finding
	for _, h := range hs {
		files := []struct{ where, path string }{{scopeGlobal, h.GlobalFile(d.userHome)}}
		if d.hasProject() {
			untracked, tracked := h.ProjectFile(d.target.Dir, false), h.ProjectFile(d.target.Dir, true)
			files = append(files, struct{ where, path string }{"here", untracked})
			if tracked != untracked {
				files = append(files, struct{ where, path string }{"here (tracked)", tracked})
			}
		}
		var cands []candidate
		for _, f := range files {
			cands = append(cands, d.inspectHooks(h, f.path, f.where))
		}
		best := bestOf(cands...)
		f := setup.Finding{Piece: "hooks", Harness: h.Name, Where: best.where, State: best.state, Detail: best.detail, Cost: setup.HooksCost(h)}
		if best.state != setup.StateInstalled {
			fix := "jig hooks " + scopeFlagsText(scopeGlobal, "")
			if strings.HasPrefix(best.where, "here") {
				fix = "jig hooks " + d.scopeFlags()
				if strings.Contains(best.where, "tracked") {
					fix += " --tracked"
				}
			}
			if h.Name != harnessClaude {
				fix += " --harness " + h.Name
			}
			f.Fix = fix + " --yes"
			if d.marker != nil && d.marker.Expects != nil && d.marker.Expects.Hooks && best.state == setup.StateMissing {
				f.Detail = "the project's jig.yaml expects hooks"
			}
		} else if !d.seen(h.Name) {
			d.note("hooks not seen: no hook or tool event from %s is in the ledger yet; start a new %s session (a harness reads its hooks at startup) and run a command in it", h.Name, h.Name)
		}
		out = append(out, f)
	}
	return out
}

func (d *doctorCtx) inspectHooks(h setup.Harness, path, where string) candidate {
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return candidate{where: where, state: setup.StateMissing}
	}
	if err != nil {
		return candidate{where: where, state: setup.StateMissing, detail: err.Error()}
	}
	events, err := h.Installed(data)
	if err != nil {
		return candidate{where: where, state: setup.StateMissing, detail: path + " cannot be read: " + err.Error()}
	}
	var absent []string
	for _, ev := range h.Events {
		if !slices.Contains(events, ev) {
			absent = append(absent, ev)
		}
	}
	switch {
	case len(events) == 0:
		return candidate{where: where, state: setup.StateMissing}
	case len(absent) > 0:
		return candidate{where: where, state: setup.StatePartial, detail: "no entry for " + strings.Join(absent, ", ")}
	}
	if d.program != "" {
		if _, changed, err := h.Merge(data, d.program); err == nil && changed {
			return candidate{where: where, state: setup.StateStale, detail: "its entries are not what this jig installs (another jig's, or an older one's)"}
		}
	}
	return candidate{where: where, state: setup.StateInstalled}
}

// seen reports whether the ledger has a hook or tool event from harness.
func (d *doctorCtx) seen(harness string) bool {
	for _, l := range d.events {
		if l.Harness == harness && (l.Event == journal.EventHook || l.Event == journal.EventTool) {
			return true
		}
	}
	return false
}

// --- git hooks ---

func (d *doctorCtx) githook() []setup.Finding {
	hooksDir := home.HooksDir(d.jigHome)
	g := gitConfig(d.e)
	f := setup.Finding{Piece: "githook", Cost: setup.GithookCost()}

	global := candidate{where: scopeGlobal, state: setup.StateMissing}
	st, err := g.InspectGlobal(hooksDir, programOrJig(d.program))
	switch {
	case err != nil:
		global.detail = err.Error()
	case st.Active && (d.program == "" || len(st.Stale) == 0):
		global.state = setup.StateInstalled
	case st.Active:
		global.state = setup.StateStale
		global.detail = fmt.Sprintf("%d wrappers differ from what this jig writes (%s)", len(st.Stale), strings.Join(st.Stale, ", "))
	case st.Config != "":
		global.detail = "core.hooksPath is set to " + st.Config + ", not jig's"
	}

	cands := []candidate{global}
	var unwrapped []string
	if d.hasProject() {
		var covered int
		for _, clone := range d.target.Clones {
			res, err := g.InspectClone(clone, hooksDir)
			if err != nil {
				continue
			}
			if res.State == setup.CloneCovered || res.State == setup.CloneHasBlock {
				covered++
			}
			if len(res.Unwrapped) > 0 && (global.state == setup.StateInstalled || res.State == setup.CloneCovered) {
				unwrapped = append(unwrapped, fmt.Sprintf("%s has %s, which the global wrappers do not run: they stop running under jig's core.hooksPath", clone, strings.Join(res.Unwrapped, ", ")))
			}
		}
		switch {
		case len(d.target.Clones) > 0 && covered == len(d.target.Clones):
			cands = append(cands, candidate{where: "here", state: setup.StateInstalled})
		case covered > 0:
			cands = append(cands, candidate{where: "here", state: setup.StatePartial, detail: fmt.Sprintf("%d of %d repositories record their commits", covered, len(d.target.Clones))})
		}
	}
	best := bestOf(cands...)
	f.Where, f.State, f.Detail = best.where, best.state, best.detail
	if best.state != setup.StateInstalled {
		f.Fix = "jig githook --global"
		if strings.HasPrefix(best.where, "here") {
			f.Fix = "jig githook " + d.scopeFlags()
		}
	}
	for _, u := range unwrapped {
		d.note("githook: %s", u)
	}
	return []setup.Finding{f}
}

func programOrJig(program string) string {
	if program == "" {
		return "jig"
	}
	return program
}

// --- the hint ---

func (d *doctorCtx) hint() []setup.Finding {
	var out []setup.Finding
	for _, name := range setup.HintHarnesses() {
		path := setup.HintFile(name, d.userHome)
		global := d.inspectHint(path, scopeGlobal)
		if name != harnessClaude && global.state == setup.StateMissing {
			// A harness the person does not use has no memory file to check.
			h, _ := setup.HarnessNamed(name)
			if info, err := os.Stat(h.ConfigDir(d.userHome)); err != nil || !info.IsDir() {
				continue
			}
		}
		cands := []candidate{global}
		if d.hasProject() && name == harnessClaude {
			cands = append(cands,
				d.inspectHint(setup.ProjectHintFile(d.target.Dir, false), "here"),
				d.inspectHint(setup.ProjectHintFile(d.target.Dir, true), "here (tracked)"),
				d.inspectHint(filepath.Join(d.target.Dir, "CLAUDE.md"), "here (tracked)"),
			)
		}
		best := bestOf(cands...)
		f := setup.Finding{Piece: "hint", Harness: name, Where: best.where, State: best.state, Detail: best.detail, Cost: setup.HintCost()}
		if best.state != setup.StateInstalled {
			f.Fix = "jig hint --global --yes"
			if name != harnessClaude {
				f.Fix += " --harness " + name
			}
			if strings.HasPrefix(best.where, "here") {
				f.Fix = "jig hint " + d.scopeFlags() + " --yes"
			}
		}
		out = append(out, f)
	}
	return out
}

func (d *doctorCtx) inspectHint(path, where string) candidate {
	data, err := os.ReadFile(path)
	if err != nil {
		return candidate{where: where, state: setup.StateMissing}
	}
	state := setup.HintState(data)
	c := candidate{where: where, state: state}
	if state == setup.StateStale {
		c.detail = "the block in " + path + " is not what this jig writes"
	}
	return c
}
