package verifydeliver

import (
	"fmt"

	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/manifest"
	"github.com/develdeco/jig/internal/store"
)

// SlicePass is one entry of review.json's "oracles_passed" list: a slice's
// latest covering pass of its own oracle, run by the slice's builder and
// witnessed by jig at Commit, which is the head or one of its ancestors. A
// gate round runs no command, so these are the only runs the reviewer is told
// of, and it reads by what they cover and runs a test only to probe.
type SlicePass struct {
	Slice     string `json:"slice"`
	Workspace string `json:"workspace"`
	// Command is the command the builder's run executed, as the oracle line
	// recorded it.
	Command string `json:"command"`
	// Commit is the commit the pass was witnessed at.
	Commit string `json:"commit"`
	// AtHead is "yes" when Commit's tree is the head's tree, so the pass is of
	// the code under review exactly, and "no" when the builder's pass was of an
	// earlier tree.
	AtHead string `json:"at_head"`
	// Seconds is the pass's wall time, rounded up to whole seconds. A reviewer
	// picks a probe's command by it (ADR 0036).
	Seconds int `json:"seconds"`
}

// atHeadWords are AtHead's two values.
const (
	passAtHead    = "yes"
	passNotAtHead = "no"
)

// witnessedPasses lists, for each slice of the ticket that builds in the repo
// (fix slices included), its latest covering oracle pass whose commit is in
// the history of dir's HEAD, in slice order. A slice with no such pass has no
// row: a pass that covers nothing (it names an Uncovered reason), a failure,
// and a pass on a commit that is not in the head's history stand for nothing.
// repo and man place each slice's workspace as the manifest names it.
func witnessedPasses(st *store.Store, ticket, repo, dir string, man manifest.Manifest, slices []store.Slice) ([]SlicePass, error) {
	lines, err := journal.Read(st, ticket)
	if err != nil {
		return nil, fmt.Errorf("verifydeliver: gate: read the journal for the slices' passes: %w", err)
	}
	head, err := gitx.RevParse(dir, "HEAD")
	if err != nil {
		return nil, fmt.Errorf("verifydeliver: gate: resolve HEAD for the slices' passes: %w", err)
	}
	headTree, err := gitx.RevParse(dir, "HEAD^{tree}")
	if err != nil {
		return nil, fmt.Errorf("verifydeliver: gate: resolve HEAD's tree: %w", err)
	}
	inHistory := map[string]bool{}
	history := func(commit string) bool {
		if v, ok := inHistory[commit]; ok {
			return v
		}
		ok, err := gitx.IsAncestor(dir, commit, head)
		inHistory[commit] = err == nil && ok
		return inHistory[commit]
	}
	atHead := func(commit string) string {
		tree, found, _ := gitx.Resolve(dir, commit+"^{tree}")
		if found && tree == headTree {
			return passAtHead
		}
		return passNotAtHead
	}

	latest := map[string]journal.Line{}
	for _, l := range lines {
		if l.Event != journal.EventOracle || l.Slice == "" || l.Outcome != "pass" || l.Uncovered != "" || l.Commit == "" || l.Command == "" {
			continue
		}
		if !history(l.Commit) {
			continue
		}
		latest[l.Slice] = l
	}
	passes := []SlicePass{}
	for _, s := range slices {
		l, ok := latest[s.ID]
		if !ok {
			continue
		}
		workspace := s.Workspace
		if ws, found := man.WorkspaceIn(repo, s.Workspace); found {
			workspace = ws.ID
		}
		passes = append(passes, SlicePass{Slice: s.ID, Workspace: workspace, Command: l.Command, Commit: l.Commit, AtHead: atHead(l.Commit), Seconds: l.Seconds})
	}
	return passes, nil
}
