# An issue's run stops at each landing, and records but never makes one

[ADR 0037](0037-a-chart-is-run-from-the-main-session.md) ended a chart's run at a ticket behind a `merged` dependency and said "do not wait for a merge": a run took tickets to published and the person merged by hand. Under the issue model the person decides every merge into the issue's branch, the session makes it at "Land X" ([ADR 0061](0061-land-is-a-phase-the-session-merges-at-the-persons-word.md)), and the issue gets one suite run ([ADR 0063](0063-an-issue-runs-the-repos-suite-once-by-witnessing.md)) and one pull request ([ADR 0062](0062-a-container-publishes-once-its-children-have-landed.md)) after every ticket has landed. A chart whose last tickets were published read as done, though nothing had reached the target.

## Decision

**A landing is an end of the run.** For a chart that sets `issue:` (a model chart), `phase.DeriveChart` adds `lands`: every published, unlanded ticket in row order, each with the prompt `Land <T>`, the tickets that must land first (`land.Rules`) and its Land state; a published multi-task is one, and the issue's own landing is the last row. A dependent of an unlanded ticket is an `end` as before, its reason `waits for <T> to land into <branch>`. The run goes on with everything else reachable first, and stops when nothing is left to brief, build, run or publish: `CHART_WAITS_LAND`, with the prompt `Continue <issue>`. A held landing holds only its dependents; the run does not keep the issue's branch quiet.

**A model chart is done when its issue has landed.** `CHART_DONE` for a legacy chart keeps its meaning (every ticket published or closed), so every chart in a store reads as before.

**The run records a landing and never makes one.** After "Land X" a session that was running the issue re-reads `jig next <issue>` and goes on, with no new `Continue`; one that was not says the prompt and stops. A merge made on the host is a `lands` row whose state is `merged on the host, not recorded`: the run runs `jig land <T>` for it, which only records. The run merges nothing itself and answers no "Land X" the person did not say.

**A multi-task is published and landed, never built.** It is in no `briefs` or `run`. Once its tasks have landed it is in `publish`, and the main session runs `jig publish <T> --yes` itself, no builder and no review round.

**The issue's tail is one block.** `jig next <issue>` answers the chart's standing with the prompt in the issue's id (an issue with no chart prints as before), and carries an `issue` block whose stage is landing, suite (the main session runs `jig verify <issue> --final` in the worktree the block names), fix (a suite failed: add the fix task to the chart and go on), publish, published (the run ends, naming `Land <issue>`) or landed. An unmet precondition other than the suite shows the same way.

**Several issues in one prompt run one after another.** "Take A and B to PR" runs A to its first stop, then B, never both at once, so the cap on parallel subagents and the files two issues touch are not shared; the order is the one named, or an issue's suggested `after`. No verb takes two issues: this is skill text.

## Consequences

Amends [ADR 0037](0037-a-chart-is-run-from-the-main-session.md) for a model chart: its run ends at a landing, not only at a merge. The help of any chart says `Land <T> (or merge it on the host), then ask: <prompt>` where it said "Merge". `jig next <issue>` of an issue with a chart answers the chart's standing, whose `issue` block carries what `jig next <issue>` said of the issue before. The TUI is its own chart: it keeps polling while `ends` is non-empty and ignores the new keys.
