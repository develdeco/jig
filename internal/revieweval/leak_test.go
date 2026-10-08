package revieweval

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"

	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/session"
	"github.com/develdeco/jig/internal/verifydeliver"
)

// leakVocabulary is the fixed set of words nothing a live dispatch reads or
// is identified by may ever equal, as a whole token: what this package
// calls itself, what a case's building blocks are called, and how a seeded
// problem got its name. It is deliberately not "eval-ish" prose detection -
// only these exact words, lowercase, plus the case's own name (checked
// separately, as a substring, in assertNoLeak).
var leakVocabulary = map[string]bool{
	"eval":       true,
	"revieweval": true,
	"fixture":    true,
	"trap":       true,
	"gold":       true,
	"seeded":     true,
}

// leakTokens splits s on every rune that is not a letter or digit -
// strings.FieldsFunc, never regexp, the same restriction the rest of this
// package holds to - and lowercases each piece.
func leakTokens(s string) []string {
	pieces := strings.FieldsFunc(s, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	for i, p := range pieces {
		pieces[i] = strings.ToLower(p)
	}
	return pieces
}

// leakScan is one leak check's settings: the temp roots it leaves out of
// the text it reads (roots, see withoutTempRoot) and the temp roots that
// must not be left in it (foreign, see strayTempRoot). A test builds its
// own, so tests that run side by side each scan against the roots their own
// run used, never a list the tests share.
type leakScan struct {
	roots   []string
	foreign []string
}

// launchLeakScan leaves out the operator's launch temp root and nothing
// else: the scan for text whose paths sit under the temp root the test
// binary started with.
func launchLeakScan() leakScan {
	return leakScan{roots: spellingsOf(launchTempRoot)}
}

// holds reports whether p sits strictly inside one of the scan's roots. It
// compares the cleaned paths (filepath.Rel), so a path that climbs out with
// ".." is not inside, nor is a sibling that merely starts with the root's
// name, nor the root itself.
func (s leakScan) holds(p string) bool {
	for _, r := range s.roots {
		rel, err := filepath.Rel(r, p)
		if err != nil || rel == "." || rel == ".." || filepath.IsAbs(rel) || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			continue
		}
		return true
	}
	return false
}

// hits returns one line per leak in text: caseName as a literal
// substring (case-insensitive: the hyphenated case name itself, exactly
// as a session reading its own dispatch or worktree would see it) or any
// token equal to leakVocabulary. label names what text is, so a failure
// can be placed without re-running the test. A temp root left in text once
// the scan's own roots are gone (strayTempRoot) is a leak too.
func (s leakScan) hits(label, caseName, text string) []string {
	if text == "" {
		return nil
	}
	text = s.withoutTempRoot(text)
	var hits []string
	if s.strayTempRoot(text) {
		hits = append(hits, fmt.Sprintf("%s: contains a temp path the run was not given", label))
	}
	if caseName != "" && strings.Contains(strings.ToLower(text), strings.ToLower(caseName)) {
		hits = append(hits, fmt.Sprintf("%s: contains the case name %q", label, caseName))
	}
	for _, tok := range leakTokens(text) {
		if leakVocabulary[tok] {
			hits = append(hits, fmt.Sprintf("%s: contains leak token %q", label, tok))
		}
	}
	return hits
}

