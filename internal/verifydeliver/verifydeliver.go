// Package verifydeliver implements jig's second pipeline: gate (review +
// re-verification rounds) and publish (the pull request body, the push and the
// pull request). It shares no in-memory state with package frontier; the
// store on disk is the only interface between them.
//
// jig launches no model here. The work happens in the operator's clone, by an
// agent jig did not start: a gate round is opened (GatePrepare), read and
// reported on by a reviewer in a fresh context, and recorded when the
// reviewer says it is done (GateFinish); publish renders the pull request
// body (PublishBody), records a pull request an agent opened
// (PublishRecordPR) or pushes the reviewed head and opens it (Publish). Publish
// reconciles nothing, squashes nothing and commits nothing on the ticket
// branch, so the person's branch never diverges from origin's because of it.
// The one launched reviewer left is the source internal/revieweval measures
// reviewers with and `jig solve` runs, until both go.
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
	"github.com/develdeco/jig/internal/repohost"
	"github.com/develdeco/jig/internal/store"
)

// Deps is verifydeliver's own dependency bundle. It never imports package
// frontier; the store on disk is the only seam between the two pipelines.
type Deps struct {
	Store   *store.Store
	Cfg     project.Config
	Machine project.MachineProject
	// Home is the jig home root whose pool holds the gate and publish
	// leases: the binary's env resolves it (cmd/jig/env.go), a test's own
	// directory in tests.
	Home string
	// UserHome is the operator's own home directory, where their local
	// agent sessions keep transcripts (the binary's env resolves it, as
	// os.UserHomeDir does; a test's own directory in tests). "" means it could
	// not be resolved: the gate's search for an intent excerpt then has
	// nowhere to look and says so.
	UserHome string
	// Host is the pull-request host Publish finds, opens or updates the pull
	// request with. nil means the host the shipped repo's own remote names
	// (repohost.New), which is itself nil for a remote with no pull-request
	// host; a test hands its own, built on a fake gh.
	Host repohost.Host
	// Confirm asks the interactive confirmation question. nil means the default.
	Confirm func(branch, ticket, openPR string, hasHost bool) bool
	// GuardedPush pushes branch to origin. nil means gitx.GuardedPush.
	GuardedPush func(dir, remote, branch string, confirmed bool) error
	// FetchOrigin refreshes the view of origin. nil means the default fetch.
	FetchOrigin func(dir string) error
	// Warn reports a soft-failure warning. nil means the default stderr write.
	Warn func(format string, args ...any)
	// Oracle runs one of the gate's own oracle commands in a lease and
	// returns its combined output, the way frontier.Deps.Oracle does for a
	// slice's run at green. nil means the real run, bounded by oracleLimit
	// with its process tree killed past it; a test that is not about the
	// gate's oracle run hands one that passes.
	Oracle func(cmd, dir string) (string, error)
}

// oracle is d.Oracle, or the real run when it is nil.
func (d Deps) oracle() oracleFunc {
	if d.Oracle != nil {
		return d.Oracle
	}
	return shellOracle
}

// publishHost is the host d hands Publish for the repo it ships: d.Host when
// set, else the host that repo's own remote names, which is nil - no pull
// request opened or updated - for a remote with no pull-request host.
func (d Deps) publishHost(remote string) (repohost.Host, error) {
	if d.Host != nil {
		return d.Host, nil
	}
	return repohost.New(remote)
}

// primaryRepo returns v0.1's single repo and its target branch (defaulting
// to "main" when unset).
func primaryRepo(cfg project.Config) (project.Repo, string, string) {
	repo := cfg.Repos[0]
	return repo, repo.Name(), repo.TargetBranch()
}

// operatorClone returns the operator's mapped clone directory for
// repoName, or "" when none is recorded. The branch copy a round reads and
// RoundInput's own OperatorClone (review.go) both resolve through this one
// lookup, so a mapped clone's absence reads the same way in both: no copy to
// read, and nothing the excerpt search can compare a session's own cwd
// against.
func operatorClone(d Deps, repoName string) string {
	if dir, ok := d.Machine.Clones[repoName]; ok && dir != "" {
		return dir
	}
	return ""
}

// CheckIdentity lets a command fail fast, before any frontier or gate work,
// when the operator's mapped clone has no git identity. Without a mapped
// clone it returns nil. `jig solve` calls it before its first session; gate
// and publish commit nothing on the branch and need no identity.
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
