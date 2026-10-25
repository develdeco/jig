# IN-1: the gate's intent from the ticket's own sessions

For the DOCS-1 ticket. Nothing in README.md, ARCHITECTURE.md, CONTEXT.md,
DECISIONS.md or docs/adr was edited; this file says what they need.

## What is now true

When a round has no brief intent section and no explicit intent (source
`none`), `intentExcerpt` (internal/verifydeliver/intent_infer.go) first asks
the machine ledger which sessions worked the ticket, and reads only their
recorded transcripts. The search of every local transcript by file overlap
stays, as the fallback. ADR 0012's precedence and provenance are unchanged:
the excerpt is still a hint named by `intent_excerpt` in `review.json`, the
round's intent source is still `none`, and no `intent.md` is written.

## Verbs, flags, journal shapes

- No verb and no flag changed. `jig session list --json` shows one more key,
  `transcript`, when a session has one (`ledger.Session` gained it, omitempty).
- The store's journal is unchanged. The machine ledger gained one key on its
  session lines.

## The ledger field

- Key `transcript` on `session-start` and `session-touch` lines, the path of
  the harness's transcript of the session, as the hook reported it. It sits
  beside `clone` on the session line (`ledger.sessionLine`), not on
  `journal.Line`, so a `jig events` reader and every journal reader are
  unaffected.
- `ledger.Registration.Transcript` writes it; `ledger.Session.Transcript`
  (`json:"transcript,omitempty"`) is the fold. Like Harness, Cwd and Clone, a
  start or touch that names a path replaces the one the session had, and one
  that names none leaves it. A resumed session that writes to a file of its own
  therefore keeps only the latest path.
- Who writes it (internal/observe/handlers.go): `sessionStart`, and
  `noteMove`, the touch that first puts a session on a ticket. The second is
  how a session whose start jig never saw (hooks installed after it began, or
  it began outside a project) gets a path. Both read `Event.Transcript`, which
  the adapters already filled (`transcript_path` in the Claude and Codex
  shape, the same key for Cursor and Gemini, `transcriptPath` for Copilot,
  `--transcript` for the neutral adapter). The new code is only the ledger
  half; nothing in the adapters changed.
- A harness or event with no path records nothing (no key is written): the
  session id is the floor. An event from a subagent (`Event.Agent` set)
  contributes no path, since a subagent's hook may name a transcript of its
  own and that says nothing of what the session was asked.

## Lookup order

`intentExcerpt(in, head, diffFiles)`, only when `in.Intent.Source` is `none`:

1. `ticketSessionExcerpt`. Candidate session ids, in this order, each once
   (`ticketSessionIDs`):
   - the ticket's driver (`Store.Driver`, the claims log; a driver on another
     machine is not in this machine's ledger and is passed over);
   - the builders (`buildersOf`, the body of `Builders` made callable without a
     `Deps`): the sessions on the journal's `claim`, `verify-pending` and
     `oracle` lines, and on the ledger's `git-commit` lines and file-changing
     `tool` lines for the ticket or its branch.

   For each id, `ledger.Sessions` must hold the session with an absolute
   `Transcript`, and `intent.ReaderFor(session.Harness)` must name a reader
   (only `claude` today). `Reader.Load` reads that one file (and the subagent
   transcripts beside it). `intent.Among` picks the loaded session that
   mentions the most of the round's changed files; ties go to the more
   recently active, then to the earlier candidate, so the driver and the first
   claimer win a dead heat. There is no acceptance bar, no time window and no
   repository identity check: the records already vouch for the session. The
   excerpt is written as before (`IntentExcerptDir/<ticket>/<id>.md`, 0600,
   under the jig home), where `<id>` is the transcript file's name without
   `.jsonl`, which is path-safe, not the ledger's id.
2. `searchedExcerpt`, today's search unchanged (mapped clone, commit-anchored
   window, repo identity, `intent.Best`'s bar). Its reasons ("no mapped clone
   recorded for this repo", "no local agent sessions found for this repo and
   window", and the rest) are still the only reasons a round reports.

New public surface: `intent.Reader.Load`, `intent.ReaderFor`, `intent.Among`,
`verifydeliver.RoundInput.Branch` (set from the gate run's branch; used only to
match ledger lines that name the branch and not the ticket).

## The fallback

The lookup falls through to the search, silently, when the ledger yields no
readable session: no ledger, no candidate in it, a candidate with no recorded
transcript, a relative path, a transcript that is gone or is not a Claude Code
transcript (no `cwd` in it), a harness with no reader (a Codex path is
recorded and not read), or an excerpt that could not be written. Nothing
regresses where hooks never ran.

