package store

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/develdeco/jig/internal/axi"
)

// GitHubIssue is the issue a ticket is mirrored to.
type GitHubIssue struct {
	// Repo is "owner/name", the repository the issue lives in.
	Repo string
	// Number is the issue's number in that repository, from 1.
	Number int
}

// TrackerIDs is what the trackers that mirror a ticket call it: the ids a
// branch can lead with besides the store's own. A ticket no tracker has
// mirrored yet has the zero value.
type TrackerIDs struct {
	// GitHub is the ticket's issue, nil when the ticket is not mirrored.
	GitHub *GitHubIssue
}

// githubRecordIDs is the part of a GitHub record the store reads: which issue
// it is. The mirror (internal/mirror) writes the record and owns its whole
// shape, so the decode is lenient and the store cannot import the mirror to
// ask.
type githubRecordIDs struct {
	Repo  string `yaml:"repo"`
	Issue int    `yaml:"issue"`
}

// githubRecordPath is the old place of a ticket's GitHub record,
// <ticket>/tracker/github.yaml.
func (s *Store) githubRecordPath(ticket string) string {
	return filepath.Join(s.TicketDir(ticket), "tracker", legacyRecordFile)
}

// TrackerIDs reads ticket's ids in the trackers that mirror it, from the
// records of the tracker tree that name it (the first, in path order, when it
// has more than one GitHub record). A ticket with no record in the tree falls
// back to the one the first mirror kept in its own folder,
// <ticket>/tracker/github.yaml, so a store whose records have not moved yet
// still binds its branches. A ticket with no record, or a record that names no
// issue yet, has none. A record in the old place that cannot be decoded is an
// *axi.Error, TRACKER_RECORD_INVALID: the mirror owns the file, so the remedy
// is the mirror's; a tree record that cannot be decoded names no ticket, and
// is the tree check's to report.
func (s *Store) TrackerIDs(ticket string) (TrackerIDs, error) {
	recs, err := s.TrackerRecordsFor(TrackerOwner{Ticket: ticket})
	if err != nil {
		return TrackerIDs{}, err
	}
	for _, rec := range recs {
		if rec.Entry.Provider != ProviderGitHub {
			continue
		}
		if repo, issue := githubIdentity(rec.Fields); issue >= 1 {
			return TrackerIDs{GitHub: &GitHubIssue{Repo: repo, Number: issue}}, nil
		}
	}
	return s.legacyTrackerIDs(ticket)
}

// legacyTrackerIDs reads ticket's ids from the record in its own folder.
func (s *Store) legacyTrackerIDs(ticket string) (TrackerIDs, error) {
	path := s.githubRecordPath(ticket)
	data, err := readFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return TrackerIDs{}, nil
		}
		return TrackerIDs{}, err
	}
	var rec githubRecordIDs
	if err := yaml.Unmarshal(data, &rec); err != nil {
		return TrackerIDs{}, &axi.Error{
			Msg:  fmt.Sprintf("%s could not be decoded: %v", path, err),
			Code: "TRACKER_RECORD_INVALID",
			Help: []string{fmt.Sprintf("The GitHub mirror writes %s; restore it from the store's history, or remove it and let the next sync adopt the ticket's issue again", path)},
		}
	}
	if rec.Issue < 1 {
		return TrackerIDs{}, nil
	}
	return TrackerIDs{GitHub: &GitHubIssue{Repo: rec.Repo, Number: rec.Issue}}, nil
}

// branchID is the id a branch for ticket leads with when from names the
// tracker that names branches: the issue number when from is GitHub (or
// unnamed, which is GitHub: the first tracker jig mirrors to) and the
// ticket is mirrored there, else the ticket's own store id. A ticket no
// tracker has mirrored yet, and a project whose branches are led by another
// tracker's id, both lead with the store id.
func (s *Store) branchID(ticket string) (string, error) {
	if !branchesFromGitHub(s.BranchesFrom) {
		return ticket, nil
	}
	ids, err := s.TrackerIDs(ticket)
	if err != nil {
		return "", err
	}
	if ids.GitHub == nil {
		return ticket, nil
	}
	return strconv.Itoa(ids.GitHub.Number), nil
}

// BranchesLedByGitHub reports whether the project's branches are led by
// GitHub's issue numbers (once a ticket has one): its jig.yaml names GitHub as
// the tracker that names branches, or names none. A project that names another
// tracker has its branches led by the store's ids, and the issue has to say
// which branch is the ticket's (internal/mirror's footer).
func (s *Store) BranchesLedByGitHub() bool { return branchesFromGitHub(s.BranchesFrom) }

// branchesFromGitHub reports whether from, a jig.yaml branches: from: value,
// leaves branch naming to GitHub's issue numbers: GitHub itself, or nothing.
func branchesFromGitHub(from string) bool {
	from = strings.TrimSpace(from)
	return from == "" || strings.EqualFold(from, "github")
}

// remoteLeads maps every id a tracker gives a ticket, spelled the way a
// branch name leads with it, to the tickets that carry it. Today that is a
// GitHub issue number; a tracker whose ids are KEY-N keys would add them to
// the same map. It reads the tracker tree afresh on each call: a record may
// have landed in it since the index was last built, and a branch the issue
// number just made must bind to its ticket at once. A ticket with no record in
// the tree is read from the old place (TrackerIDs).
//
// A ticket whose record cannot be read or decoded costs only its own claim,
// as with aliasClaims: it is that ticket's problem, and naming some other
// ticket's branch must not fail on it.
func (s *Store) remoteLeads() (map[string][]string, error) {
	ids, err := s.TicketIDs()
	if err != nil {
		return nil, err
	}
	s.invalidateTrackerIndex()
	recs, err := s.TrackerRecords()
	if err != nil {
		return nil, err
	}
	leads := map[string][]string{}
	add := func(lead, ticket string) {
		for _, have := range leads[lead] {
			if have == ticket {
				return
			}
		}
		leads[lead] = append(leads[lead], ticket)
	}
	known := make(map[string]bool, len(ids))
	for _, id := range ids {
		known[id] = true
	}
	inTree := map[string]bool{}
	for _, rec := range recs {
		if rec.Err != nil || rec.Entry.Provider != ProviderGitHub || !known[rec.Ticket] || rec.Chart != "" {
			continue
		}
		if _, issue := githubIdentity(rec.Fields); issue >= 1 {
			add(strconv.Itoa(issue), rec.Ticket)
			inTree[rec.Ticket] = true
		}
	}
	for _, id := range ids {
		if inTree[id] {
			continue
		}
		t, err := s.legacyTrackerIDs(id)
		if err != nil || t.GitHub == nil {
			continue
		}
		add(strconv.Itoa(t.GitHub.Number), id)
	}
	return leads, nil
}

// remoteLeadCollisionError refuses a lead that two or more tickets carry in
// their trackers: tickets mirrored to same-numbered issues of different
// repositories.
func remoteLeadCollisionError(lead string, owners []string) *axi.Error {
	sorted := append([]string(nil), owners...)
	sort.Strings(sorted)
	return &axi.Error{
		Msg:  fmt.Sprintf("%q is the tracker id of more than one ticket: %s", lead, strings.Join(sorted, ", ")),
		Code: "TICKET_REMOTE_ID_COLLISION",
		Help: []string{fmt.Sprintf("Name the branch with the ticket's own id (%s-<slug>) so it binds to one ticket", sorted[0])},
	}
}
