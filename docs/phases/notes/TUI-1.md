# TUI-1: a sub-steps block, activity that names its sub-step, and the TUI agrees with `jig next`

For the DOCS-1 ticket. Nothing in README.md, ARCHITECTURE.md, CONTEXT.md,
DECISIONS.md or docs/adr was edited; this file says what they need. Decisions 35
and 40. Branch `ph/TUI-1`.

## What is now true

- The Steps panel is `internal/phase`'s answer. The screens gather the facts from
  what they hold (the output of `jig status`, the ticket's journal lines, the
  roster) and call `phase.Derive`; a step is done, current or pending exactly
  when the phase is. The TUI derives nothing of its own about where a ticket
  stands, so it cannot disagree with `jig next`, `jig status` or the refusals.
- The ticket screen shows a sub-steps block under the Steps panel: the sub-steps
  of the step under the cursor (the current step until the cursor moves), framed
  like the other panels, as a checklist.
- Every activity line names the sub-step it advances, in the words of the block,
  and every kind of journal line has words of its own.
- `jig tui <T>` waits for a ticket the store would mint and has not minted yet.
- The main screen stays the project list. It reads no status, and it has no
  sub-steps.

## The ticket screen

```
jig  demo › SHOP-1                                             ◑ build verifying
────────────────────────────────────────────────────────────────────────────────
  ╭─ Steps ────────────────────────────────────────────────────── step 2 of 4 ─╮
  │  ✓ 1 Brief            approved by Dana (person) · 3 sections, 3 slices     │
  │▸ ◑ 2 Build            1 of 3 green · verifying slice clamp at bbbbbbb      │
  │  ○ 3 Review           after the build                                      │
  │  ○ 4 Publish          after a clean review                                 │
  ╰────────────────────────────────────────────────────────────────────────────╯
  ╭─ 2 Build · sub-steps ──────────────────────────────────────── 1 of 3 done ─╮
  │  ✓ slice round · verified aaaaaaa                                          │
  │  ◐ slice clamp · verifying bbbbbbb                                         │
  │  ○ slice cart · waits for clamp                                            │
  ╰────────────────────────────────────────────────────────────────────────────╯

  next  Run `jig next SHOP-1` to see what is ready, then `jig claim SHOP-1 --sl…

  ╭─ 2 Build · activity ─────────────────────────────────────── latest 3 of 6 ─╮
  │  09:01:07 ● slice round · verified aaaaaaa                                 │
  │  09:02:00 ◐ slice clamp · builder took it · Clamp negative quantities      │
  │  09:02:10 ◑ slice clamp · verifying bbbbbbb “Clamp negative quantities”    │
  ╰────────────────────────────────────────────────────────────────────────────╯
