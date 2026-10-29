# Jig

Vocabulary for jig, a CLI that attaches to the harness session a person already runs and takes a ticket from brief to merged PR: it observes the commits, proves each slice with its oracle in the background, and records the review and the publish.

## Language

**Project**:
A declared set of repos plus trackers (mirrors of the store's tickets) plus platform docs. Its identity is the `jig.yaml` marker at the root of its repo together with the store that serves it - once written, no verb re-infers it from folder layout or the current working directory.
_Avoid_: workspace config, repo group

**Marker**:
The `jig.yaml` at a project's root (not the per-repo manifest, `.claude/jig.yaml`): the project's name, `kind`, `repos`, the store's remote once shared, and defaults such as the branch rule, the spend budget and whether work is unattended. `jig init` writes it; a walk up from any directory finds the nearest one.
_Avoid_: project file, manifest (the manifest is the per-repo `.claude/jig.yaml`)

**Orchestrator**:
A project whose root is a directory above its repos, which sit nested inside it as submodules or gitignored clones (`kind: orchestrator`). A single-repo project is the other case (`kind: project`).
_Avoid_: monorepo, meta-repo

**Truth repo (store)**:
A dedicated git repo with a remote holding every ticket artifact; it is the source of truth: jig mints every ticket id and claims it here, trackers only mirror the store outward, and pull requests come from the repo host, not a tracker. Real tickets live under `tickets/<id>/`; `charts/`, `platform/`, `trackers/`, `ledger.md` and the store config (`store.yaml` at schema 3, `project.yaml` at schema 2) stay at the store root. A store at schema 2 up to the current one is accepted on every command, and `jig store migrate` moves one up a step at a time. Every commit jig makes in the store is authored and committed by the person's own git identity, as git resolves it in the store clone (`user.name` and `user.email`, or the environment); a clone with none is refused with `IDENTITY_REQUIRED` and the two commands that set one, and jig never commits under a name of its own. A store is in single-main mode, or, once `jig store migrate --writers` has run, in writers mode (see **Writer branch**).
_Avoid_: database, state dir

**Ticket**:
jig's record of one unit of work, in the store, under an id jig mints: a
key plus a number. May carry aliases, its earlier ids, kept forever; an id or
alias resolves to the ticket's current id wherever jig takes one.
_Avoid_: issue, which names a tracker's copy of a ticket

**Closed ticket**:
A ticket closed without a pull request for one of three reasons: covered (another change did the work), superseded (another ticket replaces it) or not planned (it will not be done). Its record carries a `closed:` entry, written by `jig close` and removed by `jig reopen`. It cannot be claimed, gated, published, requeued or reviewed, and the tickets that wait for it go on when it was covered or superseded and stay held when it was not planned.
_Avoid_: cancelled, deleted

**Key**:
The area part of a ticket id (`STORE` in `STORE-3`), declared in the store
config's `keys:` with a one-line meaning. A ticket's id never
changes when its ticket's area does - a key only decides what a new
ticket mints under.
_Avoid_: prefix, namespace

**Slug**:
The part of a ticket's default branch name after its id: the lowercase ASCII words of its title, at most 40 characters, fixed when the ticket is minted so that editing the title never renames the branch. A ticket's default branch is `<id>-<slug>`, where the id is the GitHub issue number once the ticket is mirrored and the project's branches are led by GitHub, else the store id; the default is derived and never recorded. A ticket with no recorded slug and no title takes it from the first heading of its brief, and with none of them the default branch is the bare id.
_Avoid_: branch name (a recorded `branch:` can be any name)

**Mirror**:
A tracker's copy of the store, written by jig at every checkpoint (its records
committed and pushed with it) and never read back. Today's one mirror projects every ticket and chart onto a GitHub
issue and a GitHub Project board, from inside the command that made the
checkpoint, best-effort: a GitHub failure warns rather than failing the
command. An edit made on the tracker side of a jig-owned field is drift,
reported and overwritten with the store's value at the next sync.
_Avoid_: tracker as the source of tickets

