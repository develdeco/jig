// Package verifydeliver implements jig's second pipeline: gate (review +
// re-verification rounds) and publish (reconcile, re-validate, docs,
// squash, and route). It shares no in-memory state with package frontier;
// the store on disk is the only interface between them.
//
// The commits Publish makes on the ticket branch (reconcile, memorize,
// squash) carry the operator's identity, resolved from their mapped clone
// rather than the pool lease, which has none of their repo-local config.
// The store's own bookkeeping commits keep jig's identity.
package verifydeliver

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/project"
	"github.com/develdeco/jig/internal/staircase"
	"github.com/develdeco/jig/internal/store"
	"github.com/develdeco/jig/internal/tracker"
)

// Deps is verifydeliver's own dependency bundle. It never imports package
// frontier; the store on disk is the only seam between the two pipelines.
type Deps struct {
	Store   *store.Store
	Cfg     project.Config
	Machine project.MachineProject
	Rungs   staircase.Config
	// Home is the jig home root whose pool holds the gate and publish
	// leases: home.Root() for the binary, a test's own directory in tests.
	Home string
	// UserHome is the operator's own home directory, where their local
	// agent sessions keep transcripts (os.UserHomeDir() for the binary, a
	// test's own directory in tests). "" means it could not be resolved:
	// gate intent inference then has nowhere to look and says so.
	UserHome string
	// Tracker is the adapter Publish finds, opens or updates the pull request
	// with, and routes the ticket through. nil means the tracker project.yaml
	// names (tracker.New); a test hands its own.
	Tracker tracker.Adapter
	// Confirm asks the interactive confirmation question. nil means the default.
	Confirm func(branch, ticket, openPR string) bool
	// GuardedPush pushes branch to origin. nil means gitx.GuardedPush.
	GuardedPush func(dir, remote, branch string, confirmed bool) error
	// FetchOrigin refreshes the view of origin. nil means the default fetch.
	FetchOrigin func(dir string) error
	// Warn reports a soft-failure warning. nil means the default stderr write.
	Warn func(format string, args ...any)
	// GitEnv is the whole process environment Publish's identity resolution
	// runs its "git var" calls under, in place of this process's own
	// inherited one. nil means inherit it, as production always does. A test
	// whose own process has an identity pinned for its own commits
	// (gittest.PinIdentity) sets its own so that pin cannot shadow a mapped
	// clone's distinct identity, or its absence, while resolving one for
	// Publish - without editing the process environment to get it.
	GitEnv []string
	// Oracle runs one of the gate's own oracle commands in a lease and
	// returns its combined output, the way frontier.Deps.Oracle does for a
	// slice's run at green. nil means the real run, bounded by oracleLimit
	// with its process tree killed past it; a test that is not about the
	// gate's oracle run hands one that passes. Publish's revalidation always
	// runs the real oracles.
	Oracle func(cmd, dir string) (string, error)
}

// oracle is d.Oracle, or the real run when it is nil.
func (d Deps) oracle() func(cmd, dir string) (string, error) {
	if d.Oracle != nil {
		return d.Oracle
	}
	return shellOracle
}

// trackerAdapter is the adapter d hands Publish: d.Tracker when set, else the
// tracker the project's config names.
func (d Deps) trackerAdapter() (tracker.Adapter, error) {
	if d.Tracker != nil {
		return d.Tracker, nil
	}
	return tracker.New(d.Cfg, d.Store)
}

// primaryRepo returns v0.1's single repo and its target branch (defaulting
// to "main" when unset).
func primaryRepo(cfg project.Config) (project.Repo, string, string) {
	repo := cfg.Repos[0]
	return repo, repo.Name(), repo.TargetBranch()
}

// operatorClone returns the operator's mapped clone directory for
// repoName, or "" when none is recorded. identityDir and RoundInput's own
// OperatorClone (review.go) both resolve through this one lookup, so a
// mapped clone's absence reads the same way in both: nothing to identify
// commits with, and nothing intent inference can compare a session's own
// cwd against.
func operatorClone(d Deps, repoName string) string {
	if dir, ok := d.Machine.Clones[repoName]; ok && dir != "" {
		return dir
	}
	return ""
}

// identityDir returns where Publish resolves the operator's identity for
// repoName: their mapped clone, or leaseDir when none is recorded.
func identityDir(d Deps, repoName, leaseDir string) string {
	if dir := operatorClone(d, repoName); dir != "" {
		return dir
	}
	return leaseDir
}

// CheckIdentity lets a command fail fast, before any frontier or gate work,
// when the operator's mapped clone has no git identity. Without a mapped
// clone it returns nil; Publish still checks its lease before committing.
func CheckIdentity(d Deps) error {
	_, repoName, _ := primaryRepo(d.Cfg)
	dir, ok := d.Machine.Clones[repoName]
	if !ok || dir == "" {
		return nil
	}
	return gitx.CheckIdentity(dir)
}

// consolidatedTitle picks the ticket's headline title: the goal of the first
// slice that did not come from a gate round. A gate round's fix slice records
// the round in FromGate, and its goal names a batch of findings, not the work,
// so it never heads a ticket: the slices of an adopted branch are all gate
// fixes, and it is titled by what follows. Falling back - when there is no
// such slice or its goal is empty - to recorded, the ticket's own recorded
// title (jig ticket new and jig graduate write one for every ticket they mint;
// empty when the record has none), and then to the ticket id itself. The
// caller passes the title from the record it already read, so choosing a
// title never touches the store.
func consolidatedTitle(recorded, ticket string, slices []store.Slice) string {
	for _, s := range slices {
		if s.FromGate != 0 {
			continue
		}
		if s.Goal != "" {
			return s.Goal
		}
		break
	}
	if recorded != "" {
		return recorded
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

// failureCode returns err's own axi.Error code, or "INTERNAL" when err
// isn't an *axi.Error or carries no code. Gate and Publish both call it to
// build the subject of the deferred, best-effort push they run on any
// error after their first journal line for that round/publish: that push
// runs at the point of failure, rather than waiting on a later command's
// own Store.Sync, so the leftovers land attributed to this failure by name
// instead of swept into some other command's anonymous commit. The
// subject carries only this code, never err's own message, because that
// message can hold an absolute host path or other detail that has no
// business in a commit subject that gets pushed to a remote. jig already
// prints the full error on stdout, where it is read once and not kept.
func failureCode(err error) string {
	var ae *axi.Error
	if errors.As(err, &ae) && ae.Code != "" {
		return ae.Code
	}
	return "INTERNAL"
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
