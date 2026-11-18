---
name: build
description: Builds a ticket's slices in the repo clone you work in - claim a slice, pull its context, commit with the slice trailer, run jig verify to prove it, ask the person when stuck, release when done. Use when asked to build, implement or continue a ticket or slice.
---

# build

Run `jig next <T>`. If the phase it names is not build, tell the person what the ticket needs and the prompt that does it, and stop. Do not run an earlier phase on your own.

You build in the person's own clone with your own tools. jig does not start you or wait on you: it keeps each slice's state in the store, records the runs of your tests, and carries questions and answers between you and the person.

## Start

```
jig here                   # which ticket and slices this branch is on
jig next <ticket>          # what is ready, and the branch to work on
jig claim <ticket> --slice <slice>
jig context <ticket> --slice <slice>
```

`jig claim` makes the slice building and records you as its holder; set `JIG_SESSION` or pass `--session`, so `jig here` can tell which slice is yours. **Stop on any claim warning.**

Read `jig skill build claim` before claiming when nobody is watching, when the claim prints a warning or is refused, when another session holds the slice, or to give a slice up.

## Read the context

`jig context` is what jig holds for the slice in one call (`--json` for one object): its goal and oracle, the brief sections and Seams, what the verified slices built, the failed attempts, its questions with answers, and notes. Read it first, and again after an answer. Find the code with your own tools, starting from the files the verified slices changed.

## Build

- Work on this slice's goal only. Test at the seams the brief names, with the narrowest tests that cover your change.
- Read the function, not the file: find code with `grep -n` or the LSP, read that range; for tests, the one you change and one neighbour as the pattern. Make independent reads in one step; an edit's result shows it applied.
- A fix slice's goal names the findings it fixes. Fix the rule each finding names wherever its oracle reaches. Before committing, mutate each term the fix adds (operand, condition, constant, return), run its oracle, and add a test for each survivor.
- Commit as you land, each commit with the trailer that ties it to the slice:

```
git commit -m "<message>" -m "Jig-Slice: <slice>"
```

  The trailer names the slice for `jig verify`, and is how the next slice's context finds what this one built. `jig here` shows what is verified.
- When the slice's work is committed, run `jig verify` in your own worktree, in the foreground or as the harness's background task. jig starts nothing on a commit: `jig verify` runs the slice's oracle where you are, in your environment, shows its output, and records the run. Read that output and fix until the footer says `covers: yes`. A slice is green only on that covering pass at your last commit, so commit first and leave the tree alone while it runs. `--dry-run` shows what would run, and `--no-record` is for exploring: it writes nothing. After the last clean gate round, `jig verify T-1 --final` runs the union of the ticket's slice commands at the reviewed head: the ticket's last run.
- Never edit the store's files by hand. A slice needing more work is `jig slice add <T> --id --goal --oracle`.

## Stuck? Ask

```
jig ask --slice <slice> "<question>"          # returns at once; the slice waits on the answer
```

Ask one question at a time, with your default.

Read `jig skill build ask` after asking, to wait for the answer, or when an answer arrives.

## Leave a note

```
jig note --slice <slice> "<text>"
```

A note is a sentence or two for whoever builds next: what you left. Without `--slice` it is on the whole ticket.

## Record a judgment call

```
jig decide --slice <slice> "<what was ambiguous>" --chose "<what you picked, and why>"
```

An ambiguity you settle yourself goes here, not in a note, an ask or a repo file. It parks nothing, and shows in the ledger and `jig recall` as a call.

## Finish or let go

A slice is green when a `jig verify` run covers a commit of it; you do not mark it. To give a slice up before that, read `jig skill build claim`.

## Handing a slice to another agent

To have a subagent or another session build a slice, read `jig skill build handoff` and give it that prompt.

With `--unattended`, jig holds the session back from stopping while a commit has no recorded result, tracked changes are uncommitted, every slice is green and no round is open (open the gate and review it), or an approved ticket is not yet published.

## Then

When every slice is green, relay `jig next <T>`'s `prompt:` to the person and stop, unless the person's prompt named later phases (a start-to-finish run): then go on to the next phase's skill (review, then publish).

## Never

- Do not claim a slice and leave it: release it, or say why in a note.
- Do not answer your own question with `jig answer`; that is the person's.
- Do not build a slice whose context you have not read.
