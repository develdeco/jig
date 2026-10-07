package verifydeliver

// The reviewer reads the build's recordings as evidence (ADR 0029, step 5b).
// The build's end-to-end scenarios recorded themselves into the jig home at
// each builder's green, and the journal's "recorded" lines say what they
// wrote. At a reviewer round, the recordings made at the reviewed head or an
// ancestor of it are listed in a recordings.json under the jig home, and the
// review prompt names that file, so the reviewer can read how the change
// behaves end to end. The selection is the one publish makes for its pick
// (pickCandidates: the latest recording of each flow, scenario and step, each
// checked again against its file); this file holds the part that is the
// reviewer's own: the order, the bound, the wire shape and the write.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/develdeco/jig/internal/home"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/media"
)

const (
	// maxReviewRecordings bounds how many recordings one round lists for its
	// reviewer. A reviewer reads the ones that bear on the change, so a list
	// longer than this is a shelf to look along, not evidence: the latest are
	// listed and the rest counted.
	maxReviewRecordings = 50

	// reviewRecordingsFile is the name of the list in the round's directory
	// under the jig home (home.ReviewDir).
	reviewRecordingsFile = "recordings.json"
)

// ReviewRecording is one entry of recordings.json: a recording of one of the
// build's end-to-end scenarios, with what its scenario tagged it with, the
// commit it was recorded at, whether it is an image or a video, and the
// absolute path of its file, which jig checked against the hash and size its
// journal line recorded.
type ReviewRecording struct {
	Scenario string `json:"scenario"`
	Flow     string `json:"flow,omitempty"`
	Step     int    `json:"step,omitempty"`
	Caption  string `json:"caption,omitempty"`
	Commit   string `json:"commit"`
	Kind     string `json:"kind"`
	File     string `json:"file"`
}

// ReviewRecordings is recordings.json's exact wire shape, the list jig writes
// for one gate reviewer round whose head's history holds recordings. The
// recordings are the latest first. Omitted counts the older ones left out when
// there are more than maxReviewRecordings, and is absent when none were.
type ReviewRecordings struct {
	Ticket     string            `json:"ticket"`
	HeadSHA    string            `json:"head_sha"`
	Recordings []ReviewRecording `json:"recordings"`
	Omitted    int               `json:"omitted,omitempty"`
}

// writeReviewRecordings writes the round's recordings.json under the jig home
// and returns its absolute path, or "" when there is nothing to hand the
// reviewer: no jig home, or no recording made at head or an ancestor of it
// that is still the file its journal line describes. A round with nothing to
// hand over writes nothing, so its input and prompt are what they were before
// recordings existed.
//
// The list sits under the jig home, never in the store (home.ReviewDir): it
// names files of this machine by absolute path, and the store is shared and
// pushed. The reviewer reads the files by those paths; a headless session's
// Read, Glob and Grep reach any path the screen does not name a credential
// location (ADR 0008), as they already reach review.json, the intent and the
// journal outside the lease, so no grant is added for them.
func writeReviewRecordings(in RoundInput, head string) (string, error) {
	if in.Home == "" {
		return "", nil
	}
	lines, err := journal.Read(in.Store, in.Ticket)
	if err != nil {
		return "", fmt.Errorf("verifydeliver: review: read the journal for recordings: %w", err)
	}
	// pickCandidates reads a Deps' Home and Store and nothing else of it.
	d := Deps{Store: in.Store, Home: in.Home}
	cands, _, err := pickCandidates(d, in.Ticket, lines, in.LeaseDir, head)
	if err != nil {
		return "", fmt.Errorf("verifydeliver: review: list the build's recordings: %w", err)
	}
	if len(cands) == 0 {
		return "", nil
	}

	// Latest first: the order of the journal lines the recordings came from.
	// A stable sort keeps the candidates' own order (flow, step, scenario)
	// within one line.
	type origin struct{ commit, run, file string }
	at := map[origin]int{}
	for i, l := range lines {
		if l.Event != "recorded" {
			continue
		}
		for _, r := range l.Recordings {
			at[origin{l.Commit, l.RecordRun, r.File}] = i
		}
	}
	sort.SliceStable(cands, func(i, j int) bool {
		a, b := cands[i], cands[j]
		return at[origin{a.Commit, a.run, a.rec.File}] > at[origin{b.Commit, b.run, b.rec.File}]
	})
	list := ReviewRecordings{Ticket: in.Ticket, HeadSHA: head}
	if len(cands) > maxReviewRecordings {
		list.Omitted = len(cands) - maxReviewRecordings
		cands = cands[:maxReviewRecordings]
	}

	storeID, err := in.Store.ID()
	if err != nil {
		return "", fmt.Errorf("verifydeliver: review: resolve the store id: %w", err)
	}
	for _, c := range cands {
		file, err := c.path(d, storeID, in.Ticket)
		if err != nil {
			return "", fmt.Errorf("verifydeliver: review: locate a recording: %w", err)
		}
		list.Recordings = append(list.Recordings, ReviewRecording{
			Scenario: c.Scenario, Flow: c.Flow, Step: c.Step, Caption: c.Caption,
			Commit: c.Commit, Kind: c.Kind, File: file,
		})
	}

	dir, err := home.ReviewDir(in.Home, storeID, in.Ticket, in.Round)
	if err != nil {
		return "", fmt.Errorf("verifydeliver: review: locate the round's directory: %w", err)
	}
	path := absPath(filepath.Join(dir, reviewRecordingsFile))
	if err := media.PlainParents(absPath(filepath.Join(in.Home, "evidence")), path); err != nil {
		return "", fmt.Errorf("verifydeliver: review: write recordings.json: %w", err)
	}
	data, err := json.MarshalIndent(list, "", "  ")
	if err != nil {
		return "", fmt.Errorf("verifydeliver: review: marshal recordings.json: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return "", fmt.Errorf("verifydeliver: review: create the round's directory: %w", err)
	}
	// An earlier attempt at this round may have left a list, or a link in its
	// place; neither is written through or read back as this attempt's.
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return "", fmt.Errorf("verifydeliver: review: clear an earlier recordings.json: %w", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return "", fmt.Errorf("verifydeliver: review: write recordings.json: %w", err)
	}
	return path, nil
}
