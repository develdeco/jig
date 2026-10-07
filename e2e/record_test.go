package e2e

// jig's own end-to-end tests record the jig runs they make (ADR 0029, step
// 4). jig sets JIG_RECORD_DIR, an empty directory, in the environment of the
// oracle run at a builder's green and only then; this suite reads it once,
// and while it is set every runJig call also writes a recording of the run
// into it: an animated SVG of what jig printed, and a tag beside it that
// names the test as the scenario and flow and the call's place in the test
// as the step. Unset, nothing is recorded and runJig is what it was.
//
// The recording is a by-product of the run, so it never fails a test: a
// recording that cannot be made or written is logged and dropped.

import (
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

	// maxRecordings is how many recordings a directory may hold: jig refuses
	// the whole collection above 50, so recording stops here rather than
	// lose them all.
	maxRecordings = 50

	// maxTagText bounds a tag's scenario and flow (the most jig accepts is
	// 200 bytes); maxCaptionText bounds its caption (jig accepts 1000, a
	// caption is a line).
	maxTagText     = 200
	maxCaptionText = 200

	// maxNameBytes bounds the test-name part of a recording's file name.
	maxNameBytes = 96
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
	dir string // "" records nothing

	mu      sync.Mutex
	steps   map[string]int // test name -> its last step
	written int            // recordings made or being made
	full    bool           // the cap was reached and logged
}

func newRecordings(dir string) *recordings {
	return &recordings{dir: dir, steps: map[string]int{}}
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
// output, and the exit code when it is not zero.
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
	meta := rec.Stream()
	fmt.Fprintf(meta, "\x1b[1m$ jig %s\x1b[0m\n", shellWords(call.args))
	code := runProcess(io.MultiWriter(stdout, rec.Stream()), io.MultiWriter(stderr, rec.Stream()))
	if code != 0 {
		fmt.Fprintf(meta, "\x1b[31m[exit %d]\x1b[0m\n", code)
	}
	finished = true
	if err := r.write(t.Name(), step, call, rec.Cast()); err != nil {
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
	if r.written >= maxRecordings {
		if !r.full {
			r.full = true
			t.Logf("record: %s holds %d recordings, the most jig accepts; the rest of this run is not recorded", recordDirEnv, maxRecordings)
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

// write renders c, the recording of call, and writes the SVG and its tag
// into the directory. A tag is written only beside an SVG, and an SVG whose
// tag could not be written is removed, so a failure leaves no half.
func (r *recordings) write(test string, step int, call recordCall, c termrec.Cast) error {
	c = fitHeight(redact(c, hostPaths(call)))
	svg, err := c.SVG(termrec.SVGOptions{})
	if err != nil {
		return err
	}
	tag, err := encodeTag(recordingTag{
		Scenario: capText(test, maxTagText),
		Flow:     capText(test, maxTagText),
		Step:     step,
		Caption:  capText(caption(call.args), maxCaptionText),
	})
	if err != nil {
		return err
	}
	name := recordingName(test, step)
	path := filepath.Join(r.dir, name)
	if err := os.WriteFile(path, svg, 0o644); err != nil {
		return err
	}
	if err := os.WriteFile(path+".json", tag, 0o644); err != nil {
		os.Remove(path)
		return err
	}
	return nil
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
// the test's name as a plain file name (letters, digits, '-' and '_'), cut
// short with a hash of the whole name when it is long, then the step. The
// name is the test's, so two tests never share one.
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
	if len(name) > maxNameBytes {
		sum := sha256.Sum256([]byte(test))
		name = strings.TrimRight(name[:maxNameBytes], "-") + "-" + hex.EncodeToString(sum[:4])
	}
	if name == "" {
		name = "test"
	}
	return fmt.Sprintf("%s-%02d.svg", name, step)
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
// the jig home and the user home the call ran with, the directory it ran in,
// the paths it was given, the host's user home and the temp directory.
func hostPaths(call recordCall) []pathName {
	roots := []pathName{{call.env.home, "<jig-home>"}}
	if call.env.userHome != "" {
		roots = append(roots, pathName{call.env.userHome, "<user-home>"})
	}
	roots = append(roots, pathName{call.cwd, dirName(call.cwd)})
	for _, a := range call.args {
		if p, ok := pathArg(a); ok {
			roots = append(roots, pathName{p, dirName(p)})
		}
	}
	if home, err := os.UserHomeDir(); err == nil {
		roots = append(roots, pathName{home, "<user-home>"})
	}
	return append(roots, pathName{os.TempDir(), "<tmp>"})
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

// redact returns c with every host path in its output replaced by its name.
// Each write is read on its own, so a path split across two writes is not
// found; jig prints a path in one write.
func redact(c termrec.Cast, roots []pathName) termrec.Cast {
	re, names := pathMatcher(roots)
	if re == nil {
		return c
	}
	out := c
	out.Events = make([]termrec.Event, len(c.Events))
	for i, e := range c.Events {
		e.Data = re.ReplaceAllStringFunc(e.Data, func(m string) string { return names[foldPath(m)] })
		out.Events[i] = e
	}
	return out
}

// foldPath is the form paths are compared in: Windows has no case in its
// paths.
func foldPath(p string) string {
	if runtime.GOOS == "windows" {
		return strings.ToLower(p)
	}
	return p
}

// pathMatcher compiles roots into one expression that finds any of them in
// the spellings output carries a path in: as it is, with forward slashes, and
// with every backslash doubled as a JSON string writes it; and, for a
// directory that is a link or has a short name, as it resolves. The longest
// spelling wins where two start at the same place, and the first root where
// two are the same. names maps each spelling, folded, to its root's name.
func pathMatcher(roots []pathName) (*regexp.Regexp, map[string]string) {
	names := map[string]string{}
	var spellings []string
	add := func(spelling, name string) {
		key := foldPath(spelling)
		if _, dup := names[key]; !dup {
			names[key] = name
			spellings = append(spellings, spelling)
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
	if len(spellings) == 0 {
		return nil, nil
	}
	sort.SliceStable(spellings, func(i, j int) bool { return len(spellings[i]) > len(spellings[j]) })
	quoted := make([]string, len(spellings))
	for i, s := range spellings {
		quoted[i] = regexp.QuoteMeta(s)
	}
	expr := strings.Join(quoted, "|")
	if runtime.GOOS == "windows" {
		expr = "(?i:" + expr + ")"
	}
	return regexp.MustCompile(expr), names
}
