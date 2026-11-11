---
name: intake
description: Grills fresh work or a rushed backlog ticket into a brief.md plus slices.yaml, or a chart when the fog spans tickets. Use when work needs shape, a backlog ticket's quality is unknown, or granularity is undecided.
---

# intake

With a ticket, run `jig next <T>` first. If its phase is not brief, tell the person what it needs and its prompt; stop. For fresh work or a ticket that does not exist, mint nothing before the verdict.

Destinations: **brief** (fog fits one ticket) or **chart** (fog spans tickets). The verdict is intake's, not a caller's guess; it can surface mid-grilling. A brief mints the ticket, a chart none.

## Recall, then grill

Before round one run `jig recall <the work, briefly>`. It lists open tickets and edges, naming those near your words (extend or block on one; never mint twice), and each chart's fog, platform documents, shipped answers. A settled question is not asked again. A round asks the whole **frontier** (every question whose prerequisites are settled), numbered, each recommended. Facts are yours (read-only scouts draft sections with real oracles); only decisions go to the person. Stop when the frontier is empty. Tag every decision user-confirmed or defaulted.

## Three shapes

| Shape | Digs | Opening slice | Gate leans on |
|---|---|---|---|
| feature | behavior, acceptance, out-of-scope, seams | first tracer bullet | spec axis |
| bug | repro, expected-vs-actual, regression window, seams | REPRO slice: oracle red before any fix | the regression test, unchanged |
| refactor | behavior contract, current coverage, seams | GUARDRAIL slice: missing coverage lands green first | guardrails staying green |

A test diet is a refactor: its PR names each removed test's seam test and `go test -cover` before/after.

## Seams

Every brief has a `## Seams` section: **public interfaces** (a CLI command and its output, an exported function, a file or wire contract like `slices.yaml`) and **critical paths** (one line each, a behavior, not a test case). It never lists test cases, asks a test per rule, or asks that a test fail when its rule goes. A critical path gets one end-to-end chain; rule variants are covered at the narrowest seam deciding them.

## Brief output (single ticket)

- Mint it with `jig ticket new --title "<title>"`; use the id and folder it prints, never your own.
- `brief.md`: `## `-sectioned behavioral spec.
- `slices.yaml`: one entry per slice, strings or string lists: `id`, `workspace`, `goal`, `oracle`, `env` (env class; optional), `blocked_by`, `from_brief`. Slices are tracer bullets: narrow, COMPLETE, demoable. A cohesive change is one slice: split only where a part builds, verifies and demos alone; REPRO, GUARDRAIL and prefactoring slices (first) stay separate. A slice proving itself with the whole suite names that manifest oracle (e.g. `test`) so the gate reuses its pass; a narrower one, a literal command. An oracle is sh on every OS. Every slice lists `Seams` in `from_brief`.
- `from_brief` hashes: write `slices.yaml`, run `jig validate <T> --check` (hashes; no approval), paste them, re-run until `valid: yes`. Later slices: `jig slice add`; after a brief edit, `jig validate <T> --restamp`, or `jig requeue <T> --from-brief-diff` first to rebuild.

## Chart output

Read `jig skill intake chart-verdict` when the verdict is a chart, or after a chart verdict ended the run.

## Done when

The person confirmed the brief, `jig validate <T>` prints `valid: yes`, and every decision carries its provenance mark.

Without `--check`, `valid: yes` records the approval `jig claim --unattended` requires (`provenance: agent` from your session, `person` at a terminal). Run it once confirmed: show the brief and ask, even if it restates the prompt. A prompt to go with your recommendations answers the round with them (each `defaulted`) and confirms for the person; otherwise ask and wait, even in a start-to-finish run. Editing `brief.md` withdraws the approval: validate again. Read its `advisories`: slices no `blocked_by` edge connects may be separate tickets; a goal near shipped work may be done.

## Then

Relay `jig next <T>`'s `prompt:` and stop, unless the prompt named later phases (a start-to-finish run): then go on to the next skill (build, review, publish).
