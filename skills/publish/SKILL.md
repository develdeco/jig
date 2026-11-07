---
name: publish
description: Publishes a ticket's reviewed branch - renders the pull request body, pushes the reviewed head, opens or records the pull request, and records a person's approval - with jig publish. Use after a gate round is clean, when asked to open or update the PR.
---

# publish

Run `jig next <T>`. If the phase it names is not publish, tell the person what it needs and the prompt that does it, and stop. Do not run an earlier phase on your own.

jig publishes the head the last clean gate round reviewed, **unchanged**: it reconciles, squashes and commits nothing on the branch. Publishing needs every slice green and a clean round (`PUBLISH_NOT_CLEAN`); the review notes say whether its reviewer built part of the change. A branch that moved since the round (`PUBLISH_UNREVIEWED_HEAD`) needs `jig gate T-1` again.

## Two ways, one hand-off

You push and open the pull request yourself, and jig renders and records:

```
jig publish T-1 --body                 # renders pr/<repo>.md and the review notes; touches no lease, branch or remote
git push origin <branch>               # your own push of the reviewed head
gh pr create --head <branch> --base <base> --title "<title>" --body-file <pr_body>
jig publish T-1 --pr <url>             # checks the PR is from the ticket's branch (and the reviewed head), then records it
```

The `--body` output gives the branch, base, title and the `pr_body` path to hand to `gh`. The branch is the ticket's recorded one, else `<id>-<slug>`. `--pr` refuses a pull request from another branch (`PR_HEAD_MISMATCH`) or at another commit; with no pull-request host it records the URL unverified and says so. `--pr` posts the review notes once per content: post nothing yourself. It also reads the title and body back and journals whether they are the render's (`body_match`: `matches`, `differs`, `unknown`); on `differs`, `gh pr edit` it with the `--body` output. When the last is recorded, run the `git worktree remove` the result gives for each worktree the ticket still has (git refuses one with changes).

Or jig pushes and opens it:

```
jig publish T-1            # asks before it pushes; --yes stands in for the answer
```

It pushes the reviewed head as it is (never forced: `PUBLISH_NOT_FAST_FORWARD` when origin moved past it), opens the pull request or updates the branch's open one, and posts the review notes as a comment unless these exact notes are already on it.

A ticket built in several repos publishes one pull request for each, from one branch name; each body names the others under `## Part of T-1` (`pending` until recorded). `--body` renders them all, or one with `--repo <r>`; push, open and record each with `jig publish T-1 --pr <url> --repo <r>`. When the last is recorded, render the bodies again and update the pull requests with the urls. Bare `jig publish` does it all, and `--approve` approves every repo's head.

A start-to-finish run that names the publish asks the person to approve it (the reviewed head and the body's first lines) and, on their yes, publishes the first way; a prompt that told you to go with your recommendations needs no yes. In a chart run (`jig skill chart`) the main session asks once for every ticket reviewed clean. A run that names no publish stops after the review and relays the publish prompt.

## A person approves

```
jig publish T-1 --approve      # at a terminal: records the person's approval of the reviewed head
```

Only a person's approval, from a terminal, stands in for the question a bare `jig publish` asks. An agent's `--approve` is recorded as an agent's and does not. Do not pass `--yes` for a person who did not say yes.

## The What you write

The body is `## What`, `## Demo` (when picked) and `## Verification`; slices, findings and reviewers are in the review notes comment. Until the ticket has a What the body's is its title and `--body` reports `what: pending` with a `what_request` file: write a few bullets there, the first saying why the change exists, and run `jig publish T-1 --body --what <file>` (`--repo <r>` in several repos). jig stores them in the ticket; later renders reuse them.

## Recordings for the demo

When the build recorded end-to-end scenarios and nobody picked, `--body` and `jig publish` report `picks: pending` and name a `picks_request` file: the candidates, each with an id, and the intent. Pick the recordings that show the change, compose them into flows, and write the answer:

```
{"flows": [{"title": "...", "items": [{"id": "r2"}, {"id": "r3", "caption": "..."}]}], "summary": "..."}
```

then run `jig publish T-1 --body --picks answer.json` (or the bare publish with `--picks`). Pick a few; an id is used once; flows keep their step order. A refused pick publishes with no demo and says why; a pick that stood is reused for the same head and recordings.

## A published ticket that is behind

`jig next T-1` says `state: behind` for a published ticket stacked on a parent that has since moved or merged. In a checkout of its branch run the `merge` its `stack` row names, then bare `jig publish T-1`: it pushes the merge and points the branch's open pull request at its base, opening no second one. A merge git made by itself needs no new round; a hand-resolved merge or any other commit needs `jig gate T-1` first (`PUBLISH_UNREVIEWED_HEAD`), which reviews your resolution alone.

## Then

Relay `jig next <T>`'s `prompt:` to the person (none once the pull request is recorded) and stop. Publish is the last phase: no skill follows it, even in a start-to-finish run.

## Never

- Do not squash, rebase or merge on the ticket's branch before publishing: the reviewed head is what ships. The one merge is the target's, for a ticket that is behind.
- Do not edit `pr/<repo>.md` by hand; render it again.
