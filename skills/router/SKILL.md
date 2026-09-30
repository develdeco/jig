---
name: router
description: Routes a stated intent to the right jig skill or command. Use when the user proposes fresh work, hands over a rushed backlog ticket, shares external work product, asks a status question, wants many tickets worked, or reports a repeated process defect.
---

# router

One table, one rule: match the phrase, deploy the row.

## Intent table

| You say | Intent | What deploys |
|---|---|---|
| "we should remove concept X everywhere" | fresh work → intake | intake grilling breadth-first; fog spans tickets → becomes chart: a map in `charts/` (destination · decisions index · fog-of-war), decision tickets one per session; resolutions graduate through `jig graduate`; the map decides, never builds |
| "make page Y feel faster" | fresh work → intake | same intake; fog fits one ticket → continues as the brief; granularity discovered mid-grilling |
| "solve T-901" (rushed backlog ticket) | ticket, quality unknown | intake evaluates as written: sharpen in place, promote to chart if secretly chart-shaped, or surface as already-covered via ledger recall |
| "here's the contractor's PR" | external work product | `jig gate` pr-mode (v0.2 - parses and reports not-implemented in v0.1) |
| "how's T-1130 going?" | status ask | `jig status` |
| "I hand-wrote a fix on branch X - check it" | validator side only | `jig ticket new`, then `jig gate T --branch X`; `jig run T` builds what the round queues on X, `jig gate T` reviews X again, and `jig publish T` ships X as it is (its commits keep their shas; jig's go on top) |
| "work the backlog today" | many tickets | fleet-liaison skill over parallel `jig solve` sessions (fleet binary verbs v0.2) |
| "it keeps making that same mistake" | process defect | retro skill (retro binary verb v0.2) |
| "have we hit something like this before?" | recall | no machine: the ledger's Answers tails; deciding not to start a tool is also the router's judgment |

## The handback rule

Judgment starts machines; machines hand back only at typed points: needs-input, stall, env gate, push confirm. Between those points, let the dispatched skill or command run.

## Granularity is never routed

Whether a phrase is one ticket or a whole chart is not a table lookup - it is intake's verdict, discovered mid-grilling. Route both "fresh work" rows and the "ticket, quality unknown" row to intake and let it decide brief vs chart.

## Other skills

- **intake** - the brief/chart skill above; the only row that runs a grilling round.
- **fleet-liaison** - many tickets in parallel; status aggregation and question relay, never code.
- **retro** - repeated-mistake mining across journals and transcripts; proposes, never auto-applies.
- **platform-sync** - catches up `platform/` between intakes; run it after tickets land.
