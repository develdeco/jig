---
name: chart
description: Runs a chart's tickets from the main session - every brief first, then a build subagent per ticket in its own worktree. Use when the person says "Take <chart> to PR" or "Continue <chart>", or says yes to a chart run.
---

# chart

Run `jig next <chart>` (several tickets: `--tickets A,B`; their chart is in `jig next`'s `charts`). If the chart is not `ready`, relay its `reason` and `prompt` and stop. Entries with no ticket: `jig graduate <chart>` first. The standing is derived: re-read it.

Three prompts make a chart run: one aimed at a chart or some of its tickets ("Take A and B to PR", "Continue A and B"), a yes to intake's confirm question, intake's explicit launch. One ticket alone: run it alone.

You are the main session. Every question to the person is yours; a subagent cannot ask. jig starts no session, records no run.

## Briefs

`briefs` lists the tickets to brief; brief them all before any build: a subagent per ticket (sonnet, background) drafts its brief with intake (its ticket exists: never mint or approve), marks each recommended answer it takes, checks it with `jig validate <T> --check`, hands back its questions. Meanwhile ask a numbered round with recommendations, record answers, send a changed one to its drafter, confirm once, then `jig validate <T>` each. Ask and wait, unless told to go with your recommendations (ADR 0035).

## Builds

Before the first wave, ask once to allow what the builds run (`jig`, `git`, the tickets' oracles); one yes covers it. A run going with the recommendations, intake's explicit launch included, asks nothing: permission settings decide, a refused command hands its ticket back and the report names it.

Before each wave read `jig skill chart stacked`: it says how overlaps and a published ticket that is behind are handled.

Go through `run` wave by wave (no edge lies inside one). Start a subagent per wave ticket (`subagent_type: general-purpose`, `model: sonnet` unless the person named another, background), each in its own worktree: run the `add` commands of the ticket's `worktrees` (`jig next <T>`, or the chart's), one at a time. `present`: use its `dir`. `no_clone`: tell the person; do not start it. Its prompt:

```
You are building ticket <T>. Work only in the worktree of each repo (`dir` of its rows), on branch <branch>; run every command there.
Build it with `jig skill build`: claim each slice with `--unattended`, commit with its `Jig-Slice` trailer.
Stuck: `jig ask --slice <slice> "<question>"` with your default, then end your turn with the question, id and default. I answer and resume you.
When every slice is green, open the gate (`jig gate <T>`) and have your own reviewer subagent review it if you have the Agent tool (`jig skill review`). A round that only queues fixes: build them, have them reviewed again. End your turn when a round is clean or leaves an ask, with its verdict, or as `jig gate` says.
Never publish. Never answer your own question.
```

Read `jig skill chart multi-repo` when `jig next <T>` shows slices ready in several repos.

Wait for the hand-backs (TaskOutput `block: true`, else the notice; none if the call returned one). jig holds a subagent at its stop while a verification runs, changes are uncommitted, or every slice is green with no round open: not stuck, wait.

## Relay

A subagent's question: put it to the person with its default, record it with `jig answer <T> <question> "<answer>"`, then resume the subagent with SendMessage (a nested builder's: via its ticket subagent). A hand-back at green with no reviewer: run `jig gate <T>`, start the reviewer yourself (`jig skill review`). A review's asks are the person's: put each, record `jig triage <T> <finding> keep|dismiss`, and resume the subagent to build the fixes and have them reviewed again.

## Publish

Tickets whose `jig next <T>` names publish are reviewed clean. Ask once for the group (each reviewed head, body's first lines), then a subagent per ticket publishes it (`jig skill publish`: demo, body, CI budgets), removing no worktree, and hands back the url; a stacked ticket's publisher starts after its parent's hands back. A go-with-your-recommendations prompt needs no yes. Remove a ticket's worktrees only once published, one at a time, with the `remove` commands of `jig publish --pr` or `jig next <T>`; git refuses one with changes: commit or discard, never force.

## Ends

After each wave re-read `jig next <chart>` (same `--tickets`) and start the next. A ticket behind a `merged` edge, or nothing reachable, ends the run: say what is published, what waits on which pull request or unnamed ticket (`ends`), and that `prompt` resumes the same tickets. Do not wait for a merge. Say the `spend` line.

## Never

- Do not start a ticket `run` does not list, or build in this clone while subagents build.
- Do not merge a pull request, or answer for the person unless told to go with your recommendations.
