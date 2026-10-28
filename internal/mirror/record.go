package mirror

import (
	"fmt"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/develdeco/jig/internal/store"
)

// recordHeader is the comment line every record file (ticket or chart)
// starts with, saying the mirror wrote it (brief.md#Records).
const recordHeader = "# This file is written by jig's GitHub mirror; edits here are overwritten.\n"

// githubRecord is the GitHub mirror's part of a record of the tracker tree,
// trackers/github/<owner>/<repo>/<number>.yaml, in the bridge's own shape so
// its records are adopted as they are. The tree owns the ticket: or chart: key
// beside these keys (internal/store); every key below is the mirror's. The
// rest of synced (title, body_sha256, state, status, store_id) is the drift
// slice's own, left for it to add.
type githubRecord struct {
	Repo   string `yaml:"repo"`
	Issue  int    `yaml:"issue"`
	NodeID string `yaml:"node_id"`
	// Item is the project item id this record's own issue was placed on
	// (brief.md#The board: "the record keeps the item id in item").
	Item string `yaml:"item,omitempty"`
	// PRs is, for a ticket's record only, each open or merged pull request
	// FindPullRequests last turned up, placed on the project
	// (brief.md#The board: "the record keeps each in prs:").
	PRs    []prRecord    `yaml:"prs,omitempty"`
	Synced *syncedFields `yaml:"synced,omitempty"`
}

// prRecord is one entry of githubRecord's prs: list (brief.md#Records).
type prRecord struct {
	Repo   string `yaml:"repo"`
	Number int    `yaml:"number"`
	NodeID string `yaml:"node_id"`
	Item   string `yaml:"item,omitempty"`
}

// syncedFields is githubRecord's synced: key: what the last sync itself
// wrote to every jig-owned field, under the bridge's own names
// (brief.md#Ownership and drift), so the next sync can tell an edit made on
// GitHub (drift, overwritten) from a change made in the store (applied with
// no drift line, since GitHub still matches what was last written).
type syncedFields struct {
	// Title is the issue title the last sync wrote.
	Title string `yaml:"title,omitempty"`
	// BodySHA256 is the sha256 (hex) of the issue body the last sync wrote,
	// in the form bodies compare in (brief.md#What an issue shows: "line
	// endings become \n and outer whitespace is trimmed").
	BodySHA256 string `yaml:"body_sha256,omitempty"`
	// State is "OPEN" or "CLOSED", the issue's own open/closed state the last
	// sync wrote.
	State string `yaml:"state,omitempty"`
	// Status is the board item's Status option name the last sync wrote.
	Status string `yaml:"status,omitempty"`
	// StoreID is the board item's Store ID text value the last sync wrote.
	StoreID string      `yaml:"store_id,omitempty"`
	Links   syncedLinks `yaml:"links,omitempty"`
}

// syncedLinks is synced.links: the node ids of the parent and blocked-by
// links this sync last wrote (brief.md#Links), so the next sync can tell
// what it itself wrote from an edit made on GitHub.
type syncedLinks struct {
	// Parent is the chart issue's node id this ticket is linked under, or ""
	// when the ticket has no chart (and so no parent link to write).
	Parent string `yaml:"parent,omitempty"`
	// BlockedBy is, in blocked_by order, the node id of each blocker's issue
	// this sync has linked; a blocker with no issue is skipped, so this can
	// be shorter than the ticket's own blocked_by list.
	BlockedBy []string `yaml:"blocked_by,omitempty"`
}

// recordRef is where one item's record sits. An item holds it by pointer, so a
// record created, moved or recreated partway through a sync is the record
// every later step of the sync finds.
type recordRef struct {
	// entry is the record's place in the tracker tree, when inTree.
	entry  store.TrackerEntry
	inTree bool
	// legacy is the record still in the item's own folder, where A6's mirror
	// first kept it. A sync moves it into the tree before it does anything
	// else; a dry run, which writes nothing, reads it from there.
	legacy *store.LegacyTrackerRecord
}

// has reports whether the item has a record: jig finds issues by their
// records only (brief.md#Adoption), never by searching GitHub for a marker,
// so an item with one is left alone and one with none gets a new issue.
func (r *recordRef) has() bool { return r.inTree || r.legacy != nil }

// githubEntry returns the tree entry of an issue:
// trackers/github/<owner>/<repo>/<number>. GitHub's owner and repository
// names are case-insensitive, so the folder is their lower-case spelling, the
// same on a file system that folds case and one that does not.
func githubEntry(repo string, issue int) store.TrackerEntry {
	return store.TrackerEntry{Provider: store.ProviderGitHub, Scope: strings.ToLower(repo), ID: strconv.Itoa(issue)}
}

