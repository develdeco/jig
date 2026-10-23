package setup

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"

	"github.com/develdeco/jig/internal/gitx"
)

// jig's git hooks. Git runs the hooks of one directory: core.hooksPath when
// that is set, else the repository's own hooks directory. To see every commit
// on a machine without touching a single repository, jig sets core.hooksPath
// globally to a directory of its own. That switches off every repository's own
// hooks, so the directory holds a small wrapper for each client hook that runs
// them again, in this order:
//
//  1. the repository's own hook, $GIT_COMMON_DIR/hooks/<name>, with the same
//     arguments and stdin, and its exit code;
//  2. the hook of the core.hooksPath that was set before jig's, kept in
//     .previous beside the wrappers;
//  3. for post-commit only, jig's own step: jig githook post-commit.
//
// A repository that sets core.hooksPath for itself is not reached by the
// global setting at all; it gets a marked block in its post-commit instead
// (InstallClone).

const (
	// wrapperMarker opens the second line of a wrapper jig wrote: the mark by
	// which jig knows a file in its hooks directory is its own.
	wrapperMarker = "# jig-githook-wrapper"
	// previousFile, in the hooks directory, holds the core.hooksPath value
	// that was set before jig's, exactly as it was written.
	previousFile = ".previous"
	// blockBegin and blockEnd bracket the block jig appends to a repository's
	// own post-commit.
	blockBegin = "# >>> jig >>>"
	blockEnd   = "# <<< jig <<<"
	// hooksPathKey is the git setting that names the hooks directory.
	hooksPathKey = "core.hooksPath"
)

// ClientHooks are the git hooks jig has a wrapper for: every client-side hook
// but two. reference-transaction fires on every ref update and reads its
// stdin as a protocol of its own, and fsmonitor-watchman answers git on
// stdout; a wrapper in front of either would cost or break more than it is
// worth, so a repository that has one is told so (jig doctor) and keeps the
// hooks directory it has.
func ClientHooks() []string {
	return []string{
		"applypatch-msg", "pre-applypatch", "post-applypatch",
		"pre-commit", "pre-merge-commit", "prepare-commit-msg", "commit-msg", "post-commit",
		"pre-rebase", "post-checkout", "post-merge", "pre-push", "pre-auto-gc", "post-rewrite",
		"post-index-change", "sendemail-validate",
		"p4-changelist", "p4-prepare-changelist", "p4-post-changelist", "p4-pre-submit",
	}
}

// stdinHooks are the hooks git feeds on stdin; the wrapper keeps a copy so the
// repository's hook and the previous one can each read it.
func stdinHook(hook string) bool { return hook == "pre-push" || hook == "post-rewrite" }

const wrapperHead = `# Written by jig githook; do not edit. jig githook --global rewrites it and
# jig githook --global --uninstall removes it.
#
# core.hooksPath points at this directory, which switches off the hooks every
# repository keeps in its own hooks directory. This wrapper runs them instead:
# the repository's own hook first, then the hook of the core.hooksPath that was
# set before jig's (saved beside this file, in .previous), then jig's own step.

`

const wrapperStdin = `# Git feeds this hook on stdin and two hooks cannot both read it: keep a copy.
stdin_file=$(mktemp "${TMPDIR:-/tmp}/jig-githook.XXXXXX" 2>/dev/null) || stdin_file=
if [ -n "$stdin_file" ]; then
	trap 'rm -f "$stdin_file"' EXIT
	trap 'exit 1' HUP INT TERM
	cat >"$stdin_file"
fi

`

const wrapperRun = `# run FILE ARGS...: run a hook file if it is there and executable, on the saved
# stdin when there is one.
run() {
	[ -f "$1" ] || return 0
	if [ ! -x "$1" ]; then
		# Git on Windows runs any hook file there is; elsewhere it skips one that is
		# not executable, and so does this.
		case $(uname -s 2>/dev/null) in MINGW* | MSYS* | CYGWIN*) ;; *) return 0 ;; esac
	fi
	if [ -n "$stdin_file" ]; then
		"$@" <"$stdin_file"
	else
		"$@"
	fi
}

status=0
run "$common/hooks/$hook" "$@" || status=$?
if [ "$status" -eq 0 ] && [ -n "$previous" ] && [ "$previous" != "$here" ]; then
	run "$previous/$hook" "$@" || status=$?
fi

`

