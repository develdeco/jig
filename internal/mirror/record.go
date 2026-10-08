package mirror

import (
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"

	"github.com/develdeco/jig/internal/store"
)

// recordHeader is the comment line every record file (ticket or chart)
// starts with, saying the mirror wrote it (brief.md#Records).
const recordHeader = "# This file is written by jig's GitHub mirror; edits here are overwritten.\n"

// githubRecord is <ticket>/tracker/github.yaml or charts/<name>/github.yaml,
// in the bridge's own shape so its records are adopted as they are. The
// rest of synced (title, body_sha256, state, status, store_id) is the
// drift slice's own, left for it to add.
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

// ticketRecordPath returns <ticket>/tracker/github.yaml, relative to the
// store root (what Store.Claim's paths want) and absolute (what os reads
// and writes).
func ticketRecordPath(st *store.Store, ticket string) (rel, abs string) {
	abs = filepath.Join(st.TicketDir(ticket), "tracker", "github.yaml")
	rel = filepath.Join(ticket, "tracker", "github.yaml")
	return rel, abs
}

// chartRecordPath returns charts/<name>/github.yaml, relative and absolute.
func chartRecordPath(st *store.Store, name string) (rel, abs string) {
	abs = filepath.Join(st.Root, "charts", name, "github.yaml")
	rel = filepath.Join("charts", name, "github.yaml")
	return rel, abs
}

// hasRecord reports whether a record already sits at abs: jig finds issues
// by their records only (brief.md#Adoption), never by searching GitHub for
// a marker, so an item with one is left alone and one with none gets a new
// issue.
func hasRecord(abs string) (bool, error) {
	if _, err := os.Stat(abs); err == nil {
		return true, nil
	} else if os.IsNotExist(err) {
		return false, nil
	} else {
		return false, err
	}
}

// writeRecord marshals rec with its leading "the mirror wrote this" comment
// and writes it atomically to abs.
func writeRecord(abs string, rec githubRecord) error {
	data, err := yaml.Marshal(rec)
	if err != nil {
		return fmt.Errorf("mirror: marshal record: %w", err)
	}
	return store.AtomicWrite(abs, append([]byte(recordHeader), data...))
}

// readRecord decodes the record at abs: Claim's write calls this after a
// rejected push's pull to see whether the record it just brought in names
// another clone's issue for the same ticket or chart (brief.md#Syncing at
// every checkpoint).
func readRecord(abs string) (githubRecord, error) {
	data, err := os.ReadFile(abs)
	if err != nil {
		return githubRecord{}, fmt.Errorf("mirror: read record: %w", err)
	}
	var rec githubRecord
	if err := yaml.Unmarshal(data, &rec); err != nil {
		return githubRecord{}, fmt.Errorf("mirror: decode record: %w", err)
	}
	return rec, nil
}

// writeRecordLinks updates the record at abs's synced.links to links,
// leaving its repo, issue and node id untouched, and writes it back
// (brief.md#Links: "the record keeps the node ids of the links it last
// wrote"). The write lands in the working tree only; it goes in with the
// next checkpoint's commit, as every record change but a creation does
// (brief.md#Syncing at every checkpoint).
func writeRecordLinks(abs string, links syncedLinks) error {
	rec, err := readRecord(abs)
	if err != nil {
		return err
	}
	if rec.Synced == nil {
		rec.Synced = &syncedFields{}
	}
	rec.Synced.Links = links
	return writeRecord(abs, rec)
}
