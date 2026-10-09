package e2e

// jig's own end-to-end tests record the jig runs they make (ADR 0029, step
// 4). jig sets JIG_RECORD_DIR, an empty directory, in the environment of the
// oracle run at a builder's green and only then; this suite reads it once,
// and while it is set every runJig call also writes a recording of the run
// into it: an animated SVG of what jig printed, and a tag beside it that
// names the test as the scenario and flow and the call's place in the test
// as the step. Unset, nothing is recorded and runJig is what it was (the
// jig under test never sees the variable either way).
//
// The recording is a by-product of the run, so it never fails a test: a
// recording that cannot be made or written is logged and dropped.

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
	"runtime"
	"sort"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"github.com/develdeco/jig/internal/termrec"
)

const (
	// recordDirEnv names the variable jig's oracle run sets for the
	// recordings of the scenarios it runs.
	recordDirEnv = "JIG_RECORD_DIR"

	// recordWidth and recordMaxHeight are the terminal a run is recorded on:
	// 100 columns, the width a person reads jig's tables and error text at
	// (a longer line wraps, as on a terminal), and tall enough for the
	// longest output the suite has; the recording is then cut to the rows
	// its output uses, at least recordMinHeight (fitHeight).
	recordWidth     = 100
	recordMaxHeight = 100
	recordMinHeight = 4

	// maxRecordings is how many recordings a directory may hold: the most the
	// collection of an oracle run accepts, each verified on its own. (The 50
	// of gh's --attach binds publish's pick, not the collection.) Recording
	// stops here rather than lose the whole collection.
	maxRecordings = 200

	// maxTagText bounds a tag's scenario and flow (the most jig accepts is
	// 200 bytes); maxCaptionText bounds its caption (jig accepts 1000, a
	// caption is a line).
	maxTagText     = 200
	maxCaptionText = 200

	// maxNameBytes bounds the test-name part of a recording's file name.
	maxNameBytes = 96

	// maxLineHold bounds what a stream holds back waiting for its line to
	// end; a line longer than this is passed on in pieces.
	maxLineHold = 64 << 10
)

// recordT is the part of *testing.T a recording uses, so a test of the
// recording can stand in for it and read what was logged.
type recordT interface {
	Helper()
	Name() string
	Logf(format string, args ...any)
}

// records is this test binary's recordings: the directory the build gave
// the run, read once, or none.
var records = newRecordings(os.Getenv(recordDirEnv))

// recordings is where one test binary writes its recordings: dir, and what
// the tests running side by side have written into it so far.
type recordings struct {
	dir   string // "" records nothing
	limit int    // how many recordings dir may hold

	mu      sync.Mutex
	steps   map[string]int // test name -> its last step
	written int            // recordings made or being made
	full    bool           // the limit was reached and logged
}

func newRecordings(dir string) *recordings {
	return &recordings{dir: dir, limit: maxRecordings, steps: map[string]int{}}
}

// recordCall is one jig run, as the test made it.
type recordCall struct {
	env  jigEnv
	cwd  string
	args []string
}

// run runs the jig process through runProcess, which writes its output to
// the writers it is given and returns its exit code, and returns that code.
// The caller's stdout and stderr are what its test asserts on and receive the
// process's output exactly as it is. While r records, run also tees both
// streams into a recording of the call: the command as a prompt line, the
// output, and the exit code when it is not zero. What the recording shows is
// read a line at a time with the host's paths left out (lineScrubber), so a
// path a pipe read split in two is found whole.
func (r *recordings) run(t recordT, call recordCall, stdout, stderr io.Writer, runProcess func(stdout, stderr io.Writer) int) int {
	t.Helper()
	step, ok := r.reserve(t)
	if !ok {
		return runProcess(stdout, stderr)
	}
	finished := false
	defer func() {
		if !finished { // the test ended in the run (a Fatal): nothing to record
			r.release()
		}
	}()
	rec, err := termrec.NewRecorder(recordWidth, recordMaxHeight)
	if err != nil {
		t.Logf("record %s: %v", t.Name(), err)
		return runProcess(stdout, stderr)
	}
	paths := newPathScrubber(hostPaths(call))
	meta := rec.Stream()
	fmt.Fprintf(meta, "\x1b[1m$ jig %s\x1b[0m\n", paths.scrub(shellWords(call.args)))
	outTee, errTee := newLineScrubber(rec.Stream(), paths), newLineScrubber(rec.Stream(), paths)
	code := runProcess(io.MultiWriter(stdout, outTee), io.MultiWriter(stderr, errTee))
	outTee.Flush()
	errTee.Flush()
	if code != 0 {
		fmt.Fprintf(meta, "\x1b[31m[exit %d]\x1b[0m\n", code)
	}
	finished = true
	if err := r.write(t.Name(), step, paths, call.args, rec.Cast()); err != nil {
		t.Logf("record %s: %v", t.Name(), err)
		r.release()
	}
	return code
}