const wrapperJig = `# jig's own step: a ledger line for the commit. It never fails the commit.
if command -v "$jig_bin" >/dev/null 2>&1; then
	"$jig_bin" githook post-commit </dev/null || true
fi

`

// WrapperScript is the wrapper for hook: a POSIX sh script. jig is the
// program part of jig's command (JigCommand), which post-commit runs.
func WrapperScript(hook, jig string) string {
	var b strings.Builder
	b.WriteString("#!/bin/sh\n")
	b.WriteString(wrapperMarker + ": " + hook + "\n")
	b.WriteString(wrapperHead)
	b.WriteString("hook=" + hook + "\n")
	b.WriteString("jig_bin=" + jig + "\n")
	b.WriteString("JIG_GITHOOK_WRAPPED=1\nexport JIG_GITHOOK_WRAPPED\n\n")
	b.WriteString("here=$(dirname \"$0\")\n")
	b.WriteString("common=$(git rev-parse --git-common-dir 2>/dev/null)\n")
	b.WriteString("previous=\n")
	b.WriteString("if [ -f \"$here/" + previousFile + "\" ]; then\n")
	b.WriteString("\tIFS= read -r previous <\"$here/" + previousFile + "\"\n")
	b.WriteString("fi\n")
	b.WriteString("case $previous in\n\"~/\"*) previous=\"$HOME/${previous#\"~/\"}\" ;;\nesac\n\n")
	if stdinHook(hook) {
		b.WriteString(wrapperStdin)
	} else {
		b.WriteString("stdin_file=\n\n")
	}
	b.WriteString(wrapperRun)
	if hook == "post-commit" {
		b.WriteString(wrapperJig)
	}
	b.WriteString("exit \"$status\"\n")
	return b.String()
}

// isWrapper reports whether data is a wrapper jig wrote.
func isWrapper(data []byte) bool {
	lines := bytes.SplitN(data, []byte("\n"), 3)
	return len(lines) >= 2 && bytes.HasPrefix(lines[1], []byte(wrapperMarker))
}

// CloneBlock is the block jig appends to a repository's own post-commit: it
// records the commit unless the global wrapper is already running this hook
// (the wrapper records it itself, and would otherwise record it twice). jig
// is the program part of jig's command.
func CloneBlock(jig string) string {
	return blockBegin + "\n" +
		"# Written by jig githook; jig githook --uninstall removes this block.\n" +
		"[ -n \"${JIG_GITHOOK_WRAPPED-}\" ] || { command -v " + jig + " >/dev/null 2>&1 && " + jig + " githook post-commit </dev/null; } || true\n" +
		blockEnd + "\n"
}

// GitConfig reads and writes git settings through gitx.
type GitConfig struct {
	// Env is laid over the process environment for every git call. A test
	// points GIT_CONFIG_GLOBAL (and, to read a system file, GIT_CONFIG_SYSTEM
	// with GIT_CONFIG_NOSYSTEM=0) at files of its own here, so what it writes
	// never reaches the machine's.
	Env []string
}

// configValue is a setting as written and as git resolves it.
type configValue struct {
	// Raw is the value as it stands in the file ("~/hooks"), Path the value
	// with a leading ~ expanded.
	Raw, Path string
	Set       bool
}

// get reads key from the config scope named by flag ("--global", "--system";
// "" for the merged view as dir sees it). A key that is not set is not an
// error.
func (g GitConfig) get(dir, flag, key string) (configValue, error) {
	read := func(typ string) (string, bool, error) {
		args := []string{"config"}
		if flag != "" {
			args = append(args, flag)
		}
		if typ != "" {
			args = append(args, typ)
		}
		args = append(args, "--get", key)
		out, err := gitx.RunEnv(dir, g.Env, args...)
		if err != nil {
			var exit *exec.ExitError
			if errors.As(err, &exit) && exit.ExitCode() == 1 {
				return "", false, nil
			}
			return "", false, err
		}
		return out, true, nil
	}
	raw, set, err := read("")
	if err != nil || !set {
		return configValue{}, err
	}
	expanded, _, err := read("--type=path")
	if err != nil {
		return configValue{}, err
	}
	return configValue{Raw: raw, Path: expanded, Set: true}, nil
}

func (g GitConfig) set(dir, flag, key, value string) error {
	_, err := gitx.RunEnv(dir, g.Env, "config", flag, key, value)
	return err
}

