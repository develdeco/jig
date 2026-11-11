A refused report (`REVIEW_INVALID`) leaves the round open: correct it and run the command again. `REVIEW_STALE_HEAD`: the branch moved; run `jig gate T-1`, review the new head.

Under `scope: delta`, do what the context's `read` line says; the tables of the context are the full round's. In a project of several repos a `repos` table gives each repo its own `worktree`, `head`, `base`, `scope`; every file is `<repo>/<path>`.

`recordings` lists the build's end-to-end recordings; read the ones that bear on the change. A finding names a recording by scenario, step and commit, never by path.

A note you filed below the floor reads `addressed` once a later round changes its file and you no longer see it: report it again (`--prior`, `--still-present`) while it stands.
