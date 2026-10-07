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
	"unicode/utf8"

	"github.com/develdeco/jig/internal/envrun"
	"github.com/develdeco/jig/internal/home"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/media"
)

const (
	// recordDirEnv names the variable that gives an oracle run its recording
	// directory. A run that does not have it set does not record. The gate's
	// own oracle run never sets it, and no oracle run, env command or headless
	// session jig starts inherits it (envrun.RecordDirEnv).
	recordDirEnv = envrun.RecordDirEnv

	// maxTagBytes bounds a tag file: a few short fields, never a document.
	maxTagBytes = 64 << 10

	// The bounds on a tag's text, which reaches the journal and, later, a pull
	// request.
	maxScenarioBytes = 200
	maxFlowBytes     = 200
	maxCaptionBytes  = 1000

	// maxDroppedNames bounds how many dropped entries a recorded line names.
	maxDroppedNames = 5

	// maxOutcomeBytes bounds a recorded line's outcome text.
	maxOutcomeBytes = 500

	// maxRunTries bounds how many directory names startRecording tries for
	// one oracle run before it runs the oracle without recording.
	maxRunTries = 100
)

// recordTarget is the directory one oracle run records into, as jig made it.
// The zero value is a run that does not record.
type recordTarget struct {
	dir string // absolute path
	run string // the run's directory name below the commit's
}

// env is the environment the oracle run is given: the recording directory, or
// nothing for a run that does not record.
func (t recordTarget) env() []string {
	if t.dir == "" {
		return nil
	}
	return []string{recordDirEnv + "=" + t.dir}
}

// evidenceTop is the evidence directory of the jig home, which every record
// directory is below.
func (rc *runCtx) evidenceTop() string {
	return absPath(filepath.Join(rc.d.Home, "evidence"))
}

// startRecording makes the new, empty directory the oracle run of sliceID's
// attempt, fix turn fix, at commit records into:
// <jig home>/evidence/<store id>/<ticket>/recordings/<commit>/<run>. Every
// run has a directory of its own, and an existing one is never cleared: a
// later run at the same commit (a retry, a fix turn, another slice that built
// nothing) must not destroy files an earlier run's journal line describes, so
// a name already taken gets a numeric suffix. It returns the zero target, and
// the oracle runs without recording, when there is no jig home or no clean
// commit to key the recordings by, or when no plain directory can be made.
func (rc *runCtx) startRecording(sliceID string, attempt, fix int, commit string) recordTarget {
	if rc.d.Home == "" || commit == "" {
		return recordTarget{}
	}
	id, err := rc.d.Store.ID()
	if err != nil {
		return recordTarget{}
	}
	top := rc.evidenceTop()
	base := fmt.Sprintf("%s-a%d-f%d", sliceID, attempt, fix)
	for try := 1; try <= maxRunTries; try++ {
		run := base
		if try > 1 {
			run = fmt.Sprintf("%s-%d", base, try)
		}
		dir, err := home.RecordDir(rc.d.Home, id, rc.ticket, commit, run)
		if err != nil {
			return recordTarget{}
		}
		// Absolute, since the oracle runs in the lease and hands the path to
		// scenarios that run from wherever they like.
		dir = absPath(dir)
		// Nothing is made below a link or junction a session swapped in.
		if media.PlainParents(top, dir) != nil || os.MkdirAll(filepath.Dir(dir), 0o755) != nil {
			return recordTarget{}
		}
		switch err := os.Mkdir(dir, 0o755); {
		case err == nil:
			return recordTarget{dir: dir, run: run}
		case errors.Is(err, fs.ErrExist):
			continue
		default:
			return recordTarget{}
		}
	}
	return recordTarget{}
}

// finishRecording settles t after the oracle run at commit. A failed run's
// recordings are discarded, since only a green scenario is evidence, and
// nothing is journaled for it. A passing run's directory is collected and,
// when it held anything, journaled as one "recorded" line keyed by commit and
// the run: with the verified recordings, or with the reason the whole
// collection was refused (and the directory removed). A directory that held
// nothing is removed. Nothing under a directory above t.dir that has become a
// link or junction is read or removed, whatever the run's outcome: the
// directory is left for the operator, and for a passing run the refusal is
// journaled. It runs before oracleAtGreen returns green, so the line is there
// before the slice is marked green.
func (rc *runCtx) finishRecording(sliceID string, attempt int, commit string, t recordTarget, passed bool) {
	if t.dir == "" {
		return
	}
	top := rc.evidenceTop()
	if !passed {
		_ = removeRecordDir(top, t.dir)
		return
	}
	line := journal.Line{Slice: sliceID, Event: "recorded", Commit: commit, Attempt: attempt, RecordRun: t.run}
	refuse := func(err error) {
		line.Outcome = "refused: " + capText(leaveOutHostPaths(err.Error(), t.dir, absPath(rc.d.Home)), maxOutcomeBytes)
		rc.journal(line)
	}
	recs, dropped, err := collectRecordings(top, t.dir)
	switch {
	case err != nil:
		_ = removeRecordDir(top, t.dir)
		refuse(err)
		return
	case len(recs) == 0 && len(dropped) == 0:
		_ = os.Remove(t.dir)
		return
	}
	line.Recordings = recs
	if len(dropped) > 0 {
		line.Outcome = capText("dropped: "+nameDropped(dropped), maxOutcomeBytes)
	}
	rc.journal(line)
}

