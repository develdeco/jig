package verifydeliver

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/develdeco/jig/internal/home"
	"github.com/develdeco/jig/internal/session"
	"github.com/develdeco/jig/internal/store"
)

// Demo statuses. A demo.yaml carries recorded or refused; existing is only
// ever a GateReport's own status, for a round that ran no demo because an
// earlier round already recorded one for the same reviewed head.
const (
	DemoRecorded = "recorded"
	DemoRefused  = "refused"
	DemoExisting = "existing"
)

// The limits a demo's media are held to: the types and sizes `gh ... --attach`
// accepts, so a file jig records is one a later publish can attach. Images
// are at most 10 MiB, videos at most 100 MiB, a file is never empty (gh
// refuses one), and a demo has at most 50 files.
const (
	demoMaxImageBytes = 10 << 20
	demoMaxVideoBytes = 100 << 20
	demoMaxFiles      = 50
)

var (
	demoImageExts = []string{"png", "jpg", "jpeg", "gif", "webp", "svg"}
	demoVideoExts = []string{"mp4", "mov", "webm"}
)

// demoReasonCap bounds a recorded refusal reason: an operating system error
// it quotes can run long, and demo.yaml is a manifest, not a log.
const demoReasonCap = 400

// DemoLimits is demo.json's "limits" block: what jig accepts of the media a
// demo session writes.
type DemoLimits struct {
	ImageExtensions []string `json:"image_extensions"`
	VideoExtensions []string `json:"video_extensions"`
	MaxImageBytes   int64    `json:"max_image_bytes"`
	MaxVideoBytes   int64    `json:"max_video_bytes"`
	MaxFiles        int      `json:"max_files"`
}

// demoLimits returns the limits every demo is held to.
func demoLimits() DemoLimits {
	return DemoLimits{
		ImageExtensions: append([]string{}, demoImageExts...),
		VideoExtensions: append([]string{}, demoVideoExts...),
		MaxImageBytes:   demoMaxImageBytes,
		MaxVideoBytes:   demoMaxVideoBytes,
		MaxFiles:        demoMaxFiles,
	}
}

// DemoRequest is demo.json's exact wire shape, the input jig writes for one
// demo dispatch. BaseSHA is the merge base with the target, so base..head is
// the whole change, not only the round's own delta; MediaDir is absolute,
// which is why demo.json lives beside the media under the jig home and never
// in the store (see demoJSONPath).
type DemoRequest struct {
	Ticket   string     `json:"ticket"`
	Round    int        `json:"round"`
	BaseSHA  string     `json:"base_sha"`
	HeadSHA  string     `json:"head_sha"`
	Intent   Intent     `json:"intent"`
	MediaDir string     `json:"media_dir"`
	Limits   DemoLimits `json:"limits"`
}

// DemoMedia is one file a demo session reports in its result: a name inside
// media_dir and the caption a reader sees beside it.
type DemoMedia struct {
	File    string `json:"file"`
	Caption string `json:"caption"`
}

// DemoResult is work/gate.round-N.demo.result.json's exact wire shape, the
// demo session's output. An empty Media with a Summary saying why nothing is
// visible is a valid result.
type DemoResult struct {
	Media   []DemoMedia `json:"media"`
	Summary string      `json:"summary"`
}

// demoPromptTemplate is the exact prompt rendered (via fmt.Sprintf) for every
// demo dispatch: it states the job and the output contract, and says what
// jig will verify. It names no tool and lists no kind of change: a repo
// documents its own demo tooling in its own CLAUDE.md, which a headless
// session already receives.
const demoPromptTemplate = `You are demonstrating round %d of ticket %s. Your inputs are in demo.json at %s.
Show a person reviewing this change that it works, in whatever form shows it best: the diff %s..%s in this worktree, as it is now, against the change's intent. demo.json's intent names it and its source: ` + intentSourcesPrompt + ` If nothing about it can be shown, record nothing and say why. Do not edit tracked files, commit, or push.
Write every file you produce directly into media_dir (%s), with no subdirectories or links. Every file's type and size must be within the limits in demo.json.
When finished, write %s with exactly one JSON object: %s
An empty media list with a summary saying why nothing is visible is a valid result.
jig checks that this worktree's HEAD and tracked files are unchanged, that the result has a non-empty summary and a non-empty caption on every file, and that every listed file is a non-empty regular file directly in media_dir, of an allowed type and size, within the file limit. A result that fails any check is refused whole: jig records why, and records none of your files. Nothing you record changes the review's verdict.`

