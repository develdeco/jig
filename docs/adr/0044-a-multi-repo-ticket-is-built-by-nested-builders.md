# A multi-repo ticket is built by nested builders

[ADR 0037](0037-a-chart-is-run-from-the-main-session.md) set one build subagent per ticket. A ticket whose ready slices span repos was built by that one subagent, one repo after the other, although `jig next <T>` lists the ready slices by repo and says the repos can build in parallel.

The open question was whether a subagent can start a subagent of its own and still be told apart by the hooks. The hold at a stop and the holder of a claim both read an agent's id from the ledger line of the call that made it. Claude Code's documentation says a tool event carries the id of the subagent it fires in, and does not speak of nesting.

## Decision

**A ticket subagent may start one nested builder per repo.** With ready slices in several repos, the ticket subagent starts a repo builder for each repo but one and builds that repo itself. A repo builder is a subagent of the ticket subagent. It builds its repo's slices with the build skill and claims with `--unattended`, so its claim holds it, and only it, at its stop. It builds only its repo's slices, opens no gate and starts no builder: the gate and the reviewer stay the ticket subagent's, and a hold that tells a builder to open the gate is the ticket subagent's to act on, so the builder hands back. The ticket subagent starts its builders in the background and waits for them before the gate.

**The hold is per agent.** The ticket subagent and its builders claim slices of one ticket at once, all in one session, so each agent is held by its own latest claim not released since, and a release clears only the claim of its slice. The holder of a claim is read among the ledger lines of the claim's own slice, since the agents claim at the same moment. A latest claim whose holder jig cannot tell still holds the whole session. The first version kept one claim per session, so only the latest claimer was held; the review of #213 found it (F1), and `TestSubagentStopHoldsEachClaimHolder` covers two claimers at once.

**Questions go up one level at a time.** A builder's question comes to the ticket subagent, which ends its turn with the question, the builder's id and its default. The main session relays the person's answer, resumes the ticket subagent, and the ticket subagent resumes the builder. The main session never talks to a builder.

**The nesting was proven on the real CLI first.** `TestHooksLiveNested` (`e2e/hooks_live_test.go`) runs `claude` against the scripted Messages API: the main session starts a ticket subagent, which starts a repo builder. The builder claims a slice with `--unattended` and commits it. The test asserts that the builder's tool lines carry an agent id that is neither empty nor the ticket subagent's, that the same id is on every line the builder makes, that a `stop-block` line names the builder, and that none names the ticket subagent or the main session. The project has no unattended marker, so a hold is the claim's. It passed on Claude Code 2.1.232. Had the id been the parent's, the ticket would have stopped there and asked: the whole session would be the claim's holder, as on a harness with no agent ids.

**With no Agent tool the ticket subagent builds its repos in turn.** A harness that offers no nesting leaves the ticket subagent alone. It builds each repo in turn, ends at green with no review round open, and hands back. The main session then runs `jig gate <T>` and starts the reviewer itself.

**The help says one builder per repo.** `jig next <T>` says "N repos have ready slices (a, b): one builder per repo can build them in parallel". The line that the slices of one repo build in order is unchanged.

## Rejected alternatives

- **The main session starts the builders.** It would hold one subagent for each repo of each ticket and relay every question of every builder: the person would talk to more agents, and the ticket's own subagent, which knows the ticket, would have nothing to do.
- **jig starts the builders.** jig starts no coding session (ADR 0037).
- **A per-repo gate round.** Review is still one round for the ticket, by the ticket subagent's reviewer. Per-repo rounds are a separate decision.

## Consequences

- Amends [ADR 0037](0037-a-chart-is-run-from-the-main-session.md): the ticket subagent may start nested builders, and a hand-back at green with no round open (no Agent tool) is reviewed from the main session.
- Nothing in `internal/observe`, `internal/ledger` or the hooks changes: the ledger contract (a tool line's agent is the id of the agent whose call it was) held for a nested agent too, and a test now says so.
- The `chart` skill gains the nesting rule and stays under its budget. `CONTEXT.md` gains *Repo builder*; `ARCHITECTURE.md` says a nested agent's id is its own.
- A reviewer nested under the ticket subagent, spend and notes per nested agent, and overlap edges between tickets are separate decisions that rest on the same id.
