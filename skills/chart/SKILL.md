---
name: chart
description: Runs a chart's tickets from the main session - every brief first, then one build subagent per ticket in its own worktree, with questions and approvals relayed. Use when the person says "Take <chart> to PR" or "Continue <chart>", or says yes to taking a chart's tickets on.
---

# chart

Run `jig next <chart>`. If the chart is not `ready`, tell the person its `reason` and the `prompt` that does it, and stop. Entries with no ticket yet need `jig graduate <chart>` first. The standing is derived from the store and the pull requests: read it again, never remember it.

A yes to intake's question "take the chart's tickets on in this session?" is this run's prompt; "only the first" is not a chart run, so run that ticket alone with its skills.

You are the main session. Every question to the person is yours to ask; a subagent cannot ask one. jig starts no session and records no run: the run is the prompt.

## Briefs

`briefs` lists the tickets to brief. Brief them all before any build, here, with the intake skill: one grilling round across every ticket (numbered, each with a recommended answer), one confirmation, then `jig validate <T>` for each. The person's moments stay theirs: ask and wait, unless the prompt told you to go with your recommendations (ADR 0035). Nothing to brief: go on.

## Builds

Go through `run` wave by wave; no edge lies inside a wave. Start one subagent for each ticket of the wave (the Agent tool, `subagent_type: general-purpose`, `model: sonnet` unless the person named another, in the background), each in its own worktree on the ticket's branch: `jig next <T>` names the branch and, for a stacked ticket, its start point (the parent's branch); cut it from there or the repo's target (`git worktree add <dir> -b <branch> <base>`), one in each repo a ticket builds in. Its prompt:

```
You are building ticket <T>. Work only in <dir>, on branch <branch>; run every command there.
Build it with the build skill (`jig skill build`): claim each slice with `--unattended`, commit with its `Jig-Slice` trailer.
Stuck: `jig ask --slice <slice> "<question>"`, saying what you would do by default, then end your turn with the question, its id and the default. I answer and resume you.
When every slice is green, open the gate (`jig gate <T>`) and have it reviewed by a reviewer subagent of your own (`jig skill review`). A round that only queues fixes: build them and have them reviewed again. End your turn when a round is clean or leaves an ask, with its verdict.
Never publish. Never answer your own question.
```

Wait for the hand-backs (TaskOutput, `block: true`). jig holds a subagent at its stop while a verification runs, tracked changes are uncommitted, or every slice is green and no round is open: it is not stuck, wait on.

## Relay

A subagent hands back a question: put it to the person with its default, record the answer with `jig answer <T> <question> "<answer>"` (off a terminal it is recorded as relayed), then resume the subagent with SendMessage. A review's asks are the person's too: put each to them, record `jig triage <T> <finding> keep|dismiss`, and resume the subagent to build the fixes and have them reviewed again.

## Publish

Tickets whose `jig next <T>` names publish are reviewed clean. Ask the person once for the group (each reviewed head and the first lines of its body), then publish each here, the hand-off way (`jig skill publish`). A prompt that told you to go with your recommendations needs no yes.

## Ends

After each wave read `jig next <chart>` again and start the next. A ticket behind a `merged` edge, or nothing reachable, ends the run: say what is published, what waits on which pull request (`ends`), and that "Continue <chart>" resumes it once that merged. Do not wait for a merge.

## Never

- Do not start a ticket `run` does not list, or build in this clone while subagents build.
- Do not merge a pull request, or answer for the person unless the prompt told you to go with your recommendations.
