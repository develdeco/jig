package verifydeliver

// Publish picks the build's recordings for the pull request's demo (ADR 0029,
// step 5). The build's end-to-end scenarios recorded themselves into the jig
// home at each builder's green, and the journal's "recorded" lines say what
// they wrote. At publish a short model session chooses the ones that show the
// change and composes them into flows; jig validates the choice in its own
// words, copies the chosen files into a directory of its own, and renders the
// pull request's ## Demo section from it. This file holds the pieces that need
// no session: the candidates, the wire shapes, the prompt, the strict parse,
// the validation and the render. picksrun.go holds the step that runs them.

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/home"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/media"
)

const (
	// maxPickTitleBytes, maxPickCaptionBytes and maxPickSummaryBytes bound the
	// session's words in a result: far past a label or a few sentences is not
	// a pick but something echoed back, and a pick reaches the journal and a
	// public pull request. A pick over a bound is refused, never truncated.
	maxPickTitleBytes   = 1000
	maxPickCaptionBytes = 1000
	maxPickSummaryBytes = 4096

	// untitledFlow stands for a flow's title when what the session wrote was
	// left out of the body whole (it named one of jig's directories) or came
	// to nothing once made safe to render.
	untitledFlow = "Recordings"
)

// PicksCandidate is one recording offered to the pick session: picks.json's
// "candidates" entry. ID is what the session names it by; the rest is what the
// build's scenarios tagged it with and where in the history it was recorded.
type PicksCandidate struct {
	ID       string `json:"id"`
	Scenario string `json:"scenario"`
	Flow     string `json:"flow,omitempty"`
	Step     int    `json:"step,omitempty"`
	Caption  string `json:"caption,omitempty"`
	Commit   string `json:"commit"`
	Kind     string `json:"kind"`
	Name     string `json:"name"`
}

// PicksRequest is picks.json's exact wire shape, the input jig writes for one
// pick dispatch. It names the intent by its source and the absolute path of
// the file that states it, so it sits under the jig home and never in the
// store.
type PicksRequest struct {
	Ticket     string           `json:"ticket"`
	HeadSHA    string           `json:"head_sha"`
	Intent     Intent           `json:"intent"`
	Candidates []PicksCandidate `json:"candidates"`
}

// PicksItem is one picked recording in a result: a candidate's id and,
// optionally, a caption to show in place of the recording's own.
type PicksItem struct {
	ID      string `json:"id"`
	Caption string `json:"caption,omitempty"`
}

// PicksFlow is one flow of a result: a title and the recordings in it, in the
// order the pull request shows them.
type PicksFlow struct {
	Title string      `json:"title"`
	Items []PicksItem `json:"items"`
}

// PicksResult is the pick session's output, its exact wire shape.
type PicksResult struct {
	Flows   []PicksFlow `json:"flows"`
	Summary string      `json:"summary"`
}

// picksPromptTemplate is the exact prompt rendered (via fmt.Sprintf) for every
// pick dispatch: it states the job and the output contract and says what jig
// will check. It names no tool and no kind of change, and the session gets no
// repository: what it judges by is the intent and what the scenarios say about
// their recordings.
const picksPromptTemplate = `Choose which of this build's recordings the pull request shows, and compose them into flows. Your input is in picks.json at %s: the change's intent, as a source and the path of the file that states it (` + intentSourcesPrompt + `), and the candidate recordings, each with an id, the scenario it records, an optional flow and step, a caption, and whether it is an image or a video.
Pick the recordings that show this change to a person reviewing the pull request, and leave out the rest. Put recordings of one flow together in step order, keep scenarios that stand alone as flows of their own, and mix the two where that reads best. Pick at most %d recordings in all. Give each flow a short title, and a caption of your own only where a recording's caption would mislead. Do not edit files, commit, or push; you work in an empty scratch directory.
When finished, write %s with exactly one JSON object: %s
jig checks that every id is a candidate and none is used twice, that recordings of one flow keep their step order, that every title and the summary are non-empty and within bounds, and that at least one and at most %d recordings are picked. A result that fails a check is refused whole, and the pull request then carries the gate's demo, if there is one.`

// picksResultSchema is the {schema} filled into picksPromptTemplate: the
// literal shape of one pick result.
const picksResultSchema = `{"flows": [{"title": "...", "items": [{"id": "<candidate id>", "caption": "<optional>"}]}], "summary": "..."}`

// RenderPicksPrompt fills picksPromptTemplate for one pick dispatch.
func RenderPicksPrompt(reqPath, resultPath string) string {
	return fmt.Sprintf(picksPromptTemplate, reqPath, media.MaxFiles, resultPath, picksResultSchema, media.MaxFiles)
}

