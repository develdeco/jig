# A round's independence is recorded, not enforced

[ADR 0032](0032-jig-attaches-to-the-harness.md) made independence a matter of provenance: jig starts no reviewer, so it judges from the records of who did what whether a round's reviewer built part of the change, and `jig review done` refused a round whose reviewer had (`REVIEW_NOT_INDEPENDENT`, the round recorded `not-independent`), as publish did (`PUBLISH_NOT_INDEPENDENT`). The refusal did not hold. jig reads a reviewer's session from `JIG_SESSION` before the harness's own variable, so a builder that names another session passes, and DECISIONS.md already recorded a builder closing a round only its subagent read as passing. Where it did act, it acted on reviews that had a fresh context: without jig's hooks a subagent runs under its parent's session id and no ledger line names its agent, so GATE-3's subagent reviewer and GATE-4's reproduction were both refused. Keeping the refusal meant a separate headless session per round for every session without hooks (GATE-4, develdeco/jig#198), one harness's machinery for a check a builder could step around. And a review in the builder's context is almost always one a person chose, which a refusal overrides and cannot undo. The owner asked, on 2026-11-04, for the review's independence to be recorded and not enforced.

## Decision

**Independence is a fact of the round, not a condition of it.** A round's verdict follows its findings alone. `independent` (`true`, `false` or `unknown`) stays on `report.yaml` and everywhere the round is shown (the round's output, `jig status`, the TUI's review screen), and the pull request's review notes now open with every reviewer round, its scope and range, and who reviewed it and whether they built part of the change, for whoever merges to weigh: a delta round reads only part of the change, so the last round's reviewer alone would not say what the whole change had for a review. `jig review done` records a builder's round clean and exits 0, with a hint that one of the builders reviewed it; publish checks the verdict, never who gave it.

**`false` only where jig saw it.** A reviewer that matches a builder on its session with no agent is `false` only where the machine ledger has a tool line of that session: a hook reported the session's tool calls, each with the agent that made it, so an agent of "" there is the session's own. With no tool line of the session, the agent is "" for want of a record, a subagent and the session look alike, and the round is `unknown`.

**A builder's round is not what a delta takes as reviewed.** The next round's scope is a delta from the head of the latest earlier round not recorded `false`, or the whole change when there is none, so a reviewer who did not build the change, reviewing the round after a builder's, covers what the builder's review covered. Without it the round after a builder's, at the same head, would review an empty delta and record `independent: true` over code no independent reviewer read. An `unknown` round still anchors a delta: jig cannot tell it from an independent one.

**The advice stays.** `jig gate` and `jig next <T>` still say whether the calling session is fresh, and a session that is not has a subagent review the ticket (decision 39); the gate's help with no session says who should review as advice.

**An older record stays what it was.** A round an older jig recorded `not-independent` is read as it was written: not clean, so publish refuses it as `PUBLISH_NOT_CLEAN`, and the next round clears it. Nothing is migrated.

## Rejected alternatives

- **Keeping the refusal, with a separate session for a session without hooks** (GATE-4, develdeco/jig#198, closed unmerged; its draft was numbered 0043). `jig gate` would have printed an `env -u JIG_SESSION -u CLAUDE_CODE_SESSION_ID claude -p ...` command for such a session to run in the background. It worked in Claude Code, but it kept a check that does not hold and added a reviewer launch written for one harness.
- **Refusing only in an unattended run.** A rule for one case, and the record already reaches the person who merges.
- **An override flag on publish.** A ceremony for exactly the case the check gets wrong.

## Consequences

- Amends ADR 0032: a round the reviewer built part of is recorded with `independent: false`, and publish takes it. `REVIEW_NOT_INDEPENDENT` and `PUBLISH_NOT_INDEPENDENT` are gone; `not-independent` is only an older record's verdict.
- An agent that reviews its own work and publishes gets a pull request whose review notes say so. Nothing stops it before the merge; nothing did before, since the refusal could be stepped around.
- A session without jig's hooks follows the same advice as any other: its subagent's round is `unknown`, and stands.
- `e2e`'s `TestReviewByTheBuilderIsRecorded` proves a builder's own round recorded clean, published and named in the review notes; `internal/verifydeliver`'s tests prove `false` and `unknown` by the session's tool lines.
