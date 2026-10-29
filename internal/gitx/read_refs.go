package gitx

import (
	"bufio"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

// maxPackedRefs is the size of packed-refs above which the in-process reads
// leave the file to the git program: they scan it whole for every question,
// and a file this big (some thirty thousand refs) costs about as much to scan
// as a process costs to start.
const maxPackedRefs = 2 << 20

// maxSymrefDepth is how many symbolic refs git follows before it gives up
// (SYMREF_MAXDEPTH).
const maxSymrefDepth = 5

// refState is how a ref lookup ended.
type refState int

const (
	// refFound: the ref exists and resolves.
	refFound refState = iota
	// refMissing: there is no such ref, or a symbolic ref on the way points
	// at none.
	refMissing
	// refUnsure: the files say something the in-process reads do not
	// interpret as git does.
	refUnsure
)

// rawRef is one ref as its file or its packed-refs line says it: an object
// name, or the ref it points at.
type rawRef struct {
	oid    string // lower case hex; set unless the ref is symbolic
	target string // set for a symbolic ref
}

// perWorktreeRef reports whether name is kept in a worktree's own git
// directory rather than the common one.
func perWorktreeRef(name string) bool {
	return !strings.HasPrefix(name, "refs/") ||
		strings.HasPrefix(name, "refs/worktree/") ||
		strings.HasPrefix(name, "refs/bisect/") ||
		strings.HasPrefix(name, "refs/rewritten/")
}

// refFile is where the loose file of the ref called name is.
func (l *repoLoc) refFile(name string) string {
	dir := l.commonDir
	if perWorktreeRef(name) {
		dir = l.gitDir
	}
	return filepath.Join(dir, filepath.FromSlash(name))
}

// readLoose reads the loose file of ref name. A missing file, and a
// directory where the file would be (refs/heads/a when refs/heads/a/b
// exists), are refMissing: git looks in packed-refs next.
func (l *repoLoc) readLoose(name string) (rawRef, refState) {
	path := l.refFile(name)
	fi, err := os.Stat(path)
	if err != nil {
		if isAbsent(err) {
			return rawRef{}, refMissing
		}
		return rawRef{}, refUnsure
	}
	if fi.IsDir() {
		return rawRef{}, refMissing
	}
	if !fi.Mode().IsRegular() || fi.Size() > 4096 {
		return rawRef{}, refUnsure
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if isAbsent(err) || errors.Is(err, syscall.EISDIR) {
			return rawRef{}, refMissing
		}
		return rawRef{}, refUnsure
	}
	return parseRefContent(string(data))
}

// parseRefContent interprets a loose ref file the way git does: a symbolic
// ref "ref: <name>", or an object name followed by the end or white space.
func parseRefContent(content string) (rawRef, refState) {
	content = strings.TrimRight(content, " \t\r\n")
	if target, ok := strings.CutPrefix(content, "ref:"); ok {
		target = strings.TrimLeft(target, " \t\r\n")
		if !plausibleRefName(target) {
			return rawRef{}, refUnsure
		}
		return rawRef{target: target}, refFound
	}
	if len(content) < 40 || !isHex40(content[:40]) || (len(content) > 40 && !isSpace(content[40])) {
		return rawRef{}, refUnsure
	}
	return rawRef{oid: strings.ToLower(content[:40])}, refFound
}

// isSpace is git's isspace: the vertical tab and form feed are not white
// space to it.
func isSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r'
}

// packedScan calls visit with each ref of packed-refs, in file order, until
// visit returns true. It reports refUnsure for a file it should not scan.
// Peeled lines (^<oid>) and the header are skipped. Only the common
// directory has packed-refs.
func (l *repoLoc) packedScan(visit func(name, oid string) (stop bool)) refState {
	f, err := os.Open(filepath.Join(l.commonDir, "packed-refs"))
	if err != nil {
		if isAbsent(err) {
			return refMissing
		}
		return refUnsure
	}
	defer f.Close()
	if fi, err := f.Stat(); err != nil || fi.Size() > maxPackedRefs {
		return refUnsure
	}
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if line == "" || line[0] == '#' || line[0] == '^' {
			continue
		}
		oid, name, ok := strings.Cut(line, " ")
		if !ok || !isHex40(oid) {
			return refUnsure
		}
		if visit(strings.TrimRight(name, "\r"), strings.ToLower(oid)) {
			return refFound
		}
	}
	if sc.Err() != nil {
		return refUnsure
	}
	return refMissing
}