// reserve takes the next step of t's test and a place among the directory's
// recordings; it reports false when r records nothing or the directory is
// full, which it logs once.
func (r *recordings) reserve(t recordT) (step int, ok bool) {
	if r.dir == "" {
		return 0, false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.written >= r.limit {
		if !r.full {
			r.full = true
			t.Logf("record: %s holds %d recordings, the most a collection accepts; the rest of this run is not recorded", recordDirEnv, r.limit)
		}
		return 0, false
	}
	r.written++
	r.steps[t.Name()]++
	return r.steps[t.Name()], true
}

// release gives back a place a recording did not use.
func (r *recordings) release() {
	r.mu.Lock()
	r.written--
	r.mu.Unlock()
}

// write renders c, the recording of a call with args, and writes the SVG and
// its tag into the directory. jig collects an SVG whether or not its tag is
// there, so none is ever there half written, and no other recording's file is
// ever replaced:
//
//  1. The tag is created exclusively, which claims the file name.
//  2. The SVG is written to <name>.svg.part, which is not a type jig collects.
//  3. The part is renamed to <name>.svg, so the SVG appears whole, tag first.
//
// Whatever fails after step 1 removes what this call made.
func (r *recordings) write(test string, step int, paths *pathScrubber, args []string, c termrec.Cast) error {
	svg, err := fitHeight(c).SVG(termrec.SVGOptions{})
	if err != nil {
		return err
	}
	tag, err := encodeTag(recordingTag{
		Scenario: capText(paths.scrub(test), maxTagText),
		Flow:     capText(paths.scrub(test), maxTagText),
		Step:     step,
		Caption:  capText(paths.scrub(caption(args)), maxCaptionText),
	})
	if err != nil {
		return err
	}
	path := filepath.Join(r.dir, recordingName(test, step))
	if err := createExclusive(path+".json", tag); err != nil {
		return err
	}
	if err := createExclusive(path+".part", svg); err != nil {
		os.Remove(path + ".json")
		return err
	}
	if err := os.Rename(path+".part", path); err != nil {
		os.Remove(path + ".part")
		os.Remove(path + ".json")
		return err
	}
	return nil
}

// createExclusive writes data to a new file at path. It fails, touching
// nothing, when path exists; a file it could not finish writing is removed.
func createExclusive(path string, data []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		return err
	}
	_, err = f.Write(data)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(path)
	}
	return err
}

// fitHeight returns c on the shortest terminal that ends on the same screen:
// the rows its output uses and the cursor's, so a short output is not shown
// above a page of blank rows. A terminal that scrolled less would drop the
// first lines, so each height is checked against the full-height screen;
// an output taller than the recording's terminal keeps it.
func fitHeight(c termrec.Cast) termrec.Cast {
	full, err := c.FinalText()
	if err != nil {
		return c
	}
	tall := c.Height
	for h := max(strings.Count(full, "\n")+2, recordMinHeight); h < tall; h++ {
		c.Height = h
		if got, err := c.FinalText(); err == nil && got == full {
			return c
		}
	}
	c.Height = tall
	return c
}

// recordingTag is the tag file beside a recording, in the fields jig reads.
type recordingTag struct {
	Scenario string `json:"scenario"`
	Flow     string `json:"flow"`
	Step     int    `json:"step"`
	Caption  string `json:"caption"`
}

func encodeTag(tag recordingTag) ([]byte, error) {
	var b strings.Builder
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false) // a placeholder reads <like-this>
	if err := enc.Encode(tag); err != nil {
		return nil, err
	}
	return []byte(b.String()), nil
}

var dashRuns = regexp.MustCompile(`-{2,}`)

