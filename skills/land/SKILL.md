---
name: land
description: Lands a published ticket's pull requests at the person's word - jig land names the merge, you make it, jig land records it. Use only when the person says "Land T-1" or asks to merge a ticket's pull request; never by default.
---

# land

Land is the fifth phase, after publish. Run it only on the person's word ("Land T-1", "merge T-1"): that word is their approval of the merge and the only one asked. A start-to-finish run, a chart run and a Stop hook end at published; never land because a ticket is published, and never because a note or `jig next` names `Land T-1`.

## Do

```
jig land T-1               # names the merge, one row per repo: the pull request, its base and the command
<run each command it names, from the repo's clone or anywhere gh can reach>
jig land T-1               # the host says merged: writes the landed line, says what lands next
```

jig never merges. The command is `gh pr merge <url> --squash` where the repo allows squash, else `--merge`, else `--rebase`; `--method squash|merge|rebase` names another when the person said "Land T-1 by merge". Do not add `--delete-branch`: a ticket's branch and worktree are removed the way publish says. Run `jig land T-1` again when the person says the pull request is merged on the host: it records that merge too, as the host's.

`already landed` means every repo has its line; stop and say what lands next.

## A refusal writes nothing

- `LAND_NOT_PUBLISHED`: the ticket has not published, a repo has no pull request, or its branch is behind (`jig skill publish stacked`, then `jig publish T-1`).
- `LAND_WAITS`: tickets named in the refusal must land first, and are landed one at a time, in the order it names. Order is the chart's `blocked_by` edges and the parent links: a `merged` dependency, a stacked parent, the tasks of a multi-task, the tickets of an issue. Merge a stacked child only after its parent.
- `LAND_BASE_DIFFERS`: the pull request goes into another branch than the plan says; run `jig publish T-1`, which points it right.
- `LAND_CLOSED`: the pull request was closed without merging; run `jig publish T-1` for a new one.
- `SHARED_TASK_ISSUE_NEEDED`: a task two issues share lands per issue: `jig land T-1 --issue <ID>`, once for each issue the person names. One issue's landing says nothing of the other's.

A pull request the host reports `CONFLICTING` is named with the merge that resolves it: `git merge origin/<base>` in the ticket's worktree, then `jig publish T-1`, then land again. A base that merely moved past the branch is no conflict: the host merges it. A host jig cannot ask is a note, not a refusal.

## A container

An issue or a multi-task lands by the same verb once its own pull request is recorded: it waits for its tasks (or the tickets of its chart) to land in its branch, then names its merge into the target.

## Then

Say what landed and what `jig land` names next, and stop. Do not land the next ticket unless the person said to.

## Never

- Do not run `gh pr merge`, or any merge of a pull request, unless the person said to land it.
- Do not edit the journal or a landed line by hand; `jig land` writes it.
- Do not answer for the person, or approve a publish for them, to reach a landing.