// demoResultSchema is the {schema} filled into demoPromptTemplate: the
// literal shape of one demo.result.json.
const demoResultSchema = `{"media": [{"file": "<name in media_dir>", "caption": "..."}], "summary": "..."}`

// RenderDemoPrompt fills demoPromptTemplate for one demo dispatch.
func RenderDemoPrompt(req DemoRequest, demoPath, resultPath string) string {
	return fmt.Sprintf(demoPromptTemplate, req.Round, req.Ticket, demoPath, req.BaseSHA, req.HeadSHA, req.MediaDir, resultPath, demoResultSchema)
}

// demoJSONPath is a demo dispatch's input: the file beside mediaDir, named
// after it (<head sha>.demo.json under the ticket's evidence directory). It
// holds the absolute media_dir, a path of this machine that names the
// operator's jig home, so it stays out of the store, whose git is committed,
// pushed and often shared. Everything else jig writes to the store for a demo
// is a name, a hash or a reason that names no host path; what the session wrote
// itself is recorded as written (see demoResultJSONPath).
func demoJSONPath(mediaDir string) string {
	return filepath.Clean(mediaDir) + ".demo.json"
}

// demoResultJSONPath is a demo dispatch's output, store-side beside the
// reviewer's own: file names, captions and a summary, the session's own words.
// They are recorded as written, here and in demo.yaml, as the reviewer's
// result.json summary is: jig does not filter or rewrite model prose, so a
// path the session was told and chose to repeat (media_dir's) is in them.
func demoResultJSONPath(st *store.Store, ticket string, n int) string {
	return filepath.Join(gateWorkDir(st, ticket), fmt.Sprintf("gate.round-%d.demo.result.json", n))
}

// demoYAMLPath is one round's recorded demo: gate/round-N/demo.yaml.
func demoYAMLPath(st *store.Store, ticket string, n int) string {
	return filepath.Join(gateRoundDir(st, ticket, n), "demo.yaml")
}

// demoResultTopKeys and demoMediaKeys are the exact key names DemoResult and
// DemoMedia's own JSON tags declare.
var (
	demoResultTopKeys = map[string]bool{"media": true, "summary": true}
	demoMediaKeys     = map[string]bool{"file": true, "caption": true}
)

// demoInvalid wraps msg as the error ParseDemoResult returns for a malformed
// or out-of-contract result.
func demoInvalid(msg string) error {
	return fmt.Errorf("demo.result.json is invalid: %s", msg)
}

