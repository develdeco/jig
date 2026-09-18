// Package revieweval scores the gate reviewer's output against a labeled
// corpus: for each case, a creation-only diff, a brief, and a gold.yaml of
// findings that must be found and traps that must not be flagged. It
// drives the reviewer through the SAME prompt template and result parser
// verifydeliver uses (RenderReviewPrompt, ParseReviewResult), so a corpus
// run measures the real gate reviewer contract, not a stand-in.
package revieweval

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"

	"gopkg.in/yaml.v3"
)

// GoldFinding is one finding a case's diff must produce.
type GoldFinding struct {
	ID           string `yaml:"id"`
	Class        string `yaml:"class"`
	TitlePattern string `yaml:"title_pattern"`
	File         string `yaml:"file"`
}

// GoldTrap is one finding a case's diff must NOT produce: correct-but-
// unusual code that tempts a false positive.
type GoldTrap struct {
	ID           string `yaml:"id"`
	TitlePattern string `yaml:"title_pattern"`
	File         string `yaml:"file"`
}

// Gold is a case's gold.yaml: the labeled truth a reviewer run is scored
// against.
type Gold struct {
	Findings []GoldFinding `yaml:"findings"`
	Traps    []GoldTrap    `yaml:"traps"`
}

// Case is one loaded corpus case: a directory of patch.diff, brief.md,
// review.json (a D1-shaped request template; its sha and path fields are
// placeholders RunCase overwrites) and gold.yaml.
type Case struct {
	Name       string
	Dir        string
	PatchPath  string
	BriefPath  string
	ReviewPath string
	Gold       Gold

	goldFindingRe []*regexp.Regexp // compiled, same order as Gold.Findings
	goldTrapRe    []*regexp.Regexp // compiled, same order as Gold.Traps
}

// LoadCase loads one case directory: it fails when patch.diff, brief.md,
// review.json or gold.yaml is missing, when gold.yaml does not parse, when
// a title_pattern does not compile as a Go regexp, or when review.json's
// scope is not "full" (every case is a single, full-scope round).
func LoadCase(dir string) (Case, error) {
	name := filepath.Base(dir)
	c := Case{
		Name:       name,
		Dir:        dir,
		PatchPath:  filepath.Join(dir, "patch.diff"),
		BriefPath:  filepath.Join(dir, "brief.md"),
		ReviewPath: filepath.Join(dir, "review.json"),
	}
	for _, p := range []string{c.PatchPath, c.BriefPath, c.ReviewPath} {
		if _, err := os.Stat(p); err != nil {
			return Case{}, fmt.Errorf("revieweval: case %s: %w", name, err)
		}
	}

	goldData, err := os.ReadFile(filepath.Join(dir, "gold.yaml"))
	if err != nil {
		return Case{}, fmt.Errorf("revieweval: case %s: read gold.yaml: %w", name, err)
	}
	var gold Gold
	if err := yaml.Unmarshal(goldData, &gold); err != nil {
		return Case{}, fmt.Errorf("revieweval: case %s: parse gold.yaml: %w", name, err)
	}

	for _, g := range gold.Findings {
		re, err := regexp.Compile(g.TitlePattern)
		if err != nil {
			return Case{}, fmt.Errorf("revieweval: case %s: gold finding %s: compile title_pattern %q: %w", name, g.ID, g.TitlePattern, err)
		}
		c.goldFindingRe = append(c.goldFindingRe, re)
	}
	for _, tr := range gold.Traps {
		re, err := regexp.Compile(tr.TitlePattern)
		if err != nil {
			return Case{}, fmt.Errorf("revieweval: case %s: trap %s: compile title_pattern %q: %w", name, tr.ID, tr.TitlePattern, err)
		}
		c.goldTrapRe = append(c.goldTrapRe, re)
	}
	c.Gold = gold

	if err := validateReviewTemplate(name, c.ReviewPath); err != nil {
		return Case{}, err
	}

	return c, nil
}

// LoadCorpus loads every case directory directly under root, in name
// order.
func LoadCorpus(root string) ([]Case, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, fmt.Errorf("revieweval: read corpus root %s: %w", root, err)
	}
	var cases []Case
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		c, err := LoadCase(filepath.Join(root, e.Name()))
		if err != nil {
			return nil, err
		}
		cases = append(cases, c)
	}
	sort.Slice(cases, func(i, j int) bool { return cases[i].Name < cases[j].Name })
	if len(cases) == 0 {
		return nil, fmt.Errorf("revieweval: corpus root %s has no case directories", root)
	}
	return cases, nil
}
