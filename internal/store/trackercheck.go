package store

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Tree problem codes: what CheckTrackerTree reports.
const (
	// TrackerRecordInvalid: a record that cannot be read or decoded.
	TrackerRecordInvalid = "TRACKER_RECORD_INVALID"
	// TrackerRecordOrphan: a record that names no ticket or chart.
	TrackerRecordOrphan = "TRACKER_RECORD_ORPHAN"
	// TrackerRecordDuplicate: two records in one scope for one ticket or chart.
	TrackerRecordDuplicate = "TRACKER_RECORD_DUPLICATE"
	// TrackerRecordMisplaced: a record whose folder is not its remote id.
	TrackerRecordMisplaced = "TRACKER_RECORD_MISPLACED"
	// TrackerLinkWrong: a link that is missing or points elsewhere.
	TrackerLinkWrong = "TRACKER_LINK_WRONG"
	// TrackerRecordUnmoved: a record still in a ticket or chart folder, or still
	// beside its link as <remote id>.yaml instead of in its entry's folder.
	TrackerRecordUnmoved = "TRACKER_RECORD_UNMOVED"
)

// TrackerProblem is one thing CheckTrackerTree found wrong.
type TrackerProblem struct {
	// Code is one of the Tracker... constants.
	Code string
	// Path is the record, link or old-place file it is about, relative to the
	// store root.
	Path string
	// Msg says what is wrong, in one line.
	Msg string
	// Help says what to do about it.
	Help string
}

// githubIdentity reads the two keys of a GitHub record that say which issue it
// is.
func githubIdentity(fields yaml.Node) (repo string, issue int) {
	var ids githubRecordIDs
	if fields.Kind == yaml.MappingNode {
		_ = fields.Decode(&ids)
	}
	return ids.Repo, ids.Issue
}

// CheckTrackerTree checks the tree against the store and against itself, with
// or without a provider configured, and returns what is wrong in path order:
//   - a record that cannot be read, or that names no ticket or chart in the
//     store (none, both, or one the store does not have);
//   - two records in one scope for the same ticket or chart;
//   - a record whose folder name is not the remote id it holds, or whose
//     scope folder is not the repository it holds;
//   - where the clone has symlinks, a link that is missing from its entry's
//     folder, or points somewhere other than its record's ticket or chart;
//   - a record still in a ticket or chart folder, or still in the flat layout.
func (s *Store) CheckTrackerTree() ([]TrackerProblem, error) {
	s.invalidateTrackerIndex()
	idx, err := s.trackerRecordIndex()
	if err != nil {
		return nil, err
	}
	type group struct {
		provider, scope string
		owner           TrackerOwner
	}
	var problems []TrackerProblem
	byGroup := map[group][]string{}
	var order []group
	linksOn := s.SymlinksOn()

	for _, rec := range idx.records {
		rel := rec.Rel
		if rec.Err != nil {
			problems = append(problems, TrackerProblem{
				Code: TrackerRecordInvalid, Path: rel,
				Msg:  fmt.Sprintf("%s could not be read: %v", rel, rec.Err),
				Help: "Restore it from the store's history, or remove it and let the next `jig trackers sync` adopt the item again",
			})
			continue
		}
		if rec.Entry.Scope == "" {
			problems = append(problems, TrackerProblem{
				Code: TrackerRecordMisplaced, Path: rel,
				Msg:  fmt.Sprintf("%s is in no scope folder: a record is trackers/<provider>/<scope>/<remote id>/record.yaml", rel),
				Help: "Move it to the folder of the repository it is for",
			})
		}
		o := rec.Owner()
		if reason := s.orphanReason(rec); reason != "" {
			problems = append(problems, TrackerProblem{
				Code: TrackerRecordOrphan, Path: rel,
				Msg:  fmt.Sprintf("%s names no ticket or chart: %s", rel, reason),
				Help: "Fix its ticket: or chart: key, or remove the record",
			})
		} else {
			g := group{rec.Entry.Provider, rec.Entry.Scope, o}
			if len(byGroup[g]) == 0 {
				order = append(order, g)
			}
			byGroup[g] = append(byGroup[g], rel)
		}
		if rec.Entry.Provider == ProviderGitHub && rec.Entry.Scope != "" {
			if repo, issue := githubIdentity(rec.Fields); issue < 1 || strconv.Itoa(issue) != rec.Entry.ID || !strings.EqualFold(repo, rec.Entry.Scope) {
				is := "names no issue"
				if issue >= 1 {
					is = fmt.Sprintf("is for %s#%d", repo, issue)
				}
				problems = append(problems, TrackerProblem{
					Code: TrackerRecordMisplaced, Path: rel,
					Msg:  fmt.Sprintf("%s %s, not %s#%s", rel, is, rec.Entry.Scope, rec.Entry.ID),
					Help: "A GitHub record is trackers/github/<owner>/<repo>/<number>/record.yaml: move it to the folder of the issue's own number",
				})
			}
		}
		// A record of the flat layout has its link where its folder goes: the
		// unmoved record is the one thing to report.
		if linksOn && !rec.Flat && s.linkable(rec) {
			if msg := s.linkFault(rec.Entry, o); msg != "" {
				problems = append(problems, TrackerProblem{
					Code: TrackerLinkWrong, Path: filepath.ToSlash(s.TrackerLinkRelPath(rec.Entry, o)),
					Msg:  msg,
					Help: "Run `jig trackers sync`: it makes every link again from the records",
				})
			}
		}
	}
	for _, g := range order {
		if paths := byGroup[g]; len(paths) > 1 {
			problems = append(problems, TrackerProblem{
				Code: TrackerRecordDuplicate, Path: paths[1],
				Msg:  fmt.Sprintf("%s are all %s/%s records of %s", strings.Join(paths, ", "), g.provider, g.scope, g.owner),
				Help: "Keep the record of the item that is really the ticket's or chart's, and remove the others",
			})
		}
	}

	scan, err := s.scanTracker()
	if err != nil {
		return nil, err
	}
	for _, rel := range scan.flat {
		e, _ := entryOfRecordPath(rel)
		problems = append(problems, TrackerProblem{
			Code: TrackerRecordUnmoved, Path: rel,
			Msg:  fmt.Sprintf("%s is still beside its link: a record is %s", rel, filepath.ToSlash(s.TrackerRecordRelPath(e))),
			Help: "Run `jig trackers sync`: it moves the record into its entry's folder and links it there",
		})
	}
	legacy, err := s.LegacyTrackerRecords()
	if err != nil {
		return nil, err
	}
	for _, l := range legacy {
		rel := filepath.ToSlash(l.Rel)
		problems = append(problems, TrackerProblem{
			Code: TrackerRecordUnmoved, Path: rel,
			Msg:  fmt.Sprintf("%s is still in the folder of %s, not in the tracker tree", rel, l.Owner),
			Help: "Run `jig trackers sync`: it moves the record into trackers/ and links it",
		})
	}
	sort.SliceStable(problems, func(i, j int) bool { return problems[i].Path < problems[j].Path })
	return problems, nil
}

