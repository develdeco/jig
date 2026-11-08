package main

import (
	"path/filepath"
	"strconv"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/clonebehind"
	"github.com/develdeco/jig/internal/project"
)

// init registers `jig here`'s clone section (sections.go): when the clone the
// working directory is in is behind origin's target branch (as of its last
// fetch), the block names how far and the pull. A clone that is level,
// ahead, elsewhere, missing or unjudgeable adds nothing, and so does any
// error: a hook that runs `jig here` must not fail or slow on this.
func init() {
	registerHereSection(hereSection{
		Order:  340,
		Name:   "clone",
		Blocks: hereCloneBlocks,
		JSON: func(c hereCtx) (any, error) {
			if r, ok := hereCloneJudged(c); ok {
				return hereCloneInfo(r), nil
			}
			return nil, nil
		},
	})
}

// hereCloneJSON is the clone key of `jig here --json`.
type hereCloneJSON struct {
	Behind int    `json:"behind"`
	Ahead  int    `json:"ahead,omitempty"`
	Of     string `json:"of"`
	Pull   string `json:"pull,omitempty"`
	Note   string `json:"note,omitempty"`
}

func hereCloneInfo(r clonebehind.Result) hereCloneJSON {
	info := hereCloneJSON{Behind: r.Behind, Of: r.Of}
	if r.State == clonebehind.Diverged {
		info.Ahead = r.Ahead
		info.Note = clonebehind.DivergedNote(r.Dir, r)
		return info
	}
	info.Pull = r.Pull
	return info
}

// hereCloneJudged is the judgment of the located clone, and whether it is
// one to name (behind or diverged).
func hereCloneJudged(c hereCtx) (clonebehind.Result, bool) {
	if c.Where.RepoDir == "" {
		return clonebehind.Result{}, false
	}
	r := clonebehind.Check(c.Where.RepoDir, hereCloneTarget(c))
	return r, r.State == clonebehind.Behind || r.State == clonebehind.Diverged
}

// hereCloneTarget is the target branch of the repo the located clone is: the
// config's repo of that name, else its only repo, else main.
func hereCloneTarget(c hereCtx) string {
	var repos []project.Repo
	switch {
	case c.Where.StorePath != "":
		if cfg, err := project.LoadConfig(c.Where.JigHome, c.Where.StorePath, c.Where.Project, c.Where.Root, c.Where.RepoDir); err == nil {
			repos = cfg.Repos
		}
	case c.Where.Root != "":
		if m, err := project.LoadMarker(filepath.Join(c.Where.Root, project.MarkerFile)); err == nil {
			repos = project.WithMarkerRepos(project.Config{Name: c.Where.Project}, c.Where.Root, m).Repos
		}
	}
	for _, r := range repos {
		if r.Name() == c.Where.Repo {
			return r.TargetBranch()
		}
	}
	if len(repos) == 1 {
		return repos[0].TargetBranch()
	}
	return "main"
}

func hereCloneBlocks(c hereCtx) ([]string, error) {
	r, ok := hereCloneJudged(c)
	if !ok {
		return nil, nil
	}
	if r.State == clonebehind.Diverged {
		return []string{axi.KV("clone", [][2]string{
			{"behind", strconv.Itoa(r.Behind)},
			{"ahead", strconv.Itoa(r.Ahead)},
			{"of", r.Of},
			{"note", clonebehind.DivergedNote(r.Dir, r)},
		})}, nil
	}
	return []string{axi.KV("clone", [][2]string{
		{"behind", strconv.Itoa(r.Behind)},
		{"of", r.Of},
		{"pull", r.Pull},
	})}, nil
}
