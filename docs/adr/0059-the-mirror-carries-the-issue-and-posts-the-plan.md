# The mirror carries the issue and posts the plan

The mirror projected every ticket and every chart onto a GitHub issue, and made each ticket a sub-issue of its chart's issue. A chart with an `issue:` ([ADR 0057](0057-a-ticket-has-a-kind-and-a-chart-is-one-issues-plan.md)) is one need's plan, and the need already has an issue of its own: a second issue for the chart would be the need twice, and a sub-issue tree would make GitHub the owner of a plan the store owns.

## Decision

**An issue ticket is always mirrored; a task is mirrored by the project's setting.** `trackers: github:` takes `tasks: true|false` (`project.GitHubTracker.Tasks`, absent means false, any other value is refused as a malformed entry). With a `trackers:` entry the mirror always carries every ticket of kind `issue`. The tasks and multi-tasks a chart with an `issue:` lists are mirrored only when `tasks` is true. A legacy chart, its tickets, and a ticket no chart lists are mirrored as before whatever the setting says, so turning it off stops nothing already carried. A task already mirrored when the setting goes off is left open and untouched, and the sync reports how many it no longer carries (`SyncReport.TasksNotCarried`, printed by `jig trackers sync`).

**A chart with an `issue:` has no issue of its own.** `collectItems` makes no chart item for it, so there is no chart body, no sub-issue link and no drift check on sub-issues; a record such a chart already holds is left alone. The issue ticket's issue body is the need's text and the footer, with no slices, brief or pull request block, and its board Status is `ChartStatus` over the statuses of its chart's tickets, mirrored or not (its own status when it is closed or has no chart yet).

**A mirrored task is a full issue that names its need.** It is never a sub-issue. Its body starts with one `Part of #<n>` line for each issue of `store.IssuesOf` that has an issue, then the multi-task's when it sits under one with an issue. A line is left out until its issue exists and the next sync adds it.

**The plan is a comment, never an edit.** For each issue ticket whose chart and issue exist, a sync posts one comment with the chart's tickets in the chart's order, a multi-task's tasks indented under it: `- #<n> <title>` for a ticket with an issue, `- <id or "not graduated"> <title>: <first line of its description>` for one without, `(waits for <ids>)` when it has blocked_by edges, and the chart's footer line. It posts a new comment whenever the rendered text differs from the last one posted, and the sha256 of the last is kept in the issue's record as `synced.plan_sha256`: the comment first, the hash second, so a crash between them can post one duplicate and never lose a comment. A change to the chart's map does not post; a change to what the comment shows (a graduation, a task's issue appearing, a title, the order) does. A dry run reports the comment and writes nothing, and a comment that trips the publish-safety scan is not posted and is a skipped item like a body.

## Consequences

- The issue's record is what binds its tracker item; `store.IssuesOf` and the `Part of` line say which need a task serves. The plan comment is the only place the plan is shown on GitHub, and GitHub's cross-reference from the comment covers the other direction.
- One project setting, no per-issue one: a project that wants its tasks on GitHub gets all of them.
- An edit to the plan comment on GitHub is not drift: comments are never read back, and the next change to the plan posts a new one.
