package setup

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// The files jig writes into a person's skills directories and memory files,
// and into a project's. Every one is recognized by a mark jig put in it (a
// stamp at the end of a skill, a block around the hint) and not by a record,
// so what is jig's is found again after an upgrade, a move or a restore, and
// nothing else is touched.

// A Skill is a skill to put on disk: the directory it lives in and the text
// of its SKILL.md as the binary embeds it.
type Skill struct {
	Name string
	Body []byte
}

const (
	skillStampOpen  = "<!-- jig-skill v="
	skillStampClose = " -->"
)

// StampSkill returns body with jig's stamp as its last line. The stamp marks
// the file as jig's and names the version of the jig that wrote it, so a later
// jig knows the file is its own and whether it is out of date.
func StampSkill(body []byte, version string) []byte {
	out := bytes.TrimRight(body, "\n")
	return append(append(append([]byte{}, out...), '\n'), []byte(skillStampOpen+version+skillStampClose+"\n")...)
}

// SkillStamp reads the stamp from the end of data: the version of the jig that
// wrote the file. ok is false when there is none.
func SkillStamp(data []byte) (version string, ok bool) {
	text := strings.TrimRight(string(data), "\r\n")
	i := strings.LastIndexByte(text, '\n')
	last := strings.TrimRight(text[i+1:], "\r")
	if !strings.HasPrefix(last, skillStampOpen) || !strings.HasSuffix(last, skillStampClose) {
		return "", false
	}
	return strings.TrimSuffix(strings.TrimPrefix(last, skillStampOpen), skillStampClose), true
}

// skillFile is the SKILL.md of the skill called name under root.
func skillFile(root, name string) string { return filepath.Join(root, name, "SKILL.md") }

// InstallSkills writes each skill to root/<name>/SKILL.md, stamped with
// version, and returns the names whose file it wrote. A file already there with
// the same content is left alone, so a second install writes nothing.
//
// A file that carries no stamp was not written by this jig. With replace, as
// for the skills jig ships, it is replaced anyway: those names are jig's, and
// `jig skills install` has always written over them. Without it, as for a
// stub whose name is no skill of jig's, it is left alone and its name comes
// back in kept.
func InstallSkills(root string, skills []Skill, version string, replace bool) (written, kept []string, err error) {
	for _, s := range skills {
		want := StampSkill(s.Body, version)
		path := skillFile(root, s.Name)
		have, exists, err := readIfExists(path)
		if err != nil {
			return written, kept, err
		}
		if exists && bytes.Equal(have, want) {
			continue
		}
		if _, stamped := SkillStamp(have); exists && !stamped && !replace {
			kept = append(kept, s.Name)
			continue
		}
		if err := writeFile(path, want, 0o644, true); err != nil {
			return written, kept, err
		}
		written = append(written, s.Name)
	}
	return written, kept, nil
}

// RemoveSkills removes the skills jig put under root and returns their names.
// With names it removes those, without it every skill under root that carries
// jig's stamp. A skill is jig's when it carries the stamp or is, byte for byte,
// one of embedded (the files `jig skills install` wrote before there were
// stamps); one that is neither is the person's and stays. An emptied skill
// directory goes with it.
func RemoveSkills(root string, names []string, embedded map[string][]byte) (removed []string, err error) {
	if names == nil {
		entries, err := os.ReadDir(root)
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		for _, e := range entries {
			if e.IsDir() {
				names = append(names, e.Name())
			}
		}
	}
	for _, name := range names {
		path := skillFile(root, name)
		data, exists, err := readIfExists(path)
		if err != nil {
			return removed, err
		}
		if !exists {
			continue
		}
		_, stamped := SkillStamp(data)
		if !stamped && !bytes.Equal(data, embedded[name]) {
			continue
		}
		if err := os.Remove(path); err != nil {
			return removed, err
		}
		_ = os.Remove(filepath.Dir(path)) // only if nothing else is in it
		removed = append(removed, name)
	}
	return removed, nil
}

// The states of a skill, or of any piece, as `jig doctor` reports them.
const (
	// StateInstalled: in place and what this jig would write.
	StateInstalled = "installed"
	// StateStale: in place, written by another version of jig (or by hand).
	StateStale = "stale"
	// StatePartial: some of it is in place.
	StatePartial = "partial"
	// StateMissing: none of it is in place.
	StateMissing = "missing"
)

// SkillStatus is how one skill stands on disk.
type SkillStatus struct {
	Name  string
	State string
	// Version is the version in the stamp of the file that is there ("" for
	// none, or for one with no stamp).
	Version string
}

