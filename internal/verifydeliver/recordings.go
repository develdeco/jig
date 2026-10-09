package verifydeliver

// The reviewer reads the build's recordings as evidence (ADR 0029, step 5b).
// The build's end-to-end scenarios recorded themselves into the jig home at
// each builder's green, and the journal's "recorded" lines say what they
// wrote. At a reviewer round, the recordings made at the reviewed head or an
// ancestor of it are listed in a recordings.json under the jig home, and the
// review prompt names that file, so the reviewer can read how the change
// behaves end to end. The selection is the one publish makes for its pick
// (recordedCandidates, then verifiedCandidates: the latest recording of each
// flow, scenario and step, each checked again against its file); this file
// holds the part that is the reviewer's own: the order, the bound, the wire
// shape, the write, and keeping the machine's paths out of what the reviewer
// writes back.

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/develdeco/jig/internal/home"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/media"
	"github.com/develdeco/jig/internal/screen"
)

const (
	// maxReviewRecordings bounds how many recordings one round lists for its
	// reviewer, and so how many files it checks and hashes. A reviewer reads
	// the ones that bear on the change, so a list longer than this is a shelf
	// to look along, not evidence: the latest are listed and the rest counted.
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
// recordings are the latest first. Omitted counts the recordings of the
// head's history left out of the list because it is cut at
// maxReviewRecordings or because the reviewer's read tools would be refused
// the file; it is absent when there were none. A recording that no longer
// matches its journal line is not evidence and is not counted.
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
// journal outside the lease, so no grant is added for them. The same screen
// refuses a file whose name looks like a credential (".env*", "*_key*"), so
// such a recording is left out of the list and counted in Omitted rather than
// listed as evidence the reviewer cannot read.
//
// Only the first maxReviewRecordings (latest first) are checked against their
// files, so a round never hashes more than that many.
func writeReviewRecordings(in RoundInput, head string) (string, error) {
	if in.Home == "" {
		return "", nil
	}
	lines, err := journal.Read(in.Store, in.Ticket)
	if err != nil {
		return "", fmt.Errorf("verifydeliver: review: read the journal for recordings: %w", err)
	}
	// The selection reads a Deps' Home and Store and nothing else of it.
	d := Deps{Store: in.Store, Home: in.Home}
	all, err := recordedCandidates(d, lines, in.LeaseDir, head)
	if err != nil {
		return "", fmt.Errorf("verifydeliver: review: list the build's recordings: %w", err)
	}
	if len(all) == 0 {
		return "", nil
	}
	storeID, err := in.Store.ID()
	if err != nil {
		return "", fmt.Errorf("verifydeliver: review: resolve the store id: %w", err)
	}

	// Latest first: the order of the journal lines the recordings came from.
	// A stable sort keeps the candidates' own order (flow, step, scenario)
	// within one line, which reads as the build's scenarios do.
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
	sort.SliceStable(all, func(i, j int) bool {
		a, b := all[i], all[j]
		return at[origin{a.Commit, a.run, a.rec.File}] > at[origin{b.Commit, b.run, b.rec.File}]
	})

	list := ReviewRecordings{Ticket: in.Ticket, HeadSHA: head}
	var readable []pickCandidate
	for _, c := range all {
		if file, err := c.path(d, storeID, in.Ticket); err == nil && !readByReviewer(file) {
			list.Omitted++
			continue
		}
		readable = append(readable, c)
	}
	if len(readable) > maxReviewRecordings {
		list.Omitted += len(readable) - maxReviewRecordings
		readable = readable[:maxReviewRecordings]
	}
	cands, _, err := verifiedCandidates(d, in.Ticket, readable)
	if err != nil {
		return "", fmt.Errorf("verifydeliver: review: check the build's recordings: %w", err)
	}
	if len(cands) == 0 {
		return "", nil
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

// readByReviewer reports whether the screen that governs a headless reviewer
// session would let it Read file: the same decision the session's hook makes.
func readByReviewer(file string) bool {
	_, ok := screen.ToolCall("Read", map[string]any{"file_path": file})
	return ok
}

// reviewerHostDirs are the directories of this machine that a reviewer's
// words, which reach the store, must not name: the lease it works in, the jig
// home (the recordings and everything else jig keeps there) and the store.
func reviewerHostDirs(in RoundInput) []hostDir {
	dirs := []hostDir{{in.LeaseDir, "<lease>"}, {in.Home, "<jig home>"}}
	if in.Store != nil {
		dirs = append(dirs, hostDir{in.Store.Root, "<store>"})
	}
	return dirs
}

// leaveOutSessionHostPaths is leaveOutHostPaths for text a session wrote: it
// also replaces the WSL mount spelling of each directory, the one herdr hands
// a session on Windows and so the one it may repeat.
func leaveOutSessionHostPaths(s string, dirs ...hostDir) string {
	for _, sp := range hostPathSpellings(true, dirs...) {
		s = strings.ReplaceAll(s, sp.text, sp.name)
	}
	return s
}

// scrubReviewResult returns r with every directory of this machine in dirs
// left out of the words the reviewer wrote that jig commits to the store: each
// finding's title, detail and risk rationale, and the summary. A finding that
// cites a recording by its path (the prompt asks for its scenario, step and
// commit instead) would otherwise commit the operator's jig home. The paths
// the result names as files are jig's to check, not text, and are left as they
// are: a finding's file and reviewed_paths are relativized or refused as
// before.
func scrubReviewResult(r ReviewResult, dirs []hostDir) ReviewResult {
	scrub := func(s string) string { return leaveOutSessionHostPaths(s, dirs...) }
	out := r
	if r.Findings != nil {
		out.Findings = make([]ResultFinding, len(r.Findings))
		for i, f := range r.Findings {
			f.Title, f.Detail, f.RiskRationale = scrub(f.Title), scrub(f.Detail), scrub(f.RiskRationale)
			out.Findings[i] = f
		}
	}
	out.Summary = scrub(r.Summary)
	return out
}

// scrubReviewResultFile rewrites the reviewer's result.json at path, whose
// bytes are data, with the directories of this machine left out, when it names
// any: the file sits in the store's work directory, which is committed. Nothing
// reads it back after the round parsed it.
func scrubReviewResultFile(path string, data []byte, dirs []hostDir) error {
	scrubbed := leaveOutSessionHostPaths(string(data), dirs...)
	if scrubbed == string(data) {
		return nil
	}
	if err := os.WriteFile(path, []byte(scrubbed), 0o644); err != nil {
		return fmt.Errorf("verifydeliver: review: rewrite result.json without host paths: %w", err)
	}
	return nil
}
