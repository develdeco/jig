# jig's testing method, and what a project owns

jig shapes the tests of every repo it builds through three things: the intake skill, which writes the briefs; the build prompt; and the review prompt. Each piece has its own ADR. Two principles the owner settled on 2026-10-05 had no home, and neither did the question of where testing knowledge lives.

## The method

The same for any repo, each item pointing at the decision that implements it:
- **Tests at agreed seams** (ADR 0016): a brief names its public interfaces and critical paths, never test cases, and the reviewer holds tests to those seams.
- **One end-to-end chain per critical path.** A critical path gets one test that runs it end to end; each rule variant is tested at the narrowest seam that holds it, not by another full chain.
- **Builders test narrowly** (ADR 0018): while working, a builder runs the tests that cover its change.
- **jig runs the oracle** (ADR 0020) at a builder's green, and **the gate reuses that pass** on the same tree (ADR 0021): the full suite runs where its result is evidence, not as a builder's habit.
- **The review reads** (ADR 0017): the gate's oracles are the test evidence, and the reviewer runs no tests.
- **A test diet is a refactor.** Its guardrails are the seams: every test it deletes or merges names the seam test that covers its behavior, or says why no seam needs it. Coverage is reported, not gated.
- **Demos come from the build's end-to-end scenarios**, decided 2026-10-05 and built by a later ticket; ADR 0014's gate demo session stands until then.

The intake skill carries the chain and diet principles into every brief, sizes slices so one cohesive change is one slice, and has a slice prove itself with the repo's own oracle by name (`test`), so it gets the repo's declared command and the gate can reuse its pass.

## Three layers

Testing knowledge lives in three places, and each fact has one owner:
- **jig's method**, the same for any repo: the skills, the prompts and jig's contract ADRs. Example: the intake skill.
- **The project's facts**, declared by the project and read by jig, never imposed: its oracles and invariant paths (`.claude/jig.yaml`) and its conventions (CLAUDE.md, AGENTS.md, CONTRIBUTING). Example: jig's own CONTRIBUTING rule that no test in `internal/` edits process-global state, from BS-1, and jig's own `test` oracle with its longer timeout.
- **A run's choices**, such as how one diet is cut: they live in that run's plan and briefs. Example: the build-speed run's verifydeliver-first diet.

## No greenfield mode

jig has no separate mode for repos it built itself. It applies one method everywhere, reads what a project declares, and still works where the project declares nothing: its enhancements are ideal, never required. A brief without a Seams section still validates; a repo with no oracle still gets a reading review; a repo with no `.claude/jig.yaml` gets the detected oracles; a project without a code graph gets builders without `related` context.

This ADR changes no earlier ADR's text; each keeps its own decision.
