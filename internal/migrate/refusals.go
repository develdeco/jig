package migrate

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/pool"
	"github.com/develdeco/jig/internal/project"
	"github.com/develdeco/jig/internal/store"
)

// CheckSchemaVersion refuses a store that is not at schema 1: the one
// version `jig store migrate --map` rewrites. A store already at
// project.CurrentSchemaVersion (or anything else) has nothing for it to do.
func CheckSchemaVersion(cfg project.Config) error {
	if cfg.SchemaVersion == 1 {
		return nil
	}
	return &axi.Error{
		Msg:  fmt.Sprintf("this store's project.yaml is at schema_version %d, not 1: `jig store migrate` only migrates a v1 store", cfg.SchemaVersion),
		Code: "VALIDATION_ERROR",
		Help: []string{"Nothing to migrate here"},
	}
}

// CheckClean refuses a store with any uncommitted change.
func CheckClean(st *store.Store) error {
	dirty, err := st.Dirty()
	if err != nil {
		return err
	}
	if !dirty {
		return nil
	}
	return &axi.Error{
		Msg:  fmt.Sprintf("the store at %s has uncommitted changes", st.Root),
		Code: "STORE_CONFLICT",
		Help: []string{"Commit or stash them there, then retry"},
	}
}

// CheckLevelWithOrigin refuses a store whose branch is not exactly at
// origin's: ahead, behind or diverged are all refused, since the migration
// commits once on the current branch and must not bury or be buried by
// anything origin does not have yet. A store with no origin has nothing to
// be level with, and passes.
func CheckLevelWithOrigin(st *store.Store) error {
	if !st.HasRemote() {
		return nil
	}
	branch, err := gitx.Run(st.Root, "symbolic-ref", "-q", "--short", "HEAD")
	if err != nil {
		return &axi.Error{
			Msg:  fmt.Sprintf("the store at %s has a detached HEAD, not a branch", st.Root),
			Code: "STORE_CONFLICT",
			Help: []string{"Check the store's state there with `git status`, resolve it, then rerun."},
		}
	}
	if _, err := gitx.Run(st.Root, "fetch", "origin", branch); err != nil {
		return err
	}
	local, err := gitx.RevParse(st.Root, "refs/heads/"+branch)
	if err != nil {
		return err
	}
	remote, err := gitx.RevParse(st.Root, "refs/remotes/origin/"+branch)
	if err != nil {
		return err
	}
	if local == remote {
		return nil
	}
	return &axi.Error{
		Msg:  fmt.Sprintf("the store at %s is not level with origin/%s", st.Root, branch),
		Code: "STORE_CONFLICT",
		Help: []string{fmt.Sprintf("Bring %s and origin/%s to the same commit (pull or push there), then retry", branch, branch)},
	}
}

// CheckNoLeases refuses when this machine's lease pool under jigHome holds
// a lease (build, gate or publish) for any of oldIDs, in any of repoNames -
// the migration renames a ticket's folder and every later command works it
// under its new id, so a lease still checked out under the old one would be
// orphaned. The refusal lists every such lease's directory.
func CheckNoLeases(jigHome string, repoNames, oldIDs []string) error {
	var leases []string
	for _, repoName := range repoNames {
		for _, id := range oldIDs {
			for _, role := range []pool.Role{pool.Build, pool.Gate, pool.Publish} {
				dir, err := pool.Dir(jigHome, repoName, id, role)
				if err != nil {
					continue
				}
				if _, err := os.Stat(dir); err == nil {
					leases = append(leases, dir)
				}
			}
		}
	}
	if len(leases) == 0 {
		return nil
	}
	sort.Strings(leases)
	return &axi.Error{
		Msg:  fmt.Sprintf("this machine's lease pool holds %d lease(s) for tickets the rename map would move: %s", len(leases), strings.Join(leases, ", ")),
		Code: "VALIDATION_ERROR",
		Help: []string{"Finish or abandon that work (publish, or remove the lease directory), then retry"},
	}
}

// BranchWarnings reports one warning line per (repo, old id) whose
// jig/<old id> branch is still on that repo's origin: after the migration
// jig works that ticket on jig/<new id> instead (brief.md#The migration
// step 2). A repo whose origin cannot be reached is skipped silently - this
// is advisory, and the migration must not fail on a repo nobody can reach
// right now.
func BranchWarnings(dir string, repos []project.Repo, renames []Rename) []string {
	var warnings []string
	for _, repo := range repos {
		for _, r := range renames {
			branch := "jig/" + r.OldID
			out, err := gitx.Run(dir, "ls-remote", "--heads", repo.Remote, branch)
			if err != nil || strings.TrimSpace(out) == "" {
				continue
			}
			warnings = append(warnings, fmt.Sprintf("%s still has branch %s on %s; after the migration jig works %s on jig/%s - merge or close it first", r.OldID, branch, repo.Name(), r.NewID, r.NewID))
		}
	}
	return warnings
}
