package gitx

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"

	format "github.com/go-git/go-git/v5/plumbing/format/config"
)

// envLookup is os.LookupEnv, or a test's own environment: the in-process
// reads look at the variables git itself would, through it, so a test can
// say what the environment holds without editing the process's.
type envLookup func(key string) (string, bool)

// readDeclinedEnv names the variables that change where git looks or what
// it finds, and that gitx's runs inherit (repoEnv are dropped from them, and
// from the in-process reads alike). With one of them set the in-process
// reads answer nothing and the git program is asked.
var readDeclinedEnv = []string{
	"GIT_CEILING_DIRECTORIES",
	"GIT_DISCOVERY_ACROSS_FILESYSTEM",
	"GIT_NAMESPACE",
	"GIT_REPLACE_REF_BASE",
	"GIT_GRAFT_FILE",
	"GIT_SHALLOW_FILE",
}

// repoLoc is where a repository's files are, as discovery found them.
type repoLoc struct {
	// workTree is the work tree's top level: the directory holding the .git
	// entry, resolved of symbolic links.
	workTree string
	// gitDir is the git directory of this work tree: workTree/.git, or the
	// directory a linked worktree's or submodule's .git file names.
	gitDir string
	// commonDir is the directory shared by every worktree of the repository
	// (objects, most refs, config): gitDir itself unless gitDir has a
	// commondir file.
	commonDir string
	// prefix is the directory discovery started in, relative to workTree, in
	// the form `git rev-parse --show-prefix` prints it: slash separated with
	// a trailing slash, and empty at the top level.
	prefix string
	// head is the content of gitDir/HEAD, trailing whitespace removed.
	head string
}

// discover finds the repository dir is in the way git does, from the files
// alone, or reports false when the git program must be asked: anything that
// is not the plain case, which is a directory (symbolic links resolved)
// holding a .git directory or a .git file of one line, "gitdir: <path>", at
// dir itself or at the nearest parent that does.
//
// It declines (false) for what it does not do exactly as git does:
//   - an environment variable that moves git's search or its refs
//     (readDeclinedEnv), and no directory to start from;
//   - a directory on the way that is named .git or is itself a git
//     directory, which git answers "inside the git directory" or "bare"
//     for, and a .git that is not valid, which git steps over;
//   - no repository above dir at all, and, on Windows, a directory reached
//     through a junction (git resolves it, filepath.EvalSymlinks does not);
//   - a .git that is a symbolic link, or a .git file that is not exactly a
//     gitdir line;
//   - a HEAD that git would not accept, a git directory without objects or
//     refs, or a commondir file that names no such directory;
//   - a config (the repository's own, in the common directory) that holds
//     core.bare true, any core.worktree, a core.repositoryformatversion
//     above 1, any extensions.* entry, or an include;
//   - a config that cannot be parsed.
//
// The ownership checks of safe.directory are not made: nothing here runs
// anything the repository names, and every other command still has git's
// own check to pass.
func discover(dir string, env envLookup) (*repoLoc, bool) {
	if dir == "" {
		return nil, false
	}
	for _, name := range readDeclinedEnv {
		if _, set := env(name); set {
			return nil, false
		}
	}
	abs, err := filepath.Abs(dir)
	if err != nil {
		return nil, false
	}
	start, err := filepath.EvalSymlinks(abs)
	if err != nil {
		return nil, false
	}
	if fi, err := os.Stat(start); err != nil || !fi.IsDir() {
		return nil, false
	}
	if runtime.GOOS == "windows" && underMountPoint(start) {
		return nil, false
	}
	for cur := start; ; {
		if strings.EqualFold(filepath.Base(cur), ".git") {
			return nil, false
		}
		dotGit := filepath.Join(cur, ".git")
		fi, err := os.Lstat(dotGit)
		switch {
		case err == nil:
			return locateAt(cur, dotGit, fi, start)
		case isAbsent(err):
			// No .git here. A git directory itself is what git would call
			// a bare repository, or the inside of one.
			if _, err := os.Lstat(filepath.Join(cur, "HEAD")); err == nil || !isAbsent(err) {
				return nil, false
			}
		default:
			return nil, false
		}
		parent := filepath.Dir(cur)
		if parent == cur {
			return nil, false
		}
		cur = parent
	}
}

// underMountPoint reports whether path, or a directory above it, is a
// reparse point other than a symbolic link (a junction, say), which
// filepath.EvalSymlinks does not resolve and git does: the real path git
// prints for a directory reached through one is not the spelling discovery
// would find. Only Windows has them.
func underMountPoint(path string) bool {
	for p := path; ; {
		fi, err := os.Lstat(p)
		if err != nil || fi.Mode()&os.ModeIrregular != 0 {
			return true
		}
		parent := filepath.Dir(p)
		if parent == p {
			return false
		}
		p = parent
	}
}

// isAbsent reports whether err says a path, or a directory on the way to
// it, is not there.
func isAbsent(err error) bool {
	return errors.Is(err, fs.ErrNotExist) || errors.Is(err, syscall.ENOTDIR)
}

