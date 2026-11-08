package verifydeliver

// A carried round (GATE-16): a head that only merges the target into the head
// a reviewer read has nothing new to read, so the gate runs the oracles, as it
// always does, and records the round itself with the verdict of the round that
// was really reviewed. No reviewer is opened and no file is left to close.

import (
	"fmt"
	"os"
	"strings"

	"github.com/develdeco/jig/internal/gitx"
	"github.com/develdeco/jig/internal/journal"
	"github.com/develdeco/jig/internal/store"
)

// carryOver records the round r opened as a clean round carrying over the
// verdict of the round that reviewed the branch before, when prep says the
// head adds nothing to read (prep.Empty) in every repo and each repo's head is
// that round's own or only merges of the target past it (classifyLine, with
// the target ref the gate uses). It reports false, recording nothing, for any
// other round: a hand-resolved merge, an ordinary commit, a first round, a
// round a scripted gate recorded, or an earlier verdict that left work open.
func (r *gateRun) carryOver(prep ReviewPrep) (GateReport, bool, error) {
	if r.n < 2 || len(prep.Repos) != len(r.repos) {
		return GateReport{}, false, nil
	}
	if !isClean(r.fold.Known) {
		return GateReport{}, false, nil
	}
	repos := make([]RoundRepo, len(r.repos))
	for i, gr := range r.repos {
		repos[i] = RoundRepo{Name: gr.name, Target: gr.target, LeaseDir: gr.lease.Dir}
	}
	origin, rep, ok, err := carriedFrom(r.d.Store, r.ticket, r.n, prep)
	if err != nil || !ok {
		return GateReport{}, false, err
	}
	heads, targetSHA := map[string]string{}, map[string]string{}
	for i, rp := range prep.Repos {
		heads[rp.Repo] = rp.Head
		tip, err := gitx.RevParse(repos[i].LeaseDir, "origin/"+repos[i].Target)
		if err != nil {
			return GateReport{}, false, inRepo(r.qualified, rp.Repo, fmt.Errorf("verifydeliver: gate: resolve origin/%s: %w", repos[i].Target, err))
		}
		targetSHA[rp.Repo] = tip
	}

	report := GateReport{
		Round: r.n, Verdict: "clean", TargetSHA: targetSHA,
		Intent: r.intent, IntentSHA256: r.intentSHA256,
		// Who read the round that was really reviewed, and whether they were
		// independent of the builders: inherited, and no new claim (ADR 0046).
		Reviewer: rep.reviewerOf(), Independent: rep.Independent,
		OracleNames:  prep.Request.Oracles,
		Copy:         r.copies(),
		ReviewedSHA:  heads,
		ReviewedBase: heads,
		Scope:        "delta",
		CarriedFrom:  origin,
	}
	if r.gb.Adopted {
		report.Branch = r.branch
	}
	findings := fmt.Sprintf("# Gate round %d\n\n%s\n", r.n, carriedNote(origin, prep, r.qualified, targetSHA))
	if err := writeCarriedRound(r.d, r.ticket, r.n, report, findings); err != nil {
		return GateReport{}, false, err
	}
	if err := os.Remove(openRoundPath(r.d.Store, r.ticket, r.n)); err != nil && !isNotExist(err) {
		return GateReport{}, false, fmt.Errorf("verifydeliver: gate: clear the open round: %w", err)
	}
	if err := journal.Append(r.d.Store, r.ticket, journal.Line{Event: journal.EventGateClean, Attempt: r.n, Carried: origin}); err != nil {
		return GateReport{}, false, fmt.Errorf("verifydeliver: gate: journal gate-clean: %w", err)
	}
	if err := r.d.Store.Push(fmt.Sprintf("%s: gate round %d clean (carried over from round %d)", r.ticket, r.n, origin)); err != nil {
		return GateReport{}, false, fmt.Errorf("verifydeliver: gate: push store: %w", err)
	}
	return report, true, nil
}

