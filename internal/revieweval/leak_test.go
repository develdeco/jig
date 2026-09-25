package revieweval

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
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

// assertNoLeak fails t when text contains caseName as a literal substring
// (case-insensitive: the hyphenated case name itself, exactly as a session
// reading its own dispatch or worktree would see it) or any token equal to
// leakVocabulary. label names what text is, for a failure a person can
// place without re-running the test.
func assertNoLeak(t *testing.T, label, caseName, text string) {
	t.Helper()
	if text == "" {
		return
	}
	if strings.Contains(strings.ToLower(text), strings.ToLower(caseName)) {
		t.Errorf("%s: contains the case name %q", label, caseName)
	}
	for _, tok := range leakTokens(text) {
		if leakVocabulary[tok] {
			t.Errorf("%s: contains leak token %q", label, tok)
		}
	}
}

// assertNoLeakUnder walks root (tracked and untracked files alike - a
// plain directory walk sees both) and checks every file's own path
// relative to root and its content, skipping every .git directory: nothing
// a session's read tools could reach under root is exempt. A session's
// reads are not bounded to its worktree, so the test walks the whole work
// root too - the store beside it (project.yaml, the ticket's brief,
// slices, journal and seeded gate records) and the judge's own inputs.
func assertNoLeakUnder(t *testing.T, label, caseName, root, what string) {
	t.Helper()
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
			return nil
		}
		assertNoLeak(t, label+" "+what+" path "+rel, caseName, rel)
		data, rerr := os.ReadFile(p)
		if rerr != nil {
			return rerr
		}
		assertNoLeak(t, label+" "+what+" file "+rel, caseName, string(data))
		return nil
	})
	if err != nil {
		t.Fatalf("%s: walk %s: %v", label, what, err)
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
