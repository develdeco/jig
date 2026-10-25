# GR-8 (GRAPH-8, old T-32): close a ticket without a pull request

Notes for the DOCS-1 ticket. Nothing in README.md, ARCHITECTURE.md, CONTEXT.md,
DECISIONS.md or docs/adr was edited; the text they need is at the end.

The approved brief is `jig-tickets/tickets/GRAPH-8/brief.md` (user-confirmed 2026-10-07). It
predates the digester and the v3 store, so the refusers are the digester verbs (`jig run` and
`jig solve` are gone) and the write goes through `Store.Claim`. A ticket that another change
covered, that another ticket replaced, or that the owner decided not to do now has a state of its
own: it is closed, it cannot be built, its issue closes on the mirror and stays closed.

## The record: `closed:` in ticket.yaml

An optional map, absent while the ticket is open.

```yaml
closed:
  reason: covered          # covered | superseded | not-planned
  by: develdeco/jig#82     # required for covered and superseded, absent for not-planned
  note: The demo kit shipped it.   # optional free text
  at: "2026-10-25"         # the UTC day it was closed
```

- `store.Ticket.Closed *Closed` (`internal/store/closed.go`; nil is open). `ticketFile` carries
  `closed,omitempty`, read by `ReadTicketFile` and written by `writeTicketFile`, so every
  `mutateTicket` caller (branch, rank, aliases, the migration) keeps the entry.
- **No schema bump.** `schema_version` stays 1. `ReadTicket` decodes strictly (`KnownFields`), so a
  jig that predates this ticket fails to read a closed ticket's record with
  `TICKET_RECORD_INVALID` ("upgrade jig"), which is the brief's intent: an old build cannot build
  a closed ticket by accident. A `closed:` entry with an unknown key inside it is refused the same
  way (`TestReadTicketRefusesAnUnknownKeyOfTheClosure`). Nothing is repurposed, so a version bump
  would only have made old jigs fail louder.
