package gitx

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/go-git/go-git/v5/plumbing"
)

// The read-only questions jig asks about a repository's refs, HEAD and
// objects (what HEAD is, whether a ref or a commit exists, what a path holds
// in a commit, the top of a work tree) run no hook and no filter, and answer
// from files git's own commands only read. gitx answers them in process,
// exactly as the git program would, when the repository is the plain case,
// and runs the git program for everything it cannot answer exactly (see
// discover for the repository, parseRev for the revisions, openObjects for
// the objects). Writes, history walks (merge-base, rev-list, diff, log
// ranges, status) and anything that depends on the machine's config (remote
// URLs, identity) stay on the git program. ADR 0011 keeps users' repositories
// on it for writes, hooks and filters; this does not change that.
//
// Each question is a pair: an in-process function that answers or declines,
// and the exported one that asks the git program when it declines. A test
// compares the first with the git program directly.

// result is how an in-process revision lookup ended.
type result int

const (
	// resFound: the revision names this object, and the answer is final.
	resFound result = iota
	// resNoRef: no ref has the name, nor any other place git would look
	// for it. Final: refs are read exactly, with no retry git would make.
	resNoRef
	// resNoEntry: the revision names an object, and the path asked for
	// is not in its tree. Final: the trees were read.
	resNoEntry
	// resNoObject: an object the revision needs is not there, or is not of
	// the kind asked for. The git program confirms before this is returned.
	resNoObject
	// resDecline: the in-process reads cannot say.
	resDecline
)

// revSpec is a revision in one of the forms the in-process reads accept: a
// base (HEAD, a fully spelled ref under refs/, or a 40 digit object name)
// with at most one suffix.
type revSpec struct {
	base   string
	isHex  bool
	suffix string // "", "^{commit}", "^{tree}", "^", or ":" for a path
	path   string // for the ":" suffix
}

// parseRev splits rev, or reports false for any other form (a short name,
// an abbreviated object name, @{u}, ~, a range, a longer chain of suffixes),
// which the git program resolves.
func parseRev(rev string) (revSpec, bool) {
	var s revSpec
	base := rev
	switch {
	case strings.HasSuffix(rev, "^{commit}"):
		base, s.suffix = strings.TrimSuffix(rev, "^{commit}"), "^{commit}"
	case strings.HasSuffix(rev, "^{tree}"):
		base, s.suffix = strings.TrimSuffix(rev, "^{tree}"), "^{tree}"
	case strings.HasSuffix(rev, "^1"):
		base, s.suffix = strings.TrimSuffix(rev, "^1"), "^"
	case strings.HasSuffix(rev, "^"):
		base, s.suffix = strings.TrimSuffix(rev, "^"), "^"
	case strings.Contains(rev, ":"):
		var path string
		base, path, _ = strings.Cut(rev, ":")
		if !cleanTreePath(path) {
			return s, false
		}
		s.suffix, s.path = ":", path
	}
	switch {
	case isHex40(base):
		s.isHex, s.base = true, strings.ToLower(base)
	case base == "HEAD", plausibleRefName(base):
		s.base = base
	default:
		return s, false
	}
	return s, true
}

// reader answers questions about one repository in process. A nil *reader
// (no plain repository found) declines everything.
type reader struct {
	loc    *repoLoc
	db     *objectDB
	dbDone bool
}

// newReader discovers the repository dir is in, or returns nil.
func newReader(dir string, env envLookup) *reader {
	loc, ok := discover(dir, env)
	if !ok {
		return nil
	}
	return &reader{loc: loc}
}

func (r *reader) close() {
	if r != nil {
		r.db.close()
	}
}

// objects opens the object store on first use.
func (r *reader) objects() (*objectDB, bool) {
	if r == nil {
		return nil, false
	}
	if !r.dbDone {
		r.dbDone = true
		r.db, _ = r.loc.openObjects()
	}
	return r.db, r.db != nil
}

// rev resolves spec.
func (r *reader) rev(spec revSpec) (string, result) {
	if r == nil {
		return "", resDecline
	}
	oid := spec.base
	if !spec.isHex {
		var st refState
		oid, st = r.loc.resolveRef(spec.base)
		switch st {
		case refUnsure:
			return "", resDecline
		case refMissing:
			if r.loc.othersClear(spec.base) {
				return "", resNoRef
			}
			return "", resDecline
		}
	}
	if spec.suffix == "" {
		return oid, resFound
	}
	db, ok := r.objects()
	if !ok {
		return "", resDecline
	}
	var (
		out string
		st  objState
	)
	switch spec.suffix {
	case "^{commit}":
		out, st = db.peelTo(oid, "commit")
	case "^{tree}":
		out, st = db.peelTo(oid, "tree")
	case "^":
		out, st = db.parentOf(oid)
	case ":":
		var tree string
		if tree, st = db.peelTo(oid, "tree"); st == objFound {
			e, est := db.entryAt(tree, spec.path)
			if est == objFound && e.mode == 0o160000 {
				return "", resDecline // a submodule's commit is another repository's
			}
			out, st = e.oid, est
		}
	}
	return out, objResult(st)
}

