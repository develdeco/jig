package store

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"sync"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/gitx"
)

// Writers mode lets several clones of one store write at once without the
// pushes of one rejecting the pushes of the others. Each clone pushes only to
// a branch of its own, writers/<name>, never to main; and reads the others by
// fetching every branch and merging the other writers branches into its own,
// with the union merge driver for NDJSON files (gitx.WritersAttributes), so
// the journals two writers both appended to merge without a conflict. What no
// two writers may do at once, mint one id or drive one ticket, goes through
// the claims log on its own branch (claims.go).
//
// A store is in writers mode when it has a claims branch, locally or on the
// origin (Mode); any other store stays in single-main mode, which is what this
// package did before, byte for byte. `jig store migrate --writers` converts a
// store (MigrateToWriters).
//
// A writers branch is read, not trusted. A machine-local ref per writer,
// refs/jig/seen/<writer>, records the tip last merged from it; a tip that does
// not descend from it was rewritten (force-pushed, reset), and is warned about
// with WRITER_REWRITTEN and left unmerged, never merged and never repaired.

const (
	// writersPrefix starts the name of every writer branch.
	writersPrefix = "writers/"
	// legacyBranch is the branch a store was on before it became a writers
	// store. Clones that have not heard of writers mode keep pushing to it,
	// so it is merged like a writer branch (as a writer named main).
	legacyBranch = "main"
	// seenPrefix is where the tips merged from each writer are kept, in the
	// clone only: the refspec jig fetches with does not carry it anywhere.
	seenPrefix = "refs/jig/seen/"
	// legacySeenKey is the seen-ref name of the legacy branch; a writer name
	// cannot start with an underscore.
	legacySeenKey = "_main"

	maxWriterNameLen = 40
)

// writerCache holds the writer name Store.writerName resolved.
type writerCache struct {
	mu   sync.Mutex
	name string
	ok   bool
}

// Mode reports whether the store is in writers mode: it has a claims branch,
// in the clone or as the origin was last fetched. The answer costs a few file
// reads, no git process, so it can be asked on every Sync, Push and Claim. A
// clone that has not fetched since the store moved to writers mode does not
// know it yet: `jig store migrate --writers` there, or a fresh clone, does.
func (s *Store) Mode() (writers bool, err error) {
	for _, ref := range []string{claimsRef, claimsRemoteRef} {
		ok, err := gitx.RefExists(s.Root, ref)
		if err != nil {
			return false, err
		}
		if ok {
			return true, nil
		}
	}
	return false, nil
}

// Writer returns the name this clone writes under: git config jig.writer in
// the store clone, else the host name, cleaned to lowercase letters, digits
// and hyphens, forty characters at most.
func (s *Store) Writer() (string, error) { return s.writerName() }

// writerName resolves the writer name once per Store.
func (s *Store) writerName() (string, error) {
	s.writer.mu.Lock()
	defer s.writer.mu.Unlock()
	if s.writer.ok {
		return s.writer.name, nil
	}
	name, err := resolveWriterName(s.Root)
	if err != nil {
		return "", err
	}
	s.writer.name, s.writer.ok = name, true
	return name, nil
}

// resolveWriterName is writerName without the cache.
func resolveWriterName(root string) (string, error) {
	configured, err := gitx.Run(root, "config", "--get", "jig.writer")
	if err != nil {
		var exitErr *exec.ExitError
		if !errors.As(err, &exitErr) || exitErr.ExitCode() != 1 {
			return "", err
		}
		configured = "" // not set
	}
	if configured != "" {
		name := sanitizeWriter(configured)
		if name == "" {
			return "", &axi.Error{
				Msg:  fmt.Sprintf("jig.writer is %q, which has no letter or digit to make a writer name of", configured),
				Code: "VALIDATION_ERROR",
				Help: []string{fmt.Sprintf("Set it to a name of lowercase letters, digits and hyphens: `git config jig.writer <name>` in the store at %s", root)},
			}
		}
		return name, nil
	}
	host, err := os.Hostname()
	if err != nil {
		return "", fmt.Errorf("store: writer name: %w", err)
	}
	if name := sanitizeWriter(host); name != "" {
		return name, nil
	}
	return "writer", nil
}

