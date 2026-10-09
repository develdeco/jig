# Jig

Vocabulary for jig, a CLI that runs a ticket from brief to merged PR as a dispatch loop over small, provable units of work.

## Language

**Project**:
A declared set of repos plus trackers (mirrors of the store's tickets) plus platform docs. Its identity is the truth repo's `project.yaml` - never inferred from folder layout or the current working directory.
_Avoid_: workspace config, repo group

**Truth repo (store)**:
A dedicated git repo with a remote holding every ticket artifact; it is the source of truth: jig mints every ticket id and claims it here, trackers only mirror the store outward, and pull requests come from the repo host, not a tracker.
_Avoid_: database, state dir

**Ticket**:
jig's record of one unit of work, in the store, under an id jig mints: a
key plus a number. May carry aliases, its earlier ids, kept forever; an id or
alias resolves to the ticket's current id wherever jig takes one.
_Avoid_: issue, which names a tracker's copy of a ticket

**Key**:
The area part of a ticket id (`STORE` in `STORE-3`), declared in
`project.yaml`'s `keys:` with a one-line meaning. A ticket's id never
changes when its ticket's area does - a key only decides what a new
ticket mints under.
_Avoid_: prefix, namespace

**Mirror**:
A tracker's copy of the store, written by jig at every checkpoint and never
read back. Today's one mirror projects every ticket and chart onto a GitHub
issue and a GitHub Project board, from inside the command that made the
checkpoint, best-effort: a GitHub failure warns rather than failing the
command. An edit made on the tracker side of a jig-owned field is drift,
reported and overwritten with the store's value at the next sync.
_Avoid_: tracker as the source of tickets

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
The command that proves a slice done. Green is done - there is no separate test stage beyond the oracle, which jig runs itself when a builder reports green.
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

**Demo**:
What a pull request shows of the change working: its `## Demo` section, a screenshot, a GIF, a video or a terminal capture per recording, composed into flows. It is a by-product of verification, not a session of its own: the build's end-to-end scenarios write recordings, and `jig publish` renders the section from the ones a short session picks (see **Recording** and **Pick**). A pull request whose build recorded nothing, or whose pick is refused, has no demo section. ADR 0014's gate demo session, which dispatched a model after a clean reviewer round to make the media, is retired (ADR 0029).
_Avoid_: receipt (evidence that a check passed), screencast

**Recording**:
What the build's end-to-end scenarios write while they run at a builder's green oracle run: a screenshot, a video or a terminal capture, optionally tagged with its scenario, flow, step and caption. jig gives that run a directory of its own under the jig home's `evidence/` (`JIG_RECORD_DIR`, which jig strips from the environment of every oracle run, env command and headless session it starts (a herdr session runs in herdr's own environment, which jig does not set)), verifies what a passing run left there, and journals it as one `recorded` line with the commit the oracle ran at and the run; a failing run's recordings are discarded, and the gate records nothing. The source of the demo (ADR 0029): publish picks among them (see **Pick**), and the gate's reviewer reads the ones on the reviewed head's history as evidence.
_Avoid_: screencast

**Pick**:
What a short session chooses at `jig publish` from the build's recordings: the ones that show the change to a person reviewing the pull request, composed into flows (recordings of one flow together in step order, scenarios that stand alone apart, mixes allowed). jig validates the answer against the candidates, copies the picked files into a directory of its own under the jig home's `evidence/`, attaches them to the pull request, and renders its `## Demo` section from them, a flow per `###` heading. It is the section's only source: a refused pick, or none to make, leaves the pull request without one (ADR 0029). Journaled as a `publish-picks` line, which a later publish of the same head and recordings reuses.
_Avoid_: selection, gallery

**Fix slice**:
A gate finding turned into a new frontier item. Review has no back-edges - every finding becomes forward work.
_Avoid_: review comment, follow-up task

**Fix budget**:
The cap on how many of a ticket's gate rounds may queue fix slices unattended (`gate.fix_rounds`), counted by how many of them appended at least one. Past it, a round parks each would-be open fix for a person instead of queuing it.
_Avoid_: rate limit, throttle

**Parked finding**:
A would-be open fix the fix budget routed to asked instead of a fix slice, recorded `routed_why: budget` in `findings.yaml`. Only a person at a terminal can keep one; keeping it queues a fix slice anyway, the way any kept ask does.
_Avoid_: blocked finding, deferred fix

