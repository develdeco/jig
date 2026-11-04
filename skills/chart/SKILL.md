---
name: chart
description: Runs a chart's tickets from the main session - every brief first, then one build subagent per ticket in its own worktree, with questions and approvals relayed. Use when the person says "Take <chart> to PR" or "Continue <chart>", or says yes to taking a chart's tickets on.
---

# chart

Run `jig next <chart>`. If the chart is not `ready`, tell the person its `reason` and the `prompt` that does it, and stop. Entries with no ticket yet need `jig graduate <chart>` first. The standing is derived: read it again, never remember it.

A yes to intake's question "take the chart's tickets on in this session?" is this run's prompt; "only the first" is not a chart run, so run that ticket alone.

You are the main session. Every question to the person is yours to ask; a subagent cannot ask one. jig starts no session and records no run.

## Briefs

`briefs` lists the tickets to brief. Brief them all before any build, here, with intake: one grilling round for all (numbered, each with a recommended answer), one confirmation, then `jig validate <T>` for each. The person's moments stay theirs: ask and wait, unless the prompt told you to go with your recommendations (ADR 0035). Nothing to brief: go on.

## Builds

Before the first wave, ask the person once to allow what the builds run: `jig` and `git` (the worktree commands too) and the oracles named in the tickets' `slices.yaml`. One yes covers the run. jig sets no permission.

Go through `run` wave by wave; no edge lies inside a wave. Start one subagent per ticket of the wave (Agent tool, `subagent_type: general-purpose`, `model: sonnet` unless the person named another, in the background), each in its own worktree: run the `add` commands in the ticket's `worktrees` from `jig next <T>` (the chart's `worktrees` carry them too), one at a time (git's worktree records are not safe against parallel adds), and read `jig next <T>` again when a stacked ticket's wave starts. `present` means it exists: use its `dir`. `no_clone` means the repo has no clone here: tell the person, do not start that ticket. jig makes no worktree. Its prompt:

```
You are building ticket <T>. Work only in the worktree of each repo (`dir` of its rows), on branch <branch>; run every command there.
Build it with the build skill (`jig skill build`): claim each slice with `--unattended`, commit with its `Jig-Slice` trailer.
Slices ready in several repos (`jig next <T>`): build one repo yourself; start a nested builder per other repo with this prompt, told to build only its repo's slices, never gate or nest. Relay its question: end your turn with its id and default; resume it when resumed. No Agent tool: build repos in turn, hand back at green; I start the reviewer.
Stuck: `jig ask --slice <slice> "<question>"`, saying what you would do by default, then end your turn with the question, its id and the default. I answer and resume you.
When every slice is green, open the gate (`jig gate <T>`) and have it reviewed by a reviewer subagent of your own if you have the Agent tool (`jig skill review`). A round that only queues fixes: build them and have them reviewed again. End your turn when a round is clean or leaves an ask, with its verdict.
Never publish. Never answer your own question.
```

Wait for the hand-backs (TaskOutput, `block: true`). jig holds a subagent at its stop while a verification runs, tracked changes are uncommitted, or every slice is green and no round is open: it is not stuck, wait.

A ticket in `run` whose phase is publish is published and behind (`state: behind`): it needs no build subagent. For each of its `stack` rows with a `merge`, run that merge yourself in a checkout of the ticket's branch (its worktree; its `worktrees` give the add when it is gone). A clean merge needs no review; one resolved by hand needs `jig gate <T>` and a clean round first. Then publish it (Publish).

## Relay

A subagent hands back a question: put it to the person with its default, record the answer with `jig answer <T> <question> "<answer>"` (off a terminal it is recorded as relayed), then resume the subagent with SendMessage (a nested builder's: via its ticket subagent). A hand-back at green, no round open: run `jig gate <T>` and start the reviewer yourself (`jig skill review`). A review's asks are the person's too: put each to them, record `jig triage <T> <finding> keep|dismiss`, and resume the subagent to build the fixes and have them reviewed again.

## Publish

Tickets whose `jig next <T>` names publish are reviewed clean. Ask the person once for the group (each reviewed head and the first lines of its body), then publish each here, the hand-off way (`jig skill publish`). A prompt that told you to go with your recommendations needs no yes. Once a ticket is published, remove its worktrees with the `remove` commands `jig publish --pr` and `jig next <T>` give, one at a time. Git refuses a worktree with changes: commit or discard them, never force.

## Ends

After each wave read `jig next <chart>` again and start the next. A ticket behind a `merged` edge, or nothing reachable, ends the run: say what is published, what waits on which pull request (`ends`), and that "Continue <chart>" resumes it once that merged. Do not wait for a merge. Say the `spend` line of `jig next <chart>` with the ends: the run's cost so far.

## Never

- Do not start a ticket `run` does not list, or build in this clone while subagents build.
- Do not merge a pull request, or answer for the person unless the prompt told you to go with your recommendations.
- Do not remove a worktree before its ticket is published.
