package store

import (
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	"gopkg.in/yaml.v3"

	"github.com/develdeco/jig/internal/axi"
)

// The tracker tree is where the store keeps what a remote tracker holds of its
// tickets and charts: one entry per remote item, at
// trackers/<provider>/<scope>/<remote id>. GitHub's scope is <owner>/<repo>, the
// place its issue numbers are unique, and its remote id is the bare number, so
// issue 65 of develdeco/jig is trackers/github/develdeco/jig/65.
//
// Beside each entry, <remote id>.yaml is the record: it names its ticket
// (ticket: <id>) or chart (chart: <name>), and every other key is the
// provider's, kept exactly as the provider wrote it. The tree code owns the id
// and the link and nothing else. The entry itself is a relative symlink to
// tickets/<id> or charts/<name>, committed as git stores symlinks. The record
// is the mapping and the link a view of it: RebuildTrackerLinks makes every
// link from the records alone, with no provider and no network.
//
// A ticket's or chart's own folder holds no tracker data. The first GitHub
// mirror kept its records there, in <ticket>/tracker/github.yaml and
// charts/<name>/github.yaml; those places are still read where a ticket has no
// record in the tree (TrackerIDs), until a sync moves them in
// (MoveLegacyTrackerRecord).

// ProviderGitHub is the provider folder of GitHub's records:
// trackers/github/<owner>/<repo>/<number>.
const ProviderGitHub = "github"

// trackerRecordSuffix ends the file name of a record: <remote id>.yaml.
const trackerRecordSuffix = ".yaml"

// legacyRecordFile is the name of the record A6's mirror first kept in a
// ticket's tracker/ folder and in a chart's folder, before the tree.
const legacyRecordFile = "github.yaml"

// TrackerEntry names one remote item in the tree.
type TrackerEntry struct {
	// Provider is the tracker's folder: "github".
	Provider string
	// Scope is the slash-separated place the provider's ids are unique in:
	// "owner/repo" for GitHub.
	Scope string
	// ID is the remote id: a GitHub issue's bare number.
	ID string
}

// String spells the entry <provider>/<scope>/<id>, the tail of its path.
func (e TrackerEntry) String() string {
	return e.Provider + "/" + e.Scope + "/" + e.ID
}

// trackerNamePattern matches one component of an entry's path: what a
// provider, an owner, a repository and a remote id are made of.
var trackerNamePattern = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)

// trackerNameOK reports whether name can be one component of an entry's path.
func trackerNameOK(name string) bool {
	return name != "." && name != ".." && trackerNamePattern.MatchString(name)
}

// check refuses an entry that does not name a place inside the tree: every
// component is a plain name, so an id read out of a file can never lead a
// write out of trackers/.
func (e TrackerEntry) check() error {
	ok := trackerNameOK(e.Provider) && trackerNameOK(e.ID) && !strings.HasSuffix(e.ID, trackerRecordSuffix) && e.Scope != ""
	for _, c := range strings.Split(e.Scope, "/") {
		ok = ok && trackerNameOK(c)
	}
	if ok {
		return nil
	}
	return &axi.Error{
		Msg:  fmt.Sprintf("%q is not a place in the tracker tree", e.String()),
		Code: "TRACKER_ENTRY_INVALID",
		Help: []string{"An entry is trackers/<provider>/<scope>/<remote id>, each part made of letters, digits, dots, dashes and underscores"},
	}
}

// TrackerOwner is what a record is of: one ticket or one chart, exactly one of
// the two set.
type TrackerOwner struct {
	// Ticket is a ticket id.
	Ticket string
	// Chart is a chart name.
	Chart string
}

// valid reports whether exactly one of the two is set.
func (o TrackerOwner) valid() bool { return (o.Ticket != "") != (o.Chart != "") }

// String names the owner the way a message does: "ticket JIG-1", "chart demo".
func (o TrackerOwner) String() string {
	if o.Chart != "" {
		return "chart " + o.Chart
	}
	return "ticket " + o.Ticket
}

// ownerRelDir is the owner's folder, relative to the store root: where its
// link points.
func (s *Store) ownerRelDir(o TrackerOwner) string {
	if o.Chart != "" {
		return filepath.Join("charts", o.Chart)
	}
	return s.TicketRelDir(o.Ticket)
}

// ownerExists reports whether the owner's folder is in the store.
func (s *Store) ownerExists(o TrackerOwner) bool {
	fi, err := os.Stat(filepath.Join(s.Root, s.ownerRelDir(o)))
	return err == nil && fi.IsDir()
}

