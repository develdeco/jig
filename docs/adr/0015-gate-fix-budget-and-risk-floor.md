# Gate fix budget and risk floor: bound the fix loop, size fix slices to one session

The gate's fix loop has no budget. Every finding the reviewer routes as a fix
becomes fix work, whatever its risk. The gate repeats until a round comes back
clean, and one round's fixes for a workspace and oracle go into a single slice.

This leads to expensive loops: a round with many low-risk findings can queue 8
or more rounds of builds and reviews. Rounds 5 onward are often low findings
alone. Those rounds hold up a ticket and cost real tokens without blocking
delivery (the low findings do not prevent shipping). Findings beyond a certain
risk matter for correctness; lower risks are notes, not slices. And slices that
grow to 20 findings at 400k context each start paying a context cost on every
round. A fix slice should fit one fresh context window, never force a rerun
of earlier findings in a session.

This ADR introduces three controls:

1. **Fix budget:** once N rounds have queued fix slices, keep the rest for a
   person to triage instead of queuing them.
2. **Risk floor:** a fix whose risk is not in a project-configured list routes
   as a note, never as open.
3. **Fix slice size cap:** split slices so no one carries more than M findings,
   keeping same-file findings together.

All three live in `project.yaml`'s optional `gate` block, with defaults chosen
to stop expensive low-risk loops while keeping the typical case free of
configuration.

## Fix budget

`gate.fix_rounds` is the number of rounds per ticket that may queue unattended
fix slices. Once that budget is reached (once N rounds have appended any fix
slice), the next round parks its fix findings for a person: their status becomes
`asked`, `routed_why: budget` is recorded in `findings.yaml`, and jig never
queues them as fix slices.

Only a person at a terminal can keep a parked finding, and keeping one queues a
fix slice anyway, using the same triage path as any kept ask. `--yes`, a
non-terminal stdin and `jig solve` leave parked findings undecided (they stay
in the open set). `jig status` shows the budget (`fix_budget: 2 of 3 rounds used`)
and a `why` column (with `routed_why: budget` for parked findings).

A value of 0 means every round parks its fixes for a person. Negative values
are refused when `project.yaml` loads.

The budget is counted by how many earlier rounds appended at least one fix
slice, whoever kept it. The scripted source's own fix slices count toward the
budget the same way: a scenario that appends slices on round 1, 2 and 3 reaches
the budget on round 3 even if no reviewer round has run yet.

This follows no-mistakes, where fix attempts are bounded per step and past the
bound a human decides. It also follows Matt Pocock's to-tickets, where each
slice fits one fresh context window.

## Risk floor

`gate.fix_risks` lists the risks whose fix findings become fix slices. A fix
finding whose risk is not in the list is routed as a note: its status becomes
`noted`, `routed_as: note` is recorded in `findings.yaml`, and it never reaches
the triage batch. An `ask` finding keeps its routing at any risk.

A note is still listed in findings.md, the gate report and in what publish
reports, and it follows every rule a reviewer's own note follows (recurrence
bound, outstanding asks, clearing). From there, a developer can decide what to
do with it - fix it later, open a separate ticket, or dismiss it as intended.

The default floor is [high, medium]. Low findings are notes. This stops low-risk
loops without blocking delivery: a ticket can ship even when a round reported only
low findings, because those findings become notes, not open fixes, and a round with
only notes folds clean (becomes note territory, never fix-slices territory).

Valid risks are `high`, `medium`, or `low`. Invalid values are refused when
`project.yaml` loads.

## Fix slice size cap

`gate.fix_slice_findings` is the maximum number of findings one fix slice can
carry. When a group of fixes for one workspace and oracle exceeds the cap, they
are split into multiple slices, each with its own subset. Findings in the same
file always share a slice: if a single file has more findings than the cap, it
gets a slice of its own.

A group that fits in one slice keeps today's id, `fix-<round>-<workspace>-<oracle>`.
A group that is split numbers its slices `fix-<round>-<workspace>-<oracle>-1`,
`fix-<round>-<workspace>-<oracle>-2`, etc. The existing disambiguation still
applies.

Each slice's goal and findings list hold only its own subset of the reported
findings. A kept ask (a single finding, per earlier rules) still gets one slice
each. The packing is greedy: files are taken in path order and packed into
slices until adding another would exceed the cap.

The default cap is 5 findings. This is low enough that each slice enters its
session with a fresh context; a 5-finding slice in a typical codebase is one or
two files, not a batch spanning domains. Must be at least 1. Invalid values are
refused when `project.yaml` loads.

