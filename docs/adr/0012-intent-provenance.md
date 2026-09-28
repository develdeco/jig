# Intent has a provenance, and the gate no longer needs a brief

The gate reviewer used to be pointed at `brief.md` unconditionally: `Gate`
built `brief_path` from the ticket's own brief file, or, in `--branch`
mode, from `--doc` directly - the reviewer read that file itself, per
round - and the prompt told the reviewer to judge the diff "against the
brief". A `--branch` ticket adopted with no brief - a developer already
iterating by hand on a working branch, gating each round against a plain
statement of intent rather than an upfront brief - pointed the reviewer
at a file that was never there, and outside `--branch`, `--doc` was
ignored outright: there was no way to hand a brief-less ticket an intent
at all. `--doc`'s content was also copied, after a successful round, into
that round's own `gate/round-N/spec-input.md` - an archival record
nothing ever read back. Neither shape survives a ticket that starts from
a branch instead of a brief.

What every round actually needs is not a brief specifically but a
statement of intent: what was asked for, so the reviewer can judge a fix
by whether it changes that. A brief already carries that statement, but
it is not the only way to state it, and a review with nothing to compare
against - a ticket with neither - is a real, ordinary state, not an error.
So intent gets a small type of its own, `verifydeliver.Intent{Source,
Path}`, resolved once per round in `Gate`, before the round's own source
runs, by one fixed precedence: the ticket's `brief.md` (source `"brief"`),
else its `intent.md` and the source recorded there, else `"none"`.
`"brief"` and `"explicit"` are binding - the human's own statement of what
was asked for, the reviewer's fix definition turns on it. `"none"` is not
a missing value jig quietly falls back from; it is itself information -
nothing states what this change is for - and the reviewer is told exactly
that, in the same place it would otherwise be told what does. A later
source, `"inferred"` (a planned addition, not yet built: reading Claude
Code transcripts for a file-overlap match), is a hint rather than
binding: it can shape a fix judgment but a human never handed jig those
words, so it never overrides an ask the way a brief or an explicit
intent would.

A ticket has one intent, not one per round: `intent.md` is a file,
ticket-scoped like `brief.md` already is, and `jig gate --intent <text>` or
`--doc <path>` writes it - a small YAML front matter (`source: explicit`)
plus the text, replacing whatever was there before, never appending. A
ticket that already has a `brief.md` always resolves to it regardless of
what `intent.md` might say, so writing one there would record a
provenance jig would never actually read; both flags are refused with
`INTENT_CONFLICT` in that case, with help pointing at amending the brief
instead. This is the same shape review's own precedence already argues
for structurally (`writeExplicitIntent` checks brief.md's existence, not
its content, and refuses before writing intent.md) rather than a rule
enforced only by convention. `--doc`'s old behavior - copying a file's
content into a round-scoped `spec-input.md` - is retired outright:
`intent.md` is ticket-scoped like `brief.md` always was, and a round no
longer needs a spec-axis input distinct from its resolved intent.

`report.yaml` records the round's resolved source and the sha256 of the
exact bytes of the file at `intent.path` (empty for `"none"`), not the
text itself: the text already lives in `brief.md` or `intent.md`, both
already in the store's trust domain, and a hash is enough to prove a
round did resolve to a particular statement without duplicating it into
every round's own report. `review.json` carries the same `{source, path}`
pair the resolver produced, replacing `brief_path` outright - no dead
field survives beside it - and the reviewer reads the file at that path
itself, exactly as it read `brief_path` before. The prompt names what
each source means once, generically, rather than branching on which
source this round actually resolved to: it already reads `review.json`'s
own `intent` block, so the prompt's job is only to say what `"brief"`,
`"explicit"` and `"none"` each mean, not to restate this round's
particular value.

What that later addition brings: a fourth source, `"inferred"`, from a
summarizer session reading local Claude Code transcripts for a
file-overlap match with this round's own scope diff, dispatched only
when no intent is already recorded. It is a hint, not binding, and it
still goes through `intent.md` - the same file, a different source
value. `resolveIntent` accepts only the source values jig itself ever
records to `intent.md` - today, only `"explicit"` - and refuses anything
else with `INTENT_INVALID_SOURCE`, so this later addition is not free:
landing it means adding `"inferred"` to that accepted set, the one place
the precedence's own source check lives, not a change to the precedence
itself.

## Amendment: inference is built

`"inferred"` now exists, in `internal/intent` plus
`internal/verifydeliver/intent_infer.go`. It runs where the earlier text
above already said it would need to: the reviewer source
(`review.go`'s `Round`, calling `intent_infer.go`'s `inferIntent`), not
`Gate`, since only the reviewer source holds the backend and computes the
scope diff, and only when
`resolveIntent` came back `"none"` and the round is about to dispatch a
reviewer at all - nothing changed and nothing outstanding skip the round
the same as any other with no reviewer to point; no mapped clone does
not skip the round, only the inference attempt within it.