// sanitizeWriter makes raw into a writer name: lowercase letters, digits and
// single hyphens, no hyphen at either end, forty characters at most. It is ""
// when raw has no letter or digit.
func sanitizeWriter(raw string) string {
	var b strings.Builder
	dash := true // swallows a leading hyphen
	for _, r := range strings.ToLower(raw) {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') {
			b.WriteRune(r)
			dash = false
		} else if !dash {
			b.WriteByte('-')
			dash = true
		}
	}
	name := strings.TrimRight(b.String(), "-")
	if len(name) > maxWriterNameLen {
		name = strings.TrimRight(name[:maxWriterNameLen], "-")
	}
	return name
}

// enterWriters turns cp, as begin made it, into the checkpoint of a writers
// store: the branch it works on is the writer own, which it checks out when
// the clone is on another (a fresh clone is on main).
func (s *Store) enterWriters(cp *checkpoint) (*checkpoint, error) {
	me, err := s.writerName()
	if err != nil {
		return nil, err
	}
	target := writersPrefix + me
	if cp.branch != target {
		if err := s.switchToWriterBranch(target); err != nil {
			return nil, err
		}
		cp.branch = target
	}
	cp.writers = true
	return cp, nil
}

// switchToWriterBranch checks out target (writers/<me>): the clone branch of
// that name if it has one, else one made from the origin branch of that name
// (a second clone of the same writer), else one made from where the clone is
// now (main, in a clone made after the store moved to writers mode).
func (s *Store) switchToWriterBranch(target string) error {
	local, err := gitx.RefExists(s.Root, "refs/heads/"+target)
	if err != nil {
		return err
	}
	if local {
		_, err = gitx.Run(s.Root, "checkout", "--quiet", target)
		return err
	}
	remote, err := gitx.RefExists(s.Root, "refs/remotes/origin/"+target)
	if err != nil {
		return err
	}
	if remote {
		_, err = gitx.Run(s.Root, "checkout", "--quiet", "-b", target, "refs/remotes/origin/"+target)
		return err
	}
	_, err = gitx.Run(s.Root, "checkout", "--quiet", "-b", target)
	return err
}

// writerRef is a branch of another writer on the origin, as fetched.
type writerRef struct {
	ref   string // refs/remotes/origin/writers/bob
	label string // writers/bob, as a person knows it
	key   string // the name of its seen ref under seenPrefix
}

// pullWriters is pull for a writers store: it fetches every branch of the
// origin, refuses a branch of this writer name that someone else moved, and
// merges in every other writer branch and the legacy main, each one only if it
// is the same history it was last time (refs/jig/seen) and brings something.
func (cp *checkpoint) pullWriters() error {
	s := cp.s
	defer s.invalidateAliasClaims()
	if _, err := gitx.Run(s.Root, "fetch", "--quiet", "origin", "+refs/heads/*:refs/remotes/origin/*"); err != nil {
		return err
	}
	if err := s.followClaims(); err != nil {
		return err
	}
	tips, err := s.refTips("refs/heads/"+cp.branch, "refs/remotes/origin/"+writersPrefix, "refs/remotes/origin/"+legacyBranch, seenPrefix)
	if err != nil {
		return err
	}
	if err := cp.joinOwnBranch(tips); err != nil {
		return err
	}

	var others []writerRef
	if _, ok := tips["refs/remotes/origin/"+legacyBranch]; ok {
		others = append(others, writerRef{ref: "refs/remotes/origin/" + legacyBranch, label: legacyBranch, key: legacySeenKey})
	}
	var names []string
	for ref := range tips {
		if name, ok := strings.CutPrefix(ref, "refs/remotes/origin/"); ok && strings.HasPrefix(name, writersPrefix) && name != cp.branch {
			names = append(names, name)
		}
	}
	sort.Strings(names)
	for _, name := range names {
		others = append(others, writerRef{
			ref:   "refs/remotes/origin/" + name,
			label: name,
			key:   strings.ReplaceAll(strings.TrimPrefix(name, writersPrefix), "/", "+"),
		})
	}

	for _, w := range others {
		tip, seen := tips[w.ref], tips[seenPrefix+w.key]
		if tip == seen {
			continue
		}
		if seen != "" {
			descends, err := gitx.IsAncestor(s.Root, seen, tip)
			if err != nil {
				return err
			}
			if !descends {
				s.warn("WRITER_REWRITTEN: %s was rewritten on the origin: its tip %s does not descend from %s, the one merged here last, so it is not merged and nothing repairs it. If the rewrite is meant, accept it with `git update-ref -d %s%s` in the store at %s, then rerun",
					w.label, shortSHA(tip), shortSHA(seen), seenPrefix, w.key, s.Root)
				continue
			}
		}
		merged, err := gitx.IsAncestor(s.Root, tip, "HEAD")
		if err != nil {
			return err
		}
		if !merged {
			if err := cp.mergeIn(w, tip); err != nil {
				return err
			}
		}
		if _, err := gitx.Run(s.Root, "update-ref", seenPrefix+w.key, tip); err != nil {
			return err
		}
	}
	return nil
}

