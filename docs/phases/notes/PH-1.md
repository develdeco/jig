# PH-1: `internal/phase`, and `jig next` and the hints read it

Decisions 35 and 36: one pure derivation of where a ticket stands, read by every
command that advises the next step. Branch `ph/PH-1`.

## What was built

- `internal/phase` (new, `phase.go`): `Derive`, the types, the codes, `Prompt`,
  `Parse`, `OpenRoundIn`. Imports `internal/journal` and the standard library
  only. (`journal` imports `internal/store` for `Append`, so the package links
  the store transitively; it imports neither `store` nor `verifydeliver`
  directly, which is what `internal/tui/invariant_test.go` checks.)
- `cmd/jig/phase_facts.go` (new): `phaseFacts(st, ticket)` gathers the facts and
  the journal lines, `derivePhase(st, ticket)` derives, `phaseAdvice(t, ticket)`
  is the one hint line, `promptHint(prompt)` is the `; ask: ...` clause,
  `briefApprovalOf` is the brief's approval state (also used by `jig status`'s
  brief block now), `gateRoundsOf` reads the rounds' reports.
- `cmd/jig/next.go`: the `phase`, `ready`, `prompt`, `code`, `reason` keys, the
  `--for` flag, a phase-aware help.
- `cmd/jig/status.go`: `nextStepHint` ends in `phaseAdvice`. The open-question,
  stalled and env-blocked hints stay as they were and still come first.
- `cmd/jig/here.go`: the help names the prompt.
- `cmd/jig/claim_checks.go`: the claim problems name the prompt that makes the
  ticket ready.
- `internal/verifydeliver`: `Deps.Prompt` (a seam), used by the
  `GATE_NOT_GREEN` refusal (`checkFrontier`) to add `ask: <prompt>`; the binary
  sets it in `verifydeliverDeps` (cmd/jig/main.go).
- `internal/mcp/tools.go`: the `next` tool gains the `for` param.
- `cmd/jig/ticket.go`: `intakeHint` ends with `; ask: Plan T`.
- Tests: `internal/phase/phase_test.go`, `cmd/jig/phase_facts_test.go`,
  `e2e/phase_test.go` (`TestNextNamesPhaseAndPrompt`, `TestHintsMatchRefusals`),
  one test in `internal/verifydeliver/gate_test.go`, one row in
  `internal/mcp/tools_test.go`.
- Existing tests whose expected hint text gained the prompt (no test deleted):
  `cmd/jig/adopt_test.go`, `main_test.go`, `status_outstanding_ask_test.go`,
  `e2e/testdata/golden/status-a-green-b-blocked.txt`; and
  `TestGateReportHintNamesTheOrderWhenFixSlicesAreQueuedToo` (`gate_test.go`)
  now records the round it describes in the journal.

## The API (`internal/phase`)

```go
type Name string // Brief, Build, Review, Publish ("brief", "build", ...)
var Names [4]Name
func Parse(word string) (Name, bool)
func Prompt(p Name, ticket string) string // "Plan T", "Build T", "Review T", "Publish T"

func Derive(f Facts, lines []journal.Line) Ticket
func OpenRoundIn(lines []journal.Line) *OpenRound // the journal's word on an open round, for a caller with no store

type Ticket struct {
    Phases  [4]Phase      // brief, build, review, publish
    Current Name          // the first phase not done, the last when all are
    Ready   bool          // whether Current can start now
    Code    string        // when not ready (the Code* constants)
    Reason  string        // one sentence for a person, when not ready
    Prompt  string        // what to ask an agent; "" when done
}
func (t Ticket) Of(n Name) Phase
func (t Ticket) For(p Name) Asked // can p run now, whatever the ticket is at

type Phase struct { Name Name; State string /* done|current|pending */; Text string; Subs []Sub }
type Sub   struct { Key, Label, State, Detail string }  // State: done|active|waiting|needs-you|failed|pending
type Asked struct { Phase Name; Ready bool; Code, Reason, Warning, Prompt string }

type Facts struct {
    Ticket string
    Adopted bool; Branch string
    BriefExists bool; Sections int
    Approval string                  // ApprovalNone|ApprovalCurrent|ApprovalChanged
    ApprovedBy, ApprovedHow string    // how: "person" or "agent"
    Slices   []Slice                 // ID, State, Goal, BlockedBy, FromGate, OpenQuestion
    Asks     []Ask                   // ID (Decision, DecidedBy, Pending are read from the triage lines)
    Blockers []Blocker               // Ticket, Kind, Reason, Prompt
    Rounds   []Round                 // N, Verdict, Reviewer: the store's reports
    Open     *OpenRound              // Round, Reviewer: the round open now
    Published bool
}
```

