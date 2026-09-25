package revieweval

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestLoadCaseAcceptsAMinimalValidCase pins the happy path every other
// loader test mutates away from one field at a time: without this, a
// broken validator that rejects everything would still make every
// "rejects" test below pass for the wrong reason.
func TestLoadCaseAcceptsAMinimalValidCase(t *testing.T) {
	dir := newMinimalCase(t, "ok", validGoldYAML)
	c, err := LoadCase(dir)
	if err != nil {
		t.Fatalf("LoadCase: %v", err)
	}
	if len(c.Rounds) != 1 || len(c.Rounds[0].Gold.Findings) != 1 {
		t.Fatalf("LoadCase = %+v, want one round with one gold finding", c)
	}
}

func TestLoadCaseRejectsMissingBrief(t *testing.T) {
	dir := newMinimalCase(t, "c", validGoldYAML)
	if err := os.Remove(filepath.Join(dir, "brief.md")); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadCase(dir); err == nil {
		t.Fatal("LoadCase: want an error for a missing brief.md")
	}
}

func TestLoadCaseRejectsMissingPatch(t *testing.T) {
	dir := newMinimalCase(t, "c", validGoldYAML)
	if err := os.Remove(filepath.Join(dir, "round-1", "patch.diff")); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadCase(dir); err == nil {
		t.Fatal("LoadCase: want an error for a missing patch.diff")
	}
}

// TestLoadCaseRejectsMissingGold checks not only that a missing gold.yaml
// errors, but that the error actually says so: a loader that only
// wrapped loadGold's own read error would still fail this test's setup
// (os.ReadFile on a missing path fails too), silently losing the dedicated
// "missing gold.yaml" message the specific os.Stat check below produces.
func TestLoadCaseRejectsMissingGold(t *testing.T) {
	dir := newMinimalCase(t, "c", validGoldYAML)
	if err := os.Remove(filepath.Join(dir, "round-1", "gold.yaml")); err != nil {
		t.Fatal(err)
	}
	_, loadErr := LoadCase(dir)
	if loadErr == nil {
		t.Fatal("LoadCase: want an error for a missing gold.yaml")
	}
	if !strings.Contains(loadErr.Error(), "missing gold.yaml") {
		t.Errorf("LoadCase error = %q, want it to name gold.yaml as missing", loadErr)
	}
}

