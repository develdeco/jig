# The gate reuses an oracle pass on the same tree

The gate ran every manifest oracle in every workspace at the start of each round. jig had just run the slice's oracle itself at the builder's green (ADR 0020), often on exactly the commit the gate then reviewed. In BS-1 that rerun was about 9 minutes of every 18 to 22 minute gate round on the Windows dev machine. jig's own CLI tests also pay a real oracle run per green slice since ADR 0020, and the gate rerunning them adds the same cost again.

no-mistakes keeps evidence over confidence and refuses a skipped check, and a pass recorded for the identical tree is evidence, not a skip.

## The rule

- **A recorded pass on the same tree is reused.** Before each run of the suite (a manifest oracle in a workspace), the gate looks in the ticket's journal for an `oracle` pass line with a commit. It needs a slice whose oracle and workspace are that run's, and a commit whose tree is the gate head's tree. If one exists, the run is reused, not run again.
  - frontier records a commit on an `oracle` line only when the lease's tree was fully clean before the run, untracked files included (ADR 0020). So the pass tested exactly that commit.
  - The same tree holds the same manifest, so the slice's oracle name and workspace resolve to the command the gate would run.
- **Only what is not covered runs.** The gate brings up the env classes only when some run is left to do.
- **The reviewer sees the evidence.** `review.json`'s `oracles_passed` lists reused runs too, each with `reused_from`, the commit whose pass it reuses. The prompt's sentence, that jig ran every command in `oracles_passed` on this head, holds for the head's content: the reused pass ran on the identical tree.
- **The gate's own runs are bounded.** They and publish's revalidation run within the same 30-minute bound as jig's run at green, and kill the process tree past it.

Publish's revalidation never reuses: it runs only after the target moved, and then the tree under test is new.

## Test seams

- **`review.json` as the gate writes it,** through `TestGateReusesAnOraclePassOnTheSameTree`:
  - every run reused, while failing tests are committed at the head, so a rerun would have stopped the round;
  - a pass recorded on another tree not reused;
  - a partial pass that runs the rest.