**Tracker tree**:
The folder `trackers/<provider>/<scope>/<remote id>/` of a store, its entry: one folder for each remote item a tracker holds of the store's tickets and charts, holding the item's record and a link to its ticket or chart named by the store id. GitHub's scope is the lower-case `<owner>/<repo>` and its remote id the bare issue number.
_Avoid_: tracker folder, mirror folder

**Record** (of the tracker tree):
The file `record.yaml` in a remote item's folder in the tracker tree. The tree owns its `ticket:` or `chart:` key and the link beside it; every other key belongs to the provider and is kept as the provider wrote it. The link is a view of the record, made again from the records on every sync, and renamed when the ticket's id is. It is not `ticket.yaml`, which through the link is the ticket's own record.
_Avoid_: tracker file

**Intake**:
The shared opening phase of a brief and a chart, run together because both need the same first read of the work. It delivers the granularity verdict: whether the request is one ticket or a chart of several. It starts from `jig recall`, which shows the open tickets and their edges, near-duplicates, the charts' fog, platform documents and past answers, and it ends when `jig validate` says `valid: yes` once the person has confirmed the brief, which is the brief's approval (`jig validate --check` checks the same without approving).
_Avoid_: onboarding

**Illustration**:
An optional `illustration.html` beside a brief's `brief.md`, covered by the brief's approval: adding, editing or removing it withdraws the approval, and a brief with none is approved exactly as before. `jig brief show` is the one viewer jig ships; any other is the project's choice.
_Avoid_: mockup, diagram

**Shape**:
A ticket's intent, one of feature, bug, or refactor.
_Avoid_: type, category, kind

**Design facet**:
A mock any shape can carry, with a fidelity per state per region: exact, adapt, or hybrid.
_Avoid_: mockup spec, UI plan

**Slice**:
A tracer-bullet unit of work with a named oracle and blocking edges to other slices. It is the unit of claim, verification, resume, and commit: a commit belongs to the slice named by its `Jig-Slice:` trailer.
_Avoid_: task, subtask, step

**Oracle**:
The command that proves a slice done. Green is done - there is no separate test stage beyond the oracle, which jig runs itself, in the background, on each commit that names the slice (see **Verification**).
_Avoid_: test suite, check

**Workspace**:
A slice's target: a repo, or a module inside one. In a project of several repos a slice writes it as `<repo>/<workspace>`, or as the bare id of the one repo whose manifest declares it; a bare id that two repos declare, or that none does, belongs to no repo until the slice names one.
_Avoid_: target dir, project root

**Env class**:
A repo-defined environment an oracle needs, with an up/check/down lifecycle and a stated unavailability policy.
_Avoid_: environment, test env

**Frontier**:
The set of slices whose blockers are all green, and so are ready to claim now (`jig next`). It is advice about what is ready, not a dispatcher: nothing hands a slice to anyone.
_Avoid_: ready queue, backlog

**Phase**:
One of the four stages a ticket goes through: brief, build, review and publish (the TUI calls them steps). Each has a skill (`intake`, `build`, `review`, `publish`) and a prompt that asks for it: "Plan SHOP-1", "Build SHOP-1", "Review SHOP-1", "Publish SHOP-1". Where a ticket stands is derived from its journal lines and its status, never recorded (`internal/phase`, shown by `jig next <ticket>`, `jig status` and the TUI): a phase is done only when the ones before it are, a phase that cannot start says what it waits for, and asking again for a phase that is done is warned about, not refused.
_Avoid_: stage, task

**Sub-step**:
A part of a phase with a state of its own: a slice in the build; a gate round, the asks and the fixes in the review; the body, the approval, a pull request and the changelog in the publish. `internal/phase` derives them with the phase, the TUI lists them under the step under the cursor, and each line of its activity names the sub-step it advances.
_Avoid_: task, sub-task

**Start-to-finish run**:
A prompt that names several phases ("Build and review SHOP-1", "Take SHOP-1 from brief to PR"), which the session runs in order, each with its skill, without waiting for a prompt to start the next. The span is the prompt and nothing records it; a refusal ends the run. It hands over no decision: the brief's questions and approval, a review's asks and the publish approval are asked of the person. For an unattended session the Stop hook's next-phase note says when the run has more to do ([ADR 0035](docs/adr/0035-start-to-finish-and-recommendations-are-separate.md)).
_Avoid_: automatic run, workflow, pipeline run