var (
	picksResultTopKeys  = map[string]bool{"flows": true, "summary": true}
	picksResultFlowKeys = map[string]bool{"title": true, "items": true}
)

// picksInvalid wraps msg as the error ParsePicksResult and resolvePicks return
// for a malformed or out-of-contract result.
func picksInvalid(format string, args ...any) error {
	return fmt.Errorf("the pick result is invalid: %s", fmt.Sprintf(format, args...))
}

// ParsePicksResult parses a pick result as strictly as ParseDemoResult parses
// a demo's: exactly one JSON object, no key repeated anywhere in it (exactly
// or only by case), no key but the recognized ones at any level, "flows" a
// list and "summary" a string, both present and not null. What it cannot know
// from the bytes alone, that an id is a candidate, is resolvePicks'.
func ParsePicksResult(data []byte) (PicksResult, error) {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return PicksResult{}, picksInvalid("it must contain exactly one JSON object")
	}
	if dup, err := duplicateObjectKey(data); err != nil {
		return PicksResult{}, picksInvalid("it is not valid JSON: %v", err)
	} else if dup != "" {
		return PicksResult{}, picksInvalid("a key repeats an earlier key in the same object")
	}
	var present map[string]json.RawMessage
	if err := json.Unmarshal(data, &present); err != nil {
		return PicksResult{}, picksInvalid("it is not valid JSON: %v", err)
	}
	for _, key := range []string{"flows", "summary"} {
		raw, ok := present[key]
		if !ok {
			return PicksResult{}, picksInvalid("%q is missing", key)
		}
		if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return PicksResult{}, picksInvalid("%q must not be null", key)
		}
	}
	if bad, err := unknownKey(data, picksResultTopKeys, listKeySpec{"flows", picksResultFlowKeys}); err == nil && bad != "" {
		return PicksResult{}, picksInvalid("it has a key other than flows, summary, title, items, id and caption")
	}

	dec := json.NewDecoder(bytes.NewReader(data))
	dec.DisallowUnknownFields()
	var res PicksResult
	if err := dec.Decode(&res); err != nil {
		// The decoder's text can carry the result's own words (an unknown
		// field's name, a value's type), which are not jig's to repeat.
		return PicksResult{}, picksInvalid("a field is missing, unknown or of the wrong type")
	}
	if _, err := dec.Token(); err != io.EOF {
		return PicksResult{}, picksInvalid("it must contain exactly one JSON object")
	}
	return res, nil
}

// pickCandidate is a candidate recording with what jig needs to find and
// check its file: PicksCandidate is what the session is told.
type pickCandidate struct {
	PicksCandidate
	run string            // the oracle run's directory below the commit's
	rec journal.Recording // the file as its recorded line describes it
}

// path is where c's file lives under the jig home's evidence directory.
func (c pickCandidate) path(d Deps, storeID, ticket string) (string, error) {
	dir, err := home.RecordDir(d.Home, storeID, ticket, c.Commit, c.run)
	if err != nil {
		return "", err
	}
	return filepath.Join(absPath(dir), c.rec.File), nil
}

// ext is c's file's lowercase extension without the dot.
func (c pickCandidate) ext() string {
	return strings.ToLower(strings.TrimPrefix(filepath.Ext(c.rec.File), "."))
}

// label is what c shows when neither the session nor its scenario gave it a
// caption: the scenario's own name.
func (c pickCandidate) label() string {
	if c.Caption != "" {
		return c.Caption
	}
	return c.Scenario
}

var hexSHA = regexp.MustCompile(`^[0-9a-f]{7,64}$`)

// pickCandidates returns the recordings a publish of head offers its pick:
// every file of a "recorded" line that wrote some and was not refused, whose
// commit is head or an ancestor of it in leaseDir (a recording of a commit the
// branch no longer holds shows something it does not ship), and of the files
// sharing a flow, scenario and step the one from the latest such line, since a
// later build recorded the same scenario again. A candidate is then checked
// against its file under the jig home (recordingProblem); one that fails is no
// candidate, and dropped names it and why. The candidates come back in a
// stable order, flow then step then scenario, with ids r1, r2, ... in that
// order, so the same set of recordings always reads the same.
func pickCandidates(d Deps, ticket string, lines []journal.Line, leaseDir, head string) (cands []pickCandidate, dropped []string, err error) {
	all, err := recordedCandidates(d, lines, leaseDir, head)
	if err != nil || len(all) == 0 {
		return nil, nil, err
	}
	return verifiedCandidates(d, ticket, all)
}

