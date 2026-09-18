package revieweval

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/develdeco/jig/internal/verifydeliver"
)

// CaseScore is one case's scoring result. Reason is set only when the
// dispatch failed or result.json was invalid (an infrastructure or
// contract failure rather than a review-quality one); Passed is then
// always false and Found/Missed/FalsePositives/Unmatched are empty.
type CaseScore struct {
	Name           string
	Found          []string // gold finding ids the result matched
	Missed         []string // gold finding ids the result did not match
	FalsePositives []string // finding titles matching a trap, or (no gold findings) any finding
	Unmatched      []string // finding titles matching neither gold nor a trap; reported, not failing
	Passed         bool
	Reason         string
}

// containsFile reports whether text names file: either its slash path or
// its base name appears in text.
func containsFile(text, file string) bool {
	if file == "" {
		return true
	}
	return strings.Contains(text, file) || strings.Contains(text, filepath.Base(file))
}

// scoreCase matches result's findings against c's gold: a result finding
// matches a gold finding when their classes are equal, the gold
// title_pattern matches "title\ndetail", and the gold file (or its base
// name) appears in title or detail. A finding that matches no gold entry
// is a false positive when it matches a trap, or (a case with no gold
// findings at all, e.g. a clean or trap case) unconditionally; otherwise
// it is reported as unmatched, which does not fail the case. A case
// passes iff it has zero missed gold findings and zero false positives.
func scoreCase(c Case, result verifydeliver.ReviewResult) CaseScore {
	sc := CaseScore{Name: c.Name}
	foundGold := make([]bool, len(c.Gold.Findings))

	for _, f := range result.Findings {
		text := f.Title + "\n" + f.Detail

		matchedGold := false
		for i, g := range c.Gold.Findings {
			if g.Class == f.Class && c.goldFindingRe[i].MatchString(text) && containsFile(text, g.File) {
				foundGold[i] = true
				matchedGold = true
			}
		}
		if matchedGold {
			continue
		}

		matchedTrap := false
		for i, tr := range c.Gold.Traps {
			if c.goldTrapRe[i].MatchString(text) && containsFile(text, tr.File) {
				matchedTrap = true
				break
			}
		}
		if matchedTrap || len(c.Gold.Findings) == 0 {
			sc.FalsePositives = append(sc.FalsePositives, f.Title)
			continue
		}
		sc.Unmatched = append(sc.Unmatched, f.Title)
	}

	for i, g := range c.Gold.Findings {
		if foundGold[i] {
			sc.Found = append(sc.Found, g.ID)
		} else {
			sc.Missed = append(sc.Missed, g.ID)
		}
	}

	sc.Passed = len(sc.Missed) == 0 && len(sc.FalsePositives) == 0
	return sc
}

// RenderReport renders a plain-text report from scores: one PASS/FAIL line
// per case, then totals (cases passed, recall, total false positives).
func RenderReport(scores []CaseScore) string {
	var b strings.Builder
	var passed, totalFound, totalGold, totalFP int
	for _, s := range scores {
		status := "PASS"
		if !s.Passed {
			status = "FAIL"
		} else {
			passed++
		}
		gold := len(s.Found) + len(s.Missed)
		totalFound += len(s.Found)
		totalGold += gold
		totalFP += len(s.FalsePositives)
		fmt.Fprintf(&b, "%s: %s found=%d/%d missed=%d fp=%d unmatched=%d", s.Name, status, len(s.Found), gold, len(s.Missed), len(s.FalsePositives), len(s.Unmatched))
		if s.Reason != "" {
			fmt.Fprintf(&b, " reason: %s", s.Reason)
		}
		b.WriteString("\n")
	}
	recall := 0.0
	if totalGold > 0 {
		recall = float64(totalFound) / float64(totalGold)
	}
	fmt.Fprintf(&b, "\ntotals: cases passed %d/%d, recall %.2f, false positives %d\n", passed, len(scores), recall, totalFP)
	return b.String()
}
