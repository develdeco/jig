package revieweval

import "testing"

// TestCorpusLoadsAndPatchesApply proves every corpus case loads, has a
// parseable gold.yaml with compilable title_pattern regexps, and its
// patch.diff applies cleanly to an empty base commit.
func TestCorpusLoadsAndPatchesApply(t *testing.T) {
	cases := loadCorpus(t)
	if len(cases) == 0 {
		t.Fatal("revieweval: corpus is empty")
	}

	for _, c := range cases {
		c := c
		t.Run(c.Name, func(t *testing.T) {
			if len(c.goldFindingRe) != len(c.Gold.Findings) {
				t.Errorf("case %s: %d gold findings but %d compiled patterns", c.Name, len(c.Gold.Findings), len(c.goldFindingRe))
			}
			if len(c.goldTrapRe) != len(c.Gold.Traps) {
				t.Errorf("case %s: %d traps but %d compiled patterns", c.Name, len(c.Gold.Traps), len(c.goldTrapRe))
			}

			if _, _, _, err := materializeCaseRepo(t.TempDir(), c); err != nil {
				t.Fatalf("case %s: patch.diff does not apply: %v", c.Name, err)
			}
		})
	}
}
