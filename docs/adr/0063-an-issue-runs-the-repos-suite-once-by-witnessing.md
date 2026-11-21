# An issue runs its repo's suite once, by witnessing, and publish waits for it

[ADR 0053](0053-a-gate-round-and-publish-run-no-test.md) left CI the only full suite: a task's gate and publish run no test, and its last run is the union of its slices' commands. An issue has no slices, and its tasks land into its branch one by one, each passing only its own slices. Nothing proved the whole before its pull request opened, and a project's CI on a pull request into an issue's branch would run the whole suite once per task. [ADR 0062](0062-a-container-publishes-once-its-children-have-landed.md) gave the publish of a container one seam for what else it waits for.

## Decision

**The suite is the repo's declared oracle named `suite`.** A repo that wants its issues proven declares `oracles: suite: <command>` in `.claude/jig.yaml` ([ADR 0055](0055-a-repo-with-no-declaration-has-no-named-oracle.md)): no new key, no default, `{path}` resolved against the root workspace. A repo with no `suite` has nothing to run and nothing waits on it; jig says so. The name is read for an issue only: no slice, task or multi-task run resolves it.

**jig runs it only by witnessing.** `jig verify <issue> --final` runs the `suite` command in the worktree it is called in, at that worktree's HEAD, and records one `final-oracle` line with no slices, under the covering rules of any final run (clean tree before and after, HEAD did not move). It is made once per repo of the issue, in the repo's own worktree on the issue's branch at its tip. jig starts no suite on its own and schedules nothing ([ADR 0052](0052-jig-witnesses-a-run-and-starts-none.md)). Its footer says a task's pull request into the branch need not run the whole suite in CI; jig reads no CI file and enforces nothing about it.

**Publish waits for it through the seam.** `land.Preconditions` gains one entry. For each repo of the issue it reads the manifest at `origin/<branch>`'s tip and the issue's journal at exactly that tip, once every child has landed: a covering pass, a waiver, or no declared suite meets it; a declared suite with no covering run at the tip is `LAND_SUITE_NOT_RUN`; a covering fail is `LAND_SUITE_FAILED`, whose prompt is to add a fix task. A run at an earlier tip counts for nothing, so a landed fix task moves the tip and the suite is due again. A fix task is an entry the session adds to the issue's chart, blocked on the chart's last entries, and lands like any task; jig mints nothing.

**A person can publish without it, and it is recorded and shown.** `jig publish <issue> --yes --without-suite "<reason>"` writes one `suite-waived` line per repo whose suite is unmet, with the tip it waives, the reason and who said it (`agent` or `person`), and publish then proceeds. A waiver stands for that tip only. jig records it and never judges who may say it ([record, not enforce](0046-a-rounds-independence-is-recorded-not-enforced.md)). `jig next` shows a waived suite with its reason, and the pull request body gains one `Verification` line per repo saying what ran, was waived, was never declared, or that no run is at the head.

## Consequences

jig's own repo declares no `suite`: whether its issues run CI's suite locally is the owner's call. A merge of the target into the issue's branch moves the tip, so the suite is due again. A multi-task has no suite.
