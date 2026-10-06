# The reviewer confirms an unchanged finding by id

A gate reviewer reports every problem in the files it reviews, including those already listed under `open`, and wrote each one in full: file, line, title, detail, action, risk, risk_rationale, oracle and prior. On a delta round most of those are unchanged. In BS-1's third round the reviewer rewrote all 15 carried low notes to add 3 new findings, and writing `result.json` alone was about 13k of the session's 40k output tokens, about 2 of its 9.2 minutes.

## The contract

`result.json` gains a required list, `still_present`, which may be empty. Each entry names an id listed under `open` or `dismissed` in `review.json`, and the line where that problem now is:

    "still_present": [{"prior": "r1-f10", "line": 52}]

- An entry means the same problem, in the same file, with the same title, detail, action, risk, risk_rationale and oracle as the earlier finding; only the line may have moved.
- A finding whose substance changed is still reported in full under `findings`, with `prior`.
- An earlier finding listed in neither place is resolved, as before.
- The round is `REVIEW_INVALID` when an entry's id names nothing under `open` or `dismissed`, appears twice, or is also a finding's `prior`, or when the earlier finding's file is neither present at head nor deleted in the scope diff. An entry also needs a non-empty id and a non-negative line, with exactly the keys `prior` and `line`.

The review prompt's reporting line says to list each unchanged earlier finding in `still_present` by id and current line, and to write in full only new findings and changed ones. The result schema line shows `still_present`.

## Expansion

Before a round is folded (`ApplyRound`), `ExpandStillPresent` turns each entry into a `ResultFinding` with the earlier finding's fields, the entry's line, and `prior` set to its id. Recurrence counting, routing, triage, the fix budget and revieweval's scoring therefore see exactly what a full re-report gives them. The reviewer's raw `result.json` stays on disk as written. The gate and revieweval's runner both expand at the same point.

## Test seams

- `ParseReviewResult` and the review-invalid rules, through `TestParseReviewResultStillPresentRules` and `TestValidateReviewResultStillPresentRules`.
- A gate round as `Gate` runs it, through `TestGateFoldsAStillPresentEntryLikeAFullReport`: a confirmed fix finding folds with its id, earlier text, new line, one recurrence and a new fix slice.
- revieweval's scoring, through `TestStillPresentScoresLikeAFullReport`: a corpus case whose second round uses the short form scores a clean pass, as the full re-report does.
- The review prompt, through `TestRenderReviewPromptMatchesDesignGolden`.