// ParseDemoResult parses demo.result.json as strictly as ParseReviewResult
// parses a reviewer's result.json: exactly one JSON object; no key repeated,
// exactly or only by case, anywhere in it; every key an exact,
// case-sensitive match of a recognized field; "media" a list, present and
// not null; a non-empty "summary"; and a non-empty file and caption on every
// entry. What it cannot know from the bytes alone - that a file exists, is
// the right kind and size - is verifyDemoMedia's.
func ParseDemoResult(data []byte) (DemoResult, error) {
	trimmed := bytes.TrimSpace(data)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return DemoResult{}, demoInvalid("must contain exactly one JSON object")
	}
	if dup, derr := duplicateObjectKey(data); derr != nil {
		return DemoResult{}, demoInvalid(fmt.Sprintf("not valid JSON: %v", derr))
	} else if dup != "" {
		return DemoResult{}, demoInvalid(fmt.Sprintf("key %q repeats an earlier key in the same object", dup))
	}
	if bad, kerr := unknownKey(data, demoResultTopKeys, listKeySpec{"media", demoMediaKeys}); kerr == nil && bad != "" {
		return DemoResult{}, demoInvalid(fmt.Sprintf("key %q is not a recognized field", bad))
	}

	// Key presence and nullness are checked apart from the typed decode below,
	// which cannot tell "never written" or "written as null" from an empty
	// list once decoded into a plain slice. Unmarshalling into a map also
	// refuses anything but one JSON value in data, so a second object after
	// the first is not valid JSON here.
	var present map[string]json.RawMessage
	if err := json.Unmarshal(data, &present); err != nil {
		return DemoResult{}, demoInvalid(fmt.Sprintf("not valid JSON: %v", err))
	}
	for _, key := range []string{"media", "summary"} {
		if _, ok := present[key]; !ok {
			return DemoResult{}, demoInvalid(fmt.Sprintf("missing %q", key))
		}
	}
	if bytes.Equal(bytes.TrimSpace(present["media"]), []byte("null")) {
		return DemoResult{}, demoInvalid(`"media" must be a list, not null`)
	}

	var res DemoResult
	if err := json.Unmarshal(data, &res); err != nil {
		return DemoResult{}, demoInvalid(fmt.Sprintf("not valid JSON: %v", err))
	}

	if strings.TrimSpace(res.Summary) == "" {
		return DemoResult{}, demoInvalid("has an empty summary")
	}
	for i, m := range res.Media {
		if strings.TrimSpace(m.File) == "" {
			return DemoResult{}, demoInvalid(fmt.Sprintf("media entry %d has an empty file", i))
		}
		if strings.TrimSpace(m.Caption) == "" {
			return DemoResult{}, demoInvalid(fmt.Sprintf("%s has an empty caption", demoEntry(i, m.File)))
		}
	}
	return res, nil
}

// demoEntry names the media entry at index i of a result, listed as file, in a
// refusal reason: by its index and the last element of file, never by file
// itself. The session was told media_dir's absolute path, so it may list a
// file by a full path, in whatever spelling its backend gave it (raw,
// forward-slash, a WSL mount), and a reason is committed to the store and
// printed. jig cannot know every spelling to leave out, so it does not repeat
// a path it did not choose.
func demoEntry(i int, file string) string {
	return fmt.Sprintf("media entry %d (%q)", i, filepath.Base(file))
}

// DemoFile is one recorded media file as demo.yaml lists it: its name in
// media_dir after jig's rename, its sha256 and size, and the session's
// caption.
type DemoFile struct {
	Name    string `yaml:"name"`
	SHA256  string `yaml:"sha256"`
	Size    int64  `yaml:"size"`
	Caption string `yaml:"caption"`
}

// recordedDemoYAML and refusedDemoYAML are demo.yaml's exact on-disk shapes:
// the two statuses carry different keys, so each has its own struct rather
// than one with omitempty fields (a recorded demo with nothing to show still
// writes `media: []`).
type recordedDemoYAML struct {
	Status  string     `yaml:"status"`
	HeadSHA string     `yaml:"head_sha"`
	Summary string     `yaml:"summary"`
	Media   []DemoFile `yaml:"media"`
}

type refusedDemoYAML struct {
	Status  string `yaml:"status"`
	HeadSHA string `yaml:"head_sha"`
	Reason  string `yaml:"reason"`
}

// demoRecord is demo.yaml read back: the union of the two shapes.
type demoRecord struct {
	Status  string     `yaml:"status"`
	HeadSHA string     `yaml:"head_sha"`
	Summary string     `yaml:"summary"`
	Reason  string     `yaml:"reason"`
	Media   []DemoFile `yaml:"media"`
}

