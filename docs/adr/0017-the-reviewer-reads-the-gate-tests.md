# The reviewer reads; the gate's oracles test

A gate round runs every manifest oracle on the lease head, and stops before any reviewer if one fails. The reviewer was never told that. It ran the tests again inside the review: in one measured ticket, about 12 minutes of each 22 to 26 minute review round went to `go test` on a head the gate's oracles had just passed.

no-mistakes splits the same work the same way this ADR does: its review step is a static read of the diff, "Review does not execute tests", and a separate test step produces the evidence.

## The contract

`review.json` gains `oracles_passed`: one entry per oracle run the gate made on this head before the review, with the oracle's name, its workspace and the command, in the order the gate ran them (workspaces in manifest order, oracles by name). The gate stops at the first failure, so every entry passed and no entry needs a result field. `oracles` still lists the manifest oracle names a finding's `oracle` may cite.

`reviewPromptTemplate` (`internal/verifydeliver/review.go`) gains one line after the read-only sentence:

> jig ran every command in review.json's oracles_passed on this head before this review, and each passed. Review by reading: run no tests.

It states the job's scope, as "Do not edit files, commit, or push" already does; it coaches nothing. The rule does not depend on the list: a repo with no oracle gets an empty `oracles_passed` and the same line, since producing test evidence is the oracles' job, not the review's.

The review eval (`internal/revieweval`) runs the case repo's oracles before each round, as `Gate` does, and hands its reviewer the runs, so it measures the review a gate reviewer gives.

## What enforces it

Nothing mechanical. The command screen does not refuse test commands: telling a test run from any other command by its text is the denylist pattern ADR 0007 rejects, and the headless backend is not a boundary anyway (ADR 0008). The contract is the statement, and the measurement is the gate session's own tool time, which should show no test runs. If reviewers keep running tests, the next step is a reviewer with no shell, which needs jig to hand it the scope diff as a file.

## Test seams

- **The rendered review prompt,** through `TestRenderReviewPromptMatchesDesignGolden`.
- **`review.json` as the reviewer receives it from `Gate`,** through `TestGateHandsTheReviewerTheOraclesItRan`: the gate's own oracle runs reach the reviewer.

## Amendment: reused passes (ADR 0021)

The gate can reuse a pass jig recorded at a slice's green on a commit with the head's tree, instead of running that oracle again. `oracles_passed` entries then carry `reused_from`, and the prompt line reads:

> Every command in review.json's oracles_passed passed on this head's tree before this review: jig ran it, or reused a pass on a commit with the same tree (reused_from). Review by reading: run no tests.