## Configuration

All three keys are optional and default as described above. An absent gate block
is equivalent to all three defaults. An absent block stays absent through a
round-trip (project.yaml unchanged by jig), the same way `routes:` does today.

```yaml
gate:
  fix_rounds: 3                # default: 3
  fix_risks: [high, medium]    # default: [high, medium]
  fix_slice_findings: 5        # default: 5
```

A negative `fix_rounds`, a `fix_risks` entry that is not `high`, `medium` or
`low`, or a `fix_slice_findings` below 1 are all refused when `project.yaml`
loads, before any gate round runs.

## Implementation

The gate's `Gate` function reads `Deps.Cfg.ResolvedGateConfig()` to get the
resolved configuration with defaults applied. `ApplyRound` takes `fixRisks` as
a parameter and uses it to decide whether a fix finding's status becomes `noted`
(below the floor) or stays `open` (eligible for a fix slice). Its override order
is recurrence bound, risk floor, missing build target, budget: the floor is
decided before the build-target rule, so a below-floor fix in a file outside
every declared workspace (or whose recorded oracle no longer resolves) is a
note, not an ask - the build-target rule only ever applied to a would-be open
fix. An outstanding ask still wins over all four, last.

`buildFixSlices` splits each (workspace, oracle) group's kept fixes into
one or more slices with `splitFixGroupIntoSlices`: findings are grouped by
file, files are taken in path order, and each slice is packed as full as
`gate.fix_slice_findings` allows before starting the next one; a file
whose own finding count exceeds the cap gets a slice of its own, over the
cap, rather than being split itself. A group that packs into exactly one
slice keeps its existing id (`fix-<round>-<workspace>-<oracle>`); a split
group's slices are numbered `-1`, `-2`, ... from 1, before the existing
collision disambiguation runs.

The fix budget (slice "fix-budget") is checked in `Gate`, before `ApplyRound`
runs: `verifydeliver.UsedFixBudget` counts how many of the ticket's earlier
gate rounds (1..round-1) appended at least one fix slice, by reading each
round's own `report.yaml`, which now carries that round's fix slice ids
(`fix_slices`) for both sources - a reviewer round's own routing and a
scripted round's scenario slices alike, so either counts toward a later,
reviewer-driven round's budget. `ApplyRound` takes the comparison's result as
`budgetReached`: a finding that clears every other check (recurrence bound,
missing build target, risk floor) and would otherwise become an open fix is
forced to `asked` with `routed_why: budget` instead. `routeRound`'s
`DefaultTriage` skips any ask with `routed_why: budget`, leaving it
undecided whatever its build target - the one mechanism `--yes`, a
non-terminal stdin and `jig solve` all share, so none of them ever keeps a
parked finding. A human at a terminal keeps or dismisses it through the same
per-ask prompt as any other ask; `jig gate`'s own report names the round's
own `fix_budget` (used of limit, and how many findings it parked) only when
it actually parked something.

`findings.yaml` now carries an additive `routed_why` field recorded whenever
jig forces a finding's status to `asked`:
- `budget`: parked because the budget was reached
- `recurrence`: forced by the recurrence bound (second recurrence)
- `build-target`: missing workspace or oracle, unfixable on its own

`jig status` shows the budget as a separate line, and the `why` column lists
`routed_why` values; `jig` route prompt shows a header when parked findings
are offered: "fix budget reached (used of limit): keeping one queues a fix anyway".

## Test seams

Test at these public seams and nowhere finer:
- **`verifydeliver.Gate`** with the reviewer source over the session fake
  backend, as gate_test.go drives it: round verdicts, appended fix slices,
  findings.yaml statuses and routed_as/routed_why.
- **`project.yaml` loading**, through `project`'s own load path: defaults,
  partial loads, and validation refusals.
- **`jig status` and the terminal triage prompt**, through cmd/jig's existing
  render and triage tests.

Tests are hermetic: no network, no real home directory, no claude CLI, and
file-path remotes.

## References

- BG-1 incident (not in public repo) showed 8 gate rounds on low-risk findings
  alone, rounds 5-8 costing roughly a quarter of the ticket's 29M weighted
  tokens and 3 hours.
- no-mistakes (internal policy): fix attempts are bounded per step; only
  blocking findings keep a loop going; past the bound, a human decides.
- Matt Pocock's to-tickets: each slice fits one fresh context window.
