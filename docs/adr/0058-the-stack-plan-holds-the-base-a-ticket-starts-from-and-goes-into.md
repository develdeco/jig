# The stack plan holds the base a ticket starts from and goes into

A ticket's pull request went into the target, or into an unmerged stacked parent's branch. With a chart that has an issue (ADR 0057), a task's pull request goes into its container's branch (the issue's, or its multi-task's) and the issue's own pull request is what reaches the target. Several readers decide where a branch starts and where its pull request goes: `jig next`, `jig here`, publish, the gate's clean-merge ref and the merge fact. Each deciding for itself would drift.

## Decision

**The plan holds the base.** `stack.Repo` gains `Container` and `ContainerBranch` (and the copy of that branch the clone holds, and the ref it is cut from), and `PRTarget` and `StartFrom` name the base. Every reader takes the base from the plan; none computes it.

- `PRTarget` is the container's branch whenever the ticket has a container, the repo's target otherwise. An unmerged stacked parent still wins while it has not merged; once it merged, the ticket goes into the container's branch, not the target.
- `StartFrom` is `origin/<target>` for a ticket with no dependency, the container's branch behind a `merged` dependency of that container or of the container above it (a multi-task's issue), and the target behind one outside them or a settled stacked parent, and the unmerged parent's branch as before.
- `Retarget` compares the ticket's branch with the head of the base the plan names.

**A task started from the target still goes into its container.** A task with no dependency starts from the target, since nothing of its container's is yet needed, and its pull request goes into the container's branch all the same. It is not told it is behind its container: a container that moved past a task is for Land to name, not for each read of the plan.

**An issue's branch name is the existing rule.** The container's branch is `ResolveTicketBranch` of the container: `<id>-<slug>`, the tracker's id when mirrored. It has one name in every repo, and the repos an issue or a multi-task builds in are the union of its tasks'. Nothing creates the branch: where the clone has no copy `jig next` says to fetch it and names the push that creates it from the target (from the issue's branch, for a multi-task's), and publish does not refuse, the host refuses a base that is absent.

**The merge fact follows the base.** The branch-tip fallback asks whether the tip is in `origin/<base>`, the container's branch for a ticket with one, so a `merged` dependency inside a chart is met when its pull request merged into the issue's branch, which is where its dependents start. The host's word on a recorded pull request holds for any base.

## Consequences

A ticket of a legacy chart, of no chart, or of two charts has no container, and every read is what it was. Publishing an issue or a multi-task, running the full suite before the issue's pull request, and naming a container that moved past a task are later entries; this one only says where each ticket starts and goes.