// TestLoadCaseDistinguishesAGoldReadErrorFromAMissingFile is the other
// half of that same check: gold.yaml exists (os.Stat succeeds, so the
// dedicated missing-file message must not fire) but is unreadable as a
// file - a directory in its place - so only loadGold's own generic read
// error can be the one that fires. Telling the two apart means neither
// message is written to cover both cases.
func TestLoadCaseDistinguishesAGoldReadErrorFromAMissingFile(t *testing.T) {
	dir := newMinimalCase(t, "c", validGoldYAML)
	goldPath := filepath.Join(dir, "round-1", "gold.yaml")
	if err := os.Remove(goldPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(goldPath, 0o755); err != nil {
		t.Fatal(err)
	}
	_, err := LoadCase(dir)
	if err == nil {
		t.Fatal("LoadCase: want an error, gold.yaml is a directory, not a file")
	}
	if strings.Contains(err.Error(), "missing gold.yaml") {
		t.Errorf("LoadCase error = %q, want a read error, not the missing-file message: the path exists", err)
	}
}

func TestLoadCaseRejectsARoundGap(t *testing.T) {
	dir := newMinimalCase(t, "c", validGoldYAML)
	// round-3 with no round-2 in between.
	writeFile(t, filepath.Join(dir, "round-1", "findings.yaml"), "scope: full\nreviewed_paths: []\nfindings: []\n")
	writeFile(t, filepath.Join(dir, "round-3", "patch.diff"), "diff\n")
	writeFile(t, filepath.Join(dir, "round-3", "gold.yaml"), validGoldYAML)
	if _, err := LoadCase(dir); err == nil {
		t.Fatal("LoadCase: want an error for a round gap (round-1, round-3, no round-2)")
	}
}

func TestLoadCaseRequiresFindingsYAMLOnANonLastRound(t *testing.T) {
	dir := newMinimalCase(t, "c", validGoldYAML) // round-1 has no findings.yaml
	writeFile(t, filepath.Join(dir, "round-2", "patch.diff"), "diff\n")
	writeFile(t, filepath.Join(dir, "round-2", "gold.yaml"), validGoldYAML)
	if _, err := LoadCase(dir); err == nil {
		t.Fatal("LoadCase: want an error, round-1 has a round-2 after it but no findings.yaml")
	}
}

func TestLoadCaseRejectsFindingsYAMLOnTheLastRound(t *testing.T) {
	dir := newMinimalCase(t, "c", validGoldYAML)
	writeFile(t, filepath.Join(dir, "round-1", "findings.yaml"), "scope: full\nreviewed_paths: []\nfindings: []\n")
	if _, err := LoadCase(dir); err == nil {
		t.Fatal("LoadCase: want an error, findings.yaml is present on the (only, so last) round")
	}
}

func TestLoadCaseRejectsAnUnknownGoldKeyLikeTitlePattern(t *testing.T) {
	gold := `exhaustive: false
findings:
  - id: f1
    file: a.go
    lines: [10, 10]
    action: fix
    title_pattern: "nil.*deref"
    description: a seeded problem
`
	dir := newMinimalCase(t, "c", gold)
	if _, err := LoadCase(dir); err == nil {
		t.Fatal("LoadCase: want an error for the unknown key title_pattern")
	}
}

func TestLoadCaseRejectsAnEmptyGoldFindingID(t *testing.T) {
	gold := `exhaustive: false
findings:
  - id: ""
    file: a.go
    lines: [10, 10]
    action: fix
    description: a seeded problem
`
	dir := newMinimalCase(t, "c", gold)
	if _, err := LoadCase(dir); err == nil {
		t.Fatal("LoadCase: want an error for an empty gold finding id")
	}
}

func TestLoadCaseRejectsADuplicateIDAcrossFindingsTrapsAndDecisions(t *testing.T) {
	gold := `exhaustive: false
findings:
  - id: dup
    file: a.go
    lines: [10, 10]
    action: fix
    description: a seeded problem
traps:
  - id: dup
    file: b.go
    lines: [1, 2]
    description: correct code that looks wrong
`
	dir := newMinimalCase(t, "c", gold)
	if _, err := LoadCase(dir); err == nil {
		t.Fatal("LoadCase: want an error, id \"dup\" is used by both a finding and a trap")
	}
}

func TestLoadCaseRejectsLinesNotExactlyTwoIntegers(t *testing.T) {
	gold := `exhaustive: false
findings:
  - id: f1
    file: a.go
    lines: [10]
    action: fix
    description: a seeded problem
`
	dir := newMinimalCase(t, "c", gold)
	if _, err := LoadCase(dir); err == nil {
		t.Fatal("LoadCase: want an error, lines has one integer, not two")
	}
}

func TestLoadCaseRejectsNonPositiveLines(t *testing.T) {
	gold := `exhaustive: false
findings:
  - id: f1
    file: a.go
    lines: [0, 5]
    action: fix
    description: a seeded problem
`
	dir := newMinimalCase(t, "c", gold)
	if _, err := LoadCase(dir); err == nil {
		t.Fatal("LoadCase: want an error, lines[0] is 0, not positive")
	}
}

func TestLoadCaseRejectsLinesFromGreaterThanTo(t *testing.T) {
	gold := `exhaustive: false
findings:
  - id: f1
    file: a.go
    lines: [10, 5]
    action: fix
    description: a seeded problem
`
	dir := newMinimalCase(t, "c", gold)
	if _, err := LoadCase(dir); err == nil {
		t.Fatal("LoadCase: want an error, lines from (10) > to (5)")
	}
}

func TestLoadCaseRejectsAnUnknownAction(t *testing.T) {
	gold := `exhaustive: false
findings:
  - id: f1
    file: a.go
    lines: [10, 10]
    action: maybe
    description: a seeded problem
`
	dir := newMinimalCase(t, "c", gold)
	if _, err := LoadCase(dir); err == nil {
		t.Fatal("LoadCase: want an error for action \"maybe\"")
	}
}

func TestLoadCaseRejectsAnUnknownDecision(t *testing.T) {
	dir := newMinimalCase(t, "c", validGoldYAML)
	writeFile(t, filepath.Join(dir, "round-1", "decisions.yaml"), `decisions:
  - id: extra
    file: b.go
    lines: [1, 1]
    description: a decided finding
    decision: maybe
`)
	if _, err := LoadCase(dir); err == nil {
		t.Fatal("LoadCase: want an error for decision \"maybe\"")
	}
}

func TestLoadCaseRejectsAnEmptyDescription(t *testing.T) {
	gold := `exhaustive: false
findings:
  - id: f1
    file: a.go
    lines: [10, 10]
    action: fix
    description: ""
`
	dir := newMinimalCase(t, "c", gold)
	if _, err := LoadCase(dir); err == nil {
		t.Fatal("LoadCase: want an error for an empty description")
	}
}

func TestLoadCaseRejectsAFileThatIsNotACleanRepoRelativePath(t *testing.T) {
	for _, file := range []string{"/abs.go", "../outside.go", "./a.go", `a\b.go`} {
		// Single-quoted YAML: a double-quoted scalar would treat the
		// backslash in the last case as an escape sequence, not a
		// literal character, and mask the very case this test wants.
		gold := "exhaustive: false\nfindings:\n  - id: f1\n    file: '" + file + "'\n    lines: [10, 10]\n    action: fix\n    description: a seeded problem\n"
		dir := newMinimalCase(t, "c", gold)
		if _, err := LoadCase(dir); err == nil {
			t.Errorf("LoadCase: file %q: want an error", file)
		}
	}
}

func TestLoadCaseRejectsAPriorThatDoesNotNameAFoldFinding(t *testing.T) {
	dir := newMinimalCase(t, "c", validGoldYAML)
	writeFile(t, filepath.Join(dir, "round-1", "findings.yaml"), "scope: full\nreviewed_paths: []\nfindings: []\n")
	writeFile(t, filepath.Join(dir, "round-2", "patch.diff"), "diff\n")
	writeFile(t, filepath.Join(dir, "round-2", "gold.yaml"), `exhaustive: false
findings:
  - id: g2
    file: a.go
    lines: [1, 1]
    action: fix
    prior: r1-f99
    description: a repeat that cites a prior nothing recorded
`)
	if _, err := LoadCase(dir); err == nil {
		t.Fatal("LoadCase: want an error, prior r1-f99 was never recorded")
	}
}

func TestLoadCaseRejectsAPriorThatNamesADismissedFinding(t *testing.T) {
	dir := newMinimalCase(t, "c", validGoldYAML)
	writeFile(t, filepath.Join(dir, "round-1", "findings.yaml"), `scope: full
reviewed_paths: []
findings:
  - id: r1-f1
    file: b.go
    line: 5
    title: t
    detail: d
    action: fix
    risk: low
    risk_rationale: r
    status: dismissed
    recurrences: 0
`)
	writeFile(t, filepath.Join(dir, "round-2", "patch.diff"), "diff\n")
	writeFile(t, filepath.Join(dir, "round-2", "gold.yaml"), `exhaustive: false
findings:
  - id: g2
    file: a.go
    lines: [1, 1]
    action: fix
    prior: r1-f1
    description: cites a dismissed prior, which is re-litigating, not repeating
`)
	if _, err := LoadCase(dir); err == nil {
		t.Fatal("LoadCase: want an error, prior r1-f1 is dismissed, not open/asked/noted")
	}
}

func TestLoadCaseAcceptsAPriorThatNamesAnOpenFoldFinding(t *testing.T) {
	dir := newMinimalCase(t, "c", validGoldYAML)
	writeFile(t, filepath.Join(dir, "round-1", "findings.yaml"), `scope: full
reviewed_paths: []
findings:
  - id: r1-f1
    file: b.go
    line: 5
    title: t
    detail: d
    action: fix
    risk: low
    risk_rationale: r
    status: open
    recurrences: 0
`)
	writeFile(t, filepath.Join(dir, "round-2", "patch.diff"), "diff\n")
	writeFile(t, filepath.Join(dir, "round-2", "gold.yaml"), `exhaustive: false
findings:
  - id: g2
    file: a.go
    lines: [1, 1]
    action: fix
    prior: r1-f1
    description: a genuine repeat of an open finding
`)
	c, err := LoadCase(dir)
	if err != nil {
		t.Fatalf("LoadCase: %v", err)
	}
	if len(c.Rounds) != 2 || c.Rounds[1].Gold.Findings[0].Prior != "r1-f1" {
		t.Fatalf("LoadCase = %+v, want round 2's gold finding to keep prior r1-f1", c)
	}
}

func TestLoadCorpusRejectsAnEmptyRoot(t *testing.T) {
	root := t.TempDir()
	if _, err := LoadCorpus(root); err == nil {
		t.Fatal("LoadCorpus: want an error for a root with no case directories")
	}
}

// --- a recorded decision locates the finding it decided --------------------

// twoRoundCaseWithFindings builds a two-round case dir: round-1 has a
// findings.yaml with one dismissed finding "r1-f1" in file b.go and one
// open finding "r1-f2" in file c.go, decisions.yaml given verbatim (a test
// mutates its recorded-link body), and round-2 is a minimal last round
// with no findings.yaml (recorded needs a round that has one, so the round
// under test is always round-1 here).
func twoRoundCaseWithFindings(t *testing.T, name, decisions string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), name)
	writeFile(t, filepath.Join(dir, "brief.md"), "# brief\n")
	writeFile(t, filepath.Join(dir, "round-1", "patch.diff"), "diff\n")
	writeFile(t, filepath.Join(dir, "round-1", "gold.yaml"), validGoldYAML)
	writeFile(t, filepath.Join(dir, "round-1", "findings.yaml"), `scope: full
reviewed_paths: []
findings:
  - id: r1-f1
    file: b.go
    line: 5
    title: t
    detail: d
    action: fix
    risk: low
    risk_rationale: r
    status: dismissed
    recurrences: 0
    triage: human
  - id: r1-f2
    file: c.go
    line: 9
    title: t2
    detail: d2
    action: fix
    risk: low
    risk_rationale: r
    status: open
    recurrences: 0
    triage: auto
`)
	if decisions != "" {
		writeFile(t, filepath.Join(dir, "round-1", "decisions.yaml"), decisions)
	}
	writeFile(t, filepath.Join(dir, "round-2", "patch.diff"), "diff\n")
	writeFile(t, filepath.Join(dir, "round-2", "gold.yaml"), validGoldYAML)
	return dir
}