func (g GitConfig) unset(dir, flag, key string) error {
	_, err := gitx.RunEnv(dir, g.Env, "config", flag, "--unset", key)
	return err
}

// pointsAt reports whether a hooks path value names dir.
func pointsAt(value, dir string) bool {
	if value == "" || !filepath.IsAbs(value) {
		return false
	}
	if gitx.SameDir(value, dir) {
		return true
	}
	return samePathText(value, dir)
}

// samePathText compares two paths as text, ignoring slash direction and, on
// Windows, case.
func samePathText(a, b string) bool {
	a, b = filepath.Clean(filepath.FromSlash(a)), filepath.Clean(filepath.FromSlash(b))
	if os.PathSeparator == '\\' {
		return strings.EqualFold(a, b)
	}
	return a == b
}

// GlobalStatus is how the global git hooks stand.
type GlobalStatus struct {
	HooksDir string
	// Config is the global core.hooksPath as written ("" when it is not set).
	Config string
	// Active is whether it names HooksDir.
	Active bool
	// Previous is the core.hooksPath chained after the repository's own hooks
	// ("" for none).
	Previous string
	// Stale lists the wrappers that are missing, or are not what this jig would
	// write (another path of the binary, an older script).
	Stale []string
}

// InspectGlobal reads how the global git hooks stand against the wrappers jig
// (whose command is jig) would write into hooksDir. It writes nothing.
func (g GitConfig) InspectGlobal(hooksDir, jig string) (GlobalStatus, error) {
	st := GlobalStatus{HooksDir: hooksDir}
	cur, err := g.get("", "--global", hooksPathKey)
	if err != nil {
		return st, err
	}
	st.Config = cur.Raw
	st.Active = cur.Set && pointsAt(cur.Path, hooksDir)
	st.Previous = readPrevious(hooksDir)
	for _, hook := range ClientHooks() {
		have, exists, err := readIfExists(filepath.Join(hooksDir, hook))
		if err != nil {
			return st, err
		}
		if !exists || string(have) != WrapperScript(hook, jig) {
			st.Stale = append(st.Stale, hook)
		}
	}
	return st, nil
}

// readPrevious reads the saved core.hooksPath of hooksDir; "" for none.
func readPrevious(hooksDir string) string {
	data, _, err := readIfExists(filepath.Join(hooksDir, previousFile))
	if err != nil {
		return ""
	}
	first, _, _ := strings.Cut(string(data), "\n")
	return strings.TrimSpace(first)
}

// systemHooksPath is the core.hooksPath of the system git config, which a
// global one would shadow; "" for none. A system file git cannot read is no
// reason to refuse.
func (g GitConfig) systemHooksPath() string {
	v, err := g.get("", "--system", hooksPathKey)
	if err != nil || !v.Set {
		return ""
	}
	return v.Raw
}

// GlobalResult is what installing or removing the global git hooks did.
type GlobalResult struct {
	HooksDir string
	// Previous is the core.hooksPath that runs after the repository's own
	// hooks ("" for none).
	Previous string
	// Config says what happened to the global core.hooksPath: "set" (now
	// names HooksDir), "unchanged", "restored" (back to its earlier value),
	// "unset", or "left" (it names something else, which uninstall does not
	// touch).
	Config string
	// Wrote and Removed list the wrapper files created or rewritten, and
	// deleted.
	Wrote, Removed []string
	// Changed is whether anything on disk or in git config changed.
	Changed bool
}