// readDemoRecord reads round n's demo.yaml. ok is false when the round has
// none (a round that ran no demo); a file that does not decode strictly, or
// names a status other than recorded or refused, is an error.
func readDemoRecord(st *store.Store, ticket string, n int) (rec demoRecord, ok bool, err error) {
	path := demoYAMLPath(st, ticket, n)
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return demoRecord{}, false, nil
		}
		return demoRecord{}, false, err
	}
	dec := yaml.NewDecoder(bytes.NewReader(data))
	dec.KnownFields(true)
	if err := dec.Decode(&rec); err != nil {
		return demoRecord{}, false, fmt.Errorf("gate/round-%d/demo.yaml cannot be read: %w", n, err)
	}
	if rec.Status != DemoRecorded && rec.Status != DemoRefused {
		return demoRecord{}, false, fmt.Errorf("gate/round-%d/demo.yaml has an unknown status %q", n, rec.Status)
	}
	return rec, true, nil
}

// recordedDemoRound returns the earliest of rounds 1..before-1 whose
// demo.yaml recorded a demo for head, 0 when none did (and with the error,
// 0 as well, when a demo.yaml before it cannot be read). A refused demo does
// not count: the next clean round on the same head runs the demo again.
func recordedDemoRound(st *store.Store, ticket string, before int, head string) (int, error) {
	for n := 1; n < before; n++ {
		rec, ok, err := readDemoRecord(st, ticket, n)
		if err != nil {
			return 0, err
		}
		if ok && rec.Status == DemoRecorded && rec.HeadSHA == head {
			return n, nil
		}
	}
	return 0, nil
}

// writeDemoRecord writes round n's demo.yaml for one attempt's outcome and
// returns the report that describes it: a refusal writes the refused shape,
// with its Reason, and nil writes the recorded one, for files and summary.
// The refusal's Detail goes to the report only. The round's directory already
// exists, since the round's own files were written before any demo ran.
func writeDemoRecord(st *store.Store, ticket string, n int, head string, files []DemoFile, summary string, refusal *demoRefusal) (DemoReport, error) {
	var (
		report DemoReport
		data   []byte
		err    error
	)
	if refusal != nil {
		report = DemoReport{Status: DemoRefused, Reason: refusal.Reason, Detail: refusal.Detail}
		data, err = yaml.Marshal(refusedDemoYAML{Status: DemoRefused, HeadSHA: head, Reason: refusal.Reason})
	} else {
		report = DemoReport{Status: DemoRecorded, Summary: summary, Media: files}
		data, err = yaml.Marshal(recordedDemoYAML{Status: DemoRecorded, HeadSHA: head, Summary: summary, Media: files})
	}
	if err != nil {
		return DemoReport{}, fmt.Errorf("marshal demo.yaml: %w", err)
	}
	if err := os.WriteFile(demoYAMLPath(st, ticket, n), data, 0o644); err != nil {
		return DemoReport{}, fmt.Errorf("write demo.yaml: %w", err)
	}
	return report, nil
}

// DemoReport is what a gate round says about its demo. Status is recorded or
// refused (both written to that round's demo.yaml) or existing (no demo ran:
// Round names the earlier round that already recorded one for this head).
type DemoReport struct {
	Status  string
	Round   int        // existing: the round whose demo.yaml recorded this head
	Summary string     // recorded: the session's summary
	Media   []DemoFile // recorded: every accepted file, renamed
	Reason  string     // refused: why, exactly as demo.yaml records it
	// Detail is a refused demo's failure text, when the demo session or its
	// backend failed: the gate report prints it and nothing records it (see
	// demoFailure).
	Detail string
	// Warning is set when demo.yaml was written but the store push that
	// carries it failed: the file is committed in the store's working copy and
	// reaches the remote with the next store push.
	Warning string
}

// demoMediaDir is where head's demo media live for this ticket under the
// jig home: <home>/evidence/<store id>/<ticket>/<head>.
func demoMediaDir(d Deps, ticket, head string) (string, error) {
	// A relative evidence path would land in whatever directory jig happens to
	// run from, and attemptDemo clears it: no silent fallback for an unset home.
	if d.Home == "" {
		return "", fmt.Errorf("no jig home is set, so there is nowhere to keep demo media")
	}
	id, err := d.Store.ID()
	if err != nil {
		return "", err
	}
	dir, err := home.EvidenceDir(d.Home, id, ticket, head)
	if err != nil {
		return "", err
	}
	return absPath(dir), nil
}

