# jig advises a ticket's worktree and makes none

[ADR 0037](0037-a-chart-is-run-from-the-main-session.md) had each build subagent of a chart run work in its own worktree, and left the git to the skill. The `chart` skill spelled out `git worktree add <dir> -b <branch> <base>`, so every run worked out three things for itself: where the folder goes, what the base is (the parent's branch for a stacked ticket, the target otherwise, and `jig next` already knew it), and whether the branch exists already. A ticket that builds in two repos needed two folders that never collide. Nothing ever removed a worktree, so a finished chart left one folder for each ticket beside the clone. And a chart run starts several subagents at once, each wanting its own approval for git and the oracles, so the person was asked the same thing once for every parallel subagent.

jig already advises a session's context and runs none of the tools ([ADR 0034](0034-jig-advises-context-and-runs-no-context-tool.md)). A worktree is the same kind of thing: the session's own checkout, made with the session's own tools.

## Decision

**jig advises a ticket's worktree and makes none.** There is no `jig worktree` verb. `jig next <T>` gives, for each repo the ticket builds in, a row (`worktrees` in `--json`, a `worktrees` table in the text) whose `action` is `add`, `present`, `remove` or `no_clone`, with the git command to run for an add and a remove. The session runs it.

**A worktree is `<clone>.worktrees/<TICKET>`.** It sits beside the clone, one folder for each clone, named by the ticket id: never inside the clone, where it would show in the clone's status and in a search of the repo, and never in jig's pool, whose leases are for jig's phases. A ticket that builds in two repos gets one beside each clone, so two repos never share a folder. A repo with no clone mapped on this machine gets `no_clone` and a note, and no command. Paths are written with forward slashes, and a path with whitespace is wrapped in double quotes in the command.

**The add is advised where the work starts.** The work is wanted once the ticket has a brief and no blocker holds it. A row is an `add` when the work is wanted, the clone is there and no work tree holds the ticket's branch. The command is `git -C <clone> worktree add <dir> -b <branch> <base>`, with the stack plan's start point as the base: the parent's branch in a repo shared with a parent that has not merged, `origin/<target>` otherwise. A branch the clone already has is added without `-b` and without a base, since a branch outlives a removed worktree and a restack or a retarget needs the worktree back. `git -C` makes the command run from anywhere.

**A work tree is found by its branch.** The advice asks git which work tree holds the ticket's branch, never what is at the usual path, so a worktree made by hand anywhere is found. One that holds the branch is `present` (with its `dir`), and that includes the clone's own work tree: an add that git would refuse is never advised. A work tree whose directory is gone (prunable) holds nothing.

**The remove is advised where the work ends.** Once the ticket is published or closed, a linked worktree that still holds its branch gets a `remove` row: `git -C <clone> worktree remove <dir>`, never forced. Git refuses a worktree with changes and the person decides. The clone's own work tree is never offered for removal. A published stacked ticket that is behind or must retarget ([ADR 0038](0038-a-published-stacked-ticket-can-be-behind.md)) has not ended: its merge is made in its worktree, so it keeps its `present` and `add` rows. `jig publish --pr` gives the removal in its result the moment the last pull request is recorded, since that is the moment the work ends; `jig next <T>` gives it after, until the worktree is gone. A bare `jig publish` says nothing of it.

**The chart carries it.** `jig next <chart>` gives each ticket of the run, in any wave, its rows, in the tickets' entries and as a `worktrees` table after the `run` table. A stacked ticket of a later wave names its parent's branch as its base before that branch exists (`origin/<parent branch>`), so the session reads `jig next <T>` again when the ticket's wave starts. `jig status <chart>` and the TUI are unchanged.

**The skill asks once.** Before the first wave the `chart` skill names what the builds run (`jig`, `git` with the worktree commands, and the oracles in the tickets' `slices.yaml`) and asks the person to allow them once. One yes covers the run, in place of a prompt for each parallel subagent. jig sets no permission. The skill runs the adds one at a time, because git's records of a repository's worktrees are not safe against parallel adds, and removes each ticket's worktrees after its publish.

**git answers.** `gitx.Worktrees` runs `git worktree list --porcelain`. The advice asks it only when the ticket's branch exists in the clone, read in process, since no work tree can hold a branch that is not there, and once for each clone in a command, however many tickets it decides. An error from either question is no worktree.

## Rejected alternatives

- **A `jig worktree add` and `remove`.** jig would then own a second place where a session's checkout is made, and a removal that failed halfway would need a recovery of its own. The commands are short and git says what is wrong with them. Advising keeps jig out of the session's files, as ADR 0034 does for context tools.
- **Worktrees in jig's pool or inside the clone.** Pool leases are clones and detached verification worktrees that jig returns warm; none holds a branch, and a session's checkout is not a lease. Inside the clone, a worktree shows up in the clone's status and in every recursive search of it.
- **jig writing the permission settings.** What a harness may run is the person's to allow, and it differs by harness. The skill asks, and jig sets nothing.
- **A forced remove.** `--force` discards changes the person has not looked at. Git's refusal is the check, and the skill says to commit or discard first.
- **Listing worktrees in process.** The git program answers, once for each clone while the ticket's branch exists. Reading git's worktree records in process is a follow-up if the git budget asks for it.

## Consequences

- Amends [ADR 0037](0037-a-chart-is-run-from-the-main-session.md): a chart run's worktrees come from jig's advice, the main session asks once to allow what the builds run, and removes the worktrees after the publish.
- `jig next <T>` runs `git worktree list` once for each clone that has the ticket's branch, and `jig next <chart>` once for each such clone of the run. `.github/git-budget.yaml` counts these in `cmd/jig`, `e2e` and `internal/gitx`.
- Registered worktrees whose directory was deleted by hand are ignored, and left to `git worktree prune`. A branch that exists only on the origin has no work tree here, so the advice cuts it from the start point; fetch it first to have it start from the origin's copy.
- `CONTEXT.md` gains *Ticket worktree* and points the *Chart run* entry at it. `ARCHITECTURE.md` changes in the `internal/gitx` row, a paragraph on the advice, and the skills paragraph. The `chart`, `publish` and `here` skills change and stay under the 6000 bytes and 100 lines the lint allows.
