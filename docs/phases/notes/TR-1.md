# TR-1: triage decisions show before the next round closes

For the DOCS-1 ticket. Nothing in README.md, ARCHITECTURE.md, CONTEXT.md,
DECISIONS.md or docs/adr was edited; this file says what they need. Decision 41.
Branch `ph/TR-1`.

## What is now true

After `jig triage <T> <id> keep|dismiss`, `jig status` lists the ask with its
decision and who made it, and the next round's request to the reviewer (and `jig
review context`) shows the same on the ask's `open` row. Before this, both showed
the ask as plainly open until the next round closed. Nothing is applied earlier:
the decision is still a journal line that the next round applies when it closes,
and an ask still leaves the open set only then (ADR 0007).

## Verbs, flags, journal shapes

- No verb and no flag changed. `jig triage` journals the same `triage` line as
  before and validates against the raw list of outstanding asks, so an ask that
  already has a pending decision can be decided again (the last line stands).
- The journal is unchanged. The `triage` line is `Ref` (finding id), `Outcome`
  (`keep` or `dismiss`), `Provenance` (`person` at a terminal, else `agent`),
  `Summary` (the reason), `Command` (`workspace=W oracle=O`, the build target the
  person chose).
- `review.json` (the request) is additive: an `open` entry gains `decision`,
  `decided_by` and `decision_reason`, each omitted when empty. An old reader
  that ignores unknown keys is unaffected.

## The one helper (`internal/verifydeliver/triage_decisions.go`)

```go
func TriageDecisions(lines []journal.Line) map[string]TriageDecision
func ReadTriageDecisions(st *store.Store, ticket string) (map[string]TriageDecision, error)