**Lease**:
A pooled worktree checked out for one phase's work and returned warm when that phase ends. A lease of a branch that exists on origin is synced with it when acquired: fast-forwarded when it has no commits of its own, kept when it is ahead, and refused as diverged when both sides have commits the other lacks.
_Avoid_: checkout, sandbox

**Adopted branch**:
A branch built outside jig that a ticket took as its own with a first `jig gate <ticket> --branch <name>`. It is recorded in the ticket's `ticket.yaml`, and the ticket's start sha starts as its tip and follows it until jig builds on it. Every later gate round reviews it and every fix slice is built on it, on top of the author's commits; jig's own commits stay in the build lease until `jig publish` pushes them, which ships the branch as it is: the author's commits keep their shas, jig's sit on top, and origin's branch is fast-forwarded (only history not yet on origin is ever squashed). A ticket has at most one, for good: it needs no brief and no slices to be gated.
_Avoid_: imported branch, external branch, hand-written branch

**Gate**:
Out-of-band verification of a ticket's branch, run on its own lease, on the staircase's dearest model, with fresh context.
_Avoid_: code review, QA pass

**Intent**:
What a change is meant to accomplish, resolved once per gate round with a provenance: `brief` (the ticket's own brief.md) or `explicit` (`jig gate --intent`/`--doc`, recorded in `intent.md`) are binding, the human's own statement of what was asked for; `inferred` is jig's own summary of the author's local agent session (`internal/intent`, matched to the scope diff by file overlap), recorded the same way in `intent.md` but a hint, not binding - it can shape a fix judgment but a human never handed jig those words; `none` means nothing states it.
_Avoid_: spec, requirements, brief (a brief is one source of intent, not the concept itself)

**Finding action**:
Who acts on a gate finding, reported by the reviewer itself: `fix` means jig queues a fix slice, `ask` means a human decides, `note` means it is recorded only.
_Avoid_: class, severity, category

**Coverage**:
The files a reviewer actually read in a round, reported as `reviewed_paths`. jig accepts a round only when coverage includes every changed file and every open finding's file, and clears an open finding from it (or, whatever coverage says, when the finding's file no longer exists at head at all).
_Avoid_: reviewed scope, read set

**Triage**:
The human seam for a gate round's `ask` findings and its `fix` batch: at a terminal, every `ask` is decided as keep (with an optional decision) or dismiss - undecided is never offered as a choice, only what an ask missing a workspace, an oracle, or both defaults to when no human is present to make that call. Every decision is recorded and becomes eval gold.
_Avoid_: review comment triage, auto-dismiss

**Gold**:
A review-eval case's seeded truth: the findings a correct review must report and the traps it must not flag, both written into the case when it is built, not read off a review after the fact. A finding beyond it is labeled from a recorded human decision instead: kept, dismissed, or, when nobody decided, pending.
_Avoid_: ground truth, expected output

**Round verdict**:
A review-eval round's result: FAIL when it was refused or failed or any failure list is non-empty (a miss, a lost finding, a dropped question, a misattributed or wrong prior, a false alarm, a re-litigation); PROVISIONAL when nothing failed but some finding is still pending a label; PASS otherwise. A case takes its worst round's verdict.
_Avoid_: pass/fail, score

**Staircase**:
The per-dispatch model ladder: the first rung first (Sonnet by default), one rung up for each earlier attempt of the slice that failed at the work, floored to the dearest model when the diff touches a path a repo declares as invariant-sensitive. The gate reviewer always runs on the dearest rung.
_Avoid_: model tiering, escalation policy

**Stall**:
The same failure signature recurring twice. It marks a misconception, not a capability shortfall, and calls for a different approach rather than another attempt.
_Avoid_: retry loop, flaky failure

**Router**:
A thin skill that classifies intent and hands off. It never judges granularity itself.
_Avoid_: dispatcher, entry skill

**Chart**:
The wayfinder layer for work spanning tickets: `charts/<name>/` holds `map.md` (for people and sessions; jig never reads it) and `tickets.yaml` (the handover jig reads and writes). The map decides what to build; it never builds anything itself. `jig graduate <name>` creates the chart's tickets and writes their ids back into `tickets.yaml`.
_Avoid_: roadmap, epic

**Dependency**:
A ticket's own blocker, recorded in its `ticket.yaml` after graduation: another ticket id plus a kind, always spelled out. `merged` means the child starts from the target branch once all of the parent's PRs have merged; `stacked` means the child starts from the parent's branch in each repo the two share. Code overlap and priority are not dependencies.
_Avoid_: dep, edge

**Ledger**:
One store entry per ticket, ending in an Answers recall tail.
_Avoid_: index, registry
