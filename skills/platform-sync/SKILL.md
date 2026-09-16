---
name: platform-sync
description: Refreshes the store's platform/ notes — contract-index entries and repo notes — from recently landed tickets. Use after a ticket lands, before a big intake starts, or when platform/ looks stale against the ledger.
---

# platform-sync

Periodic catch-up for `platform/`, not a per-ticket step.

## What it does

- Reads recent `ledger.md` entries and drafts the contract-index entries a landed ticket should have added.
- Reads `project.yaml` and each repo's own manifest to refresh `platform/`'s repo notes.
- Writes both back under `platform/`, alongside whatever `jig publish` already wrote there.

## When to run it

After a batch of tickets lands, and before a big intake starts — so the next grilling round's scouts read a `platform/` that reflects what actually shipped.

## Done when

`platform/contract-index.md` has an entry for every ticket in `ledger.md` since the last sync, and each repo note matches its current `project.yaml` manifest.