// TrackerRecord is one record of the tree as read.
type TrackerRecord struct {
	// Entry is where the record sits, read from its path.
	Entry TrackerEntry
	// Ticket is the ticket the record names, as ResolveTicket gives it (an
	// alias in the file is followed to the ticket's current id), or "".
	Ticket string
	// Named is Ticket as the file spells it.
	Named string
	// Chart is the chart the record names, or "".
	Chart string
	// Fields is every other key of the record, a mapping in file order: the
	// provider's, which the tree code never reads or changes.
	Fields yaml.Node
	// Err is why the record could not be read or decoded; the other fields
	// are then empty.
	Err error
}

// Owner returns the ticket or chart the record names: valid only when the
// record names exactly one.
func (r TrackerRecord) Owner() TrackerOwner { return TrackerOwner{Ticket: r.Ticket, Chart: r.Chart} }

// trackerState is what a Store remembers of the tree between calls.
type trackerState struct {
	// mu guards idx.
	mu sync.Mutex
	// idx is the memoized index of the records, nil until built.
	idx *trackerIndex

	// symOnce guards symOn, git's core.symlinks for this clone.
	symOnce sync.Once
	symOn   bool
	// symFailed is set when the file system refused a link that git's
	// setting allowed, after which this Store makes no more.
	symFailed atomic.Bool
	// warned makes the "no links" warning once per Store, so once per command.
	warned sync.Once
}

// trackerIndex is the records of the tree read once: in path order, and by the
// ticket or chart each names.
type trackerIndex struct {
	records []TrackerRecord
	byOwner map[TrackerOwner][]int
	byEntry map[TrackerEntry]int
}

// invalidateTrackerIndex drops the memoized index, so the next read scans the
// tree. A pull does, and so does a change of aliases (invalidateAliasClaims):
// either can bring in or rename what the index holds. The writes the tree code
// makes itself do not; they put the record they wrote into the index
// (putTrackerRecord), which is what lets a sync that writes a record per
// ticket and asks for the ticket's records again in between read the tree
// once.
func (s *Store) invalidateTrackerIndex() {
	s.tracker.mu.Lock()
	defer s.tracker.mu.Unlock()
	s.tracker.idx = nil
}

// ReloadTrackerTree drops what the Store remembers of the tree, so the next
// read scans it. A caller that needs the tree as it is on disk now (a sync
// starting, a claim looking for a record another clone just pushed) asks for
// it; a record changed by any other process is not seen otherwise.
func (s *Store) ReloadTrackerTree() { s.invalidateTrackerIndex() }

// newTrackerIndex indexes records, which are in path order, by the ticket or
// chart each names.
func newTrackerIndex(records []TrackerRecord) *trackerIndex {
	idx := &trackerIndex{records: records, byOwner: map[TrackerOwner][]int{}, byEntry: make(map[TrackerEntry]int, len(records))}
	for i, rec := range records {
		idx.byEntry[rec.Entry] = i
		if o := rec.Owner(); rec.Err == nil && o.valid() {
			idx.byOwner[o] = append(idx.byOwner[o], i)
		}
	}
	return idx
}

// recordSlashPath is the path of e's record relative to the store root, in
// the slash form scanTracker sorts by.
func recordSlashPath(e TrackerEntry) string {
	return path.Join(trackersDirName, e.Provider, e.Scope, e.ID) + trackerRecordSuffix
}

// changeTrackerIndex replaces the memoized index, when there is one, with what
// change makes of a copy of its records (which it keeps in path order). The
// index a reader holds is never edited under it.
func (s *Store) changeTrackerIndex(change func([]TrackerRecord) []TrackerRecord) {
	s.tracker.mu.Lock()
	defer s.tracker.mu.Unlock()
	if s.tracker.idx == nil {
		return
	}
	records := change(append([]TrackerRecord(nil), s.tracker.idx.records...))
	sort.SliceStable(records, func(i, j int) bool {
		return recordSlashPath(records[i].Entry) < recordSlashPath(records[j].Entry)
	})
	s.tracker.idx = newTrackerIndex(records)
}

// putTrackerRecord keeps the memoized index in step with a record just
// written.
func (s *Store) putTrackerRecord(rec TrackerRecord) {
	s.changeTrackerIndex(func(records []TrackerRecord) []TrackerRecord {
		for i := range records {
			if records[i].Entry == rec.Entry {
				records[i] = rec
				return records
			}
		}
		return append(records, rec)
	})
}

// dropTrackerRecord keeps the memoized index in step with a record just
// removed.
func (s *Store) dropTrackerRecord(e TrackerEntry) {
	s.changeTrackerIndex(func(records []TrackerRecord) []TrackerRecord {
		kept := records[:0]
		for _, rec := range records {
			if rec.Entry != e {
				kept = append(kept, rec)
			}
		}
		return kept
	})
}