Differences from the brief's sketch, all additive: `Facts.Branch` (the text of
the adopted brief), `Facts.ApprovedHow`, `Slice.BlockedBy`, `Blocker` is a struct
(it carries the sentence and the prompt that meets it), `Facts.Rounds` and
`Facts.Open` (see below), `Ask.DecidedBy`.

### Sub-step keys

| Phase | Keys |
|---|---|
| brief | `drafted`, `slices`, `approved`; none for an adopted ticket or one with no brief on record |
| build | `slice:<id>`, one per slice of slices.yaml order, fix slices included (label `fix slice <id> (round n)`) |
| review | `round:<n>` per closed round, `round:<n>` for the round open, `asks` while there are outstanding asks, `fixes` while the last round's fix slices are open, `round:<n+1>` (pending) while no clean round stands |
| publish | `body`, `approval`, `pr`, `changelog`, `done` |

Slice details: `verified <sha7>`, `verifying <sha7>`, `claimed by <session8>`,
`waits for a, b`, `ready to build`, `waits for your answer`, `stalled`, `waits for
its environment`. A round's detail: `clean`, `1 fix, 1 ask`, `opened, waiting for
a reviewer`, `taken by <session8>`. A green slice with a verify-pending line no
oracle/verified/verify-error line has followed is `verifying` (and the build is
not done).

## The rules

- A phase is done only when every phase before it is (the TUI's rule); a
  published ticket has four done phases.
- **Brief** is done when approved (`current`), or the ticket has no brief and
  took a branch or has slices ("no brief on record"), or the work started
  without an approval ("never approved": the `approved` sub-step stays
  `needs-you`). A brief `changed` after its approval is not done. "Started" is
  any build, review or publish line, a slice past queued, a fix slice, or a round
  on record.
- **Build** is done when every slice is green and none is being verified; an
  adopted ticket with no slices is built.
- **Review** is done when the last closed round is clean, no build line
  followed its closing line in the journal (the head did not move), and no round
  is open.
- **Publish** is done when published (`publish-done`).
- **Readiness of `Current`** (`Ready`, `Code`):

| Current | Not ready when | Code |
|---|---|---|
| brief | never (planning always can start; the `approved` sub-step says the person is owed) | |
| build | a blocker has not published (stacked: has no clean round) | `PHASE_BLOCKED`, prompt of the blocker's own phase |
| review | a round is open | `PHASE_ROUND_OPEN` |
| review | outstanding asks nobody decided | `PHASE_ASKS_UNDECIDED` |
| publish | published | `PHASE_DONE`, no prompt |

- **`For(p)`** answers for a phase the asker names. The current phase: Ready,
  Code, Reason as above. A phase done: Ready, with `Warning: "build is done (3
  slices green, each verified by jig); prompt: Review T"`, never a refusal. A
  phase after the current one: not ready, with the code of what it waits for:
  `PHASE_BRIEF_NOT_APPROVED` (brief current), `PHASE_NOT_BUILT` (build
  current), and with review current `PHASE_ROUND_OPEN`, `PHASE_ASKS_UNDECIDED` or
  `PHASE_NOT_REVIEWED` (no round, a round that found things, or a clean round on
  a head that moved).
