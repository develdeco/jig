# A failed claim undoes what its write touched, best effort, and names what it could not (STORE-8)

Ambiguous: `Claim`'s doc comment promised that nothing the write wrote survives a failed claim, but only the push-failure paths undid anything, and they stopped at the first failing step.

- The contract: the `paths` a write returns are meaningful whatever its error is, and list only what it touched. `Claim` undoes exactly those on a write error, and nothing when there are none (a `clean -fdx --` with no pathspec would clean the whole store). Callers whose write is one atomic single-file replace keep returning no paths on error.
- One undo, `undoPaths(sha, paths)`: restore a path that existed at the pre-claim commit, `rm -r --cached --ignore-unmatch` one that did not, then clean. `undoClaim` is `reset --soft` plus `undoPaths`; a write error needs no reset because nothing was committed. On an unborn branch only the clean runs.
- The undo is best effort: every step for every path and the final clean are tried, and the errors are joined. A locked file then costs only itself, not the minted folder after it. No retry loop (a lock that outlasts the claim outlasts a retry; the report is the cure).
- A failed undo returns an error that unwraps to the write's own error (so `errors.Is` and `errors.As` on a caller's sentinel keep working) or, on the push paths, keeps `ID_NOT_CLAIMED`; both name the leftover paths and a help line to finish by hand, and `store.IsUndoIncomplete(err)` recognizes either shape through wrapping.
- Rejected: refusing `Sync` over an uncommitted mint. Jig cannot tell a mint from a person's own edit, and it records and shows rather than refuses where it cannot prove. The next `Sync` may still commit leftovers, as it commits anything dirty.
- `Mint` is unchanged and removes nothing itself. A writers store's claims-log reservation is a committed fact and stays, a gap in the numbers.
