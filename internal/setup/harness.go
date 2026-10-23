package setup

import (
	"path/filepath"
	"slices"
)

// Harness is an agent harness jig puts hook entries into: where its hook file
// is, and how jig's entries are merged into it and taken out again. The six
// are Claude Code, Codex, the Gemini CLI, Cursor, GitHub Copilot and OpenCode.
// Only Claude Code's is proven against the real tool; the others are written
// from the vendors' documentation and unit tested.
type Harness struct {
	// Name is what --harness takes and what the entries pass to jig observe.
	Name string
	// Events are the vendor's names for the events jig listens to, in the
	// order it adds them.
	Events []string
	// Dedicated says the file holds hooks and nothing else of the person's,
	// so one that has none left is deleted rather than left empty.
	Dedicated bool
	// Note is what an install should tell the person about running the
	// harness: what it needs on its side to read the hooks.
	Note string

	// configDir is the harness's directory in the person's home, relative to
	// it: where its presence says the person uses the harness.
	configDir string
	global    func(userHome string) string
	project   func(dir string, tracked bool) string
	merge     func(data []byte, jig string) ([]byte, bool, error)
	unmerge   func(data []byte) ([]byte, bool, error)
	installed func(data []byte) ([]string, error)
}

func specEvents(hooks []HookSpec) []string {
	out := make([]string, len(hooks))
	for i, h := range hooks {
		out[i] = h.Event
	}
	return out
}

// Harnesses lists every harness jig installs hooks for, Claude Code first.
func Harnesses() []Harness {
	return []Harness{
		{
			Name:      "claude",
			Events:    specEvents(ClaudeHooks()),
			Note:      "Start a new session for the harness to read them; Claude Code ignores hooks in a folder it does not trust and under --bare",
			configDir: ".claude",
			global:    func(home string) string { return filepath.Join(home, ".claude", "settings.json") },
			project: func(dir string, tracked bool) string {
				if tracked {
					return filepath.Join(dir, ".claude", "settings.json")
				}
				return filepath.Join(dir, ".claude", "settings.local.json")
			},
			merge:     MergeClaude,
			unmerge:   UnmergeClaude,
			installed: ClaudeEvents,
		},
		{
			Name:      "codex",
			Events:    specEvents(CodexHooks()),
			Dedicated: true,
			Note:      "Codex reads hooks only when its hooks feature is on (codex_hooks in ~/.codex/config.toml, which jig does not edit); start a new session, and Codex does not read them in a folder it does not trust",
			configDir: ".codex",
			global:    func(home string) string { return filepath.Join(home, ".codex", "hooks.json") },
			project:   func(dir string, _ bool) string { return filepath.Join(dir, ".codex", "hooks.json") },
			merge:     mergeCodex,
			unmerge:   UnmergeClaude,
			installed: ClaudeEvents,
		},
		{
			Name:      "gemini",
			Events:    specEvents(GeminiHooks()),
			Note:      "Start a new Gemini CLI session; hooks must be enabled in its settings (they are on by default in recent versions)",
			configDir: ".gemini",
			global:    func(home string) string { return filepath.Join(home, ".gemini", "settings.json") },
			project:   func(dir string, _ bool) string { return filepath.Join(dir, ".gemini", "settings.json") },
			merge:     mergeGemini,
			unmerge:   UnmergeClaude,
			installed: ClaudeEvents,
		},
		{
			Name:      "cursor",
			Events:    CursorEvents(),
			Dedicated: true,
			Note:      "Restart Cursor to read them; its hooks run from the .cursor directory",
			configDir: ".cursor",
			global:    func(home string) string { return filepath.Join(home, ".cursor", "hooks.json") },
			project:   func(dir string, _ bool) string { return filepath.Join(dir, ".cursor", "hooks.json") },
			merge:     cursorFormat.merge,
			unmerge:   cursorFormat.unmerge,
			installed: cursorFormat.installed,
		},
		{
			Name:      "copilot",
			Events:    CopilotEvents(),
			Dedicated: true,
			Note:      "Start a new Copilot session; jig keeps its hooks in a jig.json of their own",
			configDir: ".copilot",
			global:    func(home string) string { return filepath.Join(home, ".copilot", "hooks", "jig.json") },
			project:   func(dir string, _ bool) string { return filepath.Join(dir, ".github", "hooks", "jig.json") },
			merge:     copilotFormat.merge,
			unmerge:   copilotFormat.unmerge,
			installed: copilotFormat.installed,
		},
		{
			Name:      "opencode",
			Events:    OpenCodeEvents(),
			Dedicated: true,
			Note:      "Restart OpenCode to load the plugin; it needs node on PATH to run jig",
			configDir: filepath.Join(".config", "opencode"),
			global:    func(home string) string { return filepath.Join(home, ".config", "opencode", "plugins", "jig.js") },
			project:   func(dir string, _ bool) string { return filepath.Join(dir, ".opencode", "plugins", "jig.js") },
			merge:     mergeOpenCode,
			unmerge:   unmergeOpenCode,
			installed: installedOpenCode,
		},
	}
}