// carriedFrom is the round whose verdict round n carries over, with its report,
// when every repo's head is that round's own or only merges of the target past
// it (prep's Line) and a reviewer read that round: false for a first round, a
// round a scripted gate recorded, a hand-resolved merge and anything else. A
// carried round names the round that was really reviewed, so a chain of them
// all name the one.
func carriedFrom(st *store.Store, ticket string, n int, prep ReviewPrep) (origin int, rep reportYAML, ok bool, err error) {
	if n < 2 || len(prep.Repos) == 0 {
		return 0, reportYAML{}, false, nil
	}
	merged := false
	for _, rp := range prep.Repos {
		if rp.Prior == "" || (rp.Line != lineSame && rp.Line != lineClean) {
			return 0, reportYAML{}, false, nil
		}
		merged = merged || rp.Line == lineClean
	}
	if !merged {
		// The gate run again on the head a reviewer read opens a round, as it
		// always has: only a merge brings in something to carry over.
		return 0, reportYAML{}, false, nil
	}
	// A decision a person made since the last round is applied by the next
	// round that closes, and a carried round applies nothing: it waits for a
	// round a reviewer reads.
	decisions, err := ReadTriageDecisions(st, ticket)
	if err != nil {
		return 0, reportYAML{}, false, err
	}
	for _, d := range decisions {
		if d.Pending {
			return 0, reportYAML{}, false, nil
		}
	}
	round, rep, err := priorRound(st, ticket, n-1)
	if err != nil {
		return 0, reportYAML{}, false, fmt.Errorf("verifydeliver: gate: read round %d report.yaml: %w", n-1, err)
	}
	if round == 0 {
		return 0, reportYAML{}, false, nil
	}
	origin = round
	if rep.CarriedFrom > 0 {
		origin = rep.CarriedFrom
	}
	return origin, rep, true, nil
}

// carriedNote is the sentence findings.md carries for a carried round: whose
// verdict it is and why it holds. targetSHA is each repo's origin/<target>
// (RoundRepo targets are named by the caller through repos).
func carriedNote(origin int, prep ReviewPrep, qualified bool, targetSHA map[string]string) string {
	var parts []string
	for _, rp := range prep.Repos {
		what := fmt.Sprintf("head %s is the head round %d reviewed", rp.Head, origin)
		if rp.Line == lineClean {
			what = fmt.Sprintf("head %s merges the target (%s) and its tree equals git's own merge", rp.Head, targetSHA[rp.Repo])
		}
		if qualified {
			what = rp.Repo + ": " + what
		}
		parts = append(parts, what)
	}
	return fmt.Sprintf("Carried over from round %d: %s; the oracle ran and passed.", origin, strings.Join(parts, "; "))
}

// carriedOver is carriedFrom and carriedNote for a launched round with nothing
// to read: the round it carries, or 0 and no note.
func carriedOver(in RoundInput, prep ReviewPrep) (origin int, note string, err error) {
	origin, _, ok, err := carriedFrom(in.Store, in.Ticket, in.Round, prep)
	if err != nil || !ok {
		return 0, "", err
	}
	tips := map[string]string{}
	for _, r := range in.repoList() {
		if tip, terr := gitx.RevParse(r.LeaseDir, "origin/"+r.Target); terr == nil {
			tips[r.Name] = tip
		}
	}
	return origin, carriedNote(origin, prep, in.qualified(), tips), nil
}

// reviewerOf is the reviewer a report records, the zero value for a round
// that records none.
func (rep reportYAML) reviewerOf() Provenance {
	if rep.Reviewer == nil {
		return Provenance{}
	}
	return *rep.Reviewer
}

// writeCarriedRound writes a carried round's files: a clean round with the
// findings.md that says whose verdict it carries, and its report.yaml and
// diff-changelog.md as any clean round has.
func writeCarriedRound(d Deps, ticket string, n int, report GateReport, findings string) error {
	dir := gateRoundDir(d.Store, ticket, n)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("verifydeliver: gate: create round dir: %w", err)
	}
	if err := os.WriteFile(dir+"/findings.md", []byte(findings), 0o644); err != nil {
		return fmt.Errorf("verifydeliver: gate: write findings.md: %w", err)
	}
	if err := writeReportYAML(dir, report); err != nil {
		return err
	}
	return writeDiffChangelog(d, ticket, dir, n)
}