func objResult(st objState) result {
	switch st {
	case objFound:
		return resFound
	case objNoEntry:
		return resNoEntry
	case objAbsent:
		return resNoObject
	}
	return resDecline
}

// answerRev resolves rev in dir, or declines.
func answerRev(dir, rev string, env envLookup) (string, result) {
	spec, ok := parseRev(rev)
	if !ok {
		return "", resDecline
	}
	r := newReader(dir, env)
	defer r.close()
	return r.rev(spec)
}

// RevParse resolves rev to a full object name in dir, as "git rev-parse
// rev" prints it. HEAD, a fully spelled ref under refs/ and a full object
// name, each with at most one of ^{commit}, ^{tree}, ^ or :path after it,
// are resolved in process; any other form, and any revision that does not
// resolve (so that git's message is the caller's error), is left to the git
// program.
func RevParse(dir, rev string) (string, error) {
	if sha, res := answerRev(dir, rev, os.LookupEnv); res == resFound {
		return sha, nil
	}
	return Run(dir, "rev-parse", rev)
}

// Verify is "git rev-parse --quiet --verify rev" for a caller that hands the
// error on: the object name rev names, or git's own error, which is what a
// revision that resolves to nothing gets (so only a resolved revision is
// answered in process).
func Verify(dir, rev string) (string, error) {
	if sha, res := answerRev(dir, rev, os.LookupEnv); res == resFound {
		return sha, nil
	}
	return Run(dir, "rev-parse", "--quiet", "--verify", rev)
}

// Resolve resolves rev the way "git rev-parse --verify --quiet rev" does:
// the object name and true, or "" and false when rev names nothing. An error
// is a failure of git itself (no repository, say), which the callers that
// asked with Run treated as the same "not there" unless they said otherwise.
// Forms are answered in process as RevParse's are; a revision it finds no ref
// for is final, and one whose object is missing is confirmed by git first.
func Resolve(dir, rev string) (string, bool, error) {
	return resolve(dir, rev, false)
}

// ResolveCommit is Resolve of rev^{commit}: the commit rev names (a tag is
// followed), if there is one.
func ResolveCommit(dir, rev string) (string, bool, error) {
	return resolve(dir, rev+"^{commit}", false)
}

// ResolveCommitTrusted is ResolveCommit for a repository whose owner is
// trusted, as pool.Usable's probe does (git is run with safe.directory=*).
func ResolveCommitTrusted(dir, rev string) (string, bool, error) {
	return resolve(dir, rev+"^{commit}", true)
}

// resolve is Resolve, and whether the git program is run trusting the
// repository's owner.
func resolve(dir, rev string, trusted bool) (string, bool, error) {
	r := newReader(dir, os.LookupEnv)
	defer r.close()
	return resolveWith(dir, rev, r, trusted)
}

// resolveWith is resolve with r, a reader to reuse across questions about
// one repository (nil when discovery found none).
func resolveWith(dir, rev string, r *reader, trusted bool) (string, bool, error) {
	if spec, ok := parseRev(rev); ok {
		switch sha, res := r.rev(spec); res {
		case resFound:
			return sha, true, nil
		case resNoRef, resNoEntry:
			return "", false, nil
		}
	}
	args := []string{"rev-parse", "--verify", "--quiet", rev}
	if trusted {
		args = append([]string{"-c", "safe.directory=*"}, args...)
	}
	out, err := Run(dir, args...)
	if err == nil {
		return out, true, nil
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
		return "", false, nil
	}
	return "", false, err
}

// HasCommit reports whether rev names a commit present in dir, as "git
// cat-file -e rev^{commit}" does.
func HasCommit(dir, rev string) bool {
	_, ok, _ := ResolveCommit(dir, rev)
	return ok
}

// hasPathAt is HasPathAt without the git program: ok is false when it cannot
// say, or when the answer is no, which git confirms.
func hasPathAt(dir, rev, path string, env envLookup) (ok bool) {
	spec, parsed := parseRev(rev + ":" + path)
	if !parsed || spec.suffix != ":" {
		return false
	}
	r := newReader(dir, env)
	defer r.close()
	sha, res := r.rev(spec)
	if res != resFound {
		return false
	}
	db, haveDB := r.objects()
	if !haveDB {
		return false
	}
	has, st := db.has(sha)
	return has && st == objFound
}

