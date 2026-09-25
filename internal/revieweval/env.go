package revieweval

import "strings"

// dispatchEnv builds the exact environment a live reviewer or judge
// dispatch's child process may see, from environ (normally os.Environ(),
// injectable so this is testable without touching the real process
// environment): every variable this package or jig itself owns - a name
// starting "JIG_" - is dropped, along with the two variables gittest.Run
// sets directly into a test binary's own process environment
// (GIT_CONFIG_GLOBAL, GIT_CONFIG_NOSYSTEM) and PWD/OLDPWD, which
// session.Options.Env's own headless handling drops and re-sets for the
// dispatch's own worktree regardless of what reaches it here. It also
// drops launch-context entries no dispatch has any business carrying,
// whatever process started this one: a name that is empty or starts with
// "=" (Windows keeps each drive's own current directory in hidden
// entries named "=C:", "=D:" and so on - cmd.exe sets them and every
// process it starts inherits them, and strings.Cut reads their name as
// "" since they hold their own second "="), "_" (a shell's own last-
// argument variable, or - for a test binary run directly rather than
// through "go test" - the compiled test binary's own path) and
// GOCOVERDIR (only ever present under "go test -cover", naming a
// directory next to the test binary itself). Everything else - PATH,
// HOME or USERPROFILE, TEMP, the auth variables the real claude CLI
// needs, whatever else this process happens to be running with - passes
// through unfiltered: this is a denylist of what jig and its own test
// scaffolding must never hand a live child, not an allowlist of what a
// session needs.
//
// goos picks the comparison: names are compared case-insensitively when
// goos == "windows", where environment variable names are not case
// sensitive (a real Windows process could just as easily carry "Path" or
// "Git_Config_Global" in a different case), and case-sensitively
// elsewhere. Live callers pass runtime.GOOS; a fixed value here keeps the
// rule itself testable on every host this package's own tests run on.
func dispatchEnv(goos string, environ []string) []string {
	sameName := func(a, b string) bool { return a == b }
	if goos == "windows" {
		sameName = strings.EqualFold
	}
	owned := func(name string) bool {
		if name == "" || strings.HasPrefix(name, "=") {
			return true
		}
		if name == "_" || sameName(name, "GOCOVERDIR") {
			return true
		}
		if len(name) >= 4 && sameName(name[:4], "JIG_") {
			return true
		}
		for _, n := range []string{"GIT_CONFIG_GLOBAL", "GIT_CONFIG_NOSYSTEM", "PWD", "OLDPWD"} {
			if sameName(name, n) {
				return true
			}
		}
		return false
	}
	out := make([]string, 0, len(environ))
	for _, kv := range environ {
		name, _, _ := strings.Cut(kv, "=")
		if owned(name) {
			continue
		}
		out = append(out, kv)
	}
	return out
}
