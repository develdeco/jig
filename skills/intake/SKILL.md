---
name: intake
description: Runs the grilling round that turns fresh work or a rushed backlog ticket into a brief.md plus slices.yaml, or into a chart when the fog spans more than one ticket. Use when fresh work needs shape, a backlog ticket's quality is unknown, or granularity is undecided.
---

# intake

With a ticket, run `jig next <T>` first. If the phase it names is not brief, tell the person what the ticket needs and the prompt that does it, and stop. For fresh work, or a named ticket that does not exist, mint nothing before the verdict.

One skill, two destinations: **brief** (fog fits one ticket) or **chart** (fog spans tickets). Which one is intake's verdict, not a caller's guess; it can surface mid-grilling. A brief verdict mints the ticket, a chart verdict none.

## Recall first

Before the first round run `jig recall <the work in a few words>`. It lists open tickets and their edges, names those near your words (extend or block on one; never mint the same work twice), and brings each chart's fog, the platform documents and the person's answers on shipped tickets. A question the ledger settled is not asked again.

## Grilling round

A round asks the whole **frontier** - every question whose prerequisites are settled - numbered, each with a recommended answer. Facts are your job: read-only scouts draft per-workspace sections with real oracles; only decisions go to the human. Stop when the frontier is empty. Tag every decision user-confirmed or defaulted, never silent.

## Three shapes

| Shape | Digs | Opening slice | Gate leans on |
|---|---|---|---|
| feature | behavior, acceptance criteria, out-of-scope, seams | first tracer bullet | spec axis |
| bug | repro, expected-vs-actual, regression window, seams | REPRO slice: oracle fails red before any fix | the regression test, unchanged |
| refactor | behavior-preservation contract, current coverage, seams | GUARDRAIL slice: missing coverage lands green first | guardrail oracles staying green |

A test diet is a refactor: its PR names the seam test for each test it removes, with `go test -cover` before and after.

## Seams

Every brief lists a `## Seams` section naming the public interfaces and critical paths tested:
- **public interfaces:** a CLI command and its output, an exported function, a file or wire contract like `review.json` or `slices.yaml`;
- **critical paths:** one line each, a behavior rather than a test case.

The section never lists test cases, asks for a test per rule, or asks that a test fail when its rule is removed. A critical path gets one end-to-end chain through its public seams; its rule variants are covered at the narrowest seam that decides them, not by more chains.
## Brief output (single ticket)

- Mint the ticket: `jig ticket new --title "<title>"`; use the id and folder it prints, never make a folder yourself.
- `brief.md`: `## `-sectioned behavioral spec, its decisions tagged.
- `slices.yaml`: one entry per slice - `id`, `workspace`, `goal`, `oracle`, `env`, `blocked_by`, `from_brief`. Slices are tracer bullets: narrow but a COMPLETE path, demoable alone. One cohesive change is one slice: split only where a part builds, verifies and demos on its own; the REPRO, GUARDRAIL and prefactoring slices (first) stay their own. A slice that proves itself with the repo's whole suite names that manifest oracle (such as `test`), so the gate can reuse its pass; a literal command is for a narrower proof. Every slice lists `Seams` in `from_brief`.
- `from_brief` hashes come from `jig validate <ticket> --check`, which approves nothing and prints a heading/sha256 table of `brief.md`'s sections: write `slices.yaml`, run it, paste the hashes into `from_brief`, re-run until `valid: yes`.

  Every field is a string or a list of strings; leave `env` (a manifest env class) out when none is needed:

  ```yaml
  slices:
    - id: round
      workspace: cart
      goal: Take the discount once on the order total.
      oracle: test
      blocked_by: []
      from_brief: [<sha256 of its section>, <sha256 of Seams>]
  ```

## Chart output (fog spans tickets)

A chart is `charts/<name>/map.md` (jig reads its sections, never writes it), sectioned Destination · Notes · Decisions so far · Not yet specified (fog) · Out of scope, plus `charts/<name>/tickets.yaml`. Decision tickets: one per grilling session, entered into `tickets.yaml` as they resolve, each with a `key:` naming the store key it mints under (omit it only when the store declares one). `jig graduate <name>` creates the entries without ids and writes the ids back, so re-running it creates just the new ones.

## Done when

The person has confirmed the brief, `jig validate <ticket>` prints `valid: yes`, and every decision carries its provenance mark.

Without `--check`, `valid: yes` also records the approval `jig claim --unattended` requires: `provenance: agent` from your session, `person` at a terminal. Run it once the person confirmed the brief: show it and ask, even when it only restates the prompt. A prompt telling you to go with your recommendations answers the round with them (each `defaulted`) and confirms for the person; otherwise ask and wait, in a start-to-finish run too. Editing `brief.md` afterwards withdraws the approval, so validate again. Read its `advisories`: slices no `blocked_by` edge connects may be separate tickets; a goal near shipped work may be done already.

## Then

Relay `jig next <T>`'s `prompt:` to the person and stop, unless the person's prompt named later phases (a start-to-finish run): then go on to the next phase's skill (build, review, publish).

A chart verdict ends a start-to-finish run (a chart is not a ticket): say why, report what graduated and what `jig next <chart>` says is ready or waiting on a merge, and ask whether to take the chart's tickets on in this session (recommend: brief them all now, then build the reachable ones to PR; `jig skill chart`). Going with your recommendations, or unattended, only reports.
