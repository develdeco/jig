package frontier

// Recording the build's end-to-end scenarios (ADR 0029, step 3). A builder's
// green oracle run is the one time the scenarios run, so it is where they
// record: jig gives that run a directory under the jig home in the
// JIG_RECORD_DIR environment variable, the scenarios write their recordings
// there, and a passing run's directory is verified and journaled with the
// commit the oracle ran at. Recording is best effort, never a reason for a
// slice to fail: a directory that cannot be made means the oracle runs
// without one, and a refused collection is a journaled line, not a red run.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/develdeco/jig/internal/home"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/media"
)

const (
	// recordDirEnv names the variable that gives an oracle run its recording
	// directory. A run that does not have it set does not record. The gate's
	// own oracle run never sets it.
	recordDirEnv = "JIG_RECORD_DIR"

	// maxTagBytes bounds a tag file: a few short fields, never a document.
	maxTagBytes = 64 << 10

	// maxDroppedNames bounds how many dropped entries a recorded line names.
	maxDroppedNames = 5
)

// recordTarget is the directory one oracle run records into, as jig made it.
// The zero value is a run that does not record.
type recordTarget struct {
	dir  string
	made os.FileInfo
}

// env is the environment the oracle run is given: the recording directory, or
// nothing for a run that does not record.
func (t recordTarget) env() []string {
	if t.dir == "" {
		return nil
	}
	return []string{recordDirEnv + "=" + t.dir}
}

// startRecording makes the empty directory the oracle run at commit records
// into: <jig home>/evidence/<store id>/<ticket>/recordings/<commit>. It
// returns the zero target, and the oracle runs without recording, when there
// is no jig home or no clean commit to key the recordings by, or when the
// directory cannot be made plain and fresh. A directory from an earlier run
// at the same commit is cleared first, so what is collected is only this
// run's.
func (rc *runCtx) startRecording(commit string) recordTarget {
	if rc.d.Home == "" || commit == "" {
		return recordTarget{}
	}
	id, err := rc.d.Store.ID()
	if err != nil {
		return recordTarget{}
	}
	dir, err := home.RecordDir(rc.d.Home, id, rc.ticket, commit)
	if err != nil {
		return recordTarget{}
	}
	// Absolute, since the oracle runs in the lease and hands the path to
	// scenarios that run from wherever they like.
	dir = absPath(dir)
	if media.PlainParents(absPath(filepath.Join(rc.d.Home, "evidence")), dir) != nil {
		return recordTarget{}
	}
	// RemoveAll does not follow a link, so one left at dir is removed as itself.
	if os.RemoveAll(dir) != nil || os.MkdirAll(dir, 0o755) != nil {
		return recordTarget{}
	}
	made, err := media.LstatPinned(dir)
	if err != nil {
		_ = os.RemoveAll(dir)
		return recordTarget{}
	}
	return recordTarget{dir: dir, made: made}
}

// finishRecording settles t after the oracle run at commit. A failed run's
// recordings are discarded, since only a green scenario is evidence. A passing
// run's directory is collected and, when it held anything, journaled as one
// "recorded" line keyed by commit: with the verified recordings, or with the
// reason the whole collection was refused (and the directory removed). A
// directory that held nothing is removed. It runs before oracleAtGreen
// returns green, so the line is there before the slice is marked green.
func (rc *runCtx) finishRecording(sliceID string, attempt int, commit string, t recordTarget, passed bool) {
	if t.dir == "" {
		return
	}
	if !passed {
		_ = os.RemoveAll(t.dir)
		return
	}
	line := journal.Line{Slice: sliceID, Event: "recorded", Commit: commit, Attempt: attempt}
	recs, dropped, err := collectRecordings(t.dir, t.made)
	switch {
	case err != nil:
		_ = os.RemoveAll(t.dir)
		line.Outcome = "refused: " + leaveOutHostPaths(err.Error(), t.dir, absPath(rc.d.Home))
	case len(recs) == 0 && len(dropped) == 0:
		_ = os.Remove(t.dir)
		return
	default:
		line.Recordings = recs
		if len(dropped) > 0 {
			line.Outcome = "dropped: " + nameDropped(dropped)
		}
	}
	rc.journal(line)
}

// recordingTag is the optional <file>.json beside a recording: how its writer
// tags it. Every field is optional.
type recordingTag struct {
	Scenario string `json:"scenario"`
	Flow     string `json:"flow"`
	Step     int    `json:"step"`
	Caption  string `json:"caption"`
}

