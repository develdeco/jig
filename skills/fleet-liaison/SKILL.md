---
name: fleet-liaison
description: Runs many tickets in parallel by spawning one jig solve per ticket in its own terminal session. Use when the backlog needs working today, a fleet's status needs aggregating across a store, or a parked session's question needs relaying.
---

# fleet-liaison

Many tickets, many sessions - one liaison.

## What it does

- Spawns one `jig solve` per ticket, each in its own terminal session.
- Aggregates progress with `jig status`, run per store.
- Relays a parked session's question back to it with `jig run --answer <qid> <text>`.

## What it never does

Never reads code. Status and questions only - the code review lives in `jig gate`, not here.

Fleet binary verbs (a single command over the whole fleet) are deferred: v0.2. Until then, this skill IS the fleet - one `jig solve` session per ticket, driven by hand.

## Done when

Every dispatched ticket is either green in `jig status`, parked on a relayed question, or explicitly left for the next round.
