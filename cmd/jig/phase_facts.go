package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/phase"
	"github.com/develdeco/jig/internal/store"
	"github.com/develdeco/jig/internal/verifydeliver"
)

// derivePhase is where ticket stands: phaseFacts and the ticket's journal,
// derived once (internal/phase). Every command that advises the next step
// asks it, so `jig next`, `jig status`, `jig here` and the refusals name the
// same phase and the same prompt.
func derivePhase(st *store.Store, ticket string) (phase.Ticket, error) {
	return derivePhaseWith(st, ticket, nil)
}

// derivePhaseWith is derivePhase with the merge facts of the blockers read from
// merges (nil: a blocker is met once it published). `jig next` reads them, so
// the phase it names and the state it prints agree on a blocker whose pull
// request is not merged.
func derivePhaseWith(st *store.Store, ticket string, merges mergeSource) (phase.Ticket, error) {
	f, lines, err := phaseFactsWith(st, ticket, merges)
	if err != nil {
		return phase.Ticket{}, err
	}
	return phase.Derive(f, lines), nil
}

// phaseFacts gathers what internal/phase derives a ticket's phase from, and
// the journal lines it reads beside them. It reads the store and writes
// nothing. Blockers are the ones a claim would be warned of or refused for
// before any merge is read (claimProblems and `jig next` read them: see
// phaseFactsWith), each with the prompt of the blocker's own phase.
func phaseFacts(st *store.Store, ticket string) (phase.Facts, []journal.Line, error) {
	return phaseFactsWith(st, ticket, nil)
}

// phaseFactsWith is phaseFacts with the blockers judged by merges, as a claim
// judges them: a blocker is held until its work has merged. nil judges by the
// journal alone, where a blocker is met once it published.
func phaseFactsWith(st *store.Store, ticket string, merges mergeSource) (phase.Facts, []journal.Line, error) {
	return phaseFactsOf(st, ticket, map[string]bool{}, merges)
}

// phaseFactsOf is phaseFacts for a ticket reached through blockers: seen holds
// the tickets already being gathered, so blockers that wait for one another
// stop at the first repeat instead of recursing for good.
func phaseFactsOf(st *store.Store, ticket string, seen map[string]bool, merges mergeSource) (phase.Facts, []journal.Line, error) {
	seen[ticket] = true
	f := phase.Facts{Ticket: ticket}
	rec, err := st.ReadTicket(ticket)
	if err != nil {
		return f, nil, err
	}
	f.Adopted, f.Branch = rec.Adopted(), rec.Branch
	if rec.Closed != nil {
		f.Closed = rec.Closed.Phrase()
	}

	if err := briefFacts(st, ticket, &f); err != nil {
		return f, nil, err
	}
	slices, err := st.ReadSlices(ticket)
	if err != nil {
		return f, nil, err
	}
	questions, err := st.ReadQuestions(ticket)
	if err != nil {
		return f, nil, err
	}
	open := map[string]bool{}
	for _, q := range questions {
		if q.Status == "open" {
			open[q.Slice] = true
		}
	}
	for _, sl := range slices {
		ss, err := st.ReadSliceState(ticket, sl.ID)
		if err != nil {
			return f, nil, err
		}
		f.Slices = append(f.Slices, phase.Slice{
			ID: sl.ID, State: ss.State, Goal: sl.Goal, BlockedBy: sl.BlockedBy, FromGate: sl.FromGate, OpenQuestion: open[sl.ID],
		})
	}

	// The decisions come from the triage lines, which Derive reads.
	asks, _, err := verifydeliver.OutstandingAsks(st, ticket)
	if err != nil {
		return f, nil, err
	}
	for _, a := range asks {
		f.Asks = append(f.Asks, phase.Ask{ID: a.ID})
	}

	for _, b := range rec.BlockedBy {
		blocker := b.Ticket
		if resolved, err := st.ResolveTicket(b.Ticket); err == nil {
			blocker = resolved
		}
		p, err := blockerProblem(st, ticket, blocker, b.Kind, claimDeps{merges: merges})
		if err != nil {
			return f, nil, err
		}
		if p != nil {
			f.Blockers = append(f.Blockers, phase.Blocker{Ticket: blocker, Kind: b.Kind, Reason: p.Message, Prompt: blockerPrompt(st, blocker, seen, merges)})
		}
	}

	if f.Rounds, err = gateRoundsOf(st, ticket); err != nil {
		return f, nil, err
	}
	lines, err := journal.Read(st, ticket)
	if err != nil {
		return f, nil, err
	}
	for _, l := range lines {
		if l.Event == journal.EventPublishDone {
			f.Published = true
		}
	}
	// The open record in the store is the word on an open round: a gate that
	// failed on its oracles journaled its opening and opened nothing.
	if s, err := verifydeliver.ReadStatus(st, ticket); err == nil {
		if s.Open != nil {
			f.Open = &phase.OpenRound{Round: s.Open.Round, Reviewer: s.Open.Reviewer.Session}
		}
	} else {
		f.Open = phase.OpenRoundIn(lines)
	}
	return f, lines, nil
}

// briefFacts fills in what the brief file and its approval say: whether there
// is a brief, its sections, and whether the approval is of the brief as it is
// now.
func briefFacts(st *store.Store, ticket string, f *phase.Facts) error {
	md, err := os.ReadFile(filepath.Join(st.TicketDir(ticket), "brief.md"))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	f.BriefExists = true
	f.Sections = len(store.BriefSections(md))
	state, approval, err := briefApprovalOf(st, ticket)
	if err != nil {
		return err
	}
	f.Approval = state
	f.ApprovedBy, f.ApprovedHow = approval.By, approval.Provenance
	return nil
}