// trackerRecordIndex returns the memoized index, building it on first use.
func (s *Store) trackerRecordIndex() (*trackerIndex, error) {
	s.tracker.mu.Lock()
	defer s.tracker.mu.Unlock()
	if s.tracker.idx != nil {
		return s.tracker.idx, nil
	}
	idx, err := s.buildTrackerIndex()
	if err != nil {
		return nil, err
	}
	s.tracker.idx = idx
	return idx, nil
}

// buildTrackerIndex reads every record under trackers/.
func (s *Store) buildTrackerIndex() (*trackerIndex, error) {
	files, _, err := s.scanTracker()
	if err != nil {
		return nil, err
	}
	records := make([]TrackerRecord, len(files))
	for i, rel := range files {
		records[i] = s.readTrackerRecord(rel)
	}
	return newTrackerIndex(records), nil
}

// TrackerRecords reads every record of the tree, in path order. A record that
// cannot be read is in the list with its Err set: it is the tree check's to
// report, and the list is still the whole tree.
func (s *Store) TrackerRecords() ([]TrackerRecord, error) {
	idx, err := s.trackerRecordIndex()
	if err != nil {
		return nil, err
	}
	return append([]TrackerRecord(nil), idx.records...), nil
}

// TrackerRecordsFor reads the records that name o, in path order. A record's
// ticket is resolved through ResolveTicket, so o.Ticket is the ticket's
// current id and a record written under an old id still finds it.
func (s *Store) TrackerRecordsFor(o TrackerOwner) ([]TrackerRecord, error) {
	idx, err := s.trackerRecordIndex()
	if err != nil {
		return nil, err
	}
	var out []TrackerRecord
	for _, i := range idx.byOwner[o] {
		out = append(out, idx.records[i])
	}
	return out, nil
}

// scanTracker lists the tree without reading a file: the record files
// (<remote id>.yaml) and the entries beside them (the links, or the plain
// files git leaves where a clone cannot make one), as slash paths relative to
// the store root. Links are not followed. An absent trackers/ lists nothing.
func (s *Store) scanTracker() (records, entries []string, err error) {
	var walk func(dir, rel string) error
	walk = func(dir, rel string) error {
		ents, err := os.ReadDir(dir)
		if err != nil {
			if os.IsNotExist(err) && rel == trackersDirName {
				return nil
			}
			return fmt.Errorf("store: list %s: %w", rel, err)
		}
		for _, e := range ents {
			name := e.Name()
			child := path.Join(rel, name)
			switch {
			case e.IsDir():
				if err := walk(filepath.Join(dir, name), child); err != nil {
					return err
				}
			case strings.HasSuffix(name, trackerRecordSuffix):
				records = append(records, child)
			case strings.HasPrefix(name, ".") && strings.HasSuffix(name, ".tmp"):
				// AtomicWrite's temporary file, mid-write or left by a crash.
			default:
				entries = append(entries, child)
			}
		}
		return nil
	}
	if err := walk(filepath.Join(s.Root, trackersDirName), trackersDirName); err != nil {
		return nil, nil, err
	}
	sort.Strings(records)
	sort.Strings(entries)
	return records, entries, nil
}

// entryOfRecordPath reads the entry a record's slash path (relative to the
// store root) sits at. A path with no scope folder (trackers/github/5.yaml)
// has an empty Scope.
func entryOfRecordPath(rel string) TrackerEntry {
	parts := strings.Split(strings.TrimPrefix(rel, trackersDirName+"/"), "/")
	e := TrackerEntry{Provider: parts[0], ID: strings.TrimSuffix(parts[len(parts)-1], trackerRecordSuffix)}
	if len(parts) > 2 {
		e.Scope = strings.Join(parts[1:len(parts)-1], "/")
	}
	return e
}

// readTrackerRecord reads the record at rel, a slash path relative to the
// store root.
func (s *Store) readTrackerRecord(rel string) TrackerRecord {
	rec := TrackerRecord{Entry: entryOfRecordPath(rel)}
	data, err := readFile(filepath.Join(s.Root, filepath.FromSlash(rel)))
	if err != nil {
		rec.Err = err
		return rec
	}
	named, chart, fields, err := decodeTrackerRecord(data)
	if err != nil {
		rec.Err = fmt.Errorf("%s could not be decoded: %w", rel, err)
		return rec
	}
	rec.Named, rec.Chart, rec.Fields = named, chart, fields
	rec.Ticket = named
	if named != "" {
		if resolved, err := s.ResolveTicket(named); err == nil {
			rec.Ticket = resolved
		}
	}
	return rec
}