type TriageDecision struct {
    Ref, Outcome, Provenance, Reason, Workspace, Oracle string
    Pending bool
}
func (d TriageDecision) Human() bool
func (d TriageDecision) Decided() bool                  // pending, and keep or dismiss
func (d TriageDecision) KeepLacksTarget(f Finding) bool
func (d TriageDecision) Label(f Finding) string         // "keep", "dismiss", "keep (no build target)", "" if not pending
```

It is the journal read that `recordedTriage` used to do inline: the latest
`triage` line per finding id (a line with no `Ref` is skipped), now with the
fields decoded and with `Pending`. `recordedTriage` reads its decisions from it
and behaves as before: it takes the latest line per finding whatever round it
came before, with no round filter.

### The pending rule

A decision is **pending** when its triage line comes after the last `gate-round`
or `gate-clean` line of the journal (by position in the file), and **spent**
when it comes before it. A spent decision is one a round already took. It is
the same rule `internal/phase` applies (decision 41, PH-1's `readAsks`), and a
cmd/jig test (`askedPhaseCode`) checks that status and the phase agree on which
asks are decided.

### Recurring-ask caveat

A triage line is never marked used. If a later round raises an ask again (the
recurrence bound turns a fix into an ask for the third time, say), `recordedTriage`
silently applies the old line when that round closes, because it filters by no
round. `jig status` shows that ask as undecided, since the line is spent. This
was deliberately not changed here (the brief says not to add a round filter);
it is noted in `recordedTriage`'s doc comment.

## `jig status`

`outstanding_asks` columns are now:

```
outstanding_asks[N]{id,risk,file:line,title,why,decision,decided_by}:
```

- `decision` is `keep`, `dismiss`, `keep (no build target)` or empty. It is
  filled from **pending** decisions only; a spent decision leaves it empty and
  the ask counts as undecided.
- `decided_by` is `person` or `agent` (the triage line's provenance), empty with
  `decision`.
- `keep (no build target)` is a pending keep after which the round would still
  leave the ask asked (route.go: a kept ask whose workspace or oracle stays
  unresolved is dropped). The check is `BuildTargetGaps` without a manifest: the
  workspace is missing when neither the finding (jig derived it when it recorded
  the finding) nor the decision (`--workspace`) names one; the oracle is missing
  when neither the finding (resolved against the manifest when recorded) nor the
  decision (`--oracle`) names one. A keep that names the missing part is plain
  `keep`.
- Decided asks stay listed (the TUI builds its ask rows from this table and
  `e2e/tui_test.go` waits for "dismissed by you"). The TUI parses the table by
  column name, so the two new columns changed nothing there.

### The help line (`outstandingAsksHelpLine`)

It counts the undecided asks and says when the decided ones take effect. An ask
is undecided when it has no pending decision or its keep has no build target.
`T` is the ticket, "Run" or "Then run" is prepended as before:

- None decided:
  "`jig triage T <id> keep|dismiss` to decide the 2 undecided asks, then `jig gate T` to apply the decisions"
- Some decided: the line above, then "; the 1 decided ask is applied when the next round closes".
- A keep with no build target among the undecided: the line, then "; a keep with
  no build target needs --workspace or --oracle, or the round leaves it asked",
  before the decided clause.
- A budget-parked ask among the undecided: "`jig triage T <id> keep|dismiss` to
  keep or dismiss the parked finding(s), then `jig gate T` to apply the
  decisions: the fix budget is reached (1 of 3)" (the same text as before), with
  the same two clauses after it.
- Every ask decided:
  "`jig gate T` to apply the decisions: the 2 decided asks are applied when the next round closes"

When the frontier is not green the ticket's own next step comes first and this
line follows as "Then run ..." (unchanged order).

## The review request and `jig review context`

- `OpenFinding` (review.json `open` entry) gains `decision` (the same label as the
  status column), `decided_by` and `decision_reason` (the `--reason` text). They
  are set on an `asked` finding with a pending decision when the request is
  built (`PrepareReviewRequest`, through `toOpenFindingList`), so a launched
  round's request carries them too.
- `jig review context` prints the `open` table with a `decision` column:
  `open[N]{id,file:line,action,risk,title,decision}:`, the cell being
  `decided: dismiss, by a person: <reason>` (`by an agent`; the reason is
  omitted when there is none; `decided: keep (no build target), by a person`).
  `OpenFinding.DecisionText` builds it. `--json` prints the request as stored.
- The ask stays on the `open` row. `dismissed` and Known (`FoldBefore`'s lists)
  are untouched.

## Decisions I took

1. **Columns are always present** on `outstanding_asks`, not only when something
   is decided, so the table has one shape for a parser. The three existing tests
   that matched the old header literally were updated (no test deleted).
2. **A keep with no build target counts as undecided in the help**, since the
   round will not apply it. The row still shows the keep, flagged, so the person
   sees what they recorded and what is missing.
3. **The request is a snapshot at the round's opening.** A decision recorded
   while a round is open is shown by `jig status` at once (it is pending), and
   the round that is open applies it when it closes, but the open round's
   request does not change after it was written.
4. **`jig triage` is untouched**: its validation already read the raw list
   (`OutstandingAsks`), and it already accepts a second decision for an ask.
5. **The no-build-target check is the manifest-free one.** An oracle the reviewer
   named that the manifest has since lost is not seen by status, only by the
   round (route.go's `BuildTargetGaps` has the manifest).

## ARCHITECTURE rows

In the `internal/verifydeliver/` row's entry points add `TriageDecisions`, and
add after "...an `ApproveReport`;" the sentence:

> a `TriageDecision` per finding (the latest `jig triage` line, pending when it
> comes after the last gate-round or gate-clean line) that `recordedTriage`
> applies when a round closes and `jig status` and the review request show
> beforehand;

In the paragraph that says "the next round applies what was decided
(`recordedTriage`)" (the routing paragraph) add: "Until then `jig status`'s
`outstanding_asks` rows show the decision (`decision`, `decided_by`; `keep (no
build target)` for a keep the round will leave asked) and the next round's
request carries it on the ask's `open` row; the ask itself stays open."

For CONTEXT.md, under the triage entry: a decision is **pending** from the
`triage` line until the next round closes, and **spent** after.

## Findings outside the brief (not fixed)

- `internal/phase` counts a pending keep with no build target as decided
  (`PHASE_ASKS_UNDECIDED` does not fire, the phase says to open the next round),
  while `jig status`'s help counts it undecided. Making them agree needs
  `phase.Ask` to carry the finding's workspace and oracle; PH-1's `Derive` and
  `phaseFacts` would change.
- The pending rule exists twice: `phase.readAsks` (over its own lines) and
  `verifydeliver.TriageDecisions`. A pure fold in `internal/journal` could serve
  both; that moves PH-1's code.
- `jig triage` does not say, when it records a keep with no build target, that
  the round will leave the ask asked. The status row and help do, afterwards.
- A decision recorded while a round is open is not in that round's request (see
  decision 3 above).
- The Write and Edit tools of the implementing session refused paths in the
  ticket's worktree ("belongs to a different worktree"), so the files were
  written through the shell.

## Verification

Gates run from `D:\DECO\repos\jig\.claude\worktrees\ph-TR-1`: `go build ./...`,
`go vet ./...` (also `GOOS=linux` and `GOOS=darwin`), `gofmt -l .`, `go test
./lint ./cmd/jig ./internal/verifydeliver ./internal/phase`, plus
`./internal/tui` and `./internal/mcp`, and `go test ./e2e -run
'TestStatusShowsPendingTriage|TestReviewContextShowsDecision'`. The e2e
`TestTUIHandBuiltToPublish`, the gate and publish tests and the status goldens
also pass (`-run 'TestStatus|TestReviewContext|TestTUIHandBuilt|TestGate|
TestNextNames|TestHints|TestPublish'`).

Tests, by package, before and after (none deleted):

| Package | Before | After | New |
|---|---|---|---|
| `cmd/jig` | 341 | 345 | `TestStatusShowsAPendingDecisionOnItsAsk` (person, agent), `TestStatusHelpWithEveryAskDecided`, `TestStatusFlagsAKeepWithNoBuildTarget` (also the re-decision), `TestStatusDoesNotShowASpentDecisionAsPending` |
| `internal/verifydeliver` | 356 | 359 | `TestTriageDecisionsFoldsTheJournal`, `TestTriageDecisionLabel`, `TestOpenRowsCarryAPendingDecision` |
| `e2e` | 133 | 135 | `TestStatusShowsPendingTriage`, `TestReviewContextShowsDecision` |

Existing tests whose expected text changed with the new columns and the counted
help: `cmd/jig/status_outstanding_ask_test.go` (two help lines and the table),
`cmd/jig/status_fix_budget_test.go` (the table).