// InspectSkills reads how skills stand under root against what a jig of the
// given version would write. It writes nothing. A skill whose file is exactly
// the unstamped body is the one `jig skills install` wrote: it counts as
// installed, with no version.
func InspectSkills(root string, skills []Skill, version string) ([]SkillStatus, error) {
	out := make([]SkillStatus, 0, len(skills))
	for _, s := range skills {
		st := SkillStatus{Name: s.Name, State: StateMissing}
		data, exists, err := readIfExists(skillFile(root, s.Name))
		if err != nil {
			return nil, err
		}
		if exists {
			st.Version, _ = SkillStamp(data)
			switch {
			case bytes.Equal(data, StampSkill(s.Body, version)), bytes.Equal(data, s.Body):
				st.State = StateInstalled
			default:
				st.State = StateStale
			}
		}
		out = append(out, st)
	}
	return out, nil
}

// SkillsState folds the states of a set of skills into one: installed when
// all are, missing when none is, stale when any in place is out of date, else
// partial.
func SkillsState(statuses []SkillStatus) string {
	var installed, stale, missing int
	for _, s := range statuses {
		switch s.State {
		case StateInstalled:
			installed++
		case StateStale:
			stale++
		default:
			missing++
		}
	}
	switch {
	case len(statuses) == 0, installed == 0 && stale == 0:
		return StateMissing
	case stale > 0:
		return StateStale
	case missing > 0:
		return StatePartial
	}
	return StateInstalled
}

// SkillSummary is the first sentence of a skill's description, for a list of
// skills where the whole description would be long. A body with no
// description has none.
func SkillSummary(body []byte) string {
	for _, line := range strings.Split(strings.ReplaceAll(string(body), "\r\n", "\n"), "\n") {
		if d, ok := strings.CutPrefix(line, "description: "); ok {
			d = strings.TrimSpace(d)
			if i := strings.Index(d, ". "); i > 0 {
				d = d[:i+1]
			}
			return d
		}
	}
	return ""
}

// StubEntry is one line of the stub: a skill and when to use it.
type StubEntry struct {
	Name    string
	Summary string
}

// StubSkillName is the name of the one skill a project gets in its untracked
// form: a stub that sends the agent to the binary for the skills themselves.
const StubSkillName = "jig"

// StubBody is the SKILL.md of the stub: jig is attached to this project, run
// jig here first, and the skills it lists are read from the binary with jig
// skill <name>, so what the project keeps never goes out of date with the
// jig that runs.
func StubBody(entries []StubEntry) []byte {
	var b strings.Builder
	b.WriteString("---\nname: " + StubSkillName + "\n")
	b.WriteString("description: jig is attached to this project. Use at the start of work, before a commit, and when a ticket, slice or question comes up: run `jig here`, then read the skill it points to with `jig skill <name>`.\n")
	b.WriteString("---\n\n# jig\n\n")
	b.WriteString("jig tracks this project's tickets and checks each commit. Run `jig here` first: it names the ticket and what to do next, and prints nothing when the folder is in no jig project.\n\n")
	b.WriteString("Read a skill's instructions with `jig skill <name>`. The binary holds the current text; do not copy it here.\n\n")
	b.WriteString("A ticket moves through four phases, each with a skill: brief (`intake`), build (`build`), review (`review`) and publish (`publish`). `jig next <ticket>` names the phase it is at and the prompt that asks for it (\"Build SHOP-1\"); each skill starts there and ends by relaying that prompt to the person.\n\n")
	b.WriteString("A prompt that names several phases (\"Take SHOP-1 from brief to PR\") is an automatic run: do the phases in order, each with its skill, and end the run at a refusal. Run the review's subagent in the foreground (`run_in_background: false`) and wait for its verdict in the same turn: never end the turn while it runs. The person's approvals stay theirs unless the prompt delegates them: \"go with your recommendations\" the brief's, and a prompt that ends at the PR (\"... to PR\") or says \"and publish it\" the publish. A delegated approval is recorded with agent provenance.\n\n")
	if len(entries) > 0 {
		b.WriteString("| Skill | Use it when |\n|---|---|\n")
		for _, e := range entries {
			b.WriteString("| `" + e.Name + "` | " + strings.ReplaceAll(e.Summary, "|", "/") + " |\n")
		}
	}
	return []byte(b.String())
}

// PruneEmptyDirs removes dir and then each parent, nearest first, that is
// empty, stopping before stop (and at the first one that is not empty).
func PruneEmptyDirs(dir, stop string) {
	for dir != "" && !samePathText(dir, stop) && strings.HasPrefix(filepath.ToSlash(dir), filepath.ToSlash(stop)+"/") {
		if err := os.Remove(dir); err != nil {
			return
		}
		dir = filepath.Dir(dir)
	}
}

