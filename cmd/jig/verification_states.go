package main

import (
	"github.com/develdeco/jig/internal/axi"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/locate"
	"github.com/develdeco/jig/internal/phase"
	"github.com/develdeco/jig/internal/project"
	"github.com/develdeco/jig/internal/store"
	"github.com/develdeco/jig/internal/verify"
)

// hereVerificationState is one commit whose verification is not clear, as
// `jig here --json` shows it.
type hereVerificationState struct {
	Slice  string `json:"slice"`
	Commit string `json:"commit"`
	// State is the state's name; Told is its words beside the slice.
	State string `json:"state"`
	Told  string `json:"told"`
	Clear string `json:"clear,omitempty"`
	// Fail is a failed run's first FAIL line.
	Fail string `json:"fail,omitempty"`
}

func statesOf(rows []phase.CommitVerification) []hereVerificationState {
	var out []hereVerificationState
	for _, r := range rows {
		if !r.State.Unclear() {
			continue
		}
		out = append(out, hereVerificationState{Slice: r.Slice, Commit: r.Commit, State: string(r.State), Told: r.State.Told(), Clear: r.Clear, Fail: r.FailLine})
	}
	return out
}

// statesTable renders the states as the verification_states table: the state
// cell carries a failed run's first FAIL line.
func statesTable(states []hereVerificationState) string {
	var rows [][]string
	for _, s := range states {
		told := s.Told
		if s.Fail != "" {
			told = "failed: " + s.Fail
		}
		clear := s.Clear
		if clear == "" {
			clear = "-"
		}
		rows = append(rows, []string{s.Slice, shortSHA(s.Commit), told, clear})
	}
	return axi.Table("verification_states", []string{"slice", "commit", "state", "clear"}, rows)
}

func sliceIDs(st *store.Store, ticket string) []string {
	slices, err := st.ReadSlices(ticket)
	if err != nil {
		return nil
	}
	ids := make([]string, len(slices))
	for i, s := range slices {
		ids[i] = s.ID
	}
	return ids
}

// hereVerificationStates names the commits of the located ticket whose
// verification is not clear. The branch is read, with one git log, only when
// the ticket was found by the branch the work is on; otherwise it judges what
// the journal shows.
func hereVerificationStates(hc hereCtx, lines []journal.Line, ids []string) []hereVerificationState {
	w := hc.Where
	t := verify.Truth{JigHome: hc.JigHome, Ticket: w.Ticket, Lines: lines, Slices: ids, Now: hc.e.now()}
	if (w.TicketHow == locate.HowBranch || w.TicketHow == locate.HowBranchAlias) && w.Repo != "" {
		dir := w.Root
		if dir == "" {
			dir = w.RepoDir
		}
		if start := verify.StartSHA(hc.Store, w.Ticket, w.Repo); start != "" && dir != "" {
			if c, err := verify.BranchCommits(dir, start, "HEAD"); err == nil {
				t.Commits, t.BranchKnown = c, true
			}
		}
	}
	return statesOf(t.Derive())
}

// verificationStatesBlocks is the status section that tells the commits of the
// ticket whose verification is not clear. It reads each repo's clone for the
// ticket's branch; without a branch to read it says the not-verified check was
// not done rather than that nothing is unverified.
func verificationStatesBlocks(e env, jigHome string, cfg project.Config, mp project.MachineProject) func(*store.Store, string) ([]string, error) {
	return func(st *store.Store, ticket string) ([]string, error) {
		lines, err := journal.Read(st, ticket)
		if err != nil {
			return nil, err
		}
		t := verify.Truth{JigHome: jigHome, Ticket: ticket, Lines: lines, Slices: sliceIDs(st, ticket), Now: e.now()}
		why := "no clone"
		if rec, err := st.ReadTicket(ticket); err == nil {
			if branch, err := ticketBranch(st, cfg, ticket, rec); err == nil {
				for _, r := range cfg.Repos {
					dir := mp.Clones[r.Name()]
					if dir == "" {
						continue
					}
					why = "no start sha"
					start := verify.StartSHA(st, ticket, r.Name())
					if start == "" {
						continue
					}
					why = "no branch " + branch
					for _, ref := range []string{"refs/heads/" + branch, "refs/remotes/origin/" + branch} {
						if c, err := verify.BranchCommits(dir, start, ref); err == nil {
							t.Commits, t.BranchKnown = append(t.Commits, c...), true
							break
						}
					}
				}
			}
		}
		var blocks []string
		if !t.BranchKnown && why == "no clone" {
			blocks = append(blocks, "not verified: not checked ("+why+")")
		}
		if states := statesOf(t.Derive()); len(states) > 0 {
			blocks = append(blocks, statesTable(states))
		}
		return blocks, nil
	}
}
