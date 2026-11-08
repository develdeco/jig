# An overlap in a chart run is a stacked edge

[ADR 0037](0037-a-chart-is-run-from-the-main-session.md) runs a wave of tickets in parallel worktrees and says no edge lies inside a wave. Nothing there reads a ticket's slices, so two tickets of one wave could build the same workspace of a repo at once and meet at the merge. `jig next` (no ticket) already warns of ready tickets that share a workspace (`workspace_overlap`); a chart said nothing.

## Decision

**An overlap is derived over the run and listed, and the skill turns it into an edge.** `jig next <chart>` and `jig status <chart>` carry `workspace_overlaps` (always present, `[]` when none): the tickets of one wave that build in one workspace of a repo, in the same element as `jig next`'s (`{repo, workspace, tickets}`). The text form prints the warning `tickets of one wave build in the same workspace`, the `workspace_overlap` table after the `run` table, and a help line. The chart skill stacks every listed pair: the later ticket gets a `blocked_by` of kind `stacked` on the earlier in its `ticket.yaml`, checked with `jig validate <T>`; the chart is read again and the next wave holds the later ticket.

**Plan order decides the pair's direction.** The tickets of a pair are in the order of `tickets.yaml`, not the wave's order (which puts a ranked ticket first). The later ticket gets the edge.

**Only a pair in one wave, with no edge between them, is listed, and only tickets still to build.** A pair in different waves already has an edge between them. A wave drops the needs that are met, so a stacked pair whose parent is reviewed clean shares a wave again; it is already stacked, so a pair with an edge either way, met or not, is left out, and so is a ticket past build (reviewed, published or behind). The list empties as the edges are added: the skill can act on "every listed pair" and read again. (The first version listed a stacked pair again once the parent's round was clean; the review of #213 found it, F2.)

**It is computed in `cmd/jig`, not in the derivation.** `nextChart.setWorkspaceOverlaps` reads each ticket's slices of a wave and calls `workspaceOverlaps`, the rule `jig next` uses, once per wave. `phase.DeriveChart`, `phase.ChartFacts` and `chartFacts` do not change. A ticket with no slices, or whose files cannot be read, has no site and overlaps nothing.

## Rejected alternatives

- **An implicit edge in `DeriveChart`.** It would hide an order the person can neither see nor change in the ticket's file, and a pure derivation would need the project's repos.
- **Serializing every overlap in jig's own run.** A person who wants two in parallel says so; the skill leaves them in one wave then, and they merge by hand.
- **Listing every pair of the run, edge or not.** The list would never empty, and the skill could not act on "every listed pair".
- **Writing the edge from jig.** jig never edits a ticket's `blocked_by` after graduation. (2026-11-08: `jig depend`, GRAPH-23, now writes `blocked_by` after graduation; this decision keeps the session's edge, not jig's own run, as the overlap answer.)

## Consequences

- Amends [ADR 0037](0037-a-chart-is-run-from-the-main-session.md): a wave may be split by the skill before it runs.
- In a repo where nearly every ticket builds in one workspace, most of a chart chains, at the cost of wall time; it saves the merge by hand when the first lands.
- The `chart` skill gains one sentence and stays under its budget. `CONTEXT.md` and `ARCHITECTURE.md` name the key and where it is computed.
