Stack the later (Plan order) on the earlier with `jig depend <later> --on <earlier> --kind stacked` for each `workspace_overlaps` pair before a wave and, with one workspace, each `conflicts` row of `jig next <T>` at each wave's start and after its hand-backs, before Publish; `jig validate <T> --check`, re-read the chart, say so. Parallel only if the person says so (merged by hand).

Re-read `jig next <T>` when a stacked ticket's wave starts.

A `run` ticket in publish is published and behind (`state: behind`): no build subagent. For each `stack` row with a `merge`, run that merge yourself in a checkout of the ticket's branch (its worktree; `worktrees` give the add when it is gone). A clean merge needs no review; one resolved by hand needs `jig gate <T>` and a clean round first. Then publish.