// orphanReason says why a record names no ticket or chart of the store, or ""
// when it names one.
func (s *Store) orphanReason(rec TrackerRecord) string {
	o := rec.Owner()
	switch {
	case rec.Ticket == "" && rec.Chart == "":
		return "it has neither a ticket: nor a chart: key"
	case !o.valid():
		return "it has both a ticket: and a chart: key"
	case !o.linkNameOK():
		return fmt.Sprintf("%q is not a ticket id or a chart name", o.linkName())
	case s.ownerExists(o):
		return ""
	case o.Chart != "":
		return fmt.Sprintf("chart %q is not in the store", o.Chart)
	case rec.Named != rec.Ticket:
		return fmt.Sprintf("ticket %q, now %q, is not in the store", rec.Named, rec.Ticket)
	}
	return fmt.Sprintf("ticket %q is not in the store", rec.Ticket)
}

// linkFault says what is wrong with o's link in e's folder, or "" when it is
// right.
func (s *Store) linkFault(e TrackerEntry, o TrackerOwner) string {
	link := s.TrackerLinkPath(e, o)
	rel := filepath.ToSlash(s.TrackerLinkRelPath(e, o))
	want := filepath.ToSlash(s.ownerRelDir(o))
	fi, err := os.Lstat(link)
	switch {
	case err != nil:
		return fmt.Sprintf("%s is missing: it should be a link to %s", rel, want)
	case fi.Mode()&os.ModeSymlink == 0:
		return fmt.Sprintf("%s is not a link: it should be a link to %s", rel, want)
	}
	got, err := os.Readlink(link)
	if err != nil {
		return fmt.Sprintf("%s cannot be read as a link: %v", rel, err)
	}
	if target, err := s.linkTarget(e, o); err == nil && sameTarget(link, got, target) {
		return ""
	}
	return fmt.Sprintf("%s points at %s, not at %s", rel, filepath.ToSlash(got), want)
}