// refTips returns the commit of every ref under the given patterns
// (for-each-ref's: a name, or a directory with its trailing slash).
func (s *Store) refTips(patterns ...string) (map[string]string, error) {
	args := append([]string{"for-each-ref", "--format=%(objectname) %(refname)"}, patterns...)
	out, err := gitx.Run(s.Root, args...)
	if err != nil {
		return nil, err
	}
	tips := map[string]string{}
	for _, line := range strings.Split(out, "\n") {
		if sha, ref, ok := strings.Cut(strings.TrimSpace(line), " "); ok {
			tips[ref] = sha
		}
	}
	return tips, nil
}

// joinOwnBranch checks that the origin branch of this writer name is a
// history this clone already has. When it is ahead (another clone of the same
// writer pushed more), this clone takes it forward; when the two have
// diverged, someone else has been writing under this name, and it is refused
// with WRITER_NAME_TAKEN before anything is merged or pushed.
func (cp *checkpoint) joinOwnBranch(tips map[string]string) error {
	s := cp.s
	own, head := tips["refs/remotes/origin/"+cp.branch], tips["refs/heads/"+cp.branch]
	if own == "" || own == head {
		return nil
	}
	ancestor, err := gitx.IsAncestor(s.Root, own, "HEAD")
	if err != nil || ancestor {
		return err
	}
	behind, err := gitx.IsAncestor(s.Root, "HEAD", own)
	if err != nil {
		return err
	}
	if behind {
		_, err = gitx.Run(s.Root, "merge", "--ff-only", "--quiet", own)
		return err
	}
	return &axi.Error{
		Msg:  fmt.Sprintf("origin/%s, the branch this clone writes to, has commits this clone does not have and this clone has commits it does not: another clone is writing under the name %s", cp.branch, strings.TrimPrefix(cp.branch, writersPrefix)),
		Code: "WRITER_NAME_TAKEN",
		Help: []string{fmt.Sprintf("Give this clone a name of its own: `git config jig.writer <name>` in the store at %s, then rerun", s.Root)},
	}
}

// mergeIn brings tip, the head of writer branch w, into HEAD: a fast-forward
// when HEAD is part of it, else a merge commit authored by the person, with
// the union merge driver in force for NDJSON files. A merge that stops on a
// conflict in any other file is aborted (STORE_CONFLICT) and the branch is
// left as it was.
func (cp *checkpoint) mergeIn(w writerRef, tip string) error {
	s := cp.s
	ff, err := gitx.IsAncestor(s.Root, "HEAD", tip)
	if err != nil {
		return err
	}
	if ff {
		_, err = gitx.Run(s.Root, "merge", "--ff-only", "--quiet", tip)
		return err
	}
	if err := s.ensureWritersAttributes(); err != nil {
		return err
	}
	id, err := s.identity()
	if err != nil {
		return err
	}
	if _, err := gitx.Run(s.Root, commitConfig(id, "merge", "--no-edit", "--quiet", "-m", "jig: merge "+w.label, tip)...); err != nil {
		return s.abortFailedMerge(err, w.label)
	}
	return s.dedupeMergedLogs(id)
}

