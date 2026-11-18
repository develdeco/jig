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
	// State is the state's name; Told is its words beside the slice (a failed
	// run that judged nothing says why).
	State string `json:"state"`
	Told  string `json:"told"`
	Clear string `json:"clear,omitempty"`
	// Fail is a failed run's first FAIL line; Error a could-not-run
	// verification's summary.
	Fail  string `json:"fail,omitempty"`
	Error string `json:"error,omitempty"`
}

func statesOf(rows []phase.CommitVerification) []hereVerificationState {
	var out []hereVerificationState
	for _, r := range rows {
		if !r.State.Unclear() {
			continue
		}
		out = append(out, hereVerificationState{Slice: r.Slice, Commit: r.Commit, State: string(r.State), Told: r.Told(), Clear: r.Clear, Fail: r.FailLine, Error: r.Error})
	}
	return out
}

// statesTable renders the states as the verification_states table: the state
// cell carries a failed run's first FAIL line, or a could-not-run summary.
func statesTable(states []hereVerificationState) string {
	var rows [][]string
	for _, s := range states {
		told := s.Told
		switch {
		case s.State == string(phase.VerifyFailed) && s.Fail != "" && s.Told == s.State:
			told = "failed: " + s.Fail
		case s.State == string(phase.VerifyCouldNotRun) && s.Error != "":
			told = "could not run: " + s.Error
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

// hereTruth is what `jig here` knows of the located ticket's verifications:
// the journal and, with one git log, the branch. The branch is read only when
// the ticket was found by the branch the work is on; otherwise it judges what
// the journal shows.
func hereTruth(hc hereCtx, lines []journal.Line, ids []string) verify.Truth {
	w := hc.Where
	t := verify.Truth{Ticket: w.Ticket, Lines: lines, Slices: ids}
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
	return t
}

// statusTruth is what `jig status` reads of a ticket's verifications: the
// journal and each repo's clone for the ticket's branch, with one git log per
// clone. why says what is missing when the branch was not read. The two
// sections that need it (last_runs and verification_states) share one read per
// ticket.
type statusTruth struct {
	cfg  project.Config
	mp   project.MachineProject
	memo map[string]statusTruthRead
}

type statusTruthRead struct {
	t   verify.Truth
	why string
	err error
}

func newStatusTruth(cfg project.Config, mp project.MachineProject) *statusTruth {
	return &statusTruth{cfg: cfg, mp: mp, memo: map[string]statusTruthRead{}}
}

func (s *statusTruth) of(st *store.Store, ticket string) (verify.Truth, string, error) {
	if r, ok := s.memo[ticket]; ok {
		return r.t, r.why, r.err
	}
	t, why, err := s.read(st, ticket)
	s.memo[ticket] = statusTruthRead{t, why, err}
	return t, why, err
}

func (s *statusTruth) read(st *store.Store, ticket string) (verify.Truth, string, error) {
	lines, err := journal.Read(st, ticket)
	if err != nil {
		return verify.Truth{}, "", err
	}
	t := verify.Truth{Ticket: ticket, Lines: lines, Slices: sliceIDs(st, ticket)}
	why := "no clone"
	rec, err := st.ReadTicket(ticket)
	if err != nil {
		return t, why, nil
	}
	branch, err := ticketBranch(st, s.cfg, ticket, rec)
	if err != nil {
		return t, why, nil
	}
	for _, r := range s.cfg.Repos {
		dir := s.mp.Clones[r.Name()]
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
	return t, why, nil
}

// verificationStatesBlocks is the status section that tells the commits of the
// ticket whose verification is not clear. Without a branch to read it says the
// check for a missing result was not done rather than that every commit has
// one.
func (s *statusTruth) verificationStatesBlocks(st *store.Store, ticket string) ([]string, error) {
	t, why, err := s.of(st, ticket)
	if err != nil {
		return nil, err
	}
	var blocks []string
	if !t.BranchKnown && why == "no clone" {
		blocks = append(blocks, "no recorded result: not checked ("+why+")")
	}
	if states := statesOf(t.Derive()); len(states) > 0 {
		blocks = append(blocks, statesTable(states))
	}
	return blocks, nil
}

// lastRunsBlocks is the last_runs section read from the shared branch read.
func (s *statusTruth) lastRunsBlocks(st *store.Store, ticket string) ([]string, error) {
	t, _, err := s.of(st, ticket)
	if err != nil {
		return nil, err
	}
	return lastRunsTable(t.LastRuns()), nil
}

// statusSectionsWithBranch is the registered status sections with last_runs
// reading the branch, plus verification_states, both from one read.
func statusSectionsWithBranch(cfg project.Config, mp project.MachineProject) []statusSection {
	shared := newStatusTruth(cfg, mp)
	secs := statusSections()
	for i := range secs {
		if secs[i].Name == "last_runs" {
			secs[i].Blocks = shared.lastRunsBlocks
		}
	}
	return append(secs, statusSection{Name: "verification_states", Blocks: shared.verificationStatesBlocks})
}
