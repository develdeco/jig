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

Read `jig skill publish multi-repo` when the ticket was built in several repos.

A start-to-finish run that names the publish asks the person to approve it (the reviewed head and the body's first lines) and, on their yes, publishes the first way; a prompt that told you to go with your recommendations needs no yes. In a chart run (`jig skill chart`) the main session asks once for every ticket reviewed clean. A run that names no publish stops after the review and relays the publish prompt.

## A person approves

```
jig publish T-1 --approve      # at a terminal: records the person's approval of the reviewed head
```

Only a person's approval, from a terminal, stands in for the question a bare `jig publish` asks. An agent's `--approve` is recorded as an agent's and does not. Do not pass `--yes` for a person who did not say yes.

## The What you write

The body is `## What`, `## Demo` (when picked) and `## Verification`, then the trailer the project's `jig.yaml` sets (`publish: trailer:`); slices, findings and reviewers are in the review notes comment. Hand `gh` the body as rendered: a line added after it reads back as `differs`, so an attribution line belongs in the trailer. Until the ticket has a What the body's is its title and `--body` reports `what: pending` with a `what_request` file: write a few bullets there, the first saying why the change exists, and run `jig publish T-1 --body --what <file>` (`--repo <r>` in several repos). jig stores them in the ticket; later renders reuse them.

## Recordings for the demo

Read `jig skill publish demo` when `--body` or `jig publish` reports `picks: pending`.

## A published ticket that is behind

Read `jig skill publish stacked` when `jig next` says `state: behind`.

## Then

Relay `jig next <T>`'s `prompt:` to the person (none once the pull request is recorded) and stop. Publish is the last phase: no skill follows it, even in a start-to-finish run.

## Never

- Do not squash, rebase or merge on the ticket's branch before publishing: the reviewed head is what ships. The one merge is the target's, for a ticket that is behind.
- Do not edit `pr/<repo>.md` by hand; render it again.
