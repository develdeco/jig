---
name: intake
description: Runs the grilling round that turns fresh work or a rushed backlog ticket into a brief.md plus slices.yaml, or into a chart when the fog spans more than one ticket. Use when fresh work needs shape, a backlog ticket's quality is unknown, or granularity is undecided.
---

# intake

One skill, two destinations: **brief** (fog fits one ticket) or **chart** (fog spans tickets). Which one is intake's verdict, not a caller's guess - it can surface mid-grilling.

## Grilling round

A round asks the whole **frontier** - every question whose prerequisites are settled - numbered, each carrying a recommended answer. Facts are your job: dispatch read-only, repo-rooted scouts to draft per-workspace sections naming real oracles; only decisions go to the human. Structural rounds (slice map, competing proposals) render as markdown tables. Stop when the frontier is empty. Record every decision as user-confirmed or defaulted - never silent.

## Three shapes

| Shape | Digs | Opening slice | Gate leans on |
|---|---|---|---|
| feature | behavior, acceptance criteria, out-of-scope, seams | first tracer bullet | spec axis |
| bug | repro, expected-vs-actual, regression window, seams | REPRO slice: oracle fails red before any fix | the regression test, unchanged |
| refactor | behavior-preservation contract, current coverage, seams | GUARDRAIL slice: missing coverage lands green first | guardrail oracles staying green |

A test diet is a refactor: its guardrails are the seams. Its PR names, for every test it deletes or merges, the seam test that covers the behavior, or says why no seam needs it, and reports `go test -cover` before and after; coverage is reported, never gated.

## Seams

Every brief lists a `## Seams` section naming the public interfaces and critical paths tested:
- **public interfaces:** a CLI command and its output, an exported function, a file or wire contract like `review.json` or `slices.yaml`;
- **critical paths:** one line each, each a behavior rather than a test case or a test per rule.

The section never lists test cases, never asks for a test per rule, and never asks that a test fail when its rule is removed. A critical path gets one end-to-end chain through its public seams; its rule variants are covered at the narrowest public seam that decides them (a renderer, a parser), not by more chains. For a bug, the seam is where its REPRO oracle runs; for a refactor, the seams are its guardrails.

## Brief output (single ticket)

- `brief.md`: `## `-sectioned behavioral spec; every decision tagged user-confirmed or defaulted.
- `slices.yaml`: one entry per slice - `id`, `workspace`, `goal`, `oracle`, `env`, `blocked_by`, `from_brief`. Slices are tracer bullets: narrow but a COMPLETE path, demoable alone, sized to one session. One cohesive change is one slice: split only where a part builds, verifies and demos on its own, since every slice is a session that starts cold; the REPRO, GUARDRAIL and prefactoring slices stay their own. Prefactoring is its own slice, first. A slice that proves itself with the repo's whole suite names that manifest oracle (its name in `.claude/jig.yaml`, or the detected default such as `test` for a Go repo), so it gets the repo's declared command and the gate can reuse its pass; a literal command is for a narrower proof. `blocked_by` encodes the blocking edges between slices. Every slice lists `Seams` in `from_brief`, so its builder reads it.
- `from_brief` hashes come from `jig validate <ticket>`, which prints a heading/sha256 table over `brief.md`'s sections: write `slices.yaml`, run validate, paste the printed hashes into `from_brief`, re-run until it prints `valid: yes`.

## Chart output (fog spans tickets)

A chart is `charts/<name>/map.md` (for people; jig never reads it), sectioned Destination · Notes · Decisions so far · Not yet specified (fog) · Out of scope, plus `charts/<name>/tickets.yaml` (the handover jig reads and writes). Decision tickets: one per grilling session, entered into `tickets.yaml` as they resolve. Graduation: `jig graduate <name>` creates the entries without ids and writes the ids back, so re-running it after fog clears creates just the new tickets - the map decides, it never builds.

## Done when

`jig validate <ticket>` prints `valid: yes`, AND every decision recorded in `brief.md` (or a chart's map.md) carries its provenance mark.