// findRecord locates owner's GitHub record: in the tree, in the folder of the
// repository the sync is configured for (repo) if the owner has a record
// there, else the first it has in another (doSync refuses a record that is
// not repo's), else the one still in the owner's own folder.
func findRecord(st *store.Store, owner store.TrackerOwner, repo string) (*recordRef, error) {
	recs, err := st.TrackerRecordsFor(owner)
	if err != nil {
		return nil, err
	}
	var other *store.TrackerEntry
	for _, rec := range recs {
		if rec.Entry.Provider != store.ProviderGitHub {
			continue
		}
		if strings.EqualFold(rec.Entry.Scope, repo) {
			return &recordRef{entry: rec.Entry, inTree: true}, nil
		}
		if other == nil {
			e := rec.Entry
			other = &e
		}
	}
	if other != nil {
		return &recordRef{entry: *other, inTree: true}, nil
	}
	legacy, ok, err := st.LegacyTrackerRecord(owner)
	if err != nil {
		return nil, err
	}
	if ok {
		return &recordRef{legacy: &legacy}, nil
	}
	return &recordRef{}, nil
}

// decodeRecord reads the mirror's keys out of a record's mapping.
func decodeRecord(fields yaml.Node) (githubRecord, error) {
	var rec githubRecord
	if fields.Kind == 0 {
		return rec, nil
	}
	if err := fields.Decode(&rec); err != nil {
		return githubRecord{}, fmt.Errorf("mirror: decode record: %w", err)
	}
	return rec, nil
}

// readRecord decodes the record of item it: from the tree, or from the item's
// own folder for one not moved yet. Claim's write calls this after a rejected
// push's pull
// to see whether the record it just brought in names another clone's issue for
// the same ticket or chart (brief.md#Syncing at every checkpoint).
func readRecord(st *store.Store, it item) (githubRecord, error) {
	if it.ref.legacy != nil {
		fields, err := st.ReadLegacyTrackerRecord(*it.ref.legacy)
		if err != nil {
			return githubRecord{}, fmt.Errorf("mirror: read record: %w", err)
		}
		return decodeRecord(fields)
	}
	recs, err := st.TrackerRecordsFor(it.owner())
	if err != nil {
		return githubRecord{}, fmt.Errorf("mirror: read record: %w", err)
	}
	for _, rec := range recs {
		if rec.Entry == it.ref.entry {
			return decodeRecord(rec.Fields)
		}
	}
	return githubRecord{}, fmt.Errorf("mirror: read record: %s has no record at %s", it.what(), it.ref.entry)
}

// writeRecord writes rec as the record of item it in the tree, with its
// leading "the mirror wrote this" comment. A record whose issue is not the one
// its file is named for (an issue opened again, or a record kept under the
// wrong name) is written under the name of the issue it holds, and the old
// file and link go.
func writeRecord(st *store.Store, it item, rec githubRecord) error {
	entry := it.ref.entry
	if rec.Issue >= 1 && rec.Repo != "" {
		entry = githubEntry(rec.Repo, rec.Issue)
		if it.ref.inTree && strings.EqualFold(entry.String(), it.ref.entry.String()) {
			// The same place, spelled in another case: on a file system that
			// folds case it is one file, which a move would delete.
			entry = it.ref.entry
		}
	}
	var fields yaml.Node
	if err := fields.Encode(rec); err != nil {
		return fmt.Errorf("mirror: marshal record: %w", err)
	}
	if err := st.WriteTrackerRecord(entry, it.owner(), &fields, recordHeader); err != nil {
		return err
	}
	if it.ref.inTree && it.ref.entry != entry {
		if err := st.RemoveTrackerRecord(it.ref.entry); err != nil {
			return err
		}
	}
	if it.ref.legacy != nil {
		if err := st.DropLegacyTrackerRecord(*it.ref.legacy); err != nil {
			return err
		}
		it.ref.legacy = nil
	}
	it.ref.entry, it.ref.inTree = entry, true
	return nil
}

// writeRecordLinks updates item it's record's synced.links to links, leaving its
// repo, issue and node id untouched, and writes it back (brief.md#Links: "the
// record keeps the node ids of the links it last wrote"). The write lands in
// the working tree only; the checkpoint that ran the sync commits and pushes
// it once the sync is done (Store.RunCheckpointHook), as it does every record
// change but a creation, which claimIssue lands itself (brief.md#Syncing at
// every checkpoint).
func writeRecordLinks(st *store.Store, it item, links syncedLinks) error {
	rec, err := readRecord(st, it)
	if err != nil {
		return err
	}
	if rec.Synced == nil {
		rec.Synced = &syncedFields{}
	}
	rec.Synced.Links = links
	return writeRecord(st, it, rec)
}

// githubRecordOf returns owner's GitHub record, wherever it sits, for a step
// that reads a record the sync is not working through (the "Waits for" line
// names a blocker's issue): the first the tree has, else the one in the
// owner's own folder; ok is false when the owner has none.
func githubRecordOf(st *store.Store, owner store.TrackerOwner) (rec githubRecord, ok bool, err error) {
	ref, err := findRecord(st, owner, "")
	if err != nil || !ref.has() {
		return githubRecord{}, false, err
	}
	it := item{kind: "ticket", id: owner.Ticket, ref: ref}
	if owner.Chart != "" {
		it = item{kind: "chart", id: owner.Chart, ref: ref}
	}
	rec, err = readRecord(st, it)
	return rec, err == nil, err
}