// HarnessNamed finds the harness called name.
func HarnessNamed(name string) (Harness, bool) {
	for _, h := range Harnesses() {
		if h.Name == name {
			return h, true
		}
	}
	return Harness{}, false
}

// HarnessNames lists the names of every harness, in the order Harnesses has
// them.
func HarnessNames() []string {
	hs := Harnesses()
	out := make([]string, len(hs))
	for i, h := range hs {
		out[i] = h.Name
	}
	return out
}

// ConfigDir is the harness's own directory in the home of the person who owns
// userHome; that it exists says the person uses the harness.
func (h Harness) ConfigDir(userHome string) string { return filepath.Join(userHome, h.configDir) }

// GlobalFile is the harness's hook file for the person who owns userHome.
func (h Harness) GlobalFile(userHome string) string { return h.global(userHome) }

// ProjectFile is the harness's hook file inside the project folder dir. With
// tracked, Claude Code's goes in the settings.json the project shares rather
// than the settings.local.json that is the person's alone; the other harnesses
// have one project file each.
func (h Harness) ProjectFile(dir string, tracked bool) string { return h.project(dir, tracked) }

// Merge adds jig's entries to the document in data; jig is the program part of
// the command (JigCommand). See MergeClaude for the rules every harness
// follows: idempotent, nothing of the person's touched, a file jig cannot read
// is an error.
func (h Harness) Merge(data []byte, jig string) (out []byte, changed bool, err error) {
	return h.merge(data, jig)
}

// Unmerge removes jig's entries from the document in data. A result of nil with
// a change means the file was jig's alone and goes.
func (h Harness) Unmerge(data []byte) (out []byte, changed bool, err error) {
	return h.unmerge(data)
}

// Installed lists the events of the document in data that carry an entry of
// jig's.
func (h Harness) Installed(data []byte) ([]string, error) { return h.installed(data) }

// Plan works out installing jig's entries into the file at path, or taking
// them out when uninstall is set, without making the change. A file that is not
// there is an empty document to install into and nothing to uninstall from.
func (h Harness) Plan(path, jig string, uninstall bool) (FilePlan, error) {
	plan, err := planFile(path, jig, uninstall, h.merge, h.unmerge)
	if err != nil {
		return plan, err
	}
	if uninstall && h.Dedicated && !plan.Delete && plan.Exists && !slices.Equal(plan.Before, plan.After) && leavesNothing(plan.After) {
		plan.Delete = true
	}
	return plan, nil
}

// leavesNothing reports whether data is a hook document with no hook left: an
// object whose only keys can be version and an empty hooks.
func leavesNothing(data []byte) bool {
	root, _, err := parseSettings(data)
	if err != nil {
		return false
	}
	for _, k := range root.keys {
		switch k {
		case "version":
		case "hooks":
			if o, ok := root.vals[k].(*object); !ok || o.len() != 0 {
				return false
			}
		default:
			return false
		}
	}
	return true
}
