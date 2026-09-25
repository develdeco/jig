package revieweval

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/develdeco/jig/internal/verifydeliver"
)

// RoundScore is one round's scoring result.
type RoundScore struct {
	Round            int
	Found            []string // gold ids matched
	Missed           []string // gold ids not matched (all of them on a refused or failed round)
	Unconfirmed      []string // gold ids matched on structure alone, for a person to confirm
	Lost             []string // gold ids (action fix or ask) matched but whose fate is noted or dismissed
	Forgotten        []string // gold ids with a prior, not matched, whose prior id was cleared
	DroppedQuestions []string // gold ids with action ask matched but whose fate is not asked
	Misattributed    []string // gold ids matched by a finding whose prior is set and differs from the gold's prior
	PriorExpected    int      // matched gold ids that have a prior
	PriorCited       int      // ... whose finding cites exactly that prior
	ActionAgreed     int      // matched gold ids whose finding's action equals the gold action
	FalseAlarms      []string // finding titles
	Relitigated      []string // finding titles
	ExtraTrue        []string // finding titles matched to a kept decision
	Pending          []string // finding titles no one has labeled
	// WrongPriors is every finding whose jig-assigned status is dismissed
	// (it cited an already-dismissed prior) but carries no surviving
	// structural edge to the dismissed fold point that prior names: the
	// citation is not a real repeat, and it fails the round.
	WrongPriors     []string // finding titles
	TriagePrompts   int      // 1 for the fix batch when any fate is open, plus 1 per fate asked
	Refused, Failed bool
	Reason          string
	Passed          bool
	// FalsePositiveGold is true when this round's gold could produce a
	// false alarm on its own - a trap, a dismissed decision, or an
	// exhaustive round. RenderReport's precision line needs it to tell "no
	// false alarm happened" from "no false alarm was ever possible": a
	// round with neither has nothing for precision to measure.
	FalsePositiveGold bool
	// Findings is every finding the round reported, in result order, with
	// how the round scored it. RenderJSON carries them so a person can
	// label a pending finding from the report alone, without the round's
	// work dir.
	Findings []ScoredFinding
}

// ScoredFinding is one reported finding as its round scored it: what the
// reviewer said, jig's own status for it, and either the seeded gold id it
// matched or, when it matched none, the Fate it was classified as.
type ScoredFinding struct {
	File   string `json:"file"`
	Line   int    `json:"line"`
	Title  string `json:"title"`
	Detail string `json:"detail"`
	Action string `json:"action"`
	Risk   string `json:"risk"`
	Prior  string `json:"prior,omitempty"`
	Status string `json:"status"`
	Gold   string `json:"gold,omitempty"`
	Fate   Fate   `json:"fate,omitempty"`
}

// CaseScore is one case's scoring result: every round it ran, and whether
// every one of them passed.
type CaseScore struct {
	Name   string
	Rounds []RoundScore
	Passed bool
}

// hasDismissedDecision reports whether decisions holds any dismissed
// entry.
func hasDismissedDecision(decisions []Decision) bool {
	for _, d := range decisions {
		if d.Decision == DecisionDismissed {
			return true
		}
	}
	return false
}

// falsePositiveGold reports whether a round's gold could produce a false
// alarm on its own: a trap, a dismissed decision, or an exhaustive round.
func falsePositiveGold(gold Gold, decisions []Decision) bool {
	return gold.Exhaustive || len(gold.Traps) > 0 || hasDismissedDecision(decisions)
}

