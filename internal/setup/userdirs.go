package setup

import "path/filepath"

// UserDirs locates a person's own harness files on this machine: their home
// directory, and the directory Claude Code reads its user settings, skills
// and memory from. Claude Code reads $CLAUDE_CONFIG_DIR in place of
// <home>/.claude when the variable is set; the caller resolves it (cmd/jig,
// through its env), so nothing in this package reads the environment.
type UserDirs struct {
	// Home is the person's home directory, which holds every other harness's
	// directory and the shared agents skills (.agents/skills).
	Home string
	// Claude is Claude Code's user directory, or "" for <Home>/.claude.
	Claude string
}

// ClaudeDir is Claude Code's user directory: Claude when it is set, else
// <Home>/.claude.
func (u UserDirs) ClaudeDir() string {
	if u.Claude != "" {
		return u.Claude
	}
	return filepath.Join(u.Home, ".claude")
}

// SkillRoots are the person's own skills directories: Claude Code's
// (<ClaudeDir>/skills), then the shared agents one (<Home>/.agents/skills).
func (u UserDirs) SkillRoots() []string {
	return []string{filepath.Join(u.ClaudeDir(), "skills"), filepath.Join(u.Home, ".agents", "skills")}
}

// inHome is a path under the person's home, for a harness whose files do not
// move with an environment variable jig honors.
func inHome(elem ...string) func(UserDirs) string {
	return func(u UserDirs) string { return filepath.Join(append([]string{u.Home}, elem...)...) }
}