// recordedCandidates is the first half of pickCandidates: the latest recording
// of each flow, scenario and step made at head or an ancestor of it, as the
// journal describes it and not yet checked against its file, in the stable
// order (flow, step, scenario) that gives them their ids. A caller that wants
// fewer than all of them, or another order, chooses before verifiedCandidates
// pays for the check of each.
func recordedCandidates(d Deps, lines []journal.Line, leaseDir, head string) ([]pickCandidate, error) {
	// Recordings are kept under the jig home, so with none there are none: a
	// relative evidence path would be read from wherever jig happens to run.
	if d.Home == "" {
		return nil, nil
	}
	var recorded []journal.Line
	var commits []string
	seen := map[string]bool{}
	for _, l := range lines {
		if l.Event != "recorded" || len(l.Recordings) == 0 || strings.HasPrefix(l.Outcome, "refused") || l.RecordRun == "" || !hexSHA.MatchString(l.Commit) {
			continue
		}
		recorded = append(recorded, l)
		if !seen[l.Commit] {
			seen[l.Commit] = true
			commits = append(commits, l.Commit)
		}
	}
	if len(recorded) == 0 {
		return nil, nil
	}
	missing, err := gitx.Missing(leaseDir, head, commits)
	if err != nil {
		return nil, fmt.Errorf("check which recorded commits the branch holds: %w", err)
	}
	gone := map[string]bool{}
	for _, c := range missing {
		gone[c] = true
	}

	type key struct {
		flow, scenario string
		step           int
	}
	latest := map[key]pickCandidate{}
	for _, l := range recorded {
		if gone[l.Commit] {
			continue
		}
		for _, r := range l.Recordings {
			latest[key{r.Flow, r.Scenario, r.Step}] = pickCandidate{
				PicksCandidate: PicksCandidate{Scenario: r.Scenario, Flow: r.Flow, Step: r.Step, Caption: r.Caption, Commit: l.Commit, Name: r.File},
				run:            l.RecordRun,
				rec:            r,
			}
		}
	}
	all := make([]pickCandidate, 0, len(latest))
	for _, c := range latest {
		all = append(all, c)
	}
	sort.Slice(all, func(i, j int) bool {
		a, b := all[i], all[j]
		switch {
		case a.Flow != b.Flow:
			return a.Flow < b.Flow
		case a.Step != b.Step:
			return a.Step < b.Step
		case a.Scenario != b.Scenario:
			return a.Scenario < b.Scenario
		case a.rec.File != b.rec.File:
			return a.rec.File < b.rec.File
		}
		return a.Commit < b.Commit
	})
	return all, nil
}

// verifiedCandidates is the second half of pickCandidates: each of all, in the
// order given, checked against its file under the jig home. One that fails is
// no candidate, and dropped names it and why; the rest come back with ids r1,
// r2, ... in that order and their kind.
func verifiedCandidates(d Deps, ticket string, all []pickCandidate) (cands []pickCandidate, dropped []string, err error) {
	storeID, err := d.Store.ID()
	if err != nil {
		return nil, nil, fmt.Errorf("resolve the store id: %w", err)
	}
	top := absPath(filepath.Join(d.Home, "evidence"))
	for _, c := range all {
		if why := recordingProblem(d, top, storeID, ticket, c); why != "" {
			dropped = append(dropped, fmt.Sprintf("%s (%s)", c.rec.File, why))
			continue
		}
		c.ID = fmt.Sprintf("r%d", len(cands)+1)
		c.Kind = media.Kind(c.ext())
		cands = append(cands, c)
	}
	return cands, dropped, nil
}

// recordingProblem says why c's file is no longer the one its recorded line
// describes, or "" when it is: a name and type media accepts, a size within
// the limit for its kind, plain directories down to it, and the file itself a
// regular file of the recorded size whose bytes hash to the recorded sha256.
// It is the check renderDemoSection makes of a demo's files, made again here
// because the files sit where the build's scenarios left them, possibly for
// days.
func recordingProblem(d Deps, top, storeID, ticket string, c pickCandidate) string {
	if !media.PlainName(c.rec.File) {
		return "not a plain file name"
	}
	kind := media.Kind(c.ext())
	if kind == "" {
		return "not an accepted type"
	}
	limit := int64(media.MaxImageBytes)
	if kind == "video" {
		limit = media.MaxVideoBytes
	}
	if c.rec.Size <= 0 || c.rec.Size > limit {
		return "its recorded size is not one that can be attached"
	}
	path, err := c.path(d, storeID, ticket)
	if err != nil {
		return "no such recording directory"
	}
	if media.PlainParents(top, path) != nil {
		return "a directory above it is a link"
	}
	info, err := media.LstatPinned(path)
	switch {
	case os.IsNotExist(err):
		return "gone"
	case err != nil:
		return "cannot be read"
	case !info.Mode().IsRegular():
		return "not a regular file"
	case info.Size() != c.rec.Size:
		return "changed size"
	}
	if sum, err := media.HashRegularFile(path, info); err != nil || sum != c.rec.SHA256 {
		return "changed"
	}
	return ""
}

