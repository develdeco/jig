package main

import (
	"encoding/json"
	"sort"

	"github.com/develdeco/jig/internal/gitx"
)

// What `jig next` says of overlapping branches is a fact about commits: the
// files a branch changes past the target are those of two commit ids, and
// whether two branches merge is the fact of two. A commit id names its whole
// history, so each is asked of git once and kept on this machine under the jig
// home, by the ids it is about (the scope that `jig next` opens, gitx.BeginScope):
// a branch nothing moved on costs no process, a moved tip is a new key so
// nothing stale is read, and a file that cannot be read or written is a miss,
// never an error.

// diffFilter is the change kinds the overlaps count.
const diffFilter = "ACDMRTUXB"

// changedFiles is the files branch changes past base in dir, from the facts
// kept when both tips are known (baseTip and tip, "" for one that is not), else
// asked of git. A failed ask is no files and is not kept.
func changedFiles(dir, base, branch, baseTip, tip string) []string {
	ask := func() ([]string, error) { return gitx.DiffNameOnlySince(dir, base, branch, diffFilter) }
	kept := gitx.KeptFor(dir)
	if kept == nil || baseTip == "" || tip == "" {
		files, _ := ask()
		return files
	}
	out, err := kept.Answer("diff "+baseTip+" "+tip+" "+diffFilter, func() (string, error) {
		files, err := ask()
		if err != nil {
			return "", err
		}
		data, err := json.Marshal(files)
		return string(data), err
	})
	if err != nil {
		return nil
	}
	var files []string
	if json.Unmarshal([]byte(out), &files) != nil {
		files, _ = ask()
	}
	return files
}

// mergedPair is testPair of two branches of dir, from the facts kept when both
// tips are known, else asked of git. A pair git could not try is untested and is
// not kept, so the next read tries it again.
func mergedPair(dir, a, b, tipA, tipB string) (string, []string) {
	kept := gitx.KeptFor(dir)
	if kept == nil || tipA == "" || tipB == "" {
		return testPair(dir, a, b)
	}
	// Merging is the same either way round.
	ids := []string{tipA, tipB}
	sort.Strings(ids)
	type merge struct {
		State string   `json:"state"`
		Files []string `json:"files,omitempty"`
	}
	state, files, tried := "", []string(nil), false
	out, err := kept.Answer("merge "+ids[0]+" "+ids[1], func() (string, error) {
		state, files = testPair(dir, a, b)
		tried = true
		if state == pairUntested {
			return "", errUntested
		}
		data, err := json.Marshal(merge{State: state, Files: files})
		return string(data), err
	})
	if tried {
		return state, files
	}
	if err != nil {
		return pairUntested, nil
	}
	var m merge
	if json.Unmarshal([]byte(out), &m) != nil || (m.State != pairClean && m.State != pairConflict) {
		return testPair(dir, a, b)
	}
	return m.State, m.Files
}

// errUntested is what a merge git could not try answers a kept fact with, so it
// is not kept.
var errUntested = untestedError{}

type untestedError struct{}

func (untestedError) Error() string { return "the merge could not be tried" }
