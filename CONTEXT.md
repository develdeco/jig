# Jig

Vocabulary for jig, a CLI that runs a ticket from brief to merged PR as a dispatch loop over small, provable units of work.

## Language

**Project**:
A declared set of repos plus a tracker plus platform docs. Its identity is the truth repo's `project.yaml` - never inferred from folder layout or the current working directory.
_Avoid_: workspace config, repo group

**Truth repo (store)**:
A dedicated git repo with a remote holding every ticket artifact; it is the source of truth, and the tracker only projects it outward.
_Avoid_: database, state dir

**Intake**:
The shared opening phase of a brief and a chart, run together because both need the same first read of the work. It delivers the granularity verdict: whether the request is one ticket or a chart of several.
_Avoid_: onboarding

**Shape**:
A ticket's intent, one of feature, bug, or refactor.
_Avoid_: type, category, kind

**Design facet**:
A mock any shape can carry, with a fidelity per state per region: exact, adapt, or hybrid.
_Avoid_: mockup spec, UI plan

**Slice**:
A tracer-bullet unit of work with a named oracle and blocking edges to other slices. It is the unit of dispatch, resume, and commit.
_Avoid_: task, subtask, step

**Oracle**:
The command that proves a slice done. Green is done - there is no separate test stage beyond the oracle.
_Avoid_: test suite, check

**Workspace**:
A slice's target: a repo, or a module inside one.
_Avoid_: target dir, project root

**Env class**:
A repo-defined environment an oracle needs, with an up/check/down lifecycle and a stated unavailability policy.
_Avoid_: environment, test env

**Frontier**:
The set of slices whose blockers are all green, and so are eligible for dispatch now.
_Avoid_: ready queue, backlog

**Scout**:
A read-only session rooted in a repo, drafting brief sections for the parent intake.
_Avoid_: research agent, explorer

**Journal**:
The append-only record of every dispatch, outcome, and commit. Changelogs render from it; they are never authored by hand.
_Avoid_: log file, history

**Receipt**:
Evidence that a check passed, stored in the ticket's `evidence/`.
_Avoid_: screenshot, proof

**Finding class**:
A gate finding's mechanical/intent split. Mechanical (typo, dead code, doc gap, formatting) bundles into one fix slice per workspace, pinned to the cheapest staircase rung; intent (behavior, correctness, security, design) becomes its own fix slice at the gate's model.
_Avoid_: severity, priority

**Fix slice**:
A gate finding turned into a new frontier item. Review has no back-edges - every finding becomes forward work.
_Avoid_: review comment, follow-up task

**Lease**:
A pooled worktree checked out for one phase's work and returned warm when that phase ends.
_Avoid_: checkout, sandbox

**Gate**:
Out-of-band verification of a ticket's branch, run on its own lease, under a different model, with fresh context.
_Avoid_: code review, QA pass

**Triage**:
The human seam after a gate round raises findings: keep or dismiss each one, before any fix slice is synthesized. A dismissal is permanent - the reviewer auto-dismisses a finding whose title later resurfaces.
_Avoid_: review, approval

**Staircase**:
The per-dispatch model ladder: cheapest model first, climbing with volume, with invariants floored to the dearest model.
_Avoid_: model tiering, escalation policy

**Stall**:
The same failure signature recurring twice. It marks a misconception, not a capability shortfall, and calls for a different approach rather than another attempt.
_Avoid_: retry loop, flaky failure

**Router**:
A thin skill that classifies intent and hands off. It never judges granularity itself.
_Avoid_: dispatcher, entry skill

**Chart**:
The wayfinder layer for work spanning tickets: a map plus decision tickets. The map decides what to build; it never builds anything itself.
_Avoid_: roadmap, epic

**Ledger**:
One store entry per ticket, ending in an Answers recall tail.
_Avoid_: index, registry