// removeRecordDir removes dir, a record directory, unless a directory above
// it has been swapped for a link or junction: RemoveAll below a swapped parent
// would delete whatever the link leads to.
func removeRecordDir(top, dir string) error {
	if err := media.PlainParents(top, dir); err != nil {
		return err
	}
	return os.RemoveAll(dir)
}

// recordingTag is the optional <file>.json beside a recording: how its writer
// tags it. Every field is optional.
type recordingTag struct {
	Scenario string `json:"scenario"`
	Flow     string `json:"flow"`
	Step     int    `json:"step"`
	Caption  string `json:"caption"`
}

// dirEntry is one entry of a record directory, as pickRecordings reads it.
type dirEntry struct {
	name    string
	regular bool // a regular file: not a directory, link or junction
}

// pick is one recording chosen from a directory and its tag file, if any.
type pick struct {
	name string
	tag  string // "" when the recording has no tag
}

// pickRecordings chooses the recordings among entries, which are in name
// order, and returns the names of the rest. A regular file whose extension
// media.Kind accepts and whose name is a single plain file name is a
// recording; of two names that differ only in case (one file on some systems,
// two on others) the first is, and the later is dropped. A regular
// <recording>.json beside a recording is its tag. Anything else, a
// subdirectory or a link or a tag whose recording is missing included, is
// dropped: it is not a recording and does not veto the ones that are.
func pickRecordings(entries []dirEntry) (picks []pick, dropped []string) {
	regular := map[string]bool{}
	for _, e := range entries {
		regular[e.name] = e.regular
	}
	taken := map[string]bool{}
	seen := map[string]bool{}
	for _, e := range entries {
		ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(e.name), "."))
		if !e.regular || media.Kind(ext) == "" || !media.PlainName(e.name) {
			continue
		}
		folded := strings.ToLower(e.name)
		if seen[folded] {
			continue
		}
		seen[folded] = true
		taken[e.name] = true
		p := pick{name: e.name}
		if tag := e.name + ".json"; regular[tag] {
			p.tag = tag
			taken[tag] = true
		}
		picks = append(picks, p)
	}
	for _, e := range entries {
		if !taken[e.name] {
			dropped = append(dropped, e.name)
		}
	}
	return picks, dropped
}

// collectRecordings reads dir, the record directory of a finished oracle run
// below top (the evidence directory of the jig home), and returns the
// recordings in it in name order and the names of the entries it left
// unrecorded (pickRecordings). Only the files directly in dir count.
//
// A harness may remove and recreate the directory (Playwright clears its
// output directory), so dir is not required to be the very directory jig
// made. What is required is that it is a plain directory under plain parents
// when it is read: those are checked first, before anything in it is read,
// and the directory is pinned then for the media rules (media.Verify), which
// are the gate demo's: their types, sizes and count, no link, and every file
// hashed from the very file checked. A directory the harness removed and did
// not recreate held nothing. An unreadable or invalid tag, or a recording that
// fails a rule, refuses the whole collection with a reason naming the file.
func collectRecordings(top, dir string) (recs []journal.Recording, dropped []string, err error) {
	if err := media.PlainParents(top, dir); err != nil {
		return nil, nil, err
	}
	made, err := media.LstatPinned(dir)
	if os.IsNotExist(err) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("%s cannot be read: %w", recordDirEnv, withoutPath(err))
	}
	if made.Mode().Type() != os.ModeDir {
		return nil, nil, fmt.Errorf("%s is not a plain directory (a link or junction is refused)", recordDirEnv)
	}
	dirEntries, err := os.ReadDir(dir)
	if err != nil {
		return nil, nil, fmt.Errorf("%s cannot be read: %w", recordDirEnv, withoutPath(err))
	}
	entries := make([]dirEntry, len(dirEntries))
	for i, e := range dirEntries {
		entries[i] = dirEntry{name: e.Name(), regular: e.Type().IsRegular()}
	}
	picks, dropped := pickRecordings(entries)
	if len(picks) == 0 {
		return nil, dropped, nil
	}

	tags := make([]recordingTag, len(picks))
	listed := make([]media.Listed, len(picks))
	for i, p := range picks {
		if p.tag != "" {
			if tags[i], err = readTag(dir, p.tag); err != nil {
				return nil, nil, err
			}
		}
		listed[i] = media.Listed{File: p.name, Caption: tags[i].Caption}
	}
	files, err := media.Verify(dir, recordDirEnv, made, listed)
	if err != nil {
		return nil, nil, err
	}
	recs = make([]journal.Recording, len(files))
	for i, f := range files {
		tag := tags[i]
		scenario := tag.Scenario
		if scenario == "" {
			scenario = strings.TrimSuffix(f.Name, filepath.Ext(f.Name))
		}
		recs[i] = journal.Recording{File: f.Name, SHA256: f.SHA256, Size: f.Size, Scenario: scenario, Flow: tag.Flow, Step: tag.Step, Caption: tag.Caption}
	}
	return recs, dropped, nil
}

