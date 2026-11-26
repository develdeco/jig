---
name: review
description: Reviews a ticket's branch in a fresh context and reports to the open gate round with jig review context, jig finding and jig review done. Use as the reviewer of a round jig gate opened, never as the agent that built the change.
---

# review

The job is to find what the builder got wrong before it ships, not to certify the change: `clean` is a valid verdict, never the goal.

Run `jig next <T>`. If the phase it names is not review (an open round is review), tell the person what the ticket needs and the prompt that does it, and stop. Do not run an earlier phase on your own.

`jig gate` starts no reviewer. The reviewer did **not** build the change; the round records if it did (`independent`). `jig next` and `jig gate` say whether this session is fresh:

- **Fresh** ("review it in this session"): review below, yourself.
- **Not fresh, or unknown**: you briefed or built it. Read `jig skill review spawn` and spawn the reviewer it describes.
- **You are that subagent** (your prompt says you did not build the ticket): review below, yourself, and ignore the advice to spawn one. Reply with the verdict line only.

## The round

```
jig review context T-1   # opens the round if none is open; --json prints the request
jig review context T-1 --session $JIG_SESSION --model <yours> --effort <yours>
```

It names the branch, its `copy`, `head`, `base`, `scope`, `worktree` advice and `request` file, with tables `must_review` (every file you must read), `open` and `dismissed` findings and `oracles_passed`.

Read `jig skill review rounds` when the report is refused or stale, the round has a `repos` table, you filed a note below the floor, or it lists open, answered or dismissed findings (`--prior`).

- **Make your worktree at the head** as the `worktree` advice says (variants and removal: `jig skill review worktree`), and read the code there, never in the clone, as the context's `read` line says, in either scope.
- Each `oracles_passed` oracle was run by its builder; `at_head` says if on this head.
- Read `jig skill review evidence` when `recordings` lists any, or the `intent` is `none`.
- `intent` is the human's own statement (`brief` or `explicit`, at `intent_path`).

## Read

The round's `scope` (per repo in a `repos` table) says what to look for:

- **full**: read the whole change against the intent, not term by term, for:
  - wrong code, and states of the intent's rules it never handles;
  - what it breaks elsewhere: callers, pinned output in other packages, docs that now say something false;
  - the repo's own rules, as its agent instructions (AGENTS.md) and contributing guide state them;
  - cost that grows with real data.
- **delta**: check only that each fix fixes its finding and its delta breaks nothing. Never search outside the delta; a defect you meet there anyway, report at its rank.

Judge code against the intent's rules, never against how the code already does it: matching existing code is no defense where a rule says otherwise. Rank each finding by the intent. Prove it at file:line in `--detail`, by its kind: a defect's input or state and wrong outcome; a broken rule and where it is stated; an ask's decision; a test gap as Report says. File only what you can prove so.

Run a test only to prove one finding you are about to file: the narrowest command, against a mutant of one term for a test gap, or to settle a suspected defect reading cannot. Never an oracle, a suite or a sweep over terms. Mutate in your worktree, never the clone, and put it back.

## Report

One call per problem, as you find it:

```
jig finding T-1 --file alpha/alpha.go --line 12 --title "..." --detail "..." \
  --action fix --risk medium --risk-rationale "..." --oracle "go test ./alpha -run 'TestAlpha'"
```

- `--action`: `fix` when the fix is objective and keeps what the intent asks; `ask` when only a person can decide; `note` when nothing needs to change but a person should know.
- `--risk`, the rank against the intent: `high`, wrong behavior a person meets; `medium`, wrong in a case the intent names, or a broken repo rule; `low`, worth a note.
- `--oracle`: the narrowest command that proves the fix everywhere its rule applies (the test the fix adds or extends, by name, or a search that must come back empty, such as `! git grep -n '<pattern>'`), required for a fix.
- A finding names the rule broken. Before filing, search the change for the same rule's other instances: the `--detail` lists them, and `--oracle` proves the rule across them. An `--action ask` about a rule lays out every case it must decide, with one recommended answer.
- Never raise again what a person dismissed.
- A missing test is a problem only at a seam the intent names, or as proof of a defect you report, at the same three ranks. A finding about a missing or weak test names the mutant its test must catch and the assertion that catches it.

## Close the round

```
jig review done T-1 --reviewed alpha/alpha.go,beta/beta.go --summary "<what you found>"
jig review done T-1 --result result.json     # or: the whole report as one file
```

`--reviewed` lists every file you read, every `must_review` path among them. The output is the round's verdict: `clean`, or `fix-slices` with the findings. A person decides its asks, and may take up a note, with `jig triage`.

## Then

Relay `jig next <T>`'s `prompt:` to the person and stop, unless the person's prompt named later phases (a start-to-finish run): then go on to the named phase's skill: build when the round queued fixes, publish when clean. The round's asks are the person's: put each to them and record the answer with `jig triage`, or decide them yourself when the prompt told you to go with your recommendations. A subagent stops at its verdict line.

## Never

- Do not review a change you built, or call `jig review done` for a round you did not read.
- Do not edit the clone, commit, push, or write the store by hand. Edit your worktree only to prove a finding, and put it back.
