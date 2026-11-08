# Overlap advice is a real conflict, shown where the session looks (GRAPH-24)

Ambiguous: the store-level overlap list named every file two branches both changed, and a project with one workspace listed every pair of ready tickets, so neither said whether two tickets would really collide.

Chosen (all defaulted in the brief):
- Cost: only pairs of tickets that share a changed file are merged, once per pair per repo (`gitx.MergeConflicts`, over `git merge-tree --write-tree --name-only`). A failed call leaves the pair `untested`, never an error. No cap.
- Refs: a ticket's local branch; `origin/<branch>` stands in only when the ticket has no local branch here. Local wins when both exist.
- Store-level `jig next`: the `overlap` table gains a `conflict` column (`conflict`, `clean` or `untested`) and `--json` `overlaps[]` gains `conflict` and `files`. The warning line no longer says "local".
- The ticket in hand: `jig here` and `jig next <T>` print a warning, a `conflict` table (repo, ticket, file, branch) and a help line; a clean sharing pair is one info line there only. `jig gate` and `jig publish` print the conflict block before they do anything else and nothing for a clean pair. The block's help names the ticket of the pair that comes first in next order (rank, then id): the later one is told to stack on it, the first is told which ticket to stack on it.
- JSON: `jig here --json` and `jig next <T> --json` carry a `conflicts` array (omitted when empty). `jig gate` and `jig publish` have no `--json`, so their `conflicts` array is not built; the pull request body carries nothing.
- Never a refusal: exit codes, gate rounds, journal verdicts and publish refusals are unchanged.
- One workspace: `workspaceOverlaps` (and so the chart's `setWorkspaceOverlaps`) returns none when the project has one workspace: the workspaces its repos' manifests declare and every ticket's slices name, across all repos, are one or none (gate round 1, r1-f3). It is judged over the project, not the tickets compared, so a pair is listed or not whatever a third ticket builds. The chart skill reads `conflicts` once branches exist and stacks on a reported one. Cost: before any branch exists, a chart's first wave is not pre-stacked.
- Tests that relied on the old rule give one ticket a second workspace so the project has two.
- Open tickets only (round 1, r1-f1): a branch bound to a closed ticket, a ticket whose work merged (asked of the merge reader; `jig here`, gate and publish use the cached one, so they start no network call) or no ticket record is left out before pairing. A published ticket whose work has not merged stays open.
- For the ticket in hand only its own pairs are merged (r1-f2); the store-level listing still tests every sharing pair.
- Chart skill (r1-f7): the lines the cap had cut are back, and the new instruction replaces the old paragraph within 6000 bytes.
- Not changed (r1-f8): a ticket with several live branches is merged by its alphabetically first one.
