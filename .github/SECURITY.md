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

jig runs sessions against a real git checkout (a "lease": a pooled worktree
checked out for one phase's work). The `headless` backend is not a security
boundary: a granted session's shell and file reads run with the operator's
own user rights and are not confined to the lease, so run jig only against
code, and only on a machine, you would already hand that same shell and
file access. The screens below are an accident guard, not a sandbox: they
deny specific destructive or history-rewriting git subcommands and
credential-shaped paths for the `headless` backend's tool calls; they do
not confine a session's shell to its lease, and ordinary git commands
(commit, checkout, fetch, and the like) are still allowed. jig's own
settings grant a `headless` session's shell and file-read tools only
through a passing screen, and its file-edit tools only inside the lease and
on its own `result.json`; the operator's own user settings still load on
top of that (`--setting-sources user`) and can grant more, the same way
their deny rules can narrow it. jig checks that the screen still answers
before each screened session starts, because the CLI's own classifier would
otherwise leave a read-only shell behind a screen that had stopped working
- though that check binds only the start of a session, not its whole life.
The lease's own `.claude` settings are not loaded, since they are part of
the code under review; its `CLAUDE.md` is read from the lease's committed
tree, capped at 64 KiB (refusing the dispatch with `LEASE_MEMORY_TOO_LARGE`
rather than truncating it), rather than from its working tree. See the Safety
section of [ARCHITECTURE.md](../ARCHITECTURE.md#safety) for how each one
works, and [ADR 0008](../docs/adr/0008-headless-permission-model.md) for
why a denylist of path spellings cannot substitute for confinement.

In scope - what jig itself guarantees, and a report that breaks one of
these:

- jig itself reading or sending content from outside the lease through its
  own dispatch: the lease's `CLAUDE.md` is read from its HEAD tree through
  `internal/gitx` (`git ls-tree` then `cat-file`), never the working tree,
  and capped in size, so an uncommitted symlink or hard link left in the
  lease cannot substitute a file from outside it. Only committed content
  ever reaches the prompt - a committed hard link is ordinary blob content,
  carried like any other committed file.
- Anything else in the code under review that reaches the session or the
  machine through jig's own dispatch: a file in the lease that is loaded
  as configuration, a hook it can cause to run, or a way to widen the
  session's grants.
- A `headless` session that starts with its shell or file-read tools
  ungranted by the screen hook - for example, when the pre-dispatch check
  that must come back denied gets any other answer (`SCREEN_UNAVAILABLE`) -
  or whose file-edit tools write outside its lease and its own
  `result.json` through jig's generated settings rather than the
  operator's own Claude Code settings. That guarantee binds only the start
  of a session: a hook that dies mid-session is not caught, and is out of
  scope (see ADR 0008).
- A guarded-push bypass: a `git push` of the ticket branch that gets past
  `gitx.GuardedPush` (`internal/gitx/gitx.go`) without the `publish`
  command's confirmed step (its interactive confirm, or `--yes`) having
  run. (Every jig command pushes the ticket store's own bookkeeping
  commits to the store's remote with no confirm - that is normal
  behavior, not in scope here.)
- Anything that lets a session reach another ticket's worktree, the store's
  own git history, or the host machine itself, through jig's own machinery
  rather than through git or the OS directly.

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
- Vulnerabilities in Go itself, the `git` binary, or a session backend
  (`claude`, `herdr`) that jig shells out to - report those upstream.
- Anything that requires an attacker to already control the machine jig
  runs on, or to have write access to the ticket branch or store repo they
  are attacking.
- A `headless` session doing something unwanted with the shell or file
  tools it was actually granted: that shell is the operator's own, running
  with the operator's rights, not a sandboxed one. `herdr` sessions run
  with no screening at all yet; that is a known gap, not something to
  report here.
