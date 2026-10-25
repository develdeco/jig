# FR-1: fresh reviewers, phase hand-offs in every skill, and automatic runs

Decisions 36 to 39: a session told "Review SHOP-1" knows whether it may review the
ticket itself; every phase skill starts and ends with the phase hand-off; a prompt
that names several phases runs them in order; an unattended session that stops with
the build done is told to open the gate. Branch `ph/FR-1`.

## What was built

- `internal/verifydeliver/freshness.go` (new): `Freshness(d Deps, ticket, session)
  (Fresh, error)`, the `Fresh{Known, Fresh bool; Why []string}` type, and the words
  for what makes a session not fresh.
- `cmd/jig/review_advice.go` (new): `sessionAdvice(e, deps, ticket)` reads the
  freshness of the calling session (`envSession`) and returns the state
  (`yes`, `no`, `unknown`) and the advice lines; `adviceFor`, `reviewerPrompt`,
  `whyWords` and `reviewNoteText` are the pure parts.
- `cmd/jig/gate.go`: the help of an opened round is `gateAdvice`. With a session it
  is the advice below; with none it is the generic two lines (have a fresh-context
  reviewer run the review skill; the builder must not review).
- `cmd/jig/next.go`: `jig next <T>` gains `fresh` and the advice when review is the
  phase that can start (`setReviewer`).
- `cmd/jig/observe_nextphase.go` (renamed from `observe_publish.go`): the note source
  is now `next-phase`, with the publish note it had and the new review note.
- `skills/{here,intake,build,review,publish}/SKILL.md` and `StubBody`
  (`internal/setup/project.go`).
- Tests: `internal/verifydeliver/freshness_test.go` (3), `cmd/jig/observe_nextphase_test.go`
  (+2), `e2e/fresh_review_test.go` (4: `TestReviewFromBuildSessionIsAdvisedToSpawn`,
  `TestSubagentReviewIsIndependent`,
  `TestMainAndSubagentBothRunningContextIsNotIndependent`,
  `TestUnattendedStopNamesReview`). One assertion was extended in
  `internal/setup/project_test.go` (the stub names the phases and the automatic
  run) and one changed, see Existing tests.

## The freshness rule

A session (`envSession`: `--session`/`JIG_SESSION`, else `CLAUDE_CODE_SESSION_ID`) is
**fresh for ticket T** when:

1. its id carries no journal line of T except the gate's (`gate-open`,
   `gate-reviewer`, `gate-round`, `gate-clean`), `triage` and the publish lines
   (`pr-body`, `publish-approved`, `pr`, `publish-done`, `publish-picks`, `changelog`,
   `memorize`), and the bookkeeping jig writes for a session (`spend`, `recorded`);
2. no ledger line of the session that names T changed a file (a tool line with
   `files`), made a commit (`git-commit`), ran `git commit` in a shell (a tool line
   whose command is `git ... commit`), or claimed (`claim`).

Every other journal event with the session's id on it (`claim`, `release`,
`verify-pending`, `oracle`, `verified`, `verify-error`, `approval`, `question`,
`answer`, `note`, and any event not listed above) makes it not fresh.

`Why` is in words, in journal order, each once: `claimed slice <s>`, `released slice
<s>`, `approved the brief`, `verified <sha7>`, `started the verification of <sha7>`,
`ran the oracle on <sha7>`, `hit an error verifying <sha7>`, `asked question <id>`,
`answered question <id>`, `left a note`, `wrote a <event> line`, and from the ledger
`changed <file>`, `made commit <sha7>`, `ran git commit`.

`Known` is false for an empty session id, and for a session with no journal line of T
in its name that the ledger has no line of either (a harness with no hooks, or
no `Home`). `Fresh` is true only for a known session with nothing of T's work in its
name. Subagents share the session's id (the commands they run carry no agent id), so
this is a fact about the whole session.

Read-only tool lines do not count (the same call as `Builders`): the review skill's own
first call is `jig next`, a tool line that names T in a repo on T's branch, and a
reviewer reads the code with the same tools. Counting them would make a session that
was fresh stop being fresh at its first call.

## The advice (text)

`jig next <T>` (review ready) and `jig gate <T>` (after it opens the round) print it as
help lines. In `jig next` the text adds `fresh: yes|no|unknown` to the `next:` block
(nothing without a session or when review is not ready, as with a round open), and the
JSON adds `"fresh": "yes|no|unknown"` and `"advice": [..]` (the key `advice` is the one
the ready-slices advice already used; with review ready there are no ready slices, so
it never carries both).

- **Fresh:** `Nothing in this session built T, so review it in this session: run the
  review skill (`jig skill review`)`
