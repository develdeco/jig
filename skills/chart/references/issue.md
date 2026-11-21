# An issue's run

Read this when `jig next <issue>` (or `jig next <chart>`) shows `lands`, `publish` or an `issue` block: the chart plans an issue and its run stops at each landing. A legacy chart (no `issue:`) shows none of them and runs by the core.

Ask `jig next <issue>`: it answers the chart's standing with the prompt in the issue's id (`Take <issue> to PR`, then `Continue <issue>`). The standing is derived: re-read it after every step.

## A landing is where the run stops

The person decides every merge into the issue's branch. A published ticket that has not landed is a row of `lands`, with its prompt `Land <T>`, `after` (tickets that land first; empty when it may be asked for now) and its state. A ticket behind a `merged` edge on it is an `end`: it waits for the landing, nothing else. Everything that does not depend on a held landing goes on: brief, build, publish.

When nothing is left to brief, build, run or publish, the verdict is `CHART_WAITS_LAND`: say the `lands` in order, each with the ticket it frees, and stop. Say the `prompt`; do not merge.

After "Land X" the `land` skill merges and records. If you were running the issue, re-read `jig next <issue>` and go on with what the landing freed, with no new `Continue`; otherwise say the prompt and stop. A row whose state is `merged on the host, not recorded` is a record to make: run `jig land <T>` (it merges nothing) and go on. Never answer a "Land X" the person did not say.

## A multi-task is published, not built

A multi-task is never in `briefs` or `run`. Its tasks run like any, landing into its branch. Once they all landed it is in `publish` (prompt `Publish <T>`): run `jig publish <T> --yes` yourself, with no builder and no review round, after the one yes for the group. It is then a `lands` row and gates its dependents like a task.

## The issue's tail

The `issue` block says where the issue stands (`stage`):

1. `landing`: tickets of the chart have not landed. Nothing else is asked yet.
2. `suite`: every ticket landed, the suite is due. Run `jig verify <issue> --final` yourself, in the background, in the worktree `issue_worktrees` names for each repo (a repo that declares no `suite` has none to run). A task's pull request into the issue's branch need not run the whole suite in CI.
3. `fix`: `LAND_SUITE_FAILED`. Add the fix task `jig next` words as an entry of the chart (intake, then `jig graduate`), blocked `merged` on the chart's last entries, and go on from the briefs: the new entry reopens the landing, so the suite is due again at the new tip.
4. `publish`: ready. Run `jig publish <issue> --yes` yourself after the one yes, or with none under a go-with-your-recommendations prompt. `--without-suite "<reason>"` is a person's call, never the run's.
5. `published`: the issue's pull request is open and the run ends, naming `Land <issue>`. `landed`: the chart is done (`CHART_DONE`).

An unmet precondition other than the suite shows the same way (`unmet`), with its own `why` and `prompt`.

## Several issues in one prompt

"Take A and B to PR" with A and B issues runs A to its first stop (a landing, its publish, or done), then B, never both at once: the cap of four parallel subagents and the files two issues touch are not shared. Report both stops together. Side by side only when the person says so. The order is the one named; an issue suggested to land after another (`jig next` shows `lands_after`) goes after it. No verb takes two issues.