// plainEvidenceParents refuses a media directory reached through a store-id
// or ticket directory that is a link or a junction, before anything is
// cleared or made below it. jig made those directories, so a link in their
// place is one a session swapped in. verifyDemoMedia's identity check catches
// a swap made during its own attempt, but one that stays would send the next
// attempt's clearing, mkdir and media through it, so it is refused before
// that. A directory that does not exist yet is fine, since MkdirAll makes it
// plain. The head directory itself is not checked: an attempt removes a link
// there as itself.
func plainEvidenceParents(mediaDir string) error {
	ticketDir := filepath.Dir(mediaDir)
	for _, p := range []string{filepath.Dir(ticketDir), ticketDir} {
		info, err := os.Lstat(p)
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("evidence directory %q cannot be read: %w", filepath.Base(p), err)
		}
		if info.Mode().Type() != os.ModeDir {
			return fmt.Errorf("evidence directory %q is not a plain directory (a link or junction is refused, and left for the operator to remove)", filepath.Base(p))
		}
	}
	return nil
}

// demoFailure is an attempt that ended because the demo session, or the
// backend that ran it, failed: the dispatch returned an error, or the session
// wrote no demo result. It is told apart from a refusal, jig's own words about
// a result it read (a bad file name by index and base name, a type, a size),
// which jig composes from names and numbers. A failure's text is not jig's:
// the backend's or the session's error can hold anything, the arguments a
// backend echoed and a whole stderr tail among it. So demo.yaml records only
// that the demo session failed and err's failure code (failureCode, the code a
// store commit subject carries for the same reason), and the gate report
// prints err's text once, where it is read and not kept.
type demoFailure struct {
	err error
}

func (f *demoFailure) Error() string { return "the demo session failed: " + f.err.Error() }
func (f *demoFailure) Unwrap() error { return f.err }

// demoRefusal says one failed attempt in the two places it is said: Reason is
// what demo.yaml records (and the report prints), and Detail is what only the
// report prints.
type demoRefusal struct {
	Reason string
	Detail string
}

// refusalOf splits a failed attempt's error into its reason and its detail. A
// demoFailure has the code as its reason and its text as its detail; any other
// error is jig's own refusal, and its text, with hide applied to what names a
// directory of this machine, is the reason and all there is to say.
func refusalOf(err error, hide func(string) string) demoRefusal {
	var f *demoFailure
	if errors.As(err, &f) {
		return demoRefusal{
			Reason: "the demo session failed: " + failureCode(f.err),
			Detail: demoOneLine(f.err.Error()),
		}
	}
	return demoRefusal{Reason: demoReason(errors.New(hide(err.Error())))}
}

// demoOneLine returns s as one line, with runs of whitespace collapsed.
func demoOneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

// demoReason renders err as a refusal reason for demo.yaml and the report:
// one line, with runs of whitespace collapsed, capped at demoReasonCap
// runes.
func demoReason(err error) string {
	s := demoOneLine(err.Error())
	if r := []rune(s); len(r) > demoReasonCap {
		s = string(r[:demoReasonCap]) + "..."
	}
	return s
}

// backendFallback reports whether data, a demo result file, has the shape a
// backend gives the result it writes itself when a session wrote none: a JSON
// object with an "outcome" field, the slice result's, which the demo contract
// does not have. It is the field, not any wording, that says so. outcome and
// summary are what the backend recorded, for the report; one that is not a
// string is left out.
func backendFallback(data []byte) (outcome, summary string, ok bool) {
	var top map[string]json.RawMessage
	if err := json.Unmarshal(data, &top); err != nil {
		return "", "", false
	}
	if _, ok := top["outcome"]; !ok {
		return "", "", false
	}
	str := func(key string) string {
		var v string
		if raw, ok := top[key]; ok {
			_ = json.Unmarshal(raw, &v)
		}
		return v
	}
	return str("outcome"), str("summary"), true
}

// hostDir is a directory of this machine that a refusal reason must not
// print by its path, and the name it prints instead.
type hostDir struct {
	dir  string
	name string
}