// InstallGlobal writes the wrappers into hooksDir and points the global
// core.hooksPath at it, saving the value it had before. It is idempotent: a
// second run finds everything as it left it. The wrappers and .previous are
// written before the setting is switched, so git never reads a half-built
// directory.
//
// A core.hooksPath that is set already, and is not hooksDir, is saved in
// .previous and chained; one set only in the system config is chained the same
// way, since the global one would otherwise shadow it. A file in hooksDir that
// is not a wrapper of jig's is an error, and nothing is changed.
func (g GitConfig) InstallGlobal(hooksDir, jig string) (GlobalResult, error) {
	res := GlobalResult{HooksDir: hooksDir}
	if err := os.MkdirAll(hooksDir, 0o755); err != nil {
		return res, err
	}
	cur, err := g.get(hooksDir, "--global", hooksPathKey)
	if err != nil {
		return res, err
	}
	active := cur.Set && pointsAt(cur.Path, hooksDir)

	switch {
	case active:
		res.Previous = readPrevious(hooksDir)
	case cur.Set:
		res.Previous = cur.Raw
	default:
		res.Previous = g.systemHooksPath()
	}

	// Check every file first, so a refusal changes nothing.
	for _, hook := range ClientHooks() {
		have, exists, err := readIfExists(filepath.Join(hooksDir, hook))
		if err != nil {
			return res, err
		}
		if exists && !isWrapper(have) {
			return res, fmt.Errorf("%s is not a hook wrapper of jig's; move it away and run again", filepath.Join(hooksDir, hook))
		}
	}

	prevPath := filepath.Join(hooksDir, previousFile)
	if res.Previous != "" {
		if readPrevious(hooksDir) != res.Previous {
			if err := writeFile(prevPath, []byte(res.Previous+"\n"), 0o644, false); err != nil {
				return res, err
			}
			res.Changed = true
		}
	} else if err := os.Remove(prevPath); err == nil {
		res.Changed = true
	}

	for _, hook := range ClientHooks() {
		file := filepath.Join(hooksDir, hook)
		want := WrapperScript(hook, jig)
		if have, exists, _ := readIfExists(file); exists && string(have) == want {
			continue
		}
		if err := writeFile(file, []byte(want), 0o755, false); err != nil {
			return res, err
		}
		res.Wrote = append(res.Wrote, hook)
		res.Changed = true
	}

	if active {
		res.Config = "unchanged"
		return res, nil
	}
	// Forward slashes: git reads them everywhere, and a backslash escapes in a
	// config value.
	if err := g.set(hooksDir, "--global", hooksPathKey, filepath.ToSlash(hooksDir)); err != nil {
		return res, err
	}
	res.Config = "set"
	res.Changed = true
	return res, nil
}

// UninstallGlobal puts the global core.hooksPath back the way it was and
// removes the wrappers. A core.hooksPath that no longer names hooksDir was set
// by someone since and is left alone. Files in hooksDir that are not wrappers
// of jig's stay, and so does the directory then.
func (g GitConfig) UninstallGlobal(hooksDir string) (GlobalResult, error) {
	res := GlobalResult{HooksDir: hooksDir, Previous: readPrevious(hooksDir)}
	parent := filepath.Dir(hooksDir)
	if _, err := os.Stat(parent); err != nil {
		parent = ""
	}
	cur, err := g.get(parent, "--global", hooksPathKey)
	if err != nil {
		return res, err
	}
	switch {
	case cur.Set && pointsAt(cur.Path, hooksDir):
		// A previous value that the system config also holds was never in the
		// global file: unsetting the global one gives it back.
		if res.Previous != "" && res.Previous != g.systemHooksPath() {
			err = g.set(parent, "--global", hooksPathKey, res.Previous)
			res.Config = "restored"
		} else {
			err = g.unset(parent, "--global", hooksPathKey)
			res.Config = "unset"
		}
		if err != nil {
			return res, err
		}
		res.Changed = true
	case cur.Set:
		res.Config = "left"
	default:
		res.Config = "unchanged"
	}

	for _, hook := range ClientHooks() {
		file := filepath.Join(hooksDir, hook)
		have, exists, err := readIfExists(file)
		if err != nil {
			return res, err
		}
		if !exists || !isWrapper(have) {
			continue
		}
		if err := os.Remove(file); err != nil {
			return res, err
		}
		res.Removed = append(res.Removed, hook)
		res.Changed = true
	}
	if err := os.Remove(filepath.Join(hooksDir, previousFile)); err == nil {
		res.Changed = true
	}
	_ = os.Remove(hooksDir) // only if empty
	return res, nil
}

// The states a repository can be in after InstallClone or UninstallClone.
const (
	// CloneCovered: the repository's hooks directory is jig's own, so the
	// wrappers already run for it.
	CloneCovered = "covered"
	// CloneHasBlock: the repository's post-commit holds jig's block.
	CloneHasBlock = "block"
	// CloneNone: it holds none (after an uninstall, or when there was none).
	CloneNone = "none"
	// CloneSkipped: jig left the repository's hook alone; Reason says why.
	CloneSkipped = "skipped"
)

// CloneResult is what installing into or removing from one repository did.
type CloneResult struct {
	Clone string
	State string
	// Path is the post-commit file the block lives in, or would.
	Path string
	// Reason says why a repository was skipped.
	Reason string
	// Changed is whether a file was written or removed.
	Changed bool
}