The existing tests that cover it: `TestGatePointsTheReviewerAtTheIntentExcerpt`
in e2e (its builder runs no hook, so the ledger has no transcript for it) and
the unit tests of `internal/verifydeliver/intent_infer_test.go` and
`intent_infer_e2e_test.go` (no ledger at all). New:
`TestIntentExcerptFallsBackToTheSearchWhenTheLedgerHasNothingToRead` (one case
per condition above) and
`TestIntentExcerptSaysWhyThereIsNoneWhenNeitherFindsOne`.

## Tests added

- e2e `TestGateIntentFromTheTicketsSession`: two sessions in the ledger, the
  builder's with a transcript outside every `.claude/projects` directory that
  states the intent, an unrelated one whose discoverable transcript mentions
  the round's files; the excerpt the reviewer is pointed at is the builder's.
  It fails when the ledger lookup is disabled (checked).
- internal/observe `TestClaudeSessionStartCarriesTheTranscriptPathIntoTheLedger`
  (the Claude adapter's `transcript_path`, through `Handle`, to the
  session-start line and the folded session), and tests of no path, the
  first touch, and a subagent's path.
- internal/ledger `TestSessionsFoldTheTranscriptPath`.
- internal/intent `Load`, `ReaderFor` and `Among` tests (`load_test.go`,
  `matcher_test.go`).
- internal/verifydeliver `intent_infer_ledger_test.go`: ledger beats search,
  only the ticket's builders are read (a reviewer's session is not), best match
  among several, the driver, the fallbacks.

Test functions before/after (touched packages): intent 33/40, ledger 24/25,
observe 54/58, verifydeliver 349/355, e2e 124/125. None deleted.

## Decisions I took

- Candidates are builders, not every session with a tool line. A tool line that
  changed no file is not counted, as for independence (`Builders`): a reviewer
  reads the code with the same tools, and its transcript mentions the diff's
  files heavily, so it would win a file-overlap ranking and be exactly the
  wrong source. The brief said "claim, verify or tool line"; this is that, with
  the one restriction.
- File overlap still orders the few candidate sessions, with no bar. The ADR
  verdict is that inference stops searching by overlap; picking among sessions
  the ledger already vouches for is not a search.
- Only the Claude harness has a reader. The adapter name `claude` is the ledger
  key (`ReaderFor`); Codex shares the payload shape but writes rollouts in
  another format.
- No change to the precedence, `resolveIntent`, `intent.md`, or the shape of
  `review.json` / `report.yaml` (`intent_excerpt`, `intent_note` as before).

## Not in scope, recorded

- "The session can state intent at claim" (the second half of the ADR 0012
  verdict in the chart) is not built here. Its provenance is undecided: a
  statement a session makes at claim is not a brief and not a person's
  `--intent` (`explicit`, binding), and whether it is a fourth source, a hint
  beside `inferred`, or nothing the reviewer treats as binding is open.
  `jig claim --intent` is untouched, and `resolveIntent` still accepts only
  `explicit` in `intent.md`.

## Text the docs need

ARCHITECTURE.md rows:

- internal/intent: "Reads the transcript behind a change. `Reader.Load` reads
  the one session a ledger record names; `Reader.Discover` and `Best` are the
  fallback search by repo, window and file overlap; `Among` orders the
  ledger-named sessions by overlap with no bar; `ReaderFor` maps a ledger
  harness name to its reader (claude only)."
- internal/ledger: "Session lines carry `clone` and `transcript` beside the
  journal line's keys; `Session.Transcript` is the latest path a start or touch
  named."
- internal/verifydeliver intent_infer.go: "The intent excerpt: the sessions
  the ledger names for the ticket (driver, then builders) read from their
  recorded transcripts, else the search of local transcripts."

ADR text to amend (DOCS-1's call):

- ADR 0032's "Where the build landed differently" bullet for 0012 says the
  ledger-based match is not built. It now is, with the file-overlap search as
  the fallback; only a session stating its intent at claim is still not built.
- ADR 0012's status line ("matched as before by `internal/intent`") reads
  "found through the machine ledger, else matched by file overlap".
- The verdict-map row for 0012 ("Inference stops searching transcripts by file
  overlap") holds for tickets whose sessions ran hooks.

## Findings outside the brief (not fixed)

- `ledger.Sessions` and `ledger.Read` (inside `buildersOf`) each read the whole
  ledger file, so a gate round with no stated intent now reads it twice. The
  ledger grows with every tool call of the machine; `ReadBackward` bounds a
  read, and a single pass that folds sessions and builders from the same lines
  would halve it.
- A resumed session keeps only its latest transcript path (see above), so the
  excerpt of a long, resumed session starts at the resume.
- `Builders` misses a session that only ever committed through a shell command
  and never claimed (design.md already notes it); such a session is no
  candidate here either.
- The Claude adapter's shape is shared with Codex, so a Codex session records
  a path that no reader opens. A Codex reader would plug into `ReaderFor`.

## Environment note

A PreToolUse hook of the launching session refuses the Edit and Write tools
for files in another worktree, so the files in this worktree were written from
the shell. Nothing outside `ph-IN-1` was touched.
