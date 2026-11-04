---
name: chart
description: Runs a chart's tickets from the main session - every brief first, then one build subagent per ticket in its own worktree, with questions and approvals relayed. Use when the person says "Take <chart> to PR" or "Continue <chart>", or says yes to taking a chart's tickets on.
---

# chart

Run `jig next <chart>`. If the chart is not `ready`, tell the person its `reason` and `prompt`, and stop. Entries with no ticket yet need `jig graduate <chart>` first. The standing is derived: read it again.

A yes to intake's "take the chart's tickets on in this session?" is this run's prompt; "only the first" is not a chart run: run that ticket alone.

You are the main session. Every question to the person is yours; a subagent cannot ask one. jig starts no session, records no run.

## Briefs

`briefs` lists the tickets to brief. Brief them all before any build: a subagent per ticket (sonnet, background) drafts its brief with intake (its ticket exists: never mint or approve), marks each recommended answer it takes, checks it with `jig validate <T> --check` and hands back its questions. Meanwhile ask one numbered round (with recommendations), record answers, send a changed one to its drafter, confirm once, then `jig validate <T>` for each. The person's moments stay theirs: ask and wait, unless told to go with your recommendations (ADR 0035). Nothing to brief: go on.

## Builds

Before the first wave, ask once to allow what the builds run: `jig`, `git` and the oracles in the tickets' `slices.yaml`. One yes covers the run. jig sets no permission.

Before each wave, stack every pair in `workspace_overlaps`: the later ticket (Plan order) gets a `stacked` `blocked_by` on the earlier in its `ticket.yaml`; check it with `jig validate <T> --check`, read the chart again, say so. Two in parallel only if the person says so (merged by hand).

Go through `run` wave by wave; no edge lies inside a wave. Start a subagent per ticket of the wave (Agent tool, `subagent_type: general-purpose`, `model: sonnet` unless the person named another, in the background), each in its own worktree: run the `add` commands of the ticket's `worktrees` (`jig next <T>`, or the chart's), one at a time, and read `jig next <T>` again when a stacked ticket's wave starts. `present`: use its `dir`. `no_clone`: tell the person, do not start that ticket. Its prompt:

```
You are building ticket <T>. Work only in the worktree of each repo (`dir` of its rows), on branch <branch>; run every command there.
Build it with the build skill (`jig skill build`): claim each slice with `--unattended`, commit with its `Jig-Slice` trailer.
Slices ready in several repos (`jig next <T>`): build one repo yourself; start a nested builder per other repo in the background with this prompt, told to build only its repo's slices, never gate or nest (a hold saying to gate is yours: it hands back), and wait for them before the gate. Relay a builder's question: end your turn with the question, its id and default; resume it when resumed. No Agent tool: build repos in turn, hand back at green; I start the reviewer.
Stuck: `jig ask --slice <slice> "<question>"` with your default, then end your turn with the question, its id and the default. I answer and resume you.
When every slice is green, open the gate (`jig gate <T>`) and have it reviewed by your own reviewer subagent if you have the Agent tool (`jig skill review`). A round that only queues fixes: build them and have them reviewed again. End your turn when a round is clean or leaves an ask, with its verdict, or as `jig gate` says.
Never publish. Never answer your own question.
```

Wait for the hand-backs (TaskOutput `block: true`, else the notice; none if the call returned one). jig holds a subagent at its stop while a verification runs, tracked changes are uncommitted, or every slice is green and no round is open: not stuck, wait.

A `run` ticket in publish is published and behind (`state: behind`): no build subagent. For each of its `stack` rows with a `merge`, run that merge yourself in a checkout of the ticket's branch (its worktree; its `worktrees` give the add when it is gone). A clean merge needs no review; one resolved by hand needs `jig gate <T>` and a clean round first. Then publish it (Publish).

## Relay

A subagent hands back a question: put it to the person with its default, record the answer with `jig answer <T> <question> "<answer>"`, then resume the subagent with SendMessage (a nested builder's: via its ticket subagent). A hand-back at green with no reviewer: run `jig gate <T>` and start the reviewer yourself (`jig skill review`). A review's asks are the person's too: put each to them, record `jig triage <T> <finding> keep|dismiss`, and resume the subagent to build the fixes and have them reviewed again.

## Publish

Tickets whose `jig next <T>` names publish are reviewed clean. Ask once for the group (each reviewed head, its body's first lines), then a subagent per ticket publishes it (`jig skill publish`: demo, body, CI budgets), removing no worktree, and hands back the url; a stacked ticket's publisher starts after its parent's hands back. A go-with-your-recommendations prompt needs no yes. Remove a published ticket's worktrees with the `remove` commands of `jig publish --pr` or `jig next <T>`, one at a time. Git refuses a worktree with changes: commit or discard them, never force.

## Ends

After each wave read `jig next <chart>` again and start the next. A ticket behind a `merged` edge, or nothing reachable, ends the run: say what is published, what waits on which pull request (`ends`), and that "Continue <chart>" resumes it after the merge. Do not wait for a merge. Say the `spend` line of `jig next <chart>` too.

## Never

- Do not start a ticket `run` does not list, or build in this clone while subagents build.
- Do not merge a pull request, or answer for the person unless told to go with your recommendations.
- Do not remove a worktree before its ticket is published.
