package revieweval

import (
	"os"
	"path/filepath"
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

func TestLoadCaseRejectsMissingGold(t *testing.T) {
	dir := newMinimalCase(t, "c", validGoldYAML)
	if err := os.Remove(filepath.Join(dir, "round-1", "gold.yaml")); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadCase(dir); err == nil {
		t.Fatal("LoadCase: want an error for a missing gold.yaml")
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
