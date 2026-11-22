Read when the prompt states more than one need, a need comes after an issue was made, or the prompt names a tracker item. The verdict's one step is `chart-verdict`'s; this reference only decides how many issues there are and what each says.

## The need first

Before the verdict, write the need as a short statement of what the person needs, never how the work is planned. Take it from the prompt when intake runs at once ("Fix this: ..."), or from the conversation when the person asks for it after the need was shaped there ("plan what we just discussed"). Show it in the confirmation; it is the `--need` of the verdict's step. It may be reworded while grilling; once the issue exists, a change to it goes to the issue's `body`.

## A tracker item

An item is one need, so one issue, never split whatever the fog and never a row of the table below; what grilling finds for it goes in its chart. jig reads the item, never `gh`: run `jig next 48`, relay its advisories and refuse nothing. The item's title and text are the need, so the verdict's step takes `--item <n>` and no `--need`. A later edit of the item is drift, never a change of need; a change of need goes to the issue's `body`. A bound item is its issue: work in that issue's chart and make nothing. A second session on one item is refused naming the issue: extend that chart.

## Several needs at once

When the prompt states several needs before any is planned, make one confirmation, never one per need. It is a numbered table with a row per need:

| # | Title | The need, in one line | Leans to | Order |
|---|---|---|---|---|

"Leans to" says whether the fog looks like one brief or a chart. "Order" is filled only when one need plainly goes first. Two answers: **as proposed** (recommended), one issue for each need; or **one issue**, one need and one chart for them all. The person may also reword, drop or split a row in the same answer. A prompt that states one need gets no table. Nothing is made before the answer.

## One need after another

Grill in the order the person confirmed. Each need reaches its own verdict and its own step before the next is grilled, and its brief or chart is drafted as `chart-verdict` says. Running them side by side is the chart skill's business once the issues exist. The order is advice, in the table and in the next need's first line: nothing records it and nothing refuses a different one.

## A need raised later

After an issue's chart was made, tell a new request from a change to that issue by one test: could it land on main by itself, so that the person sees it merge without the other?

- **Yes: a new need.** Confirm it in one line (its title and need; "not now" is the other answer), then grill it to its own verdict, whose step makes its issue and chart.
- **No: a change to the issue.** Write it where it belongs: the need's change to the issue's `body`, a planning change to its chart, a briefing change to its task's `brief.md`. Make no issue.
- A change to an issue whose pull request already merged is a new need.

Judge the meaning, never match phrases. When the test is unclear, ask inside the confirmation and recommend one answer. Take "the issues this session made" from the conversation and from `jig recall`: an open issue near the words is extended, never repeated.

## Where a change is written

What the person needs, to the issue's `body` (its `ticket.yaml`); how the work is planned, to the chart (`tickets.yaml` entries, the map's decisions); a briefing, to its task's `brief.md`.
