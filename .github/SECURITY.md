# Security policy

## Reporting a vulnerability

Please do not open a public issue for a security vulnerability. Report it
privately instead: open this repository's Security tab and choose "Report a
vulnerability". The report reaches the maintainer directly and stays private
until a fix ships.

## Supported versions

jig is pre-1.0. Only the latest tagged release gets security fixes - update
before reporting, since the issue may already be fixed.

## Scope

jig attaches to the coding agent session you already run. It starts no coding
session and links none of the code that does (`lint/deps_test.go` pins that).
What it does on your machine is bounded: it runs `git` and `gh`, the oracle
commands of your own repo's manifest in a detached worktree of a commit, and
itself, detached (`jig verify --worker`); it edits your harness settings files,
memory files and git's global `core.hooksPath` when you ask it to; and it reads
your local agent transcripts for two things, a gate round's intent excerpt and a
session's spend. See the Safety section of
[ARCHITECTURE.md](../ARCHITECTURE.md#safety) for how each one works and
[ADR 0032](../docs/adr/0032-jig-attaches-to-the-harness.md) for why jig is shaped
this way.

In scope - what jig itself guarantees, and a report that breaks one of these:

- The installers (`jig setup`, `attach`, `hooks`, `githook`, `skills`, `hint`)
  writing what they were not asked to, or past a refusal. A settings file, a
  memory file or `core.hooksPath` is edited only after a yes (`--yes`, or a
  prompt at a terminal; with neither, `NEEDS_INPUT` and nothing is written);
  every key, hook and entry that is not jig's is kept in its place; a file jig
  cannot read (not JSON, comments, a wrong type) is refused and never written
  over; an uninstall removes only what jig recognizes as its own (a hook entry
  by its command, a skill by its stamp, a hint by its marked block, the OpenCode
  plugin by its first line); and a global git hook wrapper runs the repository's
  own hook and the earlier `core.hooksPath` before anything of jig's, with their
  exit code. Writing without consent, deleting or rewriting something of yours,
  or writing outside the scope's files is in scope.
- A hook breaking the harness, or recording what it should not. `jig observe`
  and `jig githook post-commit` exit 0 whatever they are handed and print
  nothing when they have nothing to say (a harness reads exit 2 from some hooks
  as a refusal), and record nothing about a directory that is in no jig project.
  The machine ledger they write stays under the jig home on your machine, never
  in a store, and holds the first 400 bytes of each command a session ran and the
  files it touched, for sessions in jig projects only.
- Verification running something other than the commit's own work. `jig verify`
  runs the slice's oracle in a fresh detached worktree of the commit, reads the
  manifest from that checkout and checks the tree clean first, so its record is
  evidence of exactly that commit; a run bound (`timeout:`) ends its process
  tree. An oracle that escapes the worktree through jig's own machinery, or a
  record that names a commit it did not run on, is in scope. The oracle command
  itself is the repo's, run with your rights: see below.
- A guarded-push bypass: a `git push` of the ticket branch that gets past
  `gitx.GuardedPush` (`internal/gitx/gitx.go`) without the `publish` command's
  confirmed step (`--yes`, a person's recorded approval of the head, or an
  interactive yes) having run, or `publish` pushing anything but the head the
  last clean round reviewed. (Every jig command pushes the ticket store's own
  bookkeeping commits to the store's remote with no confirm - that is normal
  behavior, not in scope here. `jig share` pushes only after its plan was
  confirmed.)
- Store history that does not name the person: a store commit made under an
  identity other than the one git resolves in the store clone, or a commit made
  without one (`IDENTITY_REQUIRED`), and a writer pushing main or rewriting
  another writer's branch in writers mode.
- A gate round's intent excerpt (`internal/intent`,
  `internal/verifydeliver/intent_infer.go`) leaking a local agent transcript's
  own text into the store or a pull request. A round with no brief and no
  explicit intent reads the Claude Code transcripts of the sessions the machine
  ledger names for the ticket (its driver and builders), or, when the ledger
  yields none, the operator's local transcripts for the repo, and writes an
  excerpt of the best match - the developer's and the assistant's
  own text; tool calls, tool results and thinking blocks are dropped, and so is a
  user record the transcript marks as the harness's own (`isMeta`, a compaction
  recap, a transcript-only notice) or as someone else's (any `origin` other than
  the human) - only under the jig home, never the store, since a transcript can
  hold secrets a store commit must never carry. A record with no origin, such as
  a slash-command echo or local command output, is kept and reaches the reviewer.
  The reviewer reads the excerpt; no model summarizes it. What reaches the store
  is the request's metadata (`work/gate.round-N.review.json`: which session
  matched and the diff files), with the excerpt's absolute path cleared (the
  open record keeps it relative to the jig home), and an inferred intent never
  reaches a pull request body. The spend reading takes only a transcript's
  usage numbers, message ids and model names. A reviewer's own words, shaped by
  having read the excerpt, can still reach the store (see out of scope).