// hostPathSpelling pairs one literal spelling of a hostDir with the name a
// caller should use for it: leaveOutHostPaths' own replacement, or
// containsHostPath's report of which directory matched.
type hostPathSpelling struct{ text, name string }

// hostPathSpellings returns every spelling of dirs a caller should treat as
// naming this machine's own directories: as each is given (when absolute),
// its absolute form, each with forward slashes, and each Go-quoted (the way
// %q prints a path, where a Windows path's backslashes double) - and, when
// withWSL, each one's WSL mount spelling too (session.WSLPath): the
// respelling herdr hands a session on Windows (session.respellMentions), a
// spelling jig chose for the session rather than jig's own text, so only a
// caller asking what a session might have repeated needs it. A bare
// relative dir names nothing of the machine and yields nothing, as does an
// empty directory (which would resolve to the working directory) or a
// filesystem root, a prefix of every path that would match them all.
// Longest spelling first, so a directory nested inside another (media_dir
// under the jig home) is matched, and named, as itself.
func hostPathSpellings(withWSL bool, dirs ...hostDir) []hostPathSpelling {
	var all []hostPathSpelling
	seen := map[string]bool{}
	add := func(text, name string) {
		if text == "" || seen[text] {
			return
		}
		seen[text] = true
		all = append(all, hostPathSpelling{text, name})
	}
	for _, d := range dirs {
		if d.dir == "" {
			continue
		}
		abs := absPath(d.dir)
		for _, text := range []string{d.dir, abs, filepath.ToSlash(d.dir), filepath.ToSlash(abs)} {
			// A filesystem root is a prefix of every path, and naming it would
			// match all of them.
			if !filepath.IsAbs(text) || filepath.Dir(text) == text {
				continue
			}
			quoted := strconv.Quote(text)
			add(text, d.name)
			add(quoted[1:len(quoted)-1], d.name)
		}
		if withWSL {
			// session.WSLPath is a no-op off a Windows-drive path, so a spelling
			// that comes back unchanged is already covered by the raw spellings
			// above, and jig never hands out a bare "/" as one of its own
			// directories for this to wrongly match everything.
			for _, text := range []string{session.WSLPath(d.dir), session.WSLPath(abs)} {
				if text != d.dir && text != abs {
					add(text, d.name)
				}
			}
		}
	}
	sort.SliceStable(all, func(i, j int) bool { return len(all[i].text) > len(all[j].text) })
	return all
}

// leaveOutHostPaths returns s with every spelling of each directory
// (hostPathSpellings, without the WSL mount form: this composes jig's own
// refusal reasons, never a session's text) replaced by its name. A refusal
// reason is committed to the store and printed in the report, and the
// operating system errors it carries name the path they failed on, which for
// the jig home holds the operator's user name. It is for the paths jig
// chose. A path the session chose is never put in a reason (demoEntry), and
// the text of a failure of the session or its backend, which can spell a
// path any way it likes, is never recorded (demoFailure).
func leaveOutHostPaths(s string, dirs ...hostDir) string {
	for _, sp := range hostPathSpellings(false, dirs...) {
		s = strings.ReplaceAll(s, sp.text, sp.name)
	}
	return s
}

// containsHostPath reports whether s literally contains one of dirs' own
// spellings, including every spelling jig may have handed a demo session
// (hostPathSpellings' WSL mount form among them): the render-time check
// behind the owner's decision on r1-f13 (DECISIONS.md) that a caption or a
// demo's summary naming one of jig's own directories is left out of a
// published pull request body rather than rendered verbatim. Comparison is
// exact-string only, never a pattern: jig does not try to recognize a path
// shape it did not itself hand out.
func containsHostPath(s string, dirs ...hostDir) bool {
	for _, sp := range hostPathSpellings(true, dirs...) {
		if strings.Contains(s, sp.text) {
			return true
		}
	}
	return false
}

// demoFile is one listed file that passed verification, still under the
// session's own name.
type demoFile struct {
	name    string
	ext     string
	size    int64
	sha256  string
	caption string
}

