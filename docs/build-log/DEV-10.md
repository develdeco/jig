# The git budget is compared with main's measured counts (DEV-10)

## Each ticket no longer commits git counts

The per-package budget in `.github/git-budget.yaml` made every ticket that added git-heavy tests commit new counts, taken from CI's Linux log, and a decrease failed the leg too. Two tickets touching one package conflicted on the same line. The counting is unchanged; what each count is compared with is not.

- **Where main's counts live.** The Actions cache. The push run on main, after `go test` passes on the Linux leg with the full package list, writes a baseline (`packages:` map of counts, the shape the budget file had) and saves it under `gitbudget-main-<sha>`. A pull request restores with `restore-keys: gitbudget-main-`, the newest main entry. An artifact cannot be fetched from another run without extra API calls; a branch or ref write needs `contents: write` and has to pass the `protect main` ruleset; the cache needs neither. An entry unused for 7 days is evicted, which the no-baseline rule covers.
- **No baseline.** The leg passes and the summary says "no main baseline: counts shown, nothing compared". Recorded, never a refusal; the next main run seeds the cache.
- **The comparison.** A count above baseline + max(5% of the baseline rounded up, 100) fails. A decrease never fails. A package with no baseline entry has baseline 0, so it may start 100 processes. A baseline package that ran no test (a docs-only run tests `./lint` alone) is shown as "not run". The old floor of 3 went with the 1% rule.
- **Accepting a real increase.** The `git-budget-accepted` pull request label, read through the API (`pull-requests: read`) so a re-run sees a label added after the push. Each "over" becomes "accepted" and the leg passes; the summary records them. A yaml edit per accepted jump was rejected: it is the budget commit this removes.
- **The cost.** Creep under the threshold shows only in the job summary.
- **Config.** `.github/git-budget.yaml` holds `margin: 5` and `slack: 100`, no counts.
- **Not checked here.** Only CI's Linux leg measures; the comparison is proven by the fixture tests of `internal/gitbudget` and the lint tests of the workflow.
