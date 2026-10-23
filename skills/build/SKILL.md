---
name: build
description: Builds a ticket's slices in the repo clone you work in - claim a slice, pull its context, commit with the slice trailer so jig verifies each commit, ask the person when stuck, release when done. Use when asked to build, implement or continue a ticket or slice.
---

# build

You build in the person's own clone with your own tools. jig does not start you or wait on you: it keeps each slice's state in the store, verifies every commit, and carries questions and answers between you and the person.

## Start

```
jig here                   # which ticket and slices this branch is on
jig next <ticket>          # what is ready, and the branch to work on
jig claim <ticket> --slice <slice>
jig context <ticket> --slice <slice>
```

`jig claim` makes the slice building and records you as its holder; set `JIG_SESSION` or pass `--session`, so `jig here` can tell which slice is yours. A condition the claim does not meet (a blocker not published or merged, a stacked parent with no clean gate round, a brief nobody approved) prints as a warning and the claim goes through, because a person is watching. Pass `--unattended` when nobody is: the claim is then refused with `CLAIM_BLOCKED_MERGED`, `CLAIM_STACKED_PARENT_NOT_CLEAN` or `CLAIM_NOT_APPROVED`. A slice another session holds needs `--take`.

## Read the context

`jig context` is everything the slice needs, in one call: its goal and oracle command, the brief sections it builds from and the Seams, the commits and files of the slices already verified, the failed attempts so far with why they failed, the code a graph links to the goal, its questions with their answers, and notes. Read it before you search the repo. `--json` gives the same as one object. Run it again after an answer, or when something looks out of date.

## Build

- Work on this slice's goal only. Test at the seams the brief names, and run the narrowest tests that cover your change: jig runs the oracle on each commit, and `oracle_seconds` says what that costs.
- Commit as you land, each commit with the trailer that ties it to the slice:

```
git commit -m "<message>" -m "Jig-Slice: <slice>"
```

  The trailer is how jig finds the commit to verify, and how the next slice's context finds what this one built. `jig here` shows what is verified and what is still being checked; `jig verify` runs the oracle on a commit by hand.
- Never edit the store's files by hand; the verbs below are the way in.

## Stuck? Ask

```
jig ask --slice <slice> "<question>"          # returns at once; the slice waits on the answer
jig ask --slice <slice> --wait "<question>"   # waits (--timeout, 10m by default) and prints the answer
jig ask --slice <slice> --wait                # waits again on the question the slice has open
```

Ask one question at a time, and say what you would do by default. The answer says who it speaks for: `provenance: person` was typed by a person, `agent` is an agent relaying what a person said, so treat it as less sure. A `--wait` that runs out exits 2 and leaves the question open. Without `--wait`, carry on with what the question does not block; the answer reaches you at your next prompt, and `jig context` always has it.

## Leave a note

```
jig note --slice <slice> "<text>"
```

A note is a sentence or two for whoever builds this slice next, or the person: what you chose and why, what you left. Without `--slice` it is on the whole ticket.

## Finish or let go

A slice is green when jig has verified a commit of it; you do not mark it. To give a slice up before that, so another session can take it:

```
jig claim <ticket> --slice <slice> --release   # queued again, unless it is green
```

## Never

- Do not claim a slice and leave it: release it, or say why in a note.
- Do not answer your own question with `jig answer`; that is the person's.
- Do not build a slice whose context you have not read.
