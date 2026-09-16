---
name: retro
description: Mines journal.ndjson across tickets and machine-local transcripts for a repeated failure signature, then proposes a skill or rule edit. Use when the same mistake recurs, a process defect needs verbatim evidence, or a proposed fix needs human sign-off before it lands.
---

# retro

Finds the repeated mistake, proposes the fix, waits for a yes.

## What it does

- Mines every ticket's `journal.ndjson` plus machine-local transcripts for a **repeated failure signature**: the same defect, worded differently, across sessions.
- Requires the signature at least twice, verbatim, before proposing anything - one occurrence is an incident, not a pattern.
- Proposes concrete edits to a jig skill or a repo rule, with the ≥2-run evidence attached.

## Apply is human-gated

Always present the proposed edit and its evidence for approval before it lands. Retro never applies its own proposal - that step is the human's, every time.

The `retro` binary verb (running this mining as one command) is deferred: v0.2. Until then, this skill performs the mining by hand.

## Done when

Every proposal in front of the human carries its ≥2-run verbatim evidence, and no edit has landed without an explicit yes.