// locateAt validates the repository whose .git entry (dotGit, fi its
// Lstat) sits in workTree, discovery having started in start.
func locateAt(workTree, dotGit string, fi fs.FileInfo, start string) (*repoLoc, bool) {
	var gitDir string
	switch {
	case fi.Mode()&fs.ModeSymlink != 0:
		return nil, false
	case fi.IsDir():
		gitDir = dotGit
	case fi.Mode().IsRegular() && fi.Size() < 4096:
		data, err := os.ReadFile(dotGit)
		if err != nil {
			return nil, false
		}
		line := strings.TrimRight(string(data), "\r\n")
		path, ok := strings.CutPrefix(line, "gitdir: ")
		if !ok || path == "" || strings.ContainsAny(path, "\r\n\x00") {
			return nil, false
		}
		if !filepath.IsAbs(path) {
			path = filepath.Join(workTree, filepath.FromSlash(path))
		}
		if gitDir, err = filepath.EvalSymlinks(filepath.Clean(path)); err != nil {
			return nil, false
		}
	default:
		return nil, false
	}
	loc := &repoLoc{workTree: workTree, gitDir: gitDir, commonDir: gitDir}
	if data, err := os.ReadFile(filepath.Join(gitDir, "commondir")); err == nil {
		common := strings.TrimRight(string(data), " \t\r\n")
		if common == "" || strings.ContainsRune(common, 0) {
			return nil, false
		}
		if !filepath.IsAbs(common) {
			common = filepath.Join(gitDir, filepath.FromSlash(common))
		}
		if loc.commonDir, err = filepath.EvalSymlinks(filepath.Clean(common)); err != nil {
			return nil, false
		}
	} else if !isAbsent(err) {
		return nil, false
	}
	if fi, err := os.Lstat(filepath.Join(gitDir, "HEAD")); err != nil || !fi.Mode().IsRegular() {
		return nil, false // a symbolic link as HEAD is an old git's symbolic ref
	}
	head, err := os.ReadFile(filepath.Join(gitDir, "HEAD"))
	if err != nil || len(head) > 1024 {
		return nil, false
	}
	loc.head = strings.TrimRight(string(head), " \t\r\n")
	if !validHead(loc.head) {
		return nil, false
	}
	for _, name := range []string{"objects", "refs"} {
		if fi, err := os.Stat(filepath.Join(loc.commonDir, name)); err != nil || !fi.IsDir() {
			return nil, false
		}
	}
	if configDeclines(loc.commonDir) {
		return nil, false
	}
	if rel, err := filepath.Rel(workTree, start); err != nil {
		return nil, false
	} else if rel != "." {
		loc.prefix = filepath.ToSlash(rel) + "/"
	}
	return loc, true
}

// validHead reports whether the content of a HEAD file is one git accepts
// as a git directory's: a ref under refs/, or an object name.
func validHead(head string) bool {
	if target, ok := strings.CutPrefix(head, "ref:"); ok {
		return plausibleRefName(strings.TrimLeft(target, " \t"))
	}
	return isHex40(head)
}

// configDeclines reports whether the config in commonDir says something the
// in-process reads do not honor, or cannot be read: see discover. A missing
// config is an empty one.
func configDeclines(commonDir string) bool {
	f, err := os.Open(filepath.Join(commonDir, "config"))
	if err != nil {
		return !isAbsent(err)
	}
	defer f.Close()
	raw := format.New()
	if err := format.NewDecoder(f).Decode(raw); err != nil {
		return true
	}
	for _, s := range raw.Sections {
		if s.IsName("include") || s.IsName("includeIf") {
			return true
		}
		if s.IsName("extensions") && (len(s.Options) > 0 || len(s.Subsections) > 0) {
			return true
		}
	}
	core := raw.Section("core")
	if v := core.Option("repositoryFormatVersion"); v != "" && v != "0" && v != "1" {
		return true
	}
	return isTrue(core, "bare") || core.HasOption("worktree")
}

// isHex40 reports whether s is a full object name in hex (SHA-1).
func isHex40(s string) bool {
	if len(s) != 40 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if !('0' <= c && c <= '9' || 'a' <= c && c <= 'f' || 'A' <= c && c <= 'F') {
			return false
		}
	}
	return true
}

// plausibleRefName reports whether name is a fully spelled ref name of the
// ordinary kind: it starts with refs/ and breaks none of the rules of
// git-check-ref-format.
func plausibleRefName(name string) bool {
	rest, ok := strings.CutPrefix(name, "refs/")
	if !ok || rest == "" || strings.HasSuffix(name, "/") || strings.HasSuffix(name, ".") ||
		strings.Contains(name, "..") || strings.Contains(name, "//") || strings.Contains(name, "@{") {
		return false
	}
	for _, c := range []byte(name) {
		if c < 0x20 || c == 0x7f || strings.IndexByte(" ~^:?*[\\", c) >= 0 {
			return false
		}
	}
	for _, part := range strings.Split(name, "/") {
		if strings.HasPrefix(part, ".") || strings.HasSuffix(part, ".lock") {
			return false
		}
	}
	return true
}