func TestLoadCaseAcceptsARecordedLinkToADismissedFinding(t *testing.T) {
	dir := twoRoundCaseWithFindings(t, "c", `decisions:
  - id: dec1
    file: b.go
    lines: [4, 6]
    description: the whole thing, a defensible choice
    decision: dismissed
    recorded: r1-f1
`)
	c, err := LoadCase(dir)
	if err != nil {
		t.Fatalf("LoadCase: %v", err)
	}
	if len(c.Rounds[0].Decisions) != 1 || c.Rounds[0].Decisions[0].Recorded != "r1-f1" {
		t.Fatalf("LoadCase decisions = %+v, want round 1's decision to carry Recorded \"r1-f1\"", c.Rounds[0].Decisions)
	}
}

func TestLoadCaseRejectsARecordedLinkOnARoundWithNoFindingsYAML(t *testing.T) {
	dir := newMinimalCase(t, "c", validGoldYAML) // single round: the last round, so no findings.yaml
	writeFile(t, filepath.Join(dir, "round-1", "decisions.yaml"), `decisions:
  - id: dec1
    file: a.go
    lines: [1, 1]
    description: d
    decision: dismissed
    recorded: r1-f1
`)
	if _, err := LoadCase(dir); err == nil {
		t.Fatal("LoadCase: want an error, this round has no findings.yaml for \"recorded\" to name")
	}
}