// decodeTrackerRecord splits a record into the two keys the tree owns and the
// rest, which it hands back as the mapping it was, in file order. The comment
// above the first key is the provider's header, which the provider writes
// again, so it is dropped.
func decodeTrackerRecord(data []byte) (ticket, chart string, fields yaml.Node, err error) {
	fields = yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	var doc yaml.Node
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return "", "", fields, err
	}
	if doc.Kind == 0 {
		return "", "", fields, nil
	}
	if len(doc.Content) != 1 || doc.Content[0].Kind != yaml.MappingNode {
		return "", "", fields, errors.New("it is not a mapping")
	}
	root := doc.Content[0]
	if len(root.Content) > 0 {
		root.Content[0].HeadComment = ""
	}
	for i := 0; i+1 < len(root.Content); i += 2 {
		k, v := root.Content[i], root.Content[i+1]
		switch k.Value {
		case "ticket":
			ticket = v.Value
		case "chart":
			chart = v.Value
		default:
			fields.Content = append(fields.Content, k, v)
		}
	}
	return ticket, chart, fields, nil
}

// encodeTrackerRecord writes a record: header (the provider's comment, whole
// lines), the key naming o, then fields as they are.
func encodeTrackerRecord(header string, o TrackerOwner, fields *yaml.Node) ([]byte, error) {
	key, val := "ticket", o.Ticket
	if o.Chart != "" {
		key, val = "chart", o.Chart
	}
	root := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", Content: []*yaml.Node{
		{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
		{Kind: yaml.ScalarNode, Tag: "!!str", Value: val},
	}}
	if fields != nil && fields.Kind == yaml.MappingNode {
		root.Content = append(root.Content, fields.Content...)
	}
	data, err := yaml.Marshal(root)
	if err != nil {
		return nil, err
	}
	return append([]byte(header), data...), nil
}

// WriteTrackerRecord writes the record of entry e: header (the provider's
// comment lines), the key that names o, then fields, the provider's mapping,
// untouched. It writes the record only; the link is LinkTrackerEntry's or
// RebuildTrackerLinks'.
func (s *Store) WriteTrackerRecord(e TrackerEntry, o TrackerOwner, fields *yaml.Node, header string) error {
	if err := e.check(); err != nil {
		return err
	}
	if !o.valid() {
		return fmt.Errorf("store: a tracker record names one ticket or one chart, not %+v", o)
	}
	if err := s.checkEntryFree(e, o); err != nil {
		return err
	}
	data, err := encodeTrackerRecord(header, o, fields)
	if err != nil {
		return fmt.Errorf("store: encode %s: %w", e, err)
	}
	if err := AtomicWrite(s.TrackerRecordPath(e), data); err != nil {
		s.invalidateTrackerIndex()
		return err
	}
	rec := TrackerRecord{Entry: e, Ticket: o.Ticket, Named: o.Ticket, Chart: o.Chart}
	if fields != nil && fields.Kind == yaml.MappingNode {
		rec.Fields = *fields
	} else {
		rec.Fields = yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	}
	s.putTrackerRecord(rec)
	return nil
}

// checkEntryFree refuses to write o's record over the record of another ticket
// or chart: one remote item is one ticket's or chart's, and a write that took
// it would take the other's issue away silently. A record that cannot be read
// is no one's, and is written over.
func (s *Store) checkEntryFree(e TrackerEntry, o TrackerOwner) error {
	idx, err := s.trackerRecordIndex()
	if err != nil {
		return err
	}
	i, ok := idx.byEntry[e]
	if !ok {
		return nil
	}
	if have := idx.records[i]; have.Err == nil && have.Owner().valid() && have.Owner() != o {
		return &axi.Error{
			Msg:  fmt.Sprintf("%s is the record of %s, not of %s", filepath.ToSlash(s.TrackerRecordRelPath(e)), have.Owner(), o),
			Code: "TRACKER_ENTRY_TAKEN",
			Help: []string{"Two items cannot be one remote item: fix the ticket: or chart: key of the record that is wrong, or remove it"},
		}
	}
	return nil
}

// RemoveTrackerRecord removes the record of entry e and its link, when it has
// them, and the folders that held only those.
func (s *Store) RemoveTrackerRecord(e TrackerEntry) error {
	if err := e.check(); err != nil {
		return err
	}
	if err := os.Remove(s.TrackerRecordPath(e)); err != nil && !os.IsNotExist(err) {
		s.invalidateTrackerIndex()
		return err
	}
	s.dropTrackerRecord(e)
	if fi, err := os.Lstat(s.TrackerPath(e)); err == nil && fi.Mode()&os.ModeSymlink != 0 {
		if err := os.Remove(s.TrackerPath(e)); err != nil {
			return err
		}
	}
	// A folder with anything left in it refuses to go: that is the check.
	for dir := filepath.Dir(s.TrackerPath(e)); dir != filepath.Join(s.Root, trackersDirName); dir = filepath.Dir(dir) {
		if os.Remove(dir) != nil {
			break
		}
	}
	return nil
}