// demoKind classifies a lowercase extension: "image", "video", or "" for a
// type a demo does not accept.
func demoKind(ext string) string {
	for _, e := range demoImageExts {
		if e == ext {
			return "image"
		}
	}
	for _, e := range demoVideoExts {
		if e == ext {
			return "video"
		}
	}
	return ""
}

// plainDemoFileName reports whether name is a single file name: no path
// separator (either spelling), no drive or stream colon, not a dot name, and
// - via filepath.IsLocal - not a Windows reserved device name.
func plainDemoFileName(name string) bool {
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, "/\\:\x00") {
		return false
	}
	return filepath.IsLocal(name)
}

// lstatPinned is os.Lstat with the file's identity read now. On Windows a
// FileInfo from Lstat holds no file id: os.SameFile opens its path to read
// one the first time it compares, so an identity captured before a swap
// would be read after it, through whatever then sits at that path, and every
// comparison would pass. Comparing the info with itself makes it load the id.
func lstatPinned(path string) (os.FileInfo, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !os.SameFile(info, info) {
		return nil, fmt.Errorf("the identity of %s cannot be read", path)
	}
	return info, nil
}

// verifyDemoMedia checks every listed file against what a demo may record,
// and refuses the whole result on the first that fails one: at most
// demoMaxFiles files; media_dir itself a real directory (never a link or a
// junction a session swapped in) and the very directory jig made (made,
// pinned by lstatPinned when it was made), so a directory above it swapped
// for a link cannot carry the demo outside the evidence tree; each name a
// plain file name listed once; an allowed extension; a regular file, decided
// by Lstat, so a symlink or a Windows junction (which reads as irregular) is
// never followed; not empty (gh refuses an empty file); and within the size
// limit for its kind. The bytes are hashed from the very file Lstat saw.
// Nothing is renamed here.
func verifyDemoMedia(mediaDir string, made os.FileInfo, res DemoResult) ([]demoFile, error) {
	if len(res.Media) > demoMaxFiles {
		return nil, fmt.Errorf("the demo lists %d files; at most %d are accepted", len(res.Media), demoMaxFiles)
	}
	dirInfo, err := lstatPinned(mediaDir)
	if err != nil {
		return nil, fmt.Errorf("media_dir cannot be read: %w", err)
	}
	if dirInfo.Mode().Type() != os.ModeDir {
		return nil, fmt.Errorf("media_dir is not a plain directory (a link or junction is refused)")
	}
	if !os.SameFile(made, dirInfo) {
		return nil, fmt.Errorf("media_dir is not the directory jig made for this demo (it, or a directory above it, was replaced)")
	}

	seen := map[string]bool{}
	files := make([]demoFile, 0, len(res.Media))
	for i, m := range res.Media {
		if !plainDemoFileName(m.File) {
			return nil, fmt.Errorf("%s is not a plain file name directly inside media_dir", demoEntry(i, m.File))
		}
		key := strings.ToLower(m.File)
		if seen[key] {
			return nil, fmt.Errorf("file %q is listed more than once", m.File)
		}
		seen[key] = true

		ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(m.File), "."))
		kind := demoKind(ext)
		if kind == "" {
			return nil, fmt.Errorf("file %q has a type that is not an allowed image (%s) or video (%s) type", m.File, strings.Join(demoImageExts, ", "), strings.Join(demoVideoExts, ", "))
		}
		limit := int64(demoMaxImageBytes)
		if kind == "video" {
			limit = demoMaxVideoBytes
		}

		path := filepath.Join(mediaDir, m.File)
		info, err := lstatPinned(path)
		if err != nil {
			if os.IsNotExist(err) {
				return nil, fmt.Errorf("file %q is not in media_dir", m.File)
			}
			return nil, fmt.Errorf("file %q cannot be read: %w", m.File, err)
		}
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("file %q is not a regular file (a directory, link or junction is refused)", m.File)
		}
		if info.Size() == 0 {
			return nil, fmt.Errorf("file %q is empty", m.File)
		}
		if info.Size() > limit {
			return nil, fmt.Errorf("file %q is %d bytes; a %s may be at most %d", m.File, info.Size(), kind, limit)
		}
		sum, err := hashRegularFile(path, info)
		if err != nil {
			return nil, fmt.Errorf("file %q: %w", m.File, err)
		}
		files = append(files, demoFile{name: m.File, ext: ext, size: info.Size(), sha256: sum, caption: m.Caption})
	}
	return files, nil
}

