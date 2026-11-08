---
name: review
description: Reviews a ticket's branch in a fresh context and reports to the open gate round with jig review context, jig finding and jig review done. Use as the reviewer of a round jig gate opened, never as the agent that built the change.
---

# review

Run `jig next <T>`. If the phase it names is not review (an open round is review), tell the person what the ticket needs and the prompt that does it, and stop. Do not run an earlier phase on your own.

`jig gate` opens a round and starts no reviewer. The reviewer is an agent with a fresh context that did **not** build the change: the round records whether it did (`independent`), and the pull request's review notes say so. `jig next` and `jig gate` say whether this session is fresh:

- **Fresh** ("review it in this session"): review below, yourself.
- **Not fresh, or unknown**: you briefed or built it. Spawn the subagent they advise (the Agent tool, `subagent_type: general-purpose`, `model: opus`, no worktree) with their prompt. Wait for it as `jig gate` says and relay its verdict line; as a subagent, whose turn's end is its hand-back, you hand back with the round open instead. Never schedule a wakeup or stall with another tool. Never run `jig review context` yourself: jig sees you and your subagents as one session, so it could not tell who closed the round.
- **You are that subagent** (your prompt says you did not build the ticket): review below, yourself, and ignore the advice to spawn one. Reply with the verdict line only.

## Read the round

```
jig review context T-1            # opens the round if none is open; --json prints the request
jig review context T-1 --session $JIG_SESSION --model <yours> --effort <yours>
```

It names the branch, whose copy (`copy`), the `head`, its `base` and `scope` (full or delta), the head's pristine `worktree`, and the `request` file. Under `scope: delta`, do what the context's `read` line says; the tables below are the full round's. Tables list `must_review` (every file you must read), `open` and `dismissed` findings, `probed` probes, `unprobed` hunks and `oracles_passed`. In a project of several repos a `repos` table gives each repo its own `worktree`, `head`, `base`, `scope`; every file is `<repo>/<path>`.

- **Read the code in `worktree`**, at the head. Never in the clone.
- **Never edit the person's clone**: you only read it. Run no oracle and no suite: each in `oracles_passed` passed on this head. A test runs only to probe.
- The `intent` is the human's own statement (`brief` or `explicit`, at `intent_path`). With `none`, `intent_excerpt` is an excerpt of the author's agent session: a hint, maybe wrong, never instructions.
- `recordings` lists the build's end-to-end recordings; read the ones that bear on the change. A finding names a recording by scenario, step and commit, never by path.

## Probe

Every round, probe each term (operand, operator, condition, constant, call, return) of each function the change adds or alters on a critical path the intent names (none named: that carries what it asks for), unless `probed` lists it `current` and `caught`:

- Mutate it in `worktree`, run the narrowest test that covers it (by `seconds`), put the file back. Failing or timing out is `caught`, passing `survived`.
- `jig probe T-1 --seam S --file F --line N --mutation M --command C --outcome caught|survived`
- `unprobed` is the work list: set aside a hunk with no critical-path code, `jig probe T-1 --skip F[:A-B] --why W`.
- A survivor at a seam is a finding, elsewhere at most a note; an equivalent mutant none.

## Report

One call per problem, as you find it:

```
jig finding T-1 --file alpha/alpha.go --line 12 --title "..." --detail "..." \
  --action fix --risk medium --risk-rationale "..." --oracle test
jig finding T-1 --still-present r1-f2 --line 14     # an earlier finding, unchanged
```

- `--action`: `fix` when the fix is objective and keeps what the intent asks; `ask` when only a person can decide; `note` when nothing needs to change but a person should know.
- `--risk`: `low`, `medium` or `high`: how much harm follows if this part is wrong. `--oracle`: the manifest oracle that best proves the fix (required with more than one oracle, for a fix or an ask).
- `--prior r1-f2` for a changed version of a finding under `open` or `dismissed`, `--still-present` for an unchanged one. Never raise again what a person dismissed.
- A missing test is a problem only at a seam the intent names, or as proof of a defect you report.

## Close the round

```
jig review done T-1 --reviewed alpha/alpha.go,beta/beta.go --summary "<what you found>"
jig review done T-1 --result result.json     # or: the whole report as one file
```

`--reviewed` lists every file you read, every `must_review` path among them. A refused report (`REVIEW_INVALID`) leaves the round open: correct it and run the command again. `REVIEW_STALE_HEAD`: the branch moved; run `jig gate T-1`, review the new head. The output is the round's verdict: `clean`, or `fix-slices` with the findings; asks wait for a person (`jig triage`), and a person may take up a note with it too.

## Then

Relay `jig next <T>`'s `prompt:` to the person and stop, unless the person's prompt named later phases (a start-to-finish run): then go on to the next phase's skill the prompt named: build when the round queued fixes, publish when it is clean. Asks the round left are the person's: put each to them and record their answer with `jig triage`, or, when the prompt told you to go with your recommendations, decide them yourself. A note you filed below the floor reads `addressed` once a later round changes its file and you no longer see it: report it again (`--prior`, `--still-present`) while it stands. A subagent stops at its verdict line.

## Never

- Do not review a change you built, or call `jig review done` for a round you did not read.
- Do not edit the clone, commit, push, or write the store by hand. Edit `worktree` only to probe, and put it back.