// pickFingerprint identifies a set of candidates: the hash of everything about
// each one that decides what a pick of it would show. A re-publish of the same
// head whose candidates hash the same asks the same question, so it takes the
// earlier answer.
func pickFingerprint(cands []pickCandidate) string {
	h := sha256.New()
	for _, c := range cands {
		fmt.Fprintf(h, "%q %q %q %q %q %q %d %q\n", c.ID, c.Commit, c.run, c.rec.File, c.rec.SHA256, c.Scenario, c.Step, c.Flow+"\x00"+c.Caption)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// pickedItem is one recording a pick shows: its candidate and the caption the
// session gave it in place of its own ("" for none).
type pickedItem struct {
	cand    pickCandidate
	caption string
}

// shownCaption is what the item shows beside the recording, before it is made safe
// to render: the session's own, else the recording's, else its scenario's.
func (it pickedItem) shownCaption() string {
	if it.caption != "" {
		return it.caption
	}
	return it.cand.label()
}

type pickedFlow struct {
	title string
	items []pickedItem
}

// pick is a validated pick: the session's summary and the flows it composed,
// every item resolved to the candidate it names.
type pick struct {
	summary string
	flows   []pickedFlow
}

// items returns every item of p in the order the pull request shows them.
func (p pick) items() []pickedItem {
	var out []pickedItem
	for _, f := range p.flows {
		out = append(out, f.items...)
	}
	return out
}

// resolvePicks validates res against the candidates it was made from and
// returns it as a pick. Every check is jig's and every refusal is in jig's
// words, naming a flow and an item by position and never repeating what the
// session wrote: at least one flow, with a title, and each flow at least one
// item; every id a candidate and none used twice, across flows too; the
// recordings of one candidate flow in step order within a composed flow (those
// of different flows, or of none, may stand in any order); a title, a caption
// and the summary within their bounds and, but for a caption, not empty; and
// at least one and at most media.MaxFiles files in all.
func resolvePicks(res PicksResult, cands []pickCandidate) (pick, error) {
	byID := make(map[string]pickCandidate, len(cands))
	for _, c := range cands {
		byID[c.ID] = c
	}
	if len(res.Flows) == 0 {
		return pick{}, picksInvalid("it picked no recording")
	}
	if strings.TrimSpace(res.Summary) == "" {
		return pick{}, picksInvalid("the summary is empty")
	}
	if len(res.Summary) > maxPickSummaryBytes {
		return pick{}, picksInvalid("the summary is %d bytes, over the %d-byte limit", len(res.Summary), maxPickSummaryBytes)
	}
	out := pick{summary: res.Summary}
	used := map[string]bool{}
	total := 0
	for i, f := range res.Flows {
		if strings.TrimSpace(f.Title) == "" {
			return pick{}, picksInvalid("flow %d has an empty title", i+1)
		}
		if len(f.Title) > maxPickTitleBytes {
			return pick{}, picksInvalid("flow %d's title is %d bytes, over the %d-byte limit", i+1, len(f.Title), maxPickTitleBytes)
		}
		if len(f.Items) == 0 {
			return pick{}, picksInvalid("flow %d has no recording", i+1)
		}
		flow := pickedFlow{title: f.Title}
		lastStep := map[string]int{}
		for j, it := range f.Items {
			c, ok := byID[it.ID]
			switch {
			case !ok:
				return pick{}, picksInvalid("flow %d item %d is not a candidate", i+1, j+1)
			case used[it.ID]:
				return pick{}, picksInvalid("flow %d item %d is a candidate that is already picked", i+1, j+1)
			case len(it.Caption) > maxPickCaptionBytes:
				return pick{}, picksInvalid("flow %d item %d's caption is %d bytes, over the %d-byte limit", i+1, j+1, len(it.Caption), maxPickCaptionBytes)
			case it.Caption != "" && strings.TrimSpace(it.Caption) == "":
				return pick{}, picksInvalid("flow %d item %d has a blank caption", i+1, j+1)
			}
			used[it.ID] = true
			if c.Flow != "" {
				if step, seen := lastStep[c.Flow]; seen && c.Step < step {
					return pick{}, picksInvalid("flow %d item %d comes after a later step of its own flow", i+1, j+1)
				}
				lastStep[c.Flow] = c.Step
			}
			flow.items = append(flow.items, pickedItem{cand: c, caption: it.Caption})
			total++
		}
		out.flows = append(out.flows, flow)
	}
	if total > media.MaxFiles {
		return pick{}, picksInvalid("it picks %d recordings; at most %d are accepted", total, media.MaxFiles)
	}
	return out, nil
}

// journalPick is p as a publish-picks line records it, for the candidates
// whose fingerprint is fp.
func journalPick(p pick, fp string) *journal.Pick {
	out := &journal.Pick{Candidates: fp, Summary: p.summary}
	for _, f := range p.flows {
		jf := journal.PickFlow{Title: f.title}
		for _, it := range f.items {
			jf.Items = append(jf.Items, journal.PickItem{ID: it.cand.ID, File: it.cand.rec.File, Caption: it.caption})
		}
		out.Flows = append(out.Flows, jf)
	}
	return out
}

// picksResultOf is a journaled pick as the result it was made from, so that
// reusing it passes through the same validation a fresh pick does.
func picksResultOf(jp *journal.Pick) PicksResult {
	res := PicksResult{Summary: jp.Summary}
	for _, f := range jp.Flows {
		rf := PicksFlow{Title: f.Title}
		for _, it := range f.Items {
			rf.Items = append(rf.Items, PicksItem{ID: it.ID, Caption: it.Caption})
		}
		res.Flows = append(res.Flows, rf)
	}
	return res
}

// reusablePick is the pick an earlier publish of head recorded for candidates
// whose fingerprint is fp, or nil when there is none. The latest such line
// wins; a refused pick left none, so a refusal is asked again.
func reusablePick(lines []journal.Line, head, fp string) *journal.Pick {
	for i := len(lines) - 1; i >= 0; i-- {
		l := lines[i]
		if l.Event == "publish-picks" && l.Commit == head && l.Pick != nil && l.Pick.Candidates == fp {
			return l.Pick
		}
	}
	return nil
}

// pickLabel makes a title or a caption safe to put in the pull request body
// the way a demo's caption is: one line, without the characters that could
// open a link or close an image's alt text, capped, and HTML-escaped last.
func pickLabel(s string) string {
	return escapeDemoHTML(capDemoCaption(sanitizeDemoCaption(demoOneLine(s))))
}

// renderPicksSection renders the ## Demo section for p, whose items were
// staged as files (in the order p.items() gives), with the same care a gate
// demo's section is rendered with. Host paths are left out whole, by exact
// match, from the summary, every title and every caption (knownHostDirs), and
// named in the result for publish's output. Each item takes the one reference
// form gh rewrites for its kind: an image `![caption](./name)`, a video
// `./name: caption`, as bullets under its flow's ### heading, so the
// read-back that patches an unrewritten video reference finds them as it finds
// a demo's.
func renderPicksSection(p pick, dir string, files []DemoFile, knownHostDirs []hostDir) DemoRenderResult {
	var b strings.Builder
	b.WriteString("## Demo\n\n")
	res := DemoRenderResult{MediaDir: dir, MediaFiles: files}
	summary := p.summary
	if containsHostPath(summary, knownHostDirs...) {
		res.ScrubbedSummary = true
		summary = ""
	}
	if summary != "" {
		fmt.Fprintf(&b, "%s\n\n", escapeDemoHTML(closeOpenFence(demoteHeadings(summary))))
	}
	n := 0
	for i, f := range p.flows {
		title := f.title
		if containsHostPath(title, knownHostDirs...) {
			res.ScrubbedCaptions = append(res.ScrubbedCaptions, fmt.Sprintf("the title of flow %d", i+1))
			title = ""
		}
		if title = pickLabel(title); title == "" {
			title = untitledFlow
		}
		fmt.Fprintf(&b, "### %s\n\n", title)
		for _, it := range f.items {
			name := files[n].Name
			n++
			caption := it.shownCaption()
			if containsHostPath(caption, knownHostDirs...) {
				res.ScrubbedCaptions = append(res.ScrubbedCaptions, name)
				caption = ""
			}
			caption = pickLabel(caption)
			switch {
			case media.Kind(it.cand.ext()) == "image":
				fmt.Fprintf(&b, "- ![%s](./%s)\n", caption, name)
			case caption == "":
				fmt.Fprintf(&b, "- ./%s\n", name)
			default:
				fmt.Fprintf(&b, "- ./%s: %s\n", name, caption)
			}
		}
		b.WriteString("\n")
	}
	res.Section = b.String()
	return res
}
