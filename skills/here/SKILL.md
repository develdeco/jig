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
| `ticket`, `ticket_how` | the ticket the branch name leads with (`branch`), or the one you named (`arg`) |
| `slices` | every slice of the ticket with its state; `blocked_by` shows what each waits for |
| `questions`, `pending_verifications` | open questions to answer; commits whose oracle is still running |

A branch that leads with no ticket id (`main`, a feature branch) binds no ticket: name one, or run `jig next`.

## What to build next

```
jig next              # tickets in store order: ready, blocked (and by whom), new, done
jig next JIG-4        # its ready slices by repo, and the branch to work on
jig next JIG-4 --topology   # every slice with its repo, wave and blockers
```

A ticket is `ready` when it has a slice that is queued with every blocker green and nothing blocks the ticket; a blocked ticket names the ticket it waits for. Slices of one repo build one after the other; repos are independent, so a ready set that spans repos can go to one session each. Create the branch `jig next` names in each repo; `jig here` then binds it to the ticket.

When `jig next` ends with an `overlap` table, two tickets' local branches change the same file: build them one after the other.

## When here says nothing

No output and exit 0 means the directory is in no jig project. Nothing is wrong; do not look for one. If the repo should belong to a project:

- a fresh clone of a repo a mapped project lists is recognized by its `origin` remote and mapped on the spot;
- a repo with no remote needs `jig map <repo>=<path>` once, run inside the project;
- `jig ignore <path>` makes jig leave a directory alone (`--remove` undoes it).

## Never

- Do not infer the ticket from a commit message or the directory name; `jig here` binds it from the branch.
- Do not edit `projects.yaml` or a `jig.yaml` by hand to make `jig here` answer; `jig map` and the marker are the ways.
