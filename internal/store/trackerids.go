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

// githubRecordFile is the name of the GitHub mirror's record of a ticket,
// <ticket>/tracker/github.yaml. The mirror (internal/mirror) writes it and
// owns its whole shape; the store reads only the two keys a branch name and
// a branch binding need, and so cannot import the mirror to ask.
const githubRecordFile = "github.yaml"

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

// githubRecordIDs is the part of tracker/github.yaml TrackerIDs reads. The
// decode is lenient: the rest of the record is the mirror's.
type githubRecordIDs struct {
	Repo  string `yaml:"repo"`
	Issue int    `yaml:"issue"`
}

// githubRecordPath is <ticket>/tracker/github.yaml.
func (s *Store) githubRecordPath(ticket string) string {
	return filepath.Join(s.TicketDir(ticket), "tracker", githubRecordFile)
}

// TrackerIDs reads ticket's ids in the trackers that mirror it, from the
// records under <ticket>/tracker/. A ticket with no record, or a record that
// names no issue yet, has none. A record that cannot be decoded is an
// *axi.Error, TRACKER_RECORD_INVALID: the mirror owns the file, so the
// remedy is the mirror's.
func (s *Store) TrackerIDs(ticket string) (TrackerIDs, error) {
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
// the same map. It reads every ticket's tracker records afresh on each call:
// the mirror writes them without going through the store, so a cache could
// hold a ticket back from the id its issue was just given.
//
// A ticket whose record cannot be read or decoded costs only its own claim,
// as with aliasClaims: it is that ticket's problem, and naming some other
// ticket's branch must not fail on it.
func (s *Store) remoteLeads() (map[string][]string, error) {
	ids, err := s.TicketIDs()
	if err != nil {
		return nil, err
	}
	leads := map[string][]string{}
	for _, id := range ids {
		t, err := s.TrackerIDs(id)
		if err != nil || t.GitHub == nil {
			continue
		}
		lead := strconv.Itoa(t.GitHub.Number)
		leads[lead] = append(leads[lead], id)
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
