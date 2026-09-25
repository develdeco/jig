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
// dispatch's own worktree regardless of what reaches it here. Everything
// else - PATH, HOME or USERPROFILE, TEMP, the auth variables the real
// claude CLI needs, whatever else this process happens to be running
// with - passes through unfiltered: this is a denylist of what jig and
// its own test scaffolding must never hand a live child, not an allowlist
// of what a session needs.
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