// ScoreRound turns one round's matching result (match.RoundMatch),
// ApplyRound's own reported fate and ClearingAfterTriage's cleared ids
// into this round's RoundScore, against its gold and decisions. findings
// and reported must be the round's result.Findings and ApplyRound's
// output, same length and same order (MatchRound's own contract).
func ScoreRound(round int, gold Gold, decisions []Decision, findings []verifydeliver.ResultFinding, reported []verifydeliver.Finding, cleared []string, match RoundMatch) RoundScore {
	sc := RoundScore{Round: round}

	clearedSet := make(map[string]bool, len(cleared))
	for _, id := range cleared {
		clearedSet[id] = true
	}

	for i, g := range gold.Findings {
		j := match.MatchedFinding[i]
		if j == -1 {
			sc.Missed = append(sc.Missed, g.ID)
			if g.Prior != "" && clearedSet[g.Prior] {
				sc.Forgotten = append(sc.Forgotten, g.ID)
			}
			continue
		}
		sc.Found = append(sc.Found, g.ID)
		if match.StructureOnly[i] {
			sc.Unconfirmed = append(sc.Unconfirmed, g.ID)
		}

		rf, f := reported[j], findings[j]
		if (g.Action == verifydeliver.ActionFix || g.Action == verifydeliver.ActionAsk) &&
			(rf.Status == verifydeliver.StatusNoted || rf.Status == verifydeliver.StatusDismissed) {
			sc.Lost = append(sc.Lost, g.ID)
		}
		if g.Action == verifydeliver.ActionAsk && rf.Status != verifydeliver.StatusAsked {
			sc.DroppedQuestions = append(sc.DroppedQuestions, g.ID)
		}
		if f.Prior != "" && f.Prior != g.Prior {
			sc.Misattributed = append(sc.Misattributed, g.ID)
		}
		if g.Prior != "" {
			sc.PriorExpected++
			if f.Prior == g.Prior {
				sc.PriorCited++
			}
		}
		if f.Action == g.Action {
			sc.ActionAgreed++
		}
	}

	for j := range findings {
		fate, ok := match.Classification[j]
		if !ok || fate == FateSkip {
			continue
		}
		title := findings[j].Title
		switch fate {
		case FateFalseAlarm:
			sc.FalseAlarms = append(sc.FalseAlarms, title)
		case FateRelitigated:
			sc.Relitigated = append(sc.Relitigated, title)
		case FateExtraTrue:
			sc.ExtraTrue = append(sc.ExtraTrue, title)
		case FatePending:
			sc.Pending = append(sc.Pending, title)
		case FateWrongPrior:
			sc.WrongPriors = append(sc.WrongPriors, title)
		}
	}

	// The fix batch is one triage prompt regardless of how many fixes are
	// in it; each ask is its own, separate prompt - jig's own triage UX
	// (route.go), mirrored here as a measurement rather than replayed.
	openAny := false
	for _, rf := range reported {
		switch rf.Status {
		case verifydeliver.StatusOpen:
			openAny = true
		case verifydeliver.StatusAsked:
			sc.TriagePrompts++
		}
	}
	if openAny {
		sc.TriagePrompts++
	}

	matchedGold := make(map[int]string, len(gold.Findings))
	for i, j := range match.MatchedFinding {
		if j != -1 {
			matchedGold[j] = gold.Findings[i].ID
		}
	}
	for j, f := range findings {
		sf := ScoredFinding{
			File: f.File, Line: f.Line, Title: f.Title, Detail: f.Detail,
			Action: f.Action, Risk: f.Risk, Prior: f.Prior,
			Status: reported[j].Status, Gold: matchedGold[j],
		}
		if sf.Gold == "" {
			sf.Fate = match.Classification[j]
		}
		sc.Findings = append(sc.Findings, sf)
	}

	sc.FalsePositiveGold = falsePositiveGold(gold, decisions)

	sc.Passed = len(sc.Missed) == 0 && len(sc.Lost) == 0 && len(sc.DroppedQuestions) == 0 &&
		len(sc.Misattributed) == 0 && len(sc.FalseAlarms) == 0 && len(sc.Relitigated) == 0 &&
		len(sc.WrongPriors) == 0

	return sc
}

// RenderReport renders scores as a text report: one line per case round
// (PASS or FAIL, its non-zero counts, and any reason), then totals across
// every round.
func RenderReport(scores []CaseScore) string {
	var b strings.Builder

	var casesPassed, roundsTotal, roundsPassed int
	var totalFound, totalGold int
	var totalLost, totalForgotten, totalDropped, totalMisattributed int
	var totalFalseAlarms, totalRelitigated, totalExtraTrue, totalWrongPriors int
	var totalRefused, totalFailed int
	var priorExpected, priorCited int
	var actionAgreed int
	var totalUnconfirmed, totalPending int
	var totalTriagePrompts int
	var fpGoldRounds int

	for _, s := range scores {
		if s.Passed {
			casesPassed++
		}
		for _, r := range s.Rounds {
			roundsTotal++
			status := "FAIL"
			if r.Passed {
				roundsPassed++
				status = "PASS"
			}
			gold := len(r.Found) + len(r.Missed)
			fmt.Fprintf(&b, "%s round %d: %s found=%d/%d", s.Name, r.Round, status, len(r.Found), gold)
			for _, part := range []struct {
				label string
				n     int
			}{
				{"lost", len(r.Lost)}, {"forgotten", len(r.Forgotten)}, {"dropped", len(r.DroppedQuestions)},
				{"misattributed", len(r.Misattributed)}, {"false-alarms", len(r.FalseAlarms)}, {"relitigated", len(r.Relitigated)},
				{"wrong-priors", len(r.WrongPriors)},
			} {
				if part.n > 0 {
					fmt.Fprintf(&b, " %s=%d", part.label, part.n)
				}
			}
			if r.Refused {
				b.WriteString(" refused")
			}
			if r.Failed {
				b.WriteString(" failed")
			}
			if r.Reason != "" {
				fmt.Fprintf(&b, " reason: %s", r.Reason)
			}
			b.WriteString("\n")

			totalFound += len(r.Found)
			totalGold += gold
			totalLost += len(r.Lost)
			totalForgotten += len(r.Forgotten)
			totalDropped += len(r.DroppedQuestions)
			totalMisattributed += len(r.Misattributed)
			totalFalseAlarms += len(r.FalseAlarms)
			totalRelitigated += len(r.Relitigated)
			totalExtraTrue += len(r.ExtraTrue)
			totalWrongPriors += len(r.WrongPriors)
			if r.Refused {
				totalRefused++
			}
			if r.Failed {
				totalFailed++
			}
			priorExpected += r.PriorExpected
			priorCited += r.PriorCited
			actionAgreed += r.ActionAgreed
			totalUnconfirmed += len(r.Unconfirmed)
			totalPending += len(r.Pending)
			totalTriagePrompts += r.TriagePrompts
			if r.FalsePositiveGold {
				fpGoldRounds++
			}
		}
	}

	recall := 0.0
	if totalGold > 0 {
		recall = float64(totalFound) / float64(totalGold)
	}
	priorRate := "n/a"
	if priorExpected > 0 {
		priorRate = fmt.Sprintf("%.2f", float64(priorCited)/float64(priorExpected))
	}
	actionRate := "n/a"
	if totalFound > 0 {
		actionRate = fmt.Sprintf("%.2f", float64(actionAgreed)/float64(totalFound))
	}
	triagePerCase := 0.0
	if len(scores) > 0 {
		triagePerCase = float64(totalTriagePrompts) / float64(len(scores))
	}

	fmt.Fprintf(&b, "\ntotals: cases passed %d/%d, rounds passed %d/%d, recall %.2f\n", casesPassed, len(scores), roundsPassed, roundsTotal, recall)
	fmt.Fprintf(&b, "lost %d, forgotten %d, dropped questions %d, misattributed %d, false alarms %d, re-litigated %d, wrong priors %d, refused %d, failed %d\n",
		totalLost, totalForgotten, totalDropped, totalMisattributed, totalFalseAlarms, totalRelitigated, totalWrongPriors, totalRefused, totalFailed)
	fmt.Fprintf(&b, "prior citation rate %s, action agreement %s, triage prompts per case %.2f\n", priorRate, actionRate, triagePerCase)
	fmt.Fprintf(&b, "unconfirmed %d, pending %d\n", totalUnconfirmed, totalPending)
	if fpGoldRounds > 0 {
		// n/a with a zero denominator, like the other rates - a round
		// can hold false-positive gold (a trap, say) yet produce neither a
		// found match nor a false alarm (an entirely missed or refused
		// round), and 0/0 is not a meaningful 0.00.
		denom := totalFound + totalExtraTrue + totalFalseAlarms
		if denom > 0 {
			precision := float64(totalFound+totalExtraTrue) / float64(denom)
			fmt.Fprintf(&b, "precision %.2f\n", precision)
		} else {
			b.WriteString("precision n/a\n")
		}
	} else {
		b.WriteString("precision withheld: no scored round has false-positive gold\n")
	}

	return b.String()
}

