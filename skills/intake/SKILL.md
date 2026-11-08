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
- `slices.yaml`: one entry per slice, strings or string lists: `id`, `workspace`, `goal`, `oracle`, `env` (env class; optional), `blocked_by`, `from_brief`. Slices are tracer bullets: narrow, COMPLETE, demoable. A cohesive change is one slice: split only where a part builds, verifies and demos alone; REPRO, GUARDRAIL and prefactoring slices (first) stay separate. A slice proving itself with the whole suite names that manifest oracle (`test`) so the gate reuses its pass; a literal command, a narrower one. An oracle is sh on every OS. Every slice lists `Seams` in `from_brief`.
- `from_brief` hashes: write `slices.yaml`, run `jig validate <ticket> --check` (hashes; no approval), paste them, re-run until `valid: yes`. Later slices: `jig slice add`; after a brief edit, `jig validate <ticket> --restamp`, or `jig requeue --from-brief-diff` to rebuild.

## Chart output

Make a chart with `jig chart new <name> --title "<t>"`, not by hand. Decision tickets: one per grilling session, entered as they resolve, each with a `key:` (its store key; omit if the store declares one). `jig validate <name>` lists every problem and mints nothing; `jig graduate <name>` creates the entries without ids and writes them back, a re-run makes only new ones.

A batch spanning areas takes a chart verdict, ordering its tickets; a standalone ticket takes `jig depend <T> --on <P> [--kind stacked]` when a later one needs order.

## Done when

The person confirmed the brief, `jig validate <ticket>` prints `valid: yes`, and every decision carries its provenance mark.

Without `--check`, `valid: yes` records the approval `jig claim --unattended` requires (`provenance: agent` from your session, `person` at a terminal). Run it once confirmed: show the brief and ask, even if it restates the prompt. A prompt to go with your recommendations answers the round with them (each `defaulted`) and confirms for the person; otherwise ask and wait, even in a start-to-finish run. Editing `brief.md` withdraws the approval: validate again. Read its `advisories`: slices no `blocked_by` edge connects may be separate tickets; a goal near shipped work may be done.

## Then

Relay `jig next <T>`'s `prompt:` and stop, unless the prompt named later phases (a start-to-finish run): then go on to the next skill (build, review, publish).

A chart verdict ends a ticket run: report what graduated and what `jig next <chart>` says, then read the prompt's intent from its words (examples, no phrase list); unsure, ask.

- **No intent** (nothing past the brief: "fix this: ...", "fix this, go with your recommendations"): name the prompts that launch one ticket ("Take <ticket> to PR"), several ("Take A and B to PR") or the chart ("Take <chart> to PR"), stop, ask nothing.
- **Intent, not fully explicit** ("take it to PR", "fix this and open a PR", "take it from brief to PR, go with your recommendations"): ask once to confirm the launch it implied (the chart, or the tickets carrying a named ticket's work), "not now" the other answer; the run goes with your recommendations only if the yes says so; unattended, stop. Why: a ticket's terms ask for a ticket run, which a chart verdict ends.
- **Fully explicit** (recommendations and working to the end, whatever the shape: "go with your recommendations, keep going until it is all done", "fix it end to end with your recommendations, I'll be away"): say why in one line, run `jig skill chart`, ask nothing.

A named ticket whose grilling ends in a chart enters `tickets.yaml` with its own `id`, `title` and `body` scoped to its part; graduate keeps it `existing`.