- Who decided is the store's git history; the record does not repeat it.
- `store.Closed` has `Check()` (the record's own rules, no store read), `Phrase()` ("covered by
  develdeco/jig#82", "superseded by SHOP-4", "not planned"), `NeedsBy(reason)` and
  `Releases(reason)` (see the neighbors below).
- `Store.CloseTicket(ticket, Closed)` and `Store.ReopenTicket(ticket)` write through a new
  `updateTicket` (a `mutateTicket` whose function can refuse: nothing is written then).
  `CloseTicket` on a closed ticket is `TICKET_ALREADY_CLOSED`, `ReopenTicket` on an open one is
  `TICKET_NOT_CLOSED`, each with help. They only write the file: the verbs commit it through
  `Claim`, staging `Store.TicketFileRelPath` (T-20's helper) alone.

## The verbs: `jig close` and `jig reopen`

Registered in `cmd/jig/close.go`'s `init()` (Orders 420 and 421).

| Call | Effect |
|---|---|
| `jig close <T> --reason covered\|superseded\|not-planned [--by <ref>] [--note <text>]` | writes the entry, commits that ticket's record alone, pushes it |
| `jig reopen <T>` | removes the entry the same way |
| flags | `--reason`, `--by`, `--note` (close only), `--store`, `--project` (both) |

- The ticket may be an alias (`resolveTicketArg`). `--reason` is required; `--by` is required for
  `covered` (a reference such as `develdeco/jig#82` or a commit) and for `superseded` (a ticket of
  the store, checked: it is resolved through `ResolveTicket`, so an old id is written as the
  current one; the ticket itself is refused), and refused for `not-planned`. All are
  `VALIDATION_ERROR` (exit 2) that write and push nothing, with help in the flags' words
  (`needs --by`).
- Each runs `st.Sync()`, then commits through `Store.Claim`, as `jig ticket new` and `jig rank` do:
  with an origin the commit is pushed at once and a rejected push is undone and retried (the write
  re-reads the record); a store with no origin commits. Subjects: `JIG-2: closed, covered by
  develdeco/jig#82`, `JIG-2: reopened`. Then `st.RunCheckpointHook()`, since `Claim` never runs it:
  the mirror closes (or reopens) the issue at once.
- Closing a closed ticket, or reopening an open one, is refused with help (above).
- Output:

  ```
  closed:
    ticket: JIG-2
    reason: covered
    by: develdeco/jig#82
    note: The demo kit shipped it.
    at: 2026-10-25
  help[2]:
    JIG-2 cannot be built while it is closed, and the tickets that wait for it go on
    Take it back with `jig reopen JIG-2`
  ```

  For `not-planned` the first help line ends "stay held: they need planning again".
  `jig reopen` prints `reopened:` with the ticket and the help `Run jig next <T> to see where it
  stands`.

## The refusals: `TICKET_CLOSED`

`requireOpen(st, ticket)` (`cmd/jig/close.go`) reads the record and refuses a closed ticket with
`TICKET_CLOSED` (exit 1): "JIG-2 is closed (covered by develdeco/jig#82)", help "Run `jig reopen
JIG-2` to work it again". It runs first in:

- `jig claim` (after the ticket resolves). `jig claim --release` is not refused: a session may let
  go of a slice it holds on a ticket closed under it;
- `jig gate` (every mode, `--branch` included);
- `jig publish` (every mode, `--body` and `--pr` included);
- `jig requeue`;
- `jig review context`.

`jig validate`, `jig status` and `jig next` still work.

## Readiness: `jig next`, `internal/ready`, the claim

- `ready.StateClosed = "closed"` wins over every other state. `ready.Ticket.Closed *store.Closed`
  is filled by `readTicketData` (and for a blocker by `blockerInput`). A closed ticket offers no
  slices, lists no blockers (nothing of it waits for anything), and is never in the `next` order of
  ready tickets.
- **A closed blocker is judged by its reason, whatever the blocker's kind and whatever merge was
  read for it** (`ready.unmet`): `covered` and `superseded` are met (they release the tickets that
  wait for them, with no merge to look for), `not-planned` is unmet with the reason
  `ready.NotPlanned` ("it was closed as not planned, so this ticket needs planning again"). That
  is A2's (T-18) neighbor rule, user-confirmed in the brief. `readMerges` skips a closed ticket
  and a closed blocker.
- `jig claim` applies the same rule (`blockerProblem`, `cmd/jig/claim_checks.go`): a covered or
  superseded blocker is met; a not-planned one is the new problem `CLAIM_BLOCKER_NOT_PLANNED`
  ("JIG-2 waits for JIG-1, which was closed as not planned"; help: plan JIG-2 again without it, or
  `jig reopen JIG-1`). The wording is a choice of mine: the brief leaves "how A2 reports that" to
  A2. It is a problem like the other claim problems: a warning for a person at the keyboard, a
  refusal under `--unattended`.
- `jig next` (no ticket): the row's `state` is `closed`, with a `closed` field ("covered by
  develdeco/jig#82", `--json` and the human block) and `ready: []`. `jig next <T>` prints
  `state: closed`, `closed: <phrase>` and the help "JIG-2 is closed (...): run `jig reopen JIG-2`
  to work it again, or `jig next` to see a ticket that is ready". A ticket that waits for a
  not-planned one lists the blocker with the fact "closed as not planned".
- **Choice (brief: "name it"):** superseded points at its replacement through `by`; a ticket that
  waited for the superseded one is released but not re-pointed at the replacement. Whether it
  should wait for the replacement is a planning decision for the person, so the record does not
  guess it. The closing help says the waiting tickets "go on".

## `jig status` and `internal/phase`

- `jig status <T>`: `state: closed` wins over `stalled`, `parked`, `env-blocked`, `green`, `new`,
  `queued` and `building` (`renderStatus` decides it before `overallState`, so a closed ticket
  reads no journal for it). Right after it comes a `closed:` block with `reason`, `by`, `note` and
  `at` (each only when set), and the help is one line: "JIG-2 is closed (covered by ...): run `jig
  reopen JIG-2` to work it again". The slice tables stay as they were.
- `phase.Facts.Closed` (a phrase, "" while open; `phaseFactsOf` fills it). `phase.Derive` treats a
  closed ticket as done: the four phases are done whatever the journal says (as for a published
  one), the publish phase's text is `closed · covered by develdeco/jig#82`, the ticket is not
  `Ready`, its `Code` is the new `phase.CodeClosed = "PHASE_CLOSED"` with the reason "JIG-2 is
  closed (...): `jig reopen JIG-2` takes it back", and its `Prompt` is empty. `Ticket.For(p)` of
  any phase of a closed ticket is `PHASE_CLOSED` (no warning, no prompt). The sub-steps that were
  active, waiting or owed to the person are put at rest (pending), so no screen shows work on a
  ticket nobody works. A ticket closed after it published reads closed first (the closure wins).
  `jig next <T> --for <phase>` follows from this (`phaseAdvice` says the reason).
- The TUI reads it: `statusInfo.Closed` (parsed from the `closed` block of `jig status`) and
  `Model.factsOf` passes it to `phase.Facts.Closed`, so the phases the TUI derives read done and the
  publish step says `closed · ...`. No screen golden changed.

## The mirror

`internal/mirror` and `internal/mirror/github`.

- `TicketStatus`: a closure wins over pull requests and every other state. `covered` is `Done`
  (`mirror.StatusDone`); `superseded` and `not-planned` are `Not planned`
  (`mirror.StatusNotPlanned`). `IsOpen` is false for both. `ChartStatus`: a chart is Done when it
  has tickets and every one is Done or Not planned (a closed ticket is finished); `In progress`
  still needs a ticket In progress, In review or Done.
- The Status field gains the sixth option `Not planned` (GRAY, "Closed without being built") in
  `github.statusOptions`. `EnsureProject` adds a missing option while keeping every existing one
  by id, name, color and description, as it does for the other options, so the first sync that
  finds a project with five options adds it once and the next finds it
  (`TestEnsureProjectAddsTheNotPlannedOptionOnce`). `ProjectV2.StatusOptions` has six names.
- `updateIssues`: an issue whose status is `Done` is closed as completed (`CloseIssueCompleted`),
  one whose status is `Not planned` is closed as not planned (`CloseIssueNotPlanned`, a client
  method that already existed); one that is closed already is left alone; a ticket reopened
  (`jig reopen`) has its issue reopened (`ReopenIssue`) with the Status its state derives. A close
  is the store's word, so the mirror's own record agrees: **none of it is drift**.
- The issue body gains `**Closed:** covered by develdeco/jig#82` after the description (and
  before "Waits for"), then the note as a paragraph of its own when there is one. A superseded
  ticket names its replacement with the issue number when the replacement has an issue
  (`**Closed:** superseded by DEMO-4 (#4)`), as "Waits for" does. A reopened ticket's body loses
  the line on the next sync. Body edits are the usual `UpdateIssue`.

## Decisions I took

1. **The refusers** are `jig claim`, `gate`, `publish`, `requeue` and `review context`, the
   digester's verbs for the brief's `run`, `solve`, `gate`, `publish`, `requeue`. `claim
   --release` stays allowed.
2. **`superseded` stores the replacement's current id**, resolved through aliases, and cannot be
   the ticket itself.
3. **The schema** stays 1, and the strict decode is the guard (above). No migration: every old
   record has no `closed:` and reads as open.
4. **A not-planned blocker is a claim problem of its own** (`CLAIM_BLOCKER_NOT_PLANNED`) rather
   than the generic "blocker unmet" text, because the way out differs (re-plan, or reopen the
   blocker).
5. **A closed ticket is closed before it is published.** If a published ticket is then closed,
   closed wins; `jig reopen` takes it back to published.
6. **`at` is the UTC day**, `YYYY-MM-DD`; a hand-edited record may leave it out (`validate` only
   checks it parses when present).
7. **`jig validate` checks the entry** (`validateTicketClosed`): a known reason, `by` where the
   reason needs it (and none where it does not), an existing other ticket for `superseded`, a
   parseable `at`. Problems read `ticket.yaml: closed: ...`.

## Findings outside the brief (not fixed)

- Verbs that write to a ticket but do not refuse a closed one, as the brief lists only five:
  `jig brief approve`, `jig verify`, `jig answer`, `jig triage`, `jig ticket`, `jig rank`,
  `jig graduate`. Closing the ticket is the person's word that nothing is built; none of these
  builds. A later ticket can add `requireOpen` where a person would be surprised.
- `jig next`'s "Waiting for" fact for a ticket that waits on a covered blocker disappears at once,
  as the brief asks, but the ticket's own `blocked_by` entry stays in its ticket.yaml; nothing
  prunes it.
- The operator's step after this merges (not code, from the brief): record the closures made by
  hand before it, T-6, T-7 and T-8 as `covered` by develdeco/jig#89, #87 and #82, and T-11 as
  `not-planned` with a note naming the decision to record demos from e2e runs. T-5 stays open.

## Tests

- `e2e/close_test.go`, through the binary:
  - `TestClosedTicketCannotBeBuilt` (the critical path): `jig close` commits that ticket.yaml
    alone and pushes it to the store's origin; claim, gate, publish (and `--body`), requeue and
    review context refuse with `TICKET_CLOSED`, the closure and `jig reopen`; the slice is still
    queued and no claim line was written; `jig status` says `state: closed` with the closure;
    `jig next <T> --for build` says `closed`, nothing ready, `PHASE_CLOSED` for the phase and for
    `for`, no prompt; closing again is `TICKET_ALREADY_CLOSED`; `jig reopen` commits; reopening
    again is `TICKET_NOT_CLOSED`; the claim then works.
  - `TestNextTicketsWaitingForAClosedTicket`: a covered blocker releases (listing and an
    unattended claim with no warning); after reopen and a not-planned closure the waiting ticket
    is blocked, the listing says so, and the claim is `CLAIM_BLOCKER_NOT_PLANNED`.
  - `TestCloseRefusesWhatItCannotRecord`: no reason, an unknown reason, covered with no by,
    not-planned with a by, superseded by no ticket, by itself, a ticket the store lacks, reopen
    of a missing ticket: `VALIDATION_ERROR`, the origin does not move; `jig validate` reports a
    hand-edited superseded entry.
- `internal/mirror/closed_test.go` (the fake client): `TestClosedTicketIssueClosesOnSync` (covered
  closes as completed with Done, superseded and not-planned close as not planned with
  "Not planned", the body line and note, a later sync writes nothing, no drift, reopen reopens
  with Backlog); `TestClosedTicketStatus`. `internal/mirror/github/client_test.go`:
  `TestEnsureProjectAddsTheNotPlannedOptionOnce`, and the two existing option tests now expect six.
  `status_test.go`: the closed cases of `TestChartStatus` and `TestIsOpen`.
- `internal/store/closed_test.go`: the record round trip and that the rest of the record is kept
  (`TestCloseTicketRecordsTheClosureAndKeepsTheRest`), the refusals
  (`TestCloseAndReopenRefuseTheStateTheTicketIsAlreadyIn`), `TestClosedCheck`,
  `TestClosedPhraseAndReleases`, `TestReadTicketRefusesAnUnknownKeyOfTheClosure`.
- `cmd/jig/close_test.go`: `TestValidateTicketClosed`,
  `TestCheckClosureResolvesTheSupersedingTicket`.
- `internal/ready/ready_test.go` `TestTicketsClosed`; `internal/phase/phase_test.go`
  `TestClosedIsDoneWithNoPrompt`; `internal/tui/axi_test.go`
  `TestParseStatusReadsTheClosureOfAClosedTicket`.
- No test was deleted. Test functions added per package: store +5, mirror +2, mirror/github +1,
  ready +1, phase +1, tui +1, cmd/jig +2, e2e +3. `TestEnsureProject*` and the mirror's `fakeClient`
  changed their expectations from five Status options to six.

## ARCHITECTURE text

- `internal/store` row: add "`Closed` / `Store.CloseTicket` / `Store.ReopenTicket`: the `closed:`
  entry of ticket.yaml (reason covered, superseded or not-planned; by; note; at), written through
  `Claim` by `jig close` and `jig reopen`; `Closed.Releases` says whether the tickets that wait
  for a closed ticket go on (covered, superseded) or stay held (not-planned)".
- Verb table: "`jig close <T> --reason covered|superseded|not-planned [--by <ref>] [--note <text>]`
  closes a ticket without a pull request; `jig reopen <T>` takes it back. A closed ticket cannot
  be claimed, gated, published, requeued or reviewed (`TICKET_CLOSED`)."
- `internal/ready` row: "`StateClosed`: the ticket was closed without a pull request; it wins
  over every other state, offers no slices, and releases the tickets that wait for it when it is
  covered or superseded."
- `internal/phase` row: "`Facts.Closed`: a closed ticket is done whatever its journal says, with
  no prompt and `PHASE_CLOSED` for every phase."
- Mirror prose: "A ticket closed as covered is Done and its issue closes as completed; one closed
  as superseded or not planned has the Status Not planned (the sixth option, GRAY, 'Closed without
  being built') and its issue closes as not planned. The body says how it was closed. A chart is
  Done when each of its tickets is Done or Not planned."
- `jig status` row: add `closed` to the `state:` words (it wins over every other), and the
  `closed:` block.

CONTEXT.md vocabulary: **Closed ticket** - a ticket closed without a pull request for one of
three reasons: covered (another change did the work), superseded (another ticket replaces it) or
not planned (it will not be done). Its record carries a `closed:` entry; `jig reopen` removes it.

## Verification

`go build ./...`, `go vet ./...` on windows, linux and darwin, `gofmt -l .`,
`go test -count=1 ./lint ./cmd/jig ./internal/store ./internal/mirror/... ./internal/ready
./internal/phase ./internal/tui`, and `go test -count=1 ./e2e -run
'TestClosedTicketCannotBeBuilt|TestClosedTicketIssueClosesOnSync|TestNext|TestCloseRefuses'`
pass, after merging `phases` at 7318b12 (ST-1 and T-20).