// HasPathAt reports whether path exists, as an object that is present, in
// the tree of the commit rev names in dir, as "git cat-file -e rev:path"
// does. A path in the plain form (slash separated, no empty, . or .. part) is
// looked up in process; anything the in-process reads cannot answer, and an
// answer of no (confirmed by git first), goes to the git program.
func HasPathAt(dir, rev, path string) bool {
	if hasPathAt(dir, rev, path, os.LookupEnv) {
		return true
	}
	_, err := Run(dir, "cat-file", "-e", rev+":"+path)
	return err == nil
}

// headBranchInProcess is HeadBranch without the git program: ok is false
// when it cannot say.
func headBranchInProcess(dir string, env envLookup) (branch string, hasBranch, ok bool) {
	r := newReader(dir, env)
	if r == nil {
		return "", false, false
	}
	defer r.close()
	raw, st := parseRefContent(r.loc.head)
	switch {
	case st != refFound:
		return "", false, false
	case raw.target == "":
		return "", false, true // detached
	}
	branch, isBranch := strings.CutPrefix(raw.target, "refs/heads/")
	if !isBranch || !r.loc.unambiguousShort(branch) {
		return "", false, false
	}
	return branch, true, true
}

// HeadBranch returns the branch HEAD points at in dir, as "git symbolic-ref
// --short -q HEAD" prints it, and false when HEAD points at none (a detached
// HEAD) or git cannot say. A branch whose short name another ref also has
// (git then spells it heads/<name>) and a HEAD that points outside
// refs/heads are left to the git program.
func HeadBranch(dir string) (string, bool) {
	if branch, has, ok := headBranchInProcess(dir, os.LookupEnv); ok {
		return branch, has
	}
	out, err := Run(dir, "symbolic-ref", "--short", "-q", "HEAD")
	if err != nil {
		return "", false
	}
	return out, true
}

// unambiguousShort reports whether the short name of refs/heads/<branch> is
// <branch>: no ref is called <branch>, refs/<branch> or refs/tags/<branch>,
// which git's shortening would then have to spell around.
func (l *repoLoc) unambiguousShort(branch string) bool {
	names := []string{branch, "refs/" + branch, "refs/tags/" + branch}
	for _, n := range names {
		if _, err := os.Lstat(l.refFile(n)); err == nil || !isAbsent(err) {
			return false
		}
	}
	clear := true
	if st := l.packedScan(func(n, _ string) bool {
		if n == names[1] || n == names[2] {
			clear = false
			return true
		}
		return false
	}); st == refUnsure {
		return false
	}
	return clear
}

// refTipInProcess is RefTip without the git program.
func refTipInProcess(dir, ref string, env envLookup) (tip string, ok bool) {
	if !plausibleRefName(ref) {
		return "", false
	}
	r := newReader(dir, env)
	if r == nil {
		return "", false
	}
	defer r.close()
	switch oid, st := r.loc.resolveRef(ref); st {
	case refFound:
		return oid, true
	case refMissing:
		if under, known := r.loc.hasRefUnder(ref); known && !under {
			return "", true
		}
	}
	return "", false
}

// RefTip returns the object name the ref called ref has in dir, as "git
// for-each-ref --format=%(objectname) ref" prints it, and "" when there is
// no such ref. ref is a fully spelled name under refs/. A ref with refs
// beneath it (for-each-ref lists those too), a ref file that is not a ref
// and a repository the in-process reads decline are left to the git program.
// The object is not looked for: for-each-ref lists a ref whose object is
// missing (TestInProcessReadsMatchGit holds git to that).
func RefTip(dir, ref string) (string, error) {
	if tip, ok := refTipInProcess(dir, ref, os.LookupEnv); ok {
		return tip, nil
	}
	return Run(dir, "for-each-ref", "--format=%(objectname)", ref)
}

// subjectInProcess is Subject without the git program.
func subjectInProcess(dir, rev string, env envLookup) (string, bool) {
	spec, parsed := parseRev(rev)
	if !parsed || spec.suffix != "" {
		return "", false
	}
	r := newReader(dir, env)
	defer r.close()
	sha, res := r.rev(spec)
	if res != resFound {
		return "", false
	}
	db, haveDB := r.objects()
	if !haveDB {
		return "", false
	}
	o, st := db.load(sha)
	if st != objFound || o.kind != plumbing.CommitObject {
		return "", false
	}
	subject, st := subjectOf(o.body)
	if st != objFound {
		return "", false
	}
	return strings.TrimSpace(subject), true
}

// Subject returns the first line of the message of the commit rev names in
// dir, as "git log -1 --format=%s rev" prints it. A commit that names a
// message encoding, a subject that is not ASCII and a rev that is not a
// commit by one of the forms RevParse resolves in process are left to the
// git program.
func Subject(dir, rev string) (string, error) {
	if subject, ok := subjectInProcess(dir, rev, os.LookupEnv); ok {
		return subject, nil
	}
	return Run(dir, "log", "-1", "--format=%s", rev)
}