- **Not fresh:** `This session <why, up to three, then "(and N more)">: spawn a subagent
  to review T`, then:
  1. `In Claude Code, call the Agent tool (Task in older versions) with
     `subagent_type: general-purpose` and `model: opus` unless the person named another
     model, in the foreground, with no worktree isolation (the round has its own);
     another harness starts a fresh session instead. Pass it this prompt`
  2. `"You did not build T. In <clone>, run the review skill (`jig skill review`): `jig
     review context T --model <your model id>`, then `jig finding` for each problem,
     then `jig review done T`. You are the reviewer: do not spawn a subagent. Reply with
     the verdict line only."` (`<clone>` is the working directory of the command)
  3. `The main session must not run `jig review context` itself: jig sees a session and
     its subagents as one session, so if both read the round it cannot tell which of
     them closed it, and the round is not independent`
- **Unknown:** `jig cannot tell whether this session built T (it has no record of the
  session): spawn a subagent to review it`, then lines 1 to 3.
- **No session** (the TUI's `g`): `jig next` prints no freshness; `jig gate` prints the
  generic help (have a fresh-context reviewer run the `review` skill; the builder must
  not review).

The advice is advice only. `jig review done` still judges independence
(`JudgeIndependence`, `LedgerReviewerAgent`), unchanged.

## The skills

Each stays within 6000 bytes and 100 lines (`build` 5931, `intake` 5975; intake was
trimmed first, nothing dropped but wording and the `--json` hint of `jig recall`).

- **Every phase skill opens** with `Run jig next <T>. If the phase it names is not
  <its own>, tell the person what the ticket needs and the prompt that does it, and
  stop. Do not run an earlier phase on your own.` Intake has no earlier phase, so it
  says it only for "with a ticket" and drops the last sentence. Review adds "an open
  round is review", since a reviewer's `jig next` shows `ready: no` while the round
  they review is open.
- **Every phase skill closes** with `Relay jig next <T>'s prompt: to the person and
  stop, unless the person's prompt named later phases (an automatic run): then go on
  to the next phase's skill` (intake: build, review, publish; build: review, then
  publish; review: build for queued fixes, publish after a clean round; publish: none,
  it is the last). Under a `## Then` heading.
- **here:** a `## Phases and prompts` section: the four phases and their skills, what
  `phase:`, `ready:` and `prompt:` say, the automatic run, and the delegations.
- **review:** a fresh session reviews directly; one that is not (or unknown) spawns the
  advised subagent, passes the prompt, relays the verdict line, and never runs `jig
  review context`; the subagent reviews directly and ignores the advice to spawn one.
  A subagent stops at its verdict line.
- **build:** stops on any claim warning, tells the person what it says and the prompt
  that clears it, and releases the slice unless they say to go on. The unattended
  paragraph was wrong (an open question holds nothing; code: `internal/observe/stop.go`)
  and now lists what holds an unattended session: a verification, uncommitted tracked
  changes, the review note, the publish note.
- **intake:** a delegated approval ("go with your recommendations") is recorded with
  agent provenance, as `jig validate` already does off a terminal.
- **publish:** an automatic run publishes the harness way (`jig publish --body`, the
  session pushes, `gh pr create`, `jig publish --pr <url>`) when the prompt delegated it.
- **The automatic run** is one paragraph in `here` and one in the stub
  (`StubBody`): a prompt that names several phases runs them in order, a refusal ends
  the run; "go with your recommendations" delegates the brief's approval; a prompt that
  ends at the PR or says "and publish it" delegates the publish; a delegated approval is
  recorded with agent provenance. Skill text only, no code (decision 38).

## The unattended Stop note

`next-phase` (`cmd/jig/observe_nextphase.go`) replaces the `publish` source. At a Stop:

- the **publish note** is as it was (a clean round, a person's approval of the reviewed
  head, not published), for any session, enforced when unattended;
- the **review note** is new and only for an unattended session (`NoteCtx.Unattended`,
  from `unattended()`): `derivePhase` says review is the phase and it can start (every
  slice green, no round open, no ask waiting for a decision). Ref `review:<n>`, `n` the
  round to come, so one note per round. Text: `T is built, every slice green, and no
  review round is open: open the gate and review: run `jig gate T`, then ...` followed
  by who reviews: `review it in this session with the review skill` (fresh) or `have a
  subagent review it, not this session (<why>); `jig gate` gives the Agent call and the
  prompt to pass` (not fresh, unknown).

An attended session gets no review note and is never held. After a clean round nobody
approved, the note is gone (review is done); with a round open, review is not ready.

## Decisions I took

1. **Tool lines count only when they changed a file or committed.** The brief says "no
   ledger line (tool, git-commit or claim) names T". Taken literally, a fresh session's
   own `jig next T` (a tool line naming T) makes it not fresh before it reviews. A line
   with files, or whose command is `git commit`, is the building a reviewer does not do.
   A false positive on `git commit` (any `git` word followed by `commit`) makes a session
   look not fresh, the safe side.
2. **"Unknown" means the ledger has never heard of the session and the journal has no
   line of it.** A session with a ledger record and no work is fresh. A session id set by
   hand with no hooks, which then claims, is known (not fresh) through its journal
   lines.
3. **The advice shows when review can start, not when it is current.** With a round open
   the reviewer is at work, and the reviewer subagent's own first `jig next` then shows
   no spawn advice. If the subagent runs `jig next` before any round is open (its
   `review context` opens it), the advice would send it to spawn again; the prompt says
   "do not spawn a subagent" and the review skill says the subagent ignores it.
4. **Gate with no session keeps the generic help.** The brief says no freshness line;
   the two lines stay, reworded ("have a fresh-context reviewer ... run the `review`
   skill").
5. **The note source is renamed `next-phase`.** Its test checks the registered name.
6. **"to PR" delegates the publish.** The brief's example run, "Take SHOP-1 from brief
   to PR", ends at a pull request, so a prompt that ends there delegates the publish as
   "and publish it" does.
7. **Publish lines exempt from freshness include `changelog`, `memorize`, `spend` and
   `recorded`**, the bookkeeping publishing and the oracle run leave with a session id.

## Existing tests changed

- `TestDigesterFlow` (e2e): at step 10 an unattended session that stops with every slice
  verified and heard used to get silence; it now gets the block that tells it to open
  the gate and have a subagent review. The same session's later Stops are unchanged.
- `cmd/jig/observe_publish_test.go` is `observe_nextphase_test.go`; the check of the
  registered source names `next-phase`. The tests of the publish note are as they were.

Test functions per package, before and after (this branch's fork point): `cmd/jig` 341 to
343, `e2e` 133 to 137, `internal/verifydeliver` 356 to 359; `internal/observe`,
`internal/setup`, `internal/phase` and `lint` unchanged. None deleted.

## ARCHITECTURE rows and text

Module table: add to the `internal/verifydeliver/` row: `Freshness` (reads a session's
journal lines and ledger lines against a ticket: fresh, not fresh with why, or unknown).

Where the Stop hook and its notes are described: the `publish` source is now `next-phase`:
at a Stop it tells an unattended session whose ticket is built with no round open to open
the gate and review (naming a subagent when the session built it), and any session whose
ticket is reviewed clean and approved to publish to run `jig publish`.

Where `jig gate` and `jig next` are described: both tell the calling session (`envSession`)
whether it may review the ticket itself (fresh), or spawn a subagent (not fresh, or jig
cannot tell); `jig next <T>` adds `fresh` and `advice` when review can start.

## CONTEXT.md

**Fresh session**: a harness session that did none of a ticket's work: its id carries no
journal line of the ticket except the gate's, triage and publish lines, and no ledger line
of it changed a file or made a commit for the ticket. `jig gate` and `jig next <ticket>`
say whether the calling session is fresh; a fresh one reviews the ticket itself and one
that is not has a subagent do it (a subagent shares its session's id, so the main session
never reads the round as well). Advice only: `jig review done` judges independence from
who built, as before. _Avoid_: independent (that is the property of a recorded round).

**Automatic run**: a prompt that names several phases ("Take SHOP-1 from brief to PR"),
which runs them in order; the span is the prompt, not a record. A refusal ends it. The
person's moments (the brief's approval, the publish) stay theirs unless the prompt
delegates them.

## Findings outside the brief (not fixed)

- A subagent that runs `jig next T` before any round is open (its `jig review context`
  would open it) is shown the advice to spawn a subagent, because jig sees a session and
  its subagents as one session. The prompt and the review skill say to ignore it. A
  `--agent-id` on `jig next` or the harness telling jig the caller is a subagent would
  fix it.
- The MCP `next` tool has no `session` parameter (the CLI reads `envSession`), so over MCP the
  advice follows the session the server's environment names, and none is printed when it
  names none. A `session` parameter would fix it.

## Verification

Gates: `go build ./...`, `go vet ./...` (also `GOOS=linux`, `GOOS=darwin`), `gofmt -l .`,
`go test -count=1 ./lint ./cmd/jig ./internal/verifydeliver ./internal/observe
./internal/setup ./internal/phase`, and `go test -count=1 ./e2e -run
'TestReviewFromBuildSessionIsAdvisedToSpawn|TestSubagentReviewIsIndependent|TestMainAndSubagentBothRunningContextIsNotIndependent|TestUnattendedStopNamesReview'`;
the whole `./e2e` package also passed on the branch before the merge of `phases`, and the
digester flow and the four tests after it.
