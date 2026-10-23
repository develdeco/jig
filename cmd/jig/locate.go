package main

import (
	"fmt"
	"path/filepath"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/locate"
	"github.com/develdeco/jig/internal/project"
)

// locateStore finds the store for cwd (storeFlag, when not empty, wins) and
// loads its project config: the one place a verb's store is resolved, through
// locate.Locate, so a clone that only carries a jig.yaml marker works as
// well as a mapped one. Locate records a clone it recognizes in the machine
// map; the resolution needs nothing of the repo, the branch or the ticket, so
// it asks for the store alone.
func locateStore(jigHome, cwd, storeFlag string, getenv func(string) string) (string, project.Config, error) {
	where, err := locate.Locate(jigHome, cwd, locate.Opts{StoreFlag: storeFlag, Getenv: getenv, StoreOnly: true})
	if err != nil {
		return "", project.Config{}, err
	}
	if where.StorePath == "" {
		return "", project.Config{}, noStoreError(where)
	}
	cfg, err := project.LoadConfig(jigHome, where.StorePath, where.Project, where.Root, where.RepoDir)
	if err != nil {
		return "", project.Config{}, err
	}
	return where.StorePath, cfg, nil
}

// noStoreError says why Locate named no store: the directory is in no
// project, or its project's store is not cloned on this machine.
func noStoreError(w locate.Where) *axi.Error {
	if w.How != locate.HowNone && w.StoreRemote != "" {
		clone := filepath.Join(filepath.Dir(w.Root), w.Project+"-tickets")
		return &axi.Error{
			Msg:  fmt.Sprintf("the store of project %s is not cloned on this machine (its remote is %s)", w.Project, w.StoreRemote),
			Code: "VALIDATION_ERROR",
			Help: []string{fmt.Sprintf("Clone it next to the repo: git clone %s %s", w.StoreRemote, clone)},
		}
	}
	return &axi.Error{
		Msg:  "no jig store found for this directory: pass --store or run inside a store or a mapped clone",
		Code: "VALIDATION_ERROR",
	}
}
