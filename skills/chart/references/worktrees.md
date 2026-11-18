The rarer worktree cases of a wave's builds, by the ticket's `worktrees` rows:

- A single-repo ticket's `add` row in another clone, or any `add` with no worktree method (no Agent isolation, no EnterWorktree): you run its `command`, one at a time, then start the subagent with no isolation.
- A multi-repo ticket's other clones are its nested builders'; its own subagent takes isolation for its row in this repo.
- `present`: no isolation.
- `no_clone`: tell the person; do not start it.

The `<where>` of a builder prompt with no isolation is: "Work only in <dir> (the row's `dir`), on branch <branch>; never rename or reset."
