---
name: intake
description: Runs the grilling round that turns fresh work or a rushed backlog ticket into a brief.md plus slices.yaml, or into an epics/ chart when the fog spans more than one ticket. Use when fresh work needs shape, a backlog ticket's quality is unknown, or granularity is undecided.
---

# intake

One skill, two destinations: **brief** (fog fits one ticket) or **chart** (fog spans tickets). Which one is intake's verdict, not a caller's guess - it can surface mid-grilling.

## Grilling round

A round asks the whole **frontier** - every question whose prerequisites are settled - numbered, each carrying a recommended answer. Facts are your job: dispatch read-only, repo-rooted scouts to draft per-workspace sections naming real oracles; only decisions go to the human. Structural rounds (slice map, competing proposals) render as markdown tables. Stop when the frontier is empty. Record every decision as user-confirmed or defaulted - never silent.

## Three shapes

| Shape | Digs | Opening slice | Gate leans on |
|---|---|---|---|
| feature | behavior, acceptance criteria, out-of-scope, seams | first tracer bullet | spec axis |
| bug | repro, expected-vs-actual, regression window | REPRO slice: oracle fails red before any fix | the regression test, unchanged |
| refactor | behavior-preservation contract, current coverage | GUARDRAIL slice: missing coverage lands green first | guardrail oracles staying green |

## Brief output (single ticket)

- `brief.md`: `## `-sectioned behavioral spec; every decision tagged user-confirmed or defaulted.
- `slices.yaml`: one entry per slice - `id`, `workspace`, `goal`, `oracle`, `env`, `blocked_by`, `from_brief`. Slices are tracer bullets: narrow but a COMPLETE path, demoable alone, sized to one session. Prefactoring is its own slice, first. `blocked_by` encodes the blocking edges between slices.
- `from_brief` hashes come from `jig validate <ticket>`, which prints a heading/sha256 table over `brief.md`'s sections: write `slices.yaml`, run validate, paste the printed hashes into `from_brief`, re-run until it prints `valid: yes`.

## Chart output (fog spans tickets)

`epics/<name>/map.md`, sectioned Destination · Notes · Decisions so far · Not yet specified (fog) · Out of scope. Decision tickets: one per grilling session. Graduation: a resolved fog entry mints a real ticket via `jig ticket new` - the map decides, it never builds.

## Done when

`jig validate <ticket>` prints `valid: yes`, AND every decision recorded in `brief.md` (or a chart's map.md) carries its provenance mark.
