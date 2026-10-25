# DD-1: the detail screens show the records, through new status sections

For the DOCS-1 ticket. Nothing in README.md, ARCHITECTURE.md, CONTEXT.md,
DECISIONS.md or docs/adr was edited; this file says what they need. Decision 40.
Branch `ph/DD-1`.

## What is now true

- `jig status` carries the records the detail screens show: the slices table
  ends in the workspace, oracle and round of each slice, and three sections are
  new: `rounds`, `findings` and `publish`. They are useful on the command line
  too.
- The brief, build, review, publish and slice screens show those records. The
  TUI still reads no store (`internal/tui/invariant_test.go`): the records come
  from `jig status` and the brief's words from `jig brief show --text` (BS-1),
  both run through the Runner.
- No verb and no flag changed. No journal shape changed. Every status addition
  is a new key, column or block, and a reader that parses `jig status` by name
  (the TUI's does) is unaffected.

## `jig status`: the sections and their columns

### `slices` (cmd/jig/status.go)

```
slices[N]{id,state,attempts,blocked_by,question,goal,workspace,oracle,from_gate}:
```

The first six columns keep their order. `workspace` and `oracle` are what
`slices.yaml` names, `-` for none. `oracle` is the slice's `oracle` field as
written: a manifest oracle's name (`test`) or a literal command. `from_gate` is
the gate round a fix slice came from, `-` for any other slice.

### `rounds` (cmd/jig/status_rounds.go, Order 200)

```
rounds[N]{round,verdict,reviewer,harness,model,independent,fix,ask,note,scope,base,head,files}:
  1,findings,s-rev,claude,claude-opus-5,true,1,1,1,full,<40 hex>,<40 hex>,3
  2,clean,s-rev,claude,-,unknown,0,0,0,delta,<40 hex>,<40 hex>,1
```

One row per recorded gate round, oldest first. A cell not known is `-`.

| Column | Meaning |
|---|---|
| `round` | the round's number |
| `verdict` | `clean`, `findings` (any other verdict, fix-slices included) or `not-independent` |
| `reviewer` | the session that reported the round: report.yaml's `reviewer.session`, else the session of the round's gate-reviewer, gate-round or gate-clean line |
| `harness` | the harness a journal line of that session names (claim, note, ...); `-` when none does (the machine ledger, which knows it, is not reachable from a status section) |
| `model` | report.yaml's `model` (what the reviewer said it ran on), else the journal line's |
| `independent` | `true`, `false` or `unknown` for a round a reviewer ran; `-` for a scripted one |
| `fix`, `ask`, `note` | the findings the round raised (those whose id starts with `r<round>-`), by the reviewer's own action label |
| `scope` | `full` or `delta`, from findings.yaml |
| `base`, `head` | full shas. `head` is `reviewed_sha`, else `target_sha`; with several repos it is `repo:sha+repo:sha`. `base` is the `base_sha` of the round's `work/gate.round-N.review.json` while that file is there and names this head, else the previous round's head when the scope is a delta, else `-` |
| `files` | how many paths the reviewer read (`reviewed_paths`), `-` when the round has no findings.yaml |

A round whose report.yaml cannot be read is left out (`unreadable_gate_rounds`
already names it). A ticket with no round has no block.

### `findings` (cmd/jig/status_findings.go, Order 210)

```
findings[N]{id,round,action,risk,file:line,title,status,rationale,decision,decided_by}:
  r2-f1,2,ask,high,"a.go:5",Still unchecked,asked,why r2-f1 matters,dismiss,person
```

What the latest recorded round raised, high risk first: its findings.yaml
entries whose id starts with `r<round>-`. The entries for earlier asks whose
decision the round recorded (a clean round's file holds them) are not the
round's findings and are not listed; `outstanding_asks` lists the asks that wait.
`action` is the reviewer's label, `status` jig's (`open`, `asked`, `noted`,
`dismissed`), `rationale` the finding's `risk_rationale`. `decision` and
`decided_by` come from TR-1's `TriageDecision.Label` and are empty unless the
decision is pending. A latest round that raised nothing has no block.

### `publish` (cmd/jig/status_publish.go, Order 220)

```
publish:
  ticket: JIG-2
  pr: "https://example.test/acme/demo/pull/7"
  pr_state: opened
  head: <the head of the latest pr, publish-approved or pr-body line>
  branch: jig/JIG-2
  body_file: tickets/JIG-2/pr/demo.md
  body: "<first lines>"
  changelog_file: tickets/JIG-2/changelog/consolidated.md
  changelog: "<first lines>"
```

A key is there once the publish has recorded it. `body` and `changelog` are the
first eight non-blank lines of the file, each cut at 160 runes, joined by
newlines (axi quotes them, `\n` for a newline); the changelog leaves out the
file's `# ` title. `body_file` is the `Ref` of the latest `pr-body` line and is
read only when it lies inside the store (a journal line is shared by every
writer): a path that leaves the store reads as no body. A ticket with no
`pr-body`, `publish-approved` or `pr` line has no block.

## API added

`internal/verifydeliver/rounds.go`:

```go
func ReadRounds(st *store.Store, ticket string) ([]RoundRecord, error)
type RoundRecord struct { Round int; Verdict string; Reviewer Provenance; Model, Effort, Harness string;
    Independent Independence; Scope, Base, Head string; Files int; Findings []Finding; Fix, Ask, Note int; FixSlices []string }
const RoundClean, RoundFindings // VerdictNotIndependent already existed
```

## The screens

`internal/tui/statusinfo.go` parses the new columns and blocks by name:
`statusSlice` gains `Workspace`, `Oracle`, `FromGate`; `statusInfo` gains
`FixBudget` (the `fix_budget` line), `Rounds`, `Findings` and `Publish`. A status
that predates them reads as having none, and every screen still draws without
them (`build-plain.golden` is the build screen over such a status).

### Brief (screens_brief.go)

```
  ✓ approved by Ada Lovelace (agent) · 4 sections, 4 slices
    approved 2026-10-23 09:00 by Ada Lovelace
    recorded by the session that drafted it · brief 0123456789ab

  sections  cited by
▸ Goal      no slice
      Say hello and goodbye to a person by name, in the tone the product has.
  Greeting  slice a
      Use the first name. Fall back to "friend" when there is none.

  slices
  ● a  Greet by name  workspace app · oracle unit
  ○ d  Ship the copy  workspace copy · oracle go test ./... · after b, c
```

- The approval record: when, who, how it was recorded (`at a terminal`,
  `recorded by the session that drafted it`, `recorded by jig`) and the brief's
  sha prefix (the `Ref` of the latest `approval` line). A brief with an
  illustration says `jig brief show T` serves it.
- Each section's heading is a cursor stop (`rowStop`, opens nothing) with the
  slices that cite it, and under it the first line or two (wrapped to two lines)
  that `jig brief show T --text` prints in its `sections` table. The text is read
  once when the screen opens and kept in `ticket.brief`; a journal line of the
  brief step (`approval`, `revalidate`) or the `r` key marks it stale, and it is
  read again when the screen is next opened, at once if it is open. A read that
  fails or finds no brief says why under the header and leaves the headings.
- Each slice row ends in `workspace W · oracle O · after X` when it fits in the
  width, else the facts go on a dim line under it.

### Build (screens_build.go)

Per slice, under its row (`sliceRecords`): the facts as above, then the slice's
last three commits (`+N earlier commits` counts the rest), each from its
`verify-pending` line (sha, subject, and the time of its latest word) with one
line per oracle run against it (`` `go test ./...` passed in 15s · jig ran it ``),
`could not be verified: why` for a `verify-error`, then, when the slice's latest
oracle run failed, the end of the run's output (the oracle line's `Summary`,
wrapped to three lines) and a line saying where the rest is (the verify log on the
machine that ran it); a failed run with no `Summary` says that no output was
recorded and where to look. Then the slice's questions, an open one with a `?` and
an answered one with its answer. The builder sessions block stays, and
`fix budget 1 of 3 rounds used` (drawn forward when `, reached`) is under the step
line. "Waits for the answer to q-001" went: the question is listed under its
slice.

### Review (screens_review.go)

```
  ! round 1  findings  raised 1 fix · 2 asks · 1 note · 09:11:30
      reviewer (Claude Code · Opus 5) · independent · s-review
      full 0000000..ccccccc · 4 files read

  round 1 findings
  ◐ medium  alpha/a.go:12  to fix  Close the file  r1-f3
      a leaked handle fails the next open

  asks
▸ ! high  alpha/a.go:3  undecided  Unchecked error  r1-f1
      a failed write would pass silently · needs a human
```

- A round: its state and what it raised, who reviewed it (the harness and model
  the ledger gave the session, else the ones the status printed), whether they
  were independent (`NOT independent: it built what it reviewed` for a
  `not-independent` round, which reads `not clean`), the range and the paths read.
  An open round says who took it, or that it waits for a reviewer. The rounds come
  from the journal, as before, with the status's row joined on the round number
  once the round is closed.
- `round N findings`: what the latest round raised that is not an ask waiting for
  a person, with the reviewer's reason under each. The asks keep their own rows
  (decision before the title) and gain the reason, then the `why` jig routed
  them for.
- The sessions block went: the round names its reviewer, the harness and model,
  and the short session id.

### Publish (screens_publish.go)

Six checks, each with the time it happened (body, approval, branch pushed, pull
request, changelog, published). Under the body and the changelog, the first lines
the status printed of their files, an ellipsis ending the last when lines were cut.
The two texts take the rows the checks and the activity leave (`shareRows`: the
body three parts of five), so the page fits the window at 80x24 and above.

### Slice (screens_slice.go)

A dim line under the facts: `workspace W · oracle O`, and `fix slice of round N`.

## Heights and scrolling

- Every detail screen is exactly the terminal's height, the window moving over a
  page that may be taller. The window leaves a page at its top while the cursor is
  in the first screenful (`window` in view.go), centres on the cursor below that,
  and on the last row still shows what follows it.
- A page with no row to stand on (the publish screen, the review of a ticket with
  no round) is moved by `navEntry.scroll`: the arrow keys, page keys, home and end
  scroll it, and the publish screen's key hint says `↑↓ scroll`.
- `rowStop` is a cursor stop that opens nothing: the brief's section headings and
  the review's findings. They are what lets a person reach the top of a long page
  by moving up.
- Opening the review screen (from the step, or from an ask on the ticket screen)
  puts the cursor on that ask, or on the first, so `k` and `d` act on what the
  person came for.

## Decisions I took

1. **The oracle column is the oracle as `slices.yaml` names it**, not resolved
   against a manifest (the brief said "the command the manifest resolves, or the
   literal"). A status section is given the store and the ticket and nothing else:
   it has no clone to read a manifest from. The command that ran is on the screen
   anyway, from each `oracle` line.
2. **`harness` in `rounds` is what the journal knows.** The harness of a reviewer
   session is in the machine ledger (its session-start line), which is under the
   jig home, and a status section has no jig home. The column is filled from any
   journal line of that session that names a harness and is `-` otherwise; the
   TUI names the reviewer from its own ledger stream first. A section that is
   given the env would close this.
3. **Counts and findings are what the round raised**, by the id prefix
   (`r2-f1` is round 2's). A round's findings.yaml also holds the earlier asks
   whose decision it recorded (a clean round 2 lists `r1-f1` as dismissed); those
   are not the round's, and counting them made a clean round "raise" an ask. A
   finding a round reported again under its old id (a recurrence) is therefore not
   counted in the later round, and `outstanding_asks` or the fix slice shows it.
4. **`verdict` has three values**, `clean`, `findings` and `not-independent`. The
   brief said "findings or clean"; a round the builder reviewed is neither, and
   `findings` would have hidden it.
5. **The review screen has no sessions block.** The rounds name the reviewer with
   harness, model and short id; a second list repeated them. The build screen keeps
   its builder sessions (harness, model, start time).
6. **Findings that are asks waiting for a person are not repeated** in the
   `round N findings` block; the ask rows carry their reason.
7. **The publish screen has no body path.** The brief offered the first lines
   "or the body file path"; the lines are there, and the status carries
   `body_file` for a reader that wants the path.
8. **The window stays at the top while the cursor is in the first screenful.** The
   old rule centred the cursor from the start, which would have scrolled the
   rounds off the review screen the moment it opened over a long page.
9. **The brief text is read through `r` too**, so a person who edited the brief can
   see it without waiting for a line.

## Deviations from the brief

- The oracle column and the harness column, as decisions 1 and 2.
- `verdict` has `not-independent`, as decision 4.
- A scroll for pages with no row, `rowStop`, and the review's initial cursor are
  additions the brief's "Every screen fits 80x24 with the window scrolling" needed;
  none changes a key outside the detail screens, and the publish screen's hint line
  gained `↑↓ scroll`.

## Findings outside the brief (not fixed)

- `axi.Quote` writes a backslash as it is, so a pull request body or a changelog
  line with the two characters `\n` reads as a newline in the TUI (`axiUnquote`
  cannot tell it from the escape). BS-1 found the same for paths.
- The reviewer's harness is not reachable from a status section (decision 2).
  `statusSection.Blocks` could take the env, or the jig home.
- The oracle is not resolved against the manifest in status (decision 1).
- A finding reported again under its old id is not in the later round's
  findings or counts (decision 3). findings.yaml does not say which entries a
  round reported and which it carried; a field would.
- The commit of an `oracle` line the session ran itself (`source: session`) may
  name a commit that was never asked about with `verify-pending`; the build screen
  lists it as a commit with no subject.
- `jig status` now reads a ticket's journal and round files in several sections
  (phase, rounds, findings, publish, spend, triage). They could share one read.
- The Write and Edit tools of the implementing session refused paths in the
  ticket's worktree ("belongs to a different worktree"), and the shell here would
  not take a heredoc of about 150 lines or more in one command ("unexpected EOF"),
  so the files were written through the shell in chunks.

## Tests

Test functions, before (`a399b4c`) and after, none deleted:

| Package | Before | After | New |
|---|---|---|---|
| `cmd/jig` | 351 | 358 | `TestStatusSlicesNameTheirWorkspaceOracleAndRound`, `TestStatusRoundsListsEachRecordedRound`, `TestStatusRoundsNamesARoundNoReviewerRan`, `TestStatusFindingsListsTheLatestRoundsFindings`, `TestStatusFindingsDropsADecisionARoundHasTaken`, `TestStatusPublishReadsNoFileOutsideTheStore`, `TestStatusPublishNamesThePullRequestAndTheFirstLinesOfItsBody` |
| `internal/verifydeliver` | 363 | 367 | `TestReadRoundsJoinsTheReportTheFindingsAndTheJournal`, `TestReadRoundsTakesTheBaseFromTheRequestWhileItIsThere`, `TestReadRoundsNamesTheReviewerOfARoundWhoseReportDoesNot`, `TestReadRoundsSkipsWhatItCannotRead` |
| `internal/tui` | 91 | 113 | `records_test.go`, `records_review_test.go`, `records_publish_test.go` (wrap, shareRows, the brief text parse and its cache, the build records, the review rounds and findings, the publish fit, scrolling, the cursor on an ask), `TestBuildScreenWithoutRecords`, `TestParseStatusReadsTheRecordsOfTheSections`, `TestRoundsWithNoFindingsFileKeepTheirDash` |
| `e2e` | 146 | 147 | `TestTUIShowsTheRecordsOfTheSteps` (the real binary: the brief's text, the build's facts and commits, the round) |

(The counts are `^func Test` over the package's test files, on `ph/DD-1` before
and after its own commits and before the merge of `phases`, which brought other
tickets' tests.)

Goldens: `brief`, `build`, `review`, `review-published`, `publish`, `slice`,
`panel-ok` and `panel-fail` changed (the last two are the review screen under a
command's output); `build-plain` is new (a status without the new columns).
`main`, `ticket*`, `answer-prompt`, `tail` and `publish-confirm` are unchanged. Each
diff was read by eye: every screen is 24 lines at 80x24, and the publish and
`review-published` screens end in the activity panel's bottom edge. The e2e
goldens `status-a-green-b-blocked.txt` and `status-stalled.txt` gained the three
slice columns.

Existing tests whose expectation changed: `TestRenderStatus` (the slices header),
`TestParseStatusReadsTheTablesJigStatusPrints` (statusJIG2 carries goals, workspace
and oracle now). The fixtures in `rig_test.go` gained `briefTextJIG1`,
`jig1Records`, and the rounds, findings and publish blocks of `statusJIG2` and
`statusJIG2Done`.

## ARCHITECTURE text

TUI-1's `internal/tui/` row, after "...each line naming the sub-step it advances in
the block's words;" add:

> the detail screens show the records: the brief's sections with their first
> lines (read by the screen from `jig brief show --text` and kept until a line
> about the brief arrives), the slices' workspace and oracle, each slice's
> commits with every run of its oracle and the end of a failed run's output, each
> gate round with its reviewer, independence and range, what the latest round
> found with the reviewer's reason, and the pull request body and changelog's
> first lines, all from the tables and blocks `jig status` prints; a page longer
> than the window is moved by the cursor, and a page with no row to stand on by
> the arrow keys;

The sentence in the events section (line 134) that says "a session's id shows only
in the sessions block of the build and review screens": the review screen has no
sessions block now; a session's id shows in the build screen's sessions block and,
shortened, on a round's reviewer line.

The `internal/verifydeliver/` row's entry points gain `ReadRounds` (and
`RoundRecord`), and after "...an `ApproveReport`;" add:

> the gate rounds a ticket has recorded read back for display (`ReadRounds`: each
> round's verdict, reviewer, independence, reviewed range, paths read and the
> findings it raised, joined from report.yaml, findings.yaml, the round's request
> while it is in `work/`, and the journal's gate lines);

The `cmd/jig/` row's mention of the `statusSection` registry is unchanged. In the
store section that describes `jig status`, or wherever DOCS-1 lists its blocks: the
slices table's three added columns, and the `rounds`, `findings` and `publish`
blocks above. CONTEXT.md needs nothing: no new term.

## Verification

Gates, from `D:\DECO\repos\jig\.claude\worktrees\ph-DD-1`, before and after the
merge of `phases` (T-18, e6996b9): `go build ./...`, `go vet ./...` (also
`GOOS=linux` and `GOOS=darwin`), `gofmt -l .`, `go test -count=1 ./lint ./cmd/jig
./internal/tui ./internal/phase ./internal/verifydeliver`, and `go test -count=1
./e2e -run 'TestTUI|TestStatus'`.