// The hint: a few lines in the memory file an agent reads at the start of every
// session (CLAUDE.md, AGENTS.md, GEMINI.md), bracketed like the other blocks
// jig keeps in files it shares with the person.
const (
	hintOpen  = "<!-- >>> jig >>> -->"
	hintClose = "<!-- <<< jig <<< -->"
)

const hintText = `## jig

jig tracks this machine's tickets and checks each commit. At the start of work in a repository run ` + "`jig here`" + `: it names the ticket and what to do next, and prints nothing outside a jig project.
Read a jig skill with ` + "`jig skill <name>`" + `; ` + "`jig doctor`" + ` says what is not wired up.
`

// HintBlock is the block jig keeps in a memory file, with its markers and a
// final newline.
func HintBlock() string {
	return hintOpen + "\n" + hintText + hintClose + "\n"
}

// HintTokens estimates what the hint costs in every session, at four bytes a
// token.
func HintTokens() int { return (len(HintBlock()) + 3) / 4 }

// MergeHint puts the hint block into the memory file in data (nil for none
// yet): at the end when there is none, in place when there is one. out is data
// itself when nothing needs doing. A block opened and never closed is an error:
// jig does not guess where it stops.
func MergeHint(data []byte) (out []byte, changed bool, err error) {
	block := []byte(HintBlock())
	begin, end, found, err := findBlockBetween(data, hintOpen, hintClose)
	if err != nil {
		return nil, false, err
	}
	if found {
		if bytes.Equal(data[begin:end], block) {
			return data, false, nil
		}
		return append(append(append([]byte{}, data[:begin]...), block...), data[end:]...), true, nil
	}
	out = append([]byte{}, data...)
	if len(out) > 0 {
		if !bytes.HasSuffix(out, []byte("\n")) {
			out = append(out, '\n')
		}
		out = append(out, '\n')
	}
	return append(out, block...), true, nil
}

// UnmergeHint takes the hint block out of the memory file in data. When
// nothing else is left it returns nil with a change: the file was jig's alone.
func UnmergeHint(data []byte) (out []byte, changed bool, err error) {
	begin, end, found, err := findBlockBetween(data, hintOpen, hintClose)
	if err != nil {
		return nil, false, err
	}
	if !found {
		return data, false, nil
	}
	rest := append(append([]byte{}, data[:begin]...), data[end:]...)
	if len(bytes.TrimSpace(rest)) == 0 {
		return nil, true, nil
	}
	// The blank line MergeHint put before the block goes with it.
	if begin > 1 && data[begin-1] == '\n' && data[begin-2] == '\n' && end == len(data) {
		rest = append(append([]byte{}, data[:begin-1]...), data[end:]...)
	}
	return rest, true, nil
}

// HintState says how the hint stands in the memory file in data: installed (the
// block is what this jig writes), stale (a block of another text), or missing.
func HintState(data []byte) string {
	begin, end, found, err := findBlockBetween(data, hintOpen, hintClose)
	switch {
	case err != nil || !found:
		return StateMissing
	case bytes.Equal(data[begin:end], []byte(HintBlock())):
		return StateInstalled
	}
	return StateStale
}

// PlanHint works out putting the hint block into the memory file at path, or
// taking it out, without making the change.
func PlanHint(path string, uninstall bool) (FilePlan, error) {
	data, exists, err := readIfExists(path)
	if err != nil {
		return FilePlan{}, err
	}
	plan := FilePlan{Path: path, Exists: exists, Before: data}
	if uninstall {
		var changed bool
		plan.After, changed, err = UnmergeHint(data)
		plan.Delete = err == nil && changed && plan.After == nil
	} else {
		plan.After, _, err = MergeHint(data)
	}
	if err != nil {
		return FilePlan{}, fmt.Errorf("%s: %w", path, err)
	}
	return plan, nil
}

// HintFile is the memory file an agent harness reads in the person's home, or
// "" for a harness that has none jig writes: Claude Code's CLAUDE.md, Codex's
// AGENTS.md and the Gemini CLI's GEMINI.md.
func HintFile(harness, userHome string) string {
	switch harness {
	case "claude":
		return filepath.Join(userHome, ".claude", "CLAUDE.md")
	case "codex":
		return filepath.Join(userHome, ".codex", "AGENTS.md")
	case "gemini":
		return filepath.Join(userHome, ".gemini", "GEMINI.md")
	}
	return ""
}

// HintHarnesses are the harnesses HintFile has a file for.
func HintHarnesses() []string { return []string{"claude", "codex", "gemini"} }

// ProjectHintFile is the memory file a project's hint goes in: the
// CLAUDE.local.md Claude Code reads for the person alone, or, tracked, the
// AGENTS.md the project shares.
func ProjectHintFile(dir string, tracked bool) string {
	if tracked {
		return filepath.Join(dir, "AGENTS.md")
	}
	return filepath.Join(dir, "CLAUDE.local.md")
}
