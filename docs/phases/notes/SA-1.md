# SA-1: a commit that names no slice gets a correction note

Notes for the DOCS-1 ticket. Nothing in README.md, ARCHITECTURE.md, CONTEXT.md,
DECISIONS.md or docs/adr was edited; the text they need is at the end.

Digester chart, decision 7 and its fog note on slice inference: a commit on a
ticket's branch with no `Jig-Slice:` trailer (and no claim to stand for it) is
never verified, and until now nothing told the session. The fourth correction
tells it, once per commit, and names the slice the commit's files point to.
No new verb, no new flag, no schema bump. The note is an inference: it never
verifies a commit and never makes a slice green.

## The rule

`correct.Check` finds a **slice-missing** fact for the commit HEAD moved to
during the event (`Input.Commit`, as for the scope drift), when all hold:

- the session has claimed no slice of the ticket (`Input.Slice`, which is
  `Where.Slice`: the claim, `--slice` or `JIG_SLICE`). jig verifies a
  trailer-less commit for the claimed slice, so such a commit names one;
- the commit has no `Jig-Slice` trailer, or the trailer names a slice the ticket
  does not have (a typo is as unverified as no trailer);
- the commit is not a merge (more than one parent) and not a root commit, and
  it changed at least one file (`git commit --allow-empty`, the release commit,
  gets no note);
- at least one slice of the ticket builds in a workspace of this repo's
  manifest (`SliceWorkspaces`, the same placement the scope drift uses, so a
  slice written `<repo>/<workspace>` is read against the repo the commit is in).

The files the commit changed (`git diff --name-only --no-renames
--diff-filter=ADMT` against the first parent, as the scope drift reads them) are
matched against each slice's workspace path:

| Matches | The note names |
|---|---|
| exactly one slice | that slice, and its workspace |
| several slices | those slices, each with its workspace (the candidates) |
| none | no slice, and lists the workspaces of the ticket |

A file belongs to the slices whose workspace is the **deepest** that holds it
(`correct.Owners`): a slice that builds in the whole repo (`path: .`) does not
claim the files of a workspace inside it, and slices that share a workspace
share its files. A commit that also touches files outside every workspace is
judged by the files that are inside; the scope drift tells of the others.

Ref `slice:<sha12>`, kind (the `outcome` of the `correction` journal line)
`slice-missing`. The line has `commit` (full sha), `files` (the changed files,
first 20), `summary` (the note text), `provenance: jig`, the session, and no
`slice`: nothing is known of the slice, the note only guesses it.

## The note text

One line, as the other corrections. `<sha>` is the first 12 characters of the
commit, `<T>` the ticket, `<s>` a slice and `<w>` its workspace path. The
sentences, in order:

1. `commit <sha> on <T> names no slice, so jig verifies nothing for it.` For a
   trailer the ticket has no slice for, it reads instead
   ``commit <sha> on <T> carries `Jig-Slice: x`, but <T> has no slice x, so jig verifies nothing for it.``
2. What the files point to:
   - one slice: `It touched <w>/ (slice <s>'s workspace):`
   - several: `It touched the workspaces of slices a (alpha/), b (beta/):`
   - none: `It touched no slice's workspace (alpha/, beta/): if it is work for a slice,`
3. The remedy: ``amend it with `Jig-Slice: <s>`, or say which slice it is with `jig verify <T> <sha> --slice <slice>`.``
   (`Jig-Slice: <slice>` when there is not exactly one guess.)

For example, on SHOP-1 with slice clamp in `cart`:

```
commit 0123456789ab on SHOP-1 names no slice, so jig verifies nothing for it. It touched cart/ (slice clamp's workspace): amend it with `Jig-Slice: clamp`, or say which slice it is with `jig verify SHOP-1 0123456789ab --slice <slice>`.
```

"Say which slice it is" is `jig verify <T> <sha> --slice <s>` (it exists; `--wait`
runs the oracle in line): the person or the session decides, jig does not.

## Delivery and enforcement

It goes through the existing correction note path (`cmd/jig/observe_correct.go`,
unchanged but for its comment): `Enforce: true` like every correction, so an
attended session is told at the PostToolUse of the commit, once (the core
delivers a Ref once per session), and an unattended one (claim `--unattended`,
or the project default) is also refused once at Stop for it, with the other
corrections of that Stop.

Like the scope drift, the correction line is what offers the note again to the
session that made the commit at the events that follow (so a Stop can still hold
the session for it). Unlike the scope drift, the note **ends** when it has been
put right, so that an unattended session which did as told is not held back for
a commit it has replaced (`stillUnsliced`):

- the commit is no longer in the history of HEAD (amended with the trailer, or
  rebased away), or
- the journal has a `verify-pending`, `oracle` or `verified` line for that
  commit (`jig verify` was run for it).

Anything git cannot show (an unknown object, a different repo) counts as put
right. The amended commit has a trailer, so it is no new fact.

## Decisions I took