// readTag reads and parses the tag file name in dir, strictly: an unknown
// field, a value of the wrong type, a second document, a negative step or
// text over its bound is invalid. The file is read through the handle that
// was checked to be the regular file listed, so a swap since the listing is
// refused, not followed. A refusal says what is wrong in jig's own words,
// never the decoder's, whose text can carry the tag's own bytes.
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
	invalid := func(why string) (recordingTag, error) {
		return recordingTag{}, fmt.Errorf("tag %q is not valid: %s", name, why)
	}
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&tag); err != nil {
		return invalid(tagProblem(err))
	}
	if _, err := dec.Token(); err != io.EOF {
		return invalid("it holds more than one JSON value")
	}
	switch {
	case tag.Step < 0:
		return invalid("step is negative")
	case len(tag.Scenario) > maxScenarioBytes:
		return invalid(fmt.Sprintf("scenario is longer than %d bytes", maxScenarioBytes))
	case len(tag.Flow) > maxFlowBytes:
		return invalid(fmt.Sprintf("flow is longer than %d bytes", maxFlowBytes))
	case len(tag.Caption) > maxCaptionBytes:
		return invalid(fmt.Sprintf("caption is longer than %d bytes", maxCaptionBytes))
	}
	return tag, nil
}

// tagProblem says in jig's own words what a tag decoder's error means. It
// echoes nothing of the error's text, which for an unknown field is the
// field's name as written (a tag may name anything, a host path included).
func tagProblem(err error) string {
	var syntax *json.SyntaxError
	var wrongType *json.UnmarshalTypeError
	switch {
	case errors.Is(err, io.EOF), errors.Is(err, io.ErrUnexpectedEOF):
		return "it is empty or ends early"
	case errors.As(err, &syntax):
		return fmt.Sprintf("it is not JSON (byte %d)", syntax.Offset)
	case errors.As(err, &wrongType):
		switch wrongType.Field {
		case "scenario", "flow", "step", "caption":
			return fmt.Sprintf("field %q has the wrong type", wrongType.Field)
		}
		return "it is not a JSON object of scenario, flow, step and caption"
	case strings.HasPrefix(err.Error(), "json: unknown field"):
		return "it has a field other than scenario, flow, step and caption"
	}
	return "it is not a JSON object of scenario, flow, step and caption"
}

// nameDropped lists the first few dropped entries and counts the rest.
func nameDropped(names []string) string {
	if len(names) <= maxDroppedNames {
		return strings.Join(names, ", ")
	}
	return fmt.Sprintf("%s (and %d more)", strings.Join(names[:maxDroppedNames], ", "), len(names)-maxDroppedNames)
}

// capText keeps s to at most n bytes, ending at a whole rune and marked
// with "..." when it was cut.
func capText(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n] + "..."
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

// leaveOutHostPaths replaces the directories jig chose, in each spelling an
// error may carry them (as they are, with forward slashes, and with every
// backslash doubled as a JSON string writes it), with a name for the role
// they play, so a refusal committed to the store does not carry a path of
// this machine. The recording directory goes first, being inside the jig home.
func leaveOutHostPaths(s, recordDir, jigHome string) string {
	for _, p := range []struct{ path, name string }{
		{recordDir, recordDirEnv},
		{jigHome, "<jig home>"},
	} {
		if p.path == "" {
			continue
		}
		for _, spelling := range []string{p.path, filepath.ToSlash(p.path), strings.ReplaceAll(p.path, `\`, `\\`)} {
			s = strings.ReplaceAll(s, spelling, p.name)
		}
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
