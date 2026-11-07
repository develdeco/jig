package verifydeliver

// What `jig triage` journaled, read back. A decision is a journal line, and
// nothing applies it until the next gate round closes (recordedTriage), so
// between the two it is only a line. TriageDecisions is the one reading of
// those lines: the round's close (recordedTriage), `jig status`'s asks and the
// review request's open rows all take their decisions from it.

import (
	"fmt"

	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/store"
)

// TriageDecision is the latest `jig triage` line for one finding.
type TriageDecision struct {
	// Ref is the finding the decision is about.
	Ref string
	// Outcome is TriageKeep or TriageDismiss.
	Outcome string
	// Provenance is who decided: "person" (a terminal) or "agent".
	Provenance string
	// Reason is the text given with --reason, "" when none was.
	Reason string
	// Workspace and Oracle are the build target the person chose for a kept
	// ask that lacked part of one ("" for each they did not choose).
	Workspace, Oracle string
	// Pending is whether the line comes after the last gate-round or
	// gate-clean line: the next round that closes applies it. An older line
	// is spent, a decision some earlier round already took.
	Pending bool
}

// TriageDecisions folds a ticket's journal lines into the latest triage line
// per finding id. It filters nothing by round: a finding a later round asks
// about again takes the old decision when that round closes (recordedTriage),
// and Pending is what tells the old line from a fresh one.
func TriageDecisions(lines []journal.Line) map[string]TriageDecision {
	closed := -1
	for i, l := range lines {
		if l.Event == journal.EventGateRound || l.Event == journal.EventGateClean {
			closed = i
		}
	}
	decisions := map[string]TriageDecision{}
	for i, l := range lines {
		if l.Event != journal.EventTriage || l.Ref == "" {
			continue
		}
		ws, oracle := triageTarget(l.Command)
		decisions[l.Ref] = TriageDecision{
			Ref: l.Ref, Outcome: l.Outcome, Provenance: l.Provenance, Reason: l.Summary,
			Workspace: ws, Oracle: oracle, Pending: i > closed,
		}
	}
	return decisions
}

// ReadTriageDecisions is TriageDecisions over ticket's journal in st.
func ReadTriageDecisions(st *store.Store, ticket string) (map[string]TriageDecision, error) {
	lines, err := journal.Read(st, ticket)
	if err != nil {
		return nil, fmt.Errorf("verifydeliver: triage: read journal: %w", err)
	}
	return TriageDecisions(lines), nil
}

// Human reports whether a person at a terminal decided, as opposed to an agent.
func (d TriageDecision) Human() bool { return d.Provenance == "person" }

// Decided reports whether d is a pending keep or dismiss: a decision the next
// round applies, for an ask shown while it waits.
func (d TriageDecision) Decided() bool {
	return d.Pending && (d.Outcome == TriageKeep || d.Outcome == TriageDismiss)
}

// KeepLacksTarget reports whether d keeps f without the build target a fix
// slice needs, so that closing a round leaves f asked (routeRound drops a
// kept ask whose workspace or oracle stays unresolved). It is BuildTargetGaps
// without a manifest: a workspace is missing when neither the finding (which
// ApplyRound derived it for) nor the decision names one, and so is an oracle,
// which ApplyRound had already resolved against the manifest when it could.
// An oracle the reviewer named that the manifest no longer has is not seen
// here, only by the round.
func (d TriageDecision) KeepLacksTarget(f Finding) bool {
	if d.Outcome != TriageKeep {
		return false
	}
	return (f.Workspace == "" && d.Workspace == "") || (f.Oracle == "" && d.Oracle == "")
}

// Label is the decision as `jig status` and the review request show it for f:
// "keep", "dismiss", or "keep (no build target)" for a keep the round will
// leave asked. It is "" for a decision that is not pending.
func (d TriageDecision) Label(f Finding) string {
	if !d.Decided() {
		return ""
	}
	if d.KeepLacksTarget(f) {
		return TriageKeep + " (no build target)"
	}
	return d.Outcome
}