1. **A claimed slice stands for the commit.** `Check` skips the fact when
   `Input.Slice` is a slice of the ticket: that is how `sliceOf`
   (internal/observe/commit_slice.go) decides to verify a trailer-less commit.
   The two now agree except in one case (finding 1).
2. **Deepest workspace wins per file** instead of "any workspace that holds it",
   so a catch-all slice of the whole repo does not turn every commit into
   "several".
3. **A trailer naming no slice of the ticket is treated as no trailer**, and the
   note says so. Both leave the commit unverified.
4. **"None" gets a note too** (the brief: "several or none names the candidates or
   none"). A commit that touches no workspace at all is also a scope drift, so the
   session may hear two notes for one commit: one says it left the scope, the other
   says it will not be verified.
5. **Resolution of the note** is by git and the journal, not by a new record.
6. **`WorkspacePaths` is rebuilt on `SliceWorkspaces`** (new exported
   `correct.SliceWorkspace` and `correct.SliceWorkspaces`), the per-slice form of
   the same placement; its output is unchanged.

## Findings outside the brief (not fixed)

1. `sliceOf` also passes over a slice that builds in another repo of an
   orchestrator (`buildsHere`), so there the hook verifies nothing for a commit
   whose trailer or claim names it. The check here counts any slice of the ticket
   as naming the commit, so such a commit gets no note, though it is not
   verified. T-14's `jig verify` refuses it with a message; the hook is silent.
2. Only the commit HEAD moved to during the event is looked at, as for the scope
   drift: a tool call that makes two commits (`git commit && git commit`) is told
   of the last. The commits made outside the harness's tool calls (by the person,
   or by a script) are seen at the next tool call as one move, and the latest
   commit is the one judged.
3. A trailer-less commit made through a harness with no PostToolUse hook is not
   seen at all (the git post-commit hook starts the verification and says
   nothing).

## Tests

- `internal/correct/slice_test.go`: `TestOwnersAreTheSlicesOfTheDeepestWorkspaceThatHoldsAFile`
  (one, several, none, nested, shared, whole repo), `TestUnslicedNamesTheSlicesTheFilesPointTo`,
  `TestUnslicedPassesOverMergesEmptyAndRootCommits`,
  `TestSliceTextNamesTheSliceTheCandidatesOrNone`,
  `TestCheckTellsOnceOfACommitThatNamesNoSlice`, `TestCheckLeavesACommitThatNamesASlice`,
  `TestCheckStopsOfferingACommitOnceItIsPutRight`; `scope_test.go`:
  `TestSliceWorkspacesKeepEverySliceInOrder`.
- e2e (fake harness) in `e2e/correction_test.go`: `TestCommitWithoutSliceGetsANote`
  (the brief's: one slice, several, none; a trailer, a merge and an empty commit get none;
  nothing is verified and no slice moves) and
  `TestCommitWithoutSliceHoldsAnUnattendedSessionOnce`.
- Changed expectations, no test deleted: the commits of the older correction
  tests carry no trailer and the session claims no slice, so each now also
  leaves a slice-missing line. `corrLines` and `corrDelivered` leave those out
  (`corrSliceLines`, `corrDeliveredSlice` list them), `TestCorrectionStall` and
  `TestCorrectionScopeDrift` assert the hook of a trailer-less commit tells that
  it names no slice (`corrSliceOnly`) where they asserted silence, and
  `TestCheckScopeDriftIsOfferedAgainToTheSessionThatMadeIt` runs as a session
  that claimed slice a.

Test functions per package, before and after: `internal/correct` 25 to 33, `e2e` 146
to 148; `cmd/jig` (351) and `internal/observe` (58) unchanged. None deleted.

## ARCHITECTURE and CONTEXT text

ARCHITECTURE.md, line 142 ("Notes and corrections"): after the sentence on scope
drift, add:

> and a *missing slice*, a commit on the ticket's branch whose `Jig-Slice` trailer is absent or
> names no slice of the ticket, for a session that has claimed none; the note names the slice
> (or the candidates, or none) whose workspace holds the files it changed, and ends once the commit
> is amended away or verified. It is an inference: nothing is verified for the commit.

Module table row of `internal/correct/` (line 638): add `SliceGuess`, `Unsliced`,
`Owners`, `SliceWorkspace`, `SliceWorkspaces` to the symbols, and to the text after
"a commit outside every slice workspace": "a commit that names no slice, with the slices its files
point to".

CONTEXT.md, the *correction* entry (line 122): add "a commit that names no slice
(*slice missing*)" to the list of what is told.

## Verification

Gates, run in the worktree: `go build ./...`, `go vet ./...` (also `GOOS=linux` and
`GOOS=darwin`), `gofmt -l .` (nothing), `go test -count=1 ./lint ./cmd/jig ./internal/correct
./internal/observe`, and `go test -count=1 ./e2e -run 'TestCommitWithoutSliceGetsANote|TestCorrection'`.