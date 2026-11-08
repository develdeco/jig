# A verification a git hook starts does not inherit the hook's variables (RUN-16)

Ambiguous: a verification started from a commit's git hook ran with the hook's environment (`JIG_GITHOOK_WRAPPED`, `GIT_AUTHOR_*`, `GIT_INDEX_FILE`, `GIT_PREFIX`). Its oracle then skipped the clone block (the marker) and authored every commit it made as the commit that started it.

Chosen:
- F1: `jig githook post-commit` removes the hook-only variables from its own process before it starts anything: `JIG_GITHOOK_WRAPPED`, `GIT_AUTHOR_*`, `GIT_COMMITTER_*`, `GIT_INDEX_FILE`, `GIT_PREFIX`, `GIT_DIR`, `GIT_WORK_TREE`, `GIT_COMMON_DIR`. `GIT_EXEC_PATH` stays. The removal goes through an env seam (`unsetenv`) that a test env leaves as a no-op, because the in-process tests share one process whose pinned identity must survive.
- F1 also drops the command-line config git exports for a commit made with `git -c` (`GIT_CONFIG_PARAMETERS`, `GIT_CONFIG_COUNT`, `GIT_CONFIG_KEY_n`, `GIT_CONFIG_VALUE_n`), kept from gate round 1 (r1-f2): they outrank the repository's own config, so they would author the oracle's commits as the `-c` identity.
- F2: e2e's `jigEnv.environ()` drops `JIG_GITHOOK_WRAPPED` unless the test sets it through `extra`.
- F3: the hook reads only its working directory and repository, so nothing it records changes.

Checked: a cmd/jig test sets the hook variables, runs the hook and reads the process environment at the spawn seam (red before the fix, green after); `TestGitHookBlockInAClonesOwnHooks` passes under `JIG_GITHOOK_WRAPPED=1`.

Follow-up: test packages are still not immune to a `GIT_AUTHOR_*` a person exports in their own shell.
