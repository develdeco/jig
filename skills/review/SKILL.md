---
name: review
description: Reviews a ticket's branch in a fresh context and reports to the open gate round with jig review context, jig finding and jig review done. Use as the reviewer of a round jig gate opened, never as the agent that built the change.
---

# review

`jig gate` opens a round and starts no reviewer. You are the reviewer: an agent with a fresh context (a subagent or another session) that did **not** build the change. If you built it, stop and have another agent run this skill: a round reported by a builder is recorded but is not clean (`REVIEW_NOT_INDEPENDENT`), and publish refuses it.

## Read the round

```
jig review context T-1            # opens the round if none is open; --json prints the request
jig review context T-1 --session $JIG_SESSION --model <yours> --effort <yours>
```

It names the branch and whose copy of it is reviewed (`copy`), the `head` and what it is reviewed against (`base`, `scope` full or delta), a `worktree` that is a pristine checkout of the head, and the `request` file. Tables list `must_review` (every file you must read), `open` and `dismissed` findings from earlier rounds, and `oracles_passed`.

- **Read the code in `worktree`**, at the head. Never in the clone: its working tree is not the branch.
- **Edit nothing** and run no tests: every oracle in `oracles_passed` passed on this head's tree already.
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

## Never

- Do not review a change you built, or call `jig review done` for a round you did not read.
- Do not edit the clone or the worktree, commit, push, or write the store by hand.