// hashRegularFile returns the sha256 hex of the file at path, which
// lstatPinned (info) reported as a regular file of that size. It opens the
// file and checks it is the same one and still that size, so a swap or a
// growth between the Lstat and the read is refused instead of hashed.
func hashRegularFile(path string, info os.FileInfo) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil {
		return "", err
	}
	if !os.SameFile(info, opened) {
		return "", fmt.Errorf("changed while it was being checked")
	}
	h := sha256.New()
	n, err := io.Copy(h, io.LimitReader(f, info.Size()+1))
	if err != nil {
		return "", err
	}
	if n != info.Size() {
		return "", fmt.Errorf("changed size while it was being checked")
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// renameDemoMedia renames the accepted files to demo-<n>.<ext>, n from 1 in
// the order the session listed them, and returns them as demo.yaml lists
// them. jig, not the session, chooses the names that later reach a PR. It
// stages every file under a temporary name first, so a session that already
// used a demo-<n> name for a different file in the list (or one that differs
// only by case) cannot make one rename overwrite another's source, and it
// refuses beforehand when an unlisted file already holds a final name.
func renameDemoMedia(mediaDir string, files []demoFile) ([]DemoFile, error) {
	listed := make(map[string]bool, len(files))
	final := make([]string, len(files))
	for i, f := range files {
		listed[strings.ToLower(f.name)] = true
		final[i] = fmt.Sprintf("demo-%d.%s", i+1, f.ext)
	}
	for _, name := range final {
		if listed[strings.ToLower(name)] {
			continue
		}
		if _, err := os.Lstat(filepath.Join(mediaDir, name)); err == nil {
			return nil, fmt.Errorf("media_dir already holds %q, which the result does not list, so no listed file can take that name", name)
		} else if !os.IsNotExist(err) {
			return nil, fmt.Errorf("check media_dir for %q: %w", name, err)
		}
	}

	staged := make([]string, len(files))
	for i, f := range files {
		staged[i] = fmt.Sprintf(".jig-demo-%d.tmp", i)
		if err := os.Rename(filepath.Join(mediaDir, f.name), filepath.Join(mediaDir, staged[i])); err != nil {
			return nil, fmt.Errorf("rename %q: %w", f.name, err)
		}
	}
	out := make([]DemoFile, len(files))
	for i, f := range files {
		if err := os.Rename(filepath.Join(mediaDir, staged[i]), filepath.Join(mediaDir, final[i])); err != nil {
			return nil, fmt.Errorf("rename %q to %q: %w", f.name, final[i], err)
		}
		out[i] = DemoFile{Name: final[i], SHA256: f.sha256, Size: f.size, Caption: f.caption}
	}
	return out, nil
}

// pruneDemoMedia removes every entry of mediaDir that is not one of files,
// the accepted files after their rename, so the directory holds exactly what
// demo.yaml lists: a file the result did not list, a subdirectory, or a link
// is no part of the demo. RemoveAll removes a link as itself and never
// follows it, so what a link points at is left alone.
func pruneDemoMedia(mediaDir string, files []DemoFile) error {
	keep := make(map[string]bool, len(files))
	for _, f := range files {
		keep[f.Name] = true
	}
	entries, err := os.ReadDir(mediaDir)
	if err != nil {
		return fmt.Errorf("read media_dir: %w", err)
	}
	for _, e := range entries {
		if keep[e.Name()] {
			continue
		}
		if err := os.RemoveAll(filepath.Join(mediaDir, e.Name())); err != nil {
			return fmt.Errorf("remove %q from media_dir: %w", e.Name(), err)
		}
	}
	return nil
}