// hooksLocation is where git runs one repository's hooks from.
type hooksLocation struct {
	// Top is the top of the repository's work tree and Common its git common
	// directory.
	Top, Common string
	// Dir is the effective hooks directory: core.hooksPath as git resolves it
	// for the repository (a relative one against the work tree), else the hooks
	// directory under Common.
	Dir string
	// Shared is whether Dir comes from a core.hooksPath that is not the
	// repository's own (the global or system config sets it), so other
	// repositories run the same hooks.
	Shared bool
}

// cloneHooks finds where git runs clone's hooks from.
func (g GitConfig) cloneHooks(clone string) (hooksLocation, error) {
	var h hooksLocation
	top, err := gitx.TopLevel(clone)
	if err != nil {
		return h, fmt.Errorf("%s is not a git work tree: %w", clone, err)
	}
	common, err := gitx.CommonDir(top)
	if err != nil {
		return h, err
	}
	h.Top, h.Common = top, common
	v, err := g.get(top, "", hooksPathKey)
	if err != nil {
		return h, err
	}
	switch {
	case !v.Set:
		h.Dir = filepath.Join(common, "hooks")
		return h, nil
	case filepath.IsAbs(v.Path):
		h.Dir = filepath.Clean(v.Path)
	default:
		h.Dir = filepath.Join(top, v.Path)
	}
	local, err := g.get(top, "--local", hooksPathKey)
	if err != nil {
		return h, err
	}
	h.Shared = !local.Set
	return h, nil
}

// InstallClone makes sure a commit in clone is recorded. A repository whose
// hooks directory is jig's (the global core.hooksPath, with nothing of its own)
// is covered by the wrappers. Any other gets jig's block in its post-commit,
// in the hooks directory git really uses for it: its own .git/hooks, or the
// core.hooksPath it sets for itself, which the global setting does not reach.
//
// jig leaves a post-commit alone, and says why, when it is not a shell script
// (a block of sh would corrupt it), or when it sits in the repository's work
// tree without being ignored by git, where it may be a tracked file that a
// change of jig's would dirty.
func (g GitConfig) InstallClone(clone, hooksDir, jig string) (CloneResult, error) {
	res := CloneResult{Clone: clone}
	h, err := g.cloneHooks(clone)
	if err != nil {
		return res, err
	}
	if pointsAt(h.Dir, hooksDir) {
		res.State = CloneCovered
		return res, nil
	}
	if h.Shared {
		res.State = CloneSkipped
		res.Reason = "core.hooksPath is set outside the repository, to " + filepath.ToSlash(h.Dir) +
			", and every repository that uses it would run the block; jig githook --global chains it instead"
		return res, nil
	}
	res.Path = filepath.Join(h.Dir, "post-commit")
	if reason := g.untouchable(h.Top, h.Common, res.Path); reason != "" {
		res.State, res.Reason = CloneSkipped, reason
		return res, nil
	}
	data, exists, err := readIfExists(res.Path)
	if err != nil {
		return res, err
	}
	block := []byte(CloneBlock(jig))
	var out []byte
	switch {
	case !exists || len(bytes.TrimSpace(data)) == 0:
		out = append([]byte("#!/bin/sh\n"), block...)
	default:
		if ok, why := shellScript(data); !ok {
			res.State, res.Reason = CloneSkipped, "its post-commit is "+why+"; add jig githook post-commit to it by hand"
			return res, nil
		}
		begin, end, found, err := findBlock(data)
		if err != nil {
			return res, fmt.Errorf("%s: %w", res.Path, err)
		}
		if found {
			out = append(append(append([]byte{}, data[:begin]...), block...), data[end:]...)
		} else {
			out = append([]byte{}, data...)
			if !bytes.HasSuffix(out, []byte("\n")) {
				out = append(out, '\n')
			}
			out = append(out, block...)
		}
	}
	res.State = CloneHasBlock
	if exists && bytes.Equal(out, data) {
		return res, nil
	}
	if err := writeFile(res.Path, out, 0o755, true); err != nil {
		return res, err
	}
	res.Changed = true
	return res, nil
}