- A decision (`keep` or `dismiss`) is pending when its triage line comes after
  the last `gate-round` or `gate-clean` line; an older one is spent and the ask
  counts as undecided (decision 41's rule). Derive reads it from the lines, so
  `Asks[].Decision` is left empty by the gatherer. Reading triage was trivial
  here, so the TR-1 columns can reuse `Ask.Decision`, `DecidedBy`, `Pending`.

## What the facts come from (`phaseFacts`)

| Fact | Source |
|---|---|
| Adopted, Branch, blockers | `ticket.yaml` |
| BriefExists, Sections, Approval, ApprovedBy, ApprovedHow | `brief.md`, `approval.yaml` (`briefApprovalOf`, the same call as the status brief block) |
| Slices | `slices.yaml`, each slice's state file, open questions |
| Asks | `verifydeliver.OutstandingAsks` (ids only) |
| Blockers | `blockerProblem` with no pull-request host: unpublished merged blockers, stacked parents with no clean round. `claimProblems` also asks the host whether a published blocker's pull request is merged; the phase does not |
| Rounds | `gate/round-N/report.yaml`: the verdict publish reads (`not-independent` when `independent: false`) |
| Open | `verifydeliver.ReadStatus(...).Open` (the open record in `work/`); the journal's `gate-open` alone is not enough, since a gate that failed on its oracles wrote it and opened nothing |
| Published | a `publish-done` line |

The journal lines supply: the approval line (who, how, when the record has not),
claim and release (holder), verify-pending/oracle/verified/verify-error
(verifying, the verified commit), the gate lines (rounds, reviewers, where a
round closed), triage (decisions), and the pr-body, publish-approved, pr,
changelog lines since the last round closed (a body written for an earlier head
does not count). The store's reports win over the journal's gate lines for a
round's verdict.

## `jig next <T>`

New keys in the `next:` block, after the existing ones (`state` is unchanged;
the roster's `jig next` with no ticket is untouched):

```
next:
  ticket: JIG-1
  state: ready
  branch: jig/JIG-1
  phase: build            <- Current
  ready: yes              <- yes|no
  prompt: Build JIG-1     <- omitted when done
  code: PHASE_BLOCKED     <- only when not ready
  reason: ...             <- only when not ready
```

`--for <phase>` adds `for`, `for_ready`, `for_code`, `for_reason` and, for a
phase already done, `warning`. `--for` without a ticket, or with a word that is
no phase, is a `VALIDATION_ERROR` (exit 2). The MCP `next` tool takes `for`.

JSON: the new data is one object under `phase`, because the top-level `ready` is
already the slices ready to build (and the text keeps its `ready[n]{repo,slices}`
table or `ready: none` line after the block):

```json
"phase": {"name":"build","ready":true,"prompt":"Build JIG-1","code":"","reason":"",
          "phases":[{"name":"brief","state":"done","text":"..."}, ...],
          "for":{"phase":"review","ready":false,"code":"PHASE_NOT_BUILT","reason":"...","warning":""}}
```

`code`, `reason` and `prompt` are omitted when empty. If the phase cannot be
derived (a round it cannot read), `phase` is absent, `phase_problem` says why, and
the text has a `note:` block; the rest of `jig next` is unchanged.

The help is phase-aware: a ready ticket whose brief awaits its approval gets the
approval line first, then the slice advice; a green ticket gets `phaseAdvice`
(gate, or publish, or the open round); every line that sends a person to a phase
ends with `; ask: <prompt>`.

`jig next` with no ticket does not gain a phase column: deriving a phase reads the
gate rounds, the findings and the open record of every ticket, and the TUI polls
this roster. A follow-up can add it once the cost is measured.

## The hints and the refusals

The words, from `phaseAdvice` (the same function for `jig status`, `jig next` and
the gate report's hint):

| Phase and state | Line |
|---|---|
| brief, nothing drafted | `intakeHint(T)` + `; ask: Plan T` |
| brief, drafted | `The brief of T waits for the person's approval: run `jig validate T` once they have confirmed it; ask: Plan T (approve its brief)` |
| build | `Run `jig next T` to see what is ready, then `jig claim T --slice <slice>` to take one; ask: Build T` |
| build, blocked | the blocker's sentence + `; ask: <the blocker's prompt>` |
| review, round open | `round 2 of T is open ...: the reviewer ends it with `jig review done T`; ask: Review T` |
| review, asks undecided | the `PHASE_ASKS_UNDECIDED` reason + `; ask: Review T` (while the frontier is green `jig status` still prints its own triage line first) |
| review | `Run `jig gate T` to open a gate round` / `... the next gate round` + `; ask: Review T` |
| publish | `Run `jig publish T` to open or update the PR; ask: Publish T` |
| done | `T is published: run `jig next` to see the tickets that are left` |

Refusals:

- `CLAIM_NOT_APPROVED` (both the unapproved and the changed form): the help ends
  `; ask: Plan T (approve its brief)`.
- `CLAIM_BLOCKED_MERGED` and `CLAIM_STACKED_PARENT_NOT_CLEAN`: the help ends with
  `; ask: <prompt of the blocker's own phase>` (the blocker's facts are gathered
  with a visited set, so blockers that wait for one another stop at the repeat).
- `GATE_NOT_GREEN` (from `jig gate`, `jig review context` and `jig publish`): a
  second help line `ask: <the ticket's prompt>`.
- `intakeHint` (every "no slices yet" refusal and `jig status`/`jig ticket new`
  hint) ends `; ask: Plan T`.

The words are always `ask: <prompt>` in a hint or refusal and `prompt: <prompt>`
in `jig next`'s block.

## Decisions I took

1. **Ready is about the phase the ticket is at.** Planning is always ready (the
   `approved` sub-step carries "the person is owed"). The codes that name what
   an *asked-for* later phase waits for (`PHASE_BRIEF_NOT_APPROVED`,
   `PHASE_NOT_BUILT`, `PHASE_NOT_REVIEWED`) come from `For`, which is what the
   skills' "is my phase the ready one?" check and the "re-running a finished
   phase is warned" rule need. Skills can compare `phase:` with their own, or
   pass `--for`.
2. **Work started without an approval puts the ticket past the brief.** An
   attended claim on an unapproved brief warns (decision 8) and goes through;
   from then on `jig status` says `Build T`, not `Plan T`, so a person who went
   ahead is not sent back at every step. The claim warning goes on saying `Plan
   T (approve its brief)`. A brief that *changed* after its approval takes the
   ticket back to the brief at any point (the TUI's rule).
3. **The store is the word on a round's verdict**, the journal's gate lines the
   word on where it closed (for "the head moved") and who took it. Publish reads
   `report.yaml`, so the advice does.
4. **"The head moved" is read from the journal**: a build line (the TUI's list:
   claim, release, dispatch, verify-pending, oracle, verified, verify-error,
   question, answer, stall, requeue, result, fix-slice) after the line that closed
   the last round. It does not compare git heads, so a commit made with no jig
   line is not seen; publish's own `PUBLISH_UNREVIEWED_HEAD` still checks the real
   head.
5. **Asks:** decided asks (a pending `keep`/`dismiss`) do not block the next
   round; only undecided ones do (`PHASE_ASKS_UNDECIDED`, advice only: `jig gate`
   still opens a round).
6. **`jig here` stays best-effort**: a ticket whose phase cannot be derived gets
   the old help.

## ARCHITECTURE row for the module table

| `internal/phase/` | `Derive`, `OpenRoundIn`, `Prompt`, `Parse`, `Ticket.For` | a ticket's facts (brief and approval, slices and their states, outstanding asks, closed and open gate rounds, unpublished blockers) and its journal lines → the four phases (brief, build, review, publish) each done, current or pending with its sub-steps, the current phase, whether it can start, why not (`PHASE_BRIEF_NOT_APPROVED`, `PHASE_BLOCKED`, `PHASE_NOT_BUILT`, `PHASE_ROUND_OPEN`, `PHASE_ASKS_UNDECIDED`, `PHASE_NOT_REVIEWED`, `PHASE_DONE`) and the prompt that starts it ("Plan T", "Build T", "Review T", "Publish T"); `For` answers the same for a phase a caller names, warning (never refusing) when it is done; pure, so the TUI imports it and `jig next`, `jig status`, `jig here` and the claim and gate refusals all read it |

Text for the "what names the next phase" prose: `jig next <ticket>` prints
`phase`, `ready`, `prompt` (and `code`, `reason` when not ready); `--for <phase>`
asks about one phase. `jig status`, `jig here` and the claim and `GATE_NOT_GREEN`
refusals name the same prompt.

## For the tickets that follow

- **TUI-1**: build `phase.Facts` from `statusinfo` and the roster, and call
  `phase.OpenRoundIn(lines)` for `Facts.Open` (the TUI cannot read the open
  record). `Facts.Rounds` may stay empty: the journal's gate lines then decide.
  `journal.Line` is `Envelope.Line`. The sub-step `Label`s are the words the
  activity lines should carry; a fix slice is `fix slice <id> (round n)`.
  `TestTUIAndNextAgree` can compare `Ticket.Current` and the phase states with
  `jig next --json`'s `phase.name` and `phase.phases[].state`.
- **FR-1 (skills)**: read `jig next T` (or `jig next T --for build`): `phase`,
  `ready`, `prompt`, `reason`, `code`; relay `prompt:` at the end.
- **TR-1**: `Ask.Decision`, `DecidedBy` and `Pending` are derived in
  `internal/phase` from the triage lines; the status columns can use the same
  rule (after the last `gate-round`/`gate-clean` line).

## Findings outside the brief (not fixed)

- `jig gate`'s help still prints a fixed "Spawn a fresh-context reviewer" (decision
  39 makes it conditional; not this ticket).
- `claimProblems` asks the host whether a blocker's pull request is merged;
  `jig next` and `jig status` cannot (no host call in a read), so a ticket whose
  blocker published with an unmerged pull request is `ready: yes` in `jig next`
  and warned or refused at claim.
- The status `state:` line (`building`, `green`, ...) and `jig next`'s `state`
  still come from `ready.Tickets` and `RenderStatus`, which ignore the brief's
  approval and an open round, as before.
- `TestDigesterFlow` compares the working tree of the repository it runs from
  before and after, so a file edited while the e2e suite runs fails it; run the
  suite without editing the tree.

## Verification

Gates: `go build ./...`, `go vet ./...` (also `GOOS=linux`, `GOOS=darwin`),
`gofmt -l .`, `go test ./lint ./cmd/jig ./internal/phase ./internal/ready
./internal/mcp ./internal/verifydeliver ./internal/tui`, `go test ./e2e` (the
whole package passed; the ticket's own are `-run
'TestNextNamesPhaseAndPrompt|TestHintsMatchRefusals'`).

Test functions per package, before and after: `internal/phase` 0 to 13,
`cmd/jig` +4 (`phase_facts_test.go`), `e2e` +2, `internal/verifydeliver` +1;
none deleted. `internal/mcp` has one more row in an existing table.
