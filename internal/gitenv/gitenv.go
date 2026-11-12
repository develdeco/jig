// Package gitenv holds the one list of environment variables that describe a
// single commit inside its hooks, or a person's git identity. A child process
// or a test binary must not inherit them from the shell.
package gitenv

// HookOnly names the variables that describe one commit inside its hooks:
// the wrapper's marker and the ones git exports to a post-commit hook. After
// the hook they are nobody's setting, so what the hook starts must not inherit
// them. GIT_EXEC_PATH stays: it only names git's own helpers.
var HookOnly = []string{
	"JIG_GITHOOK_WRAPPED",
	"GIT_AUTHOR_NAME", "GIT_AUTHOR_EMAIL", "GIT_AUTHOR_DATE",
	"GIT_COMMITTER_NAME", "GIT_COMMITTER_EMAIL", "GIT_COMMITTER_DATE",
	"GIT_INDEX_FILE", "GIT_PREFIX", "GIT_DIR", "GIT_WORK_TREE", "GIT_COMMON_DIR",
	"GIT_CONFIG_PARAMETERS",
}