```

Top to bottom: the Steps panel, the sub-steps block, the rows that need a person
("needs you"), the "next" line (the status's first help line, which ends in the
prompt), the activity panel. This is `internal/tui/testdata/ticket-substeps.golden`.

### The block

- Title `<n> <Step> · sub-steps`, right side `<done> of <total> done`. The step is
  the one under the cursor (`ticketFocus`): the current step while the cursor
  follows it, the build for a question row, the review for an ask row.
- One line per `phase.Sub`: an icon for its state, its `Label`, and its `Detail`
  after a `·`. A phase with no sub-steps (an adopted ticket's brief, a build with
  no slices) has no block.
- Icons: done is `✓`; active is `◐` (`◎` for a round, `▲` in the publish);
  needs-you is `?` (`!` for the asks); failed is `✖`; waiting and pending are
  `○`. The icons are existing states (`StateOK`, `StateBuilding`,
  `StateReviewing`, `StatePublishing`, `StateNeedsInput`, `StateAsk`,
  `StateStalled`, `StateQueued`): no new state, and color is decoration only.
- The lines are `boxAdd` lines with no row reference, so `ticketFocus` and
  `followSel` count the same rows as before: the steps, then the open questions,
  then the asks (`TestTicketScreenShowsTheSubstepsOfTheStepUnderTheCursor`
  checks the page has four cursor rows).
- Past six sub-steps (or past the room there is), it shows the ones that matter,
  in their own order, and a dim `+N more` line. Weight, lowest first: needs-you
  and failed, active, waiting, pending (the next to come first), then done (the
  latest first). With room for one line only, that line is the top sub-step and
  there is no `+N more` line.

### The height budget

`ticketPage` works out the heights in this order (`bodyRows()` is 19 at 80x24):

1. The Steps panel (6 lines), the "needs you" group and the "next" group are
   fixed.
2. The activity panel keeps its floor: `2 + min(3, max(1, its lines))`, its
   frame and three lines.
3. The block takes what is left, up to `substepLines` (6) sub-steps and its
   frame, and only when at least 3 lines are left (frame and one line).
   `spare = body - fixed - activity floor`; `block = min(8, spare, subs + 2)`.
4. What still remains buys blank lines, in this order: between the "needs you"
   group and the "next" line, before the first group, before the activity panel.
   Blank lines separate text from text and are the first thing to go; a box edge
   needs none.
5. The activity panel then takes `min(8, max(3, body - lines above it - 2))` as
   before.

So at 80x24 the JIG-1 golden (a build with a question and an open "next" line)
shows two sub-steps and `+2 more`, the activity keeps its three lines, and the
screen is exactly 24 lines. Below the floor (a terminal shorter than the fixed
groups need) the page overflows and `window` scrolls, as before. Screens that
take body rows away (the tail pane, the answer prompt, a command's output) leave
less spare: the answer prompt at 24 rows shows one sub-step and no count.
`TestTheTicketScreenFitsWhateverTheStepsAreDoing` pins 24 lines with the three
panels closed, and `TestSubstepsListAtMostSixAndTheActivityKeepsThree` pins the
cap and the floor at heights 24 and 40.

## The activity lines

A line is `time icon label · text`. `label` is the key of the sub-step the line
advances, in the words the block has for it (`subLabelsOf`: the labels of
`phase.Derive`'s sub-steps, so they cannot drift), with fallbacks (`fallbackLabels`)
for a sub-step the block no longer lists (the asks of a round that closed clean).
`TestActivityLabelsAreTheWordsOfTheSubstepsBlock` checks the fallbacks against the
phase's own words.

| Line | Sub-step key | Label |
|---|---|---|
| claim, release, dispatch, verify-pending, oracle, verified, verify-error, question, answer, stall, requeue, result, recorded, note (with a slice) | `slice:<id>` | `slice <id>` (`fix slice <id> (round n)` for a fix slice) |
| approval | `approved` | `brief approved` |
| revalidate | `slices` | `slices written` |
| gate-open, gate-reviewer, gate-round, gate-clean | `round:<n>` | `round <n>` (n is the line's attempt, else the round after the ones closed before it) |
| triage | `asks` | `asks` |
| fix-slice | `fixes` | `fixes` |
| pr-body, publish-picks | `body` | `body` |
| publish-approved | `approval` | `approval` |
| pr, squash | `pr` | `pull request` |
| changelog, memorize | `changelog` | `changelog` |
| publish-done | `done` | `published` |
| anything else (adopt, route, env-*, driver, reconcile, lease-recovered, a note with no slice) | none | its step, lower case: `build`, `review`, ... |

`say()` is rewritten to follow the label: `builder took it`, `verified aaaaaaa`,
`verifying bbbbbbb “subject”`, `` `go test` passed in 3s ``, `builder asks: ...`,
`you answered the question: ...`, `opened`, `reviewer took it`, `reviewer
finished with findings`, `reviewer finished: clean`, `you dismissed “title”: why`,
`sent back fix-1: goal`, `written for ccccccc`, `you approved ccccccc`, `opened
URL`, `written` (changelog), `finished` (publish-done). The kinds that had no case
now have words: adopt (`the ticket took over <branch>`), route, squash, memorize,
publish-picks (`demo picked|reused|refused: ...`), result, recorded, env-up,
env-down, env-unavailable, driver, reconcile, lease-recovered.

The slice screen's timeline and the build screen's per-slice notes use the text
without the label (`timelineLine`, `say`): the slice is already on the row above.
The brief, build, review and publish screens' activity panels carry the labels.

## Steps from `internal/phase`

`standing.go` (`factsOf`) builds `phase.Facts` and the `[]journal.Line`:

| Fact | From |
|---|---|
| Ticket | the ticket id |
| Adopted, Branch | the status's `branch:` line (printed only for an adopted ticket) |
| BriefExists, Sections, Approval, ApprovedBy, ApprovedHow | the status's `brief` block and `brief_sections` table |
| Slices (ID, State, Goal, BlockedBy) | the status's `slices` table; `FromGate` from the `fix-slice` lines (their attempt, else the round closed by then); `OpenQuestion` from the open questions |
| Asks | the status's `outstanding_asks` ids; `internal/phase` reads each one's decision from the triage lines |
| Open | `phase.OpenRoundIn(lines)` |
| Rounds | left empty: the journal's gate lines decide |
| Published | a `publish-done` line |
| Blockers | not known (see the findings) |
| SessionName | the screens' own words for a session (`whoWith`: `reviewer (Claude Code · Opus 5)`) |

With no status read yet (the main screen reads none), the facts are the slices the
journal names in the states it shows, an approval line as the brief's approval,
and, for a review with no slice ever built, an adopted branch. `rosterState` words
the brief row of a ticket the journal shows nothing of (`drafted · waiting for your
approval`, `its slices.yaml cannot be read`, `reading`), as before.

`steps.go` maps the result onto the rows: `Done`, `Text` and the order are the
phase's; the row's `State` (the icon) is the screens' own, from the phase's state
and the slices' states; `Took` is as before. Two texts are the screens' own: a
review that was clean while a step before it is no longer done says `round N was
clean, but an earlier step changed since`, and an unread brief is worded from the
roster.

### Wording that changed, because the row texts are the phase's now

| Was | Is |
|---|---|
| `round 1 found 2 to decide · 2 wait for your decision` | `round 1 left 2 asks · 2 wait for your decision` |
| `ready for a fresh session to review the change (g opens a round)` | `ready for a fresh session to review the change` |
| `round 1's fixes are green · g runs round 2` | `round 1's fixes are green · round 2 is next` |
| `round 1's asks are decided · g runs round 2` | `round 1's asks are decided · round 2 applies them` |
| `body written · waiting for your approval (p)` | `body written · waiting for your approval` |
| `ready: p approves it, pushes the branch and opens the pull request` | `ready: approve it, push the branch and open the pull request` |
| `approved by you · pushing and opening the pull request` | `approved · pushing and opening the pull request` |
| `approved by Ada` | `approved by Ada (person)` or `(agent)`: how it was recorded |
| `N slices to build, starting with slice a` | `0 of N green` |
| `builder is on slice b` (in a build's focus) | `slice b is being built` |
| `verifying slice b at bbbbbbb “subject”` (the row) | `verifying slice b at bbbbbbb` (the block and the activity keep the subject) |

The footer's key list names the keys, so the hints in the rows went. A step is
also no longer done on a `pr` line alone: it is done at `publish-done`, as
`jig next` says, so the window between the pull request and the end of the
publish reads as `publish` current.

Decisions of a round already closed are spent in the screens' ask rows too
(`ticket.asks()`): a triage line counts only when it is after the last
`gate-round` or `gate-clean` line, the rule `internal/phase` and `jig status` use,
so the rows and the Review step never disagree about which asks wait for a person
(`TestADecisionARoundHasClosedSinceIsSpent`).

## `jig tui <T>` for a ticket not minted yet

- `cmd/jig/tui.go`: when the store has no folder for the id, `mintableKey(id)`
  says whether it is shaped like an id the store mints (`checkTicketID` and
  `store.ParseTicketID`). If its key is declared (`cfg.Keys`), the screens open
  with `Config.Awaiting` set. A malformed id is refused as before
  (`VALIDATION_ERROR`, exit 2, the not-found help); a well-formed id under a key
  the project does not declare is refused with `cfg.ResolveKey`'s error, which
  lists the declared keys. No other verb changed: `requireTicket` is untouched.
- `internal/tui` (`Config.Awaiting`, `ticket.awaiting`): the ticket screen reads
  `waiting for SHOP-1 to be created` and a dim line under it; the header says
  `○ waiting`; the footer lists `esc back · t tail · q quit`. Nothing of the
  ticket is read meanwhile: no `jig status`, so no refusal and no notice. `g` and
  `p` say `SHOP-1 does not exist yet` and run nothing.
- It fills in when the ticket has a journal line (any line the stream brings for
  it) or a row in the ticket list. The ticket list is read every
  `Config.AwaitEvery` (2s; negative never, which a test that drives the clock
  uses) while the ticket is awaited, so a ticket that exists with no line yet
  shows within two seconds. The status is read at once when it appears.
- Tests: `TestATicketThatDoesNotExistYetIsWaitedFor` (the `ticket-waiting`
  golden, no status read, the keys refused), `TestAWaitedTicketFillsInWhenTheTicketListNamesIt`,
  `TestAWaitedTicketFillsInOnItsFirstLine`, `TestATicketOpenedTheUsualWayDoesNotWait`,
  `cmd/jig` `TestTUIWaitsForATicketToBeCreated`, `e2e`
  `TestTUIWaitsForATicketThatIsNotMintedYet` (the real binary, the real
  `jig ticket new`, three refusals).

## API added

- `internal/tui`: `Config.Awaiting`, `Config.AwaitEvery`; `Model.Standing(ticket)`
  and `StandingOf(ticket, statusOutput, lines)`, which return `Standing{Current,
  Ready, States}`: the phase the Steps panel marks current, whether it can start,
  and each phase's state. `StandingOf` is the whole path from the text `jig
  status` printed and the journal to the panel, with no terminal and no store, so
  a test in another package can compare it with `jig next`.
- `internal/phase`: `Facts.SessionName func(id string) string` (additive; nil is
  the first eight characters, as before). It words the session in the sub-step
  details and the row texts, so the screens say `reviewer (Claude Code · Opus 5)`
  where `jig next` says `s-rev`.

## Tests

New (14 in `internal/tui`, 1 in `cmd/jig`, 2 in `e2e`):

- `TestTUIAndNextAgree` (`e2e/tui_phase_test.go`, the brief's test): a fixture
  ticket goes through a drafted brief, an approved one, a claim, a build done,
  round 1 open, round 1 closed with an ask, the ask dismissed, round 2 open, a
  clean round, and a published ticket. At each state the screens' current step,
  ready flag and four phase states (`tui.StandingOf` over the real `jig status`
  output and the real journal) equal `jig next --json`'s `phase`.
- `TestTheScreensStandWhereInternalPhaseSaysTheTicketStands` (`internal/tui`): the
  same states from hand-written `phase.Facts` and journal lines (a status is
  printed from the facts): the screens' standing, each step's state and each
  row's text equal `phase.Derive` on the same facts.
- `TestAStatusNobodyReadYetStandsOnTheJournalAlone`,
  `TestADecisionARoundHasClosedSinceIsSpent`.
- Sub-steps: `TestTicketScreenShowsTheSubstepsOfTheStepUnderTheCursor`
  (golden `ticket-substeps`, a build in progress with 3 slices, and the block
  following the cursor to every step), `TestSubstepsListAtMostSixAndTheActivityKeepsThree`,
  `TestTheTicketScreenFitsWhateverTheStepsAreDoing`,
  `TestPickingSubstepsKeepsWhatNeedsAPersonAndWhatIsUnderWay`.
- Activity labels: `TestEveryKindOfLineNamesTheSubstepItAdvances` (35 kinds of
  line, the label and the words of each, none the raw event name),
  `TestALineWithNoAttemptNamesTheRoundByItsPlace`,
  `TestActivityLabelsAreTheWordsOfTheSubstepsBlock`.
- Waiting: the five above.

Goldens: `ticket`, `ticket-asks`, `build`, `brief`, `review`, `review-published`,
`publish`, `publish-confirm`, `answer-prompt`, `tail`, `slice`, `panel-ok` and
`panel-fail` changed (activity lines, the sub-steps block, the lines that lost
their blanks, the review and brief row texts). `main` is unchanged. New:
`ticket-substeps`, `ticket-waiting`. Each diff was read by eye: no row overflows
at 80x24, every ticket screen is 24 lines, and the answer prompt's activity panel
still shows its bottom edge.

Existing tests whose expected text changed (none deleted):
`TestFixesGreenAfterARoundAskForTheNextRound` (`round 1's fixes are green · round
2 is next`), `TestTheLastRowShowsThePanelBelowIt` (`slice c · builder asks: Formal
or casual?`), `TestTheReviewScreenNamesWhatARoundSentBack` (`fixes · sent back
fix-1: ...`), `TestAReviewerTakingTheRoundShowsOnTheReviewStep` (says `opened` and
`reviewer took it`, and the line names `round 1`); `cmd/jig`
`TestTUIShowsTheStoreAndEndsOnQ` waits for `slice a is being built`,
`TestTUIRefusesWhatItCannotOpen` replaced its `JIG-99` row (that id is now waited
for) with a malformed id and an undeclared key; `e2e` `TestTUIHandBuiltToPublish`
waits for `round 1 left 1 ask`.

Test functions per package, before (the cut, cb0e54b) and after: `internal/tui`
77 to 91, `cmd/jig` +1 of mine (`TestTUIWaitsForATicketToBeCreated`), `e2e` +2 of
mine, `internal/phase` 13 to 13. None deleted.

## Decisions I took

1. **The row texts are the phase's.** The brief asked to keep the wording where
   it was right; the phase already says the same thing in nearly every case, and
   where it differs (the table above) one source is worth the changed words. The
   key hints in the rows went because the footer names the keys.
2. **The block's six is six sub-steps, not six lines.** A publish has five
   sub-steps and a build of five slices should show all of them when there is
   room. The room, not the six, is what limits it at 80x24.
3. **Blank lines are dropped before the block shrinks.** The brief gave the
   block a floor and the activity a floor, and at 80x24 there is no room for both
   and the old blank lines. A box edge already separates a panel from text, so
   the blanks next to boxes go first.
4. **`StateOK` for a brief or build whose own work is done while an earlier step
   is not** (as before); the phase says `pending` for it and the row is dimmed.
5. **Unread tickets keep their roster wording.** The main screen reads no status,
   so the brief of a ticket it knows nothing of is worded from `jig next`'s state.

## Findings outside the brief (not fixed)

- The screens know no blockers: `jig status` prints none, so a ticket held up by
  another ticket's publish reads as ready on the screens and as `PHASE_BLOCKED` in
  `jig next`. A status section for it (or `phase` in the roster, which PH-1 left
  out for its cost) would close it.
- The open round comes from the journal's `gate-open` line, as it did. A gate
  that failed on its oracles writes that line and opens nothing; the screens show
  `round N open` where `jig next` has no open round. `jig status` could print the
  open record.
- `ticket.phase()` in `state.go` (a state and a label for a ticket, from the
  journal) is used only by its tests now; the header and the main screen use
  `stepLabel`. It can go with its tests.
- A pull request recorded with `publish --pr` before `publish-done` leaves the
  screens at `publish` current until the done line, as `jig next` does; a
  hand-written journal with a `pr` line and no `publish-done` line used to read
  as published on the screens and does not any more.
- Reading the ticket list every two seconds while a ticket is awaited runs `jig
  next --json` in process each time (it reads the store and the clones). It stops
  as soon as the ticket exists.

## Verification

Gates (from the worktree): `go build ./...`, `go vet ./...` (also `GOOS=linux`,
`GOOS=darwin`), `gofmt -l .`, `go test -count=1 ./lint ./cmd/jig ./internal/tui
./internal/phase`, `go test -count=1 ./e2e -run TestTUI` (`TestTUIHandBuiltToPublish`,
`TestTUIAndNextAgree`, `TestTUIWaitsForATicketThatIsNotMintedYet`).

## ARCHITECTURE row for the module table (the `internal/tui/` paragraph)

| `internal/tui/` | `Model`, `New`, `Config`, `Source`, `Runner`, `Run`, `Look`, `Standing`, `StandingOf`, the `State*` names | the event stream (`events.Envelope`s from a `Source`) and the output of the verbs it runs (`jig status`, `jig next --json`) → seven screens (project, ticket, brief, build, review, publish, slice); a ticket's four steps in order, which are `internal/phase`'s answer for the facts the screens hold (the status and the journal), so the screens and `jig next` never disagree about where a ticket stands; under them, for the step under the cursor, a sub-steps block (at most six sub-steps with a state, a label and a few words each, the ones that matter first and a count of the rest, taking its room from the activity panel, which keeps three lines), then its activity, each line naming the sub-step it advances in the block's words; drawn with an icon and a color per state, and keys that act through verbs (answer as a person, keep or dismiss an ask, gate, publish: approve, then yes); `jig tui <T>` on a ticket the store would mint and has not yet (a well-formed id under a declared key) opens a screen that waits for it and fills in when the ticket has a line or a row in the ticket list; on a terminal it takes the screen, on a pipe it prints each screen whole; it reads no store (a test pins that) |
