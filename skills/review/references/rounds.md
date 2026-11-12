A refused report (`REVIEW_INVALID`) leaves the round open: correct it and run the command again. `REVIEW_STALE_HEAD`: the branch moved; run `jig gate T-1`, review the new head.

In a project of several repos a `repos` table gives each repo its own `worktree`, `head`, `base`, `scope`; every file is `<repo>/<path>`.

A note you filed below the floor reads `addressed` once a later round changes its file and you no longer see it: report it again (`--prior`, `--still-present`) while it stands.
