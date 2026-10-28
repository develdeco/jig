package store

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/gitx"
)

// SymlinksOn reports whether this clone keeps symlinks: git's core.symlinks,
// which is on unless a clone's git found its file system unable to make them,
// and not a link the file system itself has since refused. With it off, git
// leaves a plain file holding the link's target where a clone has an entry,
// and the tree code leaves that file as it is.
func (s *Store) SymlinksOn() bool {
	s.tracker.symOnce.Do(func() {
		out, err := gitx.Run(s.Root, "config", "--type=bool", "--default", "true", "--get", "core.symlinks")
		s.tracker.symOn = err != nil || out != "false"
	})
	return s.tracker.symOn && !s.tracker.symFailed.Load()
}

// warnNoLinks tells the person, once per Store, that this clone makes no
// links, and what that costs.
func (s *Store) warnNoLinks(why string) {
	s.tracker.warned.Do(func() {
		s.warn("this clone makes no links in the tracker tree (%s); every record is still there and the sync works the same, but %s/ has no link to a ticket or chart", why, trackersDirName)
	})
}

// linkTarget is where o's link in e's folder points, relative to that folder.
func (s *Store) linkTarget(e TrackerEntry, o TrackerOwner) (string, error) {
	return filepath.Rel(s.TrackerPath(e), filepath.Join(s.Root, s.ownerRelDir(o)))
}

// sameTarget reports whether a link at link that reads got points where want
// (relative to the same folder) does.
func sameTarget(link, got, want string) bool {
	dir := filepath.Dir(link)
	resolve := func(t string) string {
		t = filepath.FromSlash(t)
		if filepath.IsAbs(t) {
			return filepath.Clean(t)
		}
		return filepath.Join(dir, t)
	}
	return resolve(got) == resolve(want)
}

// Link results: what LinkTrackerEntry did.
const (
	// LinkKept: the link was already right.
	LinkKept = ""
	// LinkMade: there was no link, and now there is.
	LinkMade = "made"
	// LinkReplaced: the link pointed elsewhere (or was a plain file), and now
	// points at the owner.
	LinkReplaced = "replaced"
	// LinkBlocked: a folder sits where the link goes, and is left alone.
	LinkBlocked = "blocked"
	// LinkOff: this clone makes no links.
	LinkOff = "off"
)

// LinkTrackerEntry makes the link in entry e's folder to o's folder, named by
// the store id, replacing one that points elsewhere, and reports which it did
// (the Link constants). The link of the flat layout, where the folder goes, is
// replaced by the folder. A link under another store id (the ticket was
// renamed) is RebuildTrackerLinks' to take out. A clone without symlinks gets
// LinkOff, and a warning once. The write goes in the working tree; Claim's
// paths and the next checkpoint's commit take it from there.
func (s *Store) LinkTrackerEntry(e TrackerEntry, o TrackerOwner) (string, error) {
	if err := e.check(); err != nil {
		return "", err
	}
	if !o.linkNameOK() {
		return "", &axi.Error{
			Msg:  fmt.Sprintf("%q cannot be the name of a link in the tracker tree", o.linkName()),
			Code: "TRACKER_ENTRY_INVALID",
			Help: []string{"A link is named by its ticket's id or its chart's name, one plain name that is not record.yaml"},
		}
	}
	if !s.SymlinksOn() {
		s.warnNoLinks("git's core.symlinks is off")
		return LinkOff, nil
	}
	link := s.TrackerLinkPath(e, o)
	want, err := s.linkTarget(e, o)
	if err != nil {
		return "", err
	}
	if err := s.makeEntryFolder(e); err != nil {
		return "", err
	}
	result := LinkMade
	fi, err := os.Lstat(link)
	switch {
	case err == nil && fi.Mode()&os.ModeSymlink != 0:
		if got, rerr := os.Readlink(link); rerr == nil && sameTarget(link, got, want) {
			return LinkKept, nil
		}
		if err := os.Remove(link); err != nil {
			return "", err
		}
		result = LinkReplaced
	case err == nil && fi.IsDir():
		return LinkBlocked, nil
	case err == nil:
		// A plain file: the stand-in git left while this clone, or the one that
		// made the checkout, had no symlinks.
		if err := os.Remove(link); err != nil {
			return "", err
		}
		result = LinkReplaced
	case !errors.Is(err, fs.ErrNotExist):
		return "", err
	}
	if err := os.Symlink(want, link); err != nil {
		s.tracker.symFailed.Store(true)
		s.warnNoLinks(fmt.Sprintf("the file system refused one: %v", err))
		return LinkOff, nil
	}
	return result, nil
}

