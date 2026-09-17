// Package verifydeliver implements jig's second pipeline: gate (review +
// re-verification rounds) and publish (reconcile, re-validate, docs,
// squash, and route). It shares no in-memory state with package frontier;
// the store on disk is the only interface between them.
package verifydeliver

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/develdeco/jig/project"
	"github.com/develdeco/jig/staircase"
	"github.com/develdeco/jig/store"
)

// Deps is verifydeliver's own dependency bundle. It never imports package
// frontier; the store on disk is the only seam between the two pipelines.
type Deps struct {
	Store   *store.Store
	Cfg     project.Config
	Machine project.MachineProject
	Rungs   staircase.Config
}

// pinnedGitEnv fixes the author/committer identity and dates for every
// commit verifydeliver makes itself (memorize, squash), matching the fake
// session backend's identity so shas and messages stay comparable in tests.
var pinnedGitEnv = []string{
	"GIT_AUTHOR_NAME=jig-fixture",
	"GIT_AUTHOR_EMAIL=fixture@example.invalid",
	"GIT_COMMITTER_NAME=jig-fixture",
	"GIT_COMMITTER_EMAIL=fixture@example.invalid",
	"GIT_AUTHOR_DATE=2026-01-01T00:00:00Z",
	"GIT_COMMITTER_DATE=2026-01-01T00:00:00Z",
}

// primaryRepo returns v0.1's single repo and its target branch (defaulting
// to "main" when unset).
func primaryRepo(cfg project.Config) (project.Repo, string, string) {
	repo := cfg.Repos[0]
	target := repo.Target
	if target == "" {
		target = "main"
	}
	return repo, repo.Name(), target
}

// ticketBranch is the ticket's working branch name.
func ticketBranch(ticket string) string { return "jig/" + ticket }

// consolidatedTitle picks the ticket's headline title: the first slice's
// goal, falling back to the ticket id when there are no slices yet.
func consolidatedTitle(ticket string, slices []store.Slice) string {
	if len(slices) > 0 && slices[0].Goal != "" {
		return slices[0].Goal
	}
	return ticket
}

// distinctWorkspaces returns the distinct slice workspace ids, in
// first-seen order.
func distinctWorkspaces(slices []store.Slice) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range slices {
		if s.Workspace == "" || seen[s.Workspace] {
			continue
		}
		seen[s.Workspace] = true
		out = append(out, s.Workspace)
	}
	return out
}

// sliceWorkspaceMap builds the slice-id -> workspace-id map journal's
// renderers need.
func sliceWorkspaceMap(slices []store.Slice) map[string]string {
	m := make(map[string]string, len(slices))
	for _, s := range slices {
		m[s.ID] = s.Workspace
	}
	return m
}

// evidenceDir is <ticket>/evidence/round-<n> under the store.
func evidenceDir(st *store.Store, ticket string, n int) string {
	return filepath.Join(st.TicketDir(ticket), "evidence", fmt.Sprintf("round-%d", n))
}

// gateRoundDir is <ticket>/gate/round-<n> under the store.
func gateRoundDir(st *store.Store, ticket string, n int) string {
	return filepath.Join(st.TicketDir(ticket), "gate", fmt.Sprintf("round-%d", n))
}

// existingGateRounds counts how many gate/round-* directories already exist
// for ticket.
func existingGateRounds(st *store.Store, ticket string) (int, error) {
	dir := filepath.Join(st.TicketDir(ticket), "gate")
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, nil
		}
		return 0, err
	}
	count := 0
	for _, e := range entries {
		if e.IsDir() && strings.HasPrefix(e.Name(), "round-") {
			count++
		}
	}
	return count, nil
}