// collectRecordings reads dir, a recording directory jig made (made), and
// returns the recordings in it in name order, and the names of the entries it
// left unrecorded. Only the files directly in dir count. A regular file whose
// extension media.Kind accepts is a recording, and a regular <file>.json
// beside it is its tag; any other entry, a subdirectory or a link or a tag
// whose recording is missing included, is dropped, and left where it is. The
// recordings are held to the rules of a gate demo's media (media.Verify):
// their types, sizes and count, no link, and the directory itself the one jig
// made. An unreadable or invalid tag, or a recording that fails a rule,
// refuses the whole collection with a reason naming the file.
func collectRecordings(dir string, made os.FileInfo) (recs []journal.Recording, dropped []string, err error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, nil, fmt.Errorf("%s cannot be read: %w", recordDirEnv, withoutPath(err))
	}
	regular := map[string]bool{}
	for _, e := range entries {
		if e.Type().IsRegular() {
			regular[e.Name()] = true
		}
	}

	type found struct {
		name string
		tag  recordingTag
	}
	var items []found
	taken := map[string]bool{}
	for _, e := range entries {
		name := e.Name()
		ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(name), "."))
		if !regular[name] || media.Kind(ext) == "" {
			continue
		}
		taken[name] = true
		f := found{name: name}
		if tagName := name + ".json"; regular[tagName] {
			taken[tagName] = true
			if f.tag, err = readTag(dir, tagName); err != nil {
				return nil, nil, err
			}
		}
		items = append(items, f)
	}
	for _, e := range entries {
		if !taken[e.Name()] {
			dropped = append(dropped, e.Name())
		}
	}
	if len(items) == 0 {
		return nil, dropped, nil
	}

	listed := make([]media.Listed, len(items))
	for i, it := range items {
		listed[i] = media.Listed{File: it.name, Caption: it.tag.Caption}
	}
	files, err := media.Verify(dir, recordDirEnv, made, listed)
	if err != nil {
		return nil, nil, err
	}
	recs = make([]journal.Recording, len(files))
	for i, f := range files {
		tag := items[i].tag
		scenario := tag.Scenario
		if scenario == "" {
			scenario = strings.TrimSuffix(f.Name, filepath.Ext(f.Name))
		}
		recs[i] = journal.Recording{File: f.Name, SHA256: f.SHA256, Size: f.Size, Scenario: scenario, Flow: tag.Flow, Step: tag.Step, Caption: tag.Caption}
	}
	return recs, dropped, nil
}

// readTag reads and parses the tag file name in dir, strictly: an unknown
// field, a value of the wrong type, a second document or a negative step
// is invalid. The file is read through the handle that was checked to be the
// regular file listed, so a swap since the listing is refused, not followed.
func readTag(dir, name string) (recordingTag, error) {
	var tag recordingTag
	path := filepath.Join(dir, name)
	info, err := media.LstatPinned(path)
	if err != nil {
		return tag, fmt.Errorf("tag %q cannot be read: %w", name, withoutPath(err))
	}
	if !info.Mode().IsRegular() {
		return tag, fmt.Errorf("tag %q is not a regular file", name)
	}
	if info.Size() > maxTagBytes {
		return tag, fmt.Errorf("tag %q is %d bytes; at most %d are accepted", name, info.Size(), maxTagBytes)
	}
	f, err := os.Open(path)
	if err != nil {
		return tag, fmt.Errorf("tag %q cannot be read: %w", name, withoutPath(err))
	}
	defer f.Close()
	if opened, err := f.Stat(); err != nil || !os.SameFile(info, opened) {
		return tag, fmt.Errorf("tag %q changed while it was being read", name)
	}
	data, err := io.ReadAll(io.LimitReader(f, maxTagBytes+1))
	if err != nil {
		return tag, fmt.Errorf("tag %q cannot be read: %w", name, withoutPath(err))
	}
	if len(data) > maxTagBytes {
		return tag, fmt.Errorf("tag %q is larger than the %d bytes accepted", name, maxTagBytes)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&tag); err != nil {
		return recordingTag{}, fmt.Errorf("tag %q is not valid: %v", name, err)
	}
	if _, err := dec.Token(); err != io.EOF {
		return recordingTag{}, fmt.Errorf("tag %q is not valid: it holds more than one JSON value", name)
	}
	if tag.Step < 0 {
		return recordingTag{}, fmt.Errorf("tag %q is not valid: step %d is negative", name, tag.Step)
	}
	return tag, nil
}

// nameDropped lists the first few dropped entries and counts the rest.
func nameDropped(names []string) string {
	if len(names) <= maxDroppedNames {
		return strings.Join(names, ", ")
	}
	return fmt.Sprintf("%s (and %d more)", strings.Join(names[:maxDroppedNames], ", "), len(names)-maxDroppedNames)
}

// withoutPath is err with the path an operating system error names left out:
// a refusal is committed to the store, and the jig home holds the operator's
// user name.
func withoutPath(err error) error {
	var pe *fs.PathError
	if errors.As(err, &pe) {
		return pe.Err
	}
	return err
}

// leaveOutHostPaths replaces the directories jig chose, in either slash
// spelling, with a name for the role they play, so a refusal committed to the
// store does not carry a path of this machine. The recording directory goes
// first, being inside the jig home.
func leaveOutHostPaths(s, recordDir, jigHome string) string {
	for _, p := range []struct{ path, name string }{
		{recordDir, recordDirEnv},
		{jigHome, "<jig home>"},
	} {
		if p.path == "" {
			continue
		}
		s = strings.ReplaceAll(s, p.path, p.name)
		s = strings.ReplaceAll(s, filepath.ToSlash(p.path), p.name)
	}
	return s
}

// absPath is p as an absolute path, or p itself when that cannot be told.
func absPath(p string) string {
	if abs, err := filepath.Abs(p); err == nil {
		return abs
	}
	return p
}