// dedupeMergedLogs takes out of the merge commit at HEAD the NDJSON lines the
// union merge doubled. Union keeps both sides of a hunk the two sides changed,
// and when the histories of two writers have merged each other in turn, the
// same line sits in both sides' hunks, in different places: it comes out
// twice. A line is never meant to be in a journal twice more than it was in
// the side that has it most, so each such line is cut back to that count,
// keeping its first place, and the merge commit is amended (it is not pushed
// yet). Lines both sides carried the same number of times are left as they are.
func (s *Store) dedupeMergedLogs(id gitx.Identity) error {
	out, err := gitx.Run(s.Root, "diff", "--name-only", "HEAD^1", "HEAD", "--", "*.ndjson")
	if err != nil {
		return err
	}
	changed := false
	for _, path := range strings.Split(out, "\n") {
		path = strings.TrimSpace(path)
		if path == "" {
			continue
		}
		ours, err := gitx.Run(s.Root, "show", "HEAD^1:"+path)
		if err != nil {
			continue // new on the other side: the merge took it whole
		}
		theirs, err := gitx.Run(s.Root, "show", "HEAD^2:"+path)
		if err != nil {
			continue
		}
		abs := filepath.Join(s.Root, filepath.FromSlash(path))
		merged, err := os.ReadFile(abs)
		if err != nil {
			return err
		}
		cut, did := dedupeLines(string(merged), ours, theirs)
		if !did {
			continue
		}
		if err := os.WriteFile(abs, []byte(cut), 0o644); err != nil {
			return err
		}
		if _, err := gitx.Run(s.Root, "add", "--", path); err != nil {
			return err
		}
		changed = true
	}
	if !changed {
		return nil
	}
	_, err = gitx.Run(s.Root, commitConfig(id, "commit", "--amend", "--no-edit", "--quiet")...)
	return err
}

// dedupeLines cuts merged back so that no line is in it more often than it is
// in ours or theirs, whichever has it more; the first places are kept, blank
// lines are never touched, and the line endings stay as they were. did says it
// cut anything.
func dedupeLines(merged, ours, theirs string) (cut string, did bool) {
	limit := map[string]int{}
	for _, side := range []string{ours, theirs} {
		n := map[string]int{}
		for _, l := range strings.Split(side, "\n") {
			if l = strings.TrimRight(l, "\r"); l != "" {
				n[l]++
			}
		}
		for l, c := range n {
			if c > limit[l] {
				limit[l] = c
			}
		}
	}
	seen := map[string]int{}
	var b strings.Builder
	for _, raw := range strings.SplitAfter(merged, "\n") {
		l := strings.TrimRight(raw, "\r\n")
		if l != "" {
			if lim, ok := limit[l]; ok && seen[l] >= lim {
				did = true
				continue
			}
			seen[l]++
		}
		b.WriteString(raw)
	}
	return b.String(), did
}

// ensureWritersAttributes makes the working tree .gitattributes the
// WritersAttributes and commits it, when it is the StoreAttributes (or
// missing): the union merge driver has to be in the tree that merges, before
// the merge, whichever clone made the store a writers store. A .gitattributes
// that is anything else is the owner own, and is left alone.
func (s *Store) ensureWritersAttributes() error {
	path := filepath.Join(s.Root, ".gitattributes")
	data, err := os.ReadFile(path)
	switch {
	case err == nil:
		if strings.ReplaceAll(string(data), "\r\n", "\n") != gitx.StoreAttributes {
			return nil
		}
	case errors.Is(err, fs.ErrNotExist):
	default:
		return err
	}
	if err := os.WriteFile(path, []byte(gitx.WritersAttributes), 0o644); err != nil {
		return err
	}
	_, err = s.stageAndCommitPaths([]string{".gitattributes"}, "jig: merge ndjson files by union")
	return err
}

// abortFailedMerge handles a failed `git merge` of a writer branch. A merge
// that stopped on a conflict leaves itself in progress; the conflicting paths
// are read before anything touches the index, the merge is aborted, and the
// result is a STORE_CONFLICT naming them. A merge that failed before it began
// (nothing to abort) returns its own error.
func (s *Store) abortFailedMerge(mergeErr error, label string) error {
	mid, stateErr := inProgressRebaseOrMerge(s.Root)
	if stateErr == nil && mid == "" {
		return mergeErr
	}
	paths, _ := conflictedPaths(s.Root)
	_, abortErr := gitx.Run(s.Root, "merge", "--abort")
	where := "an unrecorded path"
	if len(paths) > 0 {
		where = strings.Join(paths, ", ")
	}
	if abortErr != nil {
		return &axi.Error{
			Msg:  fmt.Sprintf("the store at %s: merging %s conflicted in %s, and the best-effort `git merge --abort` also failed: %v", s.Root, label, where, abortErr),
			Code: "STORE_CONFLICT",
			Help: []string{fmt.Sprintf("The store at %s is still mid-merge: resolve it there with `git status`, then `git merge --abort` or commit the resolution.", s.Root)},
		}
	}
	return &axi.Error{
		Msg:  fmt.Sprintf("the store at %s: merging %s conflicted in %s and was aborted by jig; the store is back where it was", s.Root, label, where),
		Code: "STORE_CONFLICT",
		Help: []string{fmt.Sprintf("In the store at %s: `git merge origin/%s`, fix the conflict in %s, `git add` it, commit, then rerun this command.", s.Root, label, where)},
	}
}