// withoutTempRoot removes the operator's temp root from text before it is
// scanned. Every path the eval makes sits under the temp root, which is the
// operator's own ambient environment, outside what the eval scrubs: a
// temp root that happened to be named after a leak word would otherwise
// fail every check on every path, whatever jig did. Each spelling a
// session could see is removed: the root as given, with symlinks
// resolved, and on Windows each of those with its backslashes doubled, as
// a JSON file such as review.json writes them. Windows compares paths
// case-insensitively, so there the text is lowercased first; the leak
// check lowercases every token anyway.
func (s leakScan) withoutTempRoot(text string) string {
	windows := runtime.GOOS == "windows"
	if windows {
		text = strings.ToLower(text)
	}
	for _, r := range s.roots {
		if windows {
			r = strings.ToLower(r)
			text = replaceAtWordEnd(text, strings.ReplaceAll(r, `\`, `\\`))
		}
		text = replaceAtWordEnd(text, r)
	}
	return text
}

// strayTempRoot reports whether text, already stripped by withoutTempRoot,
// still holds a spelling of a foreign root: a path under a temp directory
// the run made for itself instead of taking the one it was given. It reads
// the spellings the way withoutTempRoot removes them, so a root that only
// begins a longer word is not one.
func (s leakScan) strayTempRoot(text string) bool {
	windows := runtime.GOOS == "windows"
	for _, r := range s.foreign {
		if windows {
			r = strings.ToLower(r)
			if replaceAtWordEnd(text, strings.ReplaceAll(r, `\`, `\\`)) != text {
				return true
			}
		}
		if replaceAtWordEnd(text, r) != text {
			return true
		}
	}
	return false
}

// replaceAtWordEnd replaces each occurrence of root in text with a space
// where root ends a word for the leak tokenizer: the text ends there, or
// the next character is neither a letter nor a digit. Anywhere else the
// root and what follows it are one word, and are left whole for the
// tokenizer to read as one: a root ending in a letter (macOS's ".../T")
// must not cut a sibling "Trap" down to "rap", and "/opt/eval" + "/tmp1"
// must not become "/opt/eval1". Since only a root followed by a non-word
// character is replaced, the text on either side of it can never merge
// into one token. Everything the eval writes under a temp root is the
// root followed by a separator, so every one of its paths is stripped.
func replaceAtWordEnd(text, root string) string {
	if root == "" {
		return text
	}
	var b strings.Builder
	for {
		i := strings.Index(text, root)
		if i < 0 {
			b.WriteString(text)
			return b.String()
		}
		end := i + len(root)
		b.WriteString(text[:i])
		next, _ := utf8.DecodeRuneInString(text[end:])
		if end == len(text) || !(unicode.IsLetter(next) || unicode.IsDigit(next)) {
			b.WriteString(" ")
		} else {
			b.WriteString(root)
		}
		text = text[end:]
	}
}

// spellingsOf returns roots, in order, each as given and with symlinks
// resolved, skipping empty ones. A leakScan lists its temp roots this way,
// once when it is built, not on every check. The scan of a hostile run
// (hostileTempRoot) removes only the hostile root, and flags the launch
// temp root, which it sits under: every path the run makes is under the
// hostile root, so one under the launch root alone is a temp directory the
// run was not handed. The hostile root is removed before the launch root is
// looked for because it sits under it, and the launch root alone would
// leave its name behind.
func spellingsOf(roots ...string) []string {
	var out []string
	for _, root := range roots {
		if root == "" {
			continue
		}
		root = filepath.Clean(root)
		out = append(out, root)
		if resolved, err := filepath.EvalSymlinks(root); err == nil && resolved != root {
			out = append(out, resolved)
		}
	}
	return out
}

// hostileTempRoot makes a fresh directory, directly under the operator's
// launch temp root, whose name holds a leak word, removed when the test
// ends, and returns it with the scan for a run made under it. The test
// hands the directory to everything that makes a path: the work dir it
// creates in it and runCase's scratch parent. The scan leaves out only that
// directory and flags the launch temp root, and the test requires every
// dispatch path to sit inside it (leakScan.holds), so the run passes only if
// every temp path it makes came from the directory it was given, and its
// checks leave that directory out, on every host, instead of passing where
// the temp root happens to be harmless. A run that makes a temp directory of
// its own (os.MkdirTemp("", ...) lands under the launch root) fails. The
// directory is passed along rather than set as TMP, TEMP and TMPDIR: that
// changes the whole process and would move every test running beside this
// one.
func hostileTempRoot(t *testing.T) (string, leakScan) {
	t.Helper()
	dir, err := os.MkdirTemp(launchTempRoot, "eval-tmp-")
	if err != nil {
		t.Fatalf("create hostile temp root: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir, leakScan{roots: spellingsOf(dir), foreign: spellingsOf(launchTempRoot)}
}

// assertNoLeak fails t for every leak s finds in text.
func (s leakScan) assertNoLeak(t *testing.T, label, caseName, text string) {
	t.Helper()
	for _, h := range s.hits(label, caseName, text) {
		t.Error(h)
	}
}

// hitsUnder walks root (tracked and untracked files alike - a plain
// directory walk sees both) and returns every leak in a directory's name, a
// file's path relative to root, or a file's content, skipping every .git
// directory: nothing a session's read tools could reach under root is
// exempt, and a directory's name is visible even when it is empty. A
// session's reads are not bounded to its worktree, so callers walk the
// whole work root too - the store beside it (project.yaml, the ticket's
// brief, slices, journal and seeded gate records).
func (s leakScan) hitsUnder(caseName, root, what string) ([]string, error) {
	var hits []string
	err := filepath.WalkDir(root, func(p string, de fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, rerr := filepath.Rel(root, p)
		if rerr != nil {
			return rerr
		}
		if rel == "." {
			return nil
		}
		if de.IsDir() {
			if de.Name() == ".git" {
				return filepath.SkipDir
			}
			hits = append(hits, s.hits(what+" dir "+rel, caseName, rel)...)
			return nil
		}
		hits = append(hits, s.hits(what+" path "+rel, caseName, rel)...)
		data, rerr := os.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		hits = append(hits, s.hits(what+" file "+rel, caseName, string(data))...)
		return nil
	})
	return hits, err
}

// assertNoLeakUnder fails t for every leak s finds under root.
func (s leakScan) assertNoLeakUnder(t *testing.T, label, caseName, root, what string) {
	t.Helper()
	hits, err := s.hitsUnder(caseName, root, what)
	if err != nil {
		t.Fatalf("%s: walk %s: %v", label, what, err)
	}
	for _, h := range hits {
		t.Error(label + " " + h)
	}
}

// TestLeakWalkChecksDirectoryNames pins that the walk reads directory
// names, not only files: an empty directory with a telling name is still
// something a session listing its surroundings would see.
func TestLeakWalkChecksDirectoryNames(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	for _, d := range []string{"gold", filepath.Join("a", "seeded")} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	hits, err := launchLeakScan().hitsUnder("nil-deref", root, "root")
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
	joined := strings.Join(hits, "; ")
	for _, want := range []string{`"gold"`, `"seeded"`} {
		if !strings.Contains(joined, want) {
			t.Errorf("no hit for the empty directory token %s; hits: %s", want, joined)
		}
	}
}

// assertNoLeakInGitLog checks worktree's own git log, author through body,
// the same fields a session's own `git log` could read.
func (s leakScan) assertNoLeakInGitLog(t *testing.T, label, caseName, worktree string) {
	t.Helper()
	out, err := gitx.Run(worktree, "log", "--format=%an %ae %cn %ce %s %b")
	if err != nil {
		t.Fatalf("%s: git log: %v", label, err)
	}
	s.assertNoLeak(t, label+" git log", caseName, out)
}

// leakVerdictEntry and leakVerdictsFile are leakCapturingBackend's own
// verdicts.json shape for a judge dispatch: every candidate answered Same,
// so a perfect-fixture round scores the same as it would with a nil judge
// (every "same" field shifts equally, never changing which edge wins) while
// still exercising a real judge dispatch's own leak surfaces.
type leakVerdictEntry struct {
	Candidate int  `json:"candidate"`
	Same      bool `json:"same"`
}

type leakVerdictsFile struct {
	Verdicts []leakVerdictEntry `json:"verdicts"`
}

// leakCapturingBackend answers a "gate" dispatch from the perfect fixture
// set and a "judge" dispatch by confirming every candidate Same, checking
// every surface a session can read before it answers either: the prompt, the
// dispatch paths relative to workDir, the dispatched slice file's own
// content, every file in the dispatch's own worktree and under the whole
// work root, and the worktree's git log.
type leakCapturingBackend struct {
	t          *testing.T
	scan       leakScan
	caseName   string
	workDir    string // this case's own runCase workDir
	tempRoot   string // the hostile temp root the run was given: every path of every dispatch sits inside it
	fixtureDir string // resultsDir("perfect")
	briefPath  string // the case's own brief.md source (Case.BriefPath)
}

func (b leakCapturingBackend) Run(d session.Dispatch) error {
	t := b.t
	label := fmt.Sprintf("case %s round %d %s dispatch", b.caseName, d.Attempt, d.Slice)

	b.scan.assertNoLeak(t, label+" prompt", b.caseName, d.Prompt)

	// Every path of every dispatch, the judge's included, sits inside the
	// temp root the run was given: the test fails on one that does not,
	// however it climbs out or where it was made. What is left to scan of a
	// path is the part below that root.
	paths := []string{d.SliceJSON, d.ResultJSON, d.Worktree}
	if d.ExtraWriteDir != "" {
		paths = append(paths, d.ExtraWriteDir)
	}
	for _, p := range paths {
		if !b.scan.holds(p) {
			t.Errorf("%s: dispatch path %q is not inside the temp root %q the run was given", label, p, b.tempRoot)
			continue
		}
		rel, err := filepath.Rel(b.tempRoot, p)
		if err != nil {
			t.Fatalf("%s: relativize %s to the temp root: %v", label, p, err)
		}
		b.scan.assertNoLeak(t, label+" dispatch path "+rel, b.caseName, rel)
	}

	sliceData, err := os.ReadFile(d.SliceJSON)
	if err != nil {
		return fmt.Errorf("leakCapturingBackend: read %s: %w", d.SliceJSON, err)
	}
	b.scan.assertNoLeak(t, label+" "+filepath.Base(d.SliceJSON)+" content", b.caseName, string(sliceData))

	b.scan.assertNoLeakUnder(t, label, b.caseName, d.Worktree, "worktree")
	b.scan.assertNoLeakUnder(t, label, b.caseName, b.workDir, "work root")
	b.scan.assertNoLeakInGitLog(t, label, b.caseName, d.Worktree)

	switch d.Slice {
	case "gate":
		// Pin the intent revieweval dispatches, the same one leakVocabulary
		// checks are otherwise silent about: every case round must dispatch
		// with source "brief", pointing at the store's own copy of the
		// case's brief.md (the comment in RunCase where it builds that
		// intent says why), never a missing or silently-defaulted intent a
		// reviewer would never notice from the prompt alone.
		var req verifydeliver.ReviewRequest
		if err := json.Unmarshal(sliceData, &req); err != nil {
			return fmt.Errorf("leakCapturingBackend: parse review.json: %w", err)
		}
		if req.Intent.Source != verifydeliver.IntentSourceBrief {
			t.Errorf("%s: review.json intent.source = %q, want %q", label, req.Intent.Source, verifydeliver.IntentSourceBrief)
		}
		if !filepath.IsAbs(req.Intent.Path) {
			t.Errorf("%s: review.json intent.path = %q, want an absolute path", label, req.Intent.Path)
		}
		gotBrief, err := os.ReadFile(req.Intent.Path)
		if err != nil {
			t.Errorf("%s: read review.json intent.path %s: %v", label, req.Intent.Path, err)
		}
		wantBrief, err := os.ReadFile(b.briefPath)
		if err != nil {
			return fmt.Errorf("leakCapturingBackend: read case brief %s: %w", b.briefPath, err)
		}
		if !bytes.Equal(gotBrief, wantBrief) {
			t.Errorf("%s: review.json intent.path bytes differ from the case's own brief.md", label)
		}

		caseName, err := caseNameForRunID(b.fixtureDir, d.Ticket)
		if err != nil {
			return err
		}
		path := filepath.Join(b.fixtureDir, caseName, fmt.Sprintf("round-%d.json", d.Attempt))
		data, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("leakCapturingBackend: no scripted result at %s: %w", path, err)
		}
		return os.WriteFile(d.ResultJSON, data, 0o644)
	case "judge":
		var jf judgeFileJSON
		if err := json.Unmarshal(sliceData, &jf); err != nil {
			return fmt.Errorf("leakCapturingBackend: parse judge.json: %w", err)
		}
		verdicts := leakVerdictsFile{Verdicts: make([]leakVerdictEntry, len(jf.Candidates))}
		for i := range verdicts.Verdicts {
			verdicts.Verdicts[i] = leakVerdictEntry{Candidate: i, Same: true}
		}
		out, err := json.Marshal(verdicts)
		if err != nil {
			return fmt.Errorf("leakCapturingBackend: marshal verdicts.json: %w", err)
		}
		return os.WriteFile(d.ResultJSON, out, 0o644)
	default:
		return fmt.Errorf("leakCapturingBackend: unexpected slice %q", d.Slice)
	}
}

// TestRunCaseNeverLeaksTheCorpusVocabulary extends
// TestRunCaseNeverNamesTheCaseToTheReviewerOrJudge (one case, the case name
// alone) to every case in the real corpus, run through the perfect fixtures
// with a real gate dispatch and a real judge dispatch every round, checking
// the wider leakVocabulary - not only the case's own name - at every
// surface a session with that dispatch and that worktree could read: the
// prompt; the dispatch paths relative to the work root; the dispatched
// slice file's own content; every file in the worktree, tracked and
// untracked, .git excluded; every file under the store's own ticket dir the
// request points at; and the worktree's own git log.
func TestRunCaseNeverLeaksTheCorpusVocabulary(t *testing.T) {
	t.Parallel()
	hostile, scan := hostileTempRoot(t)
	cases, err := LoadCorpus(evalCorpusRoot(t))
	if err != nil {
		t.Fatalf("LoadCorpus: %v", err)
	}
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			t.Parallel()
			// Not t.TempDir(): its own directory is named after the
			// subtest, which this loop names after the case - exactly the
			// kind of path leak this test exists to catch, this time from
			// its own harness rather than production code. review.json
			// embeds workDir-derived absolute paths (the intent path and
			// friends), so a t.TempDir() here would fail every case
			// through the harness's own doing. It sits under the hostile
			// temp root, as the judge's scratch root does (runCase below).
			workDir, err := os.MkdirTemp(hostile, "jig-")
			if err != nil {
				t.Fatalf("create work dir: %v", err)
			}
			defer func() {
				if rerr := os.RemoveAll(workDir); rerr != nil {
					t.Logf("remove work dir %s: %v", workDir, rerr)
				}
			}()

			backend := leakCapturingBackend{t: t, scan: scan, caseName: c.Name, workDir: workDir, tempRoot: hostile, fixtureDir: resultsDir("perfect"), briefPath: c.BriefPath}
			judge := &ModelJudge{Backend: backend, Model: "fixture-model"}

			cs, err := runCase(workDir, hostile, c, backend, judge, "model-a")
			if err != nil {
				t.Fatalf("RunCase: %v", err)
			}
			if !cs.Passed {
				t.Errorf("case %s: Passed = false, want true (perfect fixtures, a judge confirming every candidate Same): %+v", c.Name, cs.Rounds)
			}
		})
	}
}

// TestLeakHitsStripsTheTempRootOnlyWhereItEndsAWord pins how the temp
// root is left out, with synthetic roots so every host runs the same
// cases: a path under the root is scanned from the root on, a root that
// itself names a leak word is dropped whole, a sibling that merely starts
// with the root's last letters is read as the word it is, and removing a
// root never glues the text around it into one token.
func TestLeakHitsStripsTheTempRootOnlyWhereItEndsAWord(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, root, text string
		hit              bool
	}{
		{"a-path-under-the-root-is-scanned-after-it", "/tmp", "/tmp/eval-tmp-1/x", true},
		{"a-clean-path-under-the-root-has-no-hit", "/tmp", "/tmp/jig-1/repo", false},
		{"a-root-naming-a-leak-word-is-dropped-whole", "/tmp/eval-tmp-9", "/tmp/eval-tmp-9/jig-1/x", false},
		{"a-sibling-sharing-the-roots-last-letters-is-its-own-word", "/var/folders/ab/xyz/T", "/var/folders/ab/xyz/Trap/notes.txt", true},
		{"text-around-a-removed-root-never-merges", "/tmp", "/opt/eval/tmp1/x", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			scan := leakScan{roots: []string{filepath.FromSlash(tc.root)}}
			text := filepath.FromSlash(tc.text)
			if got := len(scan.hits("t", "", text)) > 0; got != tc.hit {
				t.Errorf("hits(%q) with root %q: hit = %v, want %v", text, tc.root, got, tc.hit)
			}
		})
	}
}

// TestLeakScanFlagsATempPathTheRunWasNotGiven pins the foreign roots, with
// synthetic roots so every host runs the same cases: once the run's own root
// is removed, a path left under the foreign root is a temp directory the run
// made for itself, in either spelling a file may carry it (the second
// spelling doubles the backslashes, as a JSON file does on Windows), while a
// path under the given root, or a name that only begins with the foreign
// root's, is not.
func TestLeakScanFlagsATempPathTheRunWasNotGiven(t *testing.T) {
	t.Parallel()
	scan := leakScan{
		roots:   []string{filepath.FromSlash("/tmp/eval-tmp-1")},
		foreign: []string{filepath.FromSlash("/tmp")},
	}
	for _, tc := range []struct {
		name, text string
		stray      bool
	}{
		{"a-path-under-the-given-root-is-not-stray", "/tmp/eval-tmp-1/jig-9/repo", false},
		{"a-path-under-another-temp-directory-is-stray", "/tmp/jig-9/repo", true},
		{"a-stray-path-beside-a-given-one-is-stray", "/tmp/eval-tmp-1/a and /tmp/jig-9", true},
		{"a-name-that-only-begins-like-the-foreign-root-is-not-stray", "/tmpfile/x", false},
		{"text-with-no-path-is-not-stray", "nothing to see", false},
	} {
		for _, spelling := range []string{"as-written", "backslashes-doubled"} {
			t.Run(tc.name+"/"+spelling, func(t *testing.T) {
				t.Parallel()
				text := filepath.FromSlash(tc.text)
				if spelling == "backslashes-doubled" {
					text = strings.ReplaceAll(text, `\`, `\`)
				}
				stray := false
				for _, h := range scan.hits("t", "", text) {
					if strings.Contains(h, "a temp path the run was not given") {
						stray = true
					}
				}
				if stray != tc.stray {
					t.Errorf("hits(%q): stray = %v, want %v", text, stray, tc.stray)
				}
			})
		}
	}
}

// TestLeakScanHoldsOnlyPathsStrictlyInsideItsRoots pins the containment
// check the dispatch paths are held to: a real comparison of cleaned paths,
// so a path that climbs out of the root, a sibling that starts with the
// root's name, the root itself and a relative path are none of them inside.
func TestLeakScanHoldsOnlyPathsStrictlyInsideItsRoots(t *testing.T) {
	t.Parallel()
	scan := leakScan{roots: []string{filepath.FromSlash("/tmp/eval-tmp-1")}}
	for _, tc := range []struct {
		name, path string
		inside     bool
	}{
		{"a-path-below-the-root", "/tmp/eval-tmp-1/jig-9/repo", true},
		{"a-path-that-climbs-out-and-back-in", "/tmp/eval-tmp-1/a/../jig-9", true},
		{"the-root-itself", "/tmp/eval-tmp-1", false},
		{"a-path-that-climbs-out", "/tmp/eval-tmp-1/../jig-9", false},
		{"a-path-that-climbs-out-further", "/tmp/eval-tmp-1/jig-9/../../x", false},
		{"a-sibling-starting-with-the-roots-name", "/tmp/eval-tmp-10/x", false},
		{"a-path-elsewhere", "/tmp/jig-9", false},
		{"a-relative-path", "jig-9/repo", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := scan.holds(filepath.FromSlash(tc.path)); got != tc.inside {
				t.Errorf("holds(%q) = %v, want %v", tc.path, got, tc.inside)
			}
		})
	}
}