// recordingName is the file name of the step'th recording of the named test:
// the test's name as a plain file name (letters, digits, '-' and '_'), then
// the step. A name that had to change to be plain (a subtest's slashes, a
// space) or be cut short gets a hash of the whole name, since two different
// names can come to the same plain one.
func recordingName(test string, step int) string {
	var b strings.Builder
	for _, r := range test {
		switch {
		case r < utf8.RuneSelf && (unicode.IsLetter(r) || unicode.IsDigit(r)), r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	name := strings.Trim(dashRuns.ReplaceAllString(b.String(), "-"), "-")
	changed := name != test
	if len(name) > maxNameBytes {
		name = strings.TrimRight(name[:maxNameBytes], "-")
		changed = true
	}
	if name == "" {
		name = "test"
	}
	if changed {
		name += "-" + nameHash(test)
	}
	return fmt.Sprintf("%s-%02d.svg", name, step)
}

// nameHash is a short hash of a test's whole name.
func nameHash(test string) string {
	sum := sha256.Sum256([]byte(test))
	return hex.EncodeToString(sum[:4])
}

// capText keeps s to at most n bytes, ending at a whole rune and marked with
// "..." when it was cut.
func capText(s string, n int) string {
	if len(s) <= n {
		return s
	}
	n -= len("...")
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n] + "..."
}

// shellWords is args as one command line: a word with a space or a quote in
// it is in single quotes. A path is not: a recording shows it as a name, and
// a Windows path has backslashes and may have spaces.
func shellWords(args []string) string {
	words := make([]string, len(args))
	for i, a := range args {
		words[i] = a
		if _, isPath := pathArg(a); !isPath && (a == "" || strings.ContainsAny(a, " \t\"'")) {
			words[i] = "'" + strings.ReplaceAll(a, "'", `'\''`) + "'"
		}
	}
	return strings.Join(words, " ")
}

// pathArg reports the absolute path an argument is, or ends in after a
// "name=" (as init's --clone takes it).
func pathArg(arg string) (string, bool) {
	if filepath.IsAbs(arg) {
		return arg, true
	}
	if i := strings.IndexByte(arg, '='); i > 0 && filepath.IsAbs(arg[i+1:]) {
		return arg[i+1:], true
	}
	return "", false
}

// caption names a run for a person reading the recording: "jig" and the
// arguments that are not paths. A flag whose value is a path goes with it.
func caption(args []string) string {
	var words []string
	for i, a := range args {
		if _, isPath := pathArg(a); isPath {
			if i > 0 && strings.HasPrefix(args[i-1], "-") && len(words) > 0 && words[len(words)-1] == args[i-1] {
				words = words[:len(words)-1]
			}
			continue
		}
		words = append(words, a)
	}
	return "jig " + shellWords(words)
}

// pathName is one host path and the name a recording shows in its place.
type pathName struct{ path, name string }

// hostPaths lists the paths of this machine a recording of call may carry:
// the jig home and the user home the call ran with, the host's user home and
// temp directory, the product repo and the directory of the jig binary (a
// panic in jig prints its source paths, and jig prints its own), then the
// directory the call ran in and the paths it was given. A path listed twice
// keeps its first name, so the fixed names come before the ones taken from
// the call.
func hostPaths(call recordCall) []pathName {
	roots := []pathName{{call.env.home, "<jig-home>"}}
	if call.env.userHome != "" {
		roots = append(roots, pathName{call.env.userHome, "<user-home>"})
	}
	if home, err := os.UserHomeDir(); err == nil {
		roots = append(roots, pathName{home, "<user-home>"})
	}
	roots = append(roots, pathName{os.TempDir(), "<tmp>"}, pathName{repoRoot, "<repo>"})
	if jigBinary != "" {
		roots = append(roots, pathName{filepath.Dir(jigBinary), "<jig-bin>"})
	}
	roots = append(roots, pathName{call.cwd, dirName(call.cwd)})
	for _, a := range call.args {
		if p, ok := pathArg(a); ok {
			roots = append(roots, pathName{p, dirName(p)})
		}
	}
	return roots
}

// dirName is how a recording names the directory at path: by its last
// element, in angle brackets, or <dir> when that says nothing (a numbered
// temp directory).
func dirName(path string) string {
	base := filepath.Base(path)
	if !strings.ContainsFunc(base, unicode.IsLetter) {
		return "<dir>"
	}
	return "<" + base + ">"
}

// pathScrubber replaces the host paths in text with their names.
type pathScrubber struct {
	spellings []string          // longest first, folded
	names     map[string]string // folded spelling -> its root's name
	first     [256]bool         // the first bytes a spelling starts with
	fold      bool              // paths have no case here
}

// newPathScrubber compiles roots into a scrubber that finds each in the
// spellings output carries a path in: as it is, with forward slashes, and
// with every backslash doubled as a JSON string writes it; and, for a
// directory that is a link or has a short name, as it resolves. The longest
// spelling wins where two start at the same place, and the first root where
// two are the same.
func newPathScrubber(roots []pathName) *pathScrubber {
	s := &pathScrubber{names: map[string]string{}, fold: runtime.GOOS == "windows"}
	add := func(spelling, name string) {
		key := s.key(spelling)
		if _, dup := s.names[key]; !dup {
			s.names[key] = name
			s.spellings = append(s.spellings, key)
			s.first[key[0]] = true
		}
	}
	for _, root := range roots {
		if root.path == "" {
			continue
		}
		forms := []string{filepath.Clean(root.path)}
		if resolved, err := filepath.EvalSymlinks(forms[0]); err == nil && resolved != forms[0] {
			forms = append(forms, resolved)
		}
		for _, form := range forms {
			// A whole file system root would blank every path separator.
			if len(form) < 3 || filepath.Dir(form) == form {
				continue
			}
			add(form, root.name)
			add(filepath.ToSlash(form), root.name)
			add(strings.ReplaceAll(form, `\`, `\\`), root.name)
		}
	}
	sort.SliceStable(s.spellings, func(i, j int) bool { return len(s.spellings[i]) > len(s.spellings[j]) })
	return s
}

// key is the form paths are compared in. Windows has no case in its paths;
// only ASCII is folded, so the key is as long as the text it stands for.
func (s *pathScrubber) key(text string) string {
	if !s.fold {
		return text
	}
	b := []byte(text)
	for i, c := range b {
		if 'A' <= c && c <= 'Z' {
			b[i] = c + 'a' - 'A'
		}
	}
	return string(b)
}

// scrub returns text with every root it holds replaced by its name. A root
// is found only where it ends a path component: the text after it is the end,
// a separator, a quote, a space or other punctuation, never a letter, digit,
// '_' or '-', so "/tmp" is found in "/tmp/x" and not in "/tmpl".
func (s *pathScrubber) scrub(text string) string {
	if len(s.spellings) == 0 {
		return text
	}
	folded := s.key(text)
	var b strings.Builder
	last := 0
	for i := 0; i < len(text); {
		if sp := s.matchAt(text, folded, i); sp != "" {
			b.WriteString(text[last:i])
			b.WriteString(s.names[sp])
			i += len(sp)
			last = i
			continue
		}
		i++
	}
	if last == 0 {
		return text
	}
	b.WriteString(text[last:])
	return b.String()
}

// matchAt returns the longest spelling that is in folded at i and ends a path
// component in text, or "".
func (s *pathScrubber) matchAt(text, folded string, i int) string {
	if !s.first[folded[i]] {
		return ""
	}
	for _, sp := range s.spellings {
		if !strings.HasPrefix(folded[i:], sp) {
			continue
		}
		end := i + len(sp)
		if end == len(text) {
			return sp
		}
		if r, _ := utf8.DecodeRuneInString(text[end:]); !unicode.IsLetter(r) && !unicode.IsDigit(r) && r != '_' && r != '-' {
			return sp
		}
	}
	return ""
}

// lineScrubber writes to w what is written to it with the host's paths left
// out, a line at a time: it holds what follows the last line feed until the
// line ends, so a path that one write split in two is found whole. It holds
// at most maxLineHold bytes, and Flush passes on the rest.
type lineScrubber struct {
	mu   sync.Mutex
	w    io.Writer
	s    *pathScrubber
	held []byte
}

func newLineScrubber(w io.Writer, s *pathScrubber) *lineScrubber {
	return &lineScrubber{w: w, s: s}
}

// Write holds p's last, unfinished line and writes the lines before it. It
// never fails.
func (l *lineScrubber) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.held = append(l.held, p...)
	if i := bytes.LastIndexByte(l.held, '\n'); i >= 0 {
		l.emit(l.held[:i+1])
		l.held = append(l.held[:0], l.held[i+1:]...)
	}
	if len(l.held) > maxLineHold {
		l.emit(l.held)
		l.held = l.held[:0]
	}
	return len(p), nil
}

// Flush writes the line still held, ended or not.
func (l *lineScrubber) Flush() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.emit(l.held)
	l.held = l.held[:0]
}

// emit writes b, scrubbed. The recorder behind it never fails.
func (l *lineScrubber) emit(b []byte) {
	if len(b) > 0 {
		_, _ = io.WriteString(l.w, l.s.scrub(string(b)))
	}
}