// TrackerLinks is what RebuildTrackerLinks did, as store-relative slash paths.
type TrackerLinks struct {
	// Made are the links it made where there were none.
	Made []string
	// Replaced are the links (or plain files) it replaced with a right one.
	Replaced []string
	// Removed are the links it took out because no record names them: one in a
	// folder with no record, or under a store id the record no longer names.
	Removed []string
	// Blocked are the entries a folder sits at, left alone.
	Blocked []string
	// Off is set when the clone makes no links, and so none was touched.
	Off bool
}

// Changed reports whether the rebuild touched the working tree.
func (l TrackerLinks) Changed() bool {
	return len(l.Made)+len(l.Replaced)+len(l.Removed) > 0
}

// linkable reports whether rec is a record that gets a link: it names a ticket
// or chart the store has, by an id that can name a link, and sits in a scope
// folder.
func (s *Store) linkable(rec TrackerRecord) bool {
	return rec.Err == nil && rec.Owner().valid() && rec.Owner().linkNameOK() && rec.Entry.Scope != "" && rec.Entry.check() == nil && s.ownerExists(rec.Owner())
}

// RebuildTrackerLinks makes every link of the tree from the records alone, with
// no provider and no network: each record that names a ticket or chart in the
// store gets a relative symlink to its folder, named by the store id, in its
// entry's folder. A link that points elsewhere is replaced, and a link under
// any other name (a ticket renamed through an alias leaves its old id's) or in
// a folder with no record is removed. A second run finds nothing to do. Where
// the clone has no symlinks it touches nothing and warns once; every record is
// still there. It reads the records as the Store remembers them;
// ReloadTrackerTree first for the tree as it is on disk now.
func (s *Store) RebuildTrackerLinks() (TrackerLinks, error) {
	var out TrackerLinks
	idx, err := s.trackerRecordIndex()
	if err != nil {
		return out, err
	}
	scan, err := s.scanTracker()
	if err != nil {
		return out, err
	}
	if len(idx.records) == 0 && len(scan.entries) == 0 {
		return out, nil
	}
	if !s.SymlinksOn() {
		out.Off = true
		for _, rec := range idx.records {
			if s.linkable(rec) {
				s.warnNoLinks("git's core.symlinks is off")
				break
			}
		}
		return out, nil
	}
	wanted := map[string]bool{}
	folders := map[string]bool{}
	left := map[string]bool{}
	for _, rec := range idx.records {
		dir := rec.dir()
		folders[dir] = true
		if !s.linkable(rec) {
			// A record that names nothing, or something the store lacks: its
			// folder and whatever links are in it are left as they are.
			left[dir] = true
			continue
		}
		rel := path.Join(dir, rec.Owner().linkName())
		wanted[rel] = true
		res, err := s.LinkTrackerEntry(rec.Entry, rec.Owner())
		if err != nil {
			return out, err
		}
		switch res {
		case LinkMade:
			out.Made = append(out.Made, rel)
		case LinkReplaced:
			out.Replaced = append(out.Replaced, rel)
		case LinkBlocked:
			out.Blocked = append(out.Blocked, rel)
		case LinkOff:
			out.Off = true
			return out, nil
		}
	}
	for _, rel := range scan.entries {
		if wanted[rel] || folders[rel] || left[path.Dir(rel)] {
			continue
		}
		abs := filepath.Join(s.Root, filepath.FromSlash(rel))
		if fi, err := os.Lstat(abs); err != nil || fi.Mode()&os.ModeSymlink == 0 {
			continue
		}
		if err := os.Remove(abs); err != nil {
			return out, err
		}
		s.pruneTrackerDirs(filepath.Dir(abs))
		out.Removed = append(out.Removed, rel)
	}
	return out, nil
}
