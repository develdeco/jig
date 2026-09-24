// Package screen implements jig's structural command screen and secret-path
// screen. git commands are parsed structurally (segment split, whitespace
// tokenize, unquote, git-binary match, global-option consumption) rather
// than matched against a regex over the whole command line - see
// ARCHITECTURE.md's Safety section for why a regex isn't enough. The only
// regexes in this package are the segment splitter and the (structural, not
// command-wide) secret-path checks.
package screen

import (
	"fmt"
	"path/filepath"
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

// toolPathArgs maps every tool a headless session is given to the input
// keys that select files for it. A tool selects files under more than one
// key - Grep filters with "glob" and Glob selects with "pattern" - so a
// fixed key list shared by every tool misses the ones it has not heard of,
// and a Grep whose "glob" names a credential file returns that file's
// contents while a Read of the same path is denied.
//
// A tool that is not in this map is one this screen cannot reason about.
// ToolCall denies it: the screen is the grant, so what it cannot judge it
// does not allow. Adding a tool to the session's surface means adding it
// here, with the keys that pick what it reads or writes.
var toolPathArgs = map[string][]string{
	"Bash":         {},
	"Read":         {"file_path"},
	"Write":        {"file_path"},
	"Edit":         {"file_path"},
	"NotebookEdit": {"notebook_path", "file_path"},
	"Glob":         {"pattern", "path"},
	"Grep":         {"glob", "path"},
}

// legacyPathKeys are checked on every known tool on top of its own keys, so
// a key renamed between CLI versions still reaches SecretPath.
var legacyPathKeys = []string{"file_path", "path", "notebook_path", "filePath"}

// requiredPathArg names, for a tool that must always say what it acts on,
// the one input key that names it: the path a file tool reads or writes,
// or the pattern Glob searches with. A known tool whose required key is
// absent, or present with a type SecretPath cannot read, is denied outright
// by ToolCall - a call the screen cannot read cannot be judged, and a
// silent skip there is exactly the fail-open gap this closes. Grep has no
// entry: its required "pattern" is a search term, not a path, and its
// path-shaped keys (glob, path) are legitimately optional - a rooted Grep
// with no path searches the cwd.
var requiredPathArg = map[string]string{
	"Read":         "file_path",
	"Write":        "file_path",
	"Edit":         "file_path",
	"NotebookEdit": "notebook_path",
	"Glob":         "pattern",
}

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
// "_key", or starts with "id_rsa"; when the extension is ".pem"; when the
// basename is a ".netrc", "_netrc" or ".npmrc"; when any segment of the path
// is a credential directory (credentialDirs: ".aws", ".ssh", ".gnupg",
// ".config/gh", ".docker", ".kube") - a directory counts as much as a file
// under it, since a tool given a search root reads everything beneath it;
// or when the path's final segments exactly name a single credential file
// that sits in an otherwise ordinary directory (credentialFiles below).
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
	if strings.HasPrefix(base, ".netrc") || strings.HasPrefix(base, "_netrc") || strings.HasPrefix(base, ".npmrc") {
		return true
	}
	if inCredentialFile(norm) {
		return true
	}
	return inCredentialDir(norm)
}

// credentialDirs are directories whose contents are credentials. A path is
// denied when it is one of them, is inside one, or would create one: the
// directory itself is as much a credential location as the files in it,
// since a tool that takes a directory (a search root, a glob root) reads
// everything under it.
var credentialDirs = [][]string{
	{".aws"},
	{".ssh"},
	{".gnupg"},
	{".config", "gh"},
	{".docker"},
	{".kube"},
}

// inCredentialDir reports whether a slash-normalized, lowercased path has a
// credential directory among its segments, whatever it is spelled with: a
// trailing separator or not, "~" or an absolute root, the directory itself
// or a file under it.
func inCredentialDir(norm string) bool {
	segs := strings.Split(strings.Trim(norm, "/"), "/")
	for _, dir := range credentialDirs {
		for i := 0; i+len(dir) <= len(segs); i++ {
			match := true
			for j, want := range dir {
				if segs[i+j] != want {
					match = false
					break
				}
			}
			if match {
				return true
			}
		}
	}
	return false
}

