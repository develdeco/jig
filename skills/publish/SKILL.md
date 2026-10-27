---
name: publish
description: Publishes a ticket's reviewed branch - renders the pull request body, pushes the reviewed head, opens or records the pull request, and records a person's approval - with jig publish. Use after a gate round is clean and independent, when asked to open or update the PR.
---

# publish

Run `jig next <T>`. If the phase it names is not publish, tell the person what the ticket needs and the prompt that does it, and stop. Do not run an earlier phase on your own.

jig publishes the head the last clean gate round reviewed, **unchanged**: it reconciles, squashes and commits nothing on the branch, so the person's branch never diverges from origin's. Publishing needs every slice green and a clean round whose reviewer did not build the change (`PUBLISH_NOT_CLEAN`, `PUBLISH_NOT_INDEPENDENT`). A branch that moved since the round (`PUBLISH_UNREVIEWED_HEAD`) needs `jig gate T-1` again.

## Two ways, one hand-off

You push and open the pull request yourself, and jig renders and records:

```
jig publish T-1 --body                 # renders pr/<repo>.md and the review notes; touches no lease, branch or remote
git push origin <branch>               # your own push of the reviewed head
gh pr create --head <branch> --base <base> --title "<title>" --body-file <pr_body>
jig publish T-1 --pr <url>             # checks the PR is from the ticket's branch (and the reviewed head), then records it
```

The `--body` output gives the branch, base, title and the absolute `pr_body` path to hand to `gh`. The base is the repo's target, or, for a ticket stacked on another, the parent's branch until the parent merges. The branch is the one the ticket records, else `<id>-<slug>` (the GitHub issue number leads it once the ticket is mirrored); `jig next T-1` names it too. `--pr` refuses a pull request from another branch (`PR_HEAD_MISMATCH`) or at another commit; with no pull-request host it records the URL unverified and says so.

Or jig pushes and opens it:

```
jig publish T-1            # asks before it pushes; --yes stands in for the answer
```

It pushes the reviewed head as it is (never forced: `PUBLISH_NOT_FAST_FORWARD` when origin has moved past it), opens the pull request or updates the one the branch has open, and posts the review notes as its first comment.

A ticket whose slices build in several repos publishes one pull request for each, all from the same branch name, and each body names the others under `## Part of T-1` (`pending` until that repo's pull request is recorded). `--body` renders them all, or one with `--repo <r>`; push and open each, then record each with `jig publish T-1 --pr <url> --repo <r>`. The ticket is published when the last is recorded; render the bodies again then, and update the pull requests with the urls. Bare `jig publish` does it all, and `--approve` approves every repo's head.

A start-to-finish run that names the publish asks the person to approve it (the reviewed head and the body's first lines) and, on their yes, publishes the first way (`--body`, your push, `gh pr create`, `--pr`); a prompt that told you to go with your recommendations needs no yes. A run that names no publish stops after the review and relays the publish prompt.

## A person approves

```
jig publish T-1 --approve      # at a terminal: records the person's approval of the reviewed head
```

Only a person's approval, from a terminal, stands in for the question a bare `jig publish` asks. An agent's `--approve` is recorded as an agent's and does not. Do not pass `--yes` for a person who did not say yes.

## Recordings for the demo

When the build recorded end-to-end scenarios and nobody picked, `--body` and `jig publish` report `picks: pending` and name a `picks_request` file: the candidates, each with an id, and the intent. Pick the recordings that show the change to a reviewer, compose them into flows, and write the answer:

```
{"flows": [{"title": "...", "items": [{"id": "r2"}, {"id": "r3", "caption": "..."}]}], "summary": "..."}
```

then run `jig publish T-1 --body --picks answer.json` (or the bare publish with `--picks`). Pick at least one and a few at most; an id is a candidate and used once; flows keep their step order. A pick jig refuses publishes with no demo and says why; the next publish of the same head and recordings reuses a pick that stood.

## Then

Relay `jig next <T>`'s `prompt:` to the person (there is none once the pull request is recorded) and stop. Publish is the last phase: no skill (intake, build, review) follows it, even in a start-to-finish run.

## Never

- Do not squash, rebase or merge on the ticket's branch to "tidy" it before publishing: the reviewed head is what ships.
- Do not edit `pr/<repo>.md` by hand; render it again.
