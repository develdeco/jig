---
name: build
description: Builds a ticket's slices in the repo clone you work in - claim a slice, pull its context, commit with the slice trailer so jig verifies each commit, ask the person when stuck, release when done. Use when asked to build, implement or continue a ticket or slice.
---

# build

Run `jig next <T>`. If the phase it names is not build, tell the person what the ticket needs and the prompt that does it, and stop. Do not run an earlier phase on your own.

You build in the person's own clone with your own tools. jig does not start you or wait on you: it keeps each slice's state in the store, verifies every commit, and carries questions and answers between you and the person.

## Start

```
jig here                   # which ticket and slices this branch is on
jig next <ticket>          # what is ready, and the branch to work on
jig claim <ticket> --slice <slice>
jig context <ticket> --slice <slice>
```

`jig claim` makes the slice building and records you as its holder; set `JIG_SESSION` or pass `--session`, so `jig here` can tell which slice is yours. A condition the claim does not meet (a blocker not published or merged, a stacked parent with no clean gate round, a brief nobody approved) prints as a warning, and the claim goes through. **Stop on any claim warning**: tell the person what it says and give the prompt that clears it (`ask:` in the warning, such as `Plan <T> (approve its brief)`), and release the slice unless they say to go on. Pass `--unattended` when nobody is watching: the claim is then refused with `CLAIM_BLOCKED_MERGED`, `CLAIM_STACKED_PARENT_NOT_CLEAN` or `CLAIM_NOT_APPROVED`. A slice another session holds needs `--take`.

## Read the context

`jig context` is what jig holds for the slice in one call (`--json` for one object): its goal and oracle, the brief sections and Seams, what the verified slices built, the failed attempts, its questions with answers, and notes. Read it first, and again after an answer. Find the code with your own tools, starting from the files the verified slices changed.

## Build

- Work on this slice's goal only. Test at the seams the brief names, with the narrowest tests that cover your change: jig runs the oracle on each commit.
- Commit as you land, each commit with the trailer that ties it to the slice:

```
git commit -m "<message>" -m "Jig-Slice: <slice>"
```

  The trailer is how jig finds the commit to verify, and how the next slice's context finds what this one built. `jig here` shows what is verified; `jig verify` runs the oracle on a commit by hand.
- Never edit the store's files by hand. A slice needing more work is `jig slice add <T> --id --goal --oracle`.

## Stuck? Ask

```
jig ask --slice <slice> "<question>"          # returns at once; the slice waits on the answer
jig ask --slice <slice> --wait "<question>"   # waits (--timeout, 10m by default) and prints the answer
jig ask --slice <slice> --wait                # waits again on the question the slice has open
```

Ask one question at a time, with your default. The answer says who it speaks for: `provenance: person` was typed by a person, `agent` is an agent relaying what a person said, so treat it as less sure. A `--wait` that runs out exits 2 and leaves the question open. Without it, carry on with what it does not block; the answer reaches you at your next prompt.

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

A slice is green when jig has verified a commit of it; you do not mark it. To give a slice up before that, so another session can take it:

```
jig claim <ticket> --slice <slice> --release   # queued again, unless it is green
```

## Handing a slice to another agent

To have a subagent or another session build a slice, read `jig skill build handoff` and give it that prompt.

With `--unattended`, jig holds the session back from stopping while a verification runs, tracked changes are uncommitted, every slice is green and no round is open (open the gate and review it), or an approved ticket is not yet published.

## Then

When every slice is green, relay `jig next <T>`'s `prompt:` to the person and stop, unless the person's prompt named later phases (a start-to-finish run): then go on to the next phase's skill (review, then publish).

## Never

- Do not claim a slice and leave it: release it, or say why in a note.
- Do not answer your own question with `jig answer`; that is the person's.
- Do not build a slice whose context you have not read.
