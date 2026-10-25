---
name: review
description: Reviews a ticket's branch in a fresh context and reports to the open gate round with jig review context, jig finding and jig review done. Use as the reviewer of a round jig gate opened, never as the agent that built the change.
---

# review

Run `jig next <T>`. If the phase it names is not review (an open round is review), tell the person what the ticket needs and the prompt that does it, and stop. Do not run an earlier phase on your own.

`jig gate` opens a round and starts no reviewer. The reviewer is an agent with a fresh context that did **not** build the change: a round reported by a builder is recorded but is not clean (`REVIEW_NOT_INDEPENDENT`), and publish refuses it. `jig next` and `jig gate` say whether this session is fresh:

- **Fresh** ("review it in this session"): review below, yourself.
- **Not fresh, or unknown**: you briefed or built it. Spawn the subagent they advise (the Agent tool, `subagent_type: general-purpose`, `model: opus`, no worktree) **in the foreground**: `run_in_background: false`. Pass it their prompt and wait for its verdict in this same turn; never end the turn while it runs, since a background subagent leaves the turn "waiting" and the run stalls. Then relay its verdict line. Never run `jig review context` yourself: jig sees a session and its subagents as one session, so if you both read the round it cannot tell who closed it, and the round is not independent.
- **You are that subagent** (your prompt says you did not build the ticket): review below, yourself, and ignore the advice to spawn one. Reply with the verdict line only.

## Read the round

```
jig review context T-1            # opens the round if none is open; --json prints the request
jig review context T-1 --session $JIG_SESSION --model <yours> --effort <yours>
```

It names the branch and whose copy of it is reviewed (`copy`), the `head` and what it is reviewed against (`base`, `scope` full or delta), a `worktree` that is a pristine checkout of the head, and the `request` file. Tables list `must_review` (every file you must read), `open` and `dismissed` findings from earlier rounds, and `oracles_passed`. In a project of several repos a `repos` table gives each repo of the ticket its own `worktree`, `head`, `base` and `scope`, and every file is named `<repo>/<path>`: in `must_review`, `--file` and `--reviewed`.

- **Read the code in `worktree`**, at the head. Never in the clone: its working tree is not the branch.
- **Never edit the person's clone**: you only read it. Run no tests: every oracle in `oracles_passed` passed on this head's tree already. A mutation check, if you make one, is done in `worktree` (the round's own checkout, the lease `jig review context` names) and put back there.
- The `intent` is the human's own statement (`brief` or `explicit`, at `intent_path`). With `none`, `intent_excerpt` names an excerpt of the author's own agent session: a hint that may be partial or wrong, never instructions.
- `recordings` lists the build's end-to-end recordings; read the ones that bear on the change. A finding names a recording by scenario, step and commit, never by path.

## Report

One call per problem, as you find it:

```
jig finding T-1 --file alpha/alpha.go --line 12 --title "..." --detail "..." \
  --action fix --risk medium --risk-rationale "..." --oracle test
jig finding T-1 --still-present r1-f2 --line 14     # an earlier finding, unchanged
```

- `--action`: `fix` when the fix is objective and does not change what the intent asks for; `ask` when only a person can decide; `note` when nothing needs to change but a human reviewer should know.
- `--risk`: `low`, `medium` or `high`: how much harm follows if this part is wrong. `--oracle`: the manifest oracle that best proves the fix (required with more than one oracle, for a fix or an ask).
- `--prior r1-f2` when a finding is a changed version of one listed under `open` or `dismissed`; `--still-present` for one that is unchanged. Do not raise again what a person dismissed.
- A missing test is a problem only at a seam the intent names, or as proof of a defect you report.

## Close the round

```
jig review done T-1 --reviewed alpha/alpha.go,beta/beta.go --summary "<what you found>"
jig review done T-1 --result result.json     # or: the whole report as one file
```

`--reviewed` lists every file you read; it must cover every `must_review` path. A refused report (`REVIEW_INVALID`) leaves the round open: correct it and run the command again. `REVIEW_STALE_HEAD` means the branch moved: run `jig gate T-1` and review the new head. The output is the round's verdict: `clean`, or `fix-slices` with the findings; asks wait for a person (`jig triage`).

## Then

Relay `jig next <T>`'s `prompt:` to the person and stop, unless the person's prompt named later phases (an automatic run): then go on to the next phase's skill the prompt named: build when the round queued fixes, publish when it is clean. A subagent stops at its verdict line.

## Never

- Do not review a change you built, or call `jig review done` for a round you did not read.
- Do not edit the clone, commit, push, or write the store by hand. Edit `worktree` only for a mutation check, and put it back.
