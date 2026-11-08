# A clone behind its origin is named by jig doctor and jig here (RUN-15)

Ambiguous: how a session learns that the machine map's clone of its project is stale, without `jig here` paying for a fetch.

What changed: `internal/clonebehind.Check(dir, target)` is the one judge both commands call. `jig doctor` adds a note per clone in scope; `jig here` adds a `clone` block (text) and a `clone` key (`--json`) for a behind or diverged clone only. The line is the brief's D8; jig advises and never pulls (ADR 0034).

Chosen, with the brief's defaulted decisions:
- D4: no fetch. The count is `HEAD..refs/remotes/origin/<target>` as of the clone's last fetch, so the line says so. `jig here` stays read-only and offline.
- D5: any commit behind is named, with the count.
- D6: only a clone with the target branch checked out is judged; another branch or a detached HEAD is deliberately elsewhere.
- D7: ahead is not named. Diverged is named with both counts and no command; `git status` is never run, so a dirty worktree changes nothing.
- D9: a missing clone, a folder that is no repository, or a check that errors adds nothing to `jig here`. `jig doctor` also notes a clone with the target checked out but no `origin/<target>`, naming `git fetch origin`.
- D10: the target is the marker's (or config's) repo for the clone: matched by directory in `jig doctor`, by name in `jig here`, else the only repo, else `main`.

Checked: `clonebehind.Check` in a table over real repos (same, one and two behind, ahead, diverged, another branch, detached HEAD, no origin ref, missing directory, not a repository); `jig doctor` for a behind and an up-to-date clone; an e2e chain where a clone one commit behind is named by `jig here` and `jig doctor` with the exact pull and by neither after `git pull --ff-only`; then the whole suite.

After gate round 1: the pull is `git -C <clone> pull --ff-only origin <target>`, so it works without an upstream and never pulls another remote (decided, r1-f2). A clone with no origin remote gets a note saying so rather than `git fetch origin` (decided, r1-f9). The clone path is quoted in both commands when it needs it. `jig doctor` judges the clone a linked worktree belongs to, as `jig here` does. The counts come from one `git rev-list --left-right --count`.
