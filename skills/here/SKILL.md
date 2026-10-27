---
name: here
description: Names where the work is from the working directory alone - project, store, repo, branch, ticket, slices, questions, verified commits, and what is ready to build next. Use at the start of a session in a repo, when unsure which ticket the work is on, or before picking up work.
---

# here

`jig here` answers "where am I" from the directory you are in. Run it before you act on a repo you did not just set up; it reads, never writes a repo or the store, and costs one call.

## Orient

```
jig here            # blocks: project, store, repo, branch, ticket, slices, questions
jig here JIG-4      # name the ticket yourself
jig here --json     # one object; every part carries how it was found
jig here --explain  # the steps the lookup took, in order
```

Run it from the repo, a directory in it, or a linked worktree of it: all read as the same clone.

## Read the answer

| Line | Means |
|---|---|
| `project`, `how` | the project, and how it was found: `marker` (a committed `jig.yaml`), `map-path` or `map-remote` (this machine's map), `store`, `flag` |
| `store` | the store's directory; `store_remote` alone means it is not cloned here - run the `git clone` the help line gives, then `jig here` again |
| `repo`, `repo_dir`, `branch` | which repo of the project this is, its clone, the branch checked out |
| `ticket`, `ticket_how` | the ticket the branch name leads with (`branch`: its id, an id it had before, or the number of its GitHub issue), or the one you named (`arg`) |
| `slices` | every slice of the ticket with its state; `blocked_by` shows what each waits for |
| `questions`, `pending_verifications` | open questions to answer; commits whose oracle is still running |

A branch that leads with no ticket (`main`, a feature branch) binds no ticket: name one, or run `jig next`. Work the person asks for with no ticket is fresh work: plan it with the intake skill, which mints its ticket. `jig claim` on such a branch records it as that ticket's branch.

## What to build next

```
jig next              # tickets in store order: ready, blocked (and by whom), new, done; the ready ones again in priority order
jig next JIG-4        # its ready slices by repo, and the branch to work on
jig next JIG-4 --topology   # every slice with its repo, wave and blockers
```

A ticket is `ready` when it has a slice that is queued with every blocker green and nothing blocks the ticket; a blocked ticket names the ticket it waits for. Slices of one repo build one after the other; repos are independent, so a ready set that spans repos can go to one session each. Create the branch `jig next` names in each repo; `jig here` then binds it to the ticket.

When `jig next` ends with an `overlap` table, two tickets' local branches change the same file: build them one after the other.

The `next` table lists the ready tickets by the owner's rank (`jig rank <T> <n>`, lowest first; unranked last, by id). `warning: rank N is shared` means the owner has not said which of those comes first. A `workspace_overlap` table names two ready tickets that build in the same workspace: start the first and the other after it.

## Phases and prompts

A ticket moves through four phases, each with a skill: brief (`intake`), build (`build`), review (`review`) and publish (`publish`). `jig next <T>` names the one it is at (`phase:`), whether it can start (`ready:`) and the prompt that asks for it (`prompt:`, such as "Build SHOP-1"). Each phase's skill starts with `jig next <T>` and stops if the phase is not its own; it ends by relaying the `prompt:` to the person, who decides when the next phase runs. When you only oriented, relay that `prompt:` and stop.

**An automatic run** is a prompt that names several phases ("Take SHOP-1 from brief to PR"): run them in order, each with its skill, and end the run at a refusal or a stop the skill gives. Wait for the review's subagent in the same turn as the review skill says (TaskOutput with `block: true` in Claude Code); do not end the turn or schedule a wakeup while it runs. The person's moments stay theirs unless the prompt delegates them: "go with your recommendations" delegates the brief's approval, and a prompt that ends at the PR ("... to PR") or says "and publish it" delegates the publish. A delegated brief approval is recorded with agent provenance; a delegated publish goes the publish skill's first way, which asks for no approval.

## When here says nothing

No output and exit 0 means the directory is in no jig project. Nothing is wrong; do not look for one. If the repo should belong to a project:

- a fresh clone of a repo a mapped project lists is recognized by its `origin` remote and mapped on the spot;
- a repo with no remote needs `jig map <repo>=<path>` once, run inside the project;
- `jig ignore <path>` makes jig leave a directory alone (`--remove` undoes it).

## Never

- Do not infer the ticket from a commit message or the directory name; `jig here` binds it from the branch.
- Do not edit `projects.yaml` or a `jig.yaml` by hand to make `jig here` answer; `jig map` and the marker are the ways.
