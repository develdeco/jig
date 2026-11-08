# A delta review round reads only its delta (GATE-17)

A round 2 over one changed file still printed round 1's whole reading: recordings, every hunk, every probe. GATE-7's delta rounds cost 75-90k tokens against a target of about 40k.

## What was chosen

- **The delta context.** For a round whose scope is delta, `jig review context` prints a `delta` block (the diff `base..head`; over 400 lines the line count and a `diff_path` beside the request), an `answers` table (open or asked findings, and noted findings a person decided), a `read` line, `must_review`, `oracles_passed` and `dismissed`. It drops `recordings`, the intent excerpt unless the intent is `none`, the `hunks` count, the `open` table, unprobed rows outside the delta, and current caught probes (one `probed_current_caught` count). A full round prints what it did. `review.json` is not trimmed, except that each open finding now carries its `status`, which `answers` needs to tell a noted finding from an open one whose label says note.
- **The rule lives in jig's output.** The review skill gains one sentence pointing at the `read` line; it stays under 6000 bytes.
- **Call sites** are found by the reviewer with `git grep`; jig computes no call graph. Recorded and shown, never refused: a reviewer that opens more is not refused.
- **Multi-repo.** A round is a delta round when every repo is; each repo has its own diff.

## Cost, not measured here

Nothing was run against a real ticket. The one-changed-file test fixture round 2 context is 2233 bytes before and 2832 after: the fixture holds no recordings and a one-line change, so the diff, answers and read line add more than the dropped tables remove. The saving appears where round 1 reads many recordings, hunks and probes, which the fixture lacks. Tables dropped: recordings, hunks count, open (folded into answers), unprobed rows outside the delta, current caught probes (one count line). Target: under about 40k tokens; the GATE-7 baseline is 75-90k.

Measurement due: on the first real ticket whose gate opens a delta round of one changed file, record in that ticket's build-log entry the reviewer's tokens (from the subagent's usage), the round number, the changed-file count and whether the reviewer opened any file the diff did not show.
