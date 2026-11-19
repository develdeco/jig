Your worktree holds the round's head and nothing of the person's: the `worktree` table of `jig review context` gives each repo its `clone`, its `dir` and a `command`; the head is in the `head` line or the `repos` table.

- In a worktree your harness made for you (Claude Code's `isolation: worktree`), run `git checkout --detach <head>` inside it. It starts at the harness's own commit, so this is the first command you run.
- With no worktree method, run the row's `command`: `git -C <clone> worktree add --detach <dir> <head>`. When `dir` exists already (an earlier round's), the row's `command` is `git -C <dir> checkout --detach <head>`.
- A round over several repos needs a worktree for each repo: the harness's covers one, the rows' commands the rest.
- Read, probe and put files back there, never in the clone.
- After `jig review done`, remove a worktree you made with its command: `git -C <clone> worktree remove <dir>`, never forced (git refuses one with changes: put them back first). One the harness made is the harness's to remove.