**Whose sessions count.** A session belongs to this repo when its own
recorded `cwd`'s git common dir (`internal/gitx.CommonDir`, the package
that already owns every git call) is the same directory as the common dir
of the operator's mapped clone (`Deps.Machine` reaches
`RoundInput.OperatorClone` through a new `verifydeliver.operatorClone`,
shared with `identityDir`'s existing lookup). Common dir, never a remote
URL: a linked worktree of the operator's clone shares it and counts, while
a jig lease clone of the very same remote gets its own and never does -
the identity check a remote-URL comparison could not make. "The same
directory" is `gitx.SameDir`, by file identity rather than by spelling:
`CommonDir` resolves symlinks and, on Windows, the drive letter's case,
but not a junction or a subst drive, so a mapped clone recorded through
one still matches its own worktrees and sessions. No mapped clone means nothing to
compare against, so inference stops there with that reason rather than
guessing; the same emptiness a `--scenario` round already gets, since the
scripted source never reaches this code. The sessions themselves are read
from the operator's own home directory (`RoundInput.UserHome`, resolved
once by `cmd/jig` and passed down like the jig home is), and with none
resolved inference says so instead of looking anywhere else.

**What counts as a match.** `internal/intent.Best` scores each candidate
session by the share of the scope diff's non-deleted files it mentions -
a tool call's own file path (relativized against the session's repo top
level, falling back to its cwd) or a structural scan of its text for
path-like tokens, matched to a diff file
by equality or a `/`-bounded suffix, with a bare-basename diff file
(nothing before the last slash - a repo-root file) matched only by an
equally bare mention, so a nested file sharing that basename never
stands in for it. A multi-file diff needs at least two overlapping files
and a score of at least 0.5; a session last active more than 24 hours
before head needs 0.8 instead; ties break toward the more recently
active session. One generic algorithm, advisory the same way the rest of
this file's own precedence is: it only ever picks which session a model
then summarizes, never judges the change or the session's own prose. Every
`*.jsonl` under a session's own `<session>/subagents/`, at any depth (a
workflow's own subagents nest one level deeper, under
`subagents/workflows/<wf-id>/`) is folded into its parent session rather
than treated as one of its own, since an implementer subagent does the
actual editing the matcher needs to see; the parent's mtime, not the
subagent's own, is what the discovery window filters on.

**The summarizer dispatch is the reviewer's own contract, once more.**
jig writes `work/intent.json` (excerpt path, diff files, agent, session
id), the backend runs a session, and jig reads back
`work/intent.result.json` as `{"summary": "..."}` - strictly parsed the
same way `result.json` is: an empty or malformed summary is a failed
inference, never a silently empty hint - and a summary over 4 KiB is
refused too, not truncated: the prompt asks for a few sentences, and one
far past that is the excerpt echoed back, which must not become a file in
the store just because it is valid JSON. The excerpt itself - the matched
session's user and assistant text, capped at `intent.MaxExcerptBytes`
(the omission marker and separators counted), with an over-long opening or
latest message cut and marked as such - is written under the jig home,
never the store: a transcript can hold secrets a store commit must never
carry, so only the model-written summary, not the excerpt, ever reaches
`intent.md`. The reader drops tool calls (only a call's file path reaches
the matcher, never its input values), tool results, thinking blocks, and a
user record that is not the developer's own. Which user records those are
is read from what the transcript itself records, never from a record's
text: a record is the developer's when its `origin` is the human's, or
when it has no origin at all (what older Claude Code versions write for
everything), and is not when it is marked as the harness's own (`isMeta`, a
compaction recap, a transcript-only notice) or attributed to someone else
(a background task's notification, a peer session's message, a workflow
coordinator's message). A record with no origin carries nothing the
reader relies on to tell a command echo, or local command output, from a
typed prompt, so what the excerpt then holds of it is left to the
summarizing model to judge, not filtered by a list of tags. The same
filter applies inside a subagent's own file: a prompt with no origin (the
parent agent's, kept and labelled as a subagent's prompt) stays, and a
message attributed to someone else or flagged by the harness is dropped
there as anywhere else. The prompt states the job
and the output contract and fences the excerpt as material to summarize,
never as instructions, the same discipline the reviewer prompt already
holds to.