// UninstallClone removes jig's block from clone's post-commit, wherever the
// repository runs hooks from (its effective hooks directory, and its own
// .git/hooks in case core.hooksPath changed since). A post-commit that held
// nothing but the block is deleted.
func (g GitConfig) UninstallClone(clone, hooksDir string) (CloneResult, error) {
	res := CloneResult{Clone: clone, State: CloneNone}
	h, err := g.cloneHooks(clone)
	if err != nil {
		return res, err
	}
	if pointsAt(h.Dir, hooksDir) {
		res.State = CloneCovered
	}
	dirs := []string{filepath.Join(h.Common, "hooks")}
	if !h.Shared {
		dirs = append(dirs, h.Dir)
	}
	for _, d := range dirs {
		file := filepath.Join(d, "post-commit")
		if res.Path != "" && samePathText(res.Path, file) {
			continue
		}
		changed, err := removeBlock(file)
		if err != nil {
			return res, err
		}
		if changed {
			res.Path, res.Changed = file, true
		}
	}
	return res, nil
}

// untouchable returns why jig must not edit file in clone's hooks directory,
// or "" when it may.
func (g GitConfig) untouchable(top, common, file string) string {
	rel, err := filepath.Rel(top, file)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return "" // outside the work tree
	}
	if crel, err := filepath.Rel(common, file); err == nil && crel != ".." && !strings.HasPrefix(crel, ".."+string(filepath.Separator)) {
		return "" // inside the git directory
	}
	if _, err := gitx.Run(top, "check-ignore", "-q", "--", filepath.ToSlash(rel)); err == nil {
		return "" // inside the work tree, and git ignores it
	}
	return "its hooks directory is inside the work tree and " + filepath.ToSlash(rel) + " is not ignored by git, so it may be tracked; jig does not edit it"
}

// shellScript reports whether data may take a block of sh at its end, and if
// not what it is instead.
func shellScript(data []byte) (ok bool, why string) {
	if bytes.IndexByte(data, 0) >= 0 {
		return false, "not a script"
	}
	if !bytes.HasPrefix(data, []byte("#!")) {
		return true, "" // git runs a script with no interpreter line as sh
	}
	line, _, _ := strings.Cut(string(data), "\n")
	fields := strings.Fields(strings.TrimPrefix(line, "#!"))
	if len(fields) == 0 {
		return true, ""
	}
	interp := path.Base(filepath.ToSlash(fields[0]))
	if interp == "env" {
		for _, f := range fields[1:] {
			if !strings.HasPrefix(f, "-") {
				interp = path.Base(f)
				break
			}
		}
	}
	switch interp {
	case "sh", "bash", "dash", "zsh", "ksh", "ash", "busybox":
		return true, ""
	}
	return false, "a " + interp + " script"
}

// findBlock locates jig's block in data: begin is the offset of its first line,
// end the offset after its last line and the newline. An opening line with no
// closing one is an error: jig will not guess where the block stops.
func findBlock(data []byte) (begin, end int, found bool, err error) {
	return findBlockBetween(data, blockBegin, blockEnd)
}

// findBlockBetween is findBlock for a block bracketed by the lines open and
// closing.
func findBlockBetween(data []byte, open, closing string) (begin, end int, found bool, err error) {
	off := 0
	begin = -1
	for off < len(data) {
		next := bytes.IndexByte(data[off:], '\n')
		lineEnd, after := len(data), len(data)
		if next >= 0 {
			lineEnd, after = off+next, off+next+1
		}
		line := strings.TrimRight(string(data[off:lineEnd]), "\r")
		switch {
		case begin < 0 && line == open:
			begin = off
		case begin >= 0 && line == closing:
			return begin, after, true, nil
		}
		off = after
	}
	if begin >= 0 {
		return 0, 0, false, errors.New("jig's block is opened and never closed; remove it by hand")
	}
	return 0, 0, false, nil
}

// removeBlock takes jig's block out of the hook file at file. A file that is
// then empty, or only the interpreter line jig wrote, is deleted. It reports
// whether it changed anything.
func removeBlock(file string) (bool, error) {
	data, exists, err := readIfExists(file)
	if err != nil || !exists {
		return false, err
	}
	begin, end, found, err := findBlock(data)
	if err != nil {
		return false, fmt.Errorf("%s: %w", file, err)
	}
	if !found {
		return false, nil
	}
	rest := append(append([]byte{}, data[:begin]...), data[end:]...)
	if t := strings.TrimSpace(string(rest)); t == "" || t == "#!/bin/sh" {
		if err := os.Remove(file); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return false, err
		}
		return true, nil
	}
	return true, writeFile(file, rest, 0o755, true)
}