// hasUnmergedInProcess is HasUnmerged without the git program.
func hasUnmergedInProcess(dir string, env envLookup) (unmerged, ok bool) {
	r := newReader(dir, env)
	if r == nil || r.loc.prefix != "" {
		return false, false
	}
	return r.loc.hasUnmerged()
}

// HasUnmerged reports whether dir's index has unmerged (conflicted)
// entries, as "git ls-files --unmerged" printing anything. dir must be a
// work tree's top level for it to be answered in process.
func HasUnmerged(dir string) (bool, error) {
	if unmerged, ok := hasUnmergedInProcess(dir, os.LookupEnv); ok {
		return unmerged, nil
	}
	out, err := Run(dir, "ls-files", "--unmerged")
	if err != nil {
		return false, err
	}
	return strings.TrimSpace(out) != "", nil
}

// perWorktreeNames are git-path names kept in a worktree's own git
// directory, and commonNames those kept in the common one, for the
// GitPaths names answered in process.
var (
	perWorktreeNames = []string{"HEAD", "index", "rebase-merge", "rebase-apply", "MERGE_HEAD", "CHERRY_PICK_HEAD", "REVERT_HEAD", "sequencer", "BISECT_LOG"}
	commonNames      = []string{"config", "packed-refs", "hooks"}
)

// gitPathBase is the directory git keeps the file called name in: the
// worktree's git directory or the common one.
func (l *repoLoc) gitPathBase(name string) (string, bool) {
	first, rest, _ := strings.Cut(name, "/")
	for _, n := range perWorktreeNames {
		if first == n {
			return l.gitDir, true
		}
	}
	for _, n := range commonNames {
		if first == n {
			return l.commonDir, true
		}
	}
	if first == "info" && rest != "" && rest != "sparse-checkout" && !strings.HasPrefix(rest, "sparse-checkout/") {
		return l.commonDir, true
	}
	return "", false
}

// gitPathsInProcess is GitPaths without the git program.
func gitPathsInProcess(dir string, names []string, env envLookup) ([]string, bool) {
	r := newReader(dir, env)
	if r == nil {
		return nil, false
	}
	paths := make([]string, 0, len(names))
	for _, name := range names {
		base, ok := r.loc.gitPathBase(name)
		if !ok || !cleanTreePath(name) {
			return nil, false
		}
		paths = append(paths, filepath.Join(base, filepath.FromSlash(name)))
	}
	return paths, true
}

// GitPaths returns, for each name, the absolute path of that file in dir's
// repository, as "git rev-parse --git-path name" locates it (a worktree's
// own files, such as rebase-merge or MERGE_HEAD, in its own git directory
// and info/exclude in the common one). The marker files of an operation in
// progress and the names info/exclude, config, packed-refs and hooks are
// located in process; other names go to the git program.
func GitPaths(dir string, names ...string) ([]string, error) {
	if paths, ok := gitPathsInProcess(dir, names, os.LookupEnv); ok {
		return paths, nil
	}
	args := []string{"rev-parse"}
	for _, name := range names {
		args = append(args, "--git-path", name)
	}
	out, err := Run(dir, args...)
	if err != nil {
		return nil, err
	}
	lines := strings.Split(strings.TrimSpace(strings.ReplaceAll(out, "\r\n", "\n")), "\n")
	if len(lines) != len(names) {
		return nil, fmt.Errorf("gitx: git printed %d paths for %d names", len(lines), len(names))
	}
	for i, line := range lines {
		line = strings.TrimSpace(line)
		if !filepath.IsAbs(line) {
			line = filepath.Join(dir, line)
		}
		lines[i] = line
	}
	return lines, nil
}

// workTreeProbeInProcess is WorkTreeProbe without the git program.
func workTreeProbeInProcess(dir string, env envLookup) (string, bool) {
	loc, ok := discover(dir, env)
	if !ok {
		return "", false
	}
	if loc.prefix == "" {
		return "true", true
	}
	return "true\n" + loc.prefix, true
}

// WorkTreeProbe returns what "git -c safe.directory=* rev-parse
// --is-inside-work-tree --show-prefix" prints in dir, with the owner of the
// repository trusted: "true" at the top level of a work tree, "true" and the
// path below the top (with a trailing slash) on a following line inside one.
// Anything else (a bare repository, the inside of a git directory, no
// repository) is the git program's answer or error.
func WorkTreeProbe(dir string) (string, error) {
	if out, ok := workTreeProbeInProcess(dir, os.LookupEnv); ok {
		return out, nil
	}
	return Run(dir, "-c", "safe.directory=*", "rev-parse", "--is-inside-work-tree", "--show-prefix")
}