// packedRef finds ref name in packed-refs.
func (l *repoLoc) packedRef(name string) (rawRef, refState) {
	var found rawRef
	switch st := l.packedScan(func(n, oid string) bool {
		if n == name {
			found = rawRef{oid: oid}
			return true
		}
		return false
	}); st {
	case refFound:
		return found, refFound
	default:
		return rawRef{}, st
	}
}

// readRef reads ref name once: the loose file first, then packed-refs,
// which per-worktree refs never use.
func (l *repoLoc) readRef(name string) (rawRef, refState) {
	if name == "HEAD" {
		// discovery read HEAD already.
		return parseRefContent(l.head)
	}
	raw, st := l.readLoose(name)
	if st != refMissing || perWorktreeRef(name) {
		return raw, st
	}
	return l.packedRef(name)
}

// resolveRef follows name through its symbolic refs to an object name.
func (l *repoLoc) resolveRef(name string) (string, refState) {
	for depth := 0; depth <= maxSymrefDepth; depth++ {
		raw, st := l.readRef(name)
		if st != refFound {
			return "", st
		}
		if raw.target == "" {
			return raw.oid, refFound
		}
		name = raw.target
	}
	return "", refUnsure
}

// otherRuleRefs are the places git looks for a name after the name itself
// (ref_rev_parse_rules); %s is the name.
var otherRuleRefs = []string{"refs/%s", "refs/tags/%s", "refs/heads/%s", "refs/remotes/%s", "refs/remotes/%s/HEAD"}

// othersClear reports whether none of the other places git would try for
// name (its short-name rules) holds a ref. Only then is a name that is no
// ref itself known to name nothing at all.
func (l *repoLoc) othersClear(name string) bool {
	var packed []string
	for _, rule := range otherRuleRefs {
		other := strings.Replace(rule, "%s", name, 1)
		if _, err := os.Lstat(l.refFile(other)); err == nil || !isAbsent(err) {
			return false
		}
		packed = append(packed, other)
	}
	clear := true
	if st := l.packedScan(func(n, _ string) bool {
		for _, p := range packed {
			if n == p {
				clear = false
				return true
			}
		}
		return false
	}); st == refUnsure {
		return false
	}
	return clear
}

// replaceRefs reports whether any ref under refs/replace exists, loose or
// packed, and whether that could be told at all (false, false is "no").
func (l *repoLoc) replaceRefs() (present, known bool) {
	dir := filepath.Join(l.commonDir, "refs", "replace")
	ents, err := os.ReadDir(dir)
	switch {
	case err == nil:
		if len(ents) > 0 {
			return true, true
		}
	case !isAbsent(err):
		return false, false
	}
	st := l.packedScan(func(n, _ string) bool { return strings.HasPrefix(n, "refs/replace/") })
	switch st {
	case refFound:
		return true, true
	case refMissing:
		return false, true
	}
	return false, false
}

// hasRefUnder reports whether any ref is named under name + "/", loose or
// packed: a for-each-ref of name lists those too.
func (l *repoLoc) hasRefUnder(name string) (has, known bool) {
	if fi, err := os.Stat(l.refFile(name)); err == nil && fi.IsDir() {
		return true, true
	} else if err != nil && !isAbsent(err) {
		return false, false
	}
	st := l.packedScan(func(n, _ string) bool { return strings.HasPrefix(n, name+"/") })
	switch st {
	case refFound:
		return true, true
	case refMissing:
		return false, true
	}
	return false, false
}
