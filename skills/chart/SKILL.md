---
name: chart
description: Runs a chart's tickets from the main session - every brief first, then one build subagent per ticket in its own worktree. Use when the person says "Take <chart> to PR" or "Continue <chart>", or says yes to taking a chart's tickets on.
---

# chart

Run `jig next <chart>`. If the chart is not `ready`, relay its `reason` and `prompt` and stop. Entries with no ticket need `jig graduate <chart>` first. The standing is derived: re-read it.

Three prompts make a chart run: one aimed at a chart, a yes to intake's confirm question, intake's explicit launch. One ticket alone is not a chart run: run it alone.

You are the main session. Every question to the person is yours; a subagent cannot ask. jig starts no session, records no run.

## Briefs

`briefs` lists the tickets to brief; brief them all before any build: a subagent per ticket (sonnet, background) drafts its brief with intake (its ticket exists: never mint or approve), marks each recommended answer it takes, checks it with `jig validate <T> --check`, hands back its questions. Meanwhile ask a numbered round (with recommendations), record answers, send a changed one to its drafter, confirm once, then `jig validate <T>` each. The person's moments stay theirs: ask and wait, unless told to go with your recommendations (ADR 0035). Nothing to brief: go on.

## Builds

Before the first wave, ask once to allow what the builds run (`jig`, `git`, the tickets' oracles); one yes covers the run. A run going with the recommendations, intake's explicit launch included, asks nothing: permission settings decide, a refused command hands its ticket back and the report names it. jig sets no permission.

Before each wave, stack every pair in `workspace_overlaps`: the later ticket (Plan order) gets a `stacked` `blocked_by` on the earlier in its `ticket.yaml`; check with `jig validate <T> --check`, re-read the chart, say so. Two in parallel only if the person says so (merged by hand).

Go through `run` wave by wave; no edge lies inside a wave. Start a subagent per ticket of the wave (`subagent_type: general-purpose`, `model: sonnet` unless the person named another, background), each in its own worktree: run the `add` commands of the ticket's `worktrees` (`jig next <T>`, or the chart's), one at a time, and re-read `jig next <T>` when a stacked ticket's wave starts. `present`: use its `dir`. `no_clone`: tell the person; do not start it. Its prompt:

```
You are building ticket <T>. Work only in the worktree of each repo (`dir` of its rows), on branch <branch>; run every command there.
Build it with `jig skill build`: claim each slice with `--unattended`, commit with its `Jig-Slice` trailer.
Slices ready in several repos (`jig next <T>`): build one repo yourself; start a nested builder per other repo in the background with this prompt, told to build only its repo's slices, never gate or nest (a hold saying to gate is yours: it hands back), and wait for them before the gate. Relay a builder's question: end your turn with the question, its id and default; resume it when resumed. No Agent tool: build repos in turn, hand back at green; I start the reviewer.
Stuck: `jig ask --slice <slice> "<question>"` with your default, then end your turn with the question, id and default. I answer and resume you.
When every slice is green, open the gate (`jig gate <T>`) and have your own reviewer subagent review it if you have the Agent tool (`jig skill review`). A round that only queues fixes: build them, have them reviewed again. End your turn when a round is clean or leaves an ask, with its verdict, or as `jig gate` says.
Never publish. Never answer your own question.
```

Wait for the hand-backs (TaskOutput `block: true`, else the notice; none if the call returned one). jig holds a subagent at its stop while a verification runs, changes are uncommitted, or every slice is green with no round open: not stuck, wait.

A `run` ticket in publish is published and behind (`state: behind`): no build subagent. For each `stack` row with a `merge`, run that merge yourself in a checkout of the ticket's branch (its worktree; `worktrees` give the add when it is gone). A clean merge needs no review; one resolved by hand needs `jig gate <T>` and a clean round first. Then publish.

## Relay

A subagent hands back a question: put it to the person with its default, record the answer with `jig answer <T> <question> "<answer>"`, then resume the subagent with SendMessage (a nested builder's: via its ticket subagent). A hand-back at green with no reviewer: run `jig gate <T>`, start the reviewer yourself (`jig skill review`). A review's asks are the person's too: put each, record `jig triage <T> <finding> keep|dismiss`, and resume the subagent to build the fixes and have them reviewed again.

## Publish

Tickets whose `jig next <T>` names publish are reviewed clean. Ask once for the group (each reviewed head, its body's first lines), then a subagent per ticket publishes it (`jig skill publish`: demo, body, CI budgets), removing no worktree, and hands back the url; a stacked ticket's publisher starts after its parent's hands back. A go-with-your-recommendations prompt needs no yes. Remove a ticket's worktrees only once it is published, one at a time, with the `remove` commands of `jig publish --pr` or `jig next <T>`; git refuses one with changes: commit or discard them, never force.

## Ends

After each wave re-read `jig next <chart>` and start the next. A ticket behind a `merged` edge, or nothing reachable, ends the run: say what is published, what waits on which pull request (`ends`), and that "Continue <chart>" resumes it after the merge. Do not wait for a merge. Say the `spend` line too.

## Never

- Do not start a ticket `run` does not list, or build in this clone while subagents build.
- Do not merge a pull request, or answer for the person unless told to go with your recommendations.