// briefApprovalOf says whether the brief as it is now is the one approved:
// briefApprovalNone, briefApprovalCurrent or briefApprovalChanged, with the
// approval when there is one. `jig status`'s brief block and the phase facts
// both ask it.
func briefApprovalOf(st *store.Store, ticket string) (string, store.Approval, error) {
	approval, approved, err := st.ReadApproval(ticket)
	if err != nil || !approved {
		return briefApprovalNone, store.Approval{}, err
	}
	sum, err := st.BriefSHA256(ticket)
	if err != nil {
		return "", store.Approval{}, err
	}
	if strings.EqualFold(sum, approval.BriefSHA256) {
		return briefApprovalCurrent, approval, nil
	}
	return briefApprovalChanged, approval, nil
}

// blockerPrompt is the prompt of the phase a blocker is at: what to ask to
// move it toward publishing. "" when it cannot be told (the blocker is already
// being gathered, or cannot be read).
func blockerPrompt(st *store.Store, blocker string, seen map[string]bool, merges mergeSource) string {
	if seen[blocker] {
		return ""
	}
	f, lines, err := phaseFactsOf(st, blocker, seen, merges)
	if err != nil {
		return ""
	}
	return phase.Derive(f, lines).Prompt
}

// promptHint is the clause that names the prompt a hint or a refusal sends a
// person to: "; ask: Build SHOP-1", nothing for no prompt.
func promptHint(prompt string) string {
	if prompt == "" {
		return ""
	}
	return "; ask: " + prompt
}

// phaseAdvice is the one line that says what to do for a ticket at its phase:
// the command that moves it on, then the prompt that asks an agent to. `jig
// status`, `jig next` and the refusals all say it, from the same derivation,
// so they never name different next steps.
func phaseAdvice(t phase.Ticket, ticket string) string {
	prompt := promptHint(t.Prompt)
	switch {
	case t.Code == phase.CodeClosed:
		return t.Reason
	case t.Code == phase.CodeDone:
		return ticket + " is published: run `jig next` to see the tickets that are left"
	case t.Current == phase.Brief && awaitsApproval(t):
		return fmt.Sprintf("The brief of %s waits for the person's approval: run `jig validate %s` once they have confirmed it%s (approve its brief)", ticket, ticket, prompt)
	case t.Current == phase.Brief:
		return intakeHint(ticket)
	case t.Code == phase.CodeBlocked:
		return t.Reason + prompt
	case t.Current == phase.Build:
		return fmt.Sprintf("Run `jig next %s` to see what is ready, then `jig claim %s --slice <slice>` to take one%s", ticket, ticket, prompt)
	case t.Code == phase.CodeRoundOpen:
		return fmt.Sprintf("%s: the reviewer ends it with `jig review done %s`%s", t.Reason, ticket, prompt)
	case t.Code == phase.CodeAsksUndecided:
		return t.Reason + prompt
	case t.Current == phase.Review && closedRounds(t) == 0:
		return fmt.Sprintf("Run `jig gate %s` to open a gate round%s", ticket, prompt)
	case t.Current == phase.Review:
		return fmt.Sprintf("Run `jig gate %s` to open the next gate round%s", ticket, prompt)
	}
	return fmt.Sprintf("Run `jig publish %s` to open or update the PR%s", ticket, prompt)
}

// awaitsApproval reports whether the brief of t is drafted and waits for the
// person: the approval is the sub-step that is theirs.
func awaitsApproval(t phase.Ticket) bool {
	for _, s := range t.Of(phase.Brief).Subs {
		if s.Key == "approved" {
			return s.State == phase.SubNeedsYou
		}
	}
	return false
}

// closedRounds is how many gate rounds the review of t has closed.
func closedRounds(t phase.Ticket) int {
	n := 0
	for _, s := range t.Of(phase.Review).Subs {
		if strings.HasPrefix(s.Key, "round:") && s.State == phase.SubDone {
			n++
		}
	}
	return n
}

// gateRoundsOf are the gate rounds the store records as closed, each with its
// verdict from report.yaml: the word publish goes by. A report that cannot be
// read gives a round with no verdict, which is not clean (latestGateRound reads
// it the same way). A clean round whose reviewer built part of the change is
// not clean either.
func gateRoundsOf(st *store.Store, ticket string) ([]phase.Round, error) {
	dir := filepath.Join(st.TicketDir(ticket), "gate")
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var out []phase.Round
	for _, e := range entries {
		m := gateRoundDirRE.FindStringSubmatch(e.Name())
		if m == nil || !e.IsDir() {
			continue
		}
		n, err := strconv.Atoi(m[1])
		if err != nil {
			continue
		}
		r := phase.Round{N: n}
		data, err := os.ReadFile(filepath.Join(dir, e.Name(), "report.yaml"))
		if err == nil {
			var rep struct {
				Verdict     string `yaml:"verdict"`
				Independent string `yaml:"independent"`
				Reviewer    *struct {
					Session string `yaml:"session"`
				} `yaml:"reviewer"`
			}
			if yaml.Unmarshal(data, &rep) == nil {
				r.Verdict = rep.Verdict
				if r.Verdict == phase.VerdictClean && rep.Independent == string(verifydeliver.IndependentNo) {
					r.Verdict = verifydeliver.VerdictNotIndependent
				}
				if rep.Reviewer != nil {
					r.Reviewer = rep.Reviewer.Session
				}
			}
		}
		out = append(out, r)
	}
	return out, nil
}
