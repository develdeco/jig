# jig's testing method, and what a project owns

jig shapes the tests of every repo it builds through its intake skill, which writes the briefs, its build and review prompts, and its own oracle runs. Each piece has its own ADR. Two principles the owner settled on 2026-10-05 had no home, and neither did the question of where testing knowledge lives.

## The method

The same for any repo, each item pointing at the decision that implements it:
- **Tests at agreed seams** (ADR 0016): a brief names its public interfaces and critical paths, never test cases, and the reviewer holds tests to those seams.
- **One end-to-end chain per critical path.** A critical path gets one test that runs it end to end through its public seams; its rule variants are covered at the narrowest public seam that decides them (a renderer, a parser, `ApplyRound`), not by another full chain. These are ADR 0016's seams: nothing finer.
- **Builders test narrowly** (ADR 0018): while working, a builder runs the tests that cover its change.
- **jig runs the oracle** (ADR 0020) at a builder's green, and **the gate reuses that pass** on the same tree (ADR 0021): the full suite runs where its result is evidence.
- **The review reads** (ADR 0017): the gate's oracles are the test evidence, and the reviewer runs no tests.
- **A test diet is a refactor.** Its guardrails are the seams. Its PR names, for every test it deletes or merges, the seam test that covers the behavior, or says why no seam needs it, and reports `go test -cover` before and after; coverage is reported, never gated. The list belongs in the PR, never in a brief's Seams section.
- **Demos come from the build's end-to-end scenarios** (ADR 0029), decided 2026-10-05; ADR 0014's gate demo session is retired.

The intake skill carries the chain and diet principles into every brief, sizes slices so one cohesive change is one slice (the REPRO, GUARDRAIL and prefactoring slices stay their own), and has a slice that proves itself with the repo's whole suite name that manifest oracle rather than write its command out, so it gets the repo's declared command and the gate can reuse its pass. The oracle's name is the project's fact: whatever `.claude/jig.yaml` declares, or the detected default, such as `test` for a Go repo.

## Three layers

Testing knowledge lives in three places, and each fact has one owner:
- **jig's method**, the same for any repo: the skills, the prompts and jig's contract ADRs. Example: the intake skill.
- **The project's facts**, declared by the project and read by jig, never imposed: its oracles and invariant paths (`.claude/jig.yaml`) and its conventions (CLAUDE.md, AGENTS.md, CONTRIBUTING). Example: jig's own CONTRIBUTING rule that no test in `internal/` edits process-global state, from BS-1, and jig's own `test` oracle with its longer timeout.
- **A run's choices**, such as how one diet is cut: they live in that run's plan and briefs. Example: the build-speed run's verifydeliver-first diet.

## No greenfield mode

jig has no separate mode for repos it built itself. It applies one method everywhere, reads what a project declares, and still works where the project declares nothing: its enhancements are ideal, never required. A brief without a Seams section still validates, and a repo with no `.claude/jig.yaml` gets the detected oracles.

This ADR changes no earlier ADR's text; each keeps its own decision.