func TestLoadCaseRejectsARecordedIDNotInFindingsYAML(t *testing.T) {
	dir := twoRoundCaseWithFindings(t, "c", `decisions:
  - id: dec1
    file: b.go
    lines: [4, 6]
    description: d
    decision: dismissed
    recorded: r1-f99
`)
	if _, err := LoadCase(dir); err == nil {
		t.Fatal("LoadCase: want an error, r1-f99 is not a finding in round 1's findings.yaml")
	}
}

func TestLoadCaseRejectsARecordedFileMismatch(t *testing.T) {
	dir := twoRoundCaseWithFindings(t, "c", `decisions:
  - id: dec1
    file: other.go
    lines: [4, 6]
    description: d
    decision: dismissed
    recorded: r1-f1
`)
	if _, err := LoadCase(dir); err == nil {
		t.Fatal("LoadCase: want an error, the decision's file does not match r1-f1's own file (b.go)")
	}
}

func TestLoadCaseRejectsADismissedDecisionRecordingAnOpenFinding(t *testing.T) {
	dir := twoRoundCaseWithFindings(t, "c", `decisions:
  - id: dec1
    file: c.go
    lines: [8, 10]
    description: d
    decision: dismissed
    recorded: r1-f2
`)
	if _, err := LoadCase(dir); err == nil {
		t.Fatal("LoadCase: want an error, r1-f2's status is open, not dismissed")
	}
}

