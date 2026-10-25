# DF-1: fixes from the trial of the automatic brief-to-PR run

For the DOCS-1 ticket. Nothing in README.md, ARCHITECTURE.md, CONTEXT.md,
DECISIONS.md or docs/adr was edited; this file says what they need. Decisions 31
(the branch), 36 to 40. Branch `ph/DF-1`.

The trial: one Claude Code session was told to take SHOP-1 from brief to PR and
ran the whole flow in twelve minutes. These are the nine defects it showed. No
verb, flag or journal shape was added or changed.

## What was built, fix by fix

### 1. The reviewer subagent runs in the foreground

- `skills/review/SKILL.md`: the not-fresh paragraph says to spawn the advised
  subagent "in the foreground: `run_in_background: false`", to wait for its verdict
  in the same turn, and never to end the turn while it runs ("a background
  subagent leaves the turn waiting and the run stalls").
- `skills/here/SKILL.md` (the automatic-run paragraph) and `StubBody`
  (`internal/setup/project.go`) say the same in one sentence.
- `cmd/jig/review_advice.go`: the second advice line of `jig gate` and `jig next`
  names `run_in_background: false` and adds "Run it in the foreground: wait for its
  verdict in this same turn, and never end the turn while it runs".
- Tests: `TestAdviceToSpawnNamesTheForegroundRule` (`cmd/jig/review_advice_test.go`,
  new: built, unknown and fresh sessions), the `spawn` list of
  `TestReviewFromBuildSessionIsAdvisedToSpawn` (`e2e`, checked in `jig next` and
  `jig gate`), and `TestStubBodyListsTheSkillsAndSendsToTheBinary`. The byte
  budget is `lint/skills_test.go` (review 5230, here 4879, intake 5968).

### 2. The planner mints a ticket, it does not make its folder

- `skills/intake/SKILL.md`: "If the ticket the person names does not exist, mint
  it with `jig ticket new --title "<title>"` and use the id it prints; never make a
  ticket's folder yourself." Wording trimmed elsewhere in the skill to stay within
  6000 bytes (nothing dropped but adjectives and small asides).
- `jig validate` adds the advisory `TICKET_NOT_MINTED` (`cmd/jig/validate_store.go`,
  `unmintedAdvisories`) for a ticket that has a `brief.md` and no `ticket.yaml`:
  message "not minted: T has a brief but no ticket.yaml, so it has no title, slug
  or minted id", help "Run `jig ticket new --title "<title>"`, which mints the id,
  and plan under the id it prints; never make a ticket's folder by hand". It is
  first in the `advisories` table. An advisory does not fail: the fixture ticket
  has no record and still validates (`TestValidateFixture` now asserts both).
- Tests: `TestValidateAdvisesATicketWithABriefAndNoRecord` (`cmd/jig`: a hand-made
  folder, a minted ticket, a ticket with no brief), `TestValidateFixture` (`e2e`).

### 3. The reviewer does not edit the person's clone

- `skills/review/SKILL.md`: "Never edit the person's clone: you only read it.
  Run no tests ... A mutation check, if you make one, is done in `worktree` (the
  round's own checkout, the lease `jig review context` names) and put back there."
  The `Never` list says the same.
- `cmd/jig/review.go`: the help line of `jig review context` says the same ("never
  edit the clone ... make a mutation check only in the worktree, putting it back"),
  so the skill and the verb agree.
- Skill text only: no test beyond the byte budget.

### 4. The default branch with no title

- `internal/store/ticket.go` `defaultBranch`: the slug is the record's, else the
  title's, else the slug of the brief's first `# ` heading with a leading `<id>:`
  taken off (`briefSlug`: the ticket's id or one of its aliases, any case, then
  the colon). With none of them the branch is the bare id (`branchID`: the GitHub
  issue number once mirrored, else the store id). `defaultBranchOr` falls back to
  the bare id too. `jig/<id>` is no longer produced anywhere; `BranchLeadID` and
  `BranchLeadIssue` still strip it, so a `jig/<id>` branch binds as an alias.
- `internal/store/brief.go`: `BriefTitle(briefMD)`, the text of the first `# `
  line ("## " is a section, never the title).
- Tests: `TestBranchFromBriefHeadingWhenNoTitle` (8 cases: id and colon, no id,
  the id in another case, an alias, a record with no title, the title winning, the
  first heading winning, a section that is not a title) and
  `TestBranchIsTheBareIdWithNoTitleAtAll` (no folder, no heading, a heading that is
  only the id, a title with no ASCII word; and that `jig/<id>` still binds), both
  in `internal/store`. `TestTicketBranchOfATicketWithNothingToSayIsItsBareId`
  (renamed from `...KeepsItsOldName`, same subject, now expects the bare id and the
  issue number once mirrored) and the two default-branch expectations in
  `ticket_test.go`.

### 5. The reviewer is named "reviewer subagent" and by the model it ran on

- `internal/tui/people.go`: `reviewerName` (a session that played the planner or
  the builder is a "reviewer subagent", since a subagent shares its parent's id;
  else the numbered "reviewer"), `roundModel` (the latest model the round's own
  gate-reviewer, gate-round and gate-clean lines name), `reviewerDesc`
  (harness: the session's, else the status row's; model: the line's own, else the
  round's, else the status row's, else the one the session started with),
  `reviewerWho`, `reviewerRole`.
- Used by: the `say` of gate-reviewer, gate-round and gate-clean
  (`reviewer subagent (Claude Code · Opus 5) took it`), the review screen's round
  records (`reviewerWords` is gone, `reviewerWho` replaces it; the status row's
  model now beats the session's start model there, which the old
  `TestReviewScreenUsesTheLedgersHarnessAndModelForTheReviewer` asserted the other
  way), and the sub-steps block.
- `internal/phase` gains `Facts.ReviewerName func(id string, round int, detailed
  bool) string`, used where the answer names the session that took or closed a
  round (the round sub-step's detail, the review row's text, the "round is open"
  reason). `detailed` is true only for the sub-step's detail, which has room for
  "(Claude Code · Opus 5)"; the one-line step row says "reviewer subagent" alone,
  so it is not cut at 80 columns. Nil falls back to `SessionName`. `jig next` and
  `jig status` pass nothing, so their words are unchanged. The slice holder's
  words ("claimed by ...") still come from `SessionName`.
- Example, in the sub-steps block: `round 1 · taken by reviewer subagent (Claude
  Code · Opus 5)`; in the activity: `round 1 · reviewer subagent (Claude Code · Opus
  5) took it`.
- Tests: `TestAReviewerSubagentIsNamedAsOneAndByTheModelItRanOn` (a builder
  session's subagent, opus on the gate-reviewer line, the session started on
  Sonnet; sub-steps and activity; a closing line with no model takes the round's;
  a closing line with its own model takes that),
  `TestAReviewerThatBuiltNothingIsJustTheReviewer`, `TestReviewScreenNamesTheModelTheRoundRanOn`
  (renamed and rewritten, see Existing tests), and in `internal/phase`
  `TestReviewerNameWordsTheReviewerOfARound`.

### 6. The header reads naturally

- `internal/tui/steps.go`: `headerWords`, a table keyed by step and state with the
  whole phrase, and `stepWords`; `stepLabel` uses it. "publish publishing" is
  "publishing". The others: `brief being read | being drafted | needs approving |
  cannot be read`; `waiting to build | ready to build | build in progress | build
  verifying | build needs you | build stalled | build blocked by its environment`;
  `waiting to review | ready to review | review round open | review findings`;
  `waiting to publish | ready to publish | publishing | published`. A pair the
  table lacks reads "<step> <state>".
- Tests: `TestTheHeaderSaysEveryStepInEveryStateInWordsThatRead` (the table, and
  that no label says a word twice in two forms), `TestTheHeaderOfAPublishUnderWayReadsPublishing`.

### 7. A tall terminal fills

- `internal/tui/screens_ticket.go`: `substepCap(body)` and `activityCap(body)`:
  the 6 and the 8 of the 80x24 screen (`bodyRef` = 19 body rows), plus a third and
  two thirds of each body row above 19. `ticketPage` and `activityPanel` (so the
  build, review, brief and publish screens too) use them. At 80x24, and on a
  shorter screen, the caps are the old ones.
- Tests: `TestATallTerminalShowsMoreActivityAndSubstepsAndLeavesNoEmptyArea`
  (100x50, 12 slices, golden `ticket-tall.golden`; asserts more than eight activity
  lines, more than six sub-steps with no "+N more", and at most one empty row
  between the last panel and the keys), `TestTheScreenOf80By24IsAsItWasWhateverTheCapsScaleTo`,
  and `TestSubstepsListAtMostSixAndTheActivityKeepsThree` (the cap is the scaled
  one at height 40).

### 8. The "next" line wraps to two lines

- `ticketPage`: `wrap(Help[0], width-8, 2)`; the second line is indented under the
  text; the ellipsis comes only after the second line.
- Tests: `TestTheNextLineWrapsToTwoLinesAndIsCutOnlyAfterTheSecond`; goldens
  `ticket-substeps.golden` (the sentence now ends in "ask: Build SHOP-1", and the
  blank line before the activity went) and `ticket-asks.golden` (see Findings).

### 9. A screen of a quiet ticket reads its status again

- `internal/tui/model.go`: `Config.IdleAfter` (3s), `Config.IdleTick` (1s; negative
  never, for a test that drives the clock), `Config.Now`; the model keeps
  `lastEvent` (the last batch that touched the watched ticket) and `lastRead` (the
  last status read it started). A one-second tick sends `idleMsg`; `onIdle` reads
  the status of the watched ticket when both are at least three seconds old and no
  read is out. Only the ticket on the screen is read, never on the main screen.
- `internal/tui/activity.go` `noteChanges` (called from `onStatus`): when a status
  shows more sections in the brief, or more planned slices, than the one before,
  the ticket gets a line of the brief's activity that no journal wrote:
  `brief drafted · 6 sections written`, `slices written · 2 slices written`. They
  live in `ticket.seen` (source `status`, events `brief-seen` and `slices-seen`),
  are merged into `stepLines` by time, name the sub-steps `drafted` and `slices`,
  and are never part of the journal lines the phase derivation reads. The first
  status read is the baseline: what was there when the screen opened is not news.
- Tests (`internal/tui/idle_test.go`): `TestAQuietTicketScreenReadsItsStatusAgainAndSaysWhatAppeared`
  (a fake runner whose status changes with no event: the sub-steps fill in, the
  activity says what appeared, and a brief there at the start is not a line),
  `TestTheIdleReadIsRateLimitedAndOnlyForTheWatchedTicket` (every third second
  over nine; an event holds it off; the list reads nothing),
  `TestTheIdleReadWaitsForTheOneUnderWay`. The rigs pass `IdleTick: -1`, as they do
  `AwaitEvery: -1`: a rig settles every command it gets, and a tick that sends a
  tick never ends.

## The default branch moved under the tests

A ticket with no record and a brief with a heading now has `<id>-<heading slug>`
as its default branch, so the fixture ticket (`# Fixture ticket brief`) is
`JIG-1-fixture-ticket-brief`, not `jig/JIG-1`.

- `internal/fixture`: `Branch` (the constant), `Fixture.Branch`, and
  `RemoveBrief(storeDir, ticket)`, which keeps the ticket's branch when a test
  makes it brief-less (it first records the heading's slug in a `ticket.yaml`).
  `TestBranchIsTheDefaultOfTheTicket` pins the constant to `Store.TicketBranch`.
- Tests that named the default as `"jig/" + fx.Ticket` use `fx.Branch`
  (`ticketBranch` in verifydeliver's `testutil_test.go`, `ticketBranchOf` in
  cmd/jig's `built_test.go`, `publish_test.go`, `publish_picks_test.go`). Tests that
  only check out a branch named `jig/<id>` to bind a session are unchanged: it still
  binds.
- Comments that said the default is `jig/<ticket>` (verifydeliver `adopt.go`,
  `gate.go`, pool `pool.go`) say `<ticket>-<slug>` now.

## Decisions I took

1. **The brief's heading, not a record, gives a hand-made ticket its slug.** The
   brief says so; the consequence is in Findings.
2. **The bare id is the branch lead** (`branchID`): the issue number once the ticket
   is mirrored. A branch named `7` binds through `BranchLeadIssue`.
3. **"Reviewer subagent" is decided by the roles the session played on the ticket**,
   as the brief says, not by the agent id on the gate lines. A session that only
   reviews is "reviewer", numbered when several did.
4. **The status row's model beats the session start model** for a round on the
   review screen (decision 40, "the round carries a model"); the activity and the
   sub-steps do not read the status, only the journal lines for the round and the
   ledger, so a closed round's line there has a model when the gate lines carry one
   (they do: `Model` is on gate-reviewer, gate-round and gate-clean).
5. **Header wording is "ready to build" / "waiting to build", not "build ready" /
   "build waiting".** Only the stutter was a defect; these read as the phrase a person
   says.
6. **The idle tick is one second, the threshold three.** A tick of three would read
   up to six seconds after an event; this reads three to four.
7. **Wrapping the "next" line has priority over the sub-steps at 80x24.** See
   Findings.

## Existing tests changed

- `TestReviewScreenUsesTheLedgersHarnessAndModelForTheReviewer` is
  `TestReviewScreenNamesTheModelTheRoundRanOn`: same subject (which harness and
  model the review screen shows for a round's reviewer); it asserted the session
  start model beat the status row's, and now asserts the harness comes from the
  ledger and the model from the round's row.
- `TestTicketBranchOfATicketWithNothingToSayKeepsItsOldName` is
  `...IsItsBareId`; `TestTicketBranchDefault` and
  `TestResolveTicketBranchWorksOnTheRecordItIsGiven` expect the bare id.
- `TestSubstepsListAtMostSixAndTheActivityKeepsThree`: at height 40 the room is the
  scaled cap, so all nine sub-steps show and none is counted.
- Goldens: `ticket-substeps.golden`, `ticket-asks.golden` (the wrapped "next" line);
  new `ticket-tall.golden`.
- The rigs (`rig_test.go`, `waiting_test.go`) set `IdleTick: -1`.
- verifydeliver's `removeBrief`, two tests in `cmd/jig/gate_intent_e2e_test.go` and
  the two intent tests of `e2e` (`TestGatePointsTheReviewerAtTheIntentExcerpt`,
  `TestGateIntentFromTheTicketsSession`) call `fixture.RemoveBrief`.
- `e2e`: the builders' branch (`events`, `hooks`, `hooks_live`, `scaffold`, `tui`,
  `verify` and `gate_recorded` tests) is `fx.Branch`, not `"jig/" + fx.Ticket`;
  `TestNextReadySetsAndTickets` expects `fx.Branch` in `jig next`'s branch line and
  its `git switch -c`; `TestNoTrackerStoreIDBranch` asserts the fixture ticket's
  branch is `fx.Branch` and, with its brief removed, the bare `JIG-1` (it asserted
  `jig/JIG-1`).

Test functions per package, before and after (this branch's fork point): `cmd/jig`
372 to 374, `e2e` 165 to 165 (two assertions extended), `internal/tui` 118 to 128,
`internal/store` 219 to 221, `internal/phase` 16 to 17, `internal/fixture` 17 to 18,
`internal/setup`, `internal/verifydeliver` and `lint` unchanged. None deleted.

## Findings outside the brief (not fixed)

- **A hand-made ticket's branch follows its brief's heading.** With no record
  there is nothing to fix the slug, so editing the heading, or removing the brief,
  renames the branch the build was made on (the tests needed `fixture.RemoveBrief`
  for it). A ticket minted with `jig ticket new` records its slug and is not
  affected. Recording the slug the first time the branch is used (as
  `WriteTicketBranch` does for an adopted one) would close it.
- **At 80x24 the wrapped "next" line costs a row.** `ticket-asks.golden`, a
  review with two asks, now shows one sub-step and "+2 more" where it showed three:
  the page was full and the sentence ("... to decide the outstanding asks, then
  `jig gate JIG-2` to apply the decisions") is the more useful of the two.
- **The one-second tick redraws the screen once a second** while a program runs,
  though nothing changed. Cheap; a tick that only fires while a ticket is open would
  avoid it.
- **The Stop hook review note** (`reviewNoteText`) still says only that `jig gate`
  gives the Agent call and the prompt to pass; the foreground rule is in what `jig
  gate` prints, not in the note.
- **A reviewer's `jig next` before any round is open** still gets the advice to
  spawn a subagent (FR-1, decision 3); the prompt and the skill say it ignores it.

## Verification

Gates (from the worktree): `go build ./...`, `go vet ./...` (also `GOOS=linux`,
`GOOS=darwin`), `gofmt -l .`, `go test -count=1 ./lint ./cmd/jig ./internal/tui
./internal/store ./internal/setup ./internal/verifydeliver ./internal/phase`,
`go test -count=1 ./e2e -run 'TestTUI|TestBranchFromBriefHeadingWhenNoTitle|TestBranchIsTheBareIdWithNoTitleAtAll|TestReviewFromBuildSessionIsAdvisedToSpawn'`
(the two branch tests are in `internal/store`).

## ARCHITECTURE rows and text

- `internal/store/` row: the default branch of a ticket with no recorded branch is
  `<id>-<slug>`, the slug from the record, else the title, else the brief's first
  heading, and the bare id when none gives one; `jig/<id>` is retired as a name and
  kept as an alias when binding a branch. `BriefTitle` joins `BriefSections`.
- `internal/phase/` row: `Facts` gains `ReviewerName`, the words for the session of
  a round (the TUI says "reviewer subagent" for a session that also built).
- `internal/tui/` row: "at most six sub-steps" and "the activity keeps three lines"
  become "six on an 80x24 screen, more on a taller one"; the "next" line takes up to
  two lines; the ticket screen reads the status again when it has had no event for
  three seconds (one read at most every three seconds, for the ticket on the
  screen), and puts what that read shows of the brief (sections, slices) in the
  brief's activity; the header says a step and its state as a phrase.
- `internal/fixture/` row: `Branch`, `Fixture.Branch`, `RemoveBrief`.
- `jig validate` text: the advisories table also carries `TICKET_NOT_MINTED`.
- The skills text (`CONTEXT.md` or wherever the review phase is described): the
  reviewer subagent runs in the foreground and its parent waits for its verdict in
  the same turn; the reviewer never edits the person's clone.
