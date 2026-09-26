package revieweval

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"unicode"

	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/session"
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

// leakHits returns one line per leak in text: caseName as a literal
// substring (case-insensitive: the hyphenated case name itself, exactly
// as a session reading its own dispatch or worktree would see it) or any
// token equal to leakVocabulary. label names what text is, so a failure
// can be placed without re-running the test.
func leakHits(label, caseName, text string) []string {
	if text == "" {
		return nil
	}
	text = withoutTempRoot(text)
	var hits []string
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
func withoutTempRoot(text string) string {
	windows := runtime.GOOS == "windows"
	if windows {
		text = strings.ToLower(text)
	}
	// A space, never "", takes the root's place: removing it outright
	// would glue the text on either side into one token ("eval" + "/tmp1"
	// reading as "eval1"), hiding a leak word next to the root.
	for _, r := range tempRootSpellings {
		if windows {
			r = strings.ToLower(r)
			text = strings.ReplaceAll(text, strings.ReplaceAll(r, `\`, `\\`), " ")
		}
		text = strings.ReplaceAll(text, r, " ")
	}
	return text
}

// tempRootSpellings lists the temp roots withoutTempRoot removes: the
// hostile root the running test chose (useHostileTempRoot), then the
// operator's launch temp root (launchTempRoot), each as given and with
// symlinks resolved. Only those two: a temp root anything else chose
// after launch is not the operator's, so a path under it stays in the
// scanned text. The hostile root comes first because it sits under the
// launch root, and removing the launch root first would leave its name
// behind. It is computed when either root is set, not on every check.
var tempRootSpellings []string

// spellingsOf returns roots, in order, each as given and with symlinks
// resolved, skipping empty ones.
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

// useHostileTempRoot points the test's temp root (TMP, TEMP, TMPDIR) at a
// fresh directory, directly under the operator's launch temp root, whose
// name holds a leak word, for the rest of the test. Every path the eval
// makes then carries that word, so a leak test passes only if its checks
// leave the temp root out, on every host, instead of passing only where
// the temp root happens to be harmless.
func useHostileTempRoot(t *testing.T) {
	t.Helper()
	dir, err := os.MkdirTemp(launchTempRoot, "eval-tmp-")
	if err != nil {
		t.Fatalf("create hostile temp root: %v", err)
	}
	// Tests in this package never run in parallel, so one package-level
	// list serves the running test.
	tempRootSpellings = spellingsOf(dir, launchTempRoot)
	t.Cleanup(func() {
		tempRootSpellings = spellingsOf(launchTempRoot)
		_ = os.RemoveAll(dir)
	})
	for _, k := range []string{"TMP", "TEMP", "TMPDIR"} {
		t.Setenv(k, dir)
	}
}

// assertNoLeak fails t for every leak leakHits finds in text.
func assertNoLeak(t *testing.T, label, caseName, text string) {
	t.Helper()
	for _, h := range leakHits(label, caseName, text) {
		t.Error(h)
	}
}

// leakHitsUnder walks root (tracked and untracked files alike - a plain
// directory walk sees both) and returns every leak in a directory's name, a
// file's path relative to root, or a file's content, skipping every .git
// directory: nothing a session's read tools could reach under root is
// exempt, and a directory's name is visible even when it is empty. A
// session's reads are not bounded to its worktree, so callers walk the
// whole work root too - the store beside it (project.yaml, the ticket's
// brief, slices, journal and seeded gate records).
func leakHitsUnder(caseName, root, what string) ([]string, error) {
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
			hits = append(hits, leakHits(what+" dir "+rel, caseName, rel)...)
			return nil
		}
		hits = append(hits, leakHits(what+" path "+rel, caseName, rel)...)
		data, rerr := os.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		hits = append(hits, leakHits(what+" file "+rel, caseName, string(data))...)
		return nil
	})
	return hits, err
}

// assertNoLeakUnder fails t for every leak leakHitsUnder finds under root.
func assertNoLeakUnder(t *testing.T, label, caseName, root, what string) {
	t.Helper()
	hits, err := leakHitsUnder(caseName, root, what)
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
	root := t.TempDir()
	for _, d := range []string{"gold", filepath.Join("a", "seeded")} {
		if err := os.MkdirAll(filepath.Join(root, d), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	hits, err := leakHitsUnder("nil-deref", root, "root")
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
func assertNoLeakInGitLog(t *testing.T, label, caseName, worktree string) {
	t.Helper()
	out, err := gitx.Run(worktree, "log", "--format=%an %ae %cn %ce %s %b")
	if err != nil {
		t.Fatalf("%s: git log: %v", label, err)
	}
	assertNoLeak(t, label+" git log", caseName, out)
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
	caseName   string
	workDir    string // this case's own RunCase workDir
	fixtureDir string // resultsDir("perfect")
}

func (b leakCapturingBackend) Run(d session.Dispatch) error {
	t := b.t
	label := fmt.Sprintf("case %s round %d %s dispatch", b.caseName, d.Attempt, d.Slice)

	assertNoLeak(t, label+" prompt", b.caseName, d.Prompt)

	for _, p := range []string{d.SliceJSON, d.ResultJSON, d.Worktree} {
		rel, err := filepath.Rel(b.workDir, p)
		if err != nil {
			t.Fatalf("%s: relativize %s to the work root: %v", label, p, err)
		}
		assertNoLeak(t, label+" dispatch path "+rel, b.caseName, rel)
	}

	sliceData, err := os.ReadFile(d.SliceJSON)
	if err != nil {
		return fmt.Errorf("leakCapturingBackend: read %s: %w", d.SliceJSON, err)
	}
	assertNoLeak(t, label+" "+filepath.Base(d.SliceJSON)+" content", b.caseName, string(sliceData))

	assertNoLeakUnder(t, label, b.caseName, d.Worktree, "worktree")
	assertNoLeakUnder(t, label, b.caseName, b.workDir, "work root")
	assertNoLeakInGitLog(t, label, b.caseName, d.Worktree)

	switch d.Slice {
	case "gate":
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
	useHostileTempRoot(t)
	cases, err := LoadCorpus(evalCorpusRoot(t))
	if err != nil {
		t.Fatalf("LoadCorpus: %v", err)
	}
	for _, c := range cases {
		t.Run(c.Name, func(t *testing.T) {
			// Not t.TempDir(): its own directory is named after the
			// subtest, which this loop names after the case - exactly the
			// kind of path leak this test exists to catch, this time from
			// its own harness rather than production code. review.json
			// embeds workDir-derived absolute paths (BriefPath and
			// friends), so a t.TempDir() here would fail every case
			// through the harness's own doing.
			workDir, err := os.MkdirTemp("", "jig-")
			if err != nil {
				t.Fatalf("create work dir: %v", err)
			}
			defer func() {
				if rerr := os.RemoveAll(workDir); rerr != nil {
					t.Logf("remove work dir %s: %v", workDir, rerr)
				}
			}()

			backend := leakCapturingBackend{t: t, caseName: c.Name, workDir: workDir, fixtureDir: resultsDir("perfect")}
			judge := &ModelJudge{Backend: backend, Model: "fixture-model"}

			cs, err := RunCase(workDir, c, backend, judge, "model-a")
			if err != nil {
				t.Fatalf("RunCase: %v", err)
			}
			if !cs.Passed {
				t.Errorf("case %s: Passed = false, want true (perfect fixtures, a judge confirming every candidate Same): %+v", c.Name, cs.Rounds)
			}
		})
	}
}

// TestLeakHitsNeverJoinsTokensAcrossTheTempRoot pins that stripping the
// temp root leaves a separator behind: a leak word right before the root
// and text right after it must still read as separate tokens.
func TestLeakHitsNeverJoinsTokensAcrossTheTempRoot(t *testing.T) {
	text := "eval" + filepath.Clean(launchTempRoot) + "1"
	hits := leakHits("joined", "", text)
	if len(hits) == 0 || !strings.Contains(strings.Join(hits, "; "), `"eval"`) {
		t.Errorf("leakHits(%q) = %q, want a hit on \"eval\" once the temp root is stripped", text, hits)
	}
}