**Recommendations**:
A hand-over the prompt says ("go with your recommendations"), in one phase or a start-to-finish run: the session answers the person's decisions itself with its recommended answers, recorded as its own (grilling answers tagged `defaulted`, the brief's approval with agent provenance, asks decided with `jig triage`, the publish the hand-off way, which asks for no approval). A start-to-finish run with recommendations has nobody to ask.
_Avoid_: delegation, auto mode

**Scout**:
A read-only session rooted in a repo, drafting brief sections for the parent intake.
_Avoid_: research agent, explorer

**Harness**:
The program a person builds in - Claude Code today; Codex, Gemini CLI, Cursor, GitHub Copilot and OpenCode by the same floor. jig attaches to it through hooks, skills, MCP and the CLI, and never starts it, picks its model or grants its tools.
_Avoid_: agent runtime, backend (a backend is what the reviewer evaluation launches)

**Session**:
One run of a harness, known to jig by the id the harness gives it (or one jig mints, `s-` and 12 hex digits), as the fold of its lines in the **Ledger**. Live until it has a `session-end` line. Its claims, verifications and tool calls are how jig knows who built what.
_Avoid_: run, conversation

**Fresh session**:
A harness session that did none of a ticket's work: its id carries no journal line of the ticket except the gate's, triage and publish lines, and no ledger line of it changed a file or made a commit for the ticket. `jig gate` and `jig next <ticket>` say whether the calling session is fresh, not fresh or unknown. A fresh one reviews the ticket itself; one that is not has a subagent do it, waits for its verdict, and never reads the round itself (a subagent shares its session's id, so if both read it the ledger cannot say which closed it). Advice only: `jig review done` judges independence from who built, as before.
_Avoid_: independent (that is the property of a recorded round)

**Claim**:
A session taking a slice to build it (`jig claim`): a journal line plus the slice's state, checked against the ticket's readiness (a merged blocker's work merged, a stacked parent's gate clean, the brief's approval current, the ticket not closed), refused when unattended and warned otherwise (a closed ticket is refused outright). Not to be confused with claiming an id, which reserves it on the claims log (see **Claims branch**).
_Avoid_: assign, lock

**Verification**:
One run of a slice's oracle on a commit, in a detached worktree of the operator's clone and a detached process (`jig verify`), started by git's post-commit hook or the harness's PostToolUse. It is recorded as `verify-pending`, `oracle`, `recorded` and `verified` (or `verify-error`) lines, keyed by (commit, slice) and idempotent. Green is what a verification proved, never what a session claimed.
_Avoid_: CI run, test run

**Journal**:
The append-only record of every claim, verification, question, round and publish of a ticket, in the store. Changelogs render from it; they are never authored by hand.
_Avoid_: log file, history

**Ledger**:
The jig home's append-only record of what happened on this machine: sessions, tool calls, hooks fired, commits seen, notes delivered, in the journal's line shape. Machine-local and not a store subtree; `jig events` reads it beside the journals as one stream. Not the store's `ledger.md` (see **Ledger entry**).
_Avoid_: history, audit log

**Ledger entry**:
One store entry per ticket in `ledger.md`, headed `## <id> - <title>` and ending in an Answers recall tail. A publish of a ticket that has an entry replaces it where it stands, and `jig store migrate` renames the id in its heading when the ticket gets a new one.
_Avoid_: index, registry

**Contract index entry**:
One line per ticket in `platform/contract-index.md`, `- <id>: workspaces ..., oracles ...`, kept the way a **Ledger entry** is: one per ticket, replaced by the next publish, renamed by the migration.
_Avoid_: ledger entry

**Hook**:
What a harness (or git) calls when something happens in a session: session start, a prompt, a tool call, a stop, a session end, a commit. jig's hooks all end in `jig observe` or `jig githook post-commit`, which exit 0 and print nothing whatever they are handed, and which record nothing for a directory in no jig project.
_Avoid_: trigger, listener

**Note**:
What a session has not yet heard: a verification outcome, an answer, a correction, a spend warning, what comes next (to open the gate and review, to publish). Delivered once per session and fact at the session's next tool call or prompt; an enforced note also holds an **unattended** session back at Stop, once.
_Avoid_: notification, alert

**Correction**:
A fact told to a session from what the journal already holds, with no second record: a **stall**, a brief that changed under verified work (*brief drift*), a commit outside every slice workspace (*scope drift*), a commit that names no slice (*slice missing*: the note guesses the slice from the files, and verifies nothing), or, for a stacked ticket, a parent that moved past it or merged (*restack*, *retarget*; see **Stacked start point**). Advisory in an interactive session, enforced in an unattended one.
_Avoid_: warning, lint

**Unattended**:
A claim made with `--unattended` (or a marker default) says no person is watching: an unmet claim condition refuses the claim, and an enforced note refuses a Stop once per fact. It is never an environment variable, since interactive cloud sessions exist.
_Avoid_: headless, background

**Spend**:
What a session cost, in tokens and list-price dollars summed from its transcript at Stop and SessionEnd, recorded as a cumulative `spend` line per session. A ticket's budget (`jig budget`) is held against the sum and told as a note at 80% and past it. Records and advice, never a stop.
_Avoid_: cost cap, quota

**Driver**:
The writer that drives a ticket: its latest `driver` line in the claims log that no `release` line of the same writer has ended since. One driver per ticket; another writer's unreleased one gives `DRIVER_HELD`, and `jig claim --take` takes it over.
_Avoid_: owner, assignee

**Writer branch**:
In writers mode, the branch `writers/<name>` a store clone works on and pushes alone. The others' writer branches and the legacy main are merged read-only with a union driver for NDJSON files, a rewritten one is refused and reported, never repaired, and no writer pushes main.
_Avoid_: feature branch, fork

**Claims branch**:
The orphan branch `claims` holding the append-only claims log (`claims.ndjson`), which every writer pushes by compare-and-swap. It holds the facts that only one writer may hold: a minted id and a ticket's driver. In single-main mode the same log is `claims.ndjson` at the store root.
_Avoid_: lock branch

**Provenance**:
Who a fact speaks for: `person`, `agent` or `jig`. Recorded on an answer, a triage decision, an approval, a note, a publish approval and on an intent (brief, explicit, inferred or none). A person is whoever is at a terminal; an agent's word never stands in for a person's where a rule asks for one.
_Avoid_: author, source

**Receipt**:
Evidence that a check passed, stored in the ticket's `evidence/`.
_Avoid_: screenshot, proof

**Demo**:
What a pull request shows of the change working: its `## Demo` section, a screenshot, a GIF, a video or a terminal capture per recording, composed into flows. It is a by-product of verification, not a session of its own: the build's end-to-end scenarios write recordings, and `jig publish` renders the section from the ones a **pick** names. A pull request whose build recorded nothing, or whose pick is refused, has no demo section. ADR 0014's gate demo session, which dispatched a model after a clean reviewer round to make the media, is retired (ADR 0029).
_Avoid_: receipt (evidence that a check passed), screencast

**Recording**:
What the build's end-to-end scenarios write while they run in a verification's oracle run: a screenshot, a video or a terminal capture, optionally tagged with its scenario, flow, step and caption. jig gives that run a directory of its own under the jig home's `evidence/` (`JIG_RECORD_DIR`, which jig strips from the environment of every other oracle run and env command it starts), verifies what a passing run left there, and journals it as one `recorded` line with the commit the oracle ran at and the run (`<slice>-v<attempt>`); a failing run's recordings are discarded, and the gate records nothing. The source of the demo (ADR 0029): publish picks among them (see **Pick**), and the gate's reviewer reads the ones on the reviewed head's history as evidence.
_Avoid_: screencast

**Pick**:
What an agent chooses at `jig publish` from the build's recordings and writes as a picks file (`--picks`): the ones that show the change to a person reviewing the pull request, composed into flows (recordings of one flow together in step order, scenarios that stand alone apart, mixes allowed). jig validates the answer against the candidates, copies the picked files into a directory of its own under the jig home's `evidence/`, attaches them to the pull request, and renders its `## Demo` section from them, a flow per `###` heading. It is the section's only source: a refused pick, or none to make, leaves the pull request without one (ADR 0029). Journaled as a `publish-picks` line, which a later publish of the same head and recordings reuses.
_Avoid_: selection, gallery

**Fix slice**:
A gate finding turned into a new ready slice (queued). Review has no back-edges - every finding becomes forward work.
_Avoid_: review comment, follow-up task

**Fix budget**:
The cap on how many of a ticket's gate rounds may queue fix slices unattended (`gate.fix_rounds`), counted by how many of them appended at least one. Past it, a round parks each would-be open fix for a person instead of queuing it.
_Avoid_: rate limit, throttle

**Parked finding**:
A would-be open fix the fix budget routed to asked instead of a fix slice, recorded `routed_why: budget` in `findings.yaml`. Keeping it (`jig triage ... keep`) queues a fix slice anyway, the way any kept ask does; a round opened with `--yes` leaves it undecided.
_Avoid_: blocked finding, deferred fix

**Lease**:
A pooled worktree checked out for one phase's work (a gate round, a publish) and returned warm when that phase ends. A lease of a branch that exists on origin is synced with it when acquired: fast-forwarded when it has no commits of its own, kept when it is ahead, and refused as diverged when both sides have commits the other lacks. The work itself happens in the operator's clone, not in a lease.
_Avoid_: checkout, sandbox

**Adopted branch**:
A branch recorded in a ticket's `ticket.yaml` as its own: by the first `jig gate <ticket> --branch <name>` for a branch built outside jig, or by a `jig claim` on a branch that names no ticket. Every later gate round reviews the copy of it in the operator's clone (or origin's), and `jig publish` ships the reviewed head as it is: nothing is reconciled, squashed or committed on it, and origin's branch is fast-forwarded. A ticket has at most one, for good: it needs no brief and no slices to be gated.
_Avoid_: imported branch, external branch, hand-written branch

**Gate**:
Out-of-band verification of a ticket's branch, run on its own lease: `jig gate` opens a round and starts nothing, and a fresh-context reviewer that jig did not start closes it (see **Recorded round**).
_Avoid_: code review, QA pass

**Recorded round**:
A gate round that is opened by `jig gate` and closed by a reviewer's `jig review done`, not launched by jig. Its request, the reviewer's drafts and its result live in the store's `work/` until it is closed and folded into `gate/round-N/`; a branch that moved before `done` is `REVIEW_STALE_HEAD`. It is *independent* unless the reviewer built part of the change, judged from the sessions on the ticket's claim, verification and ledger lines (`not-independent` is refused by publish; unknown is allowed and counted).
_Avoid_: review job, launched review

**Intent**:
What a change is meant to accomplish, resolved once per gate round with a provenance: `brief` (the ticket's own brief.md) or `explicit` (`jig gate --intent`/`--doc`, recorded in `intent.md`) are binding, the human's own statement of what was asked for; `inferred` is an excerpt of the transcript of a session that worked the ticket (found through the machine ledger's record of its driver and builders, else matched to the scope diff by file overlap in `internal/intent`) that the reviewer reads, a hint, not binding, which never reaches a pull request; `none` means nothing states it.
_Avoid_: spec, requirements, brief (a brief is one source of intent, not the concept itself)

**Finding action**:
Who acts on a gate finding, reported by the reviewer itself: `fix` means jig queues a fix slice, `ask` means a human decides, `note` means it is recorded only.
_Avoid_: class, severity, category

**Coverage**:
The files a reviewer actually read in a round, reported as `reviewed_paths`. jig accepts a round only when coverage includes every changed file and every open finding's file, and clears an open finding from it (or, whatever coverage says, when the finding's file no longer exists at head at all).
_Avoid_: reviewed scope, read set

**Repo-qualified file**:
In a project of several repos, how a gate round names a file: `<repo>/<path>`, the repo being one the ticket's slices build in. A finding's file, `reviewed_paths` and `must_review` are all named so; in a project of one repo a file is a path in the repo. A finding's workspace is named `<repo>/<id>` the same way, as a slice's is.
_Avoid_: bare path (in a project of several repos)

**Triage**:
The human seam for a gate round's `ask` findings: `jig triage <ticket> <finding> keep|dismiss` records a decision, as `person` at a terminal and `agent` otherwise, and the next round applies what was decided. A decision is *pending* from its `triage` line until the next round closes, and *spent* after; `jig status` and `jig review context` show a pending decision on its ask beforehand, and the ask stays open until the round applies it. An ask nobody decided stays undecided - it is never defaulted - unless the round was opened with `--yes`, which keeps every fix and every ask with a full build target (a workspace and an oracle). Every decision is recorded and becomes eval gold.
_Avoid_: review comment triage, auto-dismiss

**Gold**:
A review-eval case's seeded truth: the findings a correct review must report and the traps it must not flag, both written into the case when it is built, not read off a review after the fact. A finding beyond it is labeled from a recorded human decision instead: kept, dismissed, or, when nobody decided, pending.
_Avoid_: ground truth, expected output

**Round verdict**:
A review-eval round's result: FAIL when it was refused or failed or any failure list is non-empty (a miss, a lost finding, a dropped question, a misattributed or wrong prior, a false alarm, a re-litigation); PROVISIONAL when nothing failed but some finding is still pending a label; PASS otherwise. A case takes its worst round's verdict.
_Avoid_: pass/fail, score

**Staircase** (retired):
The per-dispatch model ladder jig used while it chose models: the first rung first, one rung up for each earlier attempt of the slice that failed at the work, floored to the dearest model when the diff touched a path a repo declares as invariant-sensitive (ADR 0019). jig chooses no model now (ADR 0032); the code survives only as the reviewer evaluation's default model, its `staircase:` and `builder_effort:` keys still load, and nothing in the binary reads them.
_Avoid_: model tiering, escalation policy

**Stall**:
The same failure signature recurring twice: two `oracle` failures of one slice with one signature since its last `verified` or `requeue` line. It marks a misconception, not a capability shortfall, and calls for a different approach rather than another attempt; an unattended stall also marks the slice `stalled` until `jig requeue`.
_Avoid_: retry loop, flaky failure

**Chart**:
The wayfinder layer for work spanning tickets: `charts/<name>/` holds `map.md` (for people and sessions; jig never reads it) and `tickets.yaml` (the handover jig reads and writes). The map decides what to build; it never builds anything itself. `jig graduate <name>` creates the chart's tickets and writes their ids back into `tickets.yaml`.
_Avoid_: roadmap, epic

**Dependency**:
A ticket's own blocker, recorded in its `ticket.yaml` after graduation: another ticket id plus a kind, always spelled out. `merged` means the child starts from the target branch once all of the parent's PRs have merged (see **Merge fact**); `stacked` means the child starts from the parent's branch in each repo the two share (see **Stacked start point**). Code overlap and priority are not dependencies.
_Avoid_: dep, edge

**Rank**:
The owner's place for a ticket in the order to start ready tickets: `rank:` in `ticket.yaml`, set by `jig rank`. Lower starts first, a rank two ready tickets hold is allowed and flagged, and the unranked come last, by id.
_Avoid_: priority field, weight

**Merge fact**:
Whether a ticket's work has merged, for each repo it builds in. A `merged` blocker holds its dependents until it is met: the ticket published, and the host says its recorded pull request merged or, with none recorded or the host unable to say, its branch tip is an ancestor of `origin/<target>` in the mapped clone. Patch equivalence is never taken for a merge, so a squash stays unmet until its pull request is recorded, and what is missing is named (`no merge on record`, `its PR is open`).
_Avoid_: merge status

**Stacked start point**:
Where a stacked ticket's branch starts, and where its pull request goes, in each repo it shares with its parent: the parent's branch while the parent has not merged there, `origin/<target>` and the target otherwise. It is advice computed on read (`jig next`, `jig here`, the publish), and jig never rebases or merges. When the parent's branch moves past the child's, the child's session is told to *restack*, which is to merge the parent's branch into the child's; when the parent merges, to *retarget*, which is to point the pull request at the target and merge it.
_Avoid_: base branch
