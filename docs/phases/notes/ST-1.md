# ST-1: `jig status` names a ticket nothing has started

For the DOCS-1 ticket. Nothing in README.md, ARCHITECTURE.md, CONTEXT.md,
DECISIONS.md or docs/adr was edited; this file says what they need. RUN-3 (old
T-25), re-cut for the digester: "dispatched" reads "claimed". Branch `ph/ST-1`.

## What is now true

`jig status <T>` printed `state: building` for a ticket nothing had started: a
ticket just made by `jig ticket new`, and one whose slices were all queued. The
`state:` line now says so. No verb, flag, column or journal shape changed; only
two new words can appear on the `state:` line.

## The states

The `state:` line, in order of precedence (the first that holds wins):

| State | Holds when |
|---|---|
| `stalled` | a slice is stalled (unchanged) |
| `parked` | a slice is `needs-input` (unchanged) |
| `env-blocked` | a slice is `env-blocked` (unchanged) |
| `green` | every slice is green, or the ticket adopted a branch and has no slices (unchanged) |
| `new` | the ticket has no slices and adopted no branch |
| `queued` | it has slices, every one is `queued`, none has an attempt, and the journal shows no slice ever claimed or verified |
| `building` | everything else: a slice was claimed, or has a verification, or is past queued |

`building` now means a slice was taken up. Three things count as taking one up:

- a `claim` line (or a `dispatch` line, the claim of the era before claims),
  even if a `release` followed: the slice is `queued` again and the ticket is
  still `building`, since a claim happened;
- a verification line: `verify-pending`, `oracle`, `verified` or `verify-error`;
- a slice state past queued (building, green), or an attempt on a queued slice
  (the dispatch era's record), which need no journal read.

An approval line, a note, a question or the lines that only follow a claim (a
stall, a requeue, a result) do not make a ticket `building` by themselves.

An adopted ticket with no slices keeps reading `green`; one whose gate round
queued fix slices that nobody claimed reads `queued`.

### Agreement with `internal/phase` and `internal/ready`

- `new` is the ticket whose phase is still the brief ("being drafted" and no
  slices), and the status help is `getWorkHints`, as before.
- `queued` has the build ahead of it: with the brief approved, the current phase
  `phase.Derive` gives is build; with the brief drafted and not approved it is
  still brief.
- `building` is a ticket in the build, a slice released after its claim included.
- `internal/ready` keeps its own words for `jig next` (`done`, `blocked`, `ready`,
  `active`, `green`, `new`). `new` is the same word and the same meaning there
  (no slices; see the finding below for the adopted case). `queued` and
  `building` are about a ticket's slices, which `jig next` answers per slice, so
  neither is added there.

The rule lives in `cmd/jig/status.go` (`overallState`), with one helper in the
phase package, `phase.EverClaimed(lines []journal.Line) bool`: pure, over the
journal lines, true when any line is a claim, dispatch or verification line. It
reads the journal only when every slice is queued with no attempt (the one case
the store alone cannot tell), so a status of a ticket with work in flight reads
no more than it did.

## Consumers

- **The TUI** (`internal/tui/statusinfo.go`, `state.go`): `statusInfo.State`
  lists the new words; `ticket.phase` takes `new` and `queued` as `StateQueued`
  drawn with the word the status used ("new", "queued"), not `building`. No
  screen golden shows the status word, so none changed.
- **The mirror** (`internal/mirror/status.go`): `TicketStatus` does not read
  `jig status`; it derives the board status from pull requests, a non-empty
  journal and any slice past queued. It is unchanged, so a ticket whose slices
  are queued stays Briefed (or Backlog with no brief) as long as its journal is
  empty. `TestTicketStatus` gained a case that pins it.
- **`jig next`'s renderers** print `ready.State*`, not the status word; nothing
  to change.
- Skills and prompts that read `state:` from `jig status`: none parse it (the
  hint line is what they follow).

## Tests

- `e2e/status_test.go` `TestStatusNamesATicketNothingStarted` (through the
  binary): the fixture's ticket with four queued slices reads `queued`; a ticket
  from `jig ticket new` reads `new`; with a slices.yaml it reads `queued`; after
  `jig claim` it reads `building`; after `jig claim --release` (the slice queued
  again) it still reads `building`.
- `cmd/jig/status_state_test.go` `TestStatusStateOfATicketNothingHasStarted`:
  new, queued (approved and drafted brief), a claim, a claim then release, a
  pending verification, an attempt on a queued slice, a slice building or green
  with another queued, the worse states still outranking, an adopted ticket
  with no slices staying `green`; and for the first six the phase
  `internal/phase` derives (brief for new, build for the others, brief for a
  drafted unapproved brief).
- `internal/phase/phase_test.go` `TestEverClaimed`.
- `internal/tui/state_test.go` `TestPhaseDoesNotCallATicketNothingStartedBuilding`.
- `internal/mirror/status_test.go`: a subtest of `TestTicketStatus`
  ("Briefed while its slices are all queued"); the next subtest no longer
  appends the slice (it already exists), and asserts what it did.
- No test was deleted. Every existing assertion on `state: building` is on a
  ticket with a green slice or on a claim/answer output, so none changed.

## Decisions I took

1. **"Ever claimed" is read from the journal, not from the slice state.** The
   brief says `queued` is "none has ever been claimed", and a release puts a
   claimed slice back to `queued`, so the store's state cannot tell. The lines
   counted are the claim, dispatch and verification lines only, not the whole of
   `phase`'s `buildEvents`, so a `fix-slice` line (a round queued fixes on an
   adopted ticket and nobody has claimed them) leaves the ticket `queued`.
2. **A queued slice with an attempt is `building`.** The old brief's rule ("no
   slice has an attempt") covers tickets built in the dispatch era, whose requeued
   slices are queued again with their attempts on record.
3. **`new` before `queued`, after the worse states.** A ticket with no slices has
   no slice that could be stalled or parked, so the order only matters for
   `green` (an adopted ticket), which stays first.
4. **The TUI draws `new` and `queued` as the queued look**, with the status's own
   word as the label: the building look means work under way, which these tickets
   have none of.

## Findings outside the brief (not fixed)

- `jig next`'s roster reads `new` for an adopted ticket with no slices
  (`ready.Tickets` counts slices only), while `jig status` reads `green` for it.
  The brief keeps status as it is.
- The mirror's `TicketStatus` reads `In progress` for any ticket whose journal
  has a line at all, so a queued ticket whose brief was approved (an `approval`
  line) is already In progress on the board, not Briefed. The brief says keep the
  mapping stable, so it is.
- `internal/tui`'s `ticket.phase` is not called by any screen since the steps
  came from `internal/phase` (TUI-1); only its tests call it. The status word has
  no visible effect in the TUI today.

## ARCHITECTURE text

For the `jig status` row (or its prose): "`state:` is the ticket's one word:
`stalled`, `parked`, `env-blocked` or `green` as before, else `new` (no slices,
no adopted branch), `queued` (slices, none ever claimed or verified) or
`building` (a slice was claimed or has a verification)."

For the `internal/phase` row: add "`EverClaimed(lines)`: whether any slice was
ever claimed or verified, which `jig status` reads to tell `queued` from
`building`."

## Verification

`go build ./...`, `go vet ./...` on linux, darwin and windows, `gofmt -l .`,
`go test -count=1 ./lint ./cmd/jig ./internal/mirror/... ./internal/tui
./internal/phase`, and `go test -count=1 ./e2e` (the whole package, not only
the ticket's tests) pass.