// jsonRoundReport and jsonCaseReport are RenderJSON's wire shape: every
// round's findings, grouped by classification, for a person to label -
// Pending above all, since that is the one bucket this package could not
// resolve on its own.
type jsonRoundReport struct {
	Round            int      `json:"round"`
	Refused          bool     `json:"refused,omitempty"`
	Failed           bool     `json:"failed,omitempty"`
	Reason           string   `json:"reason,omitempty"`
	Found            []string `json:"found,omitempty"`
	Missed           []string `json:"missed,omitempty"`
	Unconfirmed      []string `json:"unconfirmed,omitempty"`
	Lost             []string `json:"lost,omitempty"`
	Forgotten        []string `json:"forgotten,omitempty"`
	DroppedQuestions []string `json:"dropped_questions,omitempty"`
	Misattributed    []string `json:"misattributed,omitempty"`
	FalseAlarms      []string `json:"false_alarms,omitempty"`
	Relitigated      []string `json:"relitigated,omitempty"`
	ExtraTrue        []string `json:"extra_true,omitempty"`
	Pending          []string `json:"pending,omitempty"`
	WrongPriors      []string `json:"wrong_priors,omitempty"`
	// Findings is every reported finding with its scoring, the part a
	// person labels from.
	Findings []ScoredFinding `json:"findings,omitempty"`
}

type jsonCaseReport struct {
	Name   string            `json:"name"`
	Passed bool              `json:"passed"`
	Rounds []jsonRoundReport `json:"rounds"`
}

// RenderJSON renders scores as indented JSON: every round's findings with
// their classification, for a person to label (JSON, not text, since a
// labeling tool reads this - RenderReport is the one for a person to
// read).
func RenderJSON(scores []CaseScore) ([]byte, error) {
	out := make([]jsonCaseReport, 0, len(scores))
	for _, s := range scores {
		jc := jsonCaseReport{Name: s.Name, Passed: s.Passed}
		for _, r := range s.Rounds {
			jc.Rounds = append(jc.Rounds, jsonRoundReport{
				Round: r.Round, Refused: r.Refused, Failed: r.Failed, Reason: r.Reason,
				Found: r.Found, Missed: r.Missed, Unconfirmed: r.Unconfirmed,
				Lost: r.Lost, Forgotten: r.Forgotten, DroppedQuestions: r.DroppedQuestions, Misattributed: r.Misattributed,
				FalseAlarms: r.FalseAlarms, Relitigated: r.Relitigated, ExtraTrue: r.ExtraTrue, Pending: r.Pending,
				WrongPriors: r.WrongPriors,
				Findings:    r.Findings,
			})
		}
		out = append(out, jc)
	}
	return json.MarshalIndent(out, "", "  ")
}
