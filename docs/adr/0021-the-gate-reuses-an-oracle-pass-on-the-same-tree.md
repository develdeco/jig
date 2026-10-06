# The gate reuses an oracle pass on the same tree

The gate ran every manifest oracle in every workspace at the start of each round. jig had just run the slice's oracle itself at the builder's green (ADR 0020), often on exactly the commit the gate then reviewed. In BS-1 that rerun was about 9 minutes of every 18 to 22 minute gate round on the Windows dev machine. jig's own CLI tests also pay a real oracle run per green slice since ADR 0020, and the gate rerunning them adds the same cost again.

no-mistakes keeps evidence over confidence and refuses a skipped check, and a pass recorded for the identical tree is evidence, not a skip.

## The rule

- **A recorded pass stands for the gate's own run only when it is the same run.** Before each run of the suite (a manifest oracle in a workspace), the gate looks in the ticket's journal for an `oracle` pass line, written by frontier at a slice's green (ADR 0020). It reuses the line, instead of running the oracle again, only when all of these hold:
  - **the same command,** exactly, as the manifest at the gate's head resolves it. frontier records the command it ran on the line, since a slice can change `.claude/jig.yaml`, and every oracle runs from the repo root, so the command names the run.
  - **the same tree:** the line's commit has the gate head's tree. frontier records a commit only when the lease's tracked and untracked files were exactly that commit before the run.
  - **the same env classes:** the env class that was up for the pass, as recorded on the line, is the whole set the gate brings up for the ticket's slices. A pass with no env class stands only when the gate brings up none.
  - **no failure beside it:** no failed run of that command is recorded on the same tree, so a flaky oracle is sampled again rather than trusted on its pass.
- **Only what is not covered runs.** The gate brings up the env classes only when some run is left to do.
- **The reviewer sees the evidence.** `review.json`'s `oracles_passed` lists reused runs too, each with `reused_from`, the commit whose pass it reuses. This amends ADR 0017: the prompt now says every command in `oracles_passed` passed on this head's tree, run by jig or reused from a pass on a commit with the same tree.
- **Failures keep their output, and runs are bounded.** The gate's own runs and publish's revalidation run within the same 30-minute bound as jig's run at green, kill the process tree past it, and carry the end of the output in `GATE_ORACLE_FAILED`.

Publish's revalidation never reuses: it runs only after the target moved, and then the tree under test is new.

**What a reused pass does not cover.** Files git ignores (build outputs, caches, installed dependencies) are outside a commit's tree. A pass in the build lease ran with whatever ignored files the lease had, while the gate's own run used to start from a separate clone without them. A reused pass therefore trusts the build lease's ignored files, as a CI cache trusts its own. Requiring no ignored files at all would rule out reuse for most real repositories.

## Test seams

- **`review.json` as the gate writes it,** through `TestGateReusesAnOraclePassOnTheSameTree`:
  - every run reused, while failing tests are committed at the head, so a rerun would have stopped the round;
  - a partial pass that runs the rest;
  - no reuse for a pass on another tree, under another env class set, of another command, or beside a failed run on the same tree;
  - a failed gate run whose `GATE_ORACLE_FAILED` carries the failing test's output.
- **The review prompt,** through `TestRenderReviewPromptMatchesDesignGolden`.
