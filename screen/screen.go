// Package screen implements jig's structural command screen and secret-path
// screen. It is a Go port of a Python predecessor: git commands are parsed
// structurally (segment split, whitespace tokenize, unquote, git-binary
// match, global-option consumption) rather than matched against a regex over
// the whole command line. The only regexes in this package are the segment
// splitter and the (structural, not command-wide) secret-path checks.
package screen

import (
	"fmt"
	"regexp"
	"strings"
)

// segmentSplitRE splits a shell command into segments the way a shell would
// break it at control operators, plus command-substitution openers: any run
// of ';', '&', '|', '\n', '\r', or a literal "$(" or backtick.
var segmentSplitRE = regexp.MustCompile("[;&|\n\r]+|\\$\\(|`")

// gitValueOpts are git global options that consume the following token as
// their value (so both tokens are skipped when scanning past them).
var gitValueOpts = map[string]bool{
	"-C": true, "-c": true, "--git-dir": true, "--work-tree": true,
	"--namespace": true, "--exec-path": true, "--super-prefix": true,
	"--config-env": true,
}

// bannedSubcommands are git subcommands refused outright, with a short
// reason. "commit" is deliberately not in this table.
var bannedSubcommands = map[string]string{
	"push":        "history must not be pushed from a screened session",
	"revert":      "destructive git operation; run it yourself if you really want it",
	"rebase":      "rebase is a gated, ask-first operation, not a screened action",
	"tag":         "tagging is a release action, out of scope here",
	"cherry-pick": "history rewriting is out of scope here",
	"am":          "history rewriting is out of scope here",
}

// conditionalRule bans a subcommand only when one of its exact-token flags
// is present later in argv.
type conditionalRule struct {
	subcommand string
	flags      map[string]bool
	reason     string
}

var conditionalGit = []conditionalRule{
	{"reset", map[string]bool{"--hard": true}, "`git reset --hard` throws away uncommitted work"},
	{"branch", map[string]bool{"-D": true, "-d": true, "--delete": true}, "branch deletion is out of scope"},
	{"worktree", map[string]bool{"remove": true, "prune": true}, "worktree removal is out of scope"},
	{"stash", map[string]bool{"drop": true, "clear": true}, "dropping stashes can lose work"},
}

// pathKeys are the ToolCall input keys checked against SecretPath.
var pathKeys = []string{"file_path", "path", "notebook_path", "filePath"}

// unquote strips surrounding whitespace, then any leading/trailing single or
// double quote characters. It is not shell-grade quote parsing: a quoted
// value containing whitespace still arrives pre-split into separate tokens.
func unquote(tok string) string {
	tok = strings.TrimSpace(tok)
	return strings.Trim(tok, "'\"")
}

// isGitBinary reports whether tok names the git binary: its last path
// element, lowercased, is "git" or "git.exe" (so absolute paths and
// "git.exe" both match, case-insensitively).
func isGitBinary(tok string) bool {
	t := strings.ReplaceAll(tok, "\\", "/")
	if i := strings.LastIndexByte(t, '/'); i >= 0 {
		t = t[i+1:]
	}
	t = strings.ToLower(t)
	return t == "git" || t == "git.exe"
}

// gitInvocations scans one already-tokenized segment and returns the argv
// (subcommand onward) of every git invocation found in it. Global options
// before the subcommand are consumed per gitValueOpts (two tokens) or as a
// single flag token otherwise; a token not starting with '-' ends the
// option run and becomes the subcommand.
func gitInvocations(tokens []string) [][]string {
	var invocations [][]string
	for i := 0; i < len(tokens); i++ {
		if !isGitBinary(unquote(tokens[i])) {
			continue
		}
		j := i + 1
		for j < len(tokens) {
			candidate := unquote(tokens[j])
			if !strings.HasPrefix(candidate, "-") {
				break
			}
			if gitValueOpts[candidate] {
				j += 2
			} else {
				j++
			}
		}
		if j > len(tokens) {
			j = len(tokens)
		}
		argv := make([]string, 0, len(tokens)-j)
		for _, t := range tokens[j:] {
			argv = append(argv, unquote(t))
		}
		invocations = append(invocations, argv)
	}
	return invocations
}

