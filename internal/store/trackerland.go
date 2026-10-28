package store

import (
	"path/filepath"
	"strings"
	"sync"

	"github.com/develdeco/jig/internal/gitx"
)

// trackerLandMessage is the commit message of a landing: the tracker
// mirror's record writes, committed once the sync that made them is done.
const trackerLandMessage = "jig: record the tracker mirror's sync"

// trackerWrites is the set of paths the tracker tree's writers (record
// writes and removals, links, an old-place record's removal) changed since
// the last landing, as store-relative slash paths. Only a write that changed
// the working tree is noted, so a sync that finds the store up to date notes
// nothing and the landing costs no git process.
type trackerWrites struct {
	mu    sync.Mutex
	paths map[string]struct{}
}

// noteTrackerWrite records that rels were written, removed or linked in the
// working tree on a tracker's behalf.
func (s *Store) noteTrackerWrite(rels ...string) {
	s.landing.mu.Lock()
	defer s.landing.mu.Unlock()
	if s.landing.paths == nil {
		s.landing.paths = map[string]struct{}{}
	}
	for _, rel := range rels {
		s.landing.paths[filepath.ToSlash(rel)] = struct{}{}
	}
}

// takeTrackerWrites returns the noted paths and forgets them.
func (s *Store) takeTrackerWrites() []string {
	s.landing.mu.Lock()
	defer s.landing.mu.Unlock()
	paths := make([]string, 0, len(s.landing.paths))
	for p := range s.landing.paths {
		paths = append(paths, p)
	}
	s.landing.paths = nil
	return paths
}

// LandTrackerWrites commits what the tracker tree's writers changed in the
// working tree since the last landing and pushes the store to its origin,
// whether or not there was anything to commit: it is how `jig trackers sync`
// on a store with an origin ends with the mirror's records, and the commit
// Sync made of the leftovers before the sync, on origin.
func (s *Store) LandTrackerWrites() error {
	return s.landTrackerWrites(true)
}

// landTrackerWrites is the end of a checkpoint's mirror sync. The sync's
// record writes (an issue's prs: entry, its synced: fields, a board item, a
// link) land in the working tree only, as every record write but a
// creation's claim does (ADR 0030), and this commits exactly those paths, as
// the person, so the next checkpoint's commit is not the only thing that
// carries them. It pushes when it committed, when always is set, or when the
// branch is ahead of origin anyway (another process's Sync swept the sync's
// writes into a commit of its own, which never pushes). The commit is scoped
// to the noted paths the way Claim's is: any other dirty state in the store,
// a journal line another process just wrote, stays where it is. It holds the
// checkpoint lock (LockCheckpoint) from the commit through the push, and does
// nothing, not even run git, when nothing was noted.
//
// It returns the cause of a failure: a store with no origin never fails on a
// push, and one whose origin cannot be reached leaves the commit for the next
// checkpoint's push.
func (s *Store) landTrackerWrites(always bool) error {
	paths := s.takeTrackerWrites()
	if len(paths) == 0 && !always {
		return nil
	}
	release, err := s.LockCheckpoint()
	if err != nil {
		return err
	}
	defer release()
	cp, err := s.begin()
	if err != nil {
		return err
	}
	committed := false
	if len(paths) > 0 {
		changed, err := s.changedPaths(paths)
		if err != nil {
			return err
		}
		if len(changed) > 0 {
			committed, err = s.commitTrackerPaths(changed)
			if err != nil {
				return err
			}
		}
	}
	if !cp.hasRemote() {
		return nil
	}
	if !committed && !always && !s.aheadOfOrigin(cp.branch) {
		return nil
	}
	return cp.pushLanding()
}

// aheadOfOrigin reports whether the branch holds a commit origin does not, as
// far as the clone knows. A branch origin has never had counts as ahead.
func (s *Store) aheadOfOrigin(branch string) bool {
	out, err := gitx.Run(s.Root, "rev-list", "--count", "refs/remotes/origin/"+branch+"..HEAD")
	return err != nil || strings.TrimSpace(out) != "0"
}

// pushLanding pushes the branch once and, when origin has moved on, records
// whatever else is uncommitted the way Sync does before taking what moved:
// the landing leaves other changes in the working tree on purpose, and a
// rebase refuses a dirty one.
func (cp *checkpoint) pushLanding() error {
	rejected, err := cp.pushOnce()
	if err == nil || !rejected {
		return err
	}
	if err := cp.commit(leftoversMessage); err != nil {
		return err
	}
	return cp.push()
}

// commitTrackerPaths stages paths and commits them as the person, and reports
// whether it committed. When nothing else is staged, the commit is the index as
// it is: a pathspec commit (stageAndCommitPaths) cannot take a path that was a
// file in HEAD and is a folder now, as the flat layout's link is once its
// entry's folder takes its place ("does not have a commit checked out"), and
// the move out of the flat layout is exactly that. Something staged already is
// not this landing's to commit, so it takes the pathspec commit, which leaves
// it where it is and, for that move, fails: the landing is then a warning, and
// the next checkpoint's commit takes the paths.
func (s *Store) commitTrackerPaths(paths []string) (bool, error) {
	foreign, err := s.hasStagedChanges()
	if err != nil {
		return false, err
	}
	if foreign {
		sha, err := s.stageAndCommitPaths(paths, trackerLandMessage)
		return sha != "", err
	}
	if _, err := gitx.Run(s.Root, append([]string{"add", "--"}, paths...)...); err != nil {
		return false, err
	}
	staged, err := s.hasStagedChanges()
	if err != nil || !staged {
		return false, err
	}
	id, err := s.identity()
	if err != nil {
		return false, err
	}
	if _, err := gitx.Run(s.Root, commitConfig(id, "commit", "-m", trackerLandMessage)...); err != nil {
		return false, err
	}
	return true, nil
}

// changedPaths returns those of paths that differ from the index: modified,
// deleted or not yet tracked (and not ignored). A path a sync noted and that
// a claim's undo took off the disk again is neither, and is left out, which
// keeps `git add` from refusing a pathspec that matches nothing.
func (s *Store) changedPaths(paths []string) ([]string, error) {
	args := append([]string{"ls-files", "-z", "--modified", "--deleted", "--others", "--exclude-standard", "--"}, paths...)
	out, err := gitx.Run(s.Root, args...)
	if err != nil {
		return nil, err
	}
	var changed []string
	seen := map[string]bool{}
	for _, p := range strings.Split(out, "\x00") {
		if p == "" || seen[p] {
			continue
		}
		seen[p] = true
		changed = append(changed, p)
	}
	return changed, nil
}
