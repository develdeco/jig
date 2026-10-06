# Builders open on Sonnet, and climb on failure

The staircase chose every build attempt's model from three rungs (Haiku 4.5, Sonnet 5, Opus 5). Every attempt opened on Haiku. It climbed one rung when the ticket's cumulative lease diff passed 400 lines or 10 files, and floored to Opus when a changed file matched a declared invariant path. A failed attempt did not climb: a retry ran on the same model that had just failed.

The first ticket measured on this (BS-1, build-speed run, 2026-10-06) paid for that order:
- Haiku built the first slices, and the gate's first round found three high-risk defects in them: a lint that matched nothing, an exempted package, and a requirement never implemented.
- One Haiku attempt failed, and its retry ran on Haiku again.
- The ticket needed three fix rounds, about 3.5M weighted tokens and 95 minutes of rework.
- The Sonnet fix sessions, by contrast, tested narrowly and set timeouts that fit.

no-mistakes puts the stronger tier on the first pass and keeps model choice the user's.

## The rule

- **The default rungs are Sonnet, then Opus.** A project that wants a cheaper opening rung lists its own rungs in `project.yaml`'s `staircase`, Haiku first if it likes. Model choice stays the project owner's, and inspectable.
- **A failed attempt climbs one rung.** A dispatch opens on the first rung and climbs one rung for each earlier attempt of the same slice that failed at the work, clamped to the last rung.
  - An attempt failed at the work when its journaled result is code-bug, oracle-wrong or failed, or a green that did not verify.
  - A question (needs-input), a flawed brief or a blocked environment is not the builder failing, so it does not climb.
  - The count comes from the ticket's journal (`journal.FailedAttempts`), since slice state counts every attempt.
- **An invariant match still floors to the dearest rung.**
- **The volume rule is gone.** The cumulative lease diff measures the ticket so far, not the slice in hand. With Sonnet opening, it would have pushed most later slices of a multi-slice ticket to Opus, BS-1's by its second slice. Escalation now follows outcomes, not size thresholds.

The climb carries across requeues: a requeued slice resumes on the rung its earlier failures earned. An attempt that produced nothing jig could read counts as a failure too: a backend error, or a session that wrote no result, is journaled as failed.

The gate reviewer's model rule is unchanged here: the first rung no builder of the ticket used (`Disjoint`), or the dearest rung when the builders used them all. With two default rungs that gives an Opus reviewer once a builder ran on Sonnet only, but an Opus reviewer over Opus builders once a slice failed, and a Sonnet reviewer when an invariant floored every builder to Opus. The owner has decided the reviewer runs on the dearest rung every round, with independence coming from its fresh read-only session; that change belongs to the reviewer work that follows (build-speed item 6i). Superseded for the reviewer by [ADR 0023](0023-the-reviewer-runs-on-the-dearest-rung-and-builds-and-reviews-get-an-effort.md).

## Test seams

- **`staircase.Select`** and **`staircase.Default`**, through their table tests.
- **`journal.FailedAttempts`**, through `TestFailedAttempts`.
- **Frontier's dispatch,** through `TestRunClimbsARungPerFailedAttemptButNotForAQuestion`: a slice failing three times runs on three successive rungs, and a slice resumed after its question is answered stays on its first rung. The journal's dispatch lines are the evidence.
