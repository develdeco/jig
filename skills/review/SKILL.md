---
name: review
description: Reviews a ticket's branch in a fresh context and reports to the open gate round with jig review context, jig finding and jig review done. Use as the reviewer of a round jig gate opened, never as the agent that built the change.
---

# review

Run `jig next <T>`. If the phase it names is not review (an open round is review), tell the person what the ticket needs and the prompt that does it, and stop. Do not run an earlier phase on your own.

`jig gate` opens a round and starts no reviewer. The reviewer is an agent with a fresh context that did **not** build the change: the round records whether it did (`independent`), and the pull request's review notes say so. `jig next` and `jig gate` say whether this session is fresh:

- **Fresh** ("review it in this session"): review below, yourself.
- **Not fresh, or unknown**: you briefed or built it. Read `jig skill review spawn` and spawn the reviewer it describes.
- **You are that subagent** (your prompt says you did not build the ticket): review below, yourself, and ignore the advice to spawn one. Reply with the verdict line only.

## Read the round

```
jig review context T-1            # opens the round if none is open; --json prints the request
jig review context T-1 --session $JIG_SESSION --model <yours> --effort <yours>
```

It names the branch, whose copy (`copy`), the `head`, its `base` and `scope` (full or delta), the `worktree` advice for making your own, and the `request` file. Tables list `must_review` (every file you must read), `open` and `dismissed` findings, `probed` probes, `unprobed` hunks and `oracles_passed`.

Read `jig skill review rounds` when the report is refused or stale, the round has a `repos` table, you filed a note below the floor, or it lists open, answered or dismissed findings (`--prior`).

- **Make your worktree at the head** as the `worktree` advice says (`jig skill review worktree` for the variants and the removal), and read the code there as the context's `read` line says, in either scope. Never in the clone.
- **Never edit the person's clone**: you only read it. Run no oracle and no suite: each in `oracles_passed` was run by its builder; `at_head` says if on this head. A test runs only to probe.
- Read `jig skill review evidence` when `recordings` lists any, or the `intent` is `none`.
- The `intent` is the human's own statement (`brief` or `explicit`, at `intent_path`).

## Probe

Every round, probe each term (operand, operator, condition, constant, call, return) of each function the change adds or alters on a critical path the intent names (none named: that carries what it asks for), unless `probed` lists it `current` and `caught`:

- Mutate it in your worktree, run the narrowest test that covers it (by `seconds`), put the file back: one script per function, one mutate-run-restore loop with a command per mutant, not a call per term. Failing or timing out is `caught`, passing `survived`.
- `jig probe T-1 --seam S --file F --line N --mutation M --command C --outcome caught|survived`
- `unprobed` is the work list: set aside a hunk with no critical-path code, `jig probe T-1 --skip F[:A-B] --why W`.
- A survivor at a seam is a finding, elsewhere at most a note; an equivalent mutant none.

## Report

One call per problem, as you find it:

```
jig finding T-1 --file alpha/alpha.go --line 12 --title "..." --detail "..." \
  --action fix --risk medium --risk-rationale "..." --oracle "go test ./alpha -run 'TestAlpha'"
```

- `--action`: `fix` when the fix is objective and keeps what the intent asks; `ask` when only a person can decide; `note` when nothing needs to change but a person should know.
- `--risk`: `low`, `medium` or `high`: how much harm follows if this part is wrong. `--oracle`: the narrowest command that proves the fix everywhere its rule applies (the test the fix adds or extends, by name, or a search that must come back empty, such as `! git grep -n '<pattern>' -- <paths>`), required for a fix.
- A finding names the rule broken. Before filing, search the change for the same rule's other instances: the `--detail` lists them, and `--oracle` proves the rule across them. An `--action ask` about a rule lays out every case it must decide, with one recommended answer.
- Never raise again what a person dismissed.
- A missing test is a problem only at a seam the intent names, or as proof of a defect you report.

## Close the round

```
jig review done T-1 --reviewed alpha/alpha.go,beta/beta.go --summary "<what you found>"
jig review done T-1 --result result.json     # or: the whole report as one file
```

`--reviewed` lists every file you read, every `must_review` path among them. The output is the round's verdict: `clean`, or `fix-slices` with the findings; asks wait for a person (`jig triage`), and a person may take up a note with it too.

## Then

Relay `jig next <T>`'s `prompt:` to the person and stop, unless the person's prompt named later phases (a start-to-finish run): then go on to the next phase's skill the prompt named: build when the round queued fixes, publish when it is clean. Asks the round left are the person's: put each to them and record their answer with `jig triage`, or, when the prompt told you to go with your recommendations, decide them yourself. A subagent stops at its verdict line.

## Never

- Do not review a change you built, or call `jig review done` for a round you did not read.
- Do not edit the clone, commit, push, or write the store by hand. Edit your worktree only to probe, and put it back.
