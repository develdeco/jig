# A gate round and publish run no test, and a ticket's last run is the union of its slices' commands

**Amended by [ADR 0063](0063-an-issue-runs-the-repos-suite-once-by-witnessing.md):** "CI is the only full suite" no longer holds for an issue: `jig verify <issue> --final` runs the repo's declared `suite` oracle once, at the tip of the issue's branch. A task's final run is unchanged.

[ADR 0021](0021-the-gate-reuses-an-oracle-pass-on-the-same-tree.md) had a gate round run every manifest oracle in its own lease, cold, before a reviewer read a line, reusing a pass `jig verify` recorded on the same tree, or one a light delta away. [ADR 0052](0052-jig-witnesses-a-run-and-starts-none.md) moved the builder's run into the builder's own worktree and left jig starting none. The gate's pre-round suite then repeated, in a lease with cold caches, what each slice's builder had run and jig had witnessed, and the rules that existed only to make it cheaper (the reuse of a pass on the same tree, the light delta, the gate's own `gate-oracle` lines chained from round to round, the suite slot calls inside the gate) had no reason left.

## Decision

**A gate round and publish run no test.** `jig gate` opens a round on the head and starts no command in the gate's lease: no manifest oracle, no env class, no suite slot, no `gate-oracle` line, and none of the refusals that existed for a run. Publish already started none; it now removes what it read from the gate's runs. CI is the only full suite. jig adds no test command: it fits any project, whose final run is exactly what its slices already name.

**A ticket's last run is the union of its slices' commands.** `jig verify [T] --final` runs, in the builder's own worktree and shell and at its HEAD, every slice's command of the repo (fix slices included, in `slices.yaml` order, a manifest oracle resolved for its slice's workspace, identical text once) as one `sh -c` of parenthesised commands joined by `&&`, so the first failure ends it. It is recorded as one `final-oracle` line and, like a slice run, covers the head only when the tree was clean before and after and HEAD did not move. It changes no slice's state: it is evidence about the union. `journal.FinalRunAt` answers what the final run at a head is, and only the exact commit counts: no rule borrows a pass from another commit with the same tree.

**The reviewer reads the slices' witnessed passes.** `oracles_passed` in `review.json` and in `jig review context` lists, for each slice of the repo, its latest covering `oracle` pass at a commit in the head's history: the command, the commit, whether that commit has the head's tree (`at_head`) and the seconds, which a reviewer picks a probe's command by ([ADR 0036](0036-the-reviewer-probes-the-critical-paths-and-jig-keeps-the-probes.md), kept on the new rows).

**Publish says whether the head has a final run.** Every form that renders or pushes reports the reviewed head's final run, and the pull request body's Verification says it, with the command. It is recorded and never enforced: a head with none, or whose final run failed, is published all the same.

## Consequences

This supersedes ADR 0021 (the gate reuses a pass on the same tree) and amends [ADR 0017](0017-the-reviewer-reads-the-gate-tests.md): the reviewer still reads and runs a test only to probe, but what it reads are the slices' passes, not the gate's own runs. A ticket whose slices name narrow tests gets those at its last run, e2e journeys included because their slices name them; a project whose slices name a whole-suite oracle gets the whole suite.

The suite slot and the manifest's `heavy:` key, the gate lock, and the removal of `light:` from the manifest are other changes.