// checkGitArgv applies the deny table to one git invocation's argv
// (subcommand first). It returns ("", false) when nothing matches.
func checkGitArgv(argv []string) (string, bool) {
	if len(argv) == 0 {
		return "", false
	}
	sub := argv[0]
	rest := argv[1:]

	if why, ok := bannedSubcommands[sub]; ok {
		return fmt.Sprintf("Blocked `git %s`: %s.", sub, why), true
	}
	for _, rule := range conditionalGit {
		if sub != rule.subcommand {
			continue
		}
		for _, r := range rest {
			if rule.flags[r] {
				return fmt.Sprintf("Blocked `git %s`: %s.", sub, rule.reason), true
			}
		}
	}
	if sub == "clean" {
		for _, a := range rest {
			if strings.HasPrefix(a, "-") && !strings.HasPrefix(a, "--") && strings.Contains(a, "f") {
				return "Blocked `git clean -f`: it deletes untracked files.", true
			}
		}
	}
	// Bare-word backstop: a banned word appearing anywhere later in argv.
	for _, tok := range rest {
		if strings.HasPrefix(tok, "-") {
			continue
		}
		if why, ok := bannedSubcommands[tok]; ok {
			return fmt.Sprintf("Blocked: `%s` appears in a git command line (%s).", tok, why), true
		}
	}
	return "", false
}

// SecretPath reports whether s (a path-like string: a bare path, or one
// whitespace token of a command) looks like it names a live-credential file.
// Case-insensitive. Denies when the basename starts with ".env", contains
// "_key", or starts with "id_rsa"; when the extension is ".pem"; or when the
// path contains "/.aws/" or "\.aws\" (or has a "~/.aws" prefix), or contains
// "/.config/gh/" or "\.config\gh\" (or has a "~/.config/gh" prefix).
//
// The checks operate on the literal string, so a glob token such as
// ".env*", "*.pem", "*_key*" or "~/.aws/*" is denied whenever its fixed
// portion already matches - a conservative stance against globs that could
// expand to a secret path.
func SecretPath(s string) bool {
	if s == "" {
		return false
	}
	norm := strings.ToLower(strings.ReplaceAll(s, "\\", "/"))
	base := norm
	if i := strings.LastIndexByte(norm, '/'); i >= 0 {
		base = norm[i+1:]
	}

	if strings.HasPrefix(base, ".env") {
		return true
	}
	if strings.Contains(base, "_key") {
		return true
	}
	if strings.HasPrefix(base, "id_rsa") {
		return true
	}
	if i := strings.LastIndexByte(base, '.'); i >= 0 && base[i:] == ".pem" {
		return true
	}
	if strings.Contains(norm, "/.aws/") || strings.HasPrefix(norm, "~/.aws") {
		return true
	}
	if strings.Contains(norm, "/.config/gh/") || strings.HasPrefix(norm, "~/.config/gh") {
		return true
	}
	return false
}

// secretReason builds a deny reason mentioning the offending token.
func secretReason(token string) string {
	return fmt.Sprintf("Blocked: `%s` may hold live credentials.", token)
}

// Command screens a shell command string. It returns ("", true) when the
// command is allowed, or (reason, false) when it is denied. Every whitespace
// token is checked against SecretPath first; then every git invocation found
// in the command (see gitInvocations) is checked against the banned-
// subcommand, conditional-flag, clean-short-flag, and bare-word rules.
func Command(cmd string) (string, bool) {
	segments := segmentSplitRE.Split(cmd, -1)

	for _, seg := range segments {
		for _, raw := range strings.Fields(seg) {
			tok := unquote(raw)
			if tok == "" {
				continue
			}
			if SecretPath(tok) {
				return secretReason(tok), false
			}
		}
	}

	for _, seg := range segments {
		tokens := strings.Fields(seg)
		for _, argv := range gitInvocations(tokens) {
			if reason, denied := checkGitArgv(argv); denied {
				return reason, false
			}
		}
	}
	return "", true
}

// ToolCall screens a tool-call hook input: input["command"] (any tool) is
// routed to Command, and each of input["file_path"], input["path"],
// input["notebook_path"], input["filePath"] is checked against SecretPath.
// The tool name is accepted for signature compatibility with the hook layer
// but does not gate which checks run. It returns ("", true) when allowed, or
// (reason, false) when denied.
func ToolCall(tool string, input map[string]any) (string, bool) {
	_ = tool
	if cmdVal, ok := input["command"]; ok {
		if s, ok := cmdVal.(string); ok && s != "" {
			if reason, allowed := Command(s); !allowed {
				return reason, false
			}
		}
	}
	for _, key := range pathKeys {
		v, ok := input[key]
		if !ok {
			continue
		}
		s, ok := v.(string)
		if !ok || s == "" {
			continue
		}
		if SecretPath(s) {
			return secretReason(s), false
		}
	}
	return "", true
}