// credentialFiles are exact path-segment sequences naming one specific
// credential file that sits in an otherwise ordinary directory: git's own
// config lives beside its stored push token in ".config/git", and a
// lease's ".claude" directory holds its readable settings.json and skills
// beside the CLI's own credential cache. Unlike credentialDirs, nothing
// legitimately sits under one of these, so inCredentialFile matches only
// at the end of the path - a project's own directory that happens to share
// a segment name is not swept in.
var credentialFiles = [][]string{
	{".git-credentials"},
	{".claude", ".credentials.json"},
	{".claude.json"},
	{".config", "git", "credentials"},
	{".azure", "msal_token_cache.json"},
	{".config", "gcloud", "credentials.db"},
	{".gem", "credentials"},
	{".pypirc"},
	{".terraform.d", "credentials.tfrc.json"},
}

// inCredentialFile reports whether a slash-normalized, lowercased path's
// final segments exactly match one of credentialFiles.
func inCredentialFile(norm string) bool {
	segs := strings.Split(strings.Trim(norm, "/"), "/")
	for _, want := range credentialFiles {
		if len(want) > len(segs) {
			continue
		}
		tail := segs[len(segs)-len(want):]
		match := true
		for j, w := range want {
			if tail[j] != w {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

// isNetworkOrDevicePath reports whether p is a UNC share (\\host\share or
// //host/share) or a Windows device/extended-length path (\\?\..., \\.\...).
// filepath.VolumeName recognizes the backslash UNC form (and, on the
// platform that has volumes, the device forms); the explicit prefix check
// on top catches the forward-slash UNC spelling, which VolumeName does not,
// and keeps the check meaningful when this package is built for a GOOS
// where VolumeName always returns "". Judged on the literal string alone -
// this function does no filesystem I/O, which is the point of calling it.
func isNetworkOrDevicePath(p string) bool {
	if vol := filepath.VolumeName(p); len(vol) >= 2 && isSlash(vol[0]) && isSlash(vol[1]) {
		return true
	}
	slashed := strings.ReplaceAll(p, "\\", "/")
	return strings.HasPrefix(slashed, "//")
}

func isSlash(b byte) bool {
	return b == '\\' || b == '/'
}

// secretTarget reports whether p, resolved against the filesystem, names a
// credential location. SecretPath judges the literal string, which is what
// a command token or a glob gives it; this judges what a tool call would
// actually open, so a symlink in the lease pointing at "~/.aws", or a
// relative path that climbs out of it, is denied by where it lands rather
// than by how it is written.
//
// Resolution is best effort: a path that does not exist yet is judged by
// its literal form alone, which SecretPath already covers. A network share
// or a device path (isNetworkOrDevicePath) is never resolved, stat'd or
// opened here: doing so can dial a remote host - an unroutable address
// blocks the call for tens of seconds - or touch a device, and the screen
// has no business paying that cost, or making that connection, on the
// ticket's say-so. Such a token is judged by its spelling alone, which
// SecretPath already covers; secretTarget just declines to look further.
func secretTarget(p string) bool {
	if p == "" || isNetworkOrDevicePath(p) {
		return false
	}
	abs, err := filepath.Abs(p)
	if err != nil {
		return false
	}
	if isNetworkOrDevicePath(abs) {
		return false
	}
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = resolved
	}
	norm := strings.ToLower(strings.ReplaceAll(abs, "\\", "/"))
	if inCredentialDir(norm) {
		return true
	}
	return SecretPath(norm)
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
			if SecretPath(tok) || secretTarget(tok) {
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

// ToolCall screens a tool-call hook input. The tool name decides which
// input keys name files (toolPathArgs), and a tool that is not in that map
// is denied outright rather than judged on a guess. Bash's "command" and,
// for a tool listed in requiredPathArg, that tool's required key must both
// be present with a type SecretPath can read (a string, or a list of
// strings) - missing, or present as a list where a string is expected, a
// number, an object or null, denies the call outright: a screen that
// cannot read an argument cannot judge it, and granting by default on an
// unreadable argument is the fail-open gap this closes. unreadablePath is
// not only for the required key: every path-like argument of the tool,
// plus the legacy key names, gets the same unreadable-type check before it
// is checked against SecretPath and, resolved against the filesystem,
// secretTarget. It returns ("", true) when allowed, or (reason, false) when
// denied. An extra key the tool sends that the screen has no rule for
// (a benign one, such as Bash's "description") is not inspected and does
// not affect the decision.
func ToolCall(tool string, input map[string]any) (string, bool) {
	keys, known := toolPathArgs[tool]
	if !known {
		return fmt.Sprintf("Blocked: `%s` is a tool this screen does not know, so it cannot be judged.", tool), false
	}

	if tool == "Bash" {
		cmdVal, present := input["command"]
		if !present {
			return "Blocked: Bash call has no `command`, so it cannot be judged.", false
		}
		s, isStr := cmdVal.(string)
		if !isStr {
			return "Blocked: Bash call's `command` is not a string, so it cannot be judged.", false
		}
		if s != "" {
			if reason, allowed := Command(s); !allowed {
				return reason, false
			}
		}
	}

	if req, ok := requiredPathArg[tool]; ok {
		v, present := input[req]
		if !present {
			return fmt.Sprintf("Blocked: `%s` call has no `%s`, so it cannot be judged.", tool, req), false
		}
		if unreadablePath(v) {
			return fmt.Sprintf("Blocked: `%s` call's `%s` cannot be read, so it cannot be judged.", tool, req), false
		}
	}

	for _, key := range append(append([]string{}, keys...), legacyPathKeys...) {
		v, ok := input[key]
		if !ok {
			continue
		}
		if unreadablePath(v) {
			return fmt.Sprintf("Blocked: `%s` call's `%s` cannot be read, so it cannot be judged.", tool, key), false
		}
		for _, s := range stringValues(v) {
			if SecretPath(s) || secretTarget(s) {
				return secretReason(s), false
			}
		}
	}
	return "", true
}

// unreadablePath reports whether v cannot be read as a path or a list of
// paths: anything other than a string, or a list whose elements are all
// strings, is a type SecretPath cannot judge - a number, a bool, an
// object, null, or a list holding a non-string element (nested lists
// included).
func unreadablePath(v any) bool {
	switch t := v.(type) {
	case string:
		return false
	case []any:
		for _, e := range t {
			if _, ok := e.(string); !ok {
				return true
			}
		}
		return false
	case []string:
		return false
	default:
		return true
	}
}

// stringValues returns v as the strings it holds: the value itself, or the
// string elements of a list, since a tool may take one path or several
// under the same key. Called only after unreadablePath(v) is false, so the
// type switch here always finds one of these three shapes.
func stringValues(v any) []string {
	switch t := v.(type) {
	case string:
		if t == "" {
			return nil
		}
		return []string{t}
	case []any:
		var out []string
		for _, e := range t {
			if s, ok := e.(string); ok && s != "" {
				out = append(out, s)
			}
		}
		return out
	case []string:
		return t
	}
	return nil
}

// Granted lists the tools a passing screen grants outright: jig's own
// settings grant a headless session's shell and file-read tools only
// through the screen hook's "allow" (docs/adr/0008-headless-permission-model.md).
// A hook that never runs gets no such grant from jig, but that is not the
// same as denied outright: Claude Code's own read-only classifier still
// lets part of the shell through with no rule from jig involved, which is
// why verifyScreen proves the hook before every screened dispatch instead
// of relying on this grant alone. File-edit tools are deliberately absent:
// the headless backend grants them through path-scoped permission rules,
// and the screen only ever denies them.
var Granted = []string{"Bash", "Read", "Glob", "Grep"}

// Grants reports whether a passing screen is tool's grant, i.e. whether
// tool is in Granted.
func Grants(tool string) bool {
	for _, g := range Granted {
		if g == tool {
			return true
		}
	}
	return false
}