func TestLoadCaseAcceptsAKeptDecisionRecordingAnOpenFinding(t *testing.T) {
	dir := twoRoundCaseWithFindings(t, "c", `decisions:
  - id: dec1
    file: c.go
    lines: [8, 10]
    description: d
    decision: kept
    recorded: r1-f2
`)
	if _, err := LoadCase(dir); err != nil {
		t.Fatalf("LoadCase: %v", err)
	}
}

func TestLoadCaseRejectsAKeptDecisionRecordingADismissedFinding(t *testing.T) {
	dir := twoRoundCaseWithFindings(t, "c", `decisions:
  - id: dec1
    file: b.go
    lines: [4, 6]
    description: d
    decision: kept
    recorded: r1-f1
`)
	if _, err := LoadCase(dir); err == nil {
		t.Fatal("LoadCase: want an error, r1-f1's status is dismissed, not open or asked")
	}
}

func TestLoadCaseRejectsTwoDecisionsNamingTheSameRecord(t *testing.T) {
	dir := twoRoundCaseWithFindings(t, "c", `decisions:
  - id: dec1
    file: b.go
    lines: [4, 6]
    description: d1
    decision: dismissed
    recorded: r1-f1
  - id: dec2
    file: b.go
    lines: [4, 6]
    description: d2
    decision: dismissed
    recorded: r1-f1
`)
	if _, err := LoadCase(dir); err == nil {
		t.Fatal("LoadCase: want an error, two decisions both name recorded r1-f1")
	}
}

func TestLoadCorpusLoadsEveryCaseInNameOrder(t *testing.T) {
	root := t.TempDir()
	for _, name := range []string{"zeta", "alpha"} {
		dir := filepath.Join(root, name)
		writeFile(t, filepath.Join(dir, "brief.md"), "# brief\n")
		writeFile(t, filepath.Join(dir, "round-1", "patch.diff"), "diff\n")
		writeFile(t, filepath.Join(dir, "round-1", "gold.yaml"), validGoldYAML)
	}
	cases, err := LoadCorpus(root)
	if err != nil {
		t.Fatalf("LoadCorpus: %v", err)
	}
	if len(cases) != 2 || cases[0].Name != "alpha" || cases[1].Name != "zeta" {
		var names []string
		for _, c := range cases {
			names = append(names, c.Name)
		}
		t.Fatalf("LoadCorpus names = %v, want [alpha zeta]", names)
	}
}
