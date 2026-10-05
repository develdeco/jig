# RR-1 - retrieval notes

## risk-floor

- goal: project.yaml gains the optional gate: block (fix_rounds, fix_risks, fix_slice_findings) with its defaults and load-time refusals, carried on project.Config and read by the gate from Deps.Cfg, as the brief's "Gate config" section specifies. A fix finding whose risk is not in fix_risks routes as a note (status noted, routed_as: note), so a round whose only fixes are below the floor folds clean, as "Low findings are notes" specifies. Test at the seams the brief names. Start ADR 0015 with the config and the risk floor, and add the gate: block to ARCHITECTURE.md.
- commit: 8ba19fc

## fix-budget

- goal: The gate counts a ticket's rounds that appended fix slices; once that equals gate.fix_rounds, a round parks each would-be open fix as an ask with routed_why: budget, and DefaultTriage (--yes, no terminal, jig solve) leaves parked findings undecided, as "The fix budget" specifies. findings.yaml gains routed_why for every forced ask (budget, recurrence, build-target). jig status shows the fix_budget line, a why column in outstanding_asks and the parked help line; the triage prompt and the gate report say the budget is reached, as "What status, triage and the gate report show" specifies. Test at the seams the brief names. Extend ADR 0015, ARCHITECTURE.md (routed_why) and CONTEXT.md (fix budget, parked finding).
- commit: c0022f682aa43a74b9e682d1a981e5a04289d450

## fix-slice-size

- goal: A round's kept fixes, still grouped by (workspace, oracle), are split so no fix slice carries more than gate.fix_slice_findings findings: same-file findings share a slice, files are packed greedily in path order, a file over the bound gets its own slice, a group that fits keeps today's id and a split group's slices end in -<k>, as "Fix slices sized to one session" specifies. Test at the seams the brief names. Finish ADR 0015 and ARCHITECTURE.md with slice sizing, and add the two or three line DECISIONS.md entry pointing at ADR 0015.
- commit: 39056ab42392afc0e35acfe1765e6d9b31a9eb2f

## suite-green

- goal: The full suite fails at the gate (go test ./...) in two packages the slice oracles did not cover. (1) internal/revieweval: runner.go (and match_test.go) now pass ApplyRound a [high, medium] risk floor, so the eval scores low fix findings as lost (forgotten-finding, mechanical-batch, nit-disguise and the leak/real-child tests fail). The brief puts revieweval's scoring out of scope: the eval measures the reviewer, so it passes every risk (high, medium, low) and no budget, and its scoring is exactly what it was. (2) e2e: the status-gate-round1.txt golden lacks the new fix_budget line (update the golden to the new, intended output), and TestGateReviewerNonTerminalTriage expects r1-f2 open while its scenario labels it low, so it is now noted; keep what that test is about (non-terminal triage keeps every fix) by making that scenario finding a risk the default floor loops on, not by weakening the assertion. Then run go test ./... and leave it green.
- commit: 6cd3e30cffc96ccfa456a794810bfd1d7e82ad1c

## fix-1-root-test

- goal: Fix these gate findings:

internal/project/project.go:56 GateConfig is not gofmt-aligned, so CI's gofmt gate fails
GateConfig aligns its field types one column past what gofmt computes (FixRounds + 9 spaces, FixSliceFindings + 2), so `gofmt -l .` prints internal/project/project.go - it is the only unformatted file in the tree. CI's test job runs `test -z "$(gofmt -l .)"` on Linux (.github/workflows/ci.yml) and .github/CONTRIBUTING.md states gofmt must print nothing, so the branch is red before any test runs. `gofmt -w internal/project/project.go` is the whole fix; verified it rewrites only those three lines.
Risk (low): Formatting only, no runtime behaviour, but it fails a required CI gate for the whole branch.

internal/verifydeliver/findings.go:342 A below-floor fix in a file with no workspace is parked as an ask instead of noted
In ApplyRound's override switch, `case missingBuildTarget` is tested before `case belowFloor`, so a `fix` finding whose risk is not in gate.fix_risks and whose file lies in no declared workspace (or whose recorded oracle no longer resolves) becomes `asked` with `routed_why: build-target` rather than `noted`. The brief's "Low findings are notes" routes such a finding as a note and then says it "follows every rule a reviewer's own note follows. In particular, the recurrence bound and 'an ask already put to a person stays asked' still move it to asked" - the build-target rule is not among them, and it only ever applied to a would-be open fix. ADR 0015 states the same ordering (budget parks "a finding that clears every other check (recurrence bound, missing build target, risk floor)"). The consequence is the brief's first acceptance criterion failing for any file outside every workspace (a root-level doc, say): the round cannot fold clean, publish stays blocked, and a human is summoned to a terminal over a low finding the project does not loop on. Swapping the two cases (floor first) is enough, and no test pins the current order - TestApplyRoundBudgetNeverOverridesAMissingBuildTargetsOwnReason uses a high-risk finding.
Risk (medium): It defeats the risk floor's own acceptance criterion for files outside a declared workspace, and the failure mode is exactly the one this ticket exists to remove: a low finding keeping a round open and demanding a human.
- commit: 8bbaca7