// shortSHA is the first seven characters of a commit id.
func shortSHA(sha string) string {
	if len(sha) > 7 {
		return sha[:7]
	}
	return sha
}

// WritersMigration is what MigrateToWriters did.
type WritersMigration struct {
	// Writer is the name the clone writes under; Branch its branch.
	Writer string
	Branch string
	// Joined says the store already had a claims branch (another clone moved
	// it to writers mode first), so this clone only joined; false means this
	// clone made the claims branch.
	Joined bool
	// IDs is how many id lines the new claims branch starts with (one per
	// ticket id and alias the store has), Carried how many driver and release
	// lines of the old root claims.ndjson came with them.
	IDs     int
	Carried int
}

// WritersPlan is what MigrateToWriters would do, read without changing
// anything.
type WritersPlan struct {
	// Writer and Branch are the name and the branch the clone would write
	// under.
	Writer string
	Branch string
	// HasClaims says the store already has a claims branch (it is in writers
	// mode, or another clone moved it): the migration then only joins.
	HasClaims bool
	// IDs is how many id lines a claims branch made now would start with.
	IDs int
	// Carried is how many lines of an old root claims.ndjson would go in
	// front of them.
	Carried int
}

// PlanWriters reads what MigrateToWriters would do. It fetches nothing: the
// claims branch it reports is the one the clone already knows.
func (s *Store) PlanWriters() (WritersPlan, error) {
	var p WritersPlan
	me, err := s.writerName()
	if err != nil {
		return p, err
	}
	p.Writer, p.Branch = me, writersPrefix+me
	if p.HasClaims, err = s.Mode(); err != nil {
		return p, err
	}
	if p.HasClaims {
		return p, nil
	}
	ids, err := s.existingIDs()
	if err != nil {
		return p, err
	}
	p.IDs = len(ids)
	if old, err := os.ReadFile(filepath.Join(s.Root, claimsFile)); err == nil {
		p.Carried = len(parseClaims(string(old)))
	}
	return p, nil
}

// MigrateToWriters moves the store to writers mode, or joins this clone to a
// store another clone already moved:
//
//   - the claims branch is made, if the origin has none: a root commit holding
//     claims.ndjson with an id line for every ticket id and alias the store has
//     (the old root claims.ndjson of a single-main store, its driver lines, goes
//     first), and pushed;
//   - the clone checks out writers/<name>, made from where it is;
//   - the .gitattributes gains the union merge driver, committed there;
//   - writers/<name> is pushed.
//
// main is never pushed: it stays as it is on the origin, and is merged into
// every writer as the legacy history. The caller has checked that the store is
// clean and level with the origin; a store with no origin is refused, since
// writers coordinate through it. Running it again on a store that is already
// converted does nothing it has not done.
func (s *Store) MigrateToWriters() (WritersMigration, error) {
	var res WritersMigration
	release, err := s.LockCheckpoint()
	if err != nil {
		return res, err
	}
	defer release()
	if err := s.refuseIfMidRebaseOrMerge(); err != nil {
		return res, err
	}
	if _, err := s.currentBranch(); err != nil {
		return res, err
	}
	if !s.HasRemote() {
		return res, &axi.Error{
			Msg:  fmt.Sprintf("the store at %s has no origin, and writers coordinate through it", s.Root),
			Code: "VALIDATION_ERROR",
			Help: []string{"Give the store a remote first (`jig share`), then run `jig store migrate --writers`"},
		}
	}
	me, err := s.writerName()
	if err != nil {
		return res, err
	}
	res.Writer, res.Branch = me, writersPrefix+me
	ident, err := s.identity()
	if err != nil {
		return res, err
	}
	if _, err := gitx.Run(s.Root, "fetch", "--quiet", "origin", "+refs/heads/*:refs/remotes/origin/*"); err != nil {
		return res, err
	}

	remoteClaims, err := gitx.RefExists(s.Root, claimsRemoteRef)
	if err != nil {
		return res, err
	}
	if !remoteClaims {
		made, ids, carried, err := s.createClaimsBranch(ident)
		if err != nil {
			return res, err
		}
		res.IDs, res.Carried = ids, carried
		res.Joined = !made
	} else {
		res.Joined = true
	}
	if res.Joined {
		if err := s.followClaims(); err != nil {
			return res, err
		}
	}

	if err := s.switchToWriterBranch(res.Branch); err != nil {
		return res, err
	}
	if err := s.ensureWritersAttributes(); err != nil {
		return res, err
	}
	if _, err := gitx.Run(s.Root, "push", "origin", res.Branch); err != nil {
		return res, err
	}
	s.invalidateAliasClaims()
	return res, nil
}

