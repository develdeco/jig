# A clean merge of the target costs no review round (GATE-16)

A branch that only merged the target into the head a reviewer read opened a whole new round for a reviewer to close with nothing to read. The merge-line rule (`classifyLine`, cited as ADR 0038) already told such a head from one with new code; the gate did not use it to skip the round.

## What was chosen

- **A carried-over round.** `jig gate` runs the oracles as always. When the head only merges the target into the head a reviewer read (every repo of the round), it records the round itself: a clean round with `carried_from: N` in `report.yaml`, a `gate-clean` journal line with `carried: N`, and a `findings.md` that says whose verdict it carries. No round is left open and no reviewer is started. `N` is the round that was really reviewed, so a chain of carried rounds all name the one. The round has no model, effort, session or agent; its report copies round N's reviewer and independence and adds no new claim (ADR 0046). The launched path's empty round records the same two fields.
- **D9 amends ADR 0038.** A merge counts as git's own only when its second parent is reachable from the target ref (`origin/<target>` for the gate; for publish, the branch the pull request goes into, which is the stacked parent's branch for a stacked ticket). A clean merge of any other branch is `lineOther`: a reviewer reads what it brought, and publish refuses the head with `PUBLISH_UNREVIEWED_HEAD`. This holds for a hand-resolved tip as well, since its delta would otherwise hide the whole unrelated branch. The amendment lives here; there is no new ADR.
- **Only a merge is carried.** The gate run again on the very head a reviewer read still opens a round, as before: there is nothing new to carry.
- **A person's decision waits for a reader.** A triage decision not yet applied (pending since the last closed round) is applied by the next round that closes, and a carried round applies nothing, so such a gate opens a round as before.
- **A hand-resolved hunk** is told from a hunk of the target by tree, unchanged: the round reads the resolution alone.
- **PUB-2 is not touched.** The carried round records the head the lease holds; publish's reconcile is unchanged.

## Left out on purpose

- D3: a ticket commits no git counts (DEV-10), so the mechanical-path clause of map decision 3 (a delta touching only the budget file is recorded, not reviewed) is not built.
- A git older than 2.38 keeps `GIT_TOO_OLD` for a head that only merges lie past the reviewed head.
