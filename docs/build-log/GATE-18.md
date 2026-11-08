# A Markdown-only delta reuses the whole-suite pass (GATE-18)

A notes or docs slice changes no code, yet its oracle was the 13-minute whole suite, run again at the slice's commit and again at the gate.

## What was chosen

- **The rule is the manifest's, the mechanism is jig's.** `.claude/jig.yaml` gains `light` with `paths` (`*` stops at `/`, `**` crosses it) and `oracle`. A delta is light when it has a file and every changed file matches (`git diff --no-renames --name-only`, so a deleted or renamed file counts by both paths). A manifest with no `light` key never reuses.
- **jig's own rule.** Paths `*.md`, `.github/*.md`, `docs/**/*.md`; oracle `go test -count=1 ./lint ./internal/screen`. A `.md` under `skills/` or `testdata/`, and any non-Markdown file under `docs/`, is not light.
- **Verify.** After a pass of the slice's resolved command and env on an ancestor commit, with no failed full run of that command since, a light delta runs the light oracle in place of the slice's. The usual `oracle` line is journaled with the slice's command plus `reused_from` (the origin full run, transitive) and `ran` (the light command). No earlier pass, a non-light file, a failed full run since, or no `light` key runs the slice's oracle as before.
- **Gate.** `reusablePasses` also accepts a pass whose commit differs from HEAD only in light paths, and reports the origin as `reused_from` and the light command as `ran`. The gate runs the light oracle on HEAD itself when the pass it takes is of another tree, so a head is never admitted unrun. The env, failed-run and clean-commit rules of ADR 0021 are unchanged.
- **Shown.** `jig verify` and `jig here` print `pass: lint only, reusing <sha>`; `jig status` has a `light` table of such slices; `oracles_passed` has a `ran` column; the pull request body's oracle line says the whole-suite pass was reused and the light oracle ran.

## Table of readers

| Files | Read by | Light |
| --- | --- | --- |
| `*.md` at the root, `.github/*.md`, `docs/**/*.md` | `./lint` (build log shape, ARCHITECTURE table), `internal/screen` (ARCHITECTURE.md) | yes |
| `skills/**/*.md` | embedded in the binary, capped by the lint | no |
| `testdata/**/*.md` | test inputs | no |
| non-Markdown files under `docs/` | CI's wider `docs/*` rule | no |

## Judgment calls

- A failed light run does not stop a later light delta from reusing the pass (it says nothing of the suite); a failed full run of the command does, on whatever commit it ran.
- A delta with no changed file is not light: nothing proves it safe.

## Cost, not measured here

Expected saving: about 13 minutes of suite per docs or notes slice, at the slice and again at the gate. No git counts are committed. The measurement is due on the next real ticket with a notes slice.

## Round 1 fixes

- `LastOracleSeconds` skips a light line, so `jig context`'s `oracle_seconds` stays the suite's; `oracles_passed` gives a light line's pass the origin run's seconds, not the lint's.
- The publish body takes no per-commit diff when the manifest declares no light rule, and a diff once for each commit otherwise (the gate caches the same way).
- Tests: the gate refusing a head whose light oracle fails, a failed full run since the pass (a rerun of the passing commit, so the next Markdown delta is light from it), a pass of another command, and a deleted non-light file at verify and at the gate.

## `jig here` timing

`jig here` now reads a light pass's state. Measured on the real store from the PUB-11 worktree, four alternating runs each, on a loaded machine: the installed jig 446 to 570 ms (median about 535); this branch's binary 333 to 426 ms (median about 336). The branch adds no measurable cost, so nothing was removed.