// createClaimsBranch makes and pushes the claims branch of a store that has
// none on the origin. made is false when the push lost a race to another clone
// that made it first: the branch is then theirs, fetched, and this clone joins.
func (s *Store) createClaimsBranch(ident gitx.Identity) (made bool, ids, carried int, err error) {
	if ok, err := gitx.RefExists(s.Root, claimsRef); err != nil {
		return false, 0, 0, err
	} else if ok {
		// A claims branch of this clone the origin never took (an earlier run
		// died before its push): push it as it is.
		if out, perr := gitx.RunRaw(s.Root, "push", "origin", claimsRef+":"+claimsRef); perr != nil {
			if claimsPushRaced(out) {
				return false, 0, 0, s.fetchClaims()
			}
			return false, 0, 0, perr
		}
		return true, 0, 0, nil
	}

	var lines []ClaimLine
	if old, err := os.ReadFile(filepath.Join(s.Root, claimsFile)); err == nil {
		lines = parseClaims(string(old))
		carried = len(lines)
	} else if !errors.Is(err, fs.ErrNotExist) {
		return false, 0, 0, err
	}
	all, err := s.existingIDs()
	if err != nil {
		return false, 0, 0, err
	}
	for _, id := range all {
		l := ClaimLine{Kind: ClaimID, Ticket: id}
		completeClaim(&l, "", ident)
		lines = append(lines, l)
	}
	var content strings.Builder
	for _, l := range lines {
		enc, err := encodeClaim(l)
		if err != nil {
			return false, 0, 0, err
		}
		content.WriteString(enc)
	}
	commit, err := s.commitClaims(ident, "", content.String(), "jig: claims log of the writers store")
	if err != nil {
		return false, 0, 0, err
	}
	if _, err := gitx.Run(s.Root, "update-ref", claimsRef, commit, strings.Repeat("0", len(commit))); err != nil {
		return false, 0, 0, err
	}
	if out, perr := gitx.RunRaw(s.Root, "push", "origin", claimsRef+":"+claimsRef); perr != nil {
		_, _ = gitx.Run(s.Root, "update-ref", "-d", claimsRef, commit)
		if claimsPushRaced(out) {
			return false, 0, 0, s.fetchClaims()
		}
		return false, 0, 0, perr
	}
	// Fetch what the push made known, so the clone sees the origin branch.
	if _, err := gitx.Run(s.Root, "fetch", "--quiet", "origin", "+"+claimsRef+":"+claimsRemoteRef); err != nil {
		return false, 0, 0, err
	}
	return true, len(all), carried, nil
}

// existingIDs lists every ticket id the store has and every alias those
// tickets carry, once each, ids first: what the claims branch starts with so
// no writer ever mints one again.
func (s *Store) existingIDs() ([]string, error) {
	ids, err := s.TicketIDs()
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var out []string
	add := func(id string) {
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	for _, id := range ids {
		add(id)
	}
	for _, id := range ids {
		// A ticket.yaml that cannot be read costs only its own aliases, as for
		// nextID; jig validate reports it.
		rec, err := s.ReadTicket(id)
		if err != nil {
			continue
		}
		for _, alias := range rec.Aliases {
			add(alias)
		}
	}
	return out, nil
}