- A recording or a pick that gets past what `gh ... --attach` and `internal/media`
  accept: a plain file name, an allowed type, a regular non-empty file within its
  size limit, hashed from the very file checked and checked again after the
  confirmation and before the push (`PUBLISH_PICKS_CHANGED`). A refused pick's
  reason in the journal's `publish-picks` line is jig's own words and names jig's
  own directories (the picks directory, the jig home, the store) by role, never a
  path of this machine. See
  [ADR 0029](../docs/adr/0029-demos-are-recordings-of-the-builds-end-to-end-scenarios.md).
- Anything that lets a session reach another ticket's worktree, the store's own
  git history, or the host machine itself, through jig's own machinery rather
  than through git or the OS directly.

The reviewer evaluation (`internal/revieweval`, opt-in: `JIG_REVIEWEVAL_BACKEND`;
not part of the `jig` binary) is the one place the source tree still starts a
model, as a `headless` session (`internal/session`). That backend is not a
security boundary: a granted session's shell and file reads run with the
operator's own user rights and are not confined to its worktree, so run the
evaluation only against code, and only on a machine, you would already hand that
same shell and file access. The screens are an accident guard, not a sandbox:
they deny specific destructive or history-rewriting git subcommands and
credential-shaped paths for the backend's tool calls; they do not confine a
session's shell, and ordinary git commands (commit, checkout, fetch, and the
like) are still allowed. jig's own settings grant such a session's shell and
file-read tools only through a passing screen, and its file-edit tools only
inside the worktree and on its own `result.json`; the operator's own user
settings still load on top of that (`--setting-sources user`) and can grant
more, the same way their deny rules can narrow it. The backend checks that the
screen still answers before each screened session starts (`SCREEN_UNAVAILABLE`),
though that check binds only the start of a session. The worktree's own
`.claude` settings are not loaded, since they are part of the code under review;
its `CLAUDE.md` is read from the committed tree, capped at 64 KiB
(`LEASE_MEMORY_TOO_LARGE`), rather than from the working tree. See
[ADR 0008](../docs/adr/0008-headless-permission-model.md) for why a denylist of
path spellings cannot substitute for confinement. Within that, in scope: a
headless session that starts with its shell or file-read tools ungranted by the
screen hook, or whose file-edit tools write outside its worktree or its own
`result.json`, through jig's generated settings rather than the operator's own
Claude Code settings.

Out of scope:

- A credential-shaped path that the secret screen
  (`internal/screen.SecretPath` and `internal/screen.ToolCall`) fails to
  deny through a shell glob or variable expansion, a junction or hard
  link, or a search root anchored one level above a credential directory.
  The screen is an accident guard on an unconfined shell, not a boundary a
  denylist of path spellings can complete - see
  [ADR 0008](../docs/adr/0008-headless-permission-model.md) for why. Real
  confinement (a container, a separate user, or a credential-free `HOME`)
  is future work, not a bug in the current design.
- A `git push` (or another banned subcommand) that the structural command
  screen (`internal/screen.Command`) fails to catch through an indirection
  such as a git alias (`-c alias.x=push`), a shell variable, or command
  substitution. The screen judges one token at a time before a shell ever
  interprets it, the same accident-guard shape as the credential-path gap
  above - see [ADR 0008](../docs/adr/0008-headless-permission-model.md).
- Vulnerabilities in Go itself, the `git` binary, `gh`, or the harness jig
  attaches to (Claude Code, Codex, Gemini CLI, Cursor, GitHub Copilot, OpenCode)
  - report those upstream.
- Anything that requires an attacker to already control the machine jig
  runs on, or to have write access to the ticket branch or store repo they
  are attacking.
- A harness session doing something unwanted with the shell or file tools it
  was granted: jig grants no tool to a session it did not start, and confines
  none.
- An oracle command doing something unwanted. It is the repo's own command, from
  the manifest at the commit, run with your rights, as a CI step would be on your
  machine: `jig verify` on a commit runs that commit's oracle, so do not verify
  a commit whose code you would not run.
- A session's own words recorded as it wrote them: a reviewer's finding text and
  summary, a question or a note, a pick's `summary`, its flow titles and its
  captions. jig does not filter or rewrite model prose in the journal or the
  store (it leaves jig's own directories out of what it renders into the pull
  request body), so a path the session was told and chose to repeat can reach the
  store, which is committed and often pushed. That is the session's text, not
  jig's.
- A hook file for a harness other than Claude Code that does not fire, or fires
  with another payload than the adapter reads. Those files and adapters are
  written from each vendor's documentation, not proven end to end (a bug to
  report, not a vulnerability).
