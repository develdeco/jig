A refused report (`REVIEW_INVALID`) leaves the round open: correct it and run the command again. `REVIEW_STALE_HEAD`: the branch moved; run `jig gate T-1`, review the new head.

In a project of several repos a `repos` table gives each repo its own `head`, `base`, `scope`, and the `worktree` table one worktree for each; every file is `<repo>/<path>`.

A note you filed below the floor reads `addressed` once a later round changes its file and you no longer see it: report it again (`--prior`, `--still-present`) while it stands.

An earlier finding is carried with `--prior` or `--still-present`:

```
jig finding T-1 --still-present r1-f2 --line 14     # an earlier finding, unchanged
```

- `--prior r1-f2` for a changed version of a finding under `open`, `answers` or `dismissed`, `--still-present` for an unchanged one.
