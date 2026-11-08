package main

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/verifydeliver"
)

// deltaMaxLines is how long a delta's diff may be before the context names a
// file holding it instead of printing it.
const deltaMaxLines = 400

// deltaReadLine is the `read` line of a delta round's context: what to read,
// in one sentence. The rule lives here, in jig's output, and not in the review
// skill (GATE-17).
const deltaReadLine = "this is a delta round: read the delta block and the answers; for each function or constant the delta changes in signature, return value or contract, find its call sites with `git grep` in the worktree and read them; open a whole file only for what the diff cannot show"

// deltaDiff is one repo's delta as a delta round's context shows it: the diff
// base..head, printed when short and held in Path when long, and the files it
// changes as the round names them.
type deltaDiff struct {
	Repo       string
	Base, Head string
	Files      []string
	Lines      int
	Text       string
	Path       string
}

// roundIsDelta reports whether every repo of the round is reviewed as a delta
// from the commit an earlier round reviewed.
func roundIsDelta(p verifydeliver.Prepared) bool {
	if len(p.Repos) == 0 {
		return p.Scope == "delta"
	}
	for _, r := range p.Repos {
		if r.Scope != "delta" {
			return false
		}
	}
	return true
}

// computeDeltaDiffs reads the diff of each repo of a delta round and, over
// deltaMaxLines, writes it beside the request. It is nil for a full round.
func computeDeltaDiffs(p verifydeliver.Prepared) ([]deltaDiff, error) {
	if !roundIsDelta(p) {
		return nil, nil
	}
	type src struct{ repo, lease, base, head string }
	var srcs []src
	if len(p.Repos) == 0 {
		srcs = []src{{"", p.Lease, p.Base, p.Head}}
	}
	for _, r := range p.Repos {
		srcs = append(srcs, src{r.Repo, r.Lease, r.Base, r.Head})
	}
	var out []deltaDiff
	for _, s := range srcs {
		text, err := gitx.DiffPatch(s.lease, s.base, s.head)
		if err != nil {
			return nil, err
		}
		changed, err := gitx.DiffNameOnly(s.lease, s.base, s.head, "AMTD")
		if err != nil {
			return nil, err
		}
		sort.Strings(changed)
		d := deltaDiff{Repo: s.repo, Base: s.base, Head: s.head, Lines: strings.Count(text, "\n")}
		for _, f := range changed {
			if s.repo != "" {
				f = s.repo + "/" + f
			}
			d.Files = append(d.Files, f)
		}
		if d.Lines > deltaMaxLines {
			name := "delta.diff"
			if s.repo != "" {
				name = "delta-" + s.repo + ".diff"
			}
			d.Path = filepath.Join(filepath.Dir(p.RequestPath), name)
			if err := os.WriteFile(d.Path, []byte(text), 0o644); err != nil {
				return nil, fmt.Errorf("write the delta diff: %w", err)
			}
		} else {
			d.Text = text
		}
		out = append(out, d)
	}
	return out, nil
}

// deltaBlock renders the delta of one or more repos: the commits it runs
// between, its files and size, and the diff itself indented under diff, or the
// path of the file holding it.
func deltaBlock(diffs []deltaDiff) string {
	var lines []string
	for _, d := range diffs {
		if d.Repo != "" {
			lines = append(lines, "  repo: "+axi.Quote(d.Repo))
		}
		lines = append(lines,
			"  base: "+d.Base,
			"  head: "+d.Head,
			"  files: "+strconv.Itoa(len(d.Files)),
			"  lines: "+strconv.Itoa(d.Lines),
		)
		if d.Path != "" {
			lines = append(lines, "  diff_path: "+axi.Quote(d.Path))
			continue
		}
		lines = append(lines, "  diff: |")
		for _, l := range strings.Split(strings.TrimSuffix(d.Text, "\n"), "\n") {
			lines = append(lines, "    "+l)
		}
	}
	return strings.Join(append([]string{"delta:"}, lines...), "\n")
}

// deltaFileSet is the files the delta changes, as the round names them.
func deltaFileSet(diffs []deltaDiff) map[string]bool {
	set := map[string]bool{}
	for _, d := range diffs {
		for _, f := range d.Files {
			set[f] = true
		}
	}
	return set
}

// answers is what a delta reviewer looks for in the diff: every finding still
// open or asked, and every noted finding a person has decided (a pending keep).
func answers(open []verifydeliver.OpenFinding) [][]string {
	var rows [][]string
	for _, f := range open {
		if f.Status == verifydeliver.StatusNoted && f.Decision == "" {
			continue
		}
		rows = append(rows, []string{fmt.Sprintf("%s:%d", f.File, f.Line), f.Action, f.Risk, f.Title, f.DecisionText()})
	}
	return rows
}