**The summarizer does not run in the lease.** It has to read the excerpt
and write its result, and nothing else, so it is not run in the code under
review. Its session's working directory is a fresh, empty directory under
the jig home (`home.IntentScratchDir`: one per dispatch, made just before
it and removed after it, whatever the dispatch returned); the excerpt it
reads stays under the jig home as before. Under the headless backend the
session's edit grant is its working directory and the dispatch's own
`result.json`, so it covers nothing of the lease. The point is that the
lease is never the summarizer's working directory: nothing of the
summarizer's is in it, so there is nothing for jig to find and remove
before the reviewer looks. This is not confinement. The headless backend
is not a security boundary ([ADR 0008](0008-headless-permission-model.md)),
so a session's shell can still reach the lease, or anything else the
operator's account can, and what it leaves there the inference does not
chase: apart from the restore below, it never lists or removes the lease's
untracked or ignored paths itself. That restore is `resetLeasePristine`
(`git reset --hard`, then `git clean -fd`), the same one `Gate` and every
reviewer round use, so it does remove untracked paths. How it behaves with
a Windows junction - `git clean -fd` can descend through an untracked
junction a session planted in the lease and delete what the junction
points at - is a known gap of that shared restore, tracked separately from
this inference, which neither adds to it nor closes it.

The scratch directory is removed with `os.RemoveAll`. That is safe
whatever the session left in it, because `RemoveAll` removes a symlink or
junction itself and never follows it: a link inside the directory, at any
depth or standing in the directory's own place, is removed as the link it
is, and what it points at is never touched (tests pin that with junctions
on Windows and symlinks elsewhere). The safety is that property of the
removal, not the edit grant: a session's shell can plant a link there
whatever it was granted.

The dispatch sets `NoSessionPersistence`, which the headless backend turns
into `claude -p --no-session-persistence`. Without it Claude Code would
save the session's transcript, with the excerpt as the session read it,
under the operator's own `~/.claude/projects`, in a new directory named
after each dispatch's scratch directory, which nothing removes. The
excerpt then stays where jig put it, under the jig home. Only this
dispatch sets it; a reviewer's or a build session keeps its transcript.
The fake backend has no session to persist and herdr's agent takes no such
flag, so both ignore it.

The reviewer's own read-only rule is still checked around the dispatch.
After every summarizer dispatch, whatever it returned, jig compares the
lease's HEAD and tracked tree with the head the round captured. A change,
or a check that could not be made, puts the lease back with
`resetLeasePristine` - the recovery `Gate` and the reviewer's own round
already use - and fails the inference open: a summarizer that broke its
read-only rule has no summary worth trusting, and the reviewer that
follows is never blamed for a change that was not its own. A lease the
check confirms unchanged is left as it is. Like the reviewer's own guard,
the check counts tracked files only: an untracked or ignored file a
session put in the lease through its shell is not seen.

**Recording and reuse.** A successful inference writes `intent.md` with
`source: inferred` plus `agent`, `session` and `score` front matter
alongside the summary text - `resolveIntent` picks it up like any other
`intent.md`, so a later round never re-infers once one round has. The
round's `Review` carries the intent the reviewer was given and the exact
bytes of its file (what `inferIntent` recorded, or what Gate resolved
before the round), and Gate reports and hashes that - never a second read
of `brief.md` or `intent.md` after the round, which anything running during
it could have rewritten - so the very round that inferred it already
reports `"inferred"`, not `"none"`, and `review.json`, the report and
`report.yaml` all say what the reviewer was actually pointed at.

**Failing open.** Every step - no mapped clone, no transcripts, no
match, a dispatch failure, a malformed, empty or oversized summary, a
summarizer that changed the lease - leaves the round's intent at `"none"`,
with a one-line reason, and its cause when there is one, surfaced in the
gate report's own `intent` row (`GateReport.IntentNote`, sourced from the
reviewer source's own `Review.IntentNote`). The round then reviews the
same way a round with no intent always has, and what a failed attempt
leaves behind is stated, not nothing: `work/intent.json` (the request) in
the store, and the excerpt under the jig home when one was written. A
result the summarizer wrote is removed unless jig accepted it, so nothing
the summarizer produced but jig did not vouch for reaches the store. The
summarizer's own read-only rule is enforced by `inferIntent` checking the
lease's HEAD and tracked tree and restoring it when they changed (see
above), the same recovery the reviewer's own round already performs around
its dispatch, so a summarizer's stray edit to a tracked file, or a commit
it made, never reaches the reviewer and is never blamed on it. Inference
is advisory in the same sense the matcher is: a gate round must never fail
because a hint could not be produced - the one exception is that restore
itself failing, since a round cannot safely continue on a lease that might
still be dirty. A jig home in which the scratch directory cannot be made
is not dispatched over either: inference stops there, failing open with
that reason.

**Known limits.** The reader looks in `<home>/.claude/projects` only: an
operator who has relocated Claude Code's data (`CLAUDE_CONFIG_DIR`) gets
"no local agent sessions found", and supporting it belongs beside
`Deps.UserHome`, resolved once in `cmd/jig`, never read inside inference.
And discovery keeps a transcript only when its file was last written no
later than an hour after the head commit, so the authoring session is not
found when it is still being written to later - most directly, when the
author runs `jig gate` from it. Filtering on the session's first record
instead, with the file's mtime only for the lower bound, would keep it; a
follow-up.
