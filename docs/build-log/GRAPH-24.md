# Overlap advice is a real conflict, shown where the session looks (GRAPH-24)

Ambiguous: the store-level overlap list named every file two branches both changed, and a project with one workspace listed every pair of ready tickets, so neither said whether two tickets would really collide.

Chosen (all defaulted in the brief):
- Cost: only pairs of tickets that share a changed file are merged, once per pair per repo (`gitx.MergeConflicts`, over `git merge-tree --write-tree --name-only`). A failed call leaves the pair `untested`, never an error. No cap.
- Refs: a ticket's local branch; `origin/<branch>` stands in only when the ticket has no local branch here. Local wins when both exist.
- Store-level `jig next`: the `overlap` table gains a `conflict` column (`conflict`, `clean` or `untested`) and `--json` `overlaps[]` gains `conflict` and `files`. The warning line no longer says "local".
- The ticket in hand: `jig here` and `jig next <T>` print a warning, a `conflict` table (repo, ticket, file, branch) and a help line; a clean sharing pair is one info line there only. `jig gate` and `jig publish` print the conflict block before they do anything else and nothing for a clean pair. The block's help names the ticket of the pair that comes first in next order (rank, then id): the later one is told to stack on it, the first is told which ticket to stack on it.
- JSON: `jig here --json` and `jig next <T> --json` carry a `conflicts` array (omitted when empty). `jig gate` and `jig publish` have no `--json`, so their `conflicts` array is not built; the pull request body carries nothing.
- Never a refusal: exit codes, gate rounds, journal verdicts and publish refusals are unchanged.
- One workspace: `workspaceOverlaps` (and so the chart's `setWorkspaceOverlaps`) returns none when the slices of the tickets it compares name a single workspace, and the `workspace_overlap` warning is not printed. The tickets compared are the ready tickets of the store, or the tickets of a chart's waves, not every ticket of the project. The chart skill reads `conflicts` once branches exist and stacks the later ticket on a reported conflict. Cost: before any branch exists, a chart's first wave is not pre-stacked.
- Tests that relied on the old rule give one ticket a second workspace so the project has two.
